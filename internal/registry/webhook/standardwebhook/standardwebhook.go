// Package standardwebhook registers the `standard_webhook` trigger mechanism: a
// Standard Webhooks-compliant receiver (ADR-0048). It is a mechanism, a
// distinct deletable unit: remove this folder and the type disappears with no
// central reference left to edit.
package standardwebhook

import "github.com/Mathias-g/Servitor/internal/registry"

func init() {
	registry.Register(capability)
}

var capability = &registry.Capability{
	Name:           "standard_webhook",
	Desc:           "Standard Webhooks-compliant receiver.",
	Role:           registry.RoleTrigger,
	Delivery:       registry.DeliveryInstant,
	MechanismGroup: registry.Webhook,
	Fields:         map[string]*registry.Field{"path": {Type: "string", Required: true, Desc: "Path to receive on.", Examples: []any{"/hooks/std"}}, "secret": {Type: "string", Desc: "Secret name to verify the Standard Webhooks signature with.", Examples: []any{"WEBHOOK_SECRET"}}},
}
