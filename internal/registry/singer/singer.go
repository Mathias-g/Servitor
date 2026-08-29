// Package singer registers the record-streaming mechanisms: the `singer`
// mechanism group (ADR-0031, ADR-0045). These are the generic tap and target
// node types; the specific services they talk to are declared in
// servitor.integrations.yaml, not compiled in (SPEC: How an agent discovers
// integrations, ADR-0018).
package singer

import (
	"fmt"

	"github.com/Mathias-g/Servitor/internal/registry"
)

func init() {
	registry.Register(tap)
	registry.Register(target)
}

var tap = &registry.Capability{
	Name:           "singer-tap",
	Desc:           "Run a Singer tap and capture records and state.",
	Role:           registry.RoleAction,
	SideEffect:     true,
	MechanismGroup: registry.Singer,
	Fields: map[string]*registry.Field{
		"tap":     {Type: "string", Required: true, Desc: "The tap to run.", Examples: []any{"tap-stripe"}},
		"config":  {Type: "object", Desc: "Tap config."},
		"catalog": {Type: "array", Desc: "The selected streams to sync, copied from the tap's catalog in capabilities. Each entry is one stream with its schema and a `selected: true` metadata. Omit to sync all streams.", Examples: []any{[]any{map[string]any{"stream": "customers", "tap_stream_id": "customers", "schema": map[string]any{"type": "object"}, "metadata": []any{map[string]any{"breadcrumb": []any{}, "metadata": map[string]any{"selected": true}}}}}}},
	},
	RunKind: registry.RunSinger,
	Spawn: func(cfg map[string]any) ([]string, error) {
		tap, ok := cfg["tap"].(string)
		if !ok || tap == "" {
			return nil, fmt.Errorf("singer-tap requires a `tap` name")
		}
		return []string{tap}, nil
	},
}

var target = &registry.Capability{
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
