---
status: accepted
date: 2026-08-24
decision-makers: [Mathias]
consulted: []
informed: []
scope:
  - wafer
  - capabilities
interface-impact: none
---

# ADR-0010: Validate Wafers with a hand-written validator against registry metadata

## Context and problem statement

`servitor dry-run`, `submit`, and `update` must validate a Wafer and return the
structured error shape the SPEC pins down: each error has a JSON Pointer `path`,
a stable `code`, and often a `suggestion`, plus `warnings` such as a
side-effecting step missing `dedupe_key`. Servitor also needs JSON Schema
documents for the Wafer format and for each step and trigger type, so agents can
validate locally and so `capabilities` can surface them. We need one way to do
both, and it must not let the schema an agent reads drift from what the
validator enforces.

## Decision drivers

- The structured error shape (stable codes, paths, suggestions, warnings) is a
  hard SPEC requirement. Generic JSON Schema validators do not emit it.
- Capability discovery and validation must derive from the same source, so a
  field added to the schema appears in both the emitted JSON Schema and the
  validator (SPEC: "cannot drift").
- Best Simple System for Now: no speculative machinery, and the step/trigger
  registry is small and grows slowly.

## Considered options

- **Hand-written validator over a registry of field metadata** (chosen): a
  registry holds each step/trigger type's fields (type, required, examples).
  Validation walks the decoded YAML against that metadata, and the JSON Schema
  documents are generated from the same metadata. One source of truth.
- **JSON Schema library first**: validate the YAML with a JSON Schema engine
  (for example santhosh-tekuri/jsonschema) and emit the schemas it uses.
  Rejected because the engine's errors do not carry Servitor's codes,
  suggestions, or dedupe warnings, so the structured shape would still need a
  hand-written translation layer, and the Wafer-level semantic checks
  (unknown step type, dedupe warning, dependency cycles) are not expressible as
  plain JSON Schema anyway.

## Decision outcome

Chosen option: **hand-written validator over registry metadata.**

The `registry` package is the single source of truth: each step and trigger type
declares its fields once, with their types, required-ness, descriptions, and
examples. Validation (`wafer.Validate`) walks the decoded YAML against that
metadata, emitting structured `Issue`s with JSON Pointer paths, stable codes,
and typo suggestions (via edit distance). The JSON Schema documents a step,
trigger, or whole Wafer are generated from the same metadata, so the schema an
agent reads cannot drift from what the validator enforces. Semantic checks that
JSON Schema cannot express, notably the `missing_dedupe_key` warning for
side-effecting steps and unknown-step/trigger detection, live in the validator.

### Consequences

- Good: one definition per type drives both validation and the exposed schema;
  no drift path.
- Good: the structured error shape, including codes and suggestions, is natural
  to produce; no translation layer.
- Good: no heavy JSON Schema engine dependency.
- Bad: validation logic is hand-written and must grow as step types are added,
  but the registry keeps the per-type cost small and the cases uniform.
- Neutral: the emitted JSON Schema is generated rather than authored, which is
  exactly what keeps it in sync.

### Confirmation

`go test ./...` covers `wafer.Validate` (structured errors, suggestions,
warnings, multiple errors at once) and `registry` (sorted lookup, generated
schemas). A step type added to the registry automatically appears in both the
validator and the generated schema, so the drift guard is structural.

## Interface notes

No public interface change. The structured validation result and the JSON
Schema shape are documented in SPEC.md and are unchanged by this decision; this
ADR records how they are produced.

## More information

- SPEC: The Wafer, Structured validation errors, How an agent discovers
  integrations
- ADR-0002 (Best Simple System for Now)
