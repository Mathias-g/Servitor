# Servitor implementation plan

Build order with dependencies and a clear "done" for each phase. The design lives in [SPEC.md](SPEC.md); the decisions live in [docs/adr/](docs/adr/); this is just the sequencing.

Current state: the Go project is scaffolded, the enforcement gates are wired (`make check`, adrlint, pre-commit, CI), and all phases 1-11 are built (daemon + loopback control protocol, Wafer model/validation, capability discovery, dry-run DAG resolution, Honker integration, step execution, triggers/webhooks, varlock integration, SKILL.md, run inspection, packaging/release, and the Singer integration with its tap/target executors, bookmark state, and capabilities taps report). The daemon owns a WAL SQLite file with the Honker extension loaded; the transactional atom ({result, dedupe, downstream, claim_ack} in one commit) is a tested primitive, and for Singer steps the bookmark is part of that same commit; the worker loop runs steps as subprocesses with env filtering and dedupe; inbound triggers include webhook (Standard Webhooks + generic HMAC), cron, and manual, with event persistence and workflow registration over the loopback control plane; the runner self-heals under `varlock run` so it boots with resolved secrets, which are filtered per step into subprocess environments; a `SKILL.md` teaches agents the discover-author-dry-run-PR workflow; the full CLI command set is implemented; `make release` drives the release flow (ADR-0012, ADR-0013, ADR-0014); and Singer taps/targets run as subprocesses with bookmark state persisted in the same transaction as each step's result (ADR-0016). The runner has no workflow registry consulted by the worker; runs are built from a Wafer into a self-contained step chain, and the trigger receiver matches against a stored index of registered workflows.

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

