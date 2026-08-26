---
status: accepted
date: 2026-08-26
decision-makers: [Mathias]
consulted: []
informed: []
scope:
  - wafer
  - registry
  - capabilities
interface-impact: breaking
---

# ADR-0030: Node is the body primitive; action and flow are node kinds; triggers are not nodes

## Context and problem statement

Servitor's vocabulary for what a workflow does has drifted across several
renames. ADR-0028 unified every capability as a single "step type" with a role
(trigger, action, or both). But "step" remained ambiguous: it named both the
unified primitive and, in the executed sense, a mid-run unit. A survey of
workflow systems (n8n, Kestra, Step Functions, GitHub Actions, Zapier, and
others) showed the mainstream treats the thing that starts a run as a separate
category from the things that do work mid-run. Also, `switch` and `foreach` are
control-flow, not work; calling them "actions" reads wrong. The vocabulary and
the model did not line up.

## Decision drivers

- A trigger starts a run; it is not a node in the run's DAG. Resolving the DAG
  builds only from the body, and triggers are registered resources (webhook
  paths, cron tasks), so calling a trigger a "node" or "step" is inaccurate.
- The body's entries are all graph nodes whether they do work or route, so one
  primitive must cover both.
- Control-flow (`switch`, `foreach`) does no external work; it should not be
  called an action.
- The word "step" is overloaded enough to keep causing the same confusion, so
  it is removed from the vocabulary entirely, including internally.

## Considered options

- **Node as the body primitive, with action and flow as node kinds (chosen).**
  The body slot is `nodes:`. Every node is either an action node (does work) or
  a flow node (routes or fans out). A trigger is a separate thing under `on:`,
  not a node. Internally the registry keeps one list of capabilities, each with
  a `Role` of `trigger`, `action`, or `flow`.
- **Rename the primitive to "node" and fold triggers in as a kind of node.**
  Rejected: a trigger is not a DAG node, so "trigger is a node" is less accurate
  than "trigger is not a node," and it reintroduces the trigger-is-a-step
  problem.
- **Keep "step" as the primitive.** Rejected: the ambiguity that motivated this
  whole line of decisions persists, and it would leave internal code and docs
  inconsistent with the user-facing "node" vocabulary.

## Decision outcome

Chosen option: **node is the body primitive; action and flow are node kinds;
triggers are not nodes.**

A Wafer declares triggers (`on:`) that start the run, and nodes (`nodes:`) that
are the run's DAG. Every node is an action node (does work: `http`, `shell`,
`transform`, `singer-tap`, `mcp-call`) or a flow node (routes or fans out:
`switch`, `foreach`). The registry keeps a single list of capabilities, each
with a `Role` of `trigger`, `action`, or `flow`; `nodes:` accepts action and
flow, `on:` accepts trigger. Capabilities output `kind: node` (with `role:
action` or `flow`) and `kind: trigger`. The word "step" is removed everywhere it
meant a workflow unit, including internal code (`StepJob` becomes `NodeJob`,
`StepType` becomes `NodeType`, and so on). The step input shape `{event, steps}`
(ADR-0021) is a separate, documented contract and is not part of this rename.

### Consequences

- Good: the format matches the model. "Action" is a kind of node, "flow" is a
  kind of node, and a trigger is neither.
- Good: `switch` and `foreach` are flow nodes, resolving the discomfort of
  calling them actions.
- Good: agents see `kind` and `role` in capabilities and know whether a
  capability starts a run or is an action or flow node.
- Good: internal code, error messages, and docs all use one consistent
  vocabulary ("node"), so an agent or programmer reasoning about the codebase
  does not have to translate between "step" and "node".
- Bad: it is a breaking change to the Wafer schema (`actions:` to `nodes:`),
  the error codes (`unknown_step_type` to `unknown_node_type`, `missing_actions`
  to `missing_nodes`), the error paths (`/actions/...` to `/nodes/...`), and the
  capabilities output shape. Accepted; the project is young and Wafers are small
  files.

### Confirmation

`go test ./...` passes. A registry test pins that a trigger capability is valid
under `on:` but not `nodes:`, that action and flow capabilities are valid under
`nodes:`, and that `switch`/`foreach` have `Role: flow`. A wafer test pins that
the parser reads `nodes:`, that `missing_nodes` is emitted when it is absent,
and that error paths are `/nodes/...`. A capabilities test pins `kind: node`
with `role: action|flow` and `kind: trigger`. A worker test pins that the job
type is named for nodes, not steps.

## Interface notes

Breaking for the Wafer schema, the emitted `capabilities` files, and internal
surfaces:

- Wafer schema: the body list key changes from `actions:` to `nodes:`. The `on:`
  key is unchanged. Migration: change the `actions:` key to `nodes:` in each
  Wafer; the contents of the list are unchanged.
- Error codes: `unknown_step_type` becomes `unknown_node_type`; `missing_actions`
  becomes `missing_nodes`. Error paths move from `/actions/...` to `/nodes/...`.
- Capabilities: node capabilities emit `kind: node` with `role: action|flow`;
  triggers emit `kind: trigger`.
- Internal: `StepType` becomes `Capability`, `Role` values become
  `trigger|action|flow` (dropping `both`), and `switch`/`foreach` become
  `Role: flow`. Worker execution types and methods (`StepJob`, `StepType`,
  `RunSteps`, the `steps` queue, the `step_dedupe` table) are renamed to the
  node vocabulary. The step input shape `{event, steps}` (ADR-0021) is unchanged;
  it is a different concept (the JSON a node receives, keyed by prior node
  names).

## More information

- ADR-0028 (a step type is one thing with a role) - superseded by this decision
- ADR-0021 (the `{event, steps}` input shape, unaffected by this rename)
- SPEC: The Wafer, SPEC: Nodes
