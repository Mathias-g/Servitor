// Package http registers the `mcp-http` action mechanism: invoke one named tool
// on one named MCP server over Streamable HTTP (ADR-0047). It connects to the
// server's declared URL with its secret-referenced token, sending one
// `tools/call` request. The server's URL and headers are declared in the
// config, not compiled in. The executor runs as the hidden `__mcp_http`
// subprocess (ADR-0008); the worker looks up the server's URL from the
// boot-loaded connector registry and spawns it (PLAN Phase 17).
package http

import (
	"github.com/Mathias-g/Servitor/internal/registry"
)

func init() {
	registry.Register(capability)
}

var capability = &registry.Capability{
	Name:           "mcp-http",
	Desc:           "Invoke one named tool on one named MCP server over Streamable HTTP (ADR-0047).",
	Role:           registry.RoleAction,
	SideEffect:     true,
	MechanismGroup: registry.MCP,
	Fields: map[string]*registry.Field{
		"server": {Type: "string", Required: true, Desc: "The declared MCP server to connect to, resolved from its URL in the config.", Examples: []any{"atomic"}},
		"tool":   {Type: "string", Required: true, Desc: "The named tool to invoke.", Examples: []any{"search"}},
		"input":  {Type: "object", Desc: "The tool arguments.", Examples: []any{map[string]any{"query": "meeting notes"}}},
		"mode":   {Type: "string", Desc: "The MCP protocol mode the server speaks, `classic` or `stateless`, copied from capabilities. Omit to probe once at run time.", Examples: []any{"stateless"}},
	},
	RunKind: registry.RunMCP,
}
