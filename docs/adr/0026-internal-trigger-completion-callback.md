---
status: accepted
date: 2026-08-25
decision-makers: [Mathias]
consulted: []
informed: []
scope:
  - runner
  - triggers
interface-impact: new
---

# ADR-0026: Fire the `internal` trigger from a run-completion callback

## Context and problem statement

The `internal` trigger fires a workflow when another workflow completes (SPEC:
Triggers, `internal`). The worker that detects completion is deliberately
self-contained and holds no workflow registry (ADR-0012), so it cannot know which
other registered workflows should be started, and it must not import the runner
or trigger packages (that would be an import cycle). The completion needs to
reach the component that owns the registered-workflow index.

## Decision drivers

- The worker must stay registry-free (ADR-0012): it executes self-contained step
  chains and should not learn about other workflows.
- The trigger receiver already owns the registered-workflow index and the
  run-building path (`runner.StartRun` via `Manual`), so it is the natural owner
  of "find workflows to fire".
- Best Simple System for Now (ADR-0002): a single completion hook is simpler than
  a poller or a separate registry read in the worker.

## Considered options

- **Completion callback wired by the daemon (chosen).** The worker gets an
  optional `OnRunComplete` hook, called when a run's pending count reaches zero.
  The daemon sets it to call the trigger receiver's `Internal` method, which
  lists registered workflows, finds enabled ones with an `internal` trigger
  naming the completed workflow, and starts each with an event describing the
  completion. The worker stays decoupled; the wiring lives where both sides are
  known (the daemon).
- **A separate poller loop** watching for completed runs and firing matches.
  More moving parts and a poll interval; the completion is already known at the
  moment the worker marks the run done, so a push is simpler.
- **Give the worker a registry read.** Couples the worker to the registered
  workflows and reopens the registry question ADR-0012 closed, for no benefit.

## Decision outcome

Chosen option: **completion callback wired by the daemon.**

The worker calls an optional `OnRunComplete(workflowID, runID)` hook after it
marks a run completed. The daemon wires it to the trigger receiver's new
`Internal(completedWorkflow, completedRun)` method. `Internal` walks the stored
registered workflows and starts each enabled workflow whose `internal` trigger
has a `workflow` config matching the completed workflow, passing an event of
`{trigger: "internal", from: <workflow name>, from_run: <completed run id>}`.

### Consequences

- Good: the worker stays registry-free and decoupled from the trigger package.
- Good: the trigger receiver owns the new trigger type, matching how it already
  owns webhooks and manual.
- Good: firing is a push at the exact moment of completion; no poll interval.
- Neutral: the event carries only the completing workflow and run id, not the
  upstream run's results. The downstream workflow gets context it can act on;
  threading the full upstream result is a possible later refinement.

### Confirmation

`go test ./...` passes. Tests pin the behavior: the worker's `OnRunComplete`
fires only when a run completes (`TestOnRunCompleteFiresWhenRunFinishes`,
`TestOnRunCompleteNotFiredForIncompleteRun`); the receiver fires only matching,
enabled workflows (`TestInternalFiresDownstreamWorkflow`,
`TestInternalIgnoresOtherWorkflow`, `TestInternalSkipsDisabledWorkflow`); and a
daemon end to end runs an upstream workflow and its internal-triggered
downstream (`TestDaemonInternalTrigger`).

## Interface notes

No change to the Wafer schema or the CLI: `internal` was already a registered
trigger type with a required `workflow` field. The daemon control protocol is
unchanged. Additive to the worker's `Config` (`OnRunComplete`), an internal
Go surface, and to the trigger receiver (a new `Internal` method).

## More information

- ADR-0012 (the worker holds no workflow registry; self-contained step chains)
- ADR-0013 (the trigger receiver owns the registered-workflow index)
- SPEC: Triggers, `internal`
