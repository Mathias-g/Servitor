---
status: accepted
date: 2026-08-25
decision-makers: [Mathias]
consulted: []
informed: []
scope:
  - runner
  - control-plane
interface-impact: none
---

# ADR-0019: The Wafer deploy pipeline is operator-owned, not a shipped Servitor workflow

## Context and problem statement

Changing a workflow's behavior (submitting, updating, enabling, disabling) is
the CI/CD-gated write path (SPEC: Control plane, ADR-0009): Wafers live in a
git repo, an agent authors them, and a reviewed pull request is applied on the
box via `servitor submit`/`update`/`enable`/`disable`. The question is whether
Servitor ships the deploy pipeline itself (for example a `.github/workflows/
deploy.yml`) or leaves it to the operator to wire up.

## Decision drivers

- Getting onto the box is the operator's existing access (SSH or VPN), not a
  Servitor feature (SPEC: Control plane). A shipped pipeline would need
  deployment-specific reach (SSH credentials, a self-hosted runner) that Servitor
  cannot know generically.
- The apply logic is trivial: run `servitor dry-run` then `submit`/`enable` on
  the box. It lives naturally in the operator's own pipeline or a Wafer, not in
  the Servitor repo.
- Best Simple System for Now (ADR-0002): do not build CI/CD-deploy machinery
  until a real deployment needs it.

## Considered options

- **Operator-owned/documented (chosen).** Servitor documents the apply path and
  leaves the pipeline to the operator, who already has the box access.
- **Ship a deploy.yml pipeline.** A GitHub Actions workflow that applies Wafers
  on the box. Rejected: it assumes deployment infrastructure (self-hosted runner
  or SSH secrets) that is specific to each operator and unknown to Servitor, and
  it duplicates what an operator already runs.

## Decision outcome

Chosen option: **the deploy pipeline is operator-owned and documented.**

Servitor ships the CLI operations (`servitor dry-run`, `submit`, `update`,
`enable`, `disable`) and documents the apply flow, but does not ship a deploy
workflow. The operator wires their existing pipeline (or a Wafer, per IDEAS.md)
to reach the box and run those operations. The write path stays reviewed-PR-gated
and loopback-only (ADR-0009); Servitor never exposes a public endpoint for it.

### Consequences

- Good: no fragile, deployment-specific CI surface in the repo.
- Good: the apply path reuses the operator's existing box access and pipeline.
- Bad: a new operator must wire the apply step themselves; the documentation
  lowers that cost.
- Neutral: consistent with the control plane being loopback-gated and
  operator-reached.

### Confirmation

The apply path is documented in the README; the CLI operations it relies on are
pinned by tests. No `deploy.yml` is shipped.

## Interface notes

No change to the CLI surface or daemon protocol. Documentation only: the README
describes the operator-owned apply flow.

## More information

- ADR-0009 (gate the control plane; loopback-only)
- SPEC: Control plane
- IDEAS.md (a Wafer-driven publish as an alternative realization)
