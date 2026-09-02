// Package email is a shared component: the provider-agnostic shape of a parsed
// inbound message, the thing the core passes around when an email poll fires a
// run (SPEC: Triggers, `email_received`, ADR-0046). It names no capability and
// depends on no provider; provider-specific transport and parsing live in the
// mechanism package that produces this type (for example
// internal/registry/helper/email/gmail), which the core never depends on
// (ADR-0027).
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
