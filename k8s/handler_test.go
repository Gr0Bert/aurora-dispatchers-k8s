package k8s

import (
	"github.com/aurora-capcompute/capcompute/dispatcher"
	"context"
	"encoding/json"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type mockClient struct {
	getResult    *unstructured.Unstructured
	listResult   *unstructured.UnstructuredList
	applyResult  *unstructured.Unstructured
	applyAction  string
	logsResult   string
	eventsResult *unstructured.UnstructuredList
	err          error
	lastApply    ApplyRequest
	lastDelete   DeleteRequest
}

func (m *mockClient) Get(_ context.Context, _ GetRequest) (*unstructured.Unstructured, error) {
	return m.getResult, m.err
}
func (m *mockClient) List(_ context.Context, _ ListRequest) (*unstructured.UnstructuredList, error) {
	return m.listResult, m.err
}
func (m *mockClient) Apply(_ context.Context, req ApplyRequest) (*unstructured.Unstructured, string, error) {
	m.lastApply = req
	return m.applyResult, m.applyAction, m.err
}
func (m *mockClient) Delete(_ context.Context, req DeleteRequest) error {
	m.lastDelete = req
	return m.err
}
func (m *mockClient) Logs(_ context.Context, _ LogsRequest) (string, error) {
	return m.logsResult, m.err
}
func (m *mockClient) Events(_ context.Context, _ EventsRequest) (*unstructured.UnstructuredList, error) {
	return m.eventsResult, m.err
}

// handlerWithMock builds a Handler holding one tool bound to a mock client.
func handlerWithMock(name string, mc Client, settings Settings) *Handler {
	return &Handler{capabilities: map[string]capabilityConfig{
		name: {
			client:          mc,
			policy:          newPermissionPolicy(settings.Permissions),
			requireApproval: settings.RequireApproval,
		},
	}}
}

// anyVerb grants every verb in any namespace.
var anyVerb = Settings{Permissions: []Permission{{Verb: "*"}}}

func TestReadOperationsReturnResultImmediately(t *testing.T) {
	mc := &mockClient{
		getResult:    &unstructured.Unstructured{Object: map[string]any{"kind": "Pod"}},
		listResult:   &unstructured.UnstructuredList{Items: []unstructured.Unstructured{{Object: map[string]any{"kind": "Pod"}}}},
		logsResult:   "some logs",
		eventsResult: &unstructured.UnstructuredList{},
	}
	h := handlerWithMock("k8sTool", mc, anyVerb)
	ctx := context.Background()

	for _, tc := range []struct{ name, args string }{
		{"get", `{"verb":"get","api_version":"v1","kind":"Pod","name":"nginx"}`},
		{"list", `{"verb":"list","api_version":"v1","kind":"Pod"}`},
		{"logs", `{"verb":"logs","name":"nginx"}`},
		{"events", `{"verb":"events"}`},
	} {
		outcome, err := h.DispatchCall(ctx, dispatcher.Call{Name: "k8sTool", Args: json.RawMessage(tc.args)}, dispatcher.Authorization{})
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if outcome.Kind() != dispatcher.OutcomeResult {
			t.Fatalf("%s outcome = %s, want result", tc.name, outcome.Kind())
		}
	}
}

func TestMutationYieldsWithoutApproval(t *testing.T) {
	mc := &mockClient{
		applyResult: &unstructured.Unstructured{Object: map[string]any{"kind": "Deployment"}},
		applyAction: "created",
	}
	h := handlerWithMock("k8sTool", mc, anyVerb)

	outcome, err := h.DispatchCall(context.Background(), dispatcher.Call{
		Name: "k8sTool",
		Args: json.RawMessage(`{"verb":"apply","resource":{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"nginx","namespace":"default"}}}`),
	}, dispatcher.Authorization{})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if outcome.Kind() != dispatcher.OutcomeYield {
		t.Fatalf("apply without approval = %s, want yield", outcome.Kind())
	}
}

func TestMutationExecutesWithApproval(t *testing.T) {
	mc := &mockClient{
		applyResult: &unstructured.Unstructured{Object: map[string]any{"kind": "Deployment"}},
		applyAction: "created",
	}
	h := handlerWithMock("k8sTool", mc, anyVerb)

	outcome, err := h.DispatchCall(context.Background(), dispatcher.Call{
		Name: "k8sTool",
		Args: json.RawMessage(`{"verb":"apply","resource":{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"nginx","namespace":"default"}}}`),
	}, dispatcher.Authorization{Decision: dispatcher.Approved})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if outcome.Kind() != dispatcher.OutcomeResult {
		t.Fatalf("apply with approval = %s, want result", outcome.Kind())
	}
}

func TestDeleteYieldsWithoutApproval(t *testing.T) {
	h := handlerWithMock("k8sTool", &mockClient{}, anyVerb)

	outcome, err := h.DispatchCall(context.Background(), dispatcher.Call{
		Name: "k8sTool",
		Args: json.RawMessage(`{"verb":"delete","api_version":"v1","kind":"Pod","namespace":"default","name":"nginx"}`),
	}, dispatcher.Authorization{})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if outcome.Kind() != dispatcher.OutcomeYield {
		t.Fatalf("delete without approval = %s, want yield", outcome.Kind())
	}
}

func TestApprovalCanBeDisabledForMutations(t *testing.T) {
	mc := &mockClient{
		applyResult: &unstructured.Unstructured{Object: map[string]any{"kind": "Deployment"}},
		applyAction: "configured",
	}
	f := false
	h := handlerWithMock("k8sTool", mc, Settings{Permissions: []Permission{{Verb: "*"}}, RequireApproval: &f})

	outcome, err := h.DispatchCall(context.Background(), dispatcher.Call{
		Name: "k8sTool",
		Args: json.RawMessage(`{"verb":"apply","resource":{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"nginx","namespace":"default"}}}`),
	}, dispatcher.Authorization{})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if outcome.Kind() != dispatcher.OutcomeResult {
		t.Fatalf("apply with approval disabled = %s, want result", outcome.Kind())
	}
}

func TestApprovalCanBeEnabledForReads(t *testing.T) {
	mc := &mockClient{getResult: &unstructured.Unstructured{Object: map[string]any{"kind": "Secret"}}}
	tr := true
	h := handlerWithMock("k8sTool", mc, Settings{Permissions: []Permission{{Verb: "*"}}, RequireApproval: &tr})

	outcome, err := h.DispatchCall(context.Background(), dispatcher.Call{
		Name: "k8sTool",
		Args: json.RawMessage(`{"verb":"get","api_version":"v1","kind":"Secret","name":"creds"}`),
	}, dispatcher.Authorization{})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if outcome.Kind() != dispatcher.OutcomeYield {
		t.Fatalf("get with approval required = %s, want yield", outcome.Kind())
	}
}

func TestPermissionRejectsDisallowedNamespace(t *testing.T) {
	h := handlerWithMock("k8sTool", &mockClient{}, Settings{Permissions: []Permission{{Verb: "apply", Namespace: "staging"}}})

	outcome, err := h.DispatchCall(context.Background(), dispatcher.Call{
		Name: "k8sTool",
		Args: json.RawMessage(`{"verb":"apply","resource":{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"nginx","namespace":"production"}}}`),
	}, dispatcher.Authorization{Decision: dispatcher.Approved})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if outcome.Kind() != dispatcher.OutcomeFailed {
		t.Fatalf("apply to disallowed namespace = %s, want failed", outcome.Kind())
	}
}

func TestPermissionRejectsUngrantedVerb(t *testing.T) {
	// Only `get` on pods is granted; a delete must be refused.
	h := handlerWithMock("k8sTool", &mockClient{}, Settings{Permissions: []Permission{{Verb: "get", Resource: "pod"}}})

	outcome, err := h.DispatchCall(context.Background(), dispatcher.Call{
		Name: "k8sTool",
		Args: json.RawMessage(`{"verb":"delete","api_version":"v1","kind":"Pod","namespace":"default","name":"nginx"}`),
	}, dispatcher.Authorization{Decision: dispatcher.Approved})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if outcome.Kind() != dispatcher.OutcomeFailed {
		t.Fatalf("ungranted delete = %s, want failed", outcome.Kind())
	}
}
