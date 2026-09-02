// Package sendsignal registers the `send-signal` action mechanism: wake a
// parked run in another workflow by named signal (ADR-0042, ADR-0048). It is
// part of Servitor's spec, not a mechanism, so this package is never
// removed.
package sendsignal

import "github.com/Mathias-g/Servitor/internal/registry"

func init() {
	registry.Register(capability)
}

var capability = &registry.Capability{
	Name:           "send-signal",
	Desc:           "Wake a parked run in another workflow by named signal (ADR-0042).",
	Role:           registry.RoleAction,
	SideEffect:     false,
	MechanismGroup: registry.Core,
	Fields: map[string]*registry.Field{
		"signal":  {Type: "string", Required: true, Desc: "A JSONata expression over the step's `{event, steps}` input resolving to the effective signal name of the parked run to wake.", Examples: []any{"approval_gate.${event.order_id}"}},
		"payload": {Type: "any", Desc: "A JSONata expression for the payload delivered to the parked run's wait node result. Omit for no payload.", Examples: []any{map[string]any{"approved": true}}},
	},
	RunKind: registry.RunFlow,
}
