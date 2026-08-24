---
status: accepted
date: 2026-08-24
decision-makers: [Mathias]
consulted: []
informed: []
scope:
  - runner
  - control-plane
interface-impact: new
---

# ADR-0005: Skill-first control plane; MCP deferred

## Context and problem statement

A control plane is how a human or an agent tells the runner what to do (submit a
workflow, dry-run it, enable it, inspect runs). There are two common shapes: an
in-process MCP server that the agent talks to through MCP tools, or a CLI that
the agent consumes through a skill (the Playwrap model). We need to decide which
shape the control plane takes, and whether MCP is part of v1.

## Decision drivers

- **Token efficiency.** An MCP server keeps every tool description in the
  agent's context on every turn, whether or not it is touched. A CLI stays quiet
  until the agent types a command. This matters most for capability discovery,
  which returns large JSON schemas that would sit in context permanently under
  MCP.
- **The Wafer is already a file artifact.** The strongest agent-first idea
  (humans and agents share one file) matches the "write to disk, agent reads on
  demand" principle that drives the skill model. The Wafer never depended on a
  control-plane transport; only the operations around it do.
- **Same interface for humans and agents.** A CLI is the interface both use;
  agents get it through a skill, humans through a terminal. No separate private
  API.
- **Webhooks arrive over HTTP into the daemon regardless of the control plane.**
  Inbound triggers are data plane, not control plane.
- Best Simple System for Now: build the CLI + daemon protocol; defer the MCP
  adapter until a concrete need exists.

## Considered options

- **CLI-first, MCP deferred** (chosen): control plane is a CLI client talking to
  the runner daemon over a plain loopback protocol (HTTP or unix socket). MCP
  becomes a possible future adapter over the same protocol.
- **MCP-first**: an in-process MCP server is the API. Rejected for token cost,
  and because it binds the runner to an MCP framework (see ADR-0004).
- **Both now**: CLI and MCP in v1. Rejected as speculative — MCP has no concrete
  user yet.

## Decision outcome

Chosen option: **skill-first control plane.** The runner is a long-lived daemon;
the control plane is a CLI (`servitor submit`, `servitor dry-run`,
`servitor enable`, `servitor trigger`, `servitor runs`, and so on) that talks to
the daemon over a plain protocol kept independent of argument parsing. Agents
consume Servitor through a skill (a `SKILL.md`) that documents the CLI; humans
use the same CLI. MCP is deferred, not designed out: the daemon protocol stays
transport-agnostic so an MCP adapter can sit beside the CLI later without a
rewrite.

The core agent-first primitives are first-class CLI operations, with a possible
future MCP adapter exposing the same operations over the same protocol:

| Operation | CLI surface |
| --- | --- |
| `describe_capabilities` | `servitor capabilities` (writes schemas to a file, agent reads on demand) |
| `dry_run` | `servitor dry-run <wafer>` |
| `submit_workflow` | `servitor submit <wafer>` |
| `enable/disable_workflow` | `servitor enable/disable <name>` |
| `trigger_workflow` | `servitor trigger <name>` |
| `list_runs/get_run/cancel_run` | `servitor runs` / `servitor run <id>` / `servitor cancel <id>` |

Capability discovery, structured validation errors, schema introspection, and
dry-run all remain first-class, because they serve agents regardless of the
transport (see SPEC.md).

### Consequences

- Good: near-zero context cost — the agent only spends tokens when it runs a
  command; schemas and dry-run output go to files it reads on demand.
- Good: no dependency on an MCP framework; the Go decision (ADR-0004) stands on
  its own.
- Good: humans and agents use the identical CLI, so there is no private API.
- Neutral: MCP, if ever built, is an adapter over the daemon protocol, not the
  interface.
- Bad: none identified for v1; the main risk is drifting back toward
  MCP-centricity, guarded against by this ADR and the SPEC framing.

### Confirmation

The control plane is a CLI; there is no MCP server in the v1 feature set. The
daemon control protocol is documented independently of argument parsing, so an
MCP adapter could be added later without rewriting the runner.

## Interface notes

This defines the public control-plane surface for the first time: the CLI
commands and the daemon protocol. The exact command set is recorded in SPEC.md
(see its control-plane section) and is the contract agents and humans share.
Any later MCP adapter exposes the same operations over the same protocol.

## More information

- ADR-0004 (adopt Go, which this decision's MCP-drop is part of)
- ADR-0002 (Best Simple System for Now)
- Playwrap's `SKILL.md` and spec.md (the skill-first model this is drawn from)
