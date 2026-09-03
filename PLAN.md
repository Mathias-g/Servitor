# Servitor implementation plan

Build order with dependencies and a clear "done" for each phase. The design lives in [SPEC.md](SPEC.md); the decisions live in [docs/adr/](docs/adr/); this is just the sequencing. What works today (the current-state snapshot) lives in [STATUS.md](STATUS.md).

**PLAN.md is append-only.** Phases are numbered sequentially and never renumbered; an existing phase is never overwritten or replaced to describe new work. When new work does not belong in an existing phase, add it as a new phase with the next number (for example, if the last phase is 12, the new phase is 13). An earlier phase may be superseded by a later one, but the superseded phase stays in place as a record of what was built. Only reorder or merge phases when the developer explicitly asks.

**Partial tasks.** A partially-finished task is split into a done part (`[x]`) and a not-done part (`[ ]`), or the `[x]` line is annotated with what is deferred. Do not leave a task half-done with no marker of what remains; a `[x]` means "its intended scope is done" and a `[ ]` means "not done", with the text saying exactly what is left.

**Current state:** see [STATUS.md](STATUS.md) for what works today. This file is the build sequencing, not the current-state snapshot.

## Cross-cutting

Foundational work and cross-phase invariants, built in the early phases and
kept here because they hold across every phase rather than belonging to any one.
(They are listed before the phases because they predate them; later phases push
new work below.)

- [x] Each CLI command implemented per the SPEC's command set and its mapping to daemon operations.
- [x] Exit codes carry the signal (0 ok, 1 operation failed, 2 usage error, 3 daemon not running).
- [x] The control plane stays gated and loopback-only throughout (ADR-0009); the deploy path is CI/CD-gated and operator-owned/documented (ADR-0019).
- [x] Declared integrations config (ADR-0018): replace PATH-prefix discovery with a single declared config (`servitor.integrations.yaml`, per-mechanism sections for MCP servers and Singer taps/targets, each with exact command and env) as the source of what `capabilities` reports, plus a management CLI (`servitor mcp`/`tap`/`target` add/list/remove) that writes entries and delegates the actual software install to the ecosystem's package managers.

## Phase 1: Daemon and control protocol (foundation)

Everything else runs inside the daemon and is reached through the control protocol.

- [x] A long-lived runner daemon process that owns a SQLite file (WAL mode) and binds `127.0.0.1` only (ADR-0009). (SQLite file ownership is deferred to Phase 5 with Honker; the daemon lifecycle and loopback-only bind are built.)
- [x] A loopback control protocol (HTTP over `127.0.0.1`) between the CLI and the daemon, independent of argument parsing (ADR-0005).
- [x] `servitor run` boots the daemon; `servitor stop` drains and shuts it down.
- [x] The daemon refuses to bind a non-loopback interface (ADR-0009).

**Done when:** `servitor run` starts a daemon, the CLI talks to it over loopback, and `servitor stop` drains and shuts it down. `servitor capabilities` and `servitor dry-run` are the first commands wired through this protocol. (Lifecycle and protocol are done; wiring `capabilities`/`dry-run` happens in Phases 3-4.)

## Phase 2: Wafer model and validation

The artifact. Everything else reads, validates, and executes Wafers.

- [x] A Go representation of the Wafer (name, `triggers:` triggers, `nodes:`).
- [x] The JSON Schema for the Wafer format, and per-node and per-trigger JSON Schemas (generated from the registry; surfaced to agents by `capabilities` in Phase 3).
- [x] Structured validation: errors returned with `path` (JSON Pointer), stable `code`, and `suggestion`; multiple errors at once; `warnings` for things like a side-effecting node missing `dedupe_key` (SPEC: Structured validation errors).

**Done when:** a malformed Wafer produces the structured error shape from the SPEC, and `servitor dry-run` can validate a Wafer against the schema. (`dry-run` currently validates locally; full resolution through the daemon is Phase 4.)

## Phase 3: Capability discovery

How an agent learns what the server supports and how to use it (SPEC: How an agent discovers integrations). Capabilities are per-server: the set is what the runner has compiled in. The schema and example generator and the file writer are built here; reporting declared secrets (the declared-secrets config) and available Singer taps belongs to the phases that build those integrations.

