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

# ADR-0045: Self-registering mechanism packages

## Context and problem statement

A mechanism's definition and behavior are currently spread across a handful of
cross-cutting places: its capability metadata lives in one flat list in the
registry, its dispatch case lives in the runner's central command switch, and
its special-cased run path lives in the worker. There is no single physical
home for a mechanism, so finding it means looking in several files, and there
is no way to remove a mechanism without editing central switches that name it
explicitly. As the set of integrations grows, this becomes unmanageable, and an
operator cannot drop an integration cleanly.

The registry already groups capabilities by mechanism group (SPEC: How an agent
discovers integrations), so the physical layout should mirror that grouping
rather than introduce a second taxonomy.

## Decision drivers

- A mechanism must be findable in one place.
- A mechanism must be removable by deleting its package, with no central code
  still referencing it (no side effects).
- Shared machinery (subprocess isolation, secret env filtering, the Singer and
  MCP harnesses, the webhook receiver) must remain reusable, not duplicated per
  mechanism.
- The physical layout should mirror the registry's existing mechanism-group
  grouping.

## Considered options

- Option A: Keep the single flat registry list and the central dispatch
  switches; grow them as integrations are added.
- Option B: Split into mechanism packages under per-group directories, each
  mechanism self-registering its metadata and dispatch; shared machinery stays
  in shared packages the mechanisms import.

## Decision outcome

Chosen option: "Option B", because it gives each mechanism a single physical
home, makes deletion a matter of removing one package with no central
references to update, and keeps the reuse of shared machinery intact. The
layout mirrors the registry's mechanism groups.

Under the registry package, one directory per mechanism group (`core`,
`webhook`, `singer`, `mcp`, `helper`, `websocket`). Within a group, one
package per mechanism, and one subpackage per integration when an integration
is a distinct deletable unit (for example `helper/email`, `helper/grist`).
Each mechanism package self-registers its capability metadata and its dispatch
handler into the central registry. The
runner and worker dispatch through the registry's handler map instead of
naming each mechanism in a switch. Shared machinery that many mechanisms reuse
(Singer protocol, MCP stdio client, secret env filter, webhook receiver) lives
in shared packages that mechanisms import.

A service reachable by more than one mechanism is one integration per mechanism
group it appears in (SPEC: How an agent discovers integrations): a Grist helper
lives under `helper/grist` and a Grist MCP integration lives under `mcp/grist`;
they are separate packages because they are separate code paths, and each is
independently deletable.

### Consequences

- Good: a mechanism is findable and removable in one place; deletion has no
  side effects because nothing central names the mechanism.
- Good: the layout mirrors the existing mechanism-group grouping, so there is
  one taxonomy, not two.
- Good: shared machinery is reused through imports, not duplicated.
- Bad: a small registration surface (a handler interface) replaces a central
  switch; this is more structure than a flat list, justified by the removal and
  findability requirements.
- Neutral: existing mechanisms migrate into the new layout over time.

### Confirmation

`go test ./...` passes after each mechanism moves, including a test that a
capability is present only when its package is imported (a deleted package
removes the capability with no dangling references). The capabilities test that
pins the per-mechanism output keeps passing.

## Interface notes

No Wafer schema, CLI surface, or daemon protocol change. The change is internal
package structure. The externally visible behavior (which capabilities exist,
how `capabilities` groups its output) is unchanged.

## More information

- ADR-0031 (mechanism group is a family of mechanisms)
- ADR-0017 (capabilities grouped by mechanism)
- SPEC: How an agent discovers integrations
- SPEC: Node execution
