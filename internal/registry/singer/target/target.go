// Package target registers the `singer-target` action mechanism: run a Singer
// target consuming records (ADR-0048). The specific services a target talks to
// are declared in the config, not compiled in (SPEC: How an agent discovers
// capabilities and connectors, ADR-0018).
package target

import (
	"fmt"

	"github.com/Mathias-g/Servitor/internal/registry"
)

func init() {
	registry.Register(capability)
}

var capability = &registry.Capability{
	Name:           "singer-target",
	Desc:           "Run a Singer target consuming records.",
	Role:           registry.RoleAction,
	SideEffect:     true,
	MechanismGroup: registry.Singer,
	Fields: map[string]*registry.Field{
		"target": {Type: "string", Required: true, Desc: "The target to run.", Examples: []any{"target-grist"}},
		"config": {Type: "object", Desc: "Target config."},
	},
	RunKind: registry.RunSinger,
	Spawn: func(cfg map[string]any) ([]string, error) {
		target, ok := cfg["target"].(string)
		if !ok || target == "" {
			return nil, fmt.Errorf("singer-target requires a `target` name")
		}
		return []string{target}, nil
	},
}