- [x] A capability registry, each entry with its JSON Schema, grouped by mechanism with a `core` group for Servitor's own types.
- [x] The schema-to-example generator: render a Wafer fragment from a schema (skeleton from the schema, sample values from each property's `examples`).
- [x] `servitor capabilities [dir]` writes, per capability, the schema and its derived example to files, grouped by mechanism (`core`, `webhook`, `singer`, `mcp`, `helper`, `websocket`; ADR-0017), plus an index. A pipeline can commit the output so remote agents read it from the repo.
- [x] Report declared secrets (names and presence, not values) in `capabilities` (a `secrets.yaml` from the secret schema).
- [x] Report available Singer taps in `capabilities` (names of installed taps and their schemas). Done with the Singer integration; see Phase 11.

**Done when:** an agent runs `servitor capabilities`, reads a capability's schema and a valid example Wafer fragment, and can author a Wafer without guessing.

## Phase 4: dry-run

The pre-deploy gate. It belongs in the pipeline (ADR-0009).

- [x] `servitor dry-run <wafer>` validates and resolves the workflow's dependency DAG (run order, dependencies, cycle and unknown-reference detection) and returns it as structured output, without running anything, contacting anything, or persisting anything.
- [x] Declared secrets resolved and shown as `<redacted:secret_name>` in dry-run (names only, never values), with a `missing_secret` warning when one is absent from the environment.

**Done when:** an agent can verify structure and node config before a PR. (Secret availability checking is deferred to Phase 13.)

## Phase 5: Honker integration (durable queue)

The durability layer. Requires the cgo `mattn/go-sqlite3` driver to load the Honker extension (ADR-0004, ADR-0011).

- [x] Load the Honker SQLite extension into the daemon's connection (honker-go; extension provided via `HONKER_EXTENSION_PATH`, pinned and checksummed in CI).
- [x] The daemon owns the SQLite file (WAL mode) and its single write connection.
- [x] The transactional atom: a `CommitStepAtom` primitive that writes {result, dedupe_record, downstream_enqueues, claim_ack} as one SQLite transaction, never split (SPEC: Execution model step 8). The dedupe table and lookup are in place.
- [x] Workflow run queue worker loop (claim, execute, ack; visibility timeout and dead-letter on repeated failure) and the Honker scheduler runs. Cron trigger *registration* from Wafers is not yet wired; see Phase 7.

**Done when:** a node's completion commits result + dedupe + downstream enqueues + claim ack in one transaction (this phase), and a crashed worker's claim is re-issued on visibility timeout (Phase 6).

## Phase 6: Node execution

Every node runs as a subprocess; there is no in-process mode (ADR-0008).

- [x] Node executors spawn a subprocess per job with a filtered environment containing only the secrets the node declared.
- [x] The subprocess writes structured JSON to stdout and exits; the parent reads it and commits the fan-out transaction.
- [x] The `dedupe_key` contract: skip on a prior successful run, proceed on a prior failed run, retention window.

**Done when:** the `shell` node runs as a subprocess with env filtering and dedupe, the fan-out transaction commits atomically, and a crash mid-job re-runs safely per the `dedupe_key` contract. (The worker machinery is built; `foreach` and the integration handlers dispatch through the same subprocess machinery in later phases.)

## Phase 7: Triggers and webhooks

Inbound events.

- [x] HTTP receiver bound for webhooks, signature verification (Standard Webhooks + generic HMAC). Provider-specific receivers (grist/github/slack/atomic/email) are deferred.
- [x] Event persisted before matching; run enqueued with the event payload as input.
- [x] Trigger types: `http_webhook`, `standard_webhook`, `cron`, `manual`. Provider-specific types and `completed` are deferred; registration (`submit`/`enable`/`disable`) and manual `trigger` are wired through the control plane.
- [x] **Cron triggers wired.** Submitting or enabling a workflow with a `cron` trigger registers a scheduled task on the Honker scheduler; disabling or updating unregisters it. A `cron` trigger fires on its schedule.

**Done when:** a signed webhook is verified, the event persisted, the workflow matched, and a run enqueued. (Done for `standard_webhook`/`http_webhook`/`cron`/`manual`/`completed`; provider-specific and `email_received` remain.)

## Phase 8: Varlock integration

Secrets.

- [x] Self-healing launch: `servitor` execs itself under `varlock run --inject vars -- servitor run` if `__VARLOCK_RUN` is absent, and warns (booting without secret resolution) if varlock is not installed.
- [x] Per-node secret filtering at subprocess spawn (only the node's declared secrets).
- [x] Webhook signing secrets read from the runner's environment.

**Done when:** the runner always boots with secrets resolved, and no node subprocess sees a secret it did not declare.

## Phase 9: SKILL.md

How an agent uses Servitor (SPEC: Consuming Servitor as a skill, ADR-0009).

- [x] `SKILL.md`: the command reference teaching an agent to discover capabilities, author a Wafer, dry-run it, and open a PR (not submit directly, except on a box where it's local).
- [x] The agent workflow (discover, author, dry-run, PR through the pipeline) validated end to end.

**Done when:** an agent can go from `servitor capabilities` to a merged, applied Wafer via a reviewed PR. (Validated end to end through submit/trigger against a local daemon; the PR-through-pipeline leg is exercised on a box with the deploy path wired.)

## Phase 10: Packaging and release

- [x] Single-binary packaging and the release flow (`make release`, `VERSION` bump).
- [x] README getting-started matches the real install.

**Done when:** downloading and running the binary works with nothing else installed. (The runner is a single Go binary built with `make build`/`make release`; the only runtime dependency is the operator-supplied Honker extension, per ADR-0011, and the secret provider for secrets.)

## Phase 11: Singer integration

The record-stream integration layer (SPEC: Singer, data movement integrations). The `singer-tap` / `singer-target` capabilities are registered but have no executor; this phase builds the subprocess execution pattern that `mcp-call` (Phase 12) is modeled on. Invocation contract is ADR-0016: the runner writes config (and state/catalog) to temp files and invokes the tap with `--config`/`--state`/`--catalog`, reading records and the next bookmark from stdout; a target gets `--config` and the records on stdin.

- [x] `singer-tap` executor: write the tap's config (and prior state and the authored `catalog`) to temp files, spawn the named tap as a subprocess with a filtered secret env, pass `--config`/`--state`/`--catalog`, capture the JSON records and next bookmark from stdout, and exit (run-and-read executor).
- [x] `singer-target` executor: write the target's config to a temp file and pass `--config`, then spawn the named target as a subprocess and feed it the records on stdin.
- [x] State management: each tap's incremental sync state (the bookmark) stored in Honker and passed back into the next tap invocation (SPEC: State persistence). The bookmark commits in the same transaction as the step result (SPEC: Execution model step 8).
- [x] Schema discovery: call the tap's `--about` and `--discover` once during a capabilities refresh and cache the config schema, available streams, and record schemas. The report emits the catalog in the same shape the Wafer's `catalog` field accepts, so an agent copies it verbatim; the executor never re-discovers.
- [x] Report available Singer taps in `capabilities` (this is the open half of the Phase 3 capabilities item above).

**Done when:** a Wafer can run a real tap and target as subprocesses with filtered secrets and bookmark state, and an agent can discover a tap's schemas via `capabilities`. (Validated against a fake tap fixture that speaks the Singer protocol and the file-flag convention per ADR-0016.)

## Phase 12: MCP integration (mcp-call)

A standards-based integration path alongside the curated helpers (ADR-0015). The curated helpers remain; `mcp-call` reaches the long tail of self-hostable MCP servers.

- [x] `mcp-call` node type: spawn the named MCP server as a subprocess with a filtered secret env, send one `tools/call` over stdio, read the structured JSON response, and exit (client-mode executor, distinct from the singer run-and-read executor built in Phase 11). The `mode` the server speaks (`classic`/`stateless`) is authored into the Wafer so a node never re-probes.
- [x] Support both MCP protocol versions: probe once at discovery, cache the detected mode, speak the old `initialize` handshake or the new stateless `_meta`-carrying protocol accordingly.
- [x] Map MCP tool results (the `isError` flag and content blocks) onto Servitor's structured validation error format (`path`, `code`, `message`, `suggestion`).
- [x] Capability discovery: probe declared `mcp-*` servers during a capabilities refresh and report their tools and protocol mode in `mcp/servers.yaml` (sourced from the declared integrations config per ADR-0018).
- [x] Pin server package versions. Handled by the declared-config model: the operator's `command` is the pin (for example `npx -y atomic-server@1.2.3` or a fixed path to an installed binary), the same way opencode and other harnesses express it. No separate version field is needed.

**Done when:** an agent can discover an MCP server's tools via `servitor capabilities`, author an `mcp-call` node, and run it as a subprocess with filtered secrets and correct error mapping, against both old- and new-spec servers.

**v1 consumers:** Atomic via `mcp-call`; Grist, Slack, GitHub, email on curated helpers.

## Phase 13: Secret resolution (provider + per-node delivery)

Secrets (SPEC: Secret resolution, ADR-0032, ADR-0033, ADR-0034, ADR-0035, ADR-0036). Replaces the varlock boot mechanism (Phase 8, ADR-0034) with a pluggable provider that resolves each secret per node at subprocess spawn, and a declared-secrets config the operator owns. There is no migration: Servitor has no users, so the varlock boot path is removed outright, not transitioned.

- [x] **Provider interface** (ADR-0032): a narrow in-process `Resolve(ctx, nodeName, secretName)` contract, with failure semantics distinguishing source-unreachable / secret-missing / stale-invalid. A provider encapsulates its own mechanism (its on-box unlock: local key, TPM/vTPM, or off-box KMS; or a store credential for the pull arrangements). The axis options are shared internal components, not runtime-pluggable seams.
- [x] **A first provider: varlock as a pull source.** Build varlock in first as the pull provider (fetch each value once into the on-box at-rest store, then resolve per node from the local copy). It is not the recommended default, but it is the store already installed and working on this machine, so it is the easiest first implementation to validate the provider interface against. The on-box ciphertext provider (recommended) is built after.
- [x] **A working provider** for the recommended mechanism, push-based on-box ciphertext (`onbox`): material sealed to the box with `servitor secret seal` (value read from stdin), decrypted locally when a node needs it, at rest never plaintext (AES-GCM ciphertext under a local key in a 0600 store; the non-TPM unlock tier). The unlock key is a local file, so the stored values are ciphertext, not plaintext. Pull arrangements (external store, slow-store-into-on-box) are additional providers built as needed.
- [ ] **On-box TPM/KMS unlock tier (the non-exportable tier).** Seal the `onbox` store's key with a TPM or an off-box KMS key so the unlock key is non-exportable: a thief who steals the disk gets only ciphertext decryptable nowhere else (SPEC: Secret resolution). Future tier, not idea-blocked; build it when a host with TPM or a chosen KMS is in use.
- [x] **Per-node, per-subprocess delivery** (ADR-0033): resolve each secret at the moment its node runs, hand it to that one subprocess's filtered env, hold nothing past the subprocess. Redaction keeps operating on the running node's filtered env (ADR-0050). Resolve only what the registered Wafers reference; drop a secret whose last Wafer is removed.
- [x] **Remove the varlock boot path** (ADR-0034): drop the self-healing `varlock run` launch; the runner resolves through the provider instead. Varlock survives only as an optional pull provider, absent from the default. This means stripping the varlock integration from each place it is wired today:
  - Delete the `internal/varlock` package's boot API (`SelfHeal`, `Under`, `Available`, `ResolvedSecrets`), keeping at most a pull-provider implementation.
  - Remove the self-heal block and the `Secrets: varlock.ResolvedSecrets()` wiring in `internal/cli/cli.go`, and its import.
  - Replace the daemon's `Secrets map[string]string` and the worker's `Secrets` field + the six `exec.FilteredEnv(w.secrets, ...)` call sites (per-node filtering) with resolution through the provider per node at spawn. This reworks `exec.FilteredEnv` (which takes the whole resolved map today) to build a node's env from the provider's resolve of that node's declared names, so redaction keeps operating on the running node's filtered env.
  - Replace the webhook receiver's `r.secrets` map (`internal/trigger`) with per-use resolution of the signing key from the provider.
  - Replace `internal/capabilities` `declaredSecrets()` (which shells out to `varlock load`) with the declared-secrets config.
  - Update the varlock-dependent tests (`internal/varlock/varlock_test.go`, `internal/capabilities/capabilities_test.go`) and any worker/daemon/trigger/exec tests that assume the resolved `Secrets` map.
  - Update `internal/cli/usage.go` and the `servitor run` help text (which still say "under varlock").
- [x] **Declared secrets config + `servitor secret` CLI** (ADR-0035): a `secrets:` section in `servitor.integrations.yaml` (name + source required; account, permissions, expiry optional), managed by `servitor secret add/list/remove`. A secret referenced by a Wafer but not declared refuses to submit/run; declared-but-unused warns.
- [x] **Capabilities surface** (ADR-0035, ADR-0036): `capabilities` renders `secrets.yaml` (name + account + permissions + expiry, never values) and the `secret-resolution` mechanism group enumerating the available secret sources (the valid `source` values).
- [x] **Secret invalidity and rotation** (SPEC: Secret invalidity and rotation): reactive retry on auth failure with a fresh resolve (bounded, global `secret_retry_count` default); source-unreachable retries with backoff; secret-missing fails fast; the webhook signing key is verify-only, resolved per use, with no rollover window. (The resume-from-failure modes are a separate task below.)
- [x] **Failed-secret event emission** (SPEC: Secret invalidity and rotation, ADR-0039): when a secret-authenticated node fails after retries are exhausted, the run is marked failed and emitted as a `failed` event a workflow can trigger on (a distinct `failed` trigger, separate from `completed`), so the operator can wire their own notification. Built on the run-failure-resolution path (a dead-lettered node marks its run failed and fires a failure callback).
- [x] **Resume-from-failure modes** (SPEC: Secret invalidity and rotation): how "run it again" after a supplied secret behaves, settable globally in the servitor config and per Wafer with a CLI override: `continue` (resume from the failed node), `restart` (re-run from the top, safe only when side-effecting nodes declare `dedupe_key`), `discard` (drop the failed run). Built on the Phase 14 suspend/resume machinery (ADR-0040) and recorded as ADR-0044. Rerun is **general** (any failed run, whatever the cause), with `servitor rerun <run-id> [--mode ...]`, a `rerun-failed` node (defaults to re-running `event.from_run`), and a per-Wafer `on_failure` field as the default mode. A dead-lettered node saves its self-contained NodeJob as a failed continuation, and a generic node failure now also dead-letters and marks the run failed (previously only secret failures did). The global-config layer (a servitor config file) is deferred until one exists.

**Done when:** a node's secret is resolved per node at spawn and dies with its subprocess, the daemon no longer holds the full set or boots under varlock, an agent discovers the available secret sources and names via `capabilities`/`secrets.yaml`, and secret invalidity/rotation behaves per the SPEC. (The resume-from-failure modes are a separate task above and are not part of this phase's done-when.)

## Phase 14: Suspended waits (durable wait between nodes)

Durable wait between nodes (SPEC: Execution model step 11, Nodes: `wait`). A
`wait` flow node parks a run and resumes it later via a timer (Honker queue
`RunAt`) or a named signal. Four ADRs: the suspend/resume machinery
(ADR-0040), the `wait` node (ADR-0041), named signals (ADR-0042), and the timer
mechanism (ADR-0043). Unblocks the Phase 13 resume-from-failure modes, which
reuse the DAG-shaped continuation.

- [x] **Suspend/resume machinery** (ADR-0040): park a run in one transaction (a `suspended_continuations` row holding the parked node's downstream sub-DAG and the current `run_deps` state, the new `waiting` run status, ack the wait job's claim); the run-completion guard becomes `pending == 0 && status != waiting`; resume re-enqueues the continuation frontier (pending +1), flips status to `running`, deletes the row. The continuation is DAG-shaped so a `wait` can sit before a fan-in (multiple dependents) or have a fan-out after it.
  - [x] **Wait inside a `foreach` body (multiple concurrent parks per run).** The continuation is keyed per wait instance `(run_id, node_id)`, where `node_id` is the wait's effective id (`<body>#<i>` for a foreach iteration), so a run can park one continuation per wait at a time. A wait inside a `foreach` body where several iterations park simultaneously is supported: each parks its own row, a signal or timer resumes only its own wait, and the run stays `waiting` (the `pending == 0 && status != waiting` guard holds) until the last parked wait resumes, which flips it back to `running` for the rejoin to collect all the results in input order. The timer resume job carries the wait's `node_id` so a multi-parked run resumes the right wait. A wait before a fan-in or with a fan-out after it is supported.
- [x] **`wait` node type** (ADR-0041): register the flow node with optional `timer` and `signal`; resolve on whichever fires first; the `{source, payload}` result shape; reject a node with neither source; thread the result forward into downstream `{event, steps}` input.
- [x] **Timer mechanism** (ADR-0043): a `RunAt`-carrying `Tx.Enqueue` variant; `timer.after` (duration) and `timer.at` (absolute time) resolved to a `RunAt` at park time. The timer job is not explicitly dropped when a signal resolves first; a stale timer fire is a no-op through the repeat-resume guard, which makes timer and signal mutually exclusive by construction.
- [x] **Named signals** (ADR-0042): resolve the `signal` JSONata expression at park time; a `send-signal` node for one workflow to wake another; `servitor resume <signal-name> [payload]`; buffer a signal that arrives before the park and consume it in the park transaction; a repeat resume is a no-op (atomic compare-and-set on `waiting`); a signal addressing more than one parked run is rejected as ambiguous.
- [x] **Inspection and control surface**: `waiting` shown in `servitor runs` / `servitor run <id>`; `servitor cancel` drops parked continuations.
- [x] **Tests**: parking/resume atomicity, the `pending == 0 && status != waiting` guard, first-wins on timer vs signal, buffered pre-park signal, no-op on repeat resume, ambiguous-signal rejection, `send-signal` waking a parked run, the full daemon resume path, and a `wait` with neither source failing validation.
  - [x] Timer `RunAt` durability across a store reopen.
  - [x] Park inside a `foreach` body (a wait in a fanned body parks per iteration and collects at the rejoin; `TestForeachBodyWaitParksConcurrentlyAndCollects`).

**Done when:** a `wait` node parks a run and resumes it on a timer or a named signal, the `waiting` status is visible and cancellable, the named signal is authorable and unambiguous, and the Phase 13 resume-from-failure `continue` mode can be built on the same continuation.

## Phase 15: Self-registering mechanism packages

Splits the mechanism registry and its dispatch so each mechanism lives in its
own package under a per-group directory and self-registers into the central
registry (ADR-0045). The goal is a single physical home per mechanism and clean
deletion: removing a package removes the mechanism with no central references
left to edit.

- [x] **Registration surface.** The registry gains a handler type that carries a
  mechanism's metadata and its dispatch (how a node of that type is spawned and
  run), plus a `Register` entry point. `commandFor` in the runner and the
  worker's node-type special cases become lookups into the registry's handler
  map instead of switches that name each mechanism.
  - [x] **`Register` entry point.** `registry.Register` adds a capability,
    idempotent by name; the registry's list is populated only through it
    (ADR-0045).
  - [x] **Dispatch through the registry.** `Capability` carries a `Spawn`
    function (builds the node's argv) and a `RunKind` (which execution harness
    runs it). `commandFor` in the runner is now `registry.CommandFor`, and the
    worker's `runNode` dispatches the singer/mcp harnesses via
    `registry.RunKindFor`, so neither names a node type in a switch. The worker
    engine's control-flow routing (`wait`, `switch`, `foreach`, `send-signal`,
    `rerun-failed`, `poll`, `resume`, `skip`) stays in the worker keyed by
    `NodeType`: those are Servitor's spec primitives, not deletable
    integrations, and they are inseparable from the worker's store and queue
    (a package under the registry cannot import the worker without a cycle).
