// Package tap registers the `singer-tap` action mechanism: run a Singer tap and
// capture records and state (ADR-0048). The specific services a tap talks to
// are declared in the config, not compiled in (SPEC: How an agent discovers
// capabilities and connectors, ADR-0018).
package tap

import (
	"fmt"

	"github.com/Mathias-g/Servitor/internal/registry"
)

func init() {
	registry.Register(capability)
}

var capability = &registry.Capability{
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
