// Package transform registers the `transform` action mechanism: reshape,
// extract, or compute over previous nodes' JSON output (ADR-0048). It is part
// of Servitor's spec, not a mechanism, so this package is never removed.
package transform

import (
	"fmt"

	"github.com/Mathias-g/Servitor/internal/components/selfexe"
	"github.com/Mathias-g/Servitor/internal/registry"
)

func init() {
	registry.Register(capability)
}

var capability = &registry.Capability{
	Name:           "transform",
	Desc:           "Reshape, extract, or compute over previous nodes' JSON output.",
	Role:           registry.RoleAction,
	SideEffect:     false,
	MechanismGroup: registry.Core,
	Fields: map[string]*registry.Field{
		"expression": {Type: "string", Required: true, Desc: "A JSONata expression over the step's `{event, steps}` input (ADR-0020, ADR-0021).", Examples: []any{"$sum(steps.fetch.items[active=true].amount)"}},
	},
	RunKind: registry.RunPlain,
	Spawn: func(cfg map[string]any) ([]string, error) {
		expr, ok := cfg["expression"].(string)
		if !ok || expr == "" {
			return nil, fmt.Errorf("transform requires a string expression")
		}
		return []string{selfexe.Path(), "__transform", expr}, nil
	},
}
