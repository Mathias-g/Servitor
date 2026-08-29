package registry

import (
	"encoding/json"
	"testing"
)

func TestNodesSorted(t *testing.T) {
	types := Nodes()
	for i := 1; i < len(types); i++ {
		if types[i-1].Name >= types[i].Name {
			t.Fatalf("node types not sorted: %q before %q", types[i-1].Name, types[i].Name)
		}
	}
}

func TestLookupNode(t *testing.T) {
	if LookupNode("http") == nil {
		t.Fatal("expected http node type to exist")
	}
	if LookupNode("nope") != nil {
		t.Fatal("unexpected node type nope")
	}
}

func TestNodeJSONSchema(t *testing.T) {
	st := LookupNode("http")
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
	for _, key := range []string{"name", "triggers", "nodes"} {
		if _, ok := props[key]; !ok {
			t.Fatalf("wafer schema missing property %q", key)
		}
	}
}

func TestRolesSeparateTriggerFromNode(t *testing.T) {
	// A trigger capability (email_received) is valid under `trigger:` but not
	// under `nodes:`.
	if LookupTrigger("email_received") == nil {
		t.Fatal("email_received should be trigger-usable")
	}
	if LookupNode("email_received") != nil {
		t.Fatal("email_received should not be node-usable")
	}
	// An action node (http) is valid under `nodes:` but not `trigger:`.
	if LookupNode("http") == nil {
		t.Fatal("http should be node-usable")
	}
	if LookupTrigger("http") != nil {
		t.Fatal("http should not be trigger-usable")
	}
	// Delivery is set on triggers.
	if tr := LookupTrigger("email_received"); tr.Delivery != DeliveryPolling {
		t.Fatalf("email_received delivery = %q, want polling", tr.Delivery)
	}
	if tr := LookupTrigger("github_webhook"); tr.Delivery != DeliveryInstant {
		t.Fatalf("github_webhook delivery = %q, want instant", tr.Delivery)
	}
}

func TestFlowNodesAreNotActionsOrTriggers(t *testing.T) {
	// switch and foreach are flow nodes: valid under `nodes:` (like actions),
	// but their role is `flow`, not `action`, and they are not triggers.
	for _, name := range []string{"switch", "foreach"} {
		c := Lookup(name)
		if c == nil {
			t.Fatalf("node %q should exist", name)
		}
		if c.Role != RoleFlow {
			t.Fatalf("node %q role = %q, want flow", name, c.Role)
		}
		if LookupNode(name) == nil {
			t.Fatalf("node %q should be node-usable", name)
		}
		if LookupTrigger(name) != nil {
			t.Fatalf("node %q should not be a trigger", name)
		}
	}
	// An ordinary work node (http) is an action, not a flow node.
	if http := LookupNode("http"); http.Role != RoleAction {
		t.Fatalf("http role = %q, want action", http.Role)
	}
}
