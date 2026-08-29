---
status: accepted
date: 2026-08-29
decision-makers: [Mathias]
consulted: []
informed: []
scope:
  - runner
  - triggers
  - wafer
interface-impact: breaking
---

# ADR-0037: Rename the `on:` trigger field to `triggers:`

## Context and problem statement

The Wafer declares its triggers under a top-level `on:` key (SPEC: The Wafer).
The name comes from event-driven tooling (for example GitHub Actions, which use
`on:` for event bindings). In Servitor it is not obvious: a Wafer is read by
agents and humans, and `on:` does not clearly say that this is where the
workflow's triggers are declared. The decision record (ADR-0028, ADR-0030,
ADR-0031) and the SPEC use `on:` purely by inheritance, not by any Servitor
rationale.

## Decision drivers

- The Wafer is authored by agents and read by humans; field names should say
  what the field is, not inherit a convention from another tool.
- The top-level field holds the workflow's triggers, so naming it for that is
  clearer than a bare `on`.
- The sibling body field is `nodes:` (plural), so the trigger field should
  match: `triggers:` reads as a list alongside `nodes:` rather than a singular
  odd one out.
- Servitor has no installed base, so a breaking rename is a clean cut, not a
  migration.

## Considered options

- **Rename `on:` to `triggers:` (chosen).** The top-level field is the list of
  the workflow's triggers; `triggers:` says exactly that, and matches the plural
  `nodes:` beside it.
- **Keep `on:`.** Rejected: it is inherited jargon from event-driven CI and
  does not tell a reader what the field is for.
- **A singular `trigger:`.** Considered, and rejected: with the sibling `nodes:`
  being plural, a singular `trigger:` reads as an inconsistency for no benefit.

## Decision outcome

Chosen option: **rename `on:` to `triggers:`.**

The Wafer's top-level triggers field is `triggers:` (a list of trigger items),
where each item carries its `type:` and type-specific config:

```yaml
name: example
triggers:
  - type: manual
nodes:
  - type: shell
    name: a
    command: "true"
```

`on:` is no longer accepted. The item-level `type:` and config fields are
unchanged; only the container key is renamed.

### Consequences

- Good: the field says what it is; an agent authoring a Wafer sees `triggers:`
  and knows it is where triggers are declared.
- Good: it reads consistently with the plural `nodes:` beside it.
- Good: drops inherited CI jargon (GitHub Actions `on:`) for a name that
  describes Servitor's own model.
- Bad: a breaking rename, but with no installed base it is a clean cut.

### Confirmation

`go test ./...` passes. The Wafer parser, validator, and JSON Schema accept
`triggers:` and reject `on:`; a test asserts `on:` is no longer accepted.

## Interface notes

Breaking Wafer-schema change. The top-level `on:` key is renamed to `triggers:`.
Wafers written with `on:` must change to `triggers:`. The daemon control
protocol is unchanged (the Wafer is still submitted as YAML); only the YAML
shape changes.

## More information

- SPEC: The Wafer, Triggers
- ADR-0028 (step type with role), ADR-0030 (node as the body primitive),
  ADR-0031 (mechanism group): their examples use the then-current `on:`
  spelling and are left as historical records of that spelling
- ADR-0038 (the companion rename of the `internal` trigger type)
