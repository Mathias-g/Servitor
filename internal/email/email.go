// Package email is the provider-agnostic core contract for inbound email
// (SPEC: Triggers, `email_received`). It holds only the shape of a parsed
// inbound message, the thing the core passes around when an email poll fires a
// run. Provider-specific transport and parsing live in per-provider helpers
// (for example internal/gmail) which produce this type; the core never depends
// on a provider package (ADR-0027).
package email

import "time"

// Email is one parsed inbound message, the event payload a workflow receives
// from an `email_received` poll (ADR-0027).
type Email struct {
	From      string    `json:"from"`
	To        []string  `json:"to"`
	Subject   string    `json:"subject"`
	Body      string    `json:"body"`
	Date      time.Time `json:"date"`
	MessageID string    `json:"message_id"`
}
