# Servitor

> **Workflow automation for the agentic stack.**
> **Self-hosted. MIT-licensed.**

A self-hosted workflow automation runtime designed from the ground up for AI agents to author and operate. Workflows are declared as YAML files (which we call **Wafers**); a long-lived runner daemon executes them durably; a CLI control plane exposes the whole thing for humans and agents alike.

The workflow is fully defined by the Wafer file, nowhere else. There is no built-in web UI; if a UI exists someday, it generates Wafers and submits them through the same interface agents use.

---

## What this is for

If you have a stack of business tools (Grist for accounting and CRM, Atomic for knowledge, Slack for chat, GitHub for code, email somewhere else, plus the other things real companies use) you eventually want them to talk to each other. The existing options for that don't quite fit:

- **Zapier and similar SaaS.** Proprietary and hosted: your data and your workflows live on someone else's platform, with the lock-in that implies.
- **n8n, Activepieces, Windmill.** Self-hostable, but open-core, the parts you eventually need (SSO, audit, scale) sit behind an enterprise tier.
- **Temporal.** Built for application developers writing durable business logic inside a service, not for operators wiring up SaaS tools, a different audience.
- **Meltano.** ELT-shaped (scheduled extracts into a warehouse), which is a data-team model, not a general workflow one.

Servitor is an opinionated take, built because none of the existing options were what I wanted: a small, code-first, agent-friendly workflow runner for connecting the tools a small company actually uses. One process, one SQLite file, genuinely open source (MIT, no open core, no enterprise tier, no feature paywall).

---

## What this is not

Decisions already made. Revisiting any of these means reopening something settled deliberately.

- **Not a Zapier replacement.** Zapier's value is its 7000+ prebuilt connectors and its visual builder. This project's value is honest connectors, code-first authoring, and agent-friendliness. Different audience.
- **Not an MCP server.** The control plane is a CLI, consumed by agents through a skill. MCP is a possible future adapter over the same daemon protocol, deferred until there is a concrete user (ADR-0005).
- **Not a multi-tenant SaaS workflow platform.** SQLite's single-writer model rules that out by design.
- **Not a data orchestration tool.** Airflow, Dagster, Prefect exist for that.
- **Not a CI/CD system.** GitHub Actions, Drone, Woodpecker exist for that.
- **Not a unified API.** Singer and per-service helpers are how services are reached. There is no normalized cross-service schema.

---

## Why agent-first changes the design

Most workflow tools were designed for humans clicking through a builder, with an API bolted on. An agent using such a tool is a second-class citizen: it has to reverse-engineer what the UI assumes, guess at validation rules, and recover from opaque errors.

Designing for agents first changes specific decisions:

- **The artifact is the Wafer, not a database row.** A Wafer is the YAML file that defines a whole workflow: triggers (`triggers:`) that start the run and nodes (`nodes:`) that do the work. Agents read, write, diff, and version-control the same file a human would. There is no "form state" living somewhere the agent can't see.
- **Capability discovery is a first-class operation.** `servitor capabilities` returns every capability (trigger, action node, or flow node, with its role and delivery), every declared secret, and every Singer tap available, each with its JSON Schema and an example rendered from that schema. An agent never has to guess what fields a node takes.
- **Validation errors are structured, not stringified.** Errors are returned as JSON with paths, codes, and suggestions. An agent that submits a workflow with `type: slak` gets back an `unknown_node_type` error with `suggestion: slack`, the way an IDE would flag the typo. (See the Structured validation errors section for the full shape.)
- **Dry-run is a real primitive.** `servitor dry-run` resolves the entire workflow and returns the DAG the runner *would* execute. No nodes run, no external services are contacted, nothing is persisted. It reports the workflow's declared secret names (redacted, never values) and warns with a `missing_secret` code when one is not resolvable by the configured provider, so an agent can verify structure, secret availability, and node configuration before committing.
- **The same CLI serves humans and agents.** No private API the agent doesn't have access to. If a future UI exists, it talks to the same control plane.

These are not nice-to-haves bolted on after the fact; they are why this project exists as a separate thing rather than as a fork of an existing runner.

---

## How it works

A workflow is a YAML file (a Wafer) declaring:

1. **Triggers.** What events cause the workflow to run (webhooks, cron, manual invocation, internal events).
2. **Nodes.** What the workflow does, expressed as a DAG of typed node invocations. A node is either an action node (does work) or a flow node (routes or fans out).

The runner reads the Wafer, validates it, registers any triggers it declares, and waits for events. When an event arrives, the runner enqueues a workflow run, workers claim jobs and execute nodes, results are persisted, and downstream nodes fire as their dependencies complete.

Example Wafer:

```yaml
name: notify_on_new_lead
triggers:
  - type: hmac-webhook
    path: /hooks/grist-leads
nodes:
  - name: post_to_slack
    type: transform
    expression: |
      $body := $json($event.body);
      {"text": "New lead: " & $body.name}
```

