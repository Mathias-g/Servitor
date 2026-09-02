// Package wait registers the `wait` flow mechanism: park the run and resume it
// later, via a timer or a named signal (ADR-0041, ADR-0048). It is part of
// Servitor's spec, not a mechanism, so this package is never removed.
package wait

import "github.com/Mathias-g/Servitor/internal/registry"

func init() {
	registry.Register(capability)
}

var capability = &registry.Capability{
	Name:           "wait",
	Desc:           "Park the run and resume it later, via a timer or a named signal, resolving on whichever fires first (ADR-0041).",
	Role:           registry.RoleFlow,
	SideEffect:     false,
	MechanismGroup: registry.Core,
	Fields: map[string]*registry.Field{
		"signal": {Type: "string", Desc: "A JSONata expression over the step's `{event, steps}` input resolved at park time to the effective signal name (ADR-0042). A signal resumes the run with the payload; the result reports `source: \"signal\"`.", Examples: []any{"approval_gate.${event.order_id}"}},
		"timer":  {Type: "object", Desc: "A timer that resumes the run: `after` (a duration, for example `48h`) or `at` (an absolute time). The result reports `source: \"timer\"` (ADR-0043).", Examples: []any{map[string]any{"after": "48h"}}},
	},
	RunKind: registry.RunFlow,
}
