---
status: proposed
date: 2026-08-29
decision-makers: [maintainer]
consulted: []
informed: []
scope:
  - runner
  - capabilities
interface-impact: none
---

# ADR-0046: Shared components live apart from mechanisms and the engine

## Context and problem statement

Mechanisms and the engine both need reusable machinery that is not itself a
mechanism: subprocess isolation, expression evaluation, the record-streaming
protocol, the stdio tool protocol, and the secret provider + resolver. Before
this decision these lived scattered at the top level of `internal/` with no
named home, so there was no clear answer to where reusable, mechanism-agnostic
code should go, and no rule distinguishing it from a mechanism or from the
engine.

## Decision drivers

- A mechanism must remain a self-contained, deletable unit (ADR-0045).
- Reusable, mechanism-agnostic machinery must have a clear, distinct home.
- The boundary between a shared component, a mechanism, and the engine must be
  rigid enough that new code is routed without relitigating it.

## Considered options

- Option A: Leave shared machinery at the top level of `internal/` with no
  named home and no placement rule.
- Option B: Create `internal/components/` and move the existing shared
  machinery there, with a documented three-home routing rule.

## Decision outcome

Chosen option: "Option B", because it gives reusable machinery a distinct,
findable home and a rigid rule that separates it from mechanisms and the
engine.

Code is routed to one of three homes by asking, in order:

1. Is it Servitor's core engine (what the runner needs regardless of which
   mechanisms exist: the worker loop, dispatch, the durability store, the
   trigger receiver, the daemon, the CLI, Wafer validation)? It stays at
   `internal/` top level and is never deletable.
2. Is it a specific, deletable mechanism (ADR-0045)? It lives in
   `internal/registry/<group>/<mechanism>/`.
3. Is it reusable, mechanism-agnostic machinery that more than one consumer
   composes, and not itself a named capability? It is a shared component in
   `internal/components/<name>/`.

The existing shared machinery (exec, expression, singer, mcp, secret) moves
into `internal/components/`.

### Consequences

- Good: reusable machinery has a distinct home with a rigid routing rule.
- Good: a mechanism stays self-contained and deletable; shared code does not
  hide inside a mechanism's folder.
- Good: components import only other components and the standard library, so
  dependency points cleanly downstream and the registry/engine never depend on
  a specific component's mechanism knowledge.
- Bad: a package relocation churn on first adoption.
- Neutral: the components home makes the "single consumer" case visible so it
  is moved back into its consumer rather than left as a speculative seam.

### Confirmation

`go test ./...` passes after the move. The dependency invariant is checked by
convention (a component imports no registry, mechanism, or engine package); a
lint rule can be added if the convention is violated.

## Interface notes

No Wafer schema, CLI surface, or daemon protocol change. The change is internal
package structure and import paths; externally visible behavior is unchanged.

## More information

- ADR-0045 (mechanisms live in per-group packages and self-register)
- ADR-0002 (build the simplest thing that meets the need now)
- SPEC: How an agent discovers integrations
