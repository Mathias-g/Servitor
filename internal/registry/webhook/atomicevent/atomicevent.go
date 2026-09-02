// Package atomicevent registers the `atomic_event` trigger mechanism: Atomic
// knowledge-base changes (ADR-0048). It is a mechanism, a distinct deletable
// unit: remove this folder and the type disappears with no central reference
// left to edit.
package atomicevent

import "github.com/Mathias-g/Servitor/internal/registry"

func init() {
	registry.Register(capability)
}

var capability = &registry.Capability{
	Name:           "atomic_event",
	Desc:           "Atomic knowledge-base changes.",
	Role:           registry.RoleTrigger,
	Delivery:       registry.DeliveryInstant,
	MechanismGroup: registry.Webhook,
	Fields:         map[string]*registry.Field{"path": {Type: "string", Required: true, Desc: "Path to receive on.", Examples: []any{"/hooks/atomic"}}},
}
