---
status: accepted
date: 2026-08-25
decision-makers: [Mathias]
consulted: []
informed: []
scope:
  - runner
  - wafer
  - execution-model
interface-impact: new
---

# ADR-0024: A `foreach` step fans out a named body step and collects results as an array

## Context and problem statement

Workflows need to run a step once per element of a list (a map/fan-out-over-a-
list primitive). The `foreach` step type is registered with only `over` (the
list) and `as` (a loop-variable name) and is unimplemented. The execution model
is now a dependency DAG with dependency-counter fan-out (ADR-0023): a step runs
when all its dependencies are satisfied, and a step can fan out to multiple
dependents and rejoin via fan-in. We need to decide which step `foreach` fans
out, how each iteration sees its element, and how a downstream step consumes the
iteration results.

## Decision drivers

- Keep the flat-list Wafer model: steps are named top-level entries (SPEC: The
  Wafer), the same shape that drove the switch design (ADR-0022).
- The `{event, steps}` input contract (ADR-0021) must stay intact: prior results
  are keyed by step name.
- A `foreach` fans out to N iterations of a body step, reusing the
  dependency-counter fan-out (ADR-0023) rather than new machinery.
- Best Simple System for Now: one collection shape that matches the dominant
  convention and is easy for agents to read.
- A rejoin consumer must fire once, after all iterations complete, not before.

## Considered options

- **Name a body step, collect results as an array under the foreach step's name
  (chosen).** `foreach` names a top-level body step (like `switch` names
  branches) and an `over` expression (JSONata over its `{event, steps}` input)
  yielding the list. Each iteration runs the body with input
  `{event, steps, <as>: element}`. A rejoin that `depends_on` the body step
  collects the N results as an array at the fan-in point: `steps[<foreach-name>]`
  is the array of per-iteration body results, in input order. This matches the
  dominant convention (Argo `outputs.result`, AWS Map `ResultPath`, Dagster
  `.collect()`, Airflow's mapped list, n8n's combined `done` output).
- **Inline body with per-iteration named outputs (Kestra-style).** The body is
  nested inline and outputs are keyed by the loop value
  (`outputs.<task>[value]`). Rejected: it breaks the flat-list model and forces
  consumers to know per-iteration keys; only nested-tree systems use it.
- **Self-contained threaded merge (no store read).** Thread each iteration's
  result forward and merge into the array in the queue. Rejected: none of the
  surveyed systems do this, and it is the most complex and error-prone option
  (ordering an array of N concurrent, out-of-order results). It can be added
  later behind the same seam if the store read ever matters.
- **A rejoin depending on the foreach step itself.** Rejected: the foreach step
  is a scheduler that completes before the iterations run, so the rejoin would
  fire too early.

## Decision outcome

Chosen option: **`foreach` fans out a named body step and collects results as
an array under the foreach step's name.**

A `foreach` step has `over` (a JSONata expression over its `{event, steps}`
input yielding the list, per ADR-0020), `as` (the loop-variable name, which
becomes a third key in each iteration's input), and `body` (the name of a
top-level step to run once per element). It is a scheduler step like `switch`:
it does no subprocess work itself. When it runs, it resolves `over` to a list
and enqueues one body-step job per element, each with input
`{event, steps, <as>: element}`. The N body jobs are the fan-out.

A rejoin consumer `depends_on` the body step (not the foreach step). The
dependency-counter fan-in waits for all N iterations and fires the rejoin once.
At that point the worker reads the N committed body results and assembles
`steps[<foreach-name>]` as the array of per-iteration results in input order
(collect-at-rejoin). This is a scoped data read for aggregation, not a
control-flow lookup, and it is the dominant convention (Argo, AWS, Dagster,
Airflow all aggregate at the collection point).

The `as` value is validated to not collide with `event` or `steps`. The loop
variable is exposed as `item` (or whatever `as` names) so a body step can
reference its own element.

### Consequences

- Good: reuses the flat-list, named-step model and the dependency-counter fan-out
  (ADR-0023); consistent with `switch` (ADR-0022).
- Good: the array-of-results shape is the dominant convention and reads naturally
  for agents: `steps[<foreach-name>]` is "the list I fanned out over, after
  processing."
- Good: a rejoin fires exactly once after all iterations complete.
- Bad: the collect-at-rejoin read breaks the strict "reads nothing from the
  store" phrasing of ADR-0023, but only as a scoped data read for aggregation at
  the foreach fan-in; control flow stays self-contained. The carve-out is
  documented in ADR-0023.
- Neutral: iterations run in parallel by default; a `concurrency`/`maxParallel`
  throttle is deferred until a real workflow needs it (BSSN).
- Neutral: this is the first store read in the worker, isolated behind a small
  helper so it can be swapped for a self-contained merge later if ever needed.

### Confirmation

`go test ./...` passes. Tests pin: a `foreach` enqueues one body job per element,
each with `{event, steps, <as>: element}`; a rejoin `depends_on` the body fires
once after all iterations and reads `steps[<foreach-name>]` as the ordered array
of results; and the array is in input order regardless of completion order.

## Interface notes

Additive to the Wafer schema: the `foreach` step type gains a required `body`
field (the name of a top-level step to fan out), plus the existing `over` and
`as` fields. Each body iteration's input gains a third key (`<as>`) for the loop
element. No change to the CLI surface or the daemon control protocol.

## More information

- ADR-0022 (switch, named branches; the same named-reference convention)
- ADR-0023 (dependency-counter fan-out; the self-containment carve-out)
- ADR-0021 (`{event, steps}` input)
- ADR-0020 (JSONata expressions)
- SPEC: Step types (universal primitives), Execution model