- [x] **Group packages.** One directory per mechanism group under the registry
  (`core`, `webhook`, `singer`, `mcp`, `helper`, `websocket`). Within each, one
  package per mechanism, each registering its capability and handler. An
  integration that is its own deletable unit is a subpackage within its group
  (for example `helper/email`, `helper/grist`).
- [x] **Migrate existing mechanisms.** Move the current core, webhook, singer,
  mcp, and email capabilities into the new layout, one group at a time, with
  `go test ./...` green after each move.
- [x] **Deletion proof.** A test asserts a capability is present only when its
  package is imported, so deleting a package removes the capability with no
  dangling references (ADR-0045 confirmation).

## Phase 16: Shared components home

Gives reusable, mechanism-agnostic machinery a distinct home and a rigid
placement rule (ADR-0046), separating it from both mechanisms and the engine.

- [x] **Move shared machinery into `internal/components/`.** The exec,
  expression, singer, mcp, and secret packages move from `internal/` top level
  into `internal/components/`, and all importers (worker, runner, daemon,
  trigger, cli, capabilities) update their import paths.
- [x] **Document the routing rule.** `internal/components/doc.go` and the SPEC
  state the three-home rule (engine at `internal/` top level, mechanism in its
  group folder, shared component in `internal/components/`) and the invariants:
  a component names no capability, imports only other components and the
  standard library, and dependency points downstream. A component with a single
  consumer is moved into that consumer rather than left as a seam.


