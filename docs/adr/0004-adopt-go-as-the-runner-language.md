---
status: accepted
date: 2026-08-24
decision-makers: [Mathias]
consulted: []
informed: []
scope:
  - runner
  - control-plane
  - honker-integration
  - packaging
interface-impact: none
---

# ADR-0004: Adopt Go as the runner language

## Context and problem statement

Servitor is a new codebase: a long-lived runner process that owns a SQLite
file, spawns step subprocesses, and exposes a control plane. We need to pick a
host language now, before the structure and packaging are built. The control
plane is skill-first and CLI-based, not an in-process MCP server (ADR-0005),
so the language is genuinely open.

## Decision drivers

- The runner is a long-lived daemon that owns the SQLite write connection and
  spawns a step subprocess per job; the language must make subprocess spawn
  cheap and env-filtering easy.
- Single self-contained artifact for the operator (which runs it under varlock),
  not a portable AppImage. A dynamically linked binary is fine; a bundled Python
  runtime is a cost.
- The architecture is a sibling of Playwrap's (daemon + CLI client + subprocess
  isolation + own a store file), a shape the author has already built and knows
  in Go.
- Honker (the durable queue) is a compiled SQLite extension loaded at runtime;
  the runner talks to it as SQL, so Honker's language is irrelevant to this
  choice.
- Singer taps and targets are subprocesses either way; the runner's language is
  irrelevant to the Singer ecosystem. (Same non-dependency as Honker.)
- Best Simple System for Now: the fastest authoring loop and the smallest
  operational surface for a solo project.

## Considered options

- **Go.** Static-ish binary, fast subprocess spawn, excellent stdlib
  (`os/exec` with env construction, `net/http` for the daemon), the author's
  known language, and the same architecture as Playwrap.
- **Rust.** No GC, raw performance, same ecosystem as Honker. The ecosystem
  point is only real if we intend to extend Honker itself, which we don't.
  Slower authoring for a solo project whose hard part is workflow semantics, not
  runtime throughput.
- **Python.** Offers a rich ecosystem for MCP and Singer, but both are
  non-reasons here: the control plane is not MCP (ADR-0005), and Singer is
  subprocess-bound. Slower subprocess startup and heavier packaging (a bundled
  Python runtime) for the operator.

## Decision outcome

Chosen option: **Go**, because it fits the daemon + CLI + subprocess model
directly, gives fast subprocess spawn and a single operator artifact, and is the
language the author has already used for the same architecture. Rust's perf and
Honker-affinity are not needed by the workload; Python's MCP and Singer
affinities are not reasons (control plane is not MCP, Singer is subprocess-bound).

### Consequences

- Good: fast subprocess spawn per step; simple single-binary packaging for the
  operator; matches Playwrap's proven architecture and the author's fluency.
- Bad: loading the Honker SQLite extension requires cgo and `mattn/go-sqlite3`
  (the pure-Go driver cannot load extensions), so the binary is not fully
  static. Acceptable for a server-side tool the operator runs under varlock.
- Bad: the deferred MCP adapter (if ever built) uses a Go MCP framework
  (e.g. `mark3labs/mcp-go`), rather than a Python one.
- Neutral: Singer taps/targets remain subprocesses in whatever language their
  authors chose.

### Confirmation

`go test ./...`, `go vet`, and lint pass in CI (see ADR-0007). The runner builds
as a single binary and spawns step subprocesses with a filtered environment.

## Interface notes

None. The language choice does not change the Wafer schema, the CLI surface, or
the daemon control protocol.

## More information

- ADR-0005 (skill-first control plane, which keeps the language free of an MCP-framework dependency)
- ADR-0002 (Best Simple System for Now)
