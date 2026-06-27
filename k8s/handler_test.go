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

func TestReadOperationsReturnResultImmediately(t *testing.T) {
	mc := &mockClient{
		getResult:    &unstructured.Unstructured{Object: map[string]any{"kind": "Pod"}},
		listResult:   &unstructured.UnstructuredList{Items: []unstructured.Unstructured{{Object: map[string]any{"kind": "Pod"}}}},
		logsResult:   "some logs",
		eventsResult: &unstructured.UnstructuredList{},
	}
	h := NewHandler(mc)
	h.AddCapability("k8s.get", Settings{})
	h.AddCapability("k8s.list", Settings{})
	h.AddCapability("k8s.logs", Settings{})
	h.AddCapability("k8s.events", Settings{})

	ctx := context.Background()

	outcome, err := h.DispatchCall(ctx, dispatcher.Call{
		Name: "k8s.get",
		Args: json.RawMessage(`{"api_version":"v1","kind":"Pod","name":"nginx"}`),
	}, dispatcher.Authorization{})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if outcome.Kind() != dispatcher.OutcomeResult {
		t.Fatalf("get outcome = %s, want result", outcome.Kind())
	}

	outcome, err = h.DispatchCall(ctx, dispatcher.Call{
		Name: "k8s.list",
		Args: json.RawMessage(`{"api_version":"v1","kind":"Pod"}`),
	}, dispatcher.Authorization{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if outcome.Kind() != dispatcher.OutcomeResult {
		t.Fatalf("list outcome = %s, want result", outcome.Kind())
	}

	outcome, err = h.DispatchCall(ctx, dispatcher.Call{
		Name: "k8s.logs",
		Args: json.RawMessage(`{"name":"nginx"}`),
	}, dispatcher.Authorization{})
	if err != nil {
		t.Fatalf("logs: %v", err)
	}
	if outcome.Kind() != dispatcher.OutcomeResult {
		t.Fatalf("logs outcome = %s, want result", outcome.Kind())
	}

	outcome, err = h.DispatchCall(ctx, dispatcher.Call{
		Name: "k8s.events",
		Args: json.RawMessage(`{}`),
	}, dispatcher.Authorization{})
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if outcome.Kind() != dispatcher.OutcomeResult {
		t.Fatalf("events outcome = %s, want result", outcome.Kind())
	}
}

func TestMutationYieldsWithoutApproval(t *testing.T) {
	mc := &mockClient{
		applyResult: &unstructured.Unstructured{Object: map[string]any{"kind": "Deployment"}},
		applyAction: "created",
	}
	h := NewHandler(mc)
	h.AddCapability("k8s.apply", Settings{})

	outcome, err := h.DispatchCall(context.Background(), dispatcher.Call{
		Name: "k8s.apply",
		Args: json.RawMessage(`{"resource":{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"nginx","namespace":"default"}}}`),
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
	h := NewHandler(mc)
	h.AddCapability("k8s.apply", Settings{})

	outcome, err := h.DispatchCall(context.Background(), dispatcher.Call{
		Name: "k8s.apply",
		Args: json.RawMessage(`{"resource":{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"nginx","namespace":"default"}}}`),
	}, dispatcher.Authorization{Decision: dispatcher.Approved})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if outcome.Kind() != dispatcher.OutcomeResult {
		t.Fatalf("apply with approval = %s, want result", outcome.Kind())
	}
}

func TestDeleteYieldsWithoutApproval(t *testing.T) {
	mc := &mockClient{}
	h := NewHandler(mc)
	h.AddCapability("k8s.delete", Settings{})

	outcome, err := h.DispatchCall(context.Background(), dispatcher.Call{
		Name: "k8s.delete",
		Args: json.RawMessage(`{"api_version":"v1","kind":"Pod","namespace":"default","name":"nginx"}`),
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
	h := NewHandler(mc)
	h.AddCapability("k8s.apply", Settings{RequireApproval: &f})

	outcome, err := h.DispatchCall(context.Background(), dispatcher.Call{
		Name: "k8s.apply",
		Args: json.RawMessage(`{"resource":{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"nginx","namespace":"default"}}}`),
	}, dispatcher.Authorization{})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if outcome.Kind() != dispatcher.OutcomeResult {
		t.Fatalf("apply with approval disabled = %s, want result", outcome.Kind())
	}
}

func TestApprovalCanBeEnabledForReads(t *testing.T) {
	mc := &mockClient{
		getResult: &unstructured.Unstructured{Object: map[string]any{"kind": "Secret"}},
	}
	tr := true
	h := NewHandler(mc)
	h.AddCapability("k8s.get", Settings{RequireApproval: &tr})

	outcome, err := h.DispatchCall(context.Background(), dispatcher.Call{
		Name: "k8s.get",
		Args: json.RawMessage(`{"api_version":"v1","kind":"Secret","name":"creds"}`),
	}, dispatcher.Authorization{})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if outcome.Kind() != dispatcher.OutcomeYield {
		t.Fatalf("get with approval required = %s, want yield", outcome.Kind())
	}
}

func TestNamespacePolicyRejectsDisallowed(t *testing.T) {
	mc := &mockClient{}
	h := NewHandler(mc)
	h.AddCapability("k8s.apply", Settings{Namespaces: []string{"staging"}})

	outcome, err := h.DispatchCall(context.Background(), dispatcher.Call{
		Name: "k8s.apply",
		Args: json.RawMessage(`{"resource":{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"nginx","namespace":"production"}}}`),
	}, dispatcher.Authorization{Decision: dispatcher.Approved})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if outcome.Kind() != dispatcher.OutcomeFailed {
		t.Fatalf("apply to disallowed namespace = %s, want failed", outcome.Kind())
	}
}
