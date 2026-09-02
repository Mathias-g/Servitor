// Package cron registers the `cron` trigger mechanism: run on the Honker
// scheduler (ADR-0048). It is part of Servitor's spec, not a mechanism, so
// this package is never removed.
package cron

import "github.com/Mathias-g/Servitor/internal/registry"

func init() {
	registry.Register(capability)
}

var capability = &registry.Capability{
	Name:           "cron",
	Desc:           "Run on the Honker scheduler.",
	Role:           registry.RoleTrigger,
	Delivery:       registry.DeliveryScheduled,
	MechanismGroup: registry.Core,
	Fields:         map[string]*registry.Field{"schedule": {Type: "string", Required: true, Desc: "Cron expression.", Examples: []any{"0 * * * *"}}},
}