## Phase 17: Mechanism layout, terminology, config rename, and MCP transports

Applies the decisions recorded in ADR-0047 and ADR-0048. Two goals: make the
physical layout and the vocabulary match the definitions (a mechanism group is
a category directory, a mechanism has its own folder inside it, and a mechanism
package lives in that folder and self-registers exactly one mechanism), and
split the `mcp` group by transport.

- [x] **Split the registry into one package per mechanism (ADR-0048).** The
  group-directory packages that register several mechanisms today must become
  one mechanism folder (and package) per mechanism:
  `core` (12 mechanisms: http, shell, transform, switch, foreach, wait,
  send-signal, rerun-failed, cron, manual, completed, failed) and `webhook`
  (6: http, standard, grist, github, slack, atomic) each split into one folder
  per mechanism, e.g. `core/shell/`, `core/http/`, `webhook/grist/`,
  `webhook/http/`. `singer` (2: tap, target) splits into `singer/tap/` and
  `singer/target/`. `mcp` (1: mcp-call) and `helper/email` (1) already follow
  the model. `go test ./...` stays green after each group moves.
- [x] **Update the `mechanisms` aggregator.** `internal/registry/mechanisms/`
  blank-imports group-level packages; after the split it must import every
  mechanism package instead, or its shape changes. The deletion-proof test
  (a capability is present only when its mechanism package is imported) keeps
  passing.
