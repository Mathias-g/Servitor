---
status: superseded by ADR-0031
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

# ADR-0017: Mechanism is the organizing principle for capabilities

## Context and problem statement

Servitor reaches external services through several mechanisms, each with a
distinct shape of interaction: universal primitives (`core`), inbound HTTP
event reception (`webhook`), record streaming (`singer`), tool invocation over
stdio (`mcp`), compiled-in action wrappers (`helper`), and future inbound
streaming (`websocket`). A single service is often reachable by more than one
mechanism (Grist via a webhook trigger, a Singer tap, a Singer target, an MCP
server, and a curated helper). The question is how `servitor capabilities`
organizes what an authoring agent sees, so the agent can tell at a glance what
kind of thing an integration is and what it can do.

## Decision drivers

- The mechanism a thing speaks must be legible, so an agent does not mistake a
  record source for a tool server or an action wrapper.
- Grouping must be uniform across mechanisms, not a bespoke rule per mechanism.
- Capabilities is a per-box query: the authoritative answer to "what can this
  runner do" is what is compiled in and declared on that box
  (SPEC: How an agent discovers integrations).

## Considered options

- **Mechanism as the top-level organizing principle (chosen).** Capabilities
  groups its output by mechanism (`core/`, `webhook/`, `singer/`, `mcp/`,
  `helper/`, `websocket/`).
- **Group capabilities by service/integration.** One directory per service (for
  example `grist/`, `slack/`). Rejected: it hides the mechanism and conflates a
  webhook trigger with a helper action.
- **Group by standard vs bespoke envelope.** A `standard/` group and a
  `bespoke/` group. Rejected: standard-vs-bespoke is a per-type detail within a
  mechanism, not an organizing axis of its own.

## Decision outcome

Chosen option: **mechanism is the top-level organizing principle for
capabilities.**

`servitor capabilities` writes one file per step and trigger type (schema plus
derived example), grouped into top-level directories by mechanism: `core`,
`webhook`, `singer`, `mcp`, `helper`, `websocket`. A service reached by several
mechanisms appears in several groups; the type name carries the service
(`grist_webhook`, `slack_event`, `tap-grist`). The distinction between a
standard envelope and a bespoke one (for example `standard_webhook` vs
`http_webhook` vs `grist_webhook`) is a per-type detail within a mechanism, not
a separate group.

How integrations are discovered and enumerated is a separate concern, decided in
ADR-0018.

### Consequences

- Good: the mechanism a thing speaks is visible in capabilities grouping, so an
  agent does not mistake a source for a tool server or a webhook for a helper.
- Good: one uniform grouping rule across all mechanisms.
- Bad: bespoke webhook triggers lose their per-service subdirectory, folded into
  `webhook/` and distinguished only by type name.
- Neutral: a service using multiple mechanisms appears multiple times (once per
  mechanism); this is correct, since each is a distinct capability.

### Confirmation

`go test ./...` passes. `capabilities` groups output by mechanism (`core/`,
`webhook/`, `singer/`, `mcp/`, `helper/`), pinned by tests.

## Interface notes

Group names in the capabilities output change from service-based to
mechanism-based (`grist/` → `webhook/`), the `index.yaml` key becomes
`mechanisms:` (was `integrations:`), and the declared integration reports nest
with their mechanism (`singer/taps.yaml`, `mcp/servers.yaml`; ADR-0018);
consumers that read capabilities by directory or index should key on mechanism.
The `mcp-call` step type (ADR-0015) and the Singer contract (ADR-0016) are
unchanged.

## More information

- ADR-0008 (subprocess-per-step isolation)
- ADR-0015 (mcp-call step type)
- ADR-0016 (Singer invocation contract)
- ADR-0018 (declared integrations config: how integrations are discovered)
- SPEC: How an agent discovers integrations, What counts as an integration
