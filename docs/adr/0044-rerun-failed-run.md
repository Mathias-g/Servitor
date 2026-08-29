---
status: proposed
date: 2026-08-29
decision-makers: [Mathias]
consulted: []
informed: []
scope:
  - runner
  - control-plane
interface-impact: new
---

# ADR-0044: Rerun a failed run (continue / restart / discard)

## Context and problem statement

When a run fails (a node is dead-lettered after retries), the run is marked
`failed` and there is no way to continue it: the operator must start a brand-new
run from the top, re-running already-completed nodes and redoing their side
effects (unsafely, for nodes without a `dedupe_key`). The secret-invalidity
model (SPEC: Secret invalidity and rotation) already specifies that a failed run
should be resumable from the failed node, with a configurable "run it again"
behavior. That behavior was not built because it depends on the suspend/resume
machinery, which is now in place (ADR-0040).

## Decision drivers

- Resume from the failed node, leaving completed nodes' results and side effects
  as they are, rather than re-running the whole DAG (SPEC: Secret invalidity and
  rotation).
- The behavior is configurable: continue from the failed node, restart from the
  top, or discard the failed run.
- Reruns should be triggerable not just by a human on the CLI but by another
  workflow (agent-first), without a new network surface.
- Best Simple System for Now (ADR-0002): reuse the DAG-shaped continuation
  machinery and the existing trigger/callback wiring rather than build new
  machinery.

## Considered options

- **Save the failed node's self-contained `NodeJob` as a continuation, and add a
  `servitor rerun` CLI command plus a `rerun-failed` node (chosen).** When a run
  dead-letters, the failed node's `NodeJob` (its input and downstream sub-DAG)
  is stored durably. A rerun resumes it:
  - `continue` re-enqueues the failed node with its original input, reusing the
    DAG-shaped continuation (ADR-0040). The default, and the safe choice for
    secret invalidity.
  - `restart` re-runs from the top by rebuilding the run through the existing
    StartRun path for the same run id, resetting the dependency counts and
    pending count. Redoes completed side effects, so it is only safe when
    side-effecting nodes declare `dedupe_key`.
  - `discard` drops the saved continuation, leaving the run `failed`.
- **A dedicated resume webhook.** Rejected: it adds a network surface that the
  named-signal / webhook-triggered-broker pattern already covers without one.
- **Overload the `completed`/`failed` triggers to resume.** Rejected: those
  triggers start a run; resuming a failed run is run-id-scoped and stays a
  distinct operation (ADR-0042 kept them run-starting for the same reason).

## Decision outcome

Chosen option: **save the failed node as a continuation and rerun it via the CLI
and a `rerun-failed` node.**

Rerun is **general**: it applies to any failed run, whatever the cause of the
failure (an auth/secret failure, a transient 5xx, a shell command failing, or
any node dead-lettering after retries). The SPEC's resume-from-failure was
written narrowly around secret invalidity; the mechanism generalizes cleanly to
any dead-lettered node, and `continue` is most valuable for the auth case
because re-running completed side effects is wasteful or unsafe. (A separate
idea, "Failure modes and rerun policy", considers whether the failure *cause*
should drive the rerun mode automatically; that is not part of this decision.)

When a node is dead-lettered (retries exhausted), the runner stores that node's
self-contained `NodeJob` in a durable `failed_continuations` row before marking
the run `failed`. Then `servitor rerun <run-id> [--mode ...]` or a `rerun-failed`
node resumes it:

- **continue** (default): re-enqueue the saved failed node with its original
  `{event, steps}` input. The completed nodes' results stay in `node_results`;
  only the failed node and its remaining successors run.
- **restart**: rebuild the run from the top for the same run id (the existing
  StartRun path), resetting dependency and pending counts. Safe only when
  side-effecting nodes declare `dedupe_key`; the validator's existing
  `missing_dedupe_key` warning covers this.
- **discard**: drop the saved continuation, leaving the run `failed` (a
  terminal "not resuming" state).

The mode resolves as CLI flag > `rerun-failed` node's `mode` > per-Wafer
`on_failure` field > default `continue`. There is no global servitor config file
yet, so the global-config layer is deferred until one exists (per BSSN). A
`rerun-failed` node re-runs the run named by its `run_id` JSONata expression,
defaulting to `event.from_run` (the id of the failed run whose `failed` trigger
started the workflow), so the common watcher case needs no config. Reruns are
triggerable by a workflow via a `rerun-failed` node, and by an external system
via an ordinary webhook to a broker workflow, reusing the existing webhook
surface and the same pattern as named signals (ADR-0042).

### Consequences

- Good: resume from the failed node without redoing completed side effects.
- Good: a workflow can re-run a failed run itself (agent-first), with no new
  network surface.
- Good: reuses the DAG-shaped continuation and StartRun paths.
- Bad: a failed run now stores an extra continuation row until rerun or discard.
- Bad: `restart` redoes completed side effects unless `dedupe_key` guards them.
- Neutral: a new CLI command, a Wafer node type, and a `on_failure` Wafer field.

### Confirmation

`go test ./...` passes. Tests assert that a dead-lettered node stores its
continuation; that `continue` re-enqueues only the failed node and its
successors (completed results untouched); that `restart` re-runs from the top;
that `discard` drops the continuation and leaves the run `failed`; and that
mode resolution is flag > Wafer > default.

## Interface notes

New surface. CLI: `servitor rerun <run-id> [--mode continue|restart|discard]`.
Wafer schema: a top-level `on_failure` field (continue/restart/discard) and a
`rerun-failed` node type (with a `run_id` expression defaulting to
`event.from_run`). Daemon protocol: a `/v1/rerun` operation. Existing commands,
triggers, and nodes are unchanged.

## More information

- SPEC: Secret invalidity and rotation
- ADR-0040 (the suspend/resume machinery this reuses), ADR-0042 (named signals
  and the trigger-keeps-starting principle), ADR-0039 (the `failed` trigger)
- IDEAS.md
