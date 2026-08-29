// Package webhook registers the inbound HTTP event reception mechanisms: the
// `webhook` mechanism group (ADR-0031, ADR-0045). Each receiver differs only in
// which signing scheme it verifies (SPEC: Triggers, ADR-0017). A per-service
// receiver is its own deletable unit: remove this package (or one receiver) and
// the type disappears from validation and capabilities with no central
// reference left to edit.
package webhook

import "github.com/Mathias-g/Servitor/internal/registry"

func init() {
	registry.Register(httpWebhook)
	registry.Register(standardWebhook)
	registry.Register(gristWebhook)
	registry.Register(githubWebhook)
	registry.Register(slackEvent)
	registry.Register(atomicEvent)
}

var httpWebhook = &registry.Capability{
	Name:           "http_webhook",
	Desc:           "Generic inbound HTTP receiver with configurable HMAC verification.",
	Role:           registry.RoleTrigger,
	Delivery:       registry.DeliveryInstant,
	MechanismGroup: registry.Webhook,
	Fields:         map[string]*registry.Field{"path": {Type: "string", Required: true, Desc: "Path to receive on.", Examples: []any{"/hooks/things"}}, "secret": {Type: "string", Desc: "Secret name to verify the x-servitor-signature HMAC header with.", Examples: []any{"MY_WEBHOOK_SECRET"}}},
}

var standardWebhook = &registry.Capability{
	Name:           "standard_webhook",
	Desc:           "Standard Webhooks-compliant receiver.",
	Role:           registry.RoleTrigger,
	Delivery:       registry.DeliveryInstant,
	MechanismGroup: registry.Webhook,
	Fields:         map[string]*registry.Field{"path": {Type: "string", Required: true, Desc: "Path to receive on.", Examples: []any{"/hooks/std"}}, "secret": {Type: "string", Desc: "Secret name to verify the Standard Webhooks signature with.", Examples: []any{"WEBHOOK_SECRET"}}},
}

var gristWebhook = &registry.Capability{
	Name:           "grist_webhook",
	Desc:           "Grist-specific receiver.",
	Role:           registry.RoleTrigger,
	Delivery:       registry.DeliveryInstant,
	MechanismGroup: registry.Webhook,
	Fields:         map[string]*registry.Field{"path": {Type: "string", Required: true, Desc: "Path to receive on.", Examples: []any{"/hooks/grist"}}},
}

var githubWebhook = &registry.Capability{
	Name:           "github_webhook",
	Desc:           "GitHub-specific receiver.",
	Role:           registry.RoleTrigger,
	Delivery:       registry.DeliveryInstant,
	MechanismGroup: registry.Webhook,
	Fields:         map[string]*registry.Field{"path": {Type: "string", Required: true, Desc: "Path to receive on.", Examples: []any{"/hooks/github"}}, "secret": {Type: "string", Desc: "Secret name to verify the X-Hub-Signature-256 header with.", Examples: []any{"GITHUB_WEBHOOK_SECRET"}}},
}

var slackEvent = &registry.Capability{
	Name:           "slack_event",
	Desc:           "Slack events (messages, mentions).",
	Role:           registry.RoleTrigger,
	Delivery:       registry.DeliveryInstant,
	MechanismGroup: registry.Webhook,
	Fields:         map[string]*registry.Field{"path": {Type: "string", Required: true, Desc: "Path to receive on.", Examples: []any{"/hooks/slack"}}, "secret": {Type: "string", Desc: "Secret name to verify the X-Slack-Signature header with.", Examples: []any{"SLACK_SIGNING_SECRET"}}},
}

var atomicEvent = &registry.Capability{
	Name:           "atomic_event",
	Desc:           "Atomic knowledge-base changes.",
	Role:           registry.RoleTrigger,
	Delivery:       registry.DeliveryInstant,
	MechanismGroup: registry.Webhook,
	Fields:         map[string]*registry.Field{"path": {Type: "string", Required: true, Desc: "Path to receive on.", Examples: []any{"/hooks/atomic"}}},
}
