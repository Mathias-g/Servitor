// Package failed registers the `failed` trigger mechanism: fired by another
// workflow's failure (ADR-0048). It is part of Servitor's spec, not a
// mechanism, so this package is never removed.
package failed

import "github.com/Mathias-g/Servitor/internal/registry"

func init() {
	registry.Register(capability)
}

var capability = &registry.Capability{
	Name:           "failed",
	Desc:           "Fired by another workflow's failure.",
	Role:           registry.RoleTrigger,
	Delivery:       registry.DeliveryEvent,
	MechanismGroup: registry.Core,
	Fields:         map[string]*registry.Field{"workflow": {Type: "string", Required: true, Desc: "The workflow that fires this.", Examples: []any{"upstream-workflow"}}},
}
