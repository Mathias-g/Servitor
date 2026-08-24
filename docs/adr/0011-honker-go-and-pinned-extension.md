---
status: accepted
date: 2026-08-24
decision-makers: [Mathias]
consulted: []
informed: []
scope:
  - runner
  - honker-integration
interface-impact: none
---

# ADR-0011: Use honker-go and a config-provided, pinned Honker extension

## Context and problem statement

The runner's durability layer is the Honker SQLite extension (SPEC: Honker,
durable queue and scheduler; ADR-0004). To use it from Go we need a binding and
a way to get the extension's native `libhonker_ext.so` onto the box. ADR-0004
already accepted the cgo cost of loading a SQLite extension, so this decision
is about the concrete binding and how the `.so` is supplied and kept versioned.

## Decision drivers

- The extension must be loaded into the runner's single SQLite connection, so
  the binding must wrap the cgo `mattn/go-sqlite3` driver (ADR-0004).
- The `.so` is a platform-specific native binary. Committing it to the repo is
  a smell (bloat, linux-only, third-party artifact in version control).
- Best Simple System for Now: reuse an official binding and a published,
  checksummed extension build rather than building the extension from source
  (which would require a Rust toolchain) or vendoring a binary.

## Considered options

- **honker-go + prebuilt extension, config-provided (chosen).** Use the
  maintained Go binding and load a prebuilt `libhonker_ext.so` that the
  operator supplies via `HONKER_EXT_PATH` (or a flag). The daemon loads it at
  open time.
- **Vendor the `.so` in the repo.** Simplest runtime (always present) but
  commits a 1.5 MB linux-x64 binary to git; wrong platform breaks it, and it
  mixes a third-party artifact into version control.
- **Build the extension from source** (cargo). Requires a Rust toolchain in the
  build environment and adds a second-language build to the pipeline; the
  published prebuilt artifact already exists.
- **Drive Honker via raw SQL through mattn/go-sqlite3** instead of a binding.
  Rejected: duplicates the binding's logic and error handling, and the official
  binding is thin and maintained.

## Decision outcome

Chosen option: **honker-go + config-provided, pinned prebuilt extension.**

The runner imports `github.com/russellromney/honker-go` (which wraps
`mattn/go-sqlite3`). The daemon opens its SQLite file and loads the Honker
extension from a path the operator provides via `HONKER_EXT_PATH` (or a flag);
it refuses to run without it. The extension `.so` is not committed to the repo.
The compatible extension version is pinned: ext-v0.5.0, with its published
SHA256, and CI downloads that exact artifact so the Honker-backed tests run for
real there. Local `go test` skips Honker tests when the extension is not
configured, so plain `make check` needs no setup.

### Consequences

- Good: no native binary in the repo; the runner stays a normal Go module plus
  an operator-supplied runtime dependency, consistent with how varlock and
  Singer are supplied.
- Good: real Honker behavior is tested in CI against a pinned, checksummed
  artifact.
- Good: matches how the binding itself works (`honker.Open(db, extensionPath)`).
- Bad: a deploy without the extension cannot boot the durable runner; the
  operator/packaging must supply it. Acceptable: the runner needs varlock and
  Singer at runtime anyway.
- Neutral: local Honker tests are skipped unless `HONKER_EXT_PATH` is set, so
  developers must opt in to run them.

### Confirmation

The daemon errors when `--db` is set but no extension path is available.
`internal/honker` tests skip unless `HONKER_EXT_PATH` is set; CI sets it from
the pinned, checksum-verified ext-v0.5.0 download. The extension path is
config, not a compiled-in constant.

## Interface notes

No change to the Wafer schema, CLI surface, or daemon control protocol. The
`servitor run` command gains a `--db` flag (the SQLite file to own) and reads
`HONKER_EXT_PATH`; both are additions, not changes.

## More information

- ADR-0004 (adopt Go, which records the cgo/extension-loading cost this builds on)
- SPEC: Honker, durable queue and scheduler
