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

var validOperations = map[string]struct{}{
	"k8s.get":    {},
	"k8s.list":   {},
	"k8s.apply":  {},
	"k8s.delete": {},
	"k8s.logs":   {},
	"k8s.events": {},
}

type Registration struct{}

func (Registration) Matches(name string) bool {
	_, ok := validOperations[name]
	return ok
}

func (Registration) Normalize(name string, raw json.RawMessage) (json.RawMessage, error) {
	if _, ok := validOperations[name]; !ok {
		return nil, fmt.Errorf("unsupported k8s operation %q", name)
	}
	var settings Settings
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &settings); err != nil {
			return nil, err
		}
	}
	settings.Kubeconfig = strings.TrimSpace(settings.Kubeconfig)
	settings.Context = strings.TrimSpace(settings.Context)
	cleaned := make([]string, 0, len(settings.Namespaces))
	for _, ns := range settings.Namespaces {
		ns = strings.TrimSpace(ns)
		if ns != "" {
			cleaned = append(cleaned, ns)
		}
	}
	settings.Namespaces = cleaned
	return json.Marshal(settings)
}

func (Registration) Configure(
	_ context.Context,
	name string,
	raw json.RawMessage,
	_ registry.Services,
	config *builtin.Config,
) error {
	normalized, err := (Registration{}).Normalize(name, raw)
	if err != nil {
		return err
	}
	var settings Settings
	if err := json.Unmarshal(normalized, &settings); err != nil {
		return err
	}

	handler := findOrCreateHandler(config, settings)
	handler.AddCapability(name, settings)
	config.Capabilities = append(config.Capabilities, capabilityFor(name, settings))
	return nil
}

func findOrCreateHandler(config *builtin.Config, settings Settings) *Handler {
	for _, h := range config.Handlers {
		if kh, ok := h.(*Handler); ok {
			return kh
		}
	}
	client, err := NewClient(settings.Kubeconfig, settings.Context)
	if err != nil {
		client = &failedClient{err: err}
	}
	handler := NewHandler(client)
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

func (Registration) IsSubset(name string, parent, child json.RawMessage) error {
	var parentSettings, childSettings Settings
	if err := json.Unmarshal(parent, &parentSettings); err != nil {
		return fmt.Errorf("decode parent settings: %w", err)
	}
	if err := json.Unmarshal(child, &childSettings); err != nil {
		return fmt.Errorf("decode child settings: %w", err)
	}
	if len(parentSettings.Namespaces) > 0 {
		allowed := make(map[string]struct{}, len(parentSettings.Namespaces))
		for _, ns := range parentSettings.Namespaces {
			allowed[ns] = struct{}{}
		}
		for _, ns := range childSettings.Namespaces {
			if _, ok := allowed[ns]; !ok {
				return fmt.Errorf("child namespace %q is not in parent's allowed namespaces", ns)
			}
		}
		if len(childSettings.Namespaces) == 0 {
			return fmt.Errorf("child must specify namespaces when parent restricts them")
		}
	}
	return nil
}

func capabilityFor(name string, settings Settings) dispatcher.Capability {
	nsNote := "all namespaces"
	if len(settings.Namespaces) > 0 {
		nsNote = "namespaces: " + strings.Join(settings.Namespaces, ", ")
	}
	approvalNote := ""
	if requiresApproval(name, settings) {
		approvalNote = " Requires human approval."
	}

	switch name {
	case "k8s.get":
		return dispatcher.Capability{
			Name:        "k8s.get",
			Description: fmt.Sprintf("Get a Kubernetes resource by API version, kind, namespace, and name. %s.%s", nsNote, approvalNote),
			InputSchema: json.RawMessage(`{"type":"object","properties":{"api_version":{"type":"string","description":"API version (e.g. v1, apps/v1)"},"kind":{"type":"string","description":"Resource kind (e.g. Pod, Deployment)"},"namespace":{"type":"string","description":"Namespace (omit for cluster-scoped)"},"name":{"type":"string","description":"Resource name"}},"required":["api_version","kind","name"],"additionalProperties":false}`),
		}
	case "k8s.list":
		return dispatcher.Capability{
			Name:        "k8s.list",
			Description: fmt.Sprintf("List Kubernetes resources by API version and kind. %s.%s", nsNote, approvalNote),
			InputSchema: json.RawMessage(`{"type":"object","properties":{"api_version":{"type":"string"},"kind":{"type":"string"},"namespace":{"type":"string"},"label_selector":{"type":"string","description":"Label selector (e.g. app=nginx)"},"field_selector":{"type":"string"},"limit":{"type":"integer","minimum":1}},"required":["api_version","kind"],"additionalProperties":false}`),
		}
	case "k8s.apply":
		return dispatcher.Capability{
			Name:        "k8s.apply",
			Description: fmt.Sprintf("Create or update a Kubernetes resource from JSON. %s.%s", nsNote, approvalNote),
			InputSchema: json.RawMessage(`{"type":"object","properties":{"resource":{"type":"object","description":"Full Kubernetes resource object as JSON"}},"required":["resource"],"additionalProperties":false}`),
		}
	case "k8s.delete":
		return dispatcher.Capability{
			Name:        "k8s.delete",
			Description: fmt.Sprintf("Delete a Kubernetes resource. %s.%s", nsNote, approvalNote),
			InputSchema: json.RawMessage(`{"type":"object","properties":{"api_version":{"type":"string"},"kind":{"type":"string"},"namespace":{"type":"string"},"name":{"type":"string"}},"required":["api_version","kind","name"],"additionalProperties":false}`),
		}
	case "k8s.logs":
		return dispatcher.Capability{
			Name:        "k8s.logs",
			Description: fmt.Sprintf("Get logs from a Kubernetes pod. %s.%s", nsNote, approvalNote),
			InputSchema: json.RawMessage(`{"type":"object","properties":{"namespace":{"type":"string"},"name":{"type":"string","description":"Pod name"},"container":{"type":"string","description":"Container name (for multi-container pods)"},"tail_lines":{"type":"integer","minimum":1},"limit_bytes":{"type":"integer","minimum":1}},"required":["name"],"additionalProperties":false}`),
		}
	case "k8s.events":
		return dispatcher.Capability{
			Name:        "k8s.events",
			Description: fmt.Sprintf("List Kubernetes events. %s.%s", nsNote, approvalNote),
			InputSchema: json.RawMessage(`{"type":"object","properties":{"namespace":{"type":"string"},"involved_object":{"type":"string","description":"Name of the resource to filter events for"},"field_selector":{"type":"string"},"limit":{"type":"integer","minimum":1}},"additionalProperties":false}`),
		}
	default:
		return dispatcher.Capability{Name: name, Description: "Unknown k8s operation."}
	}
}