That's it. Submit it via CLI, enable it, and the next time an HMAC-signed
request hits `/hooks/grist-leads` (a receiver you declare in the config, named
after Grist's webhook, ADR-0049), a run fires and its `transform` parses the raw
body.

---

## The whole thing, end to end

Read this once and the rest of the document fills in the details. The split: steps 1, 2, 6, and 8 are the runner's job (receive, verify, persist, execute). Steps 3, 4, 5, and 7 are the author's job, human or agent (decide what to build, write it, submit it, react to results). The runner never decides what a workflow should do; that is always the author, because the author has the context.

1. **Start the runner.** `servitor run` boots the daemon, which obtains secrets on demand through its configured provider (SPEC: Secret resolution). One process owns the SQLite file.
2. **Discover what's possible.** `servitor capabilities` lists capabilities (triggers, action nodes, and flow nodes, with roles and delivery), secrets, and Singer taps with schemas. An agent reads this instead of guessing.
3. **Write a Wafer.** A human edits a YAML file, or an agent generates one from the capabilities schema. The Wafer declares triggers and nodes.
4. **Dry-run it.** `servitor dry-run ./wf.yml` validates and resolves the workflow without running anything, so the author sees the DAG and the declared secrets (redacted, with a `missing_secret` warning when one is absent).
5. **Deploy via the pipeline.** The agent (or human) opens a pull request for the Wafer; the pipeline dry-runs it and applies it on the box with `servitor submit`, then `servitor enable <name>` registers its triggers (ADR-0009).
6. **Run.** A webhook arrives, a cron fires, or `servitor trigger <name>` runs it manually. Workers execute nodes durably; downstream nodes fire as dependencies complete.
7. **Inspect and react.** `servitor runs` / `servitor run <id>` shows history and outcomes. The author fixes a Wafer or a node and resubmits.
8. **Stop.** `servitor stop` drains and shuts the daemon down. Crashes are recovered by the queue on restart.

---

## Architecture

The system is composed of well-defined open-source pieces, each doing its narrow job. Small interfaces compose well. The runner is written in Go (ADR-0004): a single binary that spawns a subprocess per node and owns a SQLite file.

The runner is a single OS process that owns the SQLite file and its single write connection. Inside that process is a pool of node executors. When a node executor claims a job, it runs the node as a subprocess (see Node execution modes). There is exactly one execution mode; every node, including pure-computation nodes, runs as a subprocess.

How a node runs:

- The subprocess is launched with a filtered environment containing only the secrets the node's YAML declared it needs. This is real OS-level isolation, not "we promise not to read the variable."
- The subprocess writes its result to stdout (structured JSON) and exits.
- The parent runner process reads the result and commits it to SQLite, along with the enqueue of downstream nodes, in one transaction.

This means SQLite writes are serialized through the parent process, which is the only thing holding a write connection. SQLite's single-writer rule is honored by design, not worked around.

Go keeps subprocess startup fast (roughly a millisecond), which is why every node runs as a subprocess rather than in-process (ADR-0008). Read concurrency is fine: workers reading their own claim, the control plane reading workflow state, and the trigger receivers reading config can all happen against WAL-mode SQLite without blocking the writer.

### Dependencies and standards (reference)

The runner is a single Go binary, but it composes external pieces and speaks external standards. One of these is a runtime dependency the runner actually pulls in (Honker); the others are standards it adheres to by spawning external tools or implementing a scheme (Singer, Standard Webhooks). Only the runner itself is Go. Secrets are handled separately, through a pluggable provider (Secret resolution), not as a runtime dependency on any particular store.

#### Honker, durable queue and scheduler (runtime dependency)

[Honker](https://honker.dev) is a SQLite extension that adds Postgres-style NOTIFY/LISTEN semantics to SQLite, plus a durable work queue, event streams, and a cron scheduler. One `.db` file is the entire system: no Redis, no separate broker.

The extension is a native loadable library (`libhonker_ext.so`) the runner loads at startup. It is not committed to the repo; the operator supplies it and points the runner at it via `HONKER_EXTENSION_PATH` (or a flag). The runner refuses to boot the durable store without it (ADR-0011).

What we use it for:

- **Workflow run queue.** Each node is a job. Workers claim, execute, and ack.
- **Crash safety.** If a worker dies mid-job, the claim expires after a visibility timeout and another worker reclaims. After max attempts the job lands in a dead-letter table.
- **State persistence.** Every workflow's run history, node outcomes, Singer state bookmarks, and pending events live in the same SQLite file.
- **Transactional commits.** A node's completion writes commit as a single atomic SQLite transaction rather than as separate operations. This is the mechanism behind the transactional fan-out guarantee; see step 8 of the Execution model for what the transaction contains and why it must never be split.
- **Scheduler primitive.** Cron-style triggers use Honker's built-in scheduler.

#### Singer, data movement (standard)

[Singer](https://www.singer.io) is an open spec for data movement. A *tap* is a CLI that emits records from a source as JSON; a *target* is a CLI that consumes records into a destination. Hundreds of taps exist across the ecosystem, most MIT-licensed, many actively maintained through [Meltano Hub](https://hub.meltano.com).

Singer is the record-stream layer of the runner: schemas, streams of records, and bookmark state. Most taps in practice run in batch (pull everything new since last bookmark, exit), but the spec itself is a streaming protocol and continuous taps exist; the runner treats both the same way.

What we use it for:

- **`singer-tap` capability.** Drop in `tap-stripe`, `tap-github`, `tap-hubspot`, etc. with config, and records flow into the workflow. The runner writes the tap's config (and prior bookmark, and a selected-stream catalog, when present) to temp files and invokes it with `--config`/`--state`/`--catalog`; the tap emits Singer protocol messages (SCHEMA, RECORD, STATE) on stdout, and the runner returns the records and the last STATE value as the next bookmark (ADR-0016). Stream selection is a `catalog` field copied verbatim from `servitor capabilities`; discovery runs once at refresh, never per node.
- **`singer-target` capability.** Built-in targets include `target-grist`, `target-atomic`, plus any community target. A target receives its config via `--config <file>` and the records to consume on stdin.
- **State management.** Each tap's incremental sync state (the bookmark of last synced position) is stored in Honker and passed back into the next tap invocation.
- **Self-describing schemas.** Each tap publishes its config schema, available streams, and record schemas via `--about` and `--discover`. The control plane exposes this for agents to introspect.

Singer nodes and curated helpers can both perform actions against the same external service (a `target-grist` and the `grist` helper's `write_row` both write to Grist), so the distinction isn't action vs not-action; it's the *shape* of the node. Singer nodes consume or emit streams of typed records with bookmark state. Helpers make discrete calls with discrete inputs and outputs.

#### Standard Webhooks, modern webhook reception (standard)

[Standard Webhooks](https://www.standardwebhooks.com) is a community-driven spec for webhook signing and verification, adopted by OpenAI, Anthropic, Google Gemini, Supabase, Twilio, Vanta, and others.

What we use it for:

- **A `standard-webhook` trigger type.** Any compliant producer works out of the box with one verification library.
- **Forward compatibility.** New services adopting the spec become trigger sources with zero per-service code.

For non-compliant services that sign with HMAC-SHA256, the `hmac-webhook` trigger type covers them as receiver config: which signature header, which digest encoding, and optionally a timestamped, replay-bounded form of the body are all declared per receiver in `servitor.config.yaml` (ADR-0049), never compiled in per service.

### Node execution

Every node runs as a subprocess. There is no in-process mode (ADR-0008). When a node executor claims a job, it launches a subprocess with a filtered environment containing only the secrets that node declared, the subprocess writes its result as structured JSON to stdout and exits, and the parent commits the result.

The subprocess is the isolation boundary. Because nothing runs inside the runner's own process, there is no "not a sandbox" surface: code that might be untrusted or buggy is contained by OS process isolation, and since a node cannot see secrets it did not declare, its environment contains nothing worth stealing. This is why Go's cheap subprocess startup makes a uniform subprocess model the simplest and safest choice.

### Graceful shutdown

Crash safety (covered in the execution model section) handles the runner dying unexpectedly. Graceful shutdown handles the runner being stopped on purpose. The two share a backstop but aren't the same path.

On `SIGTERM`, the runner drains with a deadline:

1. **Stop claiming.** The runner immediately stops claiming new jobs. New triggers still persist their events to Honker (so nothing is lost), but no new runs begin execution.
2. **Let in-flight nodes finish.** Nodes already running are given up to a configurable drain timeout to complete. Each that finishes commits its normal fan-out transaction (result, dedupe record, downstream enqueues, claim ack, all four in one commit), exactly as in steady state.
3. **Hard-stop stragglers at the deadline.** Any node still running when the drain timeout expires is terminated. The runner does not commit a result for these; it leaves their claims to expire. They then become ordinary crash-recovery cases: the visibility timeout re-issues the claim, and the `dedupe_key` contract governs whether re-running is side-effect-safe.
4. **Release the write connection.** Once draining ends, the runner closes its SQLite write connection cleanly so the next instance can acquire it without waiting on a stale lock.

A second `SIGTERM`, or a `SIGKILL`, skips draining and stops immediately; everything in flight becomes a crash-recovery case.

---

## Secret resolution

Secrets are obtained through a narrow, pluggable **provider** (ADR-0032): an
in-process interface, roughly `Resolve(ctx, nodeName, secretName) -> value`,
with caching and expiry as provider properties and failure semantics that
distinguish the source being unreachable, the secret being missing, and the
secret being stale or invalid (Secret invalidity and rotation). A provider
encapsulates its own mechanism (its own on-box unlock: a local key, TPM/vTPM, or
an off-box KMS call; or, for the pull arrangements, a store credential).
Multiple providers coexist, with per-secret routing and optional failover. A
secret capability is a distinct **role**, `secret`, and the available secret
sources are mechanisms under the `secret-resolution` mechanism group (ADR-0036);
that group is what an agent consults to know the valid `source` values for
`secrets.yaml`.

The recommended mechanism is **push-based on-box ciphertext**: CI/CD delivers
the material during deploy, the value is sealed to the box, and Servitor
decrypts locally when a node needs it. At rest, secrets are never plaintext on
disk: TPM is the preferred unlock tier, with a non-TPM fallback (an off-box KMS
key or a strong local-key file) that still holds the line against plaintext.

The key-custody distinction that makes this a real security difference: an
**off-box or hardware-bound key is non-exportable** (a KMS key or a TPM seal
cannot be copied off), so a thief who steals the disk or a backup gets only
ciphertext and cannot decrypt it anywhere else. This is the genuine win over
the peers, who keep a *copyable* key in the same environment as the ciphertext.
But it is not a complete boundary: it does not protect the value in the runtime
window (the plaintext is in the runner's memory and the subprocess either way),
and it does not stop code already running as the runner's user from calling the
decryption service or TPM to obtain the value on demand. So at-rest key custody
protects against disk/backup theft, not against a compromised daemon.

Recoverability follows from the same arrangement: the box holds only derived
ciphertext, and the origin (the store, or the material CI/CD pushes) lives
elsewhere, so losing the box costs nothing durable, you simply run the setup
again. The non-exportable key protects only against the thief who steals the
disk, not against a lost box.

Other arrangements are supported for their niches: a pull-based external store
(for stores fast enough to resolve from directly per node, such as AWS Secrets
Manager), a pull-based on-box ciphertext store (for slow stores, fetched once
into the local copy and then resolved per node), and plain environment (a
dev/testing fallback). The three axes a mechanism bundles (ingress, storage,
unlock) are composed within the provider in code; they are not independently
configured at runtime. The axis options are shared internal components, not
per-mechanism reimplementations: a mechanism stays the deployable unit (one
provider), but each mechanism calls the same components for the options it
uses (one TPM unlock, one KMS call, one on-box ciphertext store, and so on, in
a shared internal library). This keeps the code DRY without making the axes
runtime-pluggable seams, which would force maintaining every combination.

**Per-node, per-subprocess delivery is the security invariant** (ADR-0033): a
secret is resolved at the moment its node runs, handed to that one subprocess's
filtered env, and held by the runner only while that subprocess is alive. A
node's secret dies with its subprocess. A resolved secret may flow only to the
declaring node's subprocess, or to an external provider for the purpose of
authenticating, and is eliminated after. The runner resolves exactly the union
of secrets the registered Wafers reference, so if the last Wafer using a secret
is removed, the runner stops resolving it.

Two honest limits are part of the model, not caveats to hide:

- **"Gone after use" means no longer reachable, not erased from memory.** Go
  strings are immutable and the garbage collector does not zero memory, so there
  is no way to force a secret's bytes out of RAM. The invariant is reachability:
  once the runner drops its reference, no running code in the runner can reach
  the value, and the subprocess that held it is gone. A fully memory-compromised
  process (a core dump or a read of the runner's heap) could still find stale
  bytes, but an attacker with that level of memory access can read the next
  resolve in plaintext anyway, so this is out of scope.
- **Redaction keeps operating on the running node's filtered env.** A node's
  captured output is scrubbed of any secret value the node was granted by
  scanning the node's filtered env (ADR-0050). Per-node delivery holds a value
  only while its node runs, which is exactly the window redaction needs, and
  redaction only ever scrubs values the node was granted. There is no global
  secret map to redact from; redaction operates per node, and this is what lets
  per-node delivery compose with it.

**The auth-before-side-effect contract.** A node's secret-authenticating call
is its first outbound call and fails before any side effect, so that a retry on
a stale secret (Secret invalidity and rotation) can never redo a side effect.
Side-effecting nodes still declare a `dedupe_key` as a belt-and-suspenders
guard.

**The webhook signing key is resolved per use.** The webhook receiver resolves
the current signing key fresh each time it verifies a message, and the value is
held only for that one verification, not for the runner's life. There is no
rollover window: a message that does not verify with the current key is rejected
and logged, with no retry (an inbound webhook is sent once). The runner's only
life-long-held secrets are the pull-provider store/KMS credentials it needs to
authenticate outbound.

### Secret invalidity and rotation

A secret can become invalid at any time, whether it is in active use or idle: it
can expire, be revoked, or be rotated while nothing is running. Per-node
delivery makes fresh values free: each node resolves fresh and its value dies
with its subprocess, so once the store holds a new value the next resolve picks
it up. Two cases remain. A secret can go bad *while idle*: the next node that
needs it fails at the moment of use, and a fresh resolve returns the new value
once the store is updated; this only needs the resume-from-failure behavior
below. The harder case is a long-lived holder (a persistent node connection such
as a websocket, and the pull-provider credentials the runner uses to
authenticate out): a value is held across a connection's life, and a fresh
resolve cannot reach an already-open connection, so the holder must actively
react. Invalidity is handled reactively, on failure rather than on a schedule:

- A node whose auth fails (a 401/403, a dropped or rejected connection) fails
  and reports it to the runner. The runner respawns the node's subprocess with a
  freshly resolved secret, up to the configured retry count. Each retry is a new
  subprocess spawn with a new resolve; the failed subprocess's value dies with
  it. If the store value rotated, a fresh resolve gets the new one and the
  retried request succeeds. This composes safely with `dedupe_key` only because
  of the auth-before-side-effect contract (Secret resolution): auth is the
  node's first outbound call and fails before any side effect, so a retry never
  redoes one.
- Retries are bounded: a configured number of attempts before the node fails, so
  a genuinely bad secret does not loop forever. Initially a single global default
  in the servitor config (for example `secret_retry_count: 3`), applied to all
  nodes; a per-node or per-secret override can come later. When retries are
   exhausted, the node fails with a distinct error (`secret_auth_failed`,
   distinct from `missing_secret`) in the same structured `path`/`code` shape as
   other node errors, written to the run's log, and the run is marked failed
   and emitted as a `failed` event a workflow can trigger on (a distinct signal
   from the `completed` trigger, ADR-0039), so the operator can wire up their
   own notification.

The three failure semantics a provider can return are not handled the same way:

- **Stale/invalid (auth fails on use)** is the reactive-retry case above:
  bounded retry with a fresh resolve, because a fresh resolve may get a rotated
  value.
- **Source unreachable** (the store/provider is down) is a transient
  infrastructure failure. Retry with exponential backoff before failing, since
  the source may come back. If it stays down, fail with a distinct error.
- **Secret missing** (declared but no value in the store) is not transient, so
  retrying is pointless; fail fast, no retry, with a `missing_secret`-style
  error. The operator adds the value and resumes from the failed node.

The model never proactively polls for rotation and never holds a value longer
than it must; it only re-resolves when a failure makes a fresh value legitimate,
which is exactly what the egress rule permits.

Because the runner does not pre-check that every node's auth will work before a
run starts, a run can fail partway through its DAG with some nodes completed and
others not. Supplying the new secret should resume the run from the failed node,
not restart it from the top: restarting would re-run the already-completed
nodes, redoing their side effects (and, for nodes without a `dedupe_key`,
redoing them unsafely). Resuming from the failure point leaves the completed
nodes as they are and runs only the failed node and its remaining successors;
this reuses the suspend/resume machinery sketched for parked runs, with the
failed node as the continuation point. What "run it again" means is
configurable, settable globally in the servitor config and per Wafer, with the
CLI able to override it for a specific run:

- **continue**: resume from the failed node, leaving completed nodes and their
  side effects as they are. The default, and the safe choice for the
  secret-invalidity case.
- **restart**: re-run from the top. Redoes completed side effects, so it is only
  safe for a Wafer whose side-effecting nodes all declare a `dedupe_key`.
- **discard**: drop the failed run entirely and do not re-run it, cleaning up
  any partial state.

Rerun is **general**: it applies to any failed run, whatever the cause of the
failure (a secret/auth failure, a transient 5xx, a shell command failing, or any
node dead-lettering after retries). When a node dead-letters, the runner stores
that node's self-contained job as a failed continuation (so `continue` can
resume exactly there) and marks the run failed. The behavior is set by `servitor
rerun <run-id> [--mode continue|restart|discard]`, by a `rerun-failed` node
(which re-runs the run named by its `run_id` expression, defaulting to
`event.from_run`, the failed run whose `failed` trigger started the workflow),
and by a per-Wafer `on_failure` field as the default mode (ADR-0044). A
`failed` trigger is a normal trigger: it starts a new run of a watcher workflow;
it does not itself re-run the failed workflow, so it cannot loop on its own (a
watcher that re-runs a failing workflow repeatedly is the author's responsibility
to bound).

## Control plane

The runner is a long-lived daemon. The control plane is a CLI that talks to it, plus the daemon control protocol the CLI is one client of. Everything an agent or human does runs through this interface; there is no separate API. (The decision and rationale are in ADR-0005.)

### The CLI

The command set, grouped by what you're doing. These are the contract humans and agents share. `servitor capabilities` writes schemas to files that the agent reads on demand, so big JSON schemas never sit in the agent's context; this is the token-efficiency payoff of a CLI over an MCP server.

```
servitor run                        # boot the runner daemon
servitor capabilities               # write capability/trigger/secret/tap schemas + derived examples to files
servitor secret add|list|remove     # manage declared secrets
servitor dry-run <wafer>            # validate and resolve without executing (--json for structured)
servitor submit <wafer>             # validate and register a workflow
servitor update <wafer>             # replace a workflow's definition
servitor enable <name>              # register a workflow's triggers
servitor disable <name>             # unregister without deleting
servitor trigger <name> [inputs]    # manual run with optional inputs
servitor resume <signal-name> [payload]   # resume a parked (waiting) run
servitor rerun <run-id> [--mode ...]      # re-run a dead-lettered (failed) run
servitor runs                       # list run history
servitor run <id>                   # inspect one run
servitor cancel <id>                # stop an in-flight run
servitor stop                       # drain and shut the daemon down
```

Each command maps to an operation on the daemon; the full list and the exit codes (0 ok, 1 operation failed, 2 usage error, 3 daemon not running) are pinned in the command reference as the CLI is built.

### The daemon control protocol

The CLI talks to the daemon over a plain loopback protocol (HTTP or unix socket; the transport is an implementation detail). The protocol is kept independent of argument parsing, so a future MCP adapter can sit beside the CLI without a rewrite (ADR-0005). It exposes the same operations as the CLI: discover, dry-run, submit, update, enable, disable, trigger, inspect, cancel, stop.

### How the control plane is reached

The daemon binds `127.0.0.1` only. It has no direct network surface, and changing behavior is gated (ADR-0009). There are two distinct paths:

- **Deploy (changing behavior): CI/CD-gated.** Wafers live in a git repo. The agent authors them and submits a pull request; a pipeline validates, dry-runs, and applies them via `servitor submit`/`update`/`enable`/`disable` on the box itself. The agent's write is a reviewed PR, never a direct socket to the runner.
- **Operate (inspect, trigger, cancel): operator-gated.** Inspection (`runs`, `run <id>`) is read-only. State-changing operations (`trigger`, `cancel`) and `stop` run on the box by the pipeline or an operator, not through a wide-open agent socket.

Getting onto the box is the operator's existing access (SSH or VPN). The CLI stays loopback-to-daemon; SSH/VPN is what brings the operator or pipeline onto the box. There is no public HTTP endpoint for the control plane.

### Consuming Servitor as a skill

Agents learn the CLI from a shipped `SKILL.md`, the command reference that teaches an agent how to use Servitor. The agent discovers capabilities on demand (`servitor capabilities`), generates or edits a Wafer, dry-runs it, and opens a pull request for it; the pipeline applies it on the box (ADR-0009). Where the agent runs on the same box as the runner (local development, the pipeline's own runner), it can also inspect runs and trigger/cancel directly through the CLI. This mirrors the skill-first model: a CLI stays quiet until the agent types a command, so Servitor costs nothing in the agent's context until it is used.

---

## The Wafer

A Wafer declares a workflow's triggers and nodes. A **trigger** (under `triggers:`)
starts the run; it is not a node. The **nodes** (under `nodes:`) are what the
workflow does, all part of the run's DAG. Every capability is one of three
things: a trigger, an action node, or a flow node. An **action node** (for
example `http`, `shell`, `transform`) does work mid-run. A **flow node** (for
example `switch`, `foreach`) routes or fans out, and does no external work
itself. Every capability, whatever its role, runs as a subprocess (ADR-0008).
Triggers carry a `delivery` tag (instant, polling, scheduled, event, manual)
describing how they start a run. Both lists are representative, not exhaustive;
`servitor capabilities` returns the authoritative live set, each entry with its
JSON Schema, role, and delivery.

### How an agent discovers capabilities and connectors

Before writing a Wafer, an agent needs to know what the *target* server supports and how to use it. `servitor capabilities` answers both, and it is a per-server query: the authoritative set is what that runner has compiled in (its capabilities), what the operator has declared for secrets (names and metadata, never values; ADR-0035) and connectors (Singer taps, MCP servers; ADR-0018). The agent asks the server rather than trusting a doc, because the answer differs per deployment.

For each capability, `capabilities` returns:

- its **JSON Schema** (fields, required, types, constraints), and
- an **example Wafer fragment** rendered from that schema.

The example is **derived from the schema, not written by hand**: the structural skeleton (required fields in order, nested objects and arrays) is generated from the schema, and meaningful sample values come from each property's `examples` keyword in the same schema definition. Because the example is rendered from the schema, it cannot drift from it: a field added to the schema appears in the generated example, and a curated value like `channel: "#sales"` lives in the schema's `examples` next to the field's type, so they version together. The same generator applies to Singer taps, whose config schemas (from `--about`/`--discover`) carry `examples` too, so an agent gets an example `singer-tap` config as well.

This is how "what can this box do and how do I use it" is answered: the agent runs `capabilities`, reads the schemas and their derived examples, and generates a valid Wafer. The pipeline then re-validates the Wafer against the live server's capabilities on deploy (ADR-0009).

`servitor capabilities [dir]` writes files rather than printing, so the schemas never sit in the agent's context: one file per capability (its JSON Schema, role, and delivery, plus a derived example), grouped by **mechanism group** into top-level directories, plus a `secrets.yaml` reporting the declared secrets (name, account, permissions, and expiry, never the values) and an `index.yaml` listing the mechanism groups. A mechanism group (ADR-0031) is a family of mechanisms: `core` (universal primitives and scheduling), `webhook` (inbound HTTP reception), `singer` (record streaming), `mcp` (tool invocation), `helper` (compiled-in wrappers), `secret-resolution` (the available secret sources, the valid `source` values for `secrets.yaml`; ADR-0036), and `websocket` (inbound streaming, future). The individual types within a group are the mechanisms (for example `hmac-webhook` and `standard-webhook` are both mechanisms under `webhook`). The `secret-resolution` group is different in kind from the node-capability groups: it does not hold Wafer node types, it enumerates the secret providers an agent can name as a secret's `source` (SPEC: Secret resolution, ADR-0036). A service reached by several mechanisms appears in several groups; the type name carries the service (`singer-tap`, `mcp-stdio`, `hmac-webhook`). The declared connectors sit with their mechanism group: `singer/taps.yaml` lists the declared Singer taps, `mcp/servers.yaml` lists the declared MCP servers, and `webhook/receivers.yaml` lists the declared webhook receivers (ADR-0018, ADR-0049), so an agent sees both a capability and what is installed to run against it. The distinction between a standard envelope and a bespoke one (for example `standard-webhook` vs `hmac-webhook`) is a per-type detail within a mechanism group, not a separate group.

The mechanism source mirrors this grouping. Each mechanism has its own folder under its mechanism group's directory, and its package lives inside that folder and self-registers its capability and run behavior into the shared registry; the runner dispatches to a mechanism through the registry, never by naming it in a central switch (ADR-0045, ADR-0048). So a mechanism lives at `internal/registry/<group>/<mechanism>/`, for example `helper/email` registers the `email_received` mechanism. The mechanism's folder is the unit of deletion: removing it removes the mechanism from validation and `capabilities` with no central references left to edit. A service reachable by several mechanisms is one mechanism per mechanism group it appears in (for example a Grist helper under `helper/grist` and a Grist MCP mechanism under `mcp/grist`); they are separate code paths and independently removable.

#### Adding a mechanism group

A mechanism group is added only when a capability in it actually exists (ADR-0031). When one is added, it follows the same structure as the existing groups (`core`, `webhook`, `singer`, `mcp`, `helper`):

- A mechanism is a genuinely unique shape, not one per service (ADR-0048, ADR-0047). If the thing a mechanism talks to is a service or an external connector, it is declared in the config (`servitor.config.yaml`) rather than compiled in, and the mechanism is generic: `mcp-stdio`/`mcp-http` and `hmac-webhook`/`standard-webhook` are the model (ADR-0047, ADR-0049). Singer has been this way from the start (`singer-tap`/`singer-target`, taps and targets declared in the config).
- Create the group directory `internal/registry/<group>/`, with one folder per mechanism `internal/registry/<group>/<mechanism>/` whose package self-registers exactly that one capability.
- Register every mechanism package as a blank import in the `mechanisms` aggregator, so its `init` runs and the capability appears in validation and `capabilities`.
- Add the deletion-proof test: importing one mechanism package contributes exactly its own capabilities and nothing else.
- Surface what is declared for the group in `capabilities`, beside the generic mechanism (for example `singer/taps.yaml`, `mcp/servers.yaml`, `webhook/receivers.yaml`).
- Reusable, mechanism-agnostic machinery shared by more than one consumer goes in `internal/components/`, not in the group (ADR-0046).

Reusable machinery that is mechanism-agnostic and shared by more than one consumer lives apart from both the mechanisms and the engine, in `internal/components/` (ADR-0046). A component is written in terms of the thing it abstracts (a subprocess, a JSONata expression, a record stream, a tool call, a secret source), never a specific capability or product. A component imports only other components and the standard library, never the registry, a mechanism, or the engine. Code is routed to one of three homes by asking, in order: is it Servitor's core engine (the worker loop, dispatch, durability store, trigger receiver, daemon, CLI, Wafer validation)? it stays at `internal/` top level. Is it a specific, deletable mechanism? it goes in its mechanism's folder. Otherwise, is it reusable machinery more than one consumer composes? it is a shared component in `internal/components/`. A shared component with a single consumer is moved into that consumer rather than left as a speculative seam (ADR-0002).

For a **remote agent**, capabilities reach it the same way Wafers do: the pipeline (which already runs the CLI on the box) runs `servitor capabilities` and commits the generated directory into the git repo, and the agent reads the files from the repo on demand. Capabilities are still per-server because the directory is generated from that box's compiled-in set; committing it is a materialized snapshot, not a hand-written doc, so it cannot drift. A local agent (on the box, or the pipeline's own runner) can also run `capabilities` directly into a scratch directory.

The connectors and secrets are declared in a local `servitor.config.yaml` (ADR-0018): the operator names each MCP server, Singer tap, and Singer target with its exact command (or, for an MCP server reached over HTTP, its URL and secret-referenced headers) and the env vars it needs, and each declared secret with its source and optional metadata (ADR-0035). `servitor mcp`/`tap`/`target`/`secret` add/list/remove manage this file; the actual software install is delegated to the ecosystem's package managers (npx, pipx, uv, Meltano). `capabilities` reports only what is declared, probing each once at refresh for its schemas; there is no PATH scan and no naming convention to break.

### Triggers

- `hmac-webhook`. Inbound HTTP receiver that verifies HMAC-SHA256 over the raw
  body and delivers it to the run as the event. The signature header name and
  encoding are declared per receiver in the config (ADR-0049).
- `standard-webhook`. Inbound HTTP receiver that verifies the Standard Webhooks
  envelope (a versioned, timestamped signature with a replay window) and
  delivers the body to the run (ADR-0049).
- `email_received`. Inbound email parsed into a structured payload. Its `host`, `username`, and `secret` (a declared secret name) name the mailbox, and its `poll` schedule (default every 5 minutes) polls it for new mail, firing one run per new email. Built for Google Workspace via IMAP (app password); other providers are future helpers.
- `cron`. Honker scheduler.
- `manual`. Invoked via CLI.
- `completed`. Fired by another workflow's completion. Its `workflow` field names the workflow whose completion fires it; the run's event is `{trigger: "completed", from: <workflow name>, from_run: <completed run id>}`.
- `failed`. Fired by another workflow's failure. Its `workflow` field names the workflow whose failure fires it; the run's event is `{trigger: "failed", from: <workflow name>, from_run: <failed run id>}`. A distinct signal from `completed`, which stays success-completion-only (ADR-0039), so the operator can wire a notification to a failed run (for example a failed secret) without `completed` firing spuriously.

#### Using webhook triggers

Webhook receivers are declared in the config (ADR-0049), mirroring MCP servers:
each receiver names its `path`, its `scheme` (`hmac` or `standard`), and, when it
verifies a signature, a `secret` (the declared secret name holding the shared
key; SPEC: Secret resolution). A Wafer's webhook trigger names a receiver by its
path, and the mechanism is chosen by the receiver's declared scheme:

- **`hmac-webhook`** when the sender signs with HMAC-SHA256. The receiver
  config declares the signature header and encoding (hex or base64), and
  optionally a version `prefix` (stripped from the header value) and a
  `timestamp_header` (the body is then signed as `<prefix>:<timestamp>:<body>`
  and replay is bounded with a time window). A sender that signs the raw body
  and a sender that signs the timestamped form are both config entries, not
  separate mechanisms.
- **`standard-webhook`** when the sender speaks Standard Webhooks (it sends
  `webhook-id`, `webhook-timestamp`, and `webhook-signature` headers). Any
  compliant producer works with this type.

Both mechanisms deliver the **raw body** as the run's event. The workflow parses
it itself, typically with a `transform` node, so no per-service parsing is
compiled in (ADR-0049).

A request that hits a configured `path` is persisted before matching (SPEC:
Execution model step 2), its signature is verified against the receiver's
declared secret, and each matching enabled workflow is enqueued (SPEC:
Execution model step 5).

A webhook trigger's `type` must match the scheme of the receiver declared for
its path (hmac-webhook for `hmac`, standard-webhook for `standard`); a mismatch
is rejected at submit. A webhook trigger whose path has no declared receiver is
allowed: it matches nothing, and `webhook/receivers.yaml` shows the declared
receivers so the author sees what is available.

Both `hmac-webhook` and `standard-webhook` receivers are built and verify
signatures. A receiver declared with an unknown `scheme` is rejected at load.

#### Using email triggers

`email_received` polls a mailbox and fires one run per new email (ADR-0027). It
is one kind of the general polling mechanism: a recurring poll runs a fetcher
subprocess on a schedule and fans out one run per item, and `email_received`
is the email instance. It takes `host` (the IMAP server, for example
`imap.gmail.com`), `username`, and `secret` (the declared secret name holding
the app password, see SPEC: Secret resolution), plus an optional `poll` cron
schedule (default every 5 minutes). Each new message is parsed into the run's
event: `event.from`,
`event.to`, `event.subject`, `event.body`, `event.date`, and `event.message_id`.
Polling marks messages as read, so each email fires once. The first provider is
Google Workspace over IMAP; a future provider is a different `host`/auth on the
trigger, handled by its own helper (ADR-0027).

### Nodes

The body of a Wafer is its nodes. There are two kinds: **action nodes**, which do
work mid-run, and **flow nodes**, which route or fan out. All nodes are part of
the run's DAG and run as subprocesses (ADR-0008).

#### Action nodes

Action nodes do work: they call an external service, execute a command, or
compute over the run's data.

- `http`. Make an HTTP request, capture response.
- `shell`. Execute a command.
- `transform`. Reshape, extract, or compute over previous nodes' JSON output, returning new JSON. Its `expression` field is JSONata (ADR-0020), evaluated against the node's `{event, steps}` input (ADR-0021). It runs as a subprocess of the servitor binary's hidden `__transform` command (ADR-0008).
- `singer-tap`. Run a Singer tap with config, capture records and state.
- `singer-target`. Run a Singer target consuming records.
- `mcp-stdio`. Invoke one named tool on one named MCP server as a subprocess
  over stdio (ADR-0015, ADR-0047). The node runs the server with a filtered
  secret env, sends a single `tools/call` request on stdin, reads the
  structured JSON response on stdout, and exits. Fields: `server` (which
  declared server to run), `tool` (which named tool), `input` (the tool
  arguments), and `mode` (the protocol revision the server speaks, `classic`
  or `stateless`; omit to detect at run time). Server package versions are
  pinned by the operator's declared `command` (for example
  `npx -y atomic-server@1.2.3`), matching how tap and server versions are
  pinned (ADR-0018).
- `mcp-http`. Invoke one named tool on one named MCP server over Streamable
  HTTP (ADR-0047). The node connects to the server's declared `url` with its
  secret-referenced token and sends a single `tools/call` request. Fields:
  `server` (which declared server to connect to), `tool`, `input`, and `mode`
  (`classic` or `stateless`; omit to detect at run time). The server's `url`
  and secret-referenced `headers` are declared in the config, not in the
  Wafer. The node runs as the hidden `servitor __mcp_http` subprocess
  (ADR-0008): the worker looks up the server's URL from the boot-loaded
  connector registry and spawns it, so the HTTP client and the secret-bearing
  request headers never enter the runner's process. A header may only
  reference a secret the node declares in `secrets:` (resolved per use); a
  header naming any other secret fails like a missing secret (SPEC: Secret
  resolution, ADR-0033).
- `rerun-failed`. Re-run a dead-lettered (failed) run (ADR-0044). Its `run_id`
  is a JSONata expression over the node's `{event, steps}` input naming the
  failed run to re-run, and its `mode` is how to re-run (`continue`, `restart`,
  or `discard`, default `continue`). It is how one workflow re-runs another, for
  example a watcher fired by a `failed` trigger. It is a control node handled in
  the worker, not a subprocess.

  `run_id` gives you both the generality (target any run by name) and a
  zero-config default: when it is omitted it defaults to `event.from_run` (the
  id of the failed run whose `failed` trigger started this workflow). So the
  common watcher case needs no config, and an explicit expression re-runs any
  named run:

  ```yaml
  # Common case: re-run the failed run that fired this workflow (no run_id).
  - name: retry_it
    type: rerun-failed
    mode: continue

  # General case: re-run any named run by expression.
  - name: retry_it
    type: rerun-failed
    run_id: event.from_run        # or any JSONata expression / name
    mode: continue
  ```

  `mcp-stdio` and `mcp-http` both support the original MCP protocol (the
  `initialize` / `initialized` handshake) and the stateless revision that
  carries protocol version and capabilities inline in a `_meta` field,
  detecting which a server expects at discovery time and caching it. Tool
  schemas are discovered from the server once during a `capabilities` refresh
  and cached, not queried on every node execution. MCP tool results (an
  `isError` flag plus content blocks) map onto Servitor's structured
  validation error format. Installed servers are those declared in the config
  (ADR-0018).

  MCP is a mechanism for this node type, unrelated to the
  control-plane question of whether Servitor's own daemon interface is ever
  exposed over MCP, which stays out of scope (ADR-0005).

#### Flow nodes

Flow nodes do no external work; they route or fan out, and they are how the
run's DAG branches and loops.

- `switch`. Route to one named branch based on a value. It has an `expression` (JSONata over the node's `{event, steps}` input), a `cases` map of value to the name of a top-level node to route to, and an optional `default`. The chosen branch runs; non-chosen branches are skipped (ADR-0022, ADR-0023). If/else is a two-case switch.
- `foreach`. Fan a node out over a list. It has an `over` expression (JSONata over the node's `{event, steps}` input yielding the list), an `as` loop-variable name (exposed in each iteration's input, default `item`), and a `body` (the name of a top-level node to run once per element). A downstream node that `depends_on` the body collects the per-iteration results as an array under the foreach node's name in its `{event, steps}` input (ADR-0024).
- `wait`. Park the run and resume later, between nodes, via a timer or a named
  signal (ADR-0040, ADR-0041, ADR-0042, ADR-0043). A `wait` is one node with
  two optional sources and resolves on whichever fires first:
  - `timer`: resume after a duration or at an absolute time. Two explicit
    sub-fields: `after` (a duration, for example `48h`, resolved to a resume
    time at park) and `at` (an absolute time, for example
    `2026-09-01T10:00:00Z`). Enqueued as a one-shot job on Honker's queue
    (ADR-0043), durable across restarts.
  - `signal`: resume when a named signal arrives (ADR-0042). `signal` is a
    JSONata expression over the run's `{event, steps}` input, resolved at park
    time to the effective signal name (for example
    `approval_gate.${event.order_id}`), so distinct work yields distinct names
    and the sender needs only the business key, never the run id. A signal
    addressing more than one parked run is rejected as ambiguous. A literal
    `signal` name (for example `approval_gate`) is legal and means "any run
    parked on this name". Senders are
    another workflow's `send-signal` node, `servitor resume <signal-name>
    [payload]`, or an external service POSTing to an ordinary webhook trigger
    that starts a small broker workflow.

  The node's result is `{source: "signal" | "timer", payload: <the signal
  payload, or null on timer>}`. `source` is `"timer"` (not `"timeout"`), because
  a timed hold is not a failure. `payload` is always present and `null` when the
  timer fired. The signal payload is opaque data threaded forward as the wait
  node's step result; it is not re-injected as a new run `event`. A following
  `switch`/`transform` branches on `steps.<wait>.source` and reads
  `steps.<wait>.payload`. A node with neither `timer` nor `signal` is a
  validation error (it would park forever).

  **Race rules.** Signals are neither lost nor doubled, mirroring Temporal's
  buffered signals (ADR-0042). A signal that arrives before the run parks is
  buffered, not dropped: it is persisted like an inbound event (SPEC: Execution
  model step 2), and the `wait` node's park transaction checks for and consumes
  a buffered signal, resuming immediately if one is present. A second resume is
  a no-op: once a run is resumed (or a signal consumed), a repeat resume does
  not re-run anything, via an atomic compare-and-set on the run's `waiting`
  status. Timer and signal are mutually exclusive by construction: when a signal
  resolves the wait, the pending timer job is dropped; when the timer fires, any
  later signal is a no-op.

  **Wafer version drift.** A run parked for months, then the Wafer redeployed
  with changed nodes (ADR-0040): the continuation is frozen in the job payload
  at park time, so a parked run resumes with its original definition and new
  runs use the new wafer. Suspend is between nodes only, never inside a node's
  own work: each node runs to completion in one subprocess (ADR-0008), only its
  result survives, and a parked run resumes at the next node with the pre-wait
  results already saved.

#### Curated helpers

Small wrappers around official SDKs for the services most commonly used. Each one is well-typed, well-documented, and ships with appropriate triggers and actions.

Initial set (subject to your stack's priorities):

- `grist`. Read, write, list, query.
- `slack`. Post messages, read events.
- `github`. Issues, PRs, releases.
- `email`. Send, parse incoming.

(Atomic is reached via the `mcp-http` node type against its Streamable HTTP
MCP endpoint, rather than a hand-written helper, since it is low-frequency
and the server is self-hostable.)

Each helper uses declared secrets for auth and exposes its
actions/triggers via `servitor capabilities`.

---

## Execution model

1. **Trigger fires.** A webhook arrives, a cron fires, or someone calls `servitor trigger`.
2. **Event is persisted.** The raw event is written to Honker before any matching happens. Failed events, orphan events, and crash-survival all benefit from this.
3. **Signature verification.** The receiver verifies the signature against the relevant secret, resolved per use from the provider, in the parent process.
4. **Workflow matching.** The runner finds workflows whose `trigger:` block matches the event.
5. **Run enqueued.** A workflow run is created in Honker with the event payload as input. The run's initial node(s) are enqueued in the same transaction.
6. **Workers claim and execute.** A node executor in the parent process claims a job and checks the node's `dedupe_key` against the dedupe table. It spawns a subprocess with a filtered env containing only the secrets the node declared (every node runs as a subprocess; ADR-0008).
7. **Node runs.** Node types dispatch to handlers: HTTP, shell, transform, Singer tap, Singer target, helpers, and the flow nodes `switch` (route to one branch) and `foreach` (fan a body out over a list). A node writes its result as structured JSON to stdout and exits. A `switch` and `foreach` resolve their decision in a subprocess and then route: the worker fans out the chosen branch / body iterations through the dependency counters (ADR-0022, ADR-0024).
8. **Result committed transactionally.** When a node completes, its writes happen as a single atomic SQLite transaction: the node's result is persisted, the `dedupe_key` record is written (if any), all downstream nodes whose dependencies are now satisfied are enqueued, and the node's own claim is acked, all in one commit. Runs are built as a dependency DAG (ADR-0023): each node carries a count of unsatisfied dependencies, and the completing node decrements each dependent's count and enqueues it only when the count reaches zero (fan-in). A `switch` node enqueues its chosen branch and marks the others skipped; a `foreach` node enqueues one body job per element. The input a downstream node receives is `{event, steps}`, where `steps` is prior results keyed by node name, threaded forward and committed with the result (ADR-0021). (For Singer nodes, the updated bookmark is part of the same commit.) There is no separate scheduler process watching for completions; the worker that just finished the node performs these writes itself. This is non-negotiable because each possible split produces a distinct silent failure: result-without-enqueue stalls the workflow (a node is "done" but successors never run); enqueue-without-ack re-issues the claim on visibility timeout and re-runs the node, fanning out *again* and doubling every downstream side effect; dedupe-without-result causes future retries to skip the node without ever returning a value. The transactional atom is therefore **{result, dedupe_record, downstream_enqueues, claim_ack}**, all in one commit. If implementation pressure ever tempts splitting this transaction, the answer is no; redesign the data model instead.
9. **Crashes are safe, with a caveat.** If a subprocess dies, the parent records the failure and the executor reclaims through normal retry. If the parent dies mid-job, Honker's visibility timeout re-issues the claim to another runner instance (or to itself on restart). **Crash safety against double-firing of side effects only applies to nodes that declare a `dedupe_key`.** Nodes without one inherit Honker's at-least-once contract: a node whose side effect completes before the result is persisted may be re-issued and the side effect re-performed. The validator warns when a side-effecting node omits `dedupe_key` precisely to make this contract visible to authors.
10. **Run completes when no work is pending.** Each run tracks a count of in-flight jobs, adjusted in the same atomic commit as each node's completion (a claimed node's ack removes one, each enqueued dependent adds one). A run is marked completed when that count reaches zero. This is the dependency-based completion signal (ADR-0023): it correctly waits for a `foreach`'s body iterations and a fan-in rejoin, and for a linear chain it is the degenerate case. A skipped branch (non-chosen by a `switch`) records itself as skipped and cascades, so a run is never left waiting on a branch that did not run. A *parked* run (one at a `wait` node) is not complete: the guard is `pending == 0 && status != waiting`.
11. **A `wait` node parks and resumes the run.** A `wait` node parks the run in one transaction: it writes a `suspended_continuations` row holding the wait node's downstream sub-DAG and the current `run_deps` state for those nodes, sets the run status to `waiting`, and acks the wait job's claim. Parking is between nodes only; each node still runs to completion in one subprocess (ADR-0008). On resume, the continuation frontier is re-enqueued (pending +1), the status flips back to `running`, and the row is deleted; the run picks up at the next node after the wait with the pre-wait results already saved (ADR-0040). A parked run holds no live work, so it is drain-safe and survives restarts.

---

## Idempotency and deduplication

Honker's at-least-once delivery means a node can run more than once: a worker can complete a side effect, crash before acking, and have its claim re-issued to another worker. The naive answer is "nodes must be idempotent or use a deduplication key," but that is load-bearing enough to deserve a real primitive, not a footnote.

The runner provides:

- **`dedupe_key` field on every node.** A JSONata expression (ADR-0020) evaluated at execution time against the node's `{event, steps}` input (ADR-0021) (often the trigger event ID, a row ID, or a hash); the result is stringified to form the key. Before the node executes, the parent checks a `node_dedupe` table keyed by `(workflow_id, node_name, dedupe_key)`. If the key is present and the prior run succeeded, the node is skipped and the prior result is returned. If the key is present and the prior run failed, the node proceeds.
- **A short retention window on dedupe keys** (default 72h, configurable per node) so the table doesn't grow unboundedly.
- **Default off, but loudly recommended.** Validation emits a warning when a node performs an externally-visible side effect (sending a message, creating a row, calling a non-idempotent HTTP method) and has no `dedupe_key`. Agents see this warning as a structured error of severity `warn` and can decide whether to suppress.

For Singer taps specifically: a tap that completes records and crashes before its bookmark is persisted will re-emit those records on the next run. Targets should handle this with their own dedupe (most warehouse targets do via primary keys); for action-shaped uses downstream of a tap, set `dedupe_key` on the action node.

---

## Structured validation errors

Because agents are first-class authors, the shape of validation errors is part of the design, not an afterthought. Every error returned by `servitor submit`, `servitor update`, or `servitor dry-run` follows this shape:

```json
{
  "errors": [
    {
      "path": "/nodes/2/channel",
      "code": "missing_required_field",
      "message": "field 'channel' is required for node type 'slack'",
      "expected": "string"
    },
    {
      "path": "/nodes/3/type",
      "code": "unknown_node_type",
      "message": "unknown node type 'slak'",
      "suggestion": "slack"
    }
  ],
  "warnings": [
    {
      "path": "/nodes/2",
      "code": "missing_dedupe_key",
      "message": "node 'slack' performs an external side effect and has no dedupe_key; this node may run more than once on retry"
    }
  ]
}
```

Codes are stable identifiers (`unknown_node_type`, `missing_required_field`, `type_mismatch`, `missing_secret`, `circular_dependency`, `missing_dedupe_key`, etc.). Paths are JSON Pointers into the submitted YAML. Multiple errors are returned at once, not one-at-a-time, so an agent fixing a malformed workflow makes one round trip per fix-batch rather than one per fix. A `missing_secret` warning is emitted by `dry-run` when a node declares a secret that is not resolvable by the configured provider; the workflow's declared secret names are shown redacted (`<redacted:secret_name>`), never their values.

The full workflow JSON Schema and every capability's config schema are also retrievable through `servitor capabilities`, so agents can validate locally before submitting.

---

## Authentication

The control plane is a CLI talking to the daemon over loopback. The daemon binds `127.0.0.1` only and refuses a non-loopback interface, so the only thing that can reach it is a process already on that host. Changing behavior is gated through a reviewed pipeline, and operating the runner runs on the box (ADR-0009); there is no operator authentication inside the daemon because the network surface it would protect does not exist.

Getting onto the box is the operator's existing access (SSH or VPN), not a Servitor feature. A deployment that wants remote management puts the box itself behind the operator's network boundary; Servitor does not stand up its own auth/proxy stack.

"Auth" also shows up in two places that are not operator authentication:

- **Webhook signature verification** (inbound triggers) is not user auth; see the Standard Webhooks and per-provider trigger type sections. Secrets come from the secret provider, resolved per use.
- **Outbound credentials** for integrated services live in the secret provider. Long-lived API tokens (Slack bot tokens, GitHub PATs, Grist API keys, Stripe restricted keys) are preferred over OAuth flows. This is a deliberate scope choice: the runner is for self-hosted single-team deployments, not multi-tenant SaaS, so the OAuth-flow-and-refresh complexity isn't warranted in the core.

---

## Design principles

**Small interfaces compose well.** Honker handles durability. The secret provider handles secrets. Singer handles record streaming. Standard Webhooks handles modern webhook reception. The CLI and daemon protocol handle control. This runner is the glue. Each piece does its narrow job; replacing any one doesn't affect the others.

**The workflow is fully defined by the Wafer, nowhere else.** No state lives in a UI. Workflows are version-controllable, diff-able, and reviewable. Agents and humans manipulate the same artifact.

**Agents are first-class authors, not bolted-on consumers.** Capability discovery, structured validation errors, schema introspection, dry-run support, the `dedupe_key` primitive: these exist because agents need them, not as afterthoughts. The "Why agent-first changes the design" section makes the case in full.

**Truly open source.** MIT-licensed, no open core, no enterprise tier, no feature paywall. The thing you self-host is the whole thing.

**Code-first.** No visual builder. The Wafer is the artifact.

**Delegate hard problems to maintained tools.** Identity, webhook signing: each of these is owned by a project that does it full-time. The runner does not reimplement any of them. Secret storage is delegated to the pluggable provider (SPEC: Secret resolution), which the runner calls rather than reimplementing.

**Honest about scope.** This is for self-hosted single-tenant deployments. SQLite single-writer means one runner process owns the database. Multi-host scaling is a different problem and a different tool.

---

## Gotchas

Operational and security invariants that are easy to miss or re-litigate.

- **The subprocess env is the security boundary, not how a node's input is
  shaped.** A node can only see what its subprocess environment contains
  (ADR-0008): only the secrets it declared. Whether the worker threads prior
  results into the job or reads them back does not change what a node can
  reach. Do not add an input-scoping mechanism on security grounds; the
  isolation is the filtered subprocess env.
- **A node's input is committed atomically with the result it depends on.** The
  `{event, steps}` input a downstream node receives is written into the job's
  payload inside the same `CommitStepAtom` transaction as the prior node's
  result (SPEC: Execution model step 8, ADR-0021). A node's input can therefore
  never disagree with the committed results it was built from; keep it that way.
- **`dedupe_key` has two independent axes: the language and when it is
  evaluated.** The language is JSONata (ADR-0020). When it is evaluated (now, at
  node execution, alongside `transform`) is a separate decision (ADR-0021).
  Do not conflate them when revisiting either.
- **A run can park only one `wait` at a time.** The continuation is keyed by
  run id, so a second `wait` in the same run overwrites the first
  (ADR-0040). Sequential waits (a linear chain, a wait before a fan-in, a wait
  with a fan-out after it) are fine, since only one is active at a time. A
  `wait` inside a `foreach` body, where several iterations park at once, is not
  supported: only the last park would survive. Express "wait for all N" as N
  separate runs chained by a `completed` trigger instead. See IDEAS.md
  "Multiple concurrent parks per run".

---

## Roadmap

Things deliberately out of scope for v1, kept here so the design doesn't quietly commit to them later:

- **Multi-host scaling.** SQLite rules this out for the core runner. A different tool, not a different version of this one.
- **MCP adapter.** Deferred until there is a concrete user; the daemon protocol is kept transport-agnostic so it can be added later (ADR-0005).
- **OAuth flow management for outbound credentials.** Long-lived tokens cover the realistic self-hosted single-team case. Revisit if a critical service becomes OAuth-only.
- **A policy engine for permissions.** Role strings cover the realistic case. Revisit if real deployments hit the ceiling.

---

## Status

Early development. The daemon lifecycle, loopback control protocol, Wafer model and structured validation, capability discovery (including a `secrets.yaml` reporting declared secret names, account, permissions, and expiry, a `secret-resolution` group enumerating the available secret sources, a `singer/taps.yaml` reporting declared Singer taps, and a `mcp/servers.yaml` reporting declared MCP servers; grouped by mechanism group per ADR-0031, sourced from the declared config per ADR-0018), dry-run DAG resolution (including redacted secret names and a `missing_secret` warning), the Honker durability store (with the transactional atom), node execution (the worker loop, subprocess isolation with env filtering, and the dedupe contract), the secret-resolution model (a pluggable provider with per-node, per-subprocess delivery; the `env` and `varlock` providers; a declared-secrets section in the config and the `servitor secret` CLI; submit rejects a Wafer that references an undeclared secret; the varlock boot path is removed, ADR-0032 through ADR-0036), Singer (the `singer-tap` and `singer-target` executors, bookmark state committed with each tap node's result, and schema discovery; ADR-0016), MCP (the `mcp-stdio` node type with a client-mode executor, both classic and stateless protocol support, and structured error mapping; ADR-0015; the `mcp-http` Streamable HTTP executor, ADR-0047), inbound triggers (webhook receivers declared in `servitor.config.yaml` and verified by scheme, the `hmac-webhook` and `standard-webhook` mechanisms delivering the raw body, plus cron and manual), the shipped `SKILL.md` agent reference, run inspection (`servitor runs`, `servitor run <id>`, `servitor cancel`), and the release flow (`make release`) are built (`servitor run`, `stop`, `dry-run`, `capabilities`, `submit`, `update`, `enable`, `disable`, `trigger`, `runs`, `run`, `cancel`, `resume`, `rerun`, `secret`, `webhook`), the `transform` node handler and `dedupe_key` evaluation (JSONata via gnata, ADR-0020; `{event, steps}` threaded input, ADR-0021), and the `switch` and `foreach` node handlers with dependency-counter fan-out (ADR-0022, ADR-0023, ADR-0024). The curated helpers (grist, slack, github, email send) are not yet built. Open questions, to be resolved as implementation progresses and tracked in ADRs in the `docs/adr/` directory:

- Worker concurrency limits; runs execute as a dependency DAG with fan-out (ADR-0023), but branches run sequentially rather than in parallel.

**Suspended waits: built.** The durable `wait` flow node (ADR-0040 through ADR-0043) parks a run and resumes it later via a timer (Honker queue `RunAt`, `timer.after` / `timer.at`) or a named signal (an author-defined JSONata `signal` name; senders are a `send-signal` node, `servitor resume <signal-name>`, or a webhook-triggered broker workflow). The run parks as `waiting`, shows in `servitor runs` / `servitor run <id>`, and `servitor cancel` drops the parked continuation. Race rules are pinned: a signal that arrives before the park is buffered, a repeat resume is a no-op, and a signal naming more than one parked run is rejected as ambiguous. A wait inside a `foreach` body (several iterations parking at once) is not supported yet; the continuation is one-per-run.

**Rerun: built.** A dead-lettered node saves its self-contained job as a failed continuation and the run is marked failed (ADR-0044). `servitor rerun <run-id> [--mode ...]` re-runs it (`continue` from the failed node, `restart` from the top, `discard` to drop it), and a `rerun-failed` node lets one workflow re-run another (defaulting to `event.from_run`). A per-Wafer `on_failure` field sets the default mode. Rerun is general, applying to any failed run regardless of cause. The global-config layer for the mode is deferred until a servitor config file exists.

**Secrets model: largely implemented.** The Secret resolution section describes the target secret model (ADR-0032 through ADR-0036). The provider interface, per-node delivery, the `env`, `varlock`, and `onbox` (push-based on-box ciphertext, sealed with `servitor secret seal`) providers, the declared-secrets config and `servitor secret` CLI, the capabilities surface, the varlock boot path removal, the secret-failure semantics (missing fails fast, source-unreachable retries with backoff, stale retries with a fresh resolve then fails with `secret_auth_failed`), the failed-run event (a dead-lettered node marks its run failed and fires the `failed` trigger, ADR-0039), and the resume-from-failure modes (continue/restart/discard, `servitor rerun`, a `rerun-failed` node, and a per-Wafer `on_failure` default; ADR-0044) are built. The `onbox` provider uses the non-TPM local-key unlock tier; TPM/KMS sealing of the key (the non-exportable tier) is future work.

Contributions welcome once the initial scaffolding is in place.

## License

MIT.
