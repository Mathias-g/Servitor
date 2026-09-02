---
status: proposed
date: 2026-09-02
decision-makers: [maintainer]
consulted: []
informed: []
scope:
  - runner
  - capabilities
  - webhooks
interface-impact: breaking
---

# ADR-0049: Webhook receivers are declared in config; two mechanisms by verification scheme

## Context and problem statement

Inbound HTTP triggers are currently one mechanism per service: `http_webhook`,
`standard_webhook`, `grist_webhook`, `github_webhook`, `slack_event`, and
`atomic_event` each register as a separate capability under the `webhook` group.
But the mcp group was split on the principle that a mechanism is a genuinely
unique shape, with the thing a mechanism talks to declared in config rather than
compiled in (ADR-0047). Webhook is asymmetric with that: the service (Grist,
GitHub, Slack, Atomic) is baked in as a mechanism, when the actual behavior is a
small set of verification schemes over the same "receive an HTTP POST and
deliver the body" shape. Adding a new service currently means a new mechanism
package, which contradicts the one-genuine-shape principle and the deletion goal
(ADR-0048).

## Decision drivers

- A mechanism must be a genuinely unique shape, not one per service (ADR-0047,
  ADR-0048). The thing a mechanism talks to is declared in the config, not
  compiled in.
- The verification scheme is the real axis of difference: signing the raw body
  with HMAC-SHA256, versus the Standard Webhooks envelope (a timestamped,
  versioned signature that bounds replay). GitHub and Slack are HMAC variants
  (different header, encoding, or envelope string), not distinct mechanisms.
- The incoming body is not self-describing, so unlike MCP a generic webhook
  cannot parse per-service payloads for free; the workflow parses the raw body
  itself (SPEC: Node execution, `transform`).
- Best Simple System for Now: a generic webhook delivers the raw body to the
  workflow, which parses it, instead of compiled-in per-service parsing.

## Considered options

- Option A: Keep one mechanism per service (`grist_webhook`, `github_webhook`,
  `slack_event`, ...). Rejected: bakes the service into the mechanism, adds a
  package per service, and is asymmetric with mcp.
- Option B (chosen): Two mechanisms by verification scheme, receivers declared
  in config. `hmac-webhook` signs the raw body with HMAC-SHA256 (header and
  encoding are config); `standard-webhook` verifies the Standard Webhooks
  envelope (timestamped, replay-bounded). A receiver is a config entry with its
  `path`, `scheme`, and `secret`.
- Option C: One generic `webhook` mechanism with a `scheme` field. Rejected:
  the raw-body-HMAC and Standard-Webhooks shapes differ enough (the latter adds
  a timestamp window and signs a structured string) that a reader benefits from
  two named mechanisms, mirroring mcp's two-by-transport.

## Decision outcome

Chosen option: "Option B".

The `webhook` group holds two mechanisms:

- `hmac-webhook`: verify HMAC-SHA256 over the raw body and deliver it. The
  signature header name and the encoding (hex or base64) are config, so this
  covers the old `http_webhook` and `github_webhook` and any service that signs
  the body with HMAC.
- `standard-webhook`: verify the Standard Webhooks envelope (a versioned,
  timestamped signature over `id.timestamp.body` with a replay window) and
  deliver the body. This covers the old `standard_webhook`, and by config a
  service that signs a timestamped envelope like Slack.

Webhook receivers are declared in `servitor.config.yaml` under a `webhook:`
section, mirroring how MCP servers are declared under `mcp:` (ADR-0018,
ADR-0047). Each receiver names its `path`, its `scheme` (`hmac` or `standard`),
and, when it verifies a signature, a `secret`. A Wafer's trigger names the
receiver path; the mechanism is chosen by the receiver's declared scheme.

Both mechanisms deliver the **raw body** as the run's event. The workflow parses
it itself, with a `transform` node, so no per-service parsing is compiled in.
This is the mcp-symmetric model: generic mechanism, service in config, no
compiled-in service knowledge.

The per-service mechanisms (`grist_webhook`, `github_webhook`, `slack_event`,
`atomic_event`) are removed; a service becomes a config entry with the relevant
scheme. The old `http_webhook` and `standard_webhook` become the two mechanisms
(renamed to `hmac-webhook` and `standard-webhook`).

### Consequences

- Good: webhook is symmetric with mcp: generic mechanism, receivers declared in
  config, no per-service compiled-in code.
- Good: adding a service (Discord, Grist, GitHub) is a config entry, not a new
  mechanism package.
- Good: the raw-body delivery means no per-service parsing to build and
  maintain; parsing is the author's `transform` expression.
- Bad: a breaking change to the trigger surface: the per-service types
  (`grist_webhook`, `github_webhook`, `slack_event`, `atomic_event`) and the
  `http_webhook`/`standard_webhook` names are removed or renamed.
- Bad: the built-in `github_webhook`/`slack_event` structured parsing is gone;
  a workflow now parses the raw body itself.
- Neutral: the `email_received` trigger is unrelated and unchanged; it is a
  polling mechanism, not an inbound webhook.

### Confirmation

`go test ./...` passes. The `webhook` group reports `hmac-webhook` and
`standard-webhook`; the per-service types no longer appear, pinned by tests. A
receiver declared in the config with an unknown `scheme` is rejected. A webhook
delivers the raw body as the run's event.

## Interface notes

Breaking. The Wafer trigger surface changes: `http_webhook` becomes
`hmac-webhook`, `standard_webhook` becomes `standard-webhook`, and the
per-service types `grist_webhook`, `github_webhook`, `slack_event`, and
`atomic_event` are removed. Webhook receivers are declared in
`servitor.config.yaml` under `webhook:` (path, scheme, secret), and a Wafer's
webhook trigger names a receiver path instead of a per-service type. The
`email_received` trigger is unchanged.

## More information

- ADR-0017 (mechanism as organizing principle, superseded in its per-service
  webhook framing by this ADR)
- ADR-0047 (mcp mechanisms split by transport; config declared like MCP servers)
- ADR-0048 (a mechanism is one genuinely unique shape)
- ADR-0031 (a mechanism group is a family of mechanisms)
- SPEC: Triggers; SPEC: How an agent discovers capabilities and connectors
