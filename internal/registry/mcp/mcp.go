// Package mcp registers the tool-invocation mechanism: the `mcp` mechanism
// group (ADR-0031, ADR-0045). It is the generic `mcp-call` node type; the
// specific MCP servers it talks to are declared in servitor.integrations.yaml,
// not compiled in (SPEC: How an agent discovers integrations, ADR-0018).
package mcp

import "github.com/Mathias-g/Servitor/internal/registry"

func init() {
	registry.Register(call)
}

var call = &registry.Capability{
	Name:           "mcp-call",
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
}
