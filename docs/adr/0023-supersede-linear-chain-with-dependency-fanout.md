---
status: accepted
date: 2026-08-25
decision-makers: [Mathias]
consulted: []
informed: []
scope:
  - runner
  - execution-model
interface-impact: none
---

# ADR-0023: Supersede the linear step chain with dependency-counter fan-out

## Context and problem statement

Workflows need conditional routing: a `switch` step picks one branch of steps to
run, and a rejoin step runs only after the branches it depends on complete. The
execution model currently runs every workflow as a self-contained linear step
chain (ADR-0012): each step's `Downstream` is the next step, and on completion
the worker enqueues all of it in one atomic transaction. A linear chain cannot
express "run one of several branches" or "wait for all predecessors." This
supersedes the linear-chain model with dependency-counter fan-out.

## Decision drivers

- A switch step (ADR-0022) must enqueue only its chosen branch, not a linear
  next step.
- A rejoin step must run only after all the branches it depends on complete
  (fan-in).
- The SPEC's no-split transactional rule (SPEC: Execution model step 8) is
  non-negotiable: a dependent is enqueued exactly when its last dependency
  completes, in the same atomic commit as that completion.
- Best Simple System for Now: reuse the existing `run_deps` primitive and the
  atomic `CommitStepAtom`, not a separate scheduler.

## Considered options

- **Dependency-counter fan-out (chosen).** Maintain a per-run count of
  unsatisfied dependencies in a `run_deps` table. On a step's completion,
  decrement each dependent's count and enqueue only those that hit zero, inside
  the same `CommitStepAtom` transaction as the result, dedupe record, and claim
  ack. This is the design ADR-0012 itself anticipated as its deferred
  alternative.
- **Keep the linear chain (rejected).** Cannot express switch branches or
  fan-in rejoin; a linear chain would run both branches or miss the rejoin.
- **Separate scheduler watching for completions (rejected).** Adds a coordinator
  process and races with the atomic commit, violating the "no separate
  scheduler" model (SPEC: Execution model step 8).

## Decision outcome

Chosen option: **dependency-counter fan-out**, superseding the linear step chain
of ADR-0012.

A run is built from the Wafer as a dependency DAG, not a linear chain. The
`run_deps` table holds, per run and step, how many of a step's dependencies are
still unsatisfied. When a step completes, the worker decrements the count for
each of the step's dependents and enqueues only those whose count reaches zero,
all within the one `CommitStepAtom` transaction. A switch step (ADR-0022)
resolves to a single chosen branch and enqueues that branch's ready steps; a
rejoin step is enqueued only when all its branch predecessors complete. A run
completes when its last step finishes and no step remains pending.

A linear chain is the degenerate case of this model (each step depends on at
most the previous), so existing linear workflows keep working.

The worker stays self-contained in its control flow: it has no workflow registry
and reads nothing from the store to decide what to run next (a principle
inherited from ADR-0012 and preserved here). To keep that, skipped branches are
carried in the switch job's payload as skip-jobs rather than having the worker
look up the run's DAG. A skip-job is an ordinary queued job marked to be
skipped: it records the step as skipped and cascades to its own dependents
without executing. This means a skipped branch's propagation to a rejoin happens
through the normal queue and dependency-counter mechanism, so the worker still
needs no registry or DAG read for control flow. The tradeoff is a larger switch
payload (the skipped-branch chains ride with the switch job), which is bounded
by workflow size and acceptable at this scale (BSSN).

This self-containment is about control flow, not about never reading the store:
a `foreach` step collects its iteration results at the fan-in point by reading
the committed results (ADR-0024). That is a scoped data read for aggregation,
not a control-flow lookup; the worker still knows what to run from the job it
was given. The distinction is deliberate: control flow stays self-contained,
while a single, contained data read for aggregation is accepted where it is the
simpler and correct option (ADR-0024).

## Skipped-branch and completion semantics

When a `switch` step (ADR-0022) picks one branch, the non-chosen branches'
steps never run. Their semantics are settled as follows, matching how the
established systems treat skipped branches (Argo and GitHub Actions treat a
skipped step as terminal and satisfied; Airflow's rejoin rule is
`none_failed_min_one_success`).

- **A skipped step is satisfied (Argo-style).** A step in a non-chosen branch is
  marked satisfied and counts as done for dependency purposes, so the run
  completes uniformly and never deadlocks waiting on a branch that did not run.
  A skipped step is not a failure and does not fail the run.
- **A skipped branch contributes nothing to a rejoin's input.** The skipped
  branch's steps are absent from the `{event, steps}` input of a rejoin step,
  never present as a null value (a null would be indistinguishable from a branch
  that legitimately returned null). The fact that a branch was skipped lives on
  the step's status, not in the data.
- **The switch's output carries the chosen branch.** If a downstream consumer
  needs to know which branch ran, it reads it from the switch step's own result,
  not from the absent branch bodies.
- **No arity special-casing.** If/else is a two-case switch and behaves
  identically to a multi-way switch: exactly one branch runs, the rest are
  skipped, and a rejoin fires once the chosen branch satisfies the counter.
- **Every step records a status** (`ran` / `skipped` / `failed`) so the run's
  final report distinguishes skipped from failed from actually-ran. A skipped
  step is treated as satisfied for the counter but is recorded separately, so it
  is not conflated with a success (avoiding the confusion Argo's "skipped is
  successful" causes).
- **The all-skipped edge case runs the rejoin.** If a rejoin step's only feeder
  was skipped, the rejoin still runs (Argo-style) rather than being skipped
  itself. This is pinned by a test. Making this selectable is deferred until a
  real workflow needs it (BSSN); if one does, it becomes a new decision, not
  speculative scaffolding.

### Consequences

- Good: switch branches and fan-in rejoin are expressible, enabling conditional
  routing.
- Good: the no-split transactional rule is preserved by construction, since the
  decrement, readiness check, and enqueue all commit together with the result
  and ack.
- Good: a linear chain is the degenerate case, so existing workflows are
  unaffected.
- Bad: adds a `run_deps` table and transactional bookkeeping to the commit path.
- Neutral: run completion is no longer "no downstream"; it is derived from the
  dependency counters (no step left pending).

### Confirmation

`go test ./...` passes. Tests pin: a switch enqueues only its chosen branch; a
rejoin step runs only after all its branch predecessors complete; a skipped
branch counts as satisfied and is absent from a rejoin's input; the all-skipped
rejoin edge case runs; a linear chain still runs end to end; and the
decrement/readiness/enqueue happen in one atomic commit (a failure rolls back
the dependent enqueue).

## Interface notes

No change to the Wafer schema, CLI surface, or daemon control protocol. The
`run_deps` table, the `StepJob` payload, and the worker/runner packages are
internal. The additive `switch` step type is covered by ADR-0022.

## More information

- Supersedes ADR-0012 (self-contained linear step chain), whose "dependency-
  counter fan-out" considered option this implements.
- ADR-0022 (switch step that routes to one named branch)
- ADR-0021 (`{event, steps}` input, threaded with the job)
- SPEC: Execution model step 8 (the no-split transactional rule)
