---
status: accepted
date: 2026-08-25
decision-makers: [Mathias]
consulted: []
informed: []
scope:
  - runner
  - mcp-integration
  - capabilities
interface-impact: new
---

# ADR-0015: Add an mcp-call step type as a standards-based integration path

## Context and problem statement

Servitor integrates with external services through step types. The curated
helpers (grist, slack, github, email, atomic) are hand-written and typed, and
the subprocess-per-step model (ADR-0008) already gives a cheap integration path
for any service that publishes an executable speaking a JSON-over-stdio
standard: singer-tap / singer-target cover data movement, and Standard Webhooks
covers inbound reception. There is no standards-based path for *actions*
(send a message, create a row, list records). The Model Context Protocol (MCP)
has a large ecosystem of self-hostable servers that expose named tools, each
with a JSON Schema for its input, over stdio, which maps directly onto the
existing execution model. We want a step type that reaches that ecosystem
without writing a dedicated helper per service. (This decision also moves
Atomic off the curated-helper list onto `mcp-call`; see v1 consumers below.)

## Decision drivers

- The subprocess is the isolation boundary (ADR-0008): a step is a subprocess
  with a filtered secret env, writes structured JSON to stdout, and exits.
  An MCP server is exactly such a subprocess (spawn, send a JSON-RPC request on
  stdin, read the response on stdout, terminate).
- Capability discovery already wants per-server tool schemas; an MCP
  server's `tools/list` / `server/discover` returns exactly those schemas.
- Breadth is the goal: the MCP ecosystem is large and self-hostable, so one
  mechanism reaches many integrations, the same way Singer reaches many taps.
- Best Simple System for Now: one general step type instead of hand-writing
  helpers up front; helpers remain for the hot paths where typed polish and
  low per-step cost pay off.

## Considered options

- **Add `mcp-call` alongside the curated helpers (chosen).** `mcp-call` reaches
  the long tail of MCP servers; the typed helpers stay for the high-frequency
  integrations (see v1 consumers below).
- **Replace the curated helpers with `mcp-call`.** Rejected: helpers are the
  fast, documented path for the integrations used most, and MCP adds per-step
  process-startup latency that helpers avoid.
- **Add an OpenAPI-backed step type instead of MCP.** Rejected: an OpenAPI
  document is not an executable; there is no prebuilt subprocess to run, so it
  does not multiply integrations the way a prebuilt MCP server does, and it
  breaks the subprocess isolation model. Parked as a future direction, not a
  step type (see IDEAS.md).

## Decision outcome

Chosen option: **add `mcp-call` alongside the curated helpers.**

`mcp-call` is a step type that invokes one named tool on one named MCP server:
fields `server` (which MCP server to run), `tool` (which named tool), and an
`input` object (the tool arguments). The server runs as a subprocess with a
filtered secret env; the step sends the tool call over stdio, reads the
structured JSON response, and exits. The server package is pinned the same way
Singer taps are. The name is `mcp-call`, not `mcp-tool`, to make clear the step
invokes a tool rather than defines one.

## v1 consumers

`mcp-call` ships against real, self-hostable MCP servers where it is the right
shape, rather than abstractly:

- **Atomic: MCP.** Atomic is a knowledge base, low-frequency by nature, and
  exposes a native MCP endpoint on its self-hostable `atomic-server` (search,
  read, create, update, ingest). This is the first `mcp-call` consumer.
- **Grist: curated helper.** Grist is a high-frequency integration (accounting,
  CRM), so it keeps a typed, documented, low-latency helper rather than paying
  MCP per-step process startup. Grist Labs also publishes an official MCP server
  for full-edition self-hosters, which is a fallback or an additional surface,
  but not the primary path.
- **Slack, GitHub, email: curated helpers.** High-frequency, typed, fast.

### Consequences

- Good: one mechanism reaches a large, self-hostable ecosystem of integrations.
- Good: fits the existing subprocess-per-step isolation model (ADR-0008) and
  the filtered-secret trust boundary; same risk profile as running a Singer tap,
  mitigated the same way (someone else's code, sees only what the step declared).
- Bad: each `mcp-call` pays process startup plus the server's runtime boot. For
  I/O-bound steps (the integrations this targets) the external call dominates,
  so the cost is largely hidden; for tight CPU-bound loops it is a real,
  measurable cost, which is why the curated helpers remain.
- Neutral: introduces a client-mode executor (write one request to stdin, read
  one response) distinct from the run-and-read-stdout executor used by singer
  steps; MCP servers are long-lived by design, so the step must terminate the
  server after the call.
- Neutral: `mcp-call` is an additive Wafer schema change; no breaking impact.

### Confirmation

`go test ./...` passes. The `mcp-call` executor, its capability discovery, and
the old/new protocol support (below) are each pinned by tests. Per-step server
versions are pinned in the Wafer or resolved from a pinned location, matching
how taps are pinned.

## Protocol version support (old and new MCP)

The MCP spec revised on 2026-07-28 made the protocol stateless: the
`initialize`/`initialized` handshake was removed and protocol version and client
capabilities now travel inline in a `_meta` field on each request. Adoption is
uneven because the revision is recent, so a given server may still expect the
old handshake. `mcp-call` therefore supports both:

- **Chosen:** probe once at capability discovery (try the stateless call, and on
  the old-handshake error fall back to `initialize` → `initialized` →
  `tools/list`), cache the detected mode on the capability entry, and speak the
  detected protocol at execution. This shares one JSON-RPC framing layer and
  avoids re-probing per step.
- **Rejected alternative:** maintain two fully separate client stacks for old
  and new, or support only the new spec. The former duplicates the framing for
  no benefit; the latter silently fails against a large share of the ecosystem
  and defeats the breadth goal.

## Error mapping

MCP tool results carry their own error shape (an `isError` flag plus content
blocks). This must map explicitly onto Servitor's structured validation error
format (`path`, `code`, `message`, `suggestion`) rather than being left
undefined. The exact mapping is specified alongside the executor; it is a
separate decision tracked with this one.

## Interface notes

Additive to the Wafer schema: a new step type `mcp-call` with `server`, `tool`,
and `input` fields. Additive to `servitor capabilities`: an MCP integration
group whose tool schemas are discovered from the configured servers. No change
to the CLI surface or the daemon control protocol.

## More information

- ADR-0008 (subprocess-per-step isolation, the model `mcp-call` builds on)
- ADR-0005 (skill-first control plane; MCP as a step type is unrelated to MCP
  as a daemon interface, which stays out of scope)
- SPEC: Step execution, How an agent discovers integrations
- IDEAS.md (OpenAPI-backed step direction, parked)
