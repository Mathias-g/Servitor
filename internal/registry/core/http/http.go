// Package http registers the `http` action mechanism: make an HTTP request and
// capture the response (ADR-0048). It is part of Servitor's spec, not a
// mechanism, so this package is never removed.
package http

import "github.com/Mathias-g/Servitor/internal/registry"

func init() {
	registry.Register(capability)
}

var capability = &registry.Capability{
	Name:           "http",
	Desc:           "Make an HTTP request and capture the response.",
	Role:           registry.RoleAction,
	SideEffect:     true,
	MechanismGroup: registry.Core,
	Fields: map[string]*registry.Field{
		"url":     {Type: "string", Required: true, Desc: "The URL to request.", Examples: []any{"https://api.example.com/things"}},
		"method":  {Type: "string", Required: true, Desc: "HTTP method.", Examples: []any{"GET"}},
		"headers": {Type: "object", Desc: "Request headers as a map.", Examples: []any{map[string]any{"Content-Type": "application/json"}}},
		"body":    {Type: "any", Desc: "Request body.", Examples: []any{map[string]any{"query": "SELECT * FROM things"}}},
		"timeout": {Type: "integer", Desc: "Request timeout in seconds.", Examples: []any{30}},
	},
	RunKind: registry.RunPlain,
}
