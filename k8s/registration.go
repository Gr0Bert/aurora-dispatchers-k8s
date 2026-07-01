package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aurora-capcompute/aurora-dispatchers/builtin"
	"github.com/aurora-capcompute/aurora-dispatchers/registry"
	"github.com/aurora-capcompute/capcompute/dispatcher"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// ToolType is the manifest `type` for a Kubernetes tool.
const ToolType = "core.k8s"

type Registration struct{}

func (Registration) Matches(toolType string) bool { return toolType == ToolType }

func (Registration) Normalize(_ string, raw json.RawMessage) (json.RawMessage, error) {
	var settings Settings
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &settings); err != nil {
			return nil, err
		}
	}
	settings.Kubeconfig = strings.TrimSpace(settings.Kubeconfig)
	settings.Context = strings.TrimSpace(settings.Context)
	if len(settings.Permissions) == 0 {
		return nil, fmt.Errorf("permissions must contain at least one {resource, verb, namespace}")
	}
	for i := range settings.Permissions {
		settings.Permissions[i].Resource = strings.TrimSpace(settings.Permissions[i].Resource)
		settings.Permissions[i].Verb = strings.ToLower(strings.TrimSpace(settings.Permissions[i].Verb))
		settings.Permissions[i].Namespace = strings.TrimSpace(settings.Permissions[i].Namespace)
	}
	return json.Marshal(settings)
}

func (Registration) Configure(
	_ context.Context,
	name string,
	raw json.RawMessage,
	_ registry.Services,
	config *builtin.Config,
) error {
	normalized, err := (Registration{}).Normalize(ToolType, raw)
	if err != nil {
		return err
	}
	var settings Settings
	if err := json.Unmarshal(normalized, &settings); err != nil {
		return err
	}
	handler := findOrCreateHandler(config)
	handler.AddCapability(name, settings)
	config.Capabilities = append(config.Capabilities, capabilityFor(name, settings))
	return nil
}

func findOrCreateHandler(config *builtin.Config) *Handler {
	for _, h := range config.Handlers {
		if kh, ok := h.(*Handler); ok {
			return kh
		}
	}
	handler := NewHandler()
	config.Handlers = append(config.Handlers, handler)
	return handler
}

type failedClient struct{ err error }

func (c *failedClient) Get(context.Context, GetRequest) (*unstructured.Unstructured, error) {
	return nil, c.err
}
func (c *failedClient) List(context.Context, ListRequest) (*unstructured.UnstructuredList, error) {
	return nil, c.err
}
func (c *failedClient) Apply(context.Context, ApplyRequest) (*unstructured.Unstructured, string, error) {
	return nil, "", c.err
}
func (c *failedClient) Delete(context.Context, DeleteRequest) error { return c.err }
func (c *failedClient) Logs(context.Context, LogsRequest) (string, error) {
	return "", c.err
}
func (c *failedClient) Events(context.Context, EventsRequest) (*unstructured.UnstructuredList, error) {
	return nil, c.err
}

// capabilityFor publishes one tool capability named by the local tool name. The
// input schema is a discriminated union over the verbs the permissions grant.
func capabilityFor(name string, settings Settings) dispatcher.Capability {
	verbs := newPermissionPolicy(settings.Permissions).permittedVerbs()
	branches := make([]json.RawMessage, 0, len(verbs))
	for _, v := range verbs {
		branches = append(branches, verbSchemas[v])
	}
	schema := json.RawMessage(`{"type":"object"}`)
	if len(branches) > 0 {
		oneOf, _ := json.Marshal(map[string]any{"oneOf": branches})
		schema = oneOf
	}
	scopeNote := describePermissions(settings.Permissions)
	approvalNote := ""
	if settings.RequireApproval != nil && *settings.RequireApproval {
		approvalNote = " All operations require human approval."
	}
	return dispatcher.Capability{
		Name:        name,
		Description: fmt.Sprintf("Kubernetes operations selected by `verb` (%s). Allowed: %s.%s", strings.Join(verbs, ", "), scopeNote, approvalNote),
		InputSchema: schema,
	}
}

func describePermissions(perms []Permission) string {
	parts := make([]string, 0, len(perms))
	for _, p := range perms {
		resource := p.Resource
		if resource == "" {
			resource = "*"
		}
		ns := p.Namespace
		if ns == "" {
			ns = "*"
		}
		verb := p.Verb
		if verb == "" {
			verb = "*"
		}
		parts = append(parts, fmt.Sprintf("%s %s in %s", verb, resource, ns))
	}
	return strings.Join(parts, "; ")
}

// verbSchemas are the per-verb branches of the union input schema. Each carries
// a `verb` discriminator const plus that verb's operation fields.
var verbSchemas = map[string]json.RawMessage{
	"get":    json.RawMessage(`{"type":"object","properties":{"verb":{"const":"get"},"api_version":{"type":"string","description":"API version (e.g. v1, apps/v1)"},"kind":{"type":"string","description":"Resource kind (e.g. Pod, Deployment)"},"namespace":{"type":"string"},"name":{"type":"string"}},"required":["verb","api_version","kind","name"],"additionalProperties":false}`),
	"list":   json.RawMessage(`{"type":"object","properties":{"verb":{"const":"list"},"api_version":{"type":"string"},"kind":{"type":"string"},"namespace":{"type":"string"},"label_selector":{"type":"string"},"field_selector":{"type":"string"},"limit":{"type":"integer","minimum":1}},"required":["verb","api_version","kind"],"additionalProperties":false}`),
	"apply":  json.RawMessage(`{"type":"object","properties":{"verb":{"const":"apply"},"resource":{"type":"object","description":"Full Kubernetes resource object as JSON"}},"required":["verb","resource"],"additionalProperties":false}`),
	"delete": json.RawMessage(`{"type":"object","properties":{"verb":{"const":"delete"},"api_version":{"type":"string"},"kind":{"type":"string"},"namespace":{"type":"string"},"name":{"type":"string"}},"required":["verb","api_version","kind","name"],"additionalProperties":false}`),
	"logs":   json.RawMessage(`{"type":"object","properties":{"verb":{"const":"logs"},"namespace":{"type":"string"},"name":{"type":"string","description":"Pod name"},"container":{"type":"string"},"tail_lines":{"type":"integer","minimum":1},"limit_bytes":{"type":"integer","minimum":1}},"required":["verb","name"],"additionalProperties":false}`),
	"events": json.RawMessage(`{"type":"object","properties":{"verb":{"const":"events"},"namespace":{"type":"string"},"involved_object":{"type":"string"},"field_selector":{"type":"string"},"limit":{"type":"integer","minimum":1}},"required":["verb"],"additionalProperties":false}`),
}
