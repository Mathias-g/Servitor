# Servitor implementation plan

Build order with dependencies and a clear "done" for each phase. The design lives in [SPEC.md](SPEC.md); the decisions live in [docs/adr/](docs/adr/); this is just the sequencing.

**PLAN.md is append-only.** Phases are numbered sequentially and never renumbered; an existing phase is never overwritten or replaced to describe new work. When new work does not belong in an existing phase, add it as a new phase with the next number (for example, if the last phase is 12, the new phase is 13). An earlier phase may be superseded by a later one, but the superseded phase stays in place as a record of what was built. Only reorder or merge phases when the developer explicitly asks.

**Partial tasks.** A partially-finished task is split into a done part (`[x]`) and a not-done part (`[ ]`), or the `[x]` line is annotated with what is deferred. Do not leave a task half-done with no marker of what remains; a `[x]` means "its intended scope is done" and a `[ ]` means "not done", with the text saying exactly what is left.

Current state: the Go project is scaffolded, the enforcement gates are wired (`make check`, adrlint, pre-commit, CI), and the daemon + loopback control protocol, Wafer model/validation, capability discovery, dry-run DAG resolution, Honker integration, node execution (shell, singer-tap, singer-target, mcp-call), triggers/webhooks (http_webhook, standard_webhook, manual), the varlock integration (Phase 8), the secret-resolution model (Phase 13, ADR-0032 through ADR-0036), SKILL.md, run inspection, packaging/release, the Singer integration, the MCP integration, and the declared integrations config (ADR-0018) are built. The daemon owns a WAL SQLite file with the Honker extension loaded; the transactional atom ({result, dedupe, downstream, claim_ack} in one commit) is a tested primitive, and for Singer nodes the bookmark is part of that same commit; the worker loop runs nodes as subprocesses with env filtering and dedupe; inbound webhooks (Standard Webhooks + generic HMAC) and manual triggers are served with event persistence; secrets resolve per node through a pluggable provider (the `env`, `varlock`, and `onbox` sources) with per-node, per-subprocess delivery and the failure semantics of Secret invalidity and rotation, replacing the varlock boot path (Phase 8) which is removed; a `SKILL.md` teaches agents the discover-author-dry-run-PR workflow; the full CLI command set (plus `mcp`/`tap`/`target`/`secret`) is implemented; `make release` drives the release flow (ADR-0012, ADR-0013, ADR-0014); Singer taps/targets run as subprocesses with bookmark state persisted in the same transaction as each node's result (ADR-0016); and MCP servers are declared in a local `servitor.integrations.yaml` and probed at refresh (ADR-0018). Not yet functional: the provider-specific-webhook receivers for Grist and Atomic, and the curated helpers (send side of email included). See "Outstanding work" below. The runner has no workflow registry consulted by the worker for control flow; runs are built from a Wafer into a dependency DAG with dependency-counter fan-out (ADR-0023), and the trigger receiver matches against the stored registered workflows.

## Phase 1: Daemon and control protocol (foundation)

Everything else runs inside the daemon and is reached through the control protocol.

- [x] A long-lived runner daemon process that owns a SQLite file (WAL mode) and binds `127.0.0.1` only (ADR-0009). (SQLite file ownership is deferred to Phase 5 with Honker; the daemon lifecycle and loopback-only bind are built.)
- [x] A loopback control protocol (HTTP over `127.0.0.1`) between the CLI and the daemon, independent of argument parsing (ADR-0005).
- [x] `servitor run` boots the daemon; `servitor stop` drains and shuts it down.
- [x] The daemon refuses to bind a non-loopback interface (ADR-0009).

**Done when:** `servitor run` starts a daemon, the CLI talks to it over loopback, and `servitor stop` drains and shuts it down. `servitor capabilities` and `servitor dry-run` are the first commands wired through this protocol. (Lifecycle and protocol are done; wiring `capabilities`/`dry-run` happens in Phases 3-4.)

## Phase 2: Wafer model and validation

The artifact. Everything else reads, validates, and executes Wafers.

- [x] A Go representation of the Wafer (name, `on:` triggers, `nodes:`).
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
- [x] Trigger types: `http_webhook`, `standard_webhook`, `cron`, `manual`. Provider-specific types and `internal` are deferred; registration (`submit`/`enable`/`disable`) and manual `trigger` are wired through the control plane.
- [x] **Cron triggers wired.** Submitting or enabling a workflow with a `cron` trigger registers a scheduled task on the Honker scheduler; disabling or updating unregisters it. A `cron` trigger fires on its schedule.