- [x] **Rename the config to `servitor.config.yaml` (ADR-0047).**
  `internal/integrations` becomes `internal/config`, and the file
  `servitor.integrations.yaml` becomes `servitor.config.yaml`. The `Config`,
  `Server`, `Tap`, `Target`, and `Secret` types, `DefaultFile`, the
  `servitor mcp/tap/target/secret` CLI `--file` default, the capabilities
  reports, and all importers and tests update. The `mcp:` section gains a `url`
  variant (plus secret-referenced `headers`) alongside `command`.
- [x] **Split the `mcp` group by transport (ADR-0047).** Renamed `mcp-call` to
  `mcp-stdio` (behavior unchanged), and added `mcp-http` (Streamable HTTP to a
  declared `url`, secret-referenced token). Both carry an optional `mode` field
  (`stateless` or `classic`) that defaults to run-time detection. `mcp-stdio`
  is fully wired (registry, worker dispatch, `examples/order-wafers.md`, tests).
  `mcp-http` is registered with its schema (so it validates and appears in
  `capabilities`) but its executor is not yet built: running one fails with a
  clear "not yet built" error. The config `url`/`headers` schema is built (a
  URL-only server validates, holds its URL, and is skipped by capabilities
  discovery rather than mis-probed). The mcp-http executor is deferred to its
  own task below.
