---
status: accepted
date: 2026-08-24
decision-makers: [Mathias]
consulted: []
informed: []
scope:
  - runner
  - triggers
  - control-plane
interface-impact: new
---

# ADR-0013: Receive webhooks on a separate non-loopback listener, matched against a registered-workflow index

## Context and problem statement

Inbound triggers (webhooks and manual) added to the runner (SPEC: Triggers). Two design questions had to
be settled. First, webhooks must be reachable by external senders, which appears
to collide with the control plane's loopback-only rule (ADR-0009). Second, the
worker executes runs as self-contained step chains with no registry (ADR-0012),
but matching an inbound event to a workflow requires knowing the registered
workflows. Where should that knowledge live?

## Decision drivers

- The control plane must stay loopback-only (ADR-0009); the closed-down property
  is about who can change runner behavior or operate it, not about receiving the
  events the runner is supposed to react to.
- A webhook sender is not an operator or a deploy pipeline; it is an external
  system the runner exists to receive from. Those are different surfaces.
- The Wafer is the artifact and the source of truth (SPEC: The Wafer). A
  registered copy must be a faithful mirror, not a divergent source.
- Best Simple System for Now: a stored index of registered workflows is simpler
  than re-scanning a repo checkout the daemon does not know about.

## Considered options

- **Separate webhook listener + stored workflow index (chosen).** The daemon
  runs a second HTTP listener on a configurable `--webhook-addr` that may bind
  any interface, serving the trigger receiver. Registered workflows live in a
  `workflows` Honker table holding a faithful copy of the submitted Wafer YAML,
  plus an `events` table for the persisted raw event. `submit`/`enable`/
  `disable`/`trigger` operate on it over the loopback control plane.
- **Serve webhooks on the loopback control-plane listener.** Rejected: external
  senders cannot reach loopback, and mixing inbound untrusted events onto the
  operator-gated control-plane surface muddies the two very different trust
  domains.
- **Re-scan a checked-out repo directory of Wafers.** Rejected for now: the
  daemon has no configured repo path, and it reintroduces a filesystem dependency
  that registration (`submit`) already avoids. Revisit only if a git-sync deploy
  path emerges.

## Decision outcome

Chosen option: **separate webhook listener + stored workflow index.**

The control plane stays loopback-only and is unchanged in spirit (ADR-0009); it
now also carries `submit`/`enable`/`disable`/`trigger`, which are exactly the
gated operations ADR-0009 described. Inbound webhooks are served on a distinct
listener (disabled unless `--webhook-addr` is set). Registration stores the
submitted Wafer as a faithful copy in the `workflows` table; the trigger
receiver persists each raw event to `events`, verifies the signature, matches
enabled workflows whose trigger path the request hit, and enqueues a run per
match (SPEC: Execution model steps 1-5).

### Consequences

- Good: webhooks are externally reachable without opening the control plane; the
  two trust domains stay separate.
- Good: the worker still needs no registry (ADR-0012 holds); only the trigger
  receiver reads the workflow index to match events.
- Good: the stored Wafer is a faithful copy of the artifact, so there is no
  divergent source of truth; `enable`/`disable` only flip a trigger flag.
- Bad: a registered workflow's definition is duplicated in the store alongside
  the repo. Acceptable: `submit` is the deploy step that keeps the copy faithful,
  and the artifact remains authoritative.
- Neutral: the `secret` field added to `http_webhook`/`standard_webhook` trigger
  config is an additive, optional schema change; an empty/absent secret leaves
  the receiver open until varlock supplies signing secrets (SPEC: Varlock).

### Confirmation

`go test ./...` passes. The daemon integration tests pin the behavior: a signed
Standard Webhooks request to the webhook listener is persisted, matched, and run
(`TestDaemonWebhookReceiver`), and `submit`+`trigger` drive a run through the
control plane (`TestDaemonSubmitAndManual`). The control-plane loopback check
(`checkLoopback`) still rejects non-loopback addresses for the control plane.

## Interface notes

Additive to the Wafer schema: `http_webhook` and `standard_webhook` trigger
configs gain an optional `secret` field (the name of a runner secret to verify
with). Additive to the daemon protocol: `POST /v1/submit`, `/v1/enable`,
`/v1/disable`, `/v1/trigger`, and to the CLI: `servitor submit`, `enable`,
`disable`, `trigger`. `servitor run` gains `--webhook-addr`. None of these are
breaking; existing consumers are unaffected.

## More information

- ADR-0009 (gate the control plane; this clarifies webhooks are a separate surface)
- ADR-0012 (self-contained step chain; the worker still has no registry)
- SPEC: Triggers, Execution model steps 1-5
