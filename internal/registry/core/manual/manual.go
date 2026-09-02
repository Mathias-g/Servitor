// Package manual registers the `manual` trigger mechanism: invoked via the CLI
// (ADR-0048). It is part of Servitor's spec, not a mechanism, so this
// package is never removed.
package manual

import "github.com/Mathias-g/Servitor/internal/registry"

func init() {
	registry.Register(capability)
}

var capability = &registry.Capability{
	Name:           "manual",
	Desc:           "Invoked via the CLI.",
	Role:           registry.RoleTrigger,
	Delivery:       registry.DeliveryManual,
	MechanismGroup: registry.Core,
	Fields:         map[string]*registry.Field{},
}
