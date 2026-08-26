---
status: accepted
date: 2026-08-26
decision-makers: [Mathias]
consulted: []
informed: []
scope:
  - capabilities
  - registry
interface-impact: breaking
---

# ADR-0031: A mechanism group is a family of mechanisms; a capability is a mechanism

## Context and problem statement

ADR-0017 said "mechanism is the organizing principle" and grouped capabilities by
"mechanism", listing values such as `core`, `webhook`, `singer`, `mcp`, `helper`,
and `websocket`. Revisiting the model, that wording conflates two distinct
levels. `webhook` is not one mechanism; it is a *family* of inbound-HTTP
reception types (`grist_webhook`, `github_webhook`, `http_webhook`, ...). `singer`
is a family of record-streaming types (`singer-tap`, `singer-target`). So the
top-level organizing value is a **mechanism group** (a category of mechanisms),
and the individual capability types within it are the **mechanisms**.

## Decision drivers

- The top-level grouping value must be a category (a family of mechanisms), not
  a single mechanism, so the level is unambiguous.
- The mechanism a type speaks must stay legible to an authoring agent
  (ADR-0017's original driver), now with a precise name for the category.
- Best Simple System for Now: this is a terminology correction, not new
  machinery. It does not add mechanism groups for capabilities that do not yet
  exist.

## Considered options

- **Mechanism group as the top-level category; capability types as mechanisms
  (chosen).** The registry field is `MechanismGroup`; the values are `core`,
  `webhook`, `singer`, `mcp`, `helper`, and `websocket`. Each capability (a
  mechanism) belongs to one mechanism group. `capabilities` groups its output
  by mechanism group, and the index lists mechanism groups.
- **Keep "mechanism" as the top level (ADR-0017's wording).** Rejected: it
  conflates the category with the individual type, and leaves no clean word
  for "a family of mechanisms."
- **Name the field "Group".** Rejected: "group" is vague (group by what?); the
  field holds a mechanism *group*, so the name should say so.

## Decision outcome

Chosen option: **a mechanism group is a family of mechanisms; a capability is
a mechanism.**

The registry field is `MechanismGroup` (a category: `core`, `webhook`, `singer`,
`mcp`, `helper`, `websocket`). Each registered capability (a mechanism, for
example `grist_webhook`, `singer-tap`, or `mcp-call`) belongs to exactly one
mechanism group. `capabilities` groups its output by mechanism group, and
`index.yaml` lists the mechanism groups and the types each contains. This
supersedes ADR-0017's wording, which called the top level "mechanism"; the
grouping behavior is unchanged, only the term for the top level is corrected.

A mechanism group is added only when a capability in it actually exists. The
distinct ways a future secret provider might retrieve a secret (fetch from an
external store, decrypt on-box, unseal with TPM/vTPM, decrypt via an off-box
KMS, read from the environment) are mechanisms, and would belong to a
`secret` mechanism group; that group is added when the first such capability is
built, not before.

### Consequences

- Good: the two levels are now named precisely, so a future mechanism group
  (for example `secret`) has a clear home.
- Good: the authoring-agent legibility goal (ADR-0017's driver) is preserved
  with an accurate name for the category.
- Good: aligns the code field with the concept (the field holds a group).
- Breaking: the capabilities on-disk shape changes: the per-type `mechanism`
  key becomes `mechanism-group`, and the index key `mechanisms` becomes
  `mechanism-groups`. Consumers keying on those read differently.
- Neutral: no change to the grouping directories themselves (the values
  `core/`, `webhook/`, ... are the group names and are unchanged).

### Confirmation

`go test ./...` passes. `capabilities` groups output by mechanism group and
the index lists mechanism groups; pinned by tests. A lint rule rejects a
registry capability whose `MechanismGroup` is not one of the known constants.

## Interface notes

The capabilities output is part of the agent-facing surface. The per-type entry
key changes from `mechanism` to `mechanism-group`, and the index key changes
from `mechanisms` to `mechanism-groups`. Agents that read capabilities by these
keys must key on the new names. The grouping directories and the mechanism
values themselves are unchanged.

## More information

- ADR-0017 (superseded: its "mechanism" top level is now "mechanism group")
- SPEC: How an agent discovers integrations