- [x] **Build the `mcp-http` executor (Streamable HTTP).** The remaining half of
  the mcp-http mechanism (ADR-0047): the Streamable HTTP client, the connector
  registry (URL lookup from the config, which now carries `url`/`headers`), and
  worker dispatch so an `mcp-http` node runs instead of failing with the
  "not yet built" error. Unblocks the `search` URL-based server in
  `examples/servitor.config.yaml` (currently skipped by capabilities discovery).
  The node runs as the hidden `servitor __mcp_http` subprocess (ADR-0008): the
  worker looks up the server's URL from the boot-loaded connector registry
  (`servitor.config.yaml` `mcp:` entries with a `url`), resolves the node's
  declared secrets to the subprocess env, and spawns it, so the HTTP client and
  the secret-bearing request headers never enter the runner's process. A header
  may only reference a secret the node declares in `secrets:` (resolved per
  use); a header naming any other secret fails like a missing secret. The
  Streamable HTTP client (`internal/components/mcp/http.go`) supports both
  protocol revisions (stateless `_meta`, classic initialize handshake) and
  plain-JSON and SSE responses; `capabilities` now probes URL-based servers over
  HTTP at refresh (with `$SECRET` header references resolved from the CLI's
  resolver) instead of skipping them.
- [x] **Route the leftover packages (ADR-0046, ADR-0048).** Move
  `internal/email` (a provider-agnostic, multi-consumer, mechanism-agnostic
  `Email` struct) to `internal/components/email`, and move `internal/gmail`
  (the Gmail provider for `email_received`) under the mechanism folder, for
  example `internal/registry/helper/email/gmail/`.
- [x] **Retire the terms "integration" and "subpackage" from prose and
  comments (ADR-0048).** Sweep the remaining uses across package docstrings
  (for example `internal/registry/webhook/webhook.go`,
  `internal/registry/helper/email/email.go`, `internal/components/doc.go`) and
  replace each with the specific word: mechanism, mechanism package, service,
  or config. `internal/components/doc.go`'s engine list drops the `integrations`
  reference (the config is not engine).


## Phase 18: Webhook receivers declared in config (ADR-0049)

Makes webhook symmetric with mcp: two mechanisms by verification scheme
(`hmac-webhook`, `standard-webhook`), receivers declared in `servitor.config.yaml`
under a `webhook:` section (path, scheme, secret), and the raw body delivered to
the workflow, which parses it itself. This replaces the per-service webhook
types (`grist_webhook`, `github_webhook`, `slack_event`, `atomic_event`) and the
`http_webhook`/`standard_webhook` names (ADR-0049).