- [x] A step-type and trigger-type registry, each entry with its JSON Schema, grouped by mechanism with a `core` group for Servitor's own types.
- [x] The schema-to-example generator: render a Wafer fragment from a schema (skeleton from the schema, sample values from each property's `examples`).
- [x] `servitor capabilities [dir]` writes, per step and trigger, the schema and its derived example to files, grouped by mechanism (`core`, `webhook`, `singer`, `mcp`, `helper`, `websocket`; ADR-0017), plus an index. A pipeline can commit the output so remote agents read it from the repo.
- [x] Report declared secrets (names and presence, not values) in `capabilities` (a `secrets.yaml` from the varlock schema).
- [x] Report available Singer taps in `capabilities` (names of installed taps and their schemas). Done with the Singer integration; see Phase 11.

**Done when:** an agent runs `servitor capabilities`, reads a step's schema and a valid example Wafer fragment, and can author a Wafer without guessing.

## Phase 4: dry-run

The pre-deploy gate. It belongs in the pipeline (ADR-0009).

- [x] `servitor dry-run <wafer>` validates and resolves the workflow's dependency DAG (run order, dependencies, cycle and unknown-reference detection) and returns it as structured output, without running anything, contacting anything, or persisting anything.
- [x] Declared secrets resolved and shown as `<redacted:secret_name>` in dry-run (names only, never values), with a `missing_secret` warning when one is absent from the environment.

**Done when:** an agent can verify structure and step config before a PR. (Secret availability checking is deferred to the varlock phase.)

## Phase 5: Honker integration (durable queue)

The durability layer. Requires the cgo `mattn/go-sqlite3` driver to load the Honker extension (ADR-0004, ADR-0011).

- [x] Load the Honker SQLite extension into the daemon's connection (honker-go; extension provided via `HONKER_EXTENSION_PATH`, pinned and checksummed in CI).
- [x] The daemon owns the SQLite file (WAL mode) and its single write connection.
- [x] The transactional atom: a `CommitStepAtom` primitive that writes {result, dedupe_record, downstream_enqueues, claim_ack} as one SQLite transaction, never split (SPEC: Execution model step 8). The dedupe table and lookup are in place.
- [x] Workflow run queue worker loop (claim, execute, ack; visibility timeout and dead-letter on repeated failure) and cron triggers via Honker's scheduler. Built in Phase 6 alongside subprocess execution.

**Done when:** a step's completion commits result + dedupe + downstream enqueues + claim ack in one transaction (this phase), and a crashed worker's claim is re-issued on visibility timeout (Phase 6).

## Phase 6: Step execution

Every step runs as a subprocess; there is no in-process mode (ADR-0008).

- [x] Step executors spawn a subprocess per job with a filtered environment containing only the secrets the step declared.
- [x] The subprocess writes structured JSON to stdout and exits; the parent reads it and commits the fan-out transaction.
- [x] The `dedupe_key` contract: skip on a prior successful run, proceed on a prior failed run, retention window.

**Done when:** the `shell` step runs as a subprocess with env filtering and dedupe, the fan-out transaction commits atomically, and a crash mid-job re-runs safely per the `dedupe_key` contract. (The worker machinery is built; `transform`/`branch`/`foreach` and the integration handlers dispatch through the same subprocess machinery in later phases.)

## Phase 7: Triggers and webhooks

Inbound events.

- [x] HTTP receiver bound for webhooks, signature verification (Standard Webhooks + generic HMAC). Provider-specific receivers (grist/github/slack/atomic/email) are deferred.
- [x] Event persisted before matching; run enqueued with the event payload as input.
- [x] Trigger types: `http_webhook`, `standard_webhook`, `cron`, `manual`. Provider-specific types and `internal` are deferred; registration (`submit`/`enable`/`disable`) and manual `trigger` are wired through the control plane.

**Done when:** a signed webhook is verified, the event persisted, the workflow matched, and a run enqueued. (Done for `standard_webhook`/`http_webhook`/`manual`; provider-specific and `internal` remain.)

## Phase 8: Varlock integration

Secrets.

- [x] Self-healing launch: `servitor` re-execs under `varlock run -- servitor run` if `__VARLOCK_RUN` is absent, and warns (booting without secret resolution) if varlock is not installed.
- [x] Per-step secret filtering at subprocess spawn (only the step's declared secrets).
- [x] Webhook signing secrets read from the runner's environment.

**Done when:** the runner always boots with secrets resolved, and no step subprocess sees a secret it did not declare.

## Phase 9: SKILL.md

How an agent uses Servitor (SPEC: Consuming Servitor as a skill, ADR-0009).

- [x] `SKILL.md`: the command reference teaching an agent to discover capabilities, author a Wafer, dry-run it, and open a PR (not submit directly, except on a box where it's local).
- [x] The agent workflow (discover, author, dry-run, PR through the pipeline) validated end to end.

**Done when:** an agent can go from `servitor capabilities` to a merged, applied Wafer via a reviewed PR. (Validated end to end through submit/trigger against a local daemon; the PR-through-pipeline leg is exercised on a box with the deploy path wired.)

## Phase 10: Packaging and release

- [x] Single-binary packaging and the release flow (`make release`, `VERSION` bump).
- [x] README getting-started matches the real install.

**Done when:** downloading and running the binary works with nothing else installed. (The runner is a single Go binary built with `make build`/`make release`; the only runtime dependency is the operator-supplied Honker extension, per ADR-0011, and varlock for secrets.)

## Phase 11: Singer integration

The record-stream integration layer (SPEC: Singer, data movement integrations). The `singer-tap` / `singer-target` step types are registered but have no executor; this phase builds the subprocess execution pattern that `mcp-call` (Phase 12) is modeled on. Invocation contract is ADR-0016: the runner writes config (and state/catalog) to temp files and invokes the tap with `--config`/`--state`/`--catalog`, reading records and the next bookmark from stdout; a target gets `--config` and the records on stdin.

- [x] `singer-tap` step type executor: write the tap's config (and prior state and the authored `catalog`) to temp files, spawn the named tap as a subprocess with a filtered secret env, pass `--config`/`--state`/`--catalog`, capture the JSON records and next bookmark from stdout, and exit (run-and-read executor).
- [x] `singer-target` step type executor: write the target's config to a temp file and pass `--config`, then spawn the named target as a subprocess and feed it the records on stdin.
- [x] State management: each tap's incremental sync state (the bookmark) stored in Honker and passed back into the next tap invocation (SPEC: State persistence). The bookmark commits in the same transaction as the step result (SPEC: Execution model step 8).
- [x] Schema discovery: call the tap's `--about` and `--discover` once during a capabilities refresh and cache the config schema, available streams, and record schemas. The report emits the catalog in the same shape the Wafer's `catalog` field accepts, so an agent copies it verbatim; the executor never re-discovers.
- [x] Report available Singer taps in `capabilities` (this is the open half of the Phase 3 capabilities item above).

**Done when:** a Wafer can run a real tap and target as subprocesses with filtered secrets and bookmark state, and an agent can discover a tap's schemas via `capabilities`. (Validated against a fake tap fixture that speaks the Singer protocol and the file-flag convention per ADR-0016.)

## Phase 12: MCP integration (mcp-call)

A standards-based integration path alongside the curated helpers (ADR-0015). The curated helpers remain; `mcp-call` reaches the long tail of self-hostable MCP servers.

- [x] `mcp-call` step type: spawn the named MCP server as a subprocess with a filtered secret env, send one `tools/call` over stdio, read the structured JSON response, and exit (client-mode executor, distinct from the singer run-and-read executor built in Phase 11). The `mode` the server speaks (`classic`/`stateless`) is authored into the Wafer so a step never re-probes.
- [x] Support both MCP protocol versions: probe once at discovery, cache the detected mode, speak the old `initialize` handshake or the new stateless `_meta`-carrying protocol accordingly.
- [x] Map MCP tool results (the `isError` flag and content blocks) onto Servitor's structured validation error format (`path`, `code`, `message`, `suggestion`).
- [x] Capability discovery: probe declared `mcp-*` servers during a capabilities refresh and report their tools and protocol mode in `mcp/servers.yaml` (sourced from the declared integrations config per ADR-0018).
- [x] Pin server package versions. Handled by the declared-config model: the operator's `command` is the pin (for example `npx -y atomic-server@1.2.3` or a fixed path to an installed binary), the same way opencode and other harnesses express it. No separate version field is needed.

**Done when:** an agent can discover an MCP server's tools via `servitor capabilities`, author an `mcp-call` step, and run it as a subprocess with filtered secrets and correct error mapping, against both old- and new-spec servers.

**v1 consumers:** Atomic via `mcp-call`; Grist, Slack, GitHub, email on curated helpers.

## Cross-cutting

- [x] Each CLI command implemented per the SPEC's command set and its mapping to daemon operations.
- [x] Exit codes carry the signal (0 ok, 1 operation failed, 2 usage error, 3 daemon not running).
- [x] The control plane stays gated and loopback-only throughout (ADR-0009); the deploy path is CI/CD-gated and operator-owned/documented (ADR-0019).
- [ ] Agent authoring reference: committed examples of the Wafer format for every step and trigger type (core, singer, mcp-call, curated helpers, webhooks), so an agent sees a valid example without running `capabilities`. The generator is already generic, so this is deferred until the type set stabilizes; until then each new type's registry fields should carry `examples`, and `servitor capabilities` renders them on demand.
- [x] Declared integrations config (ADR-0018): replace PATH-prefix discovery with a single declared config (`servitor.integrations.yaml`, per-mechanism sections for MCP servers and Singer taps/targets, each with exact command and env) as the source of what `capabilities` reports, plus a management CLI (`servitor mcp`/`tap`/`target` add/list/remove) that writes entries and delegates the actual software install to the ecosystem's package managers.
