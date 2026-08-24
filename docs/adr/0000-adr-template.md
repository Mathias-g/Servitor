---
status: proposed            # proposed | accepted | deprecated | superseded by ADR-NNNN
date: YYYY-MM-DD
decision-makers: [names or roles]
consulted: [names or roles]    # optional: subject-matter experts asked for input (two-way)
informed: [names or roles]     # optional: kept up to date (one-way)
scope:                        # parts of Servitor this decision touches
  - runner
  - control-plane
interface-impact: none         # none | new | breaking  (Wafer schema, CLI surface, or daemon control protocol)
---

<!--
Based on MADR 4.0.0 (https://adr.github.io/madr/), adapted for Servitor.
Copy this file to NNNN-short-kebab-title.md. Do not edit it in place.
See docs/adr/README.md for the full convention.

An ADR records a decision and its durable rationale. Do not reference the
implementation plan's phases (for example "Phase 6") or the current state of
the codebase; both drift. Anchor to the SPEC section instead (for example
"SPEC: Execution model"). See docs/adr/README.md, "What an ADR records".
-->

# ADR-NNNN: Short title of the decision

## Context and problem statement

What is the situation, and what problem or requirement forces a decision now?
Two to four sentences. State the architecturally significant requirement plainly.

## Decision drivers

- Driver 1 (e.g. the runner must stay deployable as a single binary)
- Driver 2 (e.g. keep the subprocess-per-step isolation model)
- Driver 3

## Considered options

- Option A
- Option B
- Option C

## Decision outcome

Chosen option: "Option X", because ...

State the decision in one or two sentences first, then the justification.

### Consequences

- Good: ...
- Bad: ...
- Neutral: ...

### Confirmation

How do we verify the decision is upheld over time? Prefer automated checks.
Examples: `go test ./...` passes, a lint rule fails on violation, CI blocks a
merge that violates the rule.

## Interface notes

Fill this in only when `interface-impact` is `new` or `breaking`. Describe the
public contract being added or changed: which part of the Wafer schema, the CLI
surface, or the daemon control protocol, and what consumers must do to adapt.
For a breaking change, note the migration path.

## More information

Links to related ADRs, issues, the discussion that led here, or relevant
sections of the docs (see docs/adr/README.md).
