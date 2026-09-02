// Package stdio registers the `mcp-stdio` action mechanism: invoke one named
// tool on one named MCP server over stdio (ADR-0015, ADR-0047). It spawns the
// named server as a subprocess with a filtered secret env and sends one
// `tools/call` over its stdin/stdout. The specific servers it talks to are
// declared in the config, not compiled in (SPEC: How an agent discovers
// capabilities and connectors, ADR-0018).
package stdio

import (
	"fmt"

	"github.com/Mathias-g/Servitor/internal/registry"
)

func init() {
	registry.Register(capability)
}

var capability = &registry.Capability{
	Name:           "mcp-stdio",
	Desc:           "Invoke one named tool on one named MCP server over stdio (ADR-0015).",
	Role:           registry.RoleAction,
	SideEffect:     true,
	MechanismGroup: registry.MCP,
	Fields: map[string]*registry.Field{
		"server": {Type: "string", Required: true, Desc: "The MCP server executable to run.", Examples: []any{"atomic-server"}},
		"tool":   {Type: "string", Required: true, Desc: "The named tool to invoke.", Examples: []any{"search"}},
		"input":  {Type: "object", Desc: "The tool arguments.", Examples: []any{map[string]any{"query": "meeting notes"}}},
		"mode":   {Type: "string", Desc: "The MCP protocol mode the server speaks, `classic` or `stateless`, copied from capabilities. Omit to probe once at run time.", Examples: []any{"stateless"}},
	},
	RunKind: registry.RunMCP,
	Spawn: func(cfg map[string]any) ([]string, error) {
		server, ok := cfg["server"].(string)
		if !ok || server == "" {
			return nil, fmt.Errorf("mcp-stdio requires a `server` name")
		}
		return []string{server}, nil
	},
}
