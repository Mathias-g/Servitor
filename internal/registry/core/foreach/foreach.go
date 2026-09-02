// Package foreach registers the `foreach` flow mechanism: fan a node out over
// a list (ADR-0048). It is part of Servitor's spec, not a mechanism, so this
// package is never removed.
package foreach

import (
	"fmt"

	"github.com/Mathias-g/Servitor/internal/components/selfexe"
	"github.com/Mathias-g/Servitor/internal/registry"
)

func init() {
	registry.Register(capability)
}

var capability = &registry.Capability{
	Name:           "foreach",
	Desc:           "Fan a node out over a list.",
	Role:           registry.RoleFlow,
	SideEffect:     false,
	MechanismGroup: registry.Core,
	Fields: map[string]*registry.Field{
		"over": {Type: "string", Required: true, Desc: "A JSONata expression over the step's `{event, steps}` input yielding the list to iterate (ADR-0020, ADR-0024).", Examples: []any{"steps.fetch_ids"}},
		"as":   {Type: "string", Desc: "Name for each element in the loop, exposed in each iteration's input. Defaults to `item`.", Examples: []any{"item"}},
		"body": {Type: "string", Required: true, Desc: "Name of the top-level node to run once per element (ADR-0024).", Examples: []any{"process_one"}},
	},
	RunKind: registry.RunFlow,
	Spawn: func(cfg map[string]any) ([]string, error) {
		expr, ok := cfg["over"].(string)
		if !ok || expr == "" {
			return nil, fmt.Errorf("foreach requires a string `over` expression")
		}
		return []string{selfexe.Path(), "__foreach", expr}, nil
	},
}
