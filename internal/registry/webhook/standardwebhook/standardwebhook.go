// Package standardwebhook registers the `standard-webhook` trigger mechanism:
// an inbound HTTP receiver that verifies the Standard Webhooks envelope (a
// versioned, timestamped signature with a replay window) and delivers the raw
// body (ADR-0049). The mechanism is generic; any compliant producer (OpenAI,
// Anthropic, Supabase, Twilio, ...) works out of the box. It is a mechanism, a
// distinct deletable unit: remove this folder and the type disappears with no
// central reference left to edit.
package standardwebhook

import "github.com/Mathias-g/Servitor/internal/registry"

func init() {
	registry.Register(capability)
}

var capability = &registry.Capability{
	Name:           "standard-webhook",
	Desc:           "Inbound HTTP receiver that verifies the Standard Webhooks envelope (timestamped, replay-bounded) and delivers the body.",
	Role:           registry.RoleTrigger,
	Delivery:       registry.DeliveryInstant,
	MechanismGroup: registry.Webhook,
	Fields: map[string]*registry.Field{
		"path": {Type: "string", Required: true, Desc: "Path of the declared receiver to receive on.", Examples: []any{"/hooks/std"}},
	},
}
