---
status: accepted
date: 2026-08-29
decision-makers: [Mathias]
consulted: []
informed: []
scope:
  - runner
  - control-plane
  - webhooks
interface-impact: new
---

# ADR-0042: Named signals resume a parked run

## Context and problem statement

A `wait` node parks a run (ADR-0041). Something must later wake it: another
workflow, a human, or an external service calling back. Resuming is run-scoped
in the sense that it targets a specific parked run, but a sender should not need
to know the parked run's id to wake it.

A named-signal model addresses a parked run by a name the author chooses, so a
sender needs only the business key that identifies the work, not the run id.
This mirrors Temporal's named signals.

## Decision drivers

- A sender (another workflow, a human, an external service) should wake a parked
  run without knowing its run id.
- The name is part of the Wafer (the artifact is the source of truth, ADR-0009):
  it is author-defined and discoverable via `capabilities`.
- Signals must not be lost or double-applied: a signal that races the park, and
  a repeated signal, both need defined behavior.
- No new network surface for resuming: external callbacks reuse the existing
  webhook triggers rather than a bespoke resume endpoint.
- Best Simple System for Now (ADR-0002): no durable-execution engine; the signal
  is a persisted event that wakes a parked run.

## Considered options

- **Author-named signal, resolved per run (chosen).** `signal` is a JSONata
  expression over the run's `{event, steps}` input (the same expression
  machinery as `dedupe_key` and `transform`, ADR-0020/0021), resolved at park
  time to the effective signal name. An author writes
  `signal: approval_gate.${event.order_id}` to scope by a business key the run
  already carries. Distinct business keys yield distinct effective names, so
  concurrency is unambiguous by construction; a literal `signal: approval_gate`
  means "any run parked on this name". A collision (two runs on the same
  effective name) is rejected as ambiguous, because the author controls the
  name and a collision is an authoring bug, not something the runner should
  silently resolve.
- **Signal by run id.** Unambiguous, but the sender must know the run id, which
  external callers and even other workflows often do not. Rejected: it undoes
  the benefit of a name the sender already knows.
- **A per-run one-shot resume webhook.** Rejected: it needs a new network
  surface, per-run key provisioning, and a way to hand the URL to the caller.
  External callbacks instead POST to an ordinary `http_webhook` /
  `standard_webhook` trigger, which starts a small broker workflow that sends
  the named signal, reusing the existing webhook surface.

## Decision outcome

Chosen option: **author-named signals, resolved per run.**

The `wait` node's `signal` field is a JSONata expression over the run's
`{event, steps}` input, resolved at park time to the effective signal name.
Senders address a parked run by that name:

- another workflow sends it via a `resume` / `send-signal` node,
- a human sends it via `servitor resume <signal-name> [payload]` (an optional
  run-id form is a small nicety for humans, not a separate mechanism),
- an external service POSTs to an ordinary webhook trigger that starts a small
  broker workflow, which sends the named signal.

The payload lands in the wait node's result (ADR-0041). When more than one run
is parked on the same effective name, the signal is rejected as ambiguous
(delivered to none) and reported, so the author sees their `signal` expression
is not unique.

The race rules are pinned so callers are not surprised:

- **A signal that arrives before the run parks is buffered, not dropped.** The
  webhook receiver already persists an inbound event before matching
  (SPEC: Execution model step 2); a signal is persisted the same way, and the
  `wait` node's park transaction checks for and consumes a buffered signal,
  resuming immediately if one is present.
- **A second resume is a no-op.** Once a run is resumed (or a signal consumed),
  a repeat resume must not re-run anything. It is an atomic compare-and-set on
  the run's `waiting` status: if the run is no longer `waiting`, the resume is
  ignored. This protects against double-delivery (webhook retries, an operator
  double-click).

The `completed` and `failed` triggers are unchanged: they *start* a run, while
resuming a parked run is a different operation, so they are not overloaded.

### Consequences

- Good: a sender needs only the business key, never the run id.
- Good: the name lives in the Wafer and is discoverable, so it is agent-authorable.
- Good: signals are buffered and idempotent, so a race or a duplicate is not a
  lost or doubled wake-up.
- Good: external callbacks reuse the existing webhook surface, adding no new
  network exposure.
- Bad: a same-name collision is an authoring bug the author must fix; the runner
  rejects rather than guesses.
- Neutral: a small `send-signal` node and a `servitor resume` command are added
  to the surface.

### Confirmation

`go test ./...` passes. Tests assert that a signal resolves a `wait` on the
parked run's effective name; that a signal arriving before the park is buffered
and consumed by the park transaction; that a second resume is a no-op; and that
a signal addressing two parked runs is rejected as ambiguous.

## Interface notes

New Wafer schema: the `wait` node's `signal` field (a JSONata expression). New
CLI surface: `servitor resume <signal-name> [payload]`. New node: a
`send-signal` / `resume` node for one workflow to wake another. The `completed`
and `failed` triggers are unchanged.

## More information

- SPEC: Nodes (flow nodes), Triggers, Execution model
- ADR-0040 (suspend/resume machinery), ADR-0041 (the `wait` node),
  ADR-0020/0021 (JSONata expression and the `{event, steps}` input shape)
- IDEAS.md "Suspended waits"
