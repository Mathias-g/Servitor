// Package githubwebhook registers the `github_webhook` trigger mechanism: a
// GitHub-specific receiver (ADR-0048). It is a mechanism, a distinct
// deletable unit: remove this folder and the type disappears with no central
// reference left to edit.
package githubwebhook

import "github.com/Mathias-g/Servitor/internal/registry"

func init() {
	registry.Register(capability)
}

var capability = &registry.Capability{
	Name:           "github_webhook",
	Desc:           "GitHub-specific receiver.",
	Role:           registry.RoleTrigger,
	Delivery:       registry.DeliveryInstant,
	MechanismGroup: registry.Webhook,
	Fields:         map[string]*registry.Field{"path": {Type: "string", Required: true, Desc: "Path to receive on.", Examples: []any{"/hooks/github"}}, "secret": {Type: "string", Desc: "Secret name to verify the X-Hub-Signature-256 header with.", Examples: []any{"GITHUB_WEBHOOK_SECRET"}}},
}
