---
status: accepted
date: 2026-08-29
decision-makers: [Mathias]
consulted: []
informed: []
scope:
  - runner
  - triggers
interface-impact: new
---

# ADR-0039: The failed-secret event is a distinct signal, not a `completed` overload

## Context and problem statement

When a secret-authenticated node fails after its retries are exhausted, the
operator needs to know (SPEC: Secret invalidity and rotation). The natural
first thought is to reuse the `completed` trigger's completion callback: it
already fires a downstream workflow when an upstream run finishes. But
`completed` is defined as *successful* completion (SPEC: Triggers, ADR-0026),
and the "Suspended waits" idea plans to reuse it to resume a parked run on an
upstream's clean finish. Overloading `completed` to also fire on failure would
blur that meaning and start downstream work on an upstream the operator did not
intend to continue from.

The failed-secret notification is a distinct concern: it is an operator
alert (someone must fix the secret), not a workflow continuation. It should
have its own signal, even though it reuses the same callback *plumbing* (the
worker-to-daemon wire that the completion callback uses).

## Decision drivers

- `completed` must stay success-completion-only, because the future resume use
  ("Suspended waits") depends on a clean "upstream finished, continue" signal.
- A failed secret is an alert, not a continuation; the operator wires a
  notification to it.
- Best Simple System for Now (ADR-0002): reuse the existing callback wire rather
  than build a parallel notification channel, but keep the failure signal
  distinct from `completed`.

## Considered options

- **A distinct failure signal (chosen).** The worker fires a run-failed callback
  when a run fails (its last node dead-letters), and the daemon surfaces it as a
  `failed` event the operator can trigger a notification workflow on. `completed`
  stays success-only. Reuses the callback plumbing; does not overload
  `completed`.
- **Overload `completed` to fire on failure too.** Rejected: it would blur the
  success-only meaning and, via the planned resume use, start/resume downstream
  work the operator did not intend on an upstream failure.
- **No event at all.** Rejected: the operator needs to know a secret went bad,
  and the SPEC requires an event a workflow can trigger on.

## Decision outcome

Chosen option: **a distinct failure signal.**

When a run fails (a node is dead-lettered after retries), the worker marks the
run `failed` and fires a run-failed callback, mirroring the completion callback.
The daemon surfaces it as a `failed` event the operator can wire a notification
workflow to. The `completed` trigger remains success-completion-only and is
unchanged. The two share the callback plumbing but are different signals.

The failed run's event is distinct from the `completed` trigger's event, so an
operator wires their own alert (Slack, email, text, anything) to it without
`completed` firing spuriously.

### Consequences

- Good: `completed` stays success-only, protecting the future resume use.
- Good: the operator gets a distinct, actionable signal that a secret failed.
- Good: reuses the existing callback wire rather than a new channel.
- Bad: adds a second callback and a new event surface.

### Confirmation

`go test ./...` passes. Tests assert that a dead-lettered node marks its run
`failed` and fires the failure callback, and that the `completed` trigger is not
fired for a failed run.

## Interface notes

New surface. The worker gains a run-failed callback and the daemon a failed-run
event surface for the operator to wire a notification to. The Wafer schema is
unchanged: `completed` is unchanged, and the failed-secret signal is an event
the operator reacts to, not a new Wafer key.

## More information

- SPEC: Secret invalidity and rotation, Triggers (`completed`)
- ADR-0026 (the completion callback this reuses), ADR-0038 (the `completed`
  rename)
- IDEAS.md "Suspended waits" (the future resume use that `completed` must stay
  clean for)
