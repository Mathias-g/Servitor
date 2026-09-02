---
status: proposed
date: 2026-08-29
decision-makers: [Mathias]
consulted: []
informed: []
scope:
  - runner
  - honker
interface-impact: none
---

# ADR-0040: Suspend/resume via a DAG-shaped continuation

## Context and problem statement

Servitor checkpoints data, not a running program: each node's `{event, steps}`
input and the run's pending counter live in Honker, so a run is "a queued job
with a persisted input" rather than code being replayed from an event history
(SPEC: Execution model). To support long-lived workflows (approval waits, timed
holds, saga compensation, external-callback waits) the run must be able to pause
mid-way and resume much later, without adopting a durable-execution/replay
architecture.

The pause must be *between nodes*, never inside a node's own work: each node
runs as a subprocess (ADR-0008) that runs to completion and exits, so only its
result survives. Resuming means starting the next node with the saved results,
not re-entering a half-finished computation.

## Decision drivers

- The runner keeps its checkpoint-not-replay model; "months later" is a queued
  job with a persisted input, not a replay of an event history.
- The continuation must be general enough to resume from an arbitrary point in
  the run's dependency DAG (ADR-0023), because the resume-from-failure modes
  (SPEC: Secret invalidity and rotation) also resume from an arbitrary point.
- The no-split transactional discipline (SPEC: Execution model step 8) applies
  to parking and resuming too: each is a single SQLite transaction.
- Best Simple System for Now (ADR-0002): no durable-execution engine, no
  replay; a parked run is just a row and a paused job.

## Considered options

- **DAG-shaped continuation (chosen).** Parking writes one
  `suspended_continuations` row holding the wait node's whole downstream
  sub-DAG (its `Downstream` tree) plus the current `run_deps` state for those
  nodes, sets the run status to a new `waiting`, and acks the wait job's claim,
  all in one transaction. Resuming re-enqueues the continuation frontier
  (pending +1), flips the status back to `running`, and deletes the row. A run
  can park before a fan-in rejoin, inside a `foreach` body, or in a `switch`
  branch, because the continuation carries the sub-DAG, not a single next node.
- **Linear continuation (a single "next node id" + its input).** Simpler, but
  only fits a run that parks at a node with exactly one successor. It cannot
  express a wait before a fan-in or inside a fan-out, and it is too narrow to
  reuse for resume-from-failure, which can happen at any node.
- **Durable-execution / event replay.** Adopts a Temporal-style engine. Rejected:
  it is a large architectural change and contradicts the checkpoint-not-replay
  model that already makes Servitor's long waits cheap.
- **Resume against the current Wafer rather than a frozen continuation.** If a
  parked run's continuation were re-read from the Wafer on resume, a redeployed
  Wafer would run the parked run's remaining nodes against a definition it was
  not built from. Rejected: freezing the continuation at park time keeps a
  parked run self-consistent and matches the self-contained-job model.

## Decision outcome

Chosen option: **a DAG-shaped continuation.**

A run parks by writing a `suspended_continuations` row that holds the parked
node's downstream sub-DAG and the `run_deps` state for those nodes, setting the
run status to `waiting`, and acking the wait job's claim, all in one SQLite
transaction. A parked run is not complete, so the run-completion guard becomes
`pending == 0 && status != waiting`. Resuming re-enqueues the continuation
frontier (pending +1), flips the status to `running`, and deletes the row; the
run picks up at the next node after the park with the pre-park results already
saved.

The continuation is DAG-shaped, not a single "next node id", because a run is a
dependency DAG and a park can sit before a fan-in or inside a fan-out. This is
the same machinery the resume-from-failure `continue` mode reuses, which also
resumes from an arbitrary point in the DAG; a shared DAG-shaped continuation is
what keeps it general enough for both.

**The continuation is frozen at park time.** A run may be parked for months,
and the Wafer may be redeployed with changed nodes while it is parked. The
continuation is serialized into the `suspended_continuations` row at park time,
so a parked run resumes against its original definition and new runs use the new
wafer. This is consistent with the self-contained-job model (a `NodeJob` carries
its own definition) and needs no extra machinery for very long waits. The
alternative, resuming against whatever the current Wafer says, would run a
parked run's continuation against a definition it was not built from, which is
inconsistent and surprising.

### Consequences

- Good: a parked run is durable in the same SQLite file and survives restarts,
  with no replay machinery.
- Good: the machinery is general enough to serve both the `wait` node and the
  resume-from-failure modes.
- Good: parking and resuming each stay a single atomic transaction.
- Bad: the continuation is more complex than a linear one (a sub-DAG plus
  `run_deps` state must be serialized and restored).
- Neutral: a parked run holds no live work, so it is drain-safe by construction.

### Confirmation

`go test ./...` passes. Tests assert that parking writes the continuation row,
sets `waiting`, and acks in one transaction; that a parked run is not marked
completed (`pending == 0` alone does not complete a `waiting` run); and that
resuming re-enqueues the frontier and flips the status back to `running`. A test
covers parking inside a fan-out / before a fan-in, not just a linear chain.

## Interface notes

None. This is internal runner machinery. The `waiting` run status it introduces
is surfaced by the run inspector (SPEC: Control plane), which is covered by the
`wait` node ADR's interface notes.

## More information

- SPEC: Execution model, Secret invalidity and rotation (resume-from-failure)
- ADR-0008 (subprocess-per-node), ADR-0023 (dependency fan-out), ADR-0021
  (input shape), ADR-0002 (BSSN)
- IDEAS.md "Suspended waits" (the shape this machinery serves)
