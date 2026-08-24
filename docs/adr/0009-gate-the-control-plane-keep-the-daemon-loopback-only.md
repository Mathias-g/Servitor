---
status: accepted
date: 2026-08-24
decision-makers: [Mathias]
consulted: []
informed: []
scope:
  - runner
  - control-plane
interface-impact: breaking
---

# ADR-0009: Gate the control plane; keep the daemon loopback-only

## Context and problem statement

The runner executes workflows that touch business-critical data, so how the
control plane is reached is a security decision, not an implementation detail.
The question: when the runner runs on a production server, how does a workflow
author (a human or an agent, which usually lives on a different machine, or in
CI) get a Wafer onto the runner, and how does it operate it (trigger, inspect,
cancel)?

The draft spec deferred this to "a reverse proxy, the operator's concern." That
understates how load-bearing the closed-down model is: it does not say whether an
agent may reach a running runner directly, and it leaves the distinction between
changing a workflow and operating a running one implicit.

## Decision drivers

- The runner holds business-critical data; its behavior-changing surface must be
  closed to anything except a reviewed, gated path.
- Wafers are already files: version-controllable, diff-able, reviewable. The
  artifact model already points at a git-based deploy path, not a direct socket.
- The daemon must not expose a mutable network surface. Loopback-only is the
  default and is already stated; this decision makes the operating model around
  it explicit.
- Best Simple System for Now: reuse the access the operator already has (SSH,
  VPN, a pipeline) rather than standing up new auth/proxy infrastructure.
- The change-management path (submitting a Wafer) and the live-operation path
  (trigger, inspect, cancel) are different risks and should be gated differently.

## Considered options

- **CI/CD-gated deploy; SSH/VPN operator channel** (chosen): the agent's only way
  to change behavior is a reviewed pull request through a pipeline; the pipeline
  (or an operator) runs the CLI on the box against the loopback daemon. The
  daemon never binds a non-loopback interface.
- **Agent talks to the daemon directly** (over a proxy or network port): rejected,
  because it gives an agent mutable, unaudited access to a business-critical
  runner and contradicts the closed-down requirement.
- **Expose the daemon over a public authenticated HTTP/API**: rejected as
  heavier than needed for a single-team self-hosted runner, and it enlarges the
  network surface the product must defend.

## Decision outcome

Chosen option: **gate the control plane; keep the daemon loopback-only.**

Two separate paths, treated differently:

- **Deploy (changing behavior): CI/CD-gated.** The agent authors Wafers in a git
  repo and submits them as a pull request. A pipeline (GitHub Actions, or a
  self-hosted runner on the box) validates, dry-runs, and applies Wafers via
  `servitor submit`/`update`/`enable`/`disable` on the box itself. The agent's
  write is a reviewable PR, never a direct socket to the runner. `dry-run` is
  the pre-deploy gate that belongs in the pipeline.
- **Operate (inspect, trigger, cancel): operator-gated.** Inspection (`servitor
  runs`, `servitor run <id>`) is read-only and can be exposed read-only through
  the operator's existing channel. State-changing operations (`trigger`,
  `cancel`) are gated the same way as deploy: run on the box by the pipeline or
  an operator, not through a wide-open agent socket. `servitor stop` is the
  operator's and the pipeline's, for maintenance.

The daemon binds `127.0.0.1` only and accepts requests from the CLI on the same
host. Getting onto the box is the operator's existing access: **SSH or VPN**.
The CLI stays loopback-to-daemon; SSH/VPN is what brings the operator or the
pipeline onto the box. There is no public HTTP endpoint for the control plane.

### Consequences

- Good: the only path that changes runner behavior is a reviewed, gated pipeline;
  nothing on the network can alter the runner directly.
- Good: reuses the access the operator already has (SSH, VPN), no new
  auth/proxy infrastructure to stand up.
- Good: matches the artifact model, Wafers as files in git, so the deploy path
  is a natural extension of the design.
- Bad: an agent cannot "just connect" to a remote runner; it must go through the
  pipeline. Acceptable, because that is precisely the closed-down property
  business-critical data requires.
- Neutral: a future MCP adapter (ADR-0005) would expose the same loopback
  protocol and inherit the same gating; it does not change this model.

### Confirmation

The daemon refuses to bind a non-loopback interface. There is no network listener
for the control plane beyond loopback. The SPEC's Authentication section states
this gating model. A deploy path that lets an agent mutate a running runner
without a reviewed pipeline would violate this ADR.

## Interface notes

Breaking relative to a loose reading of the draft: the control plane has no
direct network surface. `submit`, `update`, `enable`, `disable`, `trigger`, and
`cancel` are intended to run on the box (pipeline or operator); `runs` and
`run` are the read-only surface. The daemon protocol itself is unchanged; only
the gating around it is fixed.

## More information

- ADR-0005 (skill-first control plane, whose daemon protocol this gates)
- ADR-0004 (adopt Go)
- ADR-0002 (Best Simple System for Now)
