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
	settings        Settings
	policy          namespacePolicy
	requireApproval bool
}

type Handler struct {
	client       Client
	capabilities map[string]capabilityConfig
}

func NewHandler(client Client) *Handler {
	return &Handler{
		client:       client,
		capabilities: make(map[string]capabilityConfig),
	}
}

func (h *Handler) AddCapability(name string, settings Settings) {
	h.capabilities[name] = capabilityConfig{
		settings:        settings,
		policy:          newNamespacePolicy(settings.Namespaces),
		requireApproval: requiresApproval(name, settings),
	}
}

func (h *Handler) Handles(name string) bool {
	_, ok := h.capabilities[name]
	return ok
}

func (h *Handler) DispatchCall(ctx context.Context, call dispatcher.Call, auth dispatcher.Authorization) (dispatcher.Outcome, error) {
	cap, ok := h.capabilities[call.Name]
	if !ok {
		return dispatcher.Fail("unknown k8s call: " + call.Name), nil
	}

	switch call.Name {
	case "k8s.get":
		return h.dispatchGet(ctx, call, cap, auth)
	case "k8s.list":
		return h.dispatchList(ctx, call, cap, auth)
	case "k8s.apply":
		return h.dispatchApply(ctx, call, cap, auth)
	case "k8s.delete":
		return h.dispatchDelete(ctx, call, cap, auth)
	case "k8s.logs":
		return h.dispatchLogs(ctx, call, cap, auth)
	case "k8s.events":
		return h.dispatchEvents(ctx, call, cap, auth)
	default:
		return dispatcher.Fail("unsupported k8s operation: " + call.Name), nil
	}
}

func (h *Handler) dispatchGet(ctx context.Context, call dispatcher.Call, cap capabilityConfig, auth dispatcher.Authorization) (dispatcher.Outcome, error) {
	var req GetRequest
	if err := json.Unmarshal(call.Args, &req); err != nil {
		return dispatcher.Fail(fmt.Sprintf("decode k8s.get: %v", err)), nil
	}
	if err := checkApproval(auth, cap, fmt.Sprintf("k8s.get %s/%s %s", req.Kind, req.Name, req.Namespace)); err != nil {
		return *err, nil
	}
	obj, err := h.client.Get(ctx, req)
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
		return dispatcher.Fail(fmt.Sprintf("decode k8s.list: %v", err)), nil
	}
	if err := checkApproval(auth, cap, fmt.Sprintf("k8s.list %s/%s", req.Kind, req.Namespace)); err != nil {
		return *err, nil
	}
	list, err := h.client.List(ctx, req)
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
		return dispatcher.Fail(fmt.Sprintf("decode k8s.apply: %v", err)), nil
	}
	var meta struct {
		Kind     string `json:"kind"`
		Metadata struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"metadata"`
	}
	_ = json.Unmarshal(req.Resource, &meta)
	if err := cap.policy.check(meta.Metadata.Namespace, true); err != nil {
		return dispatcher.Fail(err.Error()), nil
	}
	summary := fmt.Sprintf("k8s.apply %s/%s", meta.Kind, meta.Metadata.Name)
	if meta.Metadata.Namespace != "" {
		summary += " in " + meta.Metadata.Namespace
	}
	if err := checkApproval(auth, cap, summary); err != nil {
		return *err, nil
	}
	obj, action, err := h.client.Apply(ctx, req)
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
		return dispatcher.Fail(fmt.Sprintf("decode k8s.delete: %v", err)), nil
	}
	if err := cap.policy.check(req.Namespace, true); err != nil {
		return dispatcher.Fail(err.Error()), nil
	}
	summary := fmt.Sprintf("k8s.delete %s/%s/%s", req.Kind, req.Namespace, req.Name)
	if err := checkApproval(auth, cap, summary); err != nil {
		return *err, nil
	}
	if err := h.client.Delete(ctx, req); err != nil {
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
		return dispatcher.Fail(fmt.Sprintf("decode k8s.logs: %v", err)), nil
	}
	if err := cap.policy.check(req.Namespace, true); err != nil {
		return dispatcher.Fail(err.Error()), nil
	}
	if err := checkApproval(auth, cap, fmt.Sprintf("k8s.logs %s/%s", req.Namespace, req.Name)); err != nil {
		return *err, nil
	}
	logs, err := h.client.Logs(ctx, req)
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
		return dispatcher.Fail(fmt.Sprintf("decode k8s.events: %v", err)), nil
	}
	if err := checkApproval(auth, cap, fmt.Sprintf("k8s.events %s", req.Namespace)); err != nil {
		return *err, nil
	}
	list, err := h.client.Events(ctx, req)
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

func checkApproval(auth dispatcher.Authorization, cap capabilityConfig, summary string) *dispatcher.Outcome {
	if !cap.requireApproval {
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
