// Package http registers the `http` action mechanism: make an HTTP request and
// capture the response (ADR-0048). It is part of Servitor's spec, not a
// mechanism, so this package is never removed.
//
// The node runs as the hidden `__http` subprocess (ADR-0008): the worker spawns
// `[servitor __http <config>]` with the node's filtered secret env and the
// `{event, steps}` input on stdin. The config (baked into argv as JSON by
// Spawn) carries the request url, method, headers, body, and timeout; the
// headers may reference secrets as `$NAME`, resolved from the subprocess env.
package http

import (
	"encoding/json"
	"fmt"

	"github.com/Mathias-g/Servitor/internal/components/selfexe"
	"github.com/Mathias-g/Servitor/internal/registry"
)

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
		"headers": {Type: "object", Desc: "Request headers as a map; values may reference a declared secret as `$NAME`.", Examples: []any{map[string]any{"Content-Type": "application/json", "Authorization": "Bearer $API_TOKEN"}}},
		"body":    {Type: "any", Desc: "Request body.", Examples: []any{map[string]any{"query": "SELECT * FROM things"}}},
		"timeout": {Type: "integer", Desc: "Request timeout in seconds.", Examples: []any{30}},
	},
	RunKind: registry.RunPlain,
	Spawn: func(cfg map[string]any) ([]string, error) {
		url, _ := cfg["url"].(string)
		if url == "" {
			return nil, fmt.Errorf("http requires a string url")
		}
		method, _ := cfg["method"].(string)
		if method == "" {
			return nil, fmt.Errorf("http requires a string method")
		}
		config, err := json.Marshal(cfg)
		if err != nil {
			return nil, fmt.Errorf("http: encode config: %w", err)
		}
		return []string{selfexe.Path(), "__http", string(config)}, nil
	},
}
