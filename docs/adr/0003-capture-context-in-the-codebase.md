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

# ADR-0003: Capture context in the codebase

## Context and problem statement

We want the why, what, and how of the codebase to live in the codebase itself,
so that a developer or an AI agent can reconstruct any part's context without
relying on memory, chat logs, or tribal knowledge. We also want useful context
from each development session, whether by a human or an agent, to end up in the
right place. The risk is the opposite failure: dumping everything and drowning
the signal.

## Decision drivers

- Any part's full context should be a few cheap reads at any codebase size.
- Each kind of context needs exactly one home, reached by query or by sitting
  next to the code, never by reading piles.
- Best Simple System for Now: capture only what is durable, discard the rest.
  The value is in the distillation, not the capture.

## Considered options

- One central log plus colocated, query-tagged layers (the system below)
- Per-package decision records and notes only
- An external wiki or doc tool
- Dumping raw session transcripts into the repo
- No formal capture; rely on commit history and memory

## Decision outcome

Chosen option: a small set of layers, each owning one kind of context, plus a
routing rule applied at the end of a session. The living specification is
`SPEC.md` at the repository root (the "what the product is") and `AGENTS.md`
(the "how this repo works for contributors and agents"); ADRs record decisions.
This ADR records the decision and its rationale; `AGENTS.md` records the
mechanics once written.

The layers: exported Go identifiers and package docs for the public interface,
tests for behavior and how to call a package, `SPEC.md` for product intent and
invariants, `docs/adr/` for decisions with alternatives, and commit or PR
messages for what a change did. Decisions are found per package by querying the
`scope` field (`grep -rl <package> docs/adr/`), not by reading the whole log.

Central-plus-tagged over the alternatives because per-package-only records
fragment the log and have no home for cross-cutting decisions; an external wiki
breaks the "context lives with the code" goal; and dumping raw transcripts
recreates the signal-to-noise problem we are trying to avoid. The discipline
that makes this work is routing each thing learned to exactly one layer and
discarding what has no durable home.

### Consequences

- Good: context is co-located with code, queryable, and stays cheap to load as
  the codebase grows.
- Good: tests carry behavioral context as an enforced layer; prose is reserved
  for what an assertion cannot express.
- Bad: depends on discipline (good commit messages, the discard default, doc
  accuracy) that cannot be fully enforced by tooling.
- Neutral: tests are treated as a context layer and are written against the
  public behavior, not implementation internals.

### Confirmation

The context layers are kept accurate by the automated checks described in
ADR-0006. Everything else is convention that `AGENTS.md` makes the path of
least resistance.

## More information

- ADR-0006 (the decision to enforce this process with automated checks)
- Best Simple System for Now:
  https://dannorth.net/blog/best-simple-system-for-now/
