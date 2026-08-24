# Servitor implementation plan

Build order with dependencies and a clear "done" for each phase. The design lives in [SPEC.md](SPEC.md); the decisions live in [docs/adr/](docs/adr/); this is just the sequencing.

Current state: the Go project is scaffolded, the enforcement gates are wired (`make check`, adrlint, pre-commit, CI), and Phase 1 (daemon + loopback control protocol, `run`/`stop`) is built. Phase 2 (Wafer model and structured validation, `dry-run`) is built. Phase 3 (capability discovery, `capabilities` writing schemas and derived examples grouped by integration) is built; reporting varlock secrets and Singer taps is deferred to those integrations. The runner has no workflow state yet.

## Phase 1: Daemon and control protocol (foundation)

Everything else runs inside the daemon and is reached through the control protocol.

- [x] A long-lived runner daemon process that owns a SQLite file (WAL mode) and binds `127.0.0.1` only (ADR-0009). (SQLite file ownership is deferred to Phase 5 with Honker; the daemon lifecycle and loopback-only bind are built.)
- [x] A loopback control protocol (HTTP over `127.0.0.1`) between the CLI and the daemon, independent of argument parsing (ADR-0005).
- [x] `servitor run` boots the daemon; `servitor stop` drains and shuts it down.
- [x] The daemon refuses to bind a non-loopback interface (ADR-0009).

**Done when:** `servitor run` starts a daemon, the CLI talks to it over loopback, and `servitor stop` drains and shuts it down. `servitor capabilities` and `servitor dry-run` are the first commands wired through this protocol. (Lifecycle and protocol are done; wiring `capabilities`/`dry-run` happens in Phases 3-4.)

## Phase 2: Wafer model and validation

The artifact. Everything else reads, validates, and executes Wafers.

- [x] A Go representation of the Wafer (name, `on:` triggers, `steps:`).
- [x] The JSON Schema for the Wafer format, and per-step and per-trigger JSON Schemas (generated from the registry; surfaced to agents by `capabilities` in Phase 3).
- [x] Structured validation: errors returned with `path` (JSON Pointer), stable `code`, and `suggestion`; multiple errors at once; `warnings` for things like a side-effecting step missing `dedupe_key` (SPEC: Structured validation errors).

**Done when:** a malformed Wafer produces the structured error shape from the SPEC, and `servitor dry-run` can validate a Wafer against the schema. (`dry-run` currently validates locally; full resolution through the daemon is Phase 4.)

## Phase 3: Capability discovery

How an agent learns what the server supports and how to use it (SPEC: How an agent discovers integrations). Capabilities are per-server: the set is what the runner has compiled in. The schema and example generator and the file writer are built here; reporting declared secrets (varlock) and available Singer taps belongs to the phases that build those integrations.

- [x] A step-type and trigger-type registry, each entry with its JSON Schema, grouped by integration with a `core` group for Servitor's own types.
- [x] The schema-to-example generator: render a Wafer fragment from a schema (skeleton from the schema, sample values from each property's `examples`).
- [x] `servitor capabilities [dir]` writes, per step and trigger, the schema and its derived example to files, grouped by integration, plus an index. A pipeline can commit the output so remote agents read it from the repo.
- [ ] Report declared secrets (names and presence, not values) and available Singer taps, when varlock and Singer integrations are built.

**Done when:** an agent runs `servitor capabilities`, reads a step's schema and a valid example Wafer fragment, and can author a Wafer without guessing.

## Phase 4: dry-run

The pre-deploy gate. It belongs in the pipeline (ADR-0009).

- [ ] `servitor dry-run <wafer>` resolves the whole workflow (including secret references) and returns the DAG the runner would execute, without running anything, contacting anything, or persisting anything.
- [ ] Secret references shown as `<redacted:secret_name>`.

**Done when:** the pipeline can dry-run a Wafer before applying it, and an agent can verify structure, secret availability, and step config before a PR.

## Phase 5: Honker integration (durable queue)

The durability layer. Requires the cgo `mattn/go-sqlite3` driver to load the Honker extension (ADR-0004).

- [ ] Load the Honker SQLite extension into the daemon's connection.
- [ ] Workflow run queue: each step is a job; workers claim, execute, ack; visibility timeout and dead-letter on repeated failure.
- [ ] The transactional atom is {result, dedupe_record, downstream_enqueues, claim_ack}, committed as one SQLite transaction, never split (SPEC: Execution model step 8).
- [ ] Cron triggers via Honker's scheduler.

**Done when:** a step's completion commits result + dedupe + downstream enqueues + claim ack in one transaction, and a crashed worker's claim is re-issued on visibility timeout.

## Phase 6: Step execution

Every step runs as a subprocess; there is no in-process mode (ADR-0008).

- [ ] Step executors spawn a subprocess per job with a filtered environment containing only the secrets the step declared.
- [ ] The subprocess writes structured JSON to stdout and exits; the parent reads it and commits the fan-out transaction.
- [ ] The `dedupe_key` contract: skip on a prior successful run, proceed on a prior failed run, retention window.

**Done when:** `transform`/`branch`/`foreach` and the integration steps run as subprocesses with env filtering and dedupe, and a crash mid-job re-runs safely per the `dedupe_key` contract.

## Phase 7: Triggers and webhooks

Inbound events.

- [ ] HTTP receiver bound for webhooks, signature verification (Standard Webhooks + per-provider bespoke schemes).
- [ ] Event persisted before matching; run enqueued with the event payload as input.
- [ ] Trigger types: `http_webhook`, `standard_webhook`, provider-specific, `cron`, `manual`, `internal`.

**Done when:** a signed webhook is verified, the event persisted, the workflow matched, and a run enqueued.

## Phase 8: Varlock integration

Secrets.

- [ ] Self-healing launch: `servitor` re-execs under `varlock run --no-inject-graph -- servitor run` if `__VARLOCK_RUN` is absent.
- [ ] Per-step secret filtering at subprocess spawn (only the step's declared secrets).
- [ ] Webhook signing secrets read from the runner's environment.

**Done when:** the runner always boots with secrets resolved, and no step subprocess sees a secret it did not declare.

## Phase 9: SKILL.md

How an agent uses Servitor (SPEC: Consuming Servitor as a skill, ADR-0009).

- [ ] `SKILL.md`: the command reference teaching an agent to discover capabilities, author a Wafer, dry-run it, and open a PR (not submit directly, except on a box where it's local).
- [ ] The agent workflow (discover, author, dry-run, PR through the pipeline) validated end to end.

**Done when:** an agent can go from `servitor capabilities` to a merged, applied Wafer via a reviewed PR.

## Phase 10: Packaging and release

- [ ] Single-binary packaging and the release flow (`make release`, `VERSION` bump).
- [ ] README getting-started matches the real install.

**Done when:** downloading and running the binary works with nothing else installed.

## Cross-cutting

- [ ] Each CLI command implemented per the SPEC's command set and its mapping to daemon operations.
- [ ] Exit codes carry the signal (0 ok, 1 operation failed, 2 usage error, 3 daemon not running).
- [ ] The control plane stays gated and loopback-only throughout (ADR-0009); the deploy path is CI/CD-gated.
