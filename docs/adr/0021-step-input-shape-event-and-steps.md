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

# ADR-0021: A step's input is `{event, steps}`, threaded forward with the job

## Context and problem statement

A downstream step needs the outputs of earlier steps (SPEC: Step types, a
`transform` reshapes "previous steps' JSON output"), and `dedupe_key` is
evaluated "against the step's inputs". The worker has no workflow registry and
processes one claimed job at a time (ADR-0012), so we must decide the shape in
which a step receives the trigger event and prior results, and how that shape
travels from a completing step to its successors. Today only the run's head step
receives the trigger event as its input; every downstream step's input is empty,
so no step can currently reference a prior step's output.

## Decision drivers

- The worker stays self-contained and stateless per job (ADR-0012): it needs no
  registry or shared-state read to run the job it was given. Data a step needs
  should ride with the job.
- Crash safety: a step's input must never disagree with the committed results it
  depends on.
- One input shape serves every step type and both `transform` and `dedupe_key`
  from the same evaluator (ADR-0020).
- A step should not casually see every other step's output; the input should be
  keyed by step name so it can be scoped to what a step needs (least privilege),
  even though the current linear chain passes the full accumulated context.

## Considered options

- **Thread `{event, steps}` forward with the job (chosen).** The worker builds
  a downstream job's input at commit time from the completing step's input plus
  its result, and the result is written into the job's payload inside the same
  atomic transaction as the step's result (SPEC: Execution model step 8). The
  trigger event is carried in `event`; prior step results are keyed by step name
  in `steps`. A step references `event.<x>` or `steps.<name>.<y>`.
- **Read prior results from SQLite by run id at execution.** The worker queries
  `step_results` for the run and assembles the input on demand. Avoids
  re-serializing the accumulated map through the queue, but breaks strict
  self-containment (adds a store read per step), requires persisting the trigger
  event per run (the `runs` table has no payload today), and would make it easy
  to hand a step every result rather than only what it needs.
- **Flat `{step: result}` map (no separate `event`).** Simpler envelope, but
  merges the durable trigger payload with computed results, losing the
  distinction several workflow engines (Kestra, AWS Step Functions JSONata)
  preserve between the original event and step outputs.
- **Immediate predecessor only.** The step sees only its direct predecessor's
  result. Breaks as soon as a step needs more than one prior output; the mature
  systems (AWS, Kestra, n8n, GitHub Actions) all moved away from this toward
  named reach-back.

## Decision outcome

Chosen option: **thread `{event, steps}` forward with the job.**

Every step's input is an object with two keys: `event` holds the durable trigger
payload (the event that started the run), and `steps` is a map of prior step
results keyed by step name. A `transform` step's JSONata expression, and a
`dedupe_key` expression, are evaluated against this object. The head step's
input is `{event: <trigger payload>, steps: {}}`.

The input is threaded forward: when a step completes, the worker constructs each
downstream job's input from the completing step's input plus the completing
step's result under its step name, and that input is serialized into the
downstream job's payload. Because the downstream enqueue and the step's result
are written in the same atomic commit (SPEC: Execution model step 8), a step's
input can never disagree with the committed result it was built from. The worker
remains self-contained: it reads nothing from the store to run a job.

The full accumulated `{event, steps}` map is threaded for now. Scoping a step's
input to only its declared `DependsOn` (least privilege) is the noted refinement
and becomes natural once real DAG fan-out arrives; the keyed `steps` shape is
what makes that refinement possible.

### Consequences

- Good: the worker stays stateless and self-contained (ADR-0012); no registry,
  no per-step store read, and crash safety is by construction since input and
  result commit together.
- Good: `transform` and `dedupe_key` share one evaluator and one input shape
  (ADR-0020), so `event.id` and `steps.tap.state` both work.
- Good: the trigger event stays distinct from computed results, matching the
  separation the mature workflow systems converged on.
- Good: `singer-target` can reference its upstream tap's records explicitly as
  `steps.<tap>.records`, fixing the current gap where downstream steps receive
  no input at all.
- Bad: the accumulated `{event, steps}` map is re-serialized into the Honker
  queue at each step, growing linearly with chain length. Bounded by workflow
  size and acceptable at this scale (BSSN); the keyed shape leaves room to scope
  to dependencies later if it ever matters.
- Neutral: changes the head step's input from the raw event to the wrapped
  `{event, steps}` object, and moves `singer-target`'s records access from the
  top-level `records` to `steps.<tap>.records` (the current code is broken
  anyway, since no downstream step receives input).

### Confirmation

`go test ./...` passes. Tests pin: a `transform` step evaluates its JSONata
expression against the `{event, steps}` input and returns the result; a
`singer-target` reads records from `steps.<tap>.records`; and the runner threads
a prior step's result into its downstream job's input under the step's name.

## Interface notes

Additive to the Wafer schema: the `transform` step's `expression` field is
defined to be a JSONata expression over the `{event, steps}` input (already
settled by ADR-0020). This ADR defines the input contract every step receives:
`{event, steps}`. No change to the CLI surface or the daemon control protocol.

## More information

- ADR-0020 (JSONata via gnata as the step expression language)
- ADR-0012 (self-contained step chain, the model this threads data through)
- ADR-0008 (subprocess-per-step isolation; secret isolation lives at the
  subprocess boundary, not in how a step's input is shaped)
- SPEC: Execution model step 8; Step types (transform); Idempotency (dedupe_key)
- Discussion: survey of how workflow systems shape downstream input (Kestra
  `outputs.<task>`, AWS Step Functions JSONata `$states.input`/variables, n8n
  `$('node')`, GitHub Actions `steps.<id>.outputs`), and of the tradeoff between
  threading data through the queue versus reading it back by run id.
