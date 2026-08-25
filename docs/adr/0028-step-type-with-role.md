---
status: accepted
date: 2026-08-25
decision-makers: [Mathias]
consulted: []
informed: []
scope:
  - registry
  - wafer
  - capabilities
interface-impact: breaking
---

# ADR-0028: A step type is one thing with a role, not a separate trigger/action kind

## Context and problem statement

Servitor's registry modelled two separate concepts, `StepType` and `TriggerType`,
and the SPEC described a Wafer as declaring "triggers" and "steps" as if they
were different kinds of things (SPEC: The Wafer). But every capability,
including `email_received`, `cron`, and the webhook types, is the same primitive:
a step that runs as a subprocess (ADR-0008). The distinction between a "trigger"
and an "action" is where the step is used (in `on:` to start a run, or in
`steps:` to do work), not a difference of kind. Modelling them as two types was
imprecise and forced parallel machinery (two lists, two schemas, two examples).

## Decision drivers

- Every capability is a step that runs as a subprocess; there is no structural
  difference between a trigger and an action (ADR-0008).
- The `on:`/`steps:` split is real (validation and execution differ), but it is
  a role, not a type.
- Provider-agnostic capability discovery and authoring benefit from a single
  model with role metadata, matching how other systems (for example Zapier)
  label capabilities.

## Considered options

- **One `StepType` with a `Role` attribute (chosen).** One registry list; each
  step type carries `Role` (`trigger`, `action`, or `both`). Trigger-role steps
  also carry a `Delivery` tag (instant, polling, scheduled, event, manual),
  which is informational, shown to agents, and fixed per type. Action-role steps
  keep `SideEffect`. `on:` accepts only trigger-role step types; `steps:`
  accepts only action-role step types.
- **Keep two types.** Preserves the old structure but encodes the wrong model
  and duplicates machinery.
- **One type with no role metadata.** Simpler, but loses the ability to tell an
  agent where a step is valid and what delivery it uses.

## Decision outcome

Chosen option: **one `StepType` with a `Role` attribute.**

The registry has a single list of step types. Each carries a `Role`. The
`on:` slot validates against trigger-role step types; the `steps:` slot
validates against action-role step types. Capabilities emits every type as
`kind: step` with a `role` field, and `delivery` for trigger-role steps. There
is no separate trigger type. `StepTypes()` and `TriggerTypes()` remain as
convenience filters over the single list, so callers that only care about one
role keep working.

### Consequences

- Good: the model matches reality (one primitive, a step, with roles), so the
  registry, validation, and capabilities are no longer duplicated per kind.
- Good: agents see `role` and `delivery` in capabilities, so they know whether
  a step starts a run and how, and whether it is valid in `on:` or `steps:`.
- Good: a future SMTP send step is an action-role step type, slots into the
  same model, and the unified registry has no special handling for it.
- Bad: this changes the capabilities output shape (`kind` is now always "step",
  plus a `role` field) and the internal registry surface, so it is a breaking
  interface change for consumers of the emitted files. Accepted; the files are
  generated and consumed only by agents, who read them fresh.

### Confirmation

`go test ./...` passes. A registry test pins that a trigger-role step type is
valid under `on:` but not `steps:`, and vice versa, and that `delivery` is set
on trigger-role steps. A capabilities test pins that entries emit `role` and
`delivery`.

## Interface notes

Breaking for the emitted `capabilities` files: each entry is now `kind: step`
with a `role` field (and `delivery` for trigger-role steps), replacing the old
`kind: step|trigger`. The Wafer schema is unchanged; `on:` and `steps:` still
accept the same type names. Internal registry surface changes (`TriggerType`
is gone, `StepType` gains `Role`/`Delivery`); this is internal to the module.

## More information

- ADR-0008 (every step runs as a subprocess; the primitive is one)
- ADR-0017 (mechanism as the organizing principle)
- SPEC: The Wafer (a Wafer declares triggers and steps; both are step types with
  roles)