**Done when:** a signed webhook is verified, the event persisted, the workflow matched, and a run enqueued. (Done for `standard_webhook`/`http_webhook`/`cron`/`manual`/`internal`; provider-specific and `email_received` remain.)

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
- [x] **Per-node, per-subprocess delivery** (ADR-0033): resolve each secret at the moment its node runs, hand it to that one subprocess's filtered env, hold nothing past the subprocess. Redaction keeps operating on the running node's filtered env (ADR-0029). Resolve only what the registered Wafers reference; drop a secret whose last Wafer is removed.
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
- [x] **Secret invalidity and rotation** (SPEC: Secret invalidity and rotation): reactive retry on auth failure with a fresh resolve (bounded, global `secret_retry_count` default); source-unreachable retries with backoff; secret-missing fails fast; the webhook signing key is verify-only, resolved per use, with no rollover window. (The resume-from-failure modes and the failed-secret event are their own tasks below.)
- [ ] **Failed-secret event emission** (SPEC: Secret invalidity and rotation): when a secret-authenticated node fails after retries are exhausted, emit an event a workflow can trigger on (reusing the `internal` trigger's completion-callback plumbing) so the operator can wire their own notification. Not idea-blocked, but it depends on the broader run-failure-resolution path (a failed node's run currently does not transition to `failed` or fire the completion callback); build that general resolution together with it.
- [ ] **Resume-from-failure modes** (SPEC: Secret invalidity and rotation): how "run it again" after a supplied secret behaves, settable globally in the servitor config and per Wafer with a CLI override: `continue` (resume from the failed node), `restart` (re-run from the top, safe only when side-effecting nodes declare `dedupe_key`), `discard` (drop the failed run). **Blocked on the "Suspended waits" idea** (IDEAS.md): these reuse that idea's suspend/resume machinery (a continuation holds the next node's `{event, steps}` input; resuming re-enqueues it), which is not yet in the SPEC/PLAN. Implement this when "Suspended waits" is worked into the SPEC/PLAN.

**Done when:** a node's secret is resolved per node at spawn and dies with its subprocess, the daemon no longer holds the full set or boots under varlock, an agent discovers the available secret sources and names via `capabilities`/`secrets.yaml`, and secret invalidity/rotation behaves per the SPEC. (The resume-from-failure modes and the failed-secret event are separate tasks above and are not part of this phase's done-when.)

## Cross-cutting

- [x] Each CLI command implemented per the SPEC's command set and its mapping to daemon operations.
- [x] Exit codes carry the signal (0 ok, 1 operation failed, 2 usage error, 3 daemon not running).
- [x] The control plane stays gated and loopback-only throughout (ADR-0009); the deploy path is CI/CD-gated and operator-owned/documented (ADR-0019).
- [ ] Agent authoring reference: committed examples of the Wafer format for every capability (core, singer, mcp-call, curated helpers, webhooks), so an agent sees a valid example without running `capabilities`. The generator is already generic, so this is deferred until the type set stabilizes; until then each new type's registry fields should carry `examples`, and `servitor capabilities` renders them on demand.
- [x] Declared integrations config (ADR-0018): replace PATH-prefix discovery with a single declared config (`servitor.integrations.yaml`, per-mechanism sections for MCP servers and Singer taps/targets, each with exact command and env) as the source of what `capabilities` reports, plus a management CLI (`servitor mcp`/`tap`/`target` add/list/remove) that writes entries and delegates the actual software install to the ecosystem's package managers.

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
- [x] **Provider-specific webhook receivers (GitHub, Slack).** `github_webhook` verifies HMAC-SHA256 in `X-Hub-Signature-256`; `slack_event` verifies HMAC-SHA256 over `v0:<timestamp>:<body>` in `X-Slack-Signature` with a replay-bounding timestamp window, and answers Slack's `url_verification` handshake.
- [ ] **Provider-specific webhook receivers (Grist, Atomic).** `grist_webhook` and `atomic_event` are registered as types but `isWebhookType` does not serve them, so they cannot match inbound events yet. Note: Grist's current webhooks authenticate with a static `Authorization` header, not an HMAC, so its receiver needs the scheme clarified before building.
- [x] **`email_received` trigger (Google Workspace).** Polls a mailbox via a gmail helper subprocess (ADR-0027): the trigger carries `host`/`username`/`secret`/`poll`, a scheduled poll runs the hidden `__email_poll` command, and the daemon fans out one run per new email with the parsed email as the event. Uses pinned emersion/go-imap v1. Future providers are new helpers.
- [x] **`internal` trigger.** Registered; fired by another workflow's completion (ADR-0026). The worker calls a completion callback when a run finishes; the daemon wires it to the trigger receiver, which starts any enabled workflow whose `internal` trigger names the completed workflow, passing an event of `{trigger, from, from_run}`.
- [x] **Secret provider and per-node delivery.** Built in Phase 13: the provider + per-node delivery model (ADR-0032, ADR-0033, ADR-0034, ADR-0035, ADR-0036) with the `env`, `varlock`, and `onbox` providers.
- [x] **`secret-resolution` mechanism group and `secret` role.** Added with the first secret providers in Phase 13 (ADR-0036); `capabilities` renders the group and the available sources.

### Curated integration helpers (SPEC lists, not built)

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
- [x] **Varlock signal handling.** `varlock run` forwards SIGTERM/SIGINT to the runner child and propagates its exit code, and the self-heal now execs varlock (`--inject vars`) so the process the operator launches becomes varlock, giving a clean `manager -> varlock -> runner` tree. No minimal init needed (ADR-0029). (The varlock boot path itself is removed by Phase 13, ADR-0034.)