- [x] **Register the two webhook mechanisms (ADR-0049).** Replace the current
  per-service webhook packages under `internal/registry/webhook/` with two
  mechanism folders: `hmac-webhook` (sign the raw body with HMAC-SHA256, or a
  timestamped, replay-bounded form of it; header, encoding, timestamp header,
  and version prefix are receiver config) and `standard-webhook` (the Standard
  Webhooks envelope: timestamped, replay-bounded, versioned signature). Update
  the `mechanisms` aggregator and the deletion-proof test.
- [x] **Declare webhook receivers in the config (ADR-0049).** Add a `webhook:`
  section to `servitor.config.yaml` (`internal/config`): each receiver names its
  `path`, its `scheme` (`hmac` or `standard`), and, when it verifies a
  signature, a `secret`, plus the `hmac` signing config (header, encoding,
  timestamp_header, prefix). A receiver with an unknown `scheme` is rejected at
  load. The CLI gains management subcommands (`servitor webhook
  add/list/remove`) mirroring `mcp`/`tap`/`target`/`secret`.
- [x] **Resolve a receiver to a mechanism at runtime.** The daemon looks
  up a webhook trigger's receiver by path from the declared config and runs the
  matching mechanism (`hmac-webhook` or `standard-webhook`), following the same
  config-loaded-once pattern as the secret resolver and the MCP connector
  lookup (THREATS.md). A Wafer's webhook trigger names a receiver by path, and
  its type must match the receiver's scheme (rejected at submit; a mismatched
  trigger never matches at serve time).
- [x] **Deliver the raw body.** Both mechanisms deliver the raw body as the
  run's event; the workflow parses it with a `transform` node. Remove the
  built-in per-service parsing (GitHub hex HMAC, Slack `v0:` envelope and
  `url_verification` handshake, Grist and Atomic not-yet-built receivers). The
  GitHub and Slack HMAC variants become config on `hmac-webhook`.
- [x] **Update discovery.** `capabilities` reports the two webhook mechanisms
  and a `webhook/receivers.yaml` listing the declared receivers, mirroring
  `singer/taps.yaml` and `mcp/servers.yaml` (ADR-0049, ADR-0018). SPEC's
  Triggers section is already updated.

## Phase 19: HTTP node executor and flow-node expression evaluation in subprocesses

Closes two gaps in the "every step runs as a subprocess" model (ADR-0008): the
`http` action node is registered with a schema but has no executor, and three
flow nodes evaluate their JSONata expressions in the worker's process instead
of in a subprocess.

- [x] **`http` node executor.** The `http` action node is registered with its
  schema (so it validates and appears in `capabilities`) but has no `Spawn`
  and no executor: running one falls through the worker's plain path and fails
  with `node type "http" has no command to run`. Build the executor following
  the `transform` pattern (ADR-0008): a hidden `servitor __http` subcommand
  that reads the node's config (`url`, `method`, `headers`, `body`, `timeout`)
  and `{event, steps}` input, makes the request with `net/http` against the
  filtered secret env, and writes the structured response (status, headers,
  body) to stdout as the node's result. The capability gains a `Spawn`; the
  worker's plain path runs it with no change. The `headers` values may
  reference a declared secret as `$NAME`, resolved per use from the node's
  filtered env via the shared `refs` component (the same substitution mcp-http
  uses). The result is `{ok, status, statusText, headers, body}`.
- [x] **Flow-node expression evaluation moves to a subprocess.** `wait`
  (its `signal` name), `send-signal` (its `signal` name and `payload`), and
  `rerun-failed` (its `run_id`) evaluate their JSONata expressions in the
  worker's process (`internal/worker/suspend.go`), unlike `switch`, `foreach`,
  and `transform`, which evaluate in a subprocess. Move the expression
  resolution into a subprocess, keeping only the durable store mutation
  (parking the run, delivering the signal, performing the rerun) in the
  worker's process, because the worker owns the single SQLite write connection
  (ADR-0004) and cannot hand it to a subprocess. The split: the subprocess
  computes (evaluates the expression), the worker mutates (the transaction).
  The `wait` timer parsing (`timer.after`/`timer.at`) is not expression
  evaluation and stays in-process. Built as the hidden `servitor __eval`
  subcommand: the worker resolves each expression through a subprocess with the
  node's filtered env and the `{event, steps}` input, then uses the returned
  value to perform the store mutation in one transaction. The daemon integration
  test that exercises a `wait` node now builds the real servitor binary and
  points `selfexe` at it (a test-only override), since the running test binary
  does not serve the hidden subcommands. `dedupe_key` evaluation
  (`internal/worker/worker.go`) shares the in-process pattern; whether it also
  moves is an open question within this task, since it runs for every
  dedupe-keyed node before the dedupe lookup.

**Done when:** an `http` node runs as a subprocess and its response is the
node's result, threaded into downstream `{event, steps}` input; `wait`,
`send-signal`, and `rerun-failed` resolve their expressions in a subprocess and
the store mutation still happens in one transaction in the worker's process;
`go test ./...` stays green.

## Outstanding work

