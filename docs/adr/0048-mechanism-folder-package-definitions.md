---
status: proposed
date: 2026-09-02
decision-makers: [maintainer]
consulted: []
informed: []
scope:
  - runner
  - capabilities
  - registry
interface-impact: breaking
---

# ADR-0048: Mechanism groups, mechanism folders, mechanism packages; retire "integration" and "subpackage"

## Context and problem statement

The codebase's vocabulary for what a thing is has drifted and overloaded two
terms. "Integration" has been used for three unrelated things: a service
reached by a mechanism (removed from SPEC and the README tagline), the
declared-connectors config file (now `servitor.config.yaml`, ADR-0047), and a
compiled-in mechanism package (ADR-0045). "Subpackage" (ADR-0045) never had a
precise meaning: it was used for a package nested under a group directory,
conflating the group directory, the mechanism, and the package that registers
it. The actual layout also contradicts ADR-0045's "one package per mechanism":
`internal/registry/core/core.go` registers twelve mechanisms and
`internal/registry/webhook/webhook.go` registers six, so a group directory is
being used as a single multi-mechanism package.

## Decision drivers

- Every term must name exactly one kind of thing, so an author or a new
  contributor cannot be misled.
- A mechanism must be findable and deletable in one place: removing its folder
  removes it, with no central reference left to edit (ADR-0045).
- The physical layout must mirror the registry's mechanism-group grouping
  (SPEC: How an agent discovers integrations, ADR-0031), with no second
  taxonomy.
- Best Simple System for Now: one package per mechanism is the honest reading
  of ADR-0045, and the current group-directory-as-package layout is the
  exception to be fixed.

## Considered options

- Option A: Keep the current layout (group directory as a package registering
  many mechanisms) and define "subpackage" loosely to mean that. Rejected: it
  leaves no precise word for the unit, and it contradicts the deletion goal.
- Option B (chosen): Name the three levels precisely and retire the two
  overloaded words. A mechanism group is a category directory, a mechanism has
  its own folder inside that directory, and a mechanism package lives inside
  the mechanism's folder and self-registers exactly that one mechanism.

## Decision outcome

Chosen option: "Option B".

The nesting, on disk, is three levels:

```
internal/registry/
  webhook/                  mechanism group: category directory
    grist/                  mechanism: its own folder under the group
      grist.go              mechanism package: lives in the mechanism's folder,
                            self-registers the grist_webhook mechanism
    http/
      http.go
```

Definitions:

- **Mechanism group**: the top-level category, a family of mechanisms. The
  values are `core`, `webhook`, `singer`, `mcp`, `helper`, `websocket`. On
  disk it is a category directory: `internal/registry/<group>/`.
- **Mechanism**: an individual capability inside a group, for example
  `grist_webhook`, `http_webhook`, `mcp-stdio`, `singer-tap`. This is what an
  agent authors as a node `type:`. Each mechanism belongs to exactly one
  group, and each mechanism has its own folder under the group:
  `internal/registry/<group>/<mechanism>/`.
- **Mechanism package**: the Go package living inside the mechanism's folder,
  which self-registers exactly that one mechanism:
  `internal/registry/<group>/<mechanism>/<mechanism>.go`. One package per
  mechanism. It is the unit of deletion: remove the mechanism's folder and
  the mechanism disappears with no central reference left to edit.

"Integration" and "subpackage" are retired. Each former use is replaced by
the specific word: a service (an external product Servitor talks to), the
declared config (`servitor.config.yaml`), or a mechanism package (the compiled
unit that self-registers a mechanism).

The three homes for code, stated against these definitions:

- **Engine**: what the runner needs regardless of which mechanisms exist (the
  worker loop, dispatch, the durability store, the trigger receiver, the
  daemon, the CLI, Wafer validation). It lives at `internal/` top level and is
  never deletable.
- **Mechanism package**: a specific, deletable mechanism, at
  `internal/registry/<group>/<mechanism>/`.
- **Component**: reusable, mechanism-agnostic machinery shared by more than
  one consumer, not a capability and not the engine (`exec`, `expression`,
  `singer`, `mcp`, `secret`). It lives at `internal/components/<name>/`,
  imports only the standard library and other components, and names no
  capability. A component with a single consumer is moved into that consumer
  rather than left as a seam.

The current layout that violates this is `internal/registry/core/core.go`
(twelve mechanisms in one group-level package) and
`internal/registry/webhook/webhook.go` (six mechanisms in one group-level
package). They must be split into one package per mechanism: `core/shell/`,
`core/http/`, `webhook/grist/`, `webhook/http/`, and so on. The existing
`helper/email` already follows the model.

### Consequences

- Good: every term names one thing, and the overloaded "integration" and the
  meaningless "subpackage" are gone.
- Good: the deletion guarantee (remove the folder, the mechanism disappears)
  becomes literally true for every mechanism.
- Good: the layout mirrors the registry grouping with no second taxonomy.
- Bad: a package-splitting refactor across the registry, and a reword of the
  prose that used the retired terms.
- Neutral: the externally visible behavior (which capabilities exist, how
  `capabilities` groups them) is unchanged.

### Confirmation

`go test ./...` passes after the split, including the test that a capability
is present only when its mechanism package is imported (a deleted mechanism
folder removes the capability with no dangling references). The capabilities
output is unchanged.

## Interface notes

Breaking only in package structure and prose. The Wafer schema, the CLI
surface, and the daemon control protocol are unchanged: the same node types
exist, grouped the same way. Wafers are unaffected.

## More information

- ADR-0031 (a mechanism group is a family of mechanisms)
- ADR-0045 (mechanisms live in per-group packages and self-register; the
  "subpackage" wording this ADR replaces)
- ADR-0046 (the three homes for code: engine, mechanism, component)
- ADR-0047 (the declared config is named config; MCP mechanisms split by
  transport)
- SPEC: How an agent discovers integrations
