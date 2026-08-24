# AGENTS.md

This file tells an agent how to work in this repository. Read it before doing anything.

## What this is

Servitor is a self-hosted workflow automation runtime for the agentic stack. Workflows are declared as YAML files (called **Wafers**); a long-lived runner daemon executes them durably; a CLI control plane exposes the runner for humans and agents alike. Servitor is agent-first: capability discovery, structured validation, dry-run, and a dedupe primitive exist because agents need them, not as afterthoughts.

This file governs how context about the codebase (why things are the way they are, what each part does, how to work on it) lives in the codebase itself, and how you, the agent, read it before editing and write it after.

## The docs

- **SPEC.md**: the full product and behavior spec: what Servitor is, the control-plane (CLI) surface, the Wafer format, how it works end to end. This is the source of truth for what to build and why. (The earlier draft was archived to `old-spec.md`.)
- **PLAN.md**: the implementation plan: build phases in order, dependencies, and what "done" means for each. Follow this when building.
- **docs/adr/**: the decision log. Each significant decision, with its alternatives and rationale, recorded as a numbered, immutable ADR. This is where the "why" of the design lives.
- **README.md**: what Servitor is and how to get started.

The docs are deliberately plain-language. Keep them that way.

## Guiding principle: Best Simple System for Now (BSSN)

Build the simplest thing that meets the need right now, written to an appropriate standard, with no speculative future-proofing. See ADR-0002 for the rationale.

In practice:

- Do not record a decision you have not made. No ADR for something still open.
- Do not add a layer where the code is self-evident. An obvious change needs no ADR.
- Do not future-proof the context itself: no speculative fields, no scaffolding "just in case."
- When something can be taken away and the system still works for now, take it away.

## Decisions already made (recorded, not locked)

These are decisions already made and recorded in ADRs, with their alternatives and rationale. They are not open by default, but they are not off-limits either: the ADR is where any challenge starts. If you think one of these is wrong, or that a new decision is needed, raise it. Read the ADR's rationale first, then make the case. A change is not a routine edit; it is a new decision, recorded as a new ADR that supersedes the old one.

- **The runner is written in Go.** (ADR-0004) The architecture is a daemon + CLI client + subprocess-per-step isolation that owns a SQLite file, the same shape as Playwrap. Honker's being Rust and Singer's being Python are not reasons to change this; both are called as subprocesses or SQL.
- **Skill-first control plane; no MCP in v1.** (ADR-0005) Agents consume Servitor through a skill and the CLI, not an MCP server. MCP is deferred as a possible future adapter over the daemon protocol.
- **The Wafer is the artifact, nowhere else.** The workflow is fully defined by the YAML file. No workflow state lives in a UI or database row that a human or agent cannot see.
- **One runner process owns the SQLite file and its single write connection.** SQLite single-writer is honored by design, not worked around.
- **The transactional atom is {result, dedupe_record, downstream_enqueues, claim_ack}.** Each possible split produces a distinct silent failure; see the execution model in SPEC.md.
- **Every step runs as a subprocess; there is no in-process mode.** (ADR-0008) The subprocess is the isolation boundary; nothing runs inside the runner's process. Go's cheap subprocess startup makes this the simplest, safest model.
- **Steps should carry a `dedupe_key`** when they have externally-visible side effects; the validator warns when one omits it.

## The context layers

Several homes, each owning one kind of context. Most context is in the product/behavior spec and the decision log; the rest sits with the code.

| Layer                         | Owns                                              | Mutable? | Enforced? |
|-------------------------------|---------------------------------------------------|----------|-----------|
| `SPEC.md`                     | What the product is and how it behaves            | yes      | review |
| `docs/adr/`                   | Decisions with real alternatives, and their rationale | append-only | linter (front matter) |
| Exported Go identifiers + package docs | A package's public interface           | yes      | `go vet`, review |
| Tests (per package)           | How the package behaves and how to call it        | yes      | CI |
| Package `README.md` / docstring | Why the package is shaped this way: intent, invariants | yes   | no |
| Commit message / PR body      | What a specific change did                         | immutable | no |

Anything that does not belong to one of these layers does not go in the codebase.

## Reading before editing

Load context in this order before touching any package:

1. **What the product is and how it behaves:** `SPEC.md`. This is the source of truth for behavior.
2. **Why a decision was made (with alternatives):** `docs/adr/`, filtered to the package or area rather than read whole (see below).
3. **What a package exposes:** its exported identifiers and package docs.
4. **How a package behaves and how to call it:** the package's tests. They are working examples guaranteed current, because CI fails the moment code and test disagree.

For what a specific change did, also check the commit message or PR description.

### Finding the decisions that touch an area

Do not read the whole ADR log. Query it by the `scope` field:

    grep -rl "<area>" docs/adr/

This returns the handful of decisions touching that area out of however many total.

## Routing what you learned

How to route what you learned this session into the codebase, and what to discard. Most session material is not durable; if a thing does not clearly match one of these, discard it rather than inventing a home for it.

- **Made a choice with real alternatives someone might later reverse?** ADR. If the choice changes behavior, the test that pins the new behavior is part of recording it.
- **Changed the product's behavior or interface (Wafer schema, CLI, daemon protocol)?** Update `SPEC.md` and, if it was a genuine decision, write an ADR.
- **Established or changed how a package is supposed to behave** (an edge case, an input/output guarantee, a regression you just fixed)? A test. This is preferred over prose whenever the behavior can be asserted.
- **Learned a durable gotcha or invariant that no assertion can capture**, something about intent or rationale rather than behavior? Package README or docstring, or the Gotchas section of `SPEC.md` for cross-cutting operational lessons.
- **Just describes what this diff does?** Commit or PR body.
- **Exploration that concluded nothing durable?** Discard. Do not write it anywhere.

When something could live as either a test or a prose line, the test wins. It is enforced; the prose is not.

## Working with the developer

This file is guidance you follow while working; it catches most process gaps in conversation. The hard guarantee comes from the automated gates under "What is enforced vs trusted." Use both: you guide while the work happens, the gates block what slips through.

Assume the developer may be junior or moving fast and may not know the vocabulary. The system works only if you carry the process, not them.

How to behave:

- **Do the bookkeeping yourself.** When a change needs an ADR, a test, or a SPEC update, draft it and ask the developer to confirm. Do not tell them to go write it. Make the correct path the easy one.
- **Explain the term as you use it.** When you say Wafer, `dedupe_key`, or `in_process`, add a one-line plain explanation. Do not assume the vocabulary is known.
- **One question at a time.** Do not interrogate. Ask the single most important confirmation, act on the answer, move on.
- **Prefer the simplest action (BSSN).** Ask before adding structure.

### Stop and confirm before

- **Changing a public interface** (the Wafer schema, the CLI surface, the daemon control protocol, or a package's exported surface). Say plainly what depends on it and what changes. If it is a genuine contract change, set `interface-impact`, draft the ADR, update `SPEC.md`.
- **Making a decision with real alternatives.** Offer to record a short ADR so it is not relitigated later. Draft it if yes. Drop it if it is not actually significant (BSSN).
- **Anything destructive or hard to reverse:** deleting or renaming a package or a public name, moving a dependency boundary.
- **Adding a dependency.** Ask whether it is needed now or whether a few lines do the job for now.

### Remind, but do not block, when

- A behavior changed and no test was added for it.
- A public interface changed and no ADR is linked.
- A package's README or docstring now contradicts the code.

### Pre-commit checklist

The automated checks enforce structure. This covers what they cannot:

1. **Behavior added or changed → a test asserts it.** If you changed what a package does or fixed a bug, there should be a test that would fail without that change.
2. **A real decision was made → ADR drafted, or explicitly declined.** A real decision is one with genuine alternatives that someone might later reverse.
3. **SPEC / README still matches the code.** If the public surface or the intent changed, check that the prose still accurately describes it. Stale prose is worse than no prose.

## Conventions

### ADRs

- Location: `docs/adr/`, a single global numbered sequence.
- Template: copy `docs/adr/0000-adr-template.md`. Do not edit it in place. It is MADR 4.0 adapted with the `scope` and `interface-impact` fields.
- Filenames: `NNNN-short-kebab-title.md`, zero padded.
- Numbers are sequential and never reused.
- ADRs are immutable. To reverse a decision, write a new ADR and set the old one's status to `superseded by ADR-NNNN`.
- Status lifecycle: `proposed` -> `accepted` -> (`deprecated` | `superseded`).
- A change that breaks the Wafer schema, CLI surface, or daemon protocol requires an ADR with `interface-impact: breaking`.
- ADRs are for decisions. Do not write one to describe current state, and do not write one for a change that involved no contested choice.

### Tests

- Live per package. Run them with `go test ./...`.
- Assert the contract and documented behavior, not implementation internals. A test pinning an incidental detail breaks on every refactor.

### Prose

- **No em dashes in docs** (the user dislikes them). Use commas, colons, or parentheses instead.
- Keep language plain and easy to understand. Avoid jargon where a clear phrase works.
- Write docs in a way that a person with no prior context can follow.
- When moving content in the docs, do it non-destructively: leave a pointer to where the content went, and verify no text was lost (compare word counts against the previous commit).

## Building, testing, and releasing

- **Version lives in `VERSION`** (one line, for example `0.1.0`). It is injected into the binary at build time via `-ldflags -X <module>/internal/app.Version=$(cat VERSION)`. Never hardcode a version string in source; `internal/app/version.go` holds the `Version` variable, default `dev`.
- **Build the runner and CLI with `make build`** (or `make` for all targets). `go build ./...` compiles everything.
- **Run the checks with `make check`** (or `make test` for tests alone): `go test ./...`, `go vet ./...`, `golangci-lint run`, and `gofmt -l`. These are the same checks CI runs.
- **The runner loads the Honker SQLite extension at runtime.** Loading a loadable SQLite extension requires the cgo driver `mattn/go-sqlite3`; the pure-Go driver cannot load extensions. So the build needs cgo and is not fully static. This is a deliberate, recorded cost (ADR-0004), not something to "fix" by dropping Honker.
- **Releasing** is a future `make release <new-version>` that bumps `VERSION`, rebuilds, and prints the git tag/push commands. Not wired up until there is something to release.

## Guardrails

- The product/behavior contract lives in `SPEC.md`. Never let it drift silently from what the code does.
- Behavioral guarantees live in tests, not in prose. If it can be asserted, assert it.
- Raw session narrative does not go in the codebase. Only the distilled, routed artifacts above.
- Keep the prose layer to slow-changing things: purpose, invariants, non-obvious constraints. The more you push into prose, the more drift you buy.

## What is enforced vs trusted

Two layers hold the process together. The agent behavior above is the soft layer: it catches gaps in conversation, while the work happens. The gates below are the hard layer: they block non-compliant changes regardless of who or what made them.

Hard gates (block the change):

- Tests (`go test ./...`) in CI: behavioral guarantees.
- Static analysis (`go vet ./...`): correctness.
- Lint (`golangci-lint run`) and formatting (`gofmt -l`): idiomatic style.
- Decision log lint (`scripts/checks/`): front matter parses, statuses are valid, numbering is sequential and unique.
- CI (`.github/workflows/checks.yml`) plus branch protection on `main`: the unskippable server-side gate. See CONTRIBUTING.md for the one-time setup.

Trusted (convention, made easy but not blocked):

- Good commit and PR messages, the discard discipline, SPEC and README accuracy. Distillation quality cannot be fully enforced; this file and the agent behavior make the correct path the easy one.
- `SPEC.md` accuracy is enforced only by review; the product spec is prose by nature.
