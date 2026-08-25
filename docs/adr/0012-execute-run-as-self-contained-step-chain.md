---
status: superseded
date: 2026-08-24
decision-makers: [Mathias]
consulted: []
informed: []
scope:
  - runner
  - execution-model
interface-impact: none
---

# ADR-0012: Execute a run as a self-contained, sequential step chain

## Context and problem statement

The worker loop executes steps (SPEC: Execution model). A workflow (Wafer) is a
dependency DAG of steps, but the worker has no workflow registry to consult: it
processes one claimed job at a time, and the SPEC says there is no separate
coordinator that watches for completions, the finishing worker performs the
fan-out itself. We need a shape for "what a queued job carries" and for "which
steps to enqueue when a step completes" that the worker can act on without
shared state.

## Decision drivers

- Every step runs as a subprocess in its own process; the worker must not reach
  into a registry or database to learn what to run (ADR-0008).
- The fan-out must commit atomically with the step's result, dedupe record, and
  claim ack through the existing `honker.CommitStepAtom` primitive.
- Best Simple System for Now: no speculative machinery for parallelism that the
  current workloads do not need.

## Considered options

- **Self-contained step chain (chosen).** Each queued job (a `StepJob`) is fully
  self-contained: it carries the step's definition, command, declared secrets,
  and its successor step(s). A run is built once from the Wafer as a single
  topological-order chain, each step's `Downstream` being the next step in run
  order. Because run order respects dependencies, a step always executes after
  the steps it depends on, and no step is enqueued twice. The worker needs no
  registry: it runs the job it was given and enqueues the job's successors.
- **Dependency-counter fan-out.** Maintain a per-run count of unsatisfied
  dependencies in the store; on completion, decrement dependents and enqueue
  those that hit zero. Enables parallel and multi-dependency fan-out, but adds
  a `run_deps` table, run-registration, and transactional bookkeeping that must
  stay in the same commit as the ack.
- **Shared workflow registry.** The worker looks up the workflow definition by
  run id. Adds a registry/submit path and a lookup on every step, against the
  "no in-process state, no separate scheduler" model.

## Decision outcome

Chosen option: **self-contained step chain.**

A run is a linear chain of `StepJob`s built from the Wafer in topological order.
Each job carries its successor, so the worker, on success, enqueues exactly its
`Downstream` in the same `CommitStepAtom` transaction as its result, dedupe
record, and ack. There is no registry and no per-run bookkeeping table. The run
id is propagated to every job in the chain at build time.

### Consequences

- Good: the worker is stateless per job; it needs no registry and no shared
  state, matching the "no separate coordinator" execution model.
- Good: the fan-out stays inside the one `CommitStepAtom` transaction, so the
  SPEC's no-split rule is preserved by construction.
- Good: any acyclic workflow runs correctly, because topological order respects
  dependencies.
- Bad: independent branches execute sequentially rather than in parallel, and a
  step with multiple dependencies still runs only after all of them (correct,
  but not maximally concurrent). Parallel fan-out and the dependency-counter
  bookkeeping that enables it are deferred until a workload needs them.
- Neutral: the `Downstream` payloads duplicate the run's step definitions, which
  is acceptable at this scale and is exactly what keeps the worker self-contained.

### Confirmation

`go test ./...` passes. The worker tests pin the behavior: a step's successors
are enqueued in the same transaction as its ack (`TestExecuteShellStepCommitsFanOut`),
and the end-to-end test (`TestRunEndToEnd`) runs a two-step chain to completion
through the worker loop.

## Interface notes

No change to the Wafer schema, CLI surface, or daemon control protocol. The
`StepJob` payload and the `worker`/`runner` packages are internal.

## More information

- SPEC: Execution model (steps 6-8), Step execution (ADR-0008)
- ADR-0004 (adopt Go; the subprocess model this builds on)
