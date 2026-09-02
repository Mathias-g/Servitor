---
status: superseded by ADR-0049
date: 2026-08-25
decision-makers: [Mathias]
consulted: []
informed: []
scope:
  - triggers
interface-impact: new
---

# ADR-0025: Verify bespoke provider webhooks with per-type functions in the receiver

## Context and problem statement

Provider-specific webhook triggers (`github_webhook`, `slack_event`,
`grist_webhook`, `atomic_event`) each have a signing scheme that does not match
Standard Webhooks (SPEC: Triggers, "Using webhook triggers"). The receiver needs
to verify these inbound signatures, and the SPEC recorded as an open question
how those bespoke schemes should be framed: one generic abstraction for every
provider, or something simpler.

## Decision drivers

- The receiver already dispatches by trigger type in a small `verify` switch.
- Best Simple System for Now (ADR-0002): do not add a signing-scheme registry or
  abstraction while the number of providers is small and each has a different,
  well-documented scheme.
- The subprocess and loopback security models (ADR-0008, ADR-0009) already
  establish the trust boundaries; signature verification is a thin per-provider
  concern, not a new subsystem.

## Considered options

- **Per-type verify function in the receiver's existing switch (chosen).** Each
  provider trigger type dispatches to one small function that implements that
  provider's exact scheme, in `internal/trigger`. Adding a provider is adding a
  case and a function.
- **A signing-scheme registry.** Register each provider's verify as an
  implementation of a common interface. More structure than the two live
  providers need; a layer without a second consumer yet.
- **Delegate to an external webhook-signing library.** Adds a dependency and
  pulls in schemes Servitor does not use; the schemes are each a few lines.

## Decision outcome

Chosen option: **per-type verify function in the receiver's existing switch.**

Each provider-specific trigger type is served by its own verify function in the
trigger receiver, dispatched by the type in the same `verify` switch that serves
`standard_webhook` and `http_webhook`. No separate signing-scheme abstraction is
introduced. The two built so far are:

- `github_webhook`: HMAC-SHA256 of the body with the shared secret, hex digest
  in `X-Hub-Signature-256` as `sha256=<hex>`.
- `slack_event`: HMAC-SHA256 of `v0:<timestamp>:<body>` with the signing secret,
  hex digest in `X-Slack-Signature` as `v0=<hex>`, a replay-bounding timestamp
  window on `X-Slack-Request-Timestamp`, and the `url_verification` setup
  handshake answered by echoing the challenge.

A provider whose service signs differently (for example Grist, which today
authenticates webhooks with a static `Authorization` header rather than an HMAC)
is added the same way: a case and a function matching what that service actually
sends.

### Consequences

- Good: adding a provider is a localized change; no new abstraction to learn or
  maintain.
- Good: each function reads against the provider's real documented scheme, so it
  is easy to audit.
- Bad: if many providers arrive, the switch grows. Acceptable for now under
  BSSN; a registry can be introduced when there is a real second abstraction
  need.

### Confirmation

`go test ./...` passes. Tests in `internal/trigger` pin each provider's behavior:
a correctly signed GitHub and Slack request is verified and enqueues a run, a bad
signature is rejected with 401, Slack's stale timestamp is rejected, and Slack's
`url_verification` handshake echoes the challenge without enqueuing a run.

## Interface notes

Additive to the Wafer schema: `github_webhook` and `slack_event` trigger configs
gain an optional `secret` field, the name of a runner secret holding the shared
key, matching the existing `http_webhook`/`standard_webhook` `secret` field.
Additive to the receiver, not to the daemon control protocol or the CLI. Not
breaking; existing consumers are unaffected.

## More information

- ADR-0013 (the webhook listener and registered-workflow index)
- ADR-0017 (mechanism as the organizing principle; provider-specific types share
  the `webhook` mechanism)
- SPEC: Triggers, "Using webhook triggers"
