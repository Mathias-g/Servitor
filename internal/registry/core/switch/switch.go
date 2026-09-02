// Package switchNode registers the `switch` flow mechanism: route to one named
// branch based on a value (ADR-0048). It is part of Servitor's spec, not a
// mechanism, so this package is never removed.
package switchNode

import (
	"fmt"

	"github.com/Mathias-g/Servitor/internal/components/selfexe"
	"github.com/Mathias-g/Servitor/internal/registry"
)

func init() {
	registry.Register(capability)
}

var capability = &registry.Capability{
	Name:           "switch",
	Desc:           "Route to one named branch based on a value.",
	Role:           registry.RoleFlow,
	SideEffect:     false,
	MechanismGroup: registry.Core,
	Fields: map[string]*registry.Field{
		"expression": {Type: "string", Required: true, Desc: "A JSONata expression over the step's `{event, steps}` input producing the routing value (ADR-0020, ADR-0022).", Examples: []any{"steps.check"}},
		"cases":      {Type: "object", Required: true, Desc: "Map of value to the name of the top-level node to route to.", Examples: []any{map[string]any{"high": "notify_finance", "low": "log_and_done"}}},
		"default":    {Type: "string", Desc: "Name of the top-level node to route to when no `cases` key matches.", Examples: []any{"log_unknown"}},
	},
	RunKind: registry.RunFlow,
	Spawn: func(cfg map[string]any) ([]string, error) {
		expr, ok := cfg["expression"].(string)
		if !ok || expr == "" {
			return nil, fmt.Errorf("switch requires a string expression")
		}
		return []string{selfexe.Path(), "__switch", expr}, nil
	},
}