Everything still to do, consolidated from the review of SPEC/ADRs vs the code.

**Cross-idea dependencies.** When implementation work finds a task that cannot be
built yet because it depends on another idea in IDEAS.md that is not yet in the
SPEC/PLAN, break it out as its own small task in the phase it belongs to, marked
`[ ]` with a line saying it is blocked on that idea. Do not silently drop it or
fold it into a completed task. When the blocking idea is worked into the
SPEC/PLAN, this task becomes buildable and is picked up then.

### Bugs / gaps (documented as built but not functional)

- None outstanding. (The cron wiring gap was fixed: submit/enable register scheduled tasks, disable/update unregister them.)

### Not yet built (registered as types, no executor/receiver)

- [x] **`transform` step executor.** Runs as a subprocess of the servitor binary's hidden `__transform` command (ADR-0008), evaluating a JSONata expression (ADR-0020) against the step's `{event, steps}` input threaded forward with the job (ADR-0021). Tested per component (the CLI `__transform` command, the `commandFor` wiring, and the input threading separately); the full spawn-the-binary path is not yet covered by an integration test.
- [x] **`switch` node executor.** Routes to one named branch based on a value; non-chosen branches are skipped and cascade to a rejoin (ADR-0022, ADR-0023).
- [x] **`foreach` node executor.** Fans a named body node out over a list; results collect into an array under the foreach node's name at the rejoin (ADR-0024).
- [x] **`hmac-webhook`/`standard-webhook` receivers (ADR-0049).** The per-service GitHub and Slack webhook receivers were replaced by the two verification-scheme mechanisms: `hmac-webhook` verifies HMAC-SHA256 (header, encoding, and an optional timestamped, replay-bounded form are receiver config), and `standard-webhook` verifies the Standard Webhooks envelope. Both deliver the raw body as the run's event; the workflow parses it itself. See Phase 18.
- [ ] **Grist and Atomic webhook receivers.** With ADR-0049 there are no per-service receiver types; a service becomes a config receiver (`servitor webhook add`) with a suitable scheme. Grist's current webhooks authenticate with a static `Authorization` header, not an HMAC, so declaring it needs a verification approach (or an open receiver) before it is useful; Atomic is unbuilt. These become config entries when the sender's scheme is known, not new mechanisms.
- [x] **`email_received` trigger (Google Workspace).** Polls a mailbox via a gmail helper subprocess (ADR-0027): the trigger carries `host`/`username`/`secret`/`poll`, a scheduled poll runs the hidden `__email_poll` command, and the daemon fans out one run per new email with the parsed email as the event. Uses pinned emersion/go-imap v1. Future providers are new helpers.
- [x] **`completed` trigger.** Registered; fired by another workflow's completion (ADR-0026, ADR-0038). The worker calls a completion callback when a run finishes; the daemon wires it to the trigger receiver, which starts any enabled workflow whose `completed` trigger names the completed workflow, passing an event of `{trigger, from, from_run}`.
- [x] **Secret provider and per-node delivery.** Built in Phase 13: the provider + per-node delivery model (ADR-0032, ADR-0033, ADR-0034, ADR-0035, ADR-0036) with the `env`, `varlock`, and `onbox` providers.
- [x] **`secret-resolution` mechanism group and `secret` role.** Added with the first secret providers in Phase 13 (ADR-0036); `capabilities` renders the group and the available sources.

### Curated helpers (SPEC lists, not built)

- [ ] **`grist`** helper (read, write, list, query).
- [ ] **`slack`** helper (post messages, read events).
- [ ] **`github`** helper (issues, PRs, releases).
- [ ] **`email` send node (SMTP).** The outbound half of email: a generic SMTP node that sends a message. It carries its own `host`/`username`/`secret` config (mirroring `email_received`) and may point at a different account than the one received on; send and receive are independent. Only if many accounts are used would a named-account registry be worth introducing; not needed now.

### Deferred / open decisions

- [ ] **Agent authoring reference.** Committed examples of the Wafer format for every capability, so an agent sees a valid example without running `capabilities`. Deferred until the type set stabilizes; each new type's registry fields should carry `examples` meanwhile.
- [ ] **Worker concurrency limits.** Runs execute as a dependency DAG with fan-out (ADR-0023), but branches run sequentially rather than in parallel.
- [x] **`dedupe_key` expression language.** JSONata via `internal/expression` (ADR-0020), evaluated at execution time against the step's `{event, steps}` input (ADR-0021) and stringified into the key.
- [x] **`transform` expression language.** Settled: JSONata via gnata behind `internal/expression` (ADR-0020). Runs as a subprocess, so no host access; evaluation is bounded.
- [x] **Bespoke per-provider signing schemes.** The receiver verifies the GitHub and Slack schemes (ADR-0025); Grist and Atomic remain open.
- [x] **Varlock signal handling.** `varlock run` forwards SIGTERM/SIGINT to the runner child and propagates its exit code, and the self-heal now execs varlock (`--inject vars`) so the process the operator launches becomes varlock, giving a clean `manager -> varlock -> runner` tree. No minimal init needed (ADR-0029). (The varlock boot path itself is removed by Phase 13, ADR-0034; the redaction decision ADR-0029 bundled is re-homed in ADR-0050.)
