// Package gristwebhook registers the `grist_webhook` trigger mechanism: a
// Grist-specific receiver (ADR-0048). It is a mechanism, a distinct
// deletable unit: remove this folder and the type disappears with no central
// reference left to edit.
package gristwebhook

import "github.com/Mathias-g/Servitor/internal/registry"

func init() {
	registry.Register(capability)
}

var capability = &registry.Capability{
	Name:           "grist_webhook",
	Desc:           "Grist-specific receiver.",
	Role:           registry.RoleTrigger,
	Delivery:       registry.DeliveryInstant,
	MechanismGroup: registry.Webhook,
	Fields:         map[string]*registry.Field{"path": {Type: "string", Required: true, Desc: "Path to receive on.", Examples: []any{"/hooks/grist"}}},
}
