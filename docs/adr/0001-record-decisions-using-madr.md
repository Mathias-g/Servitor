---
status: accepted
date: 2026-08-24
decision-makers: [Mathias]
consulted: []
informed: []
scope:
  - runner
interface-impact: none
---

# ADR-0001: Record decisions using MADR

## Context and problem statement

We want significant decisions, and the reasoning behind them, to be easy to find
from inside the repository and to survive changes in team and codebase. We need
to choose a single format and structure so records stay consistent and tooling
can read them.

## Decision drivers

- Records should live next to the code and version with it.
- Format should be lean enough that writing one is not a chore (Best Simple
  System for Now: the lightest format that does the job).
- Format should be widely understood and tool-supported.

## Considered options

- MADR (Markdown Any/Architectural Decision Records)
- Michael Nygard's original template
- Y-statements (Sustainable Architectural Decisions)
- A formless or freeform convention
- An external wiki, no in-repo records

## Decision outcome

Chosen option: MADR, kept in `docs/adr/` as a single global numbered sequence.
We use the template at `docs/adr/0000-adr-template.md`, which is MADR 4.0
adapted with two Servitor-specific fields, `scope` and `interface-impact`
(see the README in `docs/adr/`). ADRs are immutable; reversing a decision means
writing a new ADR that supersedes the old.

MADR over the alternatives because it is lean, plain Markdown (version-control
and review friendly), captures options and consequences explicitly, and has
existing tooling. Nygard's template is leaner but omits the options/consequences
structure we want. Y-statements are terser still and harder to extend. An
external wiki breaks the "context lives with the code" goal.

### Consequences

- Good: one greppable decision log, consistent structure, tool compatible.
- Bad: requires the discipline to write a record when a decision is made.
- Neutral: numbering is global across the project, not per package.

### Confirmation

`docs/adr/` exists and new significant decisions land here in the same pull
request that implements them. A small lint check validates front matter, status
values, and sequential numbering (see ADR-0006).

## More information

- MADR: https://adr.github.io/madr/
- General ADR background: https://adr.github.io/
- Michael Nygard, "Documenting Architecture Decisions" (2011):
  https://cognitect.com/blog/2011/11/15/documenting-architecture-decisions.html
- ADR-0006 (the decision to enforce the ADR format with automated checks)
