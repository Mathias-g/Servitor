---
status: proposed
date: 2026-08-25
decision-makers: [Mathias]
consulted: []
informed: []
scope:
  - runner
  - capabilities
  - singer-integration
  - mcp-integration
interface-impact: none
---

# ADR-0017: Mechanism is the organizing principle for integrations

## Context and problem statement

Servitor reaches external services through several mechanisms, each with a
distinct shape of interaction: universal primitives (`core`), inbound HTTP
event reception (`webhook`), record streaming (`singer`), tool invocation over
stdio (`mcp`), compiled-in action wrappers (`helper`), and future inbound
streaming (`websocket`). A single service is often reachable by more than one
mechanism (Grist via a webhook trigger, a Singer tap, a Singer target, an MCP
server, and a curated helper). Two things must follow from this: how
`servitor capabilities` organizes what an agent sees, and how the runner
enumerates the executables it can run as subprocesses. Both should be driven by
one consistent idea of what a mechanism is, so an agent can tell at a glance
what kind of thing an integration is and what it can do.

## Decision drivers

- Capabilities is a per-box query: the authoritative answer to "what can this
  runner do" is what is installed and compiled in on that box
  (SPEC: How an agent discovers integrations).
- The mechanism a thing speaks must be legible, so an agent does not mistake a
  record source for a tool server or an action wrapper.
- Discovery and grouping must be uniform across mechanisms, not a bespoke rule
  per mechanism.
- Best Simple System for Now: no configuration store for the server list; the
  box advertises itself.

## Considered options

- **Mechanism as the top-level organizing principle (chosen).** Capabilities
  groups its output by mechanism (`core/`, `webhook/`, `singer/`, `mcp/`,
  `helper/`, `websocket/`), and subprocess executables are named by a mechanism
  prefix so they can be enumerated on PATH (`tap-*`, `target-*`, `mcp-*`).
- **Group capabilities by service/integration.** One directory per service
  (for example `grist/`, `slack/`). Rejected: it hides the mechanism, conflates
  a webhook trigger with a helper action, and offers no uniform way to
  enumerate executables.
- **An explicit configuration list of servers.** A config file or environment
  variable names the executables to expose. More flexible about arbitrary
  names, but adds a configuration surface that does not exist today and drifts
  from "the box advertises what it has."

## Decision outcome

Chosen option: **mechanism is the organizing principle for integrations.**

Two consequences, both keyed on mechanism:

**Capabilities grouping.** `servitor capabilities` writes one file per step and
trigger type (schema plus derived example), grouped into top-level directories
by mechanism: `core`, `webhook`, `singer`, `mcp`, `helper`, `websocket`. A
service reached by several mechanisms appears in several groups; the type name
carries the service (`grist_webhook`, `slack_event`, `tap-grist`). The
distinction between a standard envelope and a bespoke one (for example
`standard_webhook` vs `http_webhook` vs `grist_webhook`) is a per-type detail
within a mechanism, not a separate group.

**Executable naming.** Integration executables that Servitor runs as
subprocesses are named `<mechanism>-<service>` and discovered by scanning PATH
for each mechanism's prefix:

- `tap-<service>`: a Singer source tap (emits records).
- `target-<service>`: a Singer destination target (consumes records).
- `mcp-<service>`: an MCP server (invoke named tools over stdio).

`capabilities` scans PATH for each prefix and probes each match for its schemas
(Singer taps via `--about`/`--discover`; MCP servers via `tools/list` with the
detected protocol mode).

Curated helpers are the exception to the executable rule: they are compiled
into the runner rather than discovered as executables, so they carry no prefix
and come from the registry, not PATH. They still group under `helper/` in
capabilities. The mechanism set is `core`, `webhook`, `singer`, `mcp`,
`helper`, and `websocket` (the last future).

### Consequences

- Good: uniform discovery and grouping rule across tap, target, MCP, and (in
  capabilities) all mechanisms; one model to learn.
- Good: the mechanism a thing speaks is visible both in capabilities grouping
  and in executable names, so an agent does not mistake a source for a tool
  server or a webhook for a helper.
- Good: no configuration store; the box advertises what it has.
- Bad: bespoke webhook triggers lose their per-service subdirectory, folded
  into `webhook/` and distinguished only by type name.
- Bad: servers whose real command does not follow the prefix (for example a
  distribution that ships `atomic-server` with no `mcp-` alias) are not
  auto-discovered; an operator can alias them or a future explicit list can
  cover them.
- Neutral: a service using multiple mechanisms appears multiple times (once per
  mechanism); this is correct, since each is a distinct capability.

### Confirmation

`go test ./...` passes. `capabilities` reports Singer taps via `tap-*` and
groups output by mechanism (`core/`, `webhook/`, `singer/`, `mcp/`,
`helper/`), each pinned by tests.

## Interface notes

Additive to `servitor capabilities`: an `mcp` group whose servers are
discovered from `mcp-*` on PATH, alongside the existing `tap-*`/`target-*`
Singer discovery. Group names in the capabilities output change from
service-based to mechanism-based (`grist/` → `webhook/`); consumers that read
capabilities by directory should key on mechanism. The `mcp-call` step type
(ADR-0015) and the Singer contract (ADR-0016) are unchanged.

## More information

- ADR-0008 (subprocess-per-step isolation)
- ADR-0015 (mcp-call step type)
- ADR-0016 (Singer invocation contract)
- SPEC: How an agent discovers integrations, What counts as an integration
