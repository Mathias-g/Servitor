---
status: proposed
date: 2026-09-02
decision-makers: [maintainer]
consulted: []
informed: []
scope:
  - runner
  - capabilities
  - mcp-integration
interface-impact: breaking
---

# ADR-0047: MCP mechanisms split by transport; the declared config is named config

## Context and problem statement

The `mcp` mechanism group has a single mechanism, `mcp-call`, which spawns a
named MCP server as a stdio subprocess and sends one `tools/call` over that
subprocess's stdin and stdout (ADR-0015). But MCP has two first-class
transports: stdio (the client launches the server as a subprocess) and
Streamable HTTP (the client connects to a server's URL over HTTP, typically
with a Bearer token, per the 2025-06-18 and 2026-07-28 revisions of the MCP
spec). A single `mcp-call` mechanism cannot express both, because the two
transports differ in what the `server` config is: a command to spawn versus a
URL to connect to. So the mechanism name no longer says what it does, and
there is no home for the remote, URL-based case that services like Atomic
expose (SPEC: MCP integration).

Separately, the declared-config file is named `servitor.integrations.yaml`
(ADR-0018). The word "integration" is overloaded across the codebase: it also
means a service reached by a mechanism (removed from SPEC and the README
tagline) and a compiled-in mechanism package (ADR-0045). The file itself is a
single local config that declares the MCP servers, Singer taps and targets,
and secrets the operator has set up, so "integration" is the wrong name for
what it holds.

## Decision drivers

- A mechanism must say what it is: the transport a node uses is structural
  (it changes the config shape, how secrets flow, and what isolation means),
  while the MCP protocol revision is a per-server property that a client can
  detect.
- A service reachable by more than one MCP transport must be reachable as one
  mechanism per transport (SPEC: How an agent discovers integrations,
  ADR-0031), for example Atomic over stdio or over Streamable HTTP.
- The declared-config file holds MCP servers, Singer taps and targets, and
  secrets, so its name must not claim a narrower or overloaded meaning.
- Best Simple System for Now: keep the existing stdio implementation; do not
  build new stdio-specific work, but do not strip it either.

## Considered options

- Option A: Keep one generic `mcp-call` mechanism with a `transport` field
  (`stdio` or `http`). Rejected: the mechanism name no longer says what it is,
  and the transport changes the config schema (command versus URL) and the
  secrets model, which a single capability with a field cannot express cleanly.
- Option B: Split the group into two mechanisms by protocol revision
  (`mcp-stateless`, `mcp-classic`). Rejected: the protocol revision is a
  transient, detectable server property, not the thing an author chooses at
  authoring time; naming the mechanism after it couples a stable name to a
  detail that will age out.
- Option C (chosen): Split the group into two mechanisms by transport:
  `mcp-stdio` (the existing stdio behavior, renamed from `mcp-call`) and
  `mcp-http` (new, Streamable HTTP to a URL). Each carries an optional `mode`
  field (`stateless` or `classic`) that defaults to run-time detection.
- For the config file: rename it to `servitor.config.yaml` (chosen) rather
  than `servitor.connectors.yaml` or `servitor.declared.yaml`, because the
  file also holds secrets, which are not connectors, and "config" is the
  simplest honest word and trivially findable.

## Decision outcome

Chosen option: "Option C", because transport is the durable structural axis
and the protocol revision is a detectable per-server property, so the
mechanisms are named by transport and the revision stays a config field.

The `mcp` mechanism group gains two mechanisms:

- `mcp-stdio` (renamed from `mcp-call`): spawn the named MCP server as a
  stdio subprocess and invoke one tool (the existing behavior, unchanged in
  how it runs).
- `mcp-http` (new): connect to a named server's URL over Streamable HTTP
  with a Bearer token and invoke one tool.

Both carry an optional `mode` field (`stateless` or `classic`, the two MCP
protocol revisions), omitted to probe and auto-detect at run time. The
stateless/classic distinction is a property of the server, not of the
transport: both transports can speak either revision, and a client detects
the revision per transport (probe over stdio, HTTP status/body over HTTP).

The declared-config file is renamed from `servitor.integrations.yaml` to
`servitor.config.yaml`, and the `internal/integrations` package to
`internal/config`. The file keeps its three sections: `mcp` (each server is
now either a `command` for `mcp-stdio` or a `url` plus secret-referenced
`headers` for `mcp-http`), `singer` (taps and targets), and `secrets`
(ADR-0035). A server entry has a `command` or a `url`, not both.

### Consequences

- Good: a mechanism name says its transport, so the author and the agent know
  what a node does without reading its config.
- Good: the remote, URL-based MCP case has a home, matching how MCP services
  like Atomic expose a Streamable HTTP endpoint.
- Good: the protocol revision stays a field (defaulting to detection), so no
  authoring-time choice is required for a detectable server property.
- Good: the config file name no longer uses the overloaded word "integration"
  and is findable as the single Servitor config.
- Neutral: existing stdio behavior is preserved under the new name `mcp-stdio`;
  no capability is removed.
- Bad: a breaking rename of a node type and of the config file, with the
  usual migration for Wafers that name `mcp-call` and for existing
  `servitor.integrations.yaml` files.

### Confirmation

`go test ./...` passes. The capabilities output reports `mcp-stdio` and
`mcp-http` under the `mcp` group, and `mcp-call` no longer appears, pinned by
tests. A config with a `mcp:` server entry that sets both `command` and `url`
is rejected. A node omitting `mode` runs with run-time detection (pinned by a
test).

## Interface notes

Breaking. The Wafer node type `mcp-call` is renamed to `mcp-stdio`, and a new
node type `mcp-http` is added; Wafers that name `mcp-call` must use
`mcp-stdio`. The declared-config file `servitor.integrations.yaml` is renamed
to `servitor.config.yaml`, and the `servitor mcp/tap/target/secret` CLI
commands operate on the new file by default (`--file` still overrides). A
`mcp:` server entry in the config may carry a `command` (for `mcp-stdio`) or
a `url` plus `headers` with secret references (for `mcp-http`), and the
existing `command`-only entries keep working.

## More information

- ADR-0015 (the `mcp-call` step type, superseded in its naming by this ADR)
- ADR-0018 (the declared-config file, whose name this ADR changes)
- ADR-0031 (mechanism group is a family of mechanisms)
- ADR-0045 (mechanisms live in per-group packages and self-register)
- SPEC: How an agent discovers integrations; SPEC: MCP integration
- MCP spec, Transports (stdio and Streamable HTTP): https://modelcontextprotocol.io/specification/2025-06-18/basic/transports
