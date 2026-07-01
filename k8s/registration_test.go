package k8s

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aurora-capcompute/aurora-dispatchers/builtin"
	"github.com/aurora-capcompute/aurora-dispatchers/registry"
)

func TestK8sMatchesType(t *testing.T) {
	reg := Registration{}
	if !reg.Matches("core.k8s") {
		t.Fatal("should match core.k8s")
	}
	if reg.Matches("k8s.get") {
		t.Fatal("must match by type, not an operation name")
	}
}

func TestK8sNormalizeRequiresPermissions(t *testing.T) {
	if _, err := (Registration{}).Normalize("core.k8s", json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected error when permissions is empty")
	}
}

// One tool publishes one capability named by its local name, with a union input
// schema over exactly the verbs the permissions grant.
func TestK8sConfigurePublishesUnionOfPermittedVerbs(t *testing.T) {
	raw := json.RawMessage(`{"permissions":[{"verb":"get","resource":"pod"},{"verb":"list","resource":"pod"}]}`)
	var config builtin.Config
	if err := (Registration{}).Configure(context.Background(), "prodK8s", raw, registry.Services{}, &config); err != nil {
		t.Fatalf("configure: %v", err)
	}
	if len(config.Capabilities) != 1 || config.Capabilities[0].Name != "prodK8s" {
		t.Fatalf("capabilities = %+v, want one named prodK8s", config.Capabilities)
	}
	var schema struct {
		OneOf []map[string]any `json:"oneOf"`
	}
	if err := json.Unmarshal(config.Capabilities[0].InputSchema, &schema); err != nil {
		t.Fatalf("schema not a oneOf union: %v", err)
	}
	if len(schema.OneOf) != 2 {
		t.Fatalf("oneOf branches = %d, want 2 (get, list only)", len(schema.OneOf))
	}
	if len(config.Handlers) != 1 || !config.Handlers[0].Handles("prodK8s") {
		t.Fatal("handler must route by the local name prodK8s")
	}
}
