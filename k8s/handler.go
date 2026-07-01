package k8s

import (
	"github.com/aurora-capcompute/aurora-dispatchers/builtin"
	"github.com/aurora-capcompute/capcompute/dispatcher"
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

var _ builtin.Handler = (*Handler)(nil)

type capabilityConfig struct {
	client          Client
	policy          permissionPolicy
	requireApproval *bool
}

// Handler dispatches Kubernetes tool calls. Each tool is keyed by its local
// manifest name; a single call carries a `verb` discriminator that selects the
// operation, gated by the tool's permission allowlist.
type Handler struct {
	capabilities map[string]capabilityConfig
}

func NewHandler() *Handler {
	return &Handler{capabilities: make(map[string]capabilityConfig)}
}

func (h *Handler) AddCapability(name string, settings Settings) {
	client, err := NewClient(settings.Kubeconfig, settings.Context)
	if err != nil {
		client = &failedClient{err: err}
	}
	h.capabilities[name] = capabilityConfig{
		client:          client,
		policy:          newPermissionPolicy(settings.Permissions),
		requireApproval: settings.RequireApproval,
	}
}

func (h *Handler) Handles(name string) bool {
	_, ok := h.capabilities[name]
	return ok
}

func (h *Handler) DispatchCall(ctx context.Context, call dispatcher.Call, auth dispatcher.Authorization) (dispatcher.Outcome, error) {
	cap, ok := h.capabilities[call.Name]
	if !ok {
		return dispatcher.Fail("unknown k8s tool: " + call.Name), nil
	}
	var disc struct {
		Verb string `json:"verb"`
	}
	if err := json.Unmarshal(call.Args, &disc); err != nil {
		return dispatcher.Fail(fmt.Sprintf("decode verb: %v", err)), nil
	}
	verb := strings.ToLower(strings.TrimSpace(disc.Verb))
	switch verb {
	case "get":
		return h.dispatchGet(ctx, call, cap, auth)
	case "list":
		return h.dispatchList(ctx, call, cap, auth)
	case "apply":
		return h.dispatchApply(ctx, call, cap, auth)
	case "delete":
		return h.dispatchDelete(ctx, call, cap, auth)
	case "logs":
		return h.dispatchLogs(ctx, call, cap, auth)
	case "events":
		return h.dispatchEvents(ctx, call, cap, auth)
	default:
		return dispatcher.Fail("unsupported verb: " + disc.Verb), nil
	}
}

// permit checks the permission allowlist for an operation; it returns a Fail
// outcome when the operation is not granted, otherwise nil.
func permit(cap capabilityConfig, verb, kind, namespace string) *dispatcher.Outcome {
	if cap.policy.allows(verb, kind, namespace) {
		return nil
	}
	out := dispatcher.Fail(fmt.Sprintf("not permitted: %s %s in namespace %q", verb, kind, namespace))
	return &out
}

func (h *Handler) dispatchGet(ctx context.Context, call dispatcher.Call, cap capabilityConfig, auth dispatcher.Authorization) (dispatcher.Outcome, error) {
	var req GetRequest
	if err := json.Unmarshal(call.Args, &req); err != nil {
		return dispatcher.Fail(fmt.Sprintf("decode k8s get: %v", err)), nil
	}
	if denied := permit(cap, "get", req.Kind, req.Namespace); denied != nil {
		return *denied, nil
	}
	if err := checkApproval(auth, cap, "get", fmt.Sprintf("get %s/%s %s", req.Kind, req.Name, req.Namespace)); err != nil {
		return *err, nil
	}
	obj, err := cap.client.Get(ctx, req)
	if err != nil {
		if ctx.Err() != nil {
			return dispatcher.Outcome{}, ctx.Err()
		}
		return dispatcher.Fail(err.Error()), nil
	}
	return marshalResult(GetResponse{Resource: mustJSON(obj)})
}

func (h *Handler) dispatchList(ctx context.Context, call dispatcher.Call, cap capabilityConfig, auth dispatcher.Authorization) (dispatcher.Outcome, error) {
	var req ListRequest
	if err := json.Unmarshal(call.Args, &req); err != nil {
		return dispatcher.Fail(fmt.Sprintf("decode k8s list: %v", err)), nil
	}
	if denied := permit(cap, "list", req.Kind, req.Namespace); denied != nil {
		return *denied, nil
	}
	if err := checkApproval(auth, cap, "list", fmt.Sprintf("list %s/%s", req.Kind, req.Namespace)); err != nil {
		return *err, nil
	}
	list, err := cap.client.List(ctx, req)
	if err != nil {
		if ctx.Err() != nil {
			return dispatcher.Outcome{}, ctx.Err()
		}
		return dispatcher.Fail(err.Error()), nil
	}
	items := make([]json.RawMessage, 0, len(list.Items))
	for _, item := range list.Items {
		items = append(items, mustJSON(&item))
	}
	return marshalResult(ListResponse{Items: items, Count: len(items)})
}

func (h *Handler) dispatchApply(ctx context.Context, call dispatcher.Call, cap capabilityConfig, auth dispatcher.Authorization) (dispatcher.Outcome, error) {
	var req ApplyRequest
	if err := json.Unmarshal(call.Args, &req); err != nil {
		return dispatcher.Fail(fmt.Sprintf("decode k8s apply: %v", err)), nil
	}
	var meta struct {
		Kind     string `json:"kind"`
		Metadata struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"metadata"`
	}
	_ = json.Unmarshal(req.Resource, &meta)
	if denied := permit(cap, "apply", meta.Kind, meta.Metadata.Namespace); denied != nil {
		return *denied, nil
	}
	summary := fmt.Sprintf("apply %s/%s", meta.Kind, meta.Metadata.Name)
	if meta.Metadata.Namespace != "" {
		summary += " in " + meta.Metadata.Namespace
	}
	if err := checkApproval(auth, cap, "apply", summary); err != nil {
		return *err, nil
	}
	obj, action, err := cap.client.Apply(ctx, req)
	if err != nil {
		if ctx.Err() != nil {
			return dispatcher.Outcome{}, ctx.Err()
		}
		return dispatcher.Fail(err.Error()), nil
	}
	return marshalResult(ApplyResponse{Resource: mustJSON(obj), Action: action})
}

func (h *Handler) dispatchDelete(ctx context.Context, call dispatcher.Call, cap capabilityConfig, auth dispatcher.Authorization) (dispatcher.Outcome, error) {
	var req DeleteRequest
	if err := json.Unmarshal(call.Args, &req); err != nil {
		return dispatcher.Fail(fmt.Sprintf("decode k8s delete: %v", err)), nil
	}
	if denied := permit(cap, "delete", req.Kind, req.Namespace); denied != nil {
		return *denied, nil
	}
	summary := fmt.Sprintf("delete %s/%s/%s", req.Kind, req.Namespace, req.Name)
	if err := checkApproval(auth, cap, "delete", summary); err != nil {
		return *err, nil
	}
	if err := cap.client.Delete(ctx, req); err != nil {
		if ctx.Err() != nil {
			return dispatcher.Outcome{}, ctx.Err()
		}
		return dispatcher.Fail(err.Error()), nil
	}
	return marshalResult(DeleteResponse{Deleted: true, Name: req.Name})
}

func (h *Handler) dispatchLogs(ctx context.Context, call dispatcher.Call, cap capabilityConfig, auth dispatcher.Authorization) (dispatcher.Outcome, error) {
	var req LogsRequest
	if err := json.Unmarshal(call.Args, &req); err != nil {
		return dispatcher.Fail(fmt.Sprintf("decode k8s logs: %v", err)), nil
	}
	if denied := permit(cap, "logs", "Pod", req.Namespace); denied != nil {
		return *denied, nil
	}
	if err := checkApproval(auth, cap, "logs", fmt.Sprintf("logs %s/%s", req.Namespace, req.Name)); err != nil {
		return *err, nil
	}
	logs, err := cap.client.Logs(ctx, req)
	if err != nil {
		if ctx.Err() != nil {
			return dispatcher.Outcome{}, ctx.Err()
		}
		return dispatcher.Fail(err.Error()), nil
	}
	return marshalResult(LogsResponse{Logs: logs})
}

func (h *Handler) dispatchEvents(ctx context.Context, call dispatcher.Call, cap capabilityConfig, auth dispatcher.Authorization) (dispatcher.Outcome, error) {
	var req EventsRequest
	if err := json.Unmarshal(call.Args, &req); err != nil {
		return dispatcher.Fail(fmt.Sprintf("decode k8s events: %v", err)), nil
	}
	if denied := permit(cap, "events", "Event", req.Namespace); denied != nil {
		return *denied, nil
	}
	if err := checkApproval(auth, cap, "events", fmt.Sprintf("events %s", req.Namespace)); err != nil {
		return *err, nil
	}
	list, err := cap.client.Events(ctx, req)
	if err != nil {
		if ctx.Err() != nil {
			return dispatcher.Outcome{}, ctx.Err()
		}
		return dispatcher.Fail(err.Error()), nil
	}
	items := make([]json.RawMessage, 0, len(list.Items))
	for _, item := range list.Items {
		items = append(items, mustJSON(&item))
	}
	return marshalResult(EventsResponse{Items: items, Count: len(items)})
}

func checkApproval(auth dispatcher.Authorization, cap capabilityConfig, verb, summary string) *dispatcher.Outcome {
	if !requiresApproval(verb, cap.requireApproval) {
		return nil
	}
	if auth.Decision == dispatcher.Approved {
		return nil
	}
	outcome := dispatcher.Yield(fmt.Sprintf("Approve: %s", strings.TrimSpace(summary)))
	return &outcome
}

func marshalResult(value any) (dispatcher.Outcome, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return dispatcher.Outcome{}, err
	}
	return dispatcher.Result(raw), nil
}

func mustJSON(obj any) json.RawMessage {
	raw, _ := json.Marshal(obj)
	return raw
}
