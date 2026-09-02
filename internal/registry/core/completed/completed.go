// Package completed registers the `completed` trigger mechanism: fired by
// another workflow's completion (ADR-0048). It is part of Servitor's spec, not
// a mechanism, so this package is never removed.
package completed

import "github.com/Mathias-g/Servitor/internal/registry"

func init() {
	registry.Register(capability)
}

var capability = &registry.Capability{
	Name:           "completed",
	Desc:           "Fired by another workflow's completion.",
	Role:           registry.RoleTrigger,
	Delivery:       registry.DeliveryEvent,
	MechanismGroup: registry.Core,
	Fields:         map[string]*registry.Field{"workflow": {Type: "string", Required: true, Desc: "The workflow that fires this.", Examples: []any{"upstream-workflow"}}},
}
