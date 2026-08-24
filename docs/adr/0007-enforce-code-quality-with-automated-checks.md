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

# ADR-0007: Enforce code quality with automated checks

## Context and problem statement

The codebase needs to stay correct and idiomatic as it grows. Relying on code
review alone to catch failing tests, type errors, or non-idiomatic Go does not
scale and is exactly what fails under time pressure. We need code quality to be
enforced at the gate.

## Decision drivers

- Behavioral guarantees must be verified on every change, not assumed.
- Static problems (vet) and style issues (lint) should be caught before they
  reach code review.
- Best Simple System for Now: use established Go tooling and wire it into the
  same two-layer gate as ADR-0006.

## Considered options

- Rely on code review alone
- Run checks locally by convention only
- Pre-commit hooks only (local)
- CI checks only (server)
- Both: pre-commit hooks plus CI gated by branch protection

## Decision outcome

Chosen option: both layers, following the same approach as ADR-0006. The checks
are `go test ./...` (behavioral guarantees), `go vet ./...` (static correctness),
and `golangci-lint` (idiomatic style and common bugs, as the single configured
linter aggregator). The standard `go fmt` formatting is enforced as part of
lint. These are the default, maintained Go tools; no custom linter is written
until a real gap appears.

### Consequences

- Good: test failures, vet findings, and lint violations are all caught before
  code reaches `main`.
- Good: fast local feedback reduces failed CI runs.
- Bad: a small amount of setup per contributor (`pre-commit install`) and one
  repository setting (branch protection) that an admin must enable.
- Neutral: `golangci-lint` configuration can grow as the codebase does.

### Confirmation

`go test ./...` and `go vet ./...` run in CI and pass. `golangci-lint` runs as
part of the same `checks` workflow required by branch protection on `main`. A
green CI run on every pull request is the confirmation.

## More information

- ADR-0006 (context system checks that use the same two-layer approach)
- golangci-lint: https://golangci-lint.run/
- Best Simple System for Now:
  https://dannorth.net/blog/best-simple-system-for-now/
