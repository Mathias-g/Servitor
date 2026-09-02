// Package slackevent registers the `slack_event` trigger mechanism: Slack
// events (messages, mentions) (ADR-0048). It is a mechanism, a distinct
// deletable unit: remove this folder and the type disappears with no central
// reference left to edit.
package slackevent

import "github.com/Mathias-g/Servitor/internal/registry"

func init() {
	registry.Register(capability)
}

var capability = &registry.Capability{
	Name:           "slack_event",
	Desc:           "Slack events (messages, mentions).",
	Role:           registry.RoleTrigger,
	Delivery:       registry.DeliveryInstant,
	MechanismGroup: registry.Webhook,
	Fields:         map[string]*registry.Field{"path": {Type: "string", Required: true, Desc: "Path to receive on.", Examples: []any{"/hooks/slack"}}, "secret": {Type: "string", Desc: "Secret name to verify the X-Slack-Signature header with.", Examples: []any{"SLACK_SIGNING_SECRET"}}},
}
