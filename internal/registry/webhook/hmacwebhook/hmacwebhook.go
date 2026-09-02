// Package hmacwebhook registers the `hmac-webhook` trigger mechanism: an
// inbound HTTP receiver that verifies HMAC-SHA256 over a string and delivers
// the raw body (ADR-0049). The mechanism is generic; the thing it verifies is
// declared per receiver in the config (which header, which encoding, and
// optionally a timestamped prefix), so any HMAC signer, raw-body or
// timestamped, is a config entry, not a mechanism. It is a mechanism, a
// distinct deletable unit: remove this folder and the type disappears with no
// central reference left to edit.
package hmacwebhook

import "github.com/Mathias-g/Servitor/internal/registry"

func init() {
	registry.Register(capability)
}

var capability = &registry.Capability{
	Name:           "hmac-webhook",
	Desc:           "Inbound HTTP receiver that verifies an HMAC-SHA256 signature over the raw body (or a timestamped form of it) and delivers the body.",
	Role:           registry.RoleTrigger,
	Delivery:       registry.DeliveryInstant,
	MechanismGroup: registry.Webhook,
	Fields: map[string]*registry.Field{
		"path": {Type: "string", Required: true, Desc: "Path of the declared receiver to receive on.", Examples: []any{"/hooks/things"}},
	},
}
