---
status: proposed
date: 2026-08-29
decision-makers: [Mathias]
consulted: []
informed: []
scope:
  - runner
  - honker
interface-impact: new
---

# ADR-0043: The wait timer uses the queue's one-shot RunAt

## Context and problem statement

The `wait` node (ADR-0041) can resume on a timer. The runner needs a durable,
one-shot "resume at time T" job that survives restarts, for a `wait.timer` that
may be hours or days in the future.

## Decision drivers

- The resume job must be durable in the same SQLite file and survive a daemon
  restart, like every other queued job.
- It is a one-shot fire at a time, not a recurring schedule.
- Best Simple System for Now (ADR-0002): reuse Honker's existing durability
  rather than build a new delayed-queue construct.
- The author-facing surface is explicit, so the author never relies on type
  inference about whether a value is a duration or an absolute time.

## Considered options

- **Honker queue `EnqueueOptions{RunAt}` (chosen).** Enqueuing a resume job with
  `RunAt` (an absolute unix epoch) makes it claimable at that time. It is
  durable in the same `_honker_live` table as every other job and survives
  restarts, and the worker's claim loop (`ClaimWaker`) sleeps until the soonest
  future `RunAt` and wakes then. No new construct.
- **The Honker scheduler.** Rejected: the scheduler is cron-only (recurring
  schedules), not a one-shot "resume at T".
- **A dedicated delayed-queue construct.** Rejected: it would partly reimplement
  what the queue's `RunAt` already does, against BSSN.

## Decision outcome

Chosen option: **the queue's one-shot `RunAt`.**

A `wait.timer` enqueues a resume job with `EnqueueOptions{RunAt}` on the node
queue. The timer is expressed in the Wafer with two explicit sub-fields, both
optional:

- `after`: a duration (for example `48h`), resolved to a `RunAt` at park time
  (`now + duration`).
- `at`: an absolute resume time (for example `2026-09-01T10:00:00Z`), used
  directly as the `RunAt`.

Explicit sub-fields rather than one auto-detected field, so the author never
relies on type inference. The Honker wrapper's `Tx.Enqueue` needs a
`RunAt`-carrying variant, since it currently hardcodes an empty
`EnqueueOptions`.

### Consequences

- Good: reuses Honker's durability and restart survival with no new machinery.
- Good: `after` and `at` are explicit, so the author does not guess the field's
  type.
- Good: the worker's claim loop already handles the "sleep until a future
  `RunAt`" case.
- Bad: a `RunAt`-carrying enqueue variant must be added to the honker wrapper.
- Neutral: a `wait` with a timer also needs to cancel its pending `RunAt` job
  when a signal resolves the wait first (ADR-0041's first-wins semantics).

### Confirmation

`go test ./...` passes. Tests assert that a `wait.timer` enqueues a job whose
`RunAt` matches the resolved time, that the job is claimable only after that
time, and that it survives a store reopen (restart).

## Interface notes

New Wafer schema: the `wait` node's `timer` sub-fields `after` (duration) and
`at` (absolute time). No change to existing cron or poll scheduling.

## More information

- SPEC: Nodes (flow nodes)
- ADR-0041 (the `wait` node), ADR-0040 (suspend/resume machinery)
- Honker queue `EnqueueOptions{RunAt}` (honker-go), IDEAS.md "Suspended waits"
