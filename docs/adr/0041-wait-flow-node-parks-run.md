---
status: accepted
date: 2026-08-29
decision-makers: [Mathias]
consulted: []
informed: []
scope:
  - runner
  - control-plane
interface-impact: new
---

# ADR-0041: A `wait` flow node that parks a run

## Context and problem statement

Servitor has action nodes (they do work) and flow nodes (`switch`, `foreach`,
which route and fan out). The long-lived use cases (approval waits, timed holds,
saga compensation, external-callback waits) need a node that pauses the run and
resumes later, between nodes, using the suspend/resume machinery of ADR-0040.

The canonical durable-wait is an approval with a deadline: wait for an external
signal but auto-continue (or auto-fail) when a grace period expires, with the
downstream flow able to tell which happened. That is one wait, not two.

## Decision drivers

- A single primitive should express "wait for the first of {a timer, a signal}"
  (the approval-with-deadline shape), not force the author to build it from two
  nodes.
- The node stays a dumb primitive: a small fixed result, no reshaping of its own.
  Authors add shape with `transform` and `switch`, as everywhere else.
- The node is a flow node (it does no external work), consistent with `switch`
  and `foreach`, and runs as a subprocess like every node (ADR-0008).
- Best Simple System for Now (ADR-0002): no durable-execution engine; the node
  just parks the run via ADR-0040.

## Considered options

- **A single `wait` node with both a timer and a signal, resolving on whichever
  fires first (chosen).** `timer` and `signal` are optional sub-fields; a
  timer-only wait is a timed hold, a signal-only wait is "wait until told", and
  both is the approval-with-deadline case. The result records which source
  resolved it, so a downstream `switch`/`transform` routes on the outcome.
- **Separate `wait-timer` and `wait-signal` node types.** Rejected: expressing
  "whichever fires first" from two nodes requires extra control flow, and the
  approval-with-deadline case becomes awkward, unlike the Temporal primitive it
  is meant to match.
- **A wait that re-enters a node's computation.** Rejected: it violates the
  subprocess-per-node model (ADR-0008); a node runs to completion and exits.

## Decision outcome

Chosen option: **a single `wait` flow node with optional `timer` and `signal`
sources, resolving on whichever fires first.**

```yaml
- name: await_approval
  type: wait
  signal: approval_gate.${event.order_id}   # optional; see ADR-0042
  timer:
    after: 48h                              # optional; see ADR-0043
```

The node's result is a small, fixed shape that the downstream flow routes on:

```
{source: "signal" | "timer", payload: <the signal payload, or null on timer>}
```

- `source` is `"signal"` when a signal resolved the wait and `"timer"` when the
  timer did. It is `"timer"`, not `"timeout"`: a pure timed hold is not a
  failure, and a downstream `switch` reads `source: "timer"` as "the deadline
  elapsed".
- `payload` is always present and `null` when the timer fired.
- The signal payload is opaque data: whatever the sender passed, threaded
  forward as the wait node's step result. It is not re-injected as a new run
  `event`, and the wait node does not interpret it. This keeps the single
  `{event, steps}` input shape (ADR-0021) with no second plumbing for signals.

A following `switch`/`transform` branches on `steps.<wait>.source` and extracts
`steps.<wait>.payload`, exactly as with any other node's result.

### Consequences

- Good: the approval-with-deadline case is one node, matching the Temporal
  primitive it is modeled on.
- Good: the node stays dumb; complexity lives in `transform`/`switch`.
- Good: timer-only and signal-only waits fall out as the two degenerate cases.
- Bad: the `wait` node carries two optional sources, so a node with neither is a
  validation error (it would park forever).
- Neutral: adds a flow-node type and the `waiting` run status to the inspector.

### Confirmation

`go test ./...` passes. Tests assert that a `wait` parks the run (ADR-0040) and
resolves on whichever of timer and signal fires first; that a timer-only wait
reports `source: "timer"` with `payload: null`; that a signal-only wait reports
`source: "signal"` with the payload; and that a wait with neither `timer` nor
`signal` is rejected by validation.

## Interface notes

New Wafer schema: a `wait` node type with optional `timer` (`after` / `at`) and
`signal` fields, and the `{source, payload}` result shape under `steps.<name>`.
New run status `waiting`, shown in `servitor runs` and `servitor run <id>`.
`servitor cancel` drops parked continuations. Existing nodes and triggers are
unchanged.

## More information

- SPEC: Nodes (flow nodes), Execution model, Control plane
- ADR-0040 (the suspend/resume machinery), ADR-0042 (named signals),
  ADR-0043 (the timer mechanism)
- IDEAS.md "Suspended waits"
