// Package httpwebhook registers the `http_webhook` trigger mechanism: a
// generic inbound HTTP receiver with configurable HMAC verification (ADR-0048).
// It is a mechanism, a distinct deletable unit: remove this folder and the
// type disappears with no central reference left to edit.
package httpwebhook

import "github.com/Mathias-g/Servitor/internal/registry"

func init() {
	registry.Register(capability)
}

var capability = &registry.Capability{
	Name:           "http_webhook",
	Desc:           "Generic inbound HTTP receiver with configurable HMAC verification.",
	Role:           registry.RoleTrigger,
	Delivery:       registry.DeliveryInstant,
	MechanismGroup: registry.Webhook,
	Fields:         map[string]*registry.Field{"path": {Type: "string", Required: true, Desc: "Path to receive on.", Examples: []any{"/hooks/things"}}, "secret": {Type: "string", Desc: "Secret name to verify the x-servitor-signature HMAC header with.", Examples: []any{"MY_WEBHOOK_SECRET"}}},
}
