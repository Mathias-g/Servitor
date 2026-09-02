// Package email registers the `email_received` trigger mechanism under the
// `helper` mechanism group (ADR-0031, ADR-0048). Email is a mechanism, a
// distinct deletable unit: remove this folder and the type disappears from
// validation and capabilities with no central reference left to edit. Its
// provider transport and auth live in the gmail mechanism package alongside it
// (ADR-0027).
package email

import "github.com/Mathias-g/Servitor/internal/registry"

func init() {
	registry.Register(received)
}

var received = &registry.Capability{
	Name:           "email_received",
	Desc:           "Inbound email parsed into a structured payload.",
	Role:           registry.RoleTrigger,
	Delivery:       registry.DeliveryPolling,
	MechanismGroup: registry.Helper,
	Fields: map[string]*registry.Field{
		"host":     {Type: "string", Required: true, Desc: "IMAP host of the mailbox, for example imap.gmail.com.", Examples: []any{"imap.gmail.com"}},
		"username": {Type: "string", Required: true, Desc: "Mailbox account, for example me@company.com.", Examples: []any{"me@company.com"}},
		"secret":   {Type: "string", Required: true, Desc: "Varlock secret name holding the app password.", Examples: []any{"GMAIL_APP_PASSWORD"}},
		"poll":     {Type: "string", Desc: "Cron schedule to poll for new mail; default every 5 minutes.", Examples: []any{"*/5 * * * *"}},
	},
}
