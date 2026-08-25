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

# ADR-0022: Add a `switch` step that routes to one named branch

## Context and problem statement

Workflows need conditional routing: based on data, run one branch of steps or
another. The existing `branch` step type is registered but unimplemented, and
the execution model runs a workflow as a self-contained linear step chain
(ADR-0012): each job's `Downstream` is the next step, and on completion the
worker enqueues all of it in one atomic transaction. We need a way to express
"run one of several branches based on a value."

## Decision drivers

- Keep the flat-list Wafer model: steps are named top-level entries with
  `depends_on` (SPEC: The Wafer). Do not introduce a nested subtree.
- Reuse the existing name-reference and dependency machinery; branch bodies are
  ordinary steps.
- The branch decision must happen where the data is: at execution time, when the
  step's `{event, steps}` input is known (ADR-0021).
- Best Simple System for Now: one routing primitive that also expresses
  if/else (as a two-case switch), rather than a separate gate.

## Considered options

- **A `switch` step with value-matched named targets (chosen).** The switch
  evaluates a JSONata `expression` over its `{event, steps}` input to a value,
  matches it against a `cases` map of value → target step name, and enqueues
  only the named target's job. An optional `default` covers an unmatched value.
  Branches are ordinary top-level steps referenced by name (Design B).
- **A gate `branch` (condition false skips downstream).** Rejected: a pure gate
  only covers "run this or skip," not the common "run A or run B" case (Grist
  approval escalation, lead/PR notification routing). The use-case analysis of
  the intended integrations favors routing.
- **If/else routing (`then:`/`else:`).** A special case of switch; the general
  multi-way switch subsumes it (a two-case switch), matching how the routing
  systems (AWS Step Functions Choice, n8n Switch, Kestra Switch) generalize.
- **Inline nested branches (Design A).** Rejected: the research showed that
  systems with a flat list of named entities (Step Functions, Argo, GitHub
  Actions, Airflow, n8n) all use named top-level branch targets, while only
  nested-tree systems (Kestra, Digdag) use inline branches. Servitor is a flat
  list, so named targets are the consistent choice and reuse `depends_on`.

## Decision outcome

Chosen option: **a `switch` step that routes to one named branch.**

A `switch` step has an `expression` field (JSONata, over the step's `{event,
steps}` input) and a `cases` field: a map from value to the name of a top-level
step. The worker evaluates `expression`, matches the result against the `cases`
keys, and enqueues the job for the named target step only. An optional
`default` field names the step to enqueue when no `cases` key matches. Branch
bodies are ordinary top-level steps elsewhere in the Wafer that the switch
routes to; they may themselves have `depends_on`.

Because a switch enqueues exactly one named target rather than the linear next
step, it is the first step type that routes conditionally. If/else is expressed
as a two-case switch (for example `{true: ..., false: ...}`), so no separate
`branch` or gate is needed.

### Consequences

- Good: reuses the flat-list, named-step, `depends_on` model; no nested subtree,
  no new namespace.
- Good: one primitive covers both multi-way routing and if/else; agents express
  either naturally.
- Good: the branch decision happens at execution time against the `{event,
  steps}` input, where the data lives (ADR-0021).
- Bad: breaks the strict linear-chain assumption (ADR-0012): the worker's
  "enqueue all downstream" becomes "enqueue the chosen target." This is the
  first conditional enqueue and needs the switch to know its possible targets.
- Neutral: the full case of a switch isn't visible in one place (the branch
  bodies are elsewhere in the file), the known tradeoff of the named-target
  model.

### Confirmation

`go test ./...` passes. Tests pin: a `switch` evaluates its expression against
the `{event, steps}` input and enqueues exactly the named target step; an
unmatched value falls back to `default` (or runs nothing if absent); and the
chosen branch's job carries the threaded `{event, steps}` input. The `branch`
step type is replaced by `switch`.

## Interface notes

Additive to the Wafer schema: a new step type `switch` with `expression`,
`cases` (map of value → step name), and optional `default` (a step name). The
unimplemented `branch` step type is removed. No change to the CLI surface or the
daemon control protocol.

## More information

- ADR-0012 (self-contained linear step chain; this is the first conditional
  enqueue on top of it)
- ADR-0020 (JSONata as the step expression language)
- ADR-0021 (`{event, steps}` input, threaded with the job)
- SPEC: Step types (universal primitives), Execution model step 8
- Discussion: survey of how routing systems place branch bodies (flat-list
  systems use named top-level targets; nested-tree systems use inline branches),
  and of multi-way switch as the general primitive subsuming if/else.
