package registry

import (
	"encoding/json"
	"testing"
)

func TestStepTypesSorted(t *testing.T) {
	types := StepTypes()
	for i := 1; i < len(types); i++ {
		if types[i-1].Name >= types[i].Name {
			t.Fatalf("step types not sorted: %q before %q", types[i-1].Name, types[i].Name)
		}
	}
}

func TestLookupStep(t *testing.T) {
	if LookupStep("http") == nil {
		t.Fatal("expected http step type to exist")
	}
	if LookupStep("nope") != nil {
		t.Fatal("unexpected step type nope")
	}
}

func TestStepJSONSchema(t *testing.T) {
	st := LookupStep("http")
	if st == nil {
		t.Fatal("http missing")
	}
	s := st.JSONSchema()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	if len(b) == 0 {
		t.Fatal("empty schema")
	}
	// required must include url and method.
	req, ok := s["required"].([]any)
	if !ok {
		t.Fatalf("schema has no required list: %s", b)
	}
	for _, want := range []string{"url", "method"} {
		found := false
		for _, r := range req {
			if r == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("required %q missing from schema: %s", want, b)
		}
	}
}

func TestWaferSchema(t *testing.T) {
	s := WaferSchema()
	if _, ok := s["$schema"]; !ok {
		t.Fatal("wafer schema missing $schema")
	}
	props, ok := s["properties"].(map[string]any)
	if !ok {
		t.Fatal("wafer schema missing properties")
	}
	for _, key := range []string{"name", "on", "steps"} {
		if _, ok := props[key]; !ok {
			t.Fatalf("wafer schema missing property %q", key)
		}
	}
}

func TestRolesSeparateTriggerFromAction(t *testing.T) {
	// A trigger-role step type (email_received) is valid under `on:` but not
	// under `steps:`.
	if LookupTrigger("email_received") == nil {
		t.Fatal("email_received should be trigger-usable")
	}
	if LookupStep("email_received") != nil {
		t.Fatal("email_received should not be action-usable")
	}
	// An action-role step type (http) is valid under `steps:` but not `on:`.
	if LookupStep("http") == nil {
		t.Fatal("http should be action-usable")
	}
	if LookupTrigger("http") != nil {
		t.Fatal("http should not be trigger-usable")
	}
	// Delivery is set on trigger-role steps.
	if tr := LookupTrigger("email_received"); tr.Delivery != DeliveryPolling {
		t.Fatalf("email_received delivery = %q, want polling", tr.Delivery)
	}
	if tr := LookupTrigger("github_webhook"); tr.Delivery != DeliveryInstant {
		t.Fatalf("github_webhook delivery = %q, want instant", tr.Delivery)
	}
}
