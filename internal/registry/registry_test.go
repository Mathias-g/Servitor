package registry_test

import (
	"encoding/json"
	"testing"

	"github.com/Mathias-g/Servitor/internal/registry"
	_ "github.com/Mathias-g/Servitor/internal/registry/mechanisms"
)

func TestNodesSorted(t *testing.T) {
	types := registry.Nodes()
	for i := 1; i < len(types); i++ {
		if types[i-1].Name >= types[i].Name {
			t.Fatalf("node types not sorted: %q before %q", types[i-1].Name, types[i].Name)
		}
	}
}

func TestLookupNode(t *testing.T) {
	if registry.LookupNode("http") == nil {
		t.Fatal("expected http node type to exist")
	}
	if registry.LookupNode("nope") != nil {
		t.Fatal("unexpected node type nope")
	}
}

func TestNodeJSONSchema(t *testing.T) {
	st := registry.LookupNode("http")
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
	s := registry.WaferSchema()
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
	if registry.LookupTrigger("email_received") == nil {
		t.Fatal("email_received should be trigger-usable")
	}
	if registry.LookupNode("email_received") != nil {
		t.Fatal("email_received should not be node-usable")
	}
	// An action node (http) is valid under `nodes:` but not `trigger:`.
	if registry.LookupNode("http") == nil {
		t.Fatal("http should be node-usable")
	}
	if registry.LookupTrigger("http") != nil {
		t.Fatal("http should not be trigger-usable")
	}
	// Delivery is set on triggers.
	if tr := registry.LookupTrigger("email_received"); tr.Delivery != registry.DeliveryPolling {
		t.Fatalf("email_received delivery = %q, want polling", tr.Delivery)
	}
	if tr := registry.LookupTrigger("hmac-webhook"); tr.Delivery != registry.DeliveryInstant {
		t.Fatalf("hmac-webhook delivery = %q, want instant", tr.Delivery)
	}
}

func TestFlowNodesAreNotActionsOrTriggers(t *testing.T) {
	// switch and foreach are flow nodes: valid under `nodes:` (like actions),
	// but their role is `flow`, not `action`, and they are not triggers.
	for _, name := range []string{"switch", "foreach"} {
		c := registry.Lookup(name)
		if c == nil {
			t.Fatalf("node %q should exist", name)
		}
		if c.Role != registry.RoleFlow {
			t.Fatalf("node %q role = %q, want flow", name, c.Role)
		}
		if registry.LookupNode(name) == nil {
			t.Fatalf("node %q should be node-usable", name)
		}
		if registry.LookupTrigger(name) != nil {
			t.Fatalf("node %q should not be a trigger", name)
		}
	}
	// An ordinary work node (http) is an action, not a flow node.
	if http := registry.LookupNode("http"); http.Role != registry.RoleAction {
		t.Fatalf("http role = %q, want action", http.Role)
	}
}

func TestCommandForDispatchesThroughRegistry(t *testing.T) {
	// Spawn is set by each mechanism's package and the runner dispatches through
	// it (ADR-0045), so the runner names no node type in a switch.
	cmd, err := registry.CommandFor("shell", map[string]any{"command": "echo hi"})
	if err != nil || len(cmd) != 3 || cmd[0] != "/bin/sh" || cmd[1] != "-c" || cmd[2] != "echo hi" {
		t.Fatalf("shell spawn = %v, %v; want /bin/sh -c echo hi", cmd, err)
	}
	cmd, err = registry.CommandFor("singer-tap", map[string]any{"tap": "tap-stripe"})
	if err != nil || len(cmd) != 1 || cmd[0] != "tap-stripe" {
		t.Fatalf("singer-tap spawn = %v, %v; want [tap-stripe]", cmd, err)
	}
	cmd, err = registry.CommandFor("mcp-stdio", map[string]any{"server": "atomic-server"})
	if err != nil || len(cmd) != 1 || cmd[0] != "atomic-server" {
		t.Fatalf("mcp-stdio spawn = %v, %v; want [atomic-server]", cmd, err)
	}
	// A control node has no plain subprocess: nil, nil.
	if cmd, err := registry.CommandFor("wait", map[string]any{}); err != nil || cmd != nil {
		t.Fatalf("wait spawn = %v, %v; want nil, nil", cmd, err)
	}
	// An unknown type is rejected.
	if _, err := registry.CommandFor("nope", nil); err == nil {
		t.Fatal("unknown type should error")
	}
}

func TestRunKindForDispatchesThroughRegistry(t *testing.T) {
	cases := map[string]registry.RunKind{
		"singer-tap":    registry.RunSinger,
		"singer-target": registry.RunSinger,
		"mcp-stdio":     registry.RunMCP,
		"mcp-http":      registry.RunMCP,
		"switch":        registry.RunFlow,
		"foreach":       registry.RunFlow,
		"wait":          registry.RunFlow,
		"shell":         registry.RunPlain,
		"http":          registry.RunPlain,
		"nope":          registry.RunPlain,
	}
	for typ, want := range cases {
		if got := registry.RunKindFor(typ); got != want {
			t.Fatalf("RunKindFor(%q) = %q, want %q", typ, got, want)
		}
	}
}

func TestMCPTransportMechanisms(t *testing.T) {
	// The mcp group splits by transport (ADR-0047): mcp-stdio spawns a server
	// as a subprocess; mcp-http connects to a URL. Both have RunKind RunMCP, so
	// the worker dispatches them through runMCPNode. mcp-http has no Spawn
	// (there is no command to build; its URL comes from the connector registry
	// and it runs as the hidden __mcp_http subprocess), so CommandFor returns
	// nil, nil just like a control node, and the worker handles it.
	if registry.Lookup("mcp-stdio") == nil {
		t.Fatal("mcp-stdio should be registered")
	}
	if registry.Lookup("mcp-http") == nil {
		t.Fatal("mcp-http should be registered")
	}
	if cmd, err := registry.CommandFor("mcp-stdio", map[string]any{"server": "srv"}); err != nil || len(cmd) != 1 || cmd[0] != "srv" {
		t.Fatalf("mcp-stdio spawn = %v, %v; want [srv]", cmd, err)
	}
	if cmd, err := registry.CommandFor("mcp-http", map[string]any{"server": "atomic"}); err != nil || cmd != nil {
		t.Fatalf("mcp-http spawn = %v, %v; want nil, nil (no command; worker dispatches it)", cmd, err)
	}
	if c := registry.Lookup("mcp-http"); c.RunKind != registry.RunMCP {
		t.Fatalf("mcp-http run kind = %q, want mcp", c.RunKind)
	}
}
