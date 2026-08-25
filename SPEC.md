# Servitor

> **Workflow automation for the agentic stack.**
> **Self-hosted. MIT-licensed. X integrations.**

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

- **Not a Zapier replacement.** Zapier's value is its 7000+ commercial integrations and its visual builder. This project's value is honest integrations, code-first authoring, and agent-friendliness. Different audience.
- **Not an MCP server.** The control plane is a CLI, consumed by agents through a skill. MCP is a possible future adapter over the same daemon protocol, deferred until there is a concrete user (ADR-0005).
- **Not a multi-tenant SaaS workflow platform.** SQLite's single-writer model rules that out by design.
- **Not a data orchestration tool.** Airflow, Dagster, Prefect exist for that.
- **Not a CI/CD system.** GitHub Actions, Drone, Woodpecker exist for that.
- **Not a unified API.** Singer and per-service helpers are the integration model. There is no normalized cross-service schema.

---

## Why agent-first changes the design

Most workflow tools were designed for humans clicking through a builder, with an API bolted on. An agent using such a tool is a second-class citizen: it has to reverse-engineer what the UI assumes, guess at validation rules, and recover from opaque errors.

Designing for agents first changes specific decisions:

- **The artifact is the Wafer, not a database row.** Agents read, write, diff, and version-control the same file a human would. There is no "form state" living somewhere the agent can't see.
- **Capability discovery is a first-class operation.** `servitor capabilities` returns every step type, every trigger type, every declared secret, and every Singer tap available, each with its JSON Schema and an example rendered from that schema. An agent never has to guess what fields a step takes.
- **Validation errors are structured, not stringified.** Errors are returned as JSON with paths, codes, and suggestions. An agent that submits a workflow with `type: slak` gets back an `unknown_step_type` error with `suggestion: slack`, the way an IDE would flag the typo. (See the Structured validation errors section for the full shape.)
- **Dry-run is a real primitive.** `servitor dry-run` resolves the entire workflow and returns the DAG the runner *would* execute. No steps run, no external services are contacted, nothing is persisted. It reports the workflow's declared secret names (redacted, never values) and warns with a `missing_secret` code when one is not present in the environment, so an agent can verify structure, secret availability, and step configuration before committing.
- **The same CLI serves humans and agents.** No private API the agent doesn't have access to. If a future UI exists, it talks to the same control plane.

These are not nice-to-haves bolted on after the fact; they are why this project exists as a separate thing rather than as a fork of an existing runner.

---

## How it works

A workflow is a YAML file (a Wafer) declaring:

1. **Triggers.** What events cause the workflow to run (webhooks, cron, manual invocation, internal events).
2. **Steps.** What the workflow does, expressed as a sequence (or DAG) of typed step invocations.

The runner reads the Wafer, validates it, registers any triggers it declares, and waits for events. When an event arrives, the runner enqueues a workflow run, workers claim jobs and execute steps, results are persisted, and downstream steps fire as their dependencies complete.

Example Wafer:

```yaml
name: notify_on_new_lead
on:
  grist_webhook:
    table: Leads
    event: row_added
steps:
  - name: post_to_slack
    type: slack
    action: post_message
    channel: "#sales"
    text: "New lead: ${trigger.payload.fields.name}"
```

That's it. Submit it via CLI, enable it, and the next time a row is added to your Grist `Leads` table, a Slack message goes out.

---

## The whole thing, end to end

Read this once and the rest of the document fills in the details. The split: steps 1, 2, 6, and 8 are the runner's job (receive, verify, persist, execute). Steps 3, 4, 5, and 7 are the author's job, human or agent (decide what to build, write it, submit it, react to results). The runner never decides what a workflow should do; that is always the author, because the author has the context.

1. **Start the runner.** `servitor run` boots the daemon under varlock, which resolves secrets into its environment. One process owns the SQLite file.
2. **Discover what's possible.** `servitor capabilities` lists step types, trigger types, secrets, and Singer taps with schemas. An agent reads this instead of guessing.
3. **Write a Wafer.** A human edits a YAML file, or an agent generates one from the capabilities schema. The Wafer declares triggers and steps.
4. **Dry-run it.** `servitor dry-run ./wf.yml` validates and resolves the workflow without running anything, so the author sees the DAG and the declared secrets (redacted, with a `missing_secret` warning when one is absent).
5. **Deploy via the pipeline.** The agent (or human) opens a pull request for the Wafer; the pipeline dry-runs it and applies it on the box with `servitor submit`, then `servitor enable <name>` registers its triggers (ADR-0009).
6. **Run.** A webhook arrives, a cron fires, or `servitor trigger <name>` runs it manually. Workers execute steps durably; downstream steps fire as dependencies complete.
7. **Inspect and react.** `servitor runs` / `servitor run <id>` shows history and outcomes. The author fixes a Wafer or a step and resubmits.
8. **Stop.** `servitor stop` drains and shuts the daemon down. Crashes are recovered by the queue on restart.

---

## Architecture

The system is composed of well-defined open-source pieces, each doing its narrow job. Small interfaces compose well. The runner is written in Go (ADR-0004): a single binary that spawns a subprocess per step and owns a SQLite file.

The runner is a single OS process that owns the SQLite file and its single write connection. Inside that process is a pool of step executors. When a step executor claims a job, it runs the step as a subprocess (see Step execution modes). There is exactly one execution mode; every step, including pure-computation steps, runs as a subprocess.

How a step runs:

- The subprocess is launched with a filtered environment containing only the secrets the step's YAML declared it needs. This is real OS-level isolation, not "we promise not to read the variable."
- The subprocess writes its result to stdout (structured JSON) and exits.
- The parent runner process reads the result and commits it to SQLite, along with the enqueue of downstream steps, in one transaction.

This means SQLite writes are serialized through the parent process, which is the only thing holding a write connection. SQLite's single-writer rule is honored by design, not worked around.

Go keeps subprocess startup fast (roughly a millisecond), which is why every step runs as a subprocess rather than in-process (ADR-0008). Read concurrency is fine: workers reading their own claim, the control plane reading workflow state, and the trigger receivers reading config can all happen against WAL-mode SQLite without blocking the writer.

### Building blocks (reference)

What each dependency is and what we use it for. Each is a stable interface, so each can be written in whatever language suits it (Honker is Rust, Varlock is JavaScript, Singer taps and targets are whatever their authors wrote them in). Only the runner itself is Go.

#### Honker, durable queue and scheduler

[Honker](https://honker.dev) is a SQLite extension that adds Postgres-style NOTIFY/LISTEN semantics to SQLite, plus a durable work queue, event streams, and a cron scheduler. One `.db` file is the entire system: no Redis, no separate broker.

The extension is a native loadable library (`libhonker_ext.so`) the runner loads at startup. It is not committed to the repo; the operator supplies it and points the runner at it via `HONKER_EXTENSION_PATH` (or a flag). The runner refuses to boot the durable store without it (ADR-0011).

What we use it for:

- **Workflow run queue.** Each step is a job. Workers claim, execute, and ack.
- **Crash safety.** If a worker dies mid-job, the claim expires after a visibility timeout and another worker reclaims. After max attempts the job lands in a dead-letter table.
- **State persistence.** Every workflow's run history, step outcomes, Singer state bookmarks, and pending events live in the same SQLite file.
- **Transactional commits.** A step's completion writes commit as a single atomic SQLite transaction rather than as separate operations. This is the mechanism behind the transactional fan-out guarantee; see step 8 of the Execution model for what the transaction contains and why it must never be split.
- **Scheduler primitive.** Cron-style triggers use Honker's built-in scheduler.

#### Varlock, secret management

[Varlock](https://varlock.dev) is a typed, schema-validated `.env` replacement with runtime log redaction and plugin support for a range of secret managers (e.g. 1Password, HashiCorp Vault, AWS Secrets Manager). Servitor does not assume any particular one: the operator points varlock at whatever backing store they already use, and the runner only ever sees resolved env vars, so the choice of manager is the operator's, not Servitor's.

What we use it for:

- **Secret resolution at process start.** The operator just runs `servitor`. The process checks whether it is already running under varlock; if not, it re-execs itself as `varlock run -- servitor run`. Varlock resolves secrets from their backing store, validates them against the schema, and injects them as individual env vars before any of the runner's real code executes.
- **Per-step secret filtering at subprocess spawn.** When the runner spawns a step subprocess, it constructs the subprocess's env from scratch and includes *only* the secrets the step declared. Webhook secrets, runner-internal secrets, and other steps' secrets never appear in the subprocess env. Because every step runs as a subprocess (ADR-0008), no step ever runs in the runner's process where it could reach the resolved-secret cache.
- **Webhook signature secrets.** Each integrated service's webhook signing key is declared in the varlock schema. The receiver reads them from the process environment at verification time, in the runner process only.
- **Log redaction.** Sensitive values are redacted from runner output by Varlock at runtime.

**Self-healing launch.** The danger with exposing the inner `servitor run` target is that someone reads `--help`, types `servitor run` directly, and boots the runner with no secrets in its environment, which is the one startup mistake that matters. This is prevented by a sentinel rather than by hiding the command. Varlock always sets `__VARLOCK_RUN=1` in the environment of the process it launches. So on startup the runner checks for `__VARLOCK_RUN`: if it is present, the process is already wrapped and boots normally; if it is absent, the process re-execs itself as `varlock run -- servitor run` and lets varlock populate the environment first. Both `servitor` and a directly typed `servitor run` therefore converge on the same wrapped path. The re-exec is idempotent: the inner invocation runs with `__VARLOCK_RUN` set, so it boots rather than wrapping itself again. If varlock is not installed, the runner boots anyway and warns that secret resolution is off; steps that declare secrets will then fail, which is the visible signal that varlock is missing.

#### Singer, data movement integrations

[Singer](https://www.singer.io) is an open spec for data integration. A *tap* is a CLI that emits records from a source as JSON; a *target* is a CLI that consumes records into a destination. Hundreds of taps exist across the ecosystem, most MIT-licensed, many actively maintained through [Meltano Hub](https://hub.meltano.com).

Singer is the record-stream integration layer of the runner: schemas, streams of records, and bookmark state. Most taps in practice run in batch (pull everything new since last bookmark, exit), but the spec itself is a streaming protocol and continuous taps exist; the runner treats both the same way.

What we use it for:

- **`singer-tap` step type.** Drop in `tap-stripe`, `tap-github`, `tap-hubspot`, etc. with config, and records flow into the workflow.
- **`singer-target` step type.** Built-in targets include `target-grist`, `target-atomic`, plus any community target.
- **State management.** Each tap's incremental sync state (the bookmark of last synced position) is stored in Honker and passed back into the next tap invocation.
- **Self-describing schemas.** Each tap publishes its config schema, available streams, and record schemas via `--about` and `--discover`. The control plane exposes this for agents to introspect.

Singer steps and curated helpers can both perform actions against the same external service (a `target-grist` and the `grist` helper's `write_row` both write to Grist), so the distinction isn't action vs not-action; it's the *shape* of the step. Singer steps consume or emit streams of typed records with bookmark state. Helpers make discrete calls with discrete inputs and outputs.

#### Standard Webhooks, modern webhook reception

[Standard Webhooks](https://www.standardwebhooks.com) is a community-driven spec for webhook signing and verification, adopted by OpenAI, Anthropic, Google Gemini, Supabase, Twilio, Vanta, and others.

What we use it for:

- **A `standard_webhook` trigger type.** Any compliant producer works out of the box with one verification library.
- **Forward compatibility.** New services adopting the spec become trigger sources with zero per-service code.

For non-compliant services (Grist, GitHub, Stripe, Slack, etc.), provider-specific trigger types handle their bespoke signing schemes.

### Step execution

Every step runs as a subprocess. There is no in-process mode (ADR-0008). When a step executor claims a job, it launches a subprocess with a filtered environment containing only the secrets that step declared, the subprocess writes its result as structured JSON to stdout and exits, and the parent commits the result.

The subprocess is the isolation boundary. Because nothing runs inside the runner's own process, there is no "not a sandbox" surface: code that might be untrusted or buggy is contained by OS process isolation, and since a step cannot see secrets it did not declare, its environment contains nothing worth stealing. This is why Go's cheap subprocess startup makes a uniform subprocess model the simplest and safest choice.

### Graceful shutdown

Crash safety (covered in the execution model section) handles the runner dying unexpectedly. Graceful shutdown handles the runner being stopped on purpose. The two share a backstop but aren't the same path.

On `SIGTERM`, the runner drains with a deadline:

1. **Stop claiming.** The runner immediately stops claiming new jobs. New triggers still persist their events to Honker (so nothing is lost), but no new runs begin execution.
2. **Let in-flight steps finish.** Steps already running are given up to a configurable drain timeout to complete. Each that finishes commits its normal fan-out transaction (result, dedupe record, downstream enqueues, claim ack, all four in one commit), exactly as in steady state.
3. **Hard-stop stragglers at the deadline.** Any step still running when the drain timeout expires is terminated. The runner does not commit a result for these; it leaves their claims to expire. They then become ordinary crash-recovery cases: the visibility timeout re-issues the claim, and the `dedupe_key` contract governs whether re-running is side-effect-safe.
4. **Release the write connection.** Once draining ends, the runner closes its SQLite write connection cleanly so the next instance can acquire it without waiting on a stale lock.

A second `SIGTERM`, or a `SIGKILL`, skips draining and stops immediately; everything in flight becomes a crash-recovery case.

---

## Control plane

The runner is a long-lived daemon. The control plane is a CLI that talks to it, plus the daemon control protocol the CLI is one client of. Everything an agent or human does runs through this interface; there is no separate API. (The decision and rationale are in ADR-0005.)

### The CLI

The command set, grouped by what you're doing. These are the contract humans and agents share. `servitor capabilities` writes schemas to files that the agent reads on demand, so big JSON schemas never sit in the agent's context; this is the token-efficiency payoff of a CLI over an MCP server.

```
servitor run                        # boot the runner daemon (under varlock)
servitor capabilities               # write step/trigger/secret/tap schemas + derived examples to files
servitor dry-run <wafer>            # validate and resolve without executing (--json for structured)
servitor submit <wafer>             # validate and register a workflow
servitor update <wafer>             # replace a workflow's definition
servitor enable <name>              # register a workflow's triggers
servitor disable <name>             # unregister without deleting
servitor trigger <name> [inputs]    # manual run with optional inputs
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

A Wafer declares two things: **triggers** (what starts a workflow, written under `on:`) and **steps** (what it does once running, written under `steps:`). Both lists are representative, not exhaustive; `servitor capabilities` returns the authoritative live set, each entry with its JSON Schema.

### How an agent discovers integrations

Before writing a Wafer, an agent needs to know what the *target* server supports and how to use it. `servitor capabilities` answers both, and it is a per-server query: the authoritative set is what that runner has compiled in (step types, trigger types), what its varlock schema declares (secrets, present or not), and which Singer taps are installed. The agent asks the server rather than trusting a doc, because the answer differs per deployment.

For each step type and trigger type, `capabilities` returns:

- its **JSON Schema** (fields, required, types, constraints), and
- an **example Wafer fragment** rendered from that schema.

The example is **derived from the schema, not written by hand**: the structural skeleton (required fields in order, nested objects and arrays) is generated from the schema, and meaningful sample values come from each property's `examples` keyword in the same schema definition. Because the example is rendered from the schema, it cannot drift from it: a field added to the schema appears in the generated example, and a curated value like `channel: "#sales"` lives in the schema's `examples` next to the field's type, so they version together. The same generator applies to Singer taps, whose config schemas (from `--about`/`--discover`) carry `examples` too, so an agent gets an example `singer-tap` config as well.

This is how "what integrations exist and how do I use them" is answered: the agent runs `capabilities`, reads the schemas and their derived examples, and generates a valid Wafer. The pipeline then re-validates the Wafer against the live server's capabilities on deploy (ADR-0009).

`servitor capabilities [dir]` writes files rather than printing, so the schemas never sit in the agent's context: one file per step and trigger type (its JSON Schema plus derived example), grouped by integration into directories, with a `core` group for Servitor's own step and trigger types (the universal primitives and generic webhooks), a `secrets.yaml` reporting the declared secret names and whether each is present (never the values), plus an `index.yaml` listing the integrations. An integration is a service the runner can reach by any mechanism; a service with both a helper and a trigger (for example Slack's `slack` helper and `slack_event` trigger) is one integration, one directory (SPEC: What counts as an integration).

For a **remote agent**, capabilities reach it the same way Wafers do: the pipeline (which already runs the CLI on the box) runs `servitor capabilities` and commits the generated directory into the git repo, and the agent reads the files from the repo on demand. Capabilities are still per-server because the directory is generated from that box's compiled-in set; committing it is a materialized snapshot, not a hand-written doc, so it cannot drift. A local agent (on the box, or the pipeline's own runner) can also run `capabilities` directly into a scratch directory.

### Triggers

- `http_webhook`. Generic inbound HTTP receiver with configurable HMAC verification.
- `standard_webhook`. Standard Webhooks-compliant receiver.
- `grist_webhook`. Grist-specific, knows the payload shape and HMAC scheme.
- `github_webhook`. GitHub-specific.
- `slack_event`. Slack events (messages, mentions, and so on).
- `atomic_event`. Atomic knowledge-base changes. Atomic is a separate, self-hostable project (atomicapp.ai) Servitor integrates with; it is not built as part of Servitor.
- `email_received`. Inbound email parsed into a structured payload.
- *(more per-service trigger types as integrations are added)*
- `cron`. Honker scheduler.
- `manual`. Invoked via CLI.
- `internal`. Fired by another workflow's completion.

### Steps

Step types come in three kinds, roughly from most general to most specific:

#### Universal primitives

- `http`. Make an HTTP request, capture response.
- `shell`. Execute a command.
- `transform`. Reshape, extract, or compute over previous steps' JSON output, returning new JSON.
- `branch`. Conditional routing based on inputs.
- `foreach`. Fan out a step over a list.

#### Singer integration

- `singer-tap`. Run a Singer tap with config, capture records and state.
- `singer-target`. Run a Singer target consuming records.

#### MCP integration

- `mcp-call`. Invoke one named tool on one named MCP server as a subprocess
  (ADR-0015). An MCP server is a subprocess that exposes named tools, each with
  a JSON Schema for its input, over stdio. The step runs the server with a
  filtered secret env, sends a single `tools/call` request on stdin, reads the
  structured JSON response on stdout, and exits. Fields: `server` (which MCP
  server to run), `tool` (which named tool), and `input` (the tool arguments).
  Server packages are pinned the same way Singer taps are.

  `mcp-call` supports both the original MCP protocol (the `initialize` /
  `initialized` handshake) and the stateless revision that carries protocol
  version and capabilities inline in a `_meta` field, detecting which a server
  expects at discovery time and caching it. Tool schemas are discovered from the
  server once during a `capabilities` refresh and cached, not queried on every
  step execution. MCP tool results (an `isError` flag plus content blocks) map
  onto Servitor's structured validation error format.

  MCP is an integration mechanism for this step type, unrelated to the
  control-plane question of whether Servitor's own daemon interface is ever
  exposed over MCP, which stays out of scope (ADR-0005).

#### Curated integration helpers

Small wrappers around official SDKs for the services most commonly used. Each one is well-typed, well-documented, and ships with appropriate triggers and actions.

Initial set (subject to your stack's priorities):

- `grist`. Read, write, list, query.
- `slack`. Post messages, read events.
- `github`. Issues, PRs, releases.
- `email`. Send, parse incoming.

(Atomic is reached via the `mcp-call` step type against its native MCP server
rather than a hand-written helper, since it is low-frequency and the server is
self-hostable alongside the runner.)

Each helper uses varlock-injected secrets for auth and exposes its
actions/triggers via `servitor capabilities`.

**What counts as an integration.** The "X integrations" number in the tagline counts services, not mechanisms. Any service the runner can talk to via any dedicated mechanism (a Singer tap, a Singer target, a trigger type, a curated helper, or any combination) counts as one integration. Slack having both a `slack_event` trigger and a `slack` helper is one integration, not two.

---

## Execution model

1. **Trigger fires.** A webhook arrives, a cron fires, or someone calls `servitor trigger`.
2. **Event is persisted.** The raw event is written to Honker before any matching happens. Failed events, orphan events, and crash-survival all benefit from this.
3. **Signature verification.** The receiver verifies the signature against the relevant secret from varlock, in the parent process.
4. **Workflow matching.** The runner finds workflows whose `on:` block matches the event.
5. **Run enqueued.** A workflow run is created in Honker with the event payload as input. The run's initial step(s) are enqueued in the same transaction.
6. **Workers claim and execute.** A step executor in the parent process claims a job and checks the step's `dedupe_key` against the dedupe table. It spawns a subprocess with a filtered env containing only the secrets the step declared (every step runs as a subprocess; ADR-0008).
7. **Step runs.** Step types dispatch to handlers: HTTP, shell, transform, Singer tap, Singer target, integration helpers. A step writes its result as structured JSON to stdout and exits.
8. **Result committed transactionally.** When a step completes, its writes happen as a single atomic SQLite transaction: the step's result is persisted, the `dedupe_key` record is written (if any), all downstream steps whose dependencies are now satisfied are enqueued, and the step's own claim is acked, all in one commit. (For Singer steps, the updated bookmark is part of the same commit.) There is no separate scheduler process watching for completions; the worker that just finished the step performs these writes itself. This is non-negotiable because each possible split produces a distinct silent failure: result-without-enqueue stalls the workflow (a step is "done" but successors never run); enqueue-without-ack re-issues the claim on visibility timeout and re-runs the step, fanning out *again* and doubling every downstream side effect; dedupe-without-result causes future retries to skip the step without ever returning a value. The transactional atom is therefore **{result, dedupe_record, downstream_enqueues, claim_ack}**, all in one commit. If implementation pressure ever tempts splitting this transaction, the answer is no; redesign the data model instead.
9. **Crashes are safe, with a caveat.** If a subprocess dies, the parent records the failure and the executor reclaims through normal retry. If the parent dies mid-job, Honker's visibility timeout re-issues the claim to another runner instance (or to itself on restart). **Crash safety against double-firing of side effects only applies to steps that declare a `dedupe_key`.** Steps without one inherit Honker's at-least-once contract: a step whose side effect completes before the result is persisted may be re-issued and the side effect re-performed. The validator warns when a side-effecting step omits `dedupe_key` precisely to make this contract visible to authors.

---

## Idempotency and deduplication

Honker's at-least-once delivery means a step can run more than once: a worker can complete a side effect, crash before acking, and have its claim re-issued to another worker. The naive answer is "steps must be idempotent or use a deduplication key," but that is load-bearing enough to deserve a real primitive, not a footnote.

The runner provides:

- **`dedupe_key` field on every step.** A string expression evaluated against the step's inputs (often the trigger event ID, a row ID, or a hash). Before the step executes (whether by spawning a subprocess or running in-process), the parent checks a `step_dedupe` table keyed by `(workflow_id, step_name, dedupe_key)`. If the key is present and the prior run succeeded, the step is skipped and the prior result is returned. If the key is present and the prior run failed, the step proceeds.
- **A short retention window on dedupe keys** (default 72h, configurable per step) so the table doesn't grow unboundedly.
- **Default off, but loudly recommended.** Validation emits a warning when a step performs an externally-visible side effect (sending a message, creating a row, calling a non-idempotent HTTP method) and has no `dedupe_key`. Agents see this warning as a structured error of severity `warn` and can decide whether to suppress.

For Singer taps specifically: a tap that completes records and crashes before its bookmark is persisted will re-emit those records on the next run. Targets should handle this with their own dedupe (most warehouse targets do via primary keys); for action-shaped uses downstream of a tap, set `dedupe_key` on the action step.

---

## Structured validation errors

Because agents are first-class authors, the shape of validation errors is part of the design, not an afterthought. Every error returned by `servitor submit`, `servitor update`, or `servitor dry-run` follows this shape:

```json
{
  "errors": [
    {
      "path": "/steps/2/channel",
      "code": "missing_required_field",
      "message": "field 'channel' is required for step type 'slack' action 'post_message'",
      "expected": "string"
    },
    {
      "path": "/steps/3/type",
      "code": "unknown_step_type",
      "message": "unknown step type 'slak'",
      "suggestion": "slack"
    }
  ],
  "warnings": [
    {
      "path": "/steps/2",
      "code": "missing_dedupe_key",
      "message": "step performs an external side effect and has no dedupe_key; this step may run more than once on retry"
    }
  ]
}
```

Codes are stable identifiers (`unknown_step_type`, `missing_required_field`, `type_mismatch`, `missing_secret`, `circular_dependency`, `missing_dedupe_key`, etc.). Paths are JSON Pointers into the submitted YAML. Multiple errors are returned at once, not one-at-a-time, so an agent fixing a malformed workflow makes one round trip per fix-batch rather than one per fix. A `missing_secret` warning is emitted by `dry-run` when a step declares a secret that is not present in the environment; the workflow's declared secret names are shown redacted (`<redacted:secret_name>`), never their values.

The full workflow JSON Schema and every step type's config schema are also retrievable through `servitor capabilities`, so agents can validate locally before submitting.

---

## Authentication

The control plane is a CLI talking to the daemon over loopback. The daemon binds `127.0.0.1` only and refuses a non-loopback interface, so the only thing that can reach it is a process already on that host. Changing behavior is gated through a reviewed pipeline, and operating the runner runs on the box (ADR-0009); there is no operator authentication inside the daemon because the network surface it would protect does not exist.

Getting onto the box is the operator's existing access (SSH or VPN), not a Servitor feature. A deployment that wants remote management puts the box itself behind the operator's network boundary; Servitor does not stand up its own auth/proxy stack.

"Auth" also shows up in two places that are not operator authentication:

- **Webhook signature verification** (inbound triggers) is not user auth; see the Standard Webhooks and per-provider trigger type sections. Secrets come from varlock.
- **Outbound credentials** for integrated services live in varlock. Long-lived API tokens (Slack bot tokens, GitHub PATs, Grist API keys, Stripe restricted keys) are preferred over OAuth flows. This is a deliberate scope choice: the runner is for self-hosted single-team deployments, not multi-tenant SaaS, so the OAuth-flow-and-refresh complexity isn't warranted in the core.

---

## Design principles

**Small interfaces compose well.** Honker handles durability. Varlock handles secrets. Singer handles record-stream integration. Standard Webhooks handles modern webhook reception. The CLI and daemon protocol handle control. This runner is the glue. Each piece does its narrow job; replacing any one doesn't affect the others.

**The workflow is fully defined by the Wafer, nowhere else.** No state lives in a UI. Workflows are version-controllable, diff-able, and reviewable. Agents and humans manipulate the same artifact.

**Agents are first-class authors, not bolted-on consumers.** Capability discovery, structured validation errors, schema introspection, dry-run support, the `dedupe_key` primitive: these exist because agents need them, not as afterthoughts. The "Why agent-first changes the design" section makes the case in full.

**Truly open source.** MIT-licensed, no open core, no enterprise tier, no feature paywall. The thing you self-host is the whole thing.

**Code-first.** No visual builder. The Wafer is the artifact.

**Delegate hard problems to maintained tools.** Identity, secret storage, webhook signing: each of these is owned by a project that does it full-time. The runner does not reimplement any of them.

**Honest about scope.** This is for self-hosted single-tenant deployments. SQLite single-writer means one runner process owns the database. Multi-host scaling is a different problem and a different tool.

---

## Roadmap

Things deliberately out of scope for v1, kept here so the design doesn't quietly commit to them later:

- **Multi-host scaling.** SQLite rules this out for the core runner. A different tool, not a different version of this one.
- **MCP adapter.** Deferred until there is a concrete user; the daemon protocol is kept transport-agnostic so it can be added later (ADR-0005).
- **OAuth flow management for outbound credentials.** Long-lived tokens cover the realistic self-hosted single-team case. Revisit if a critical integration becomes OAuth-only.
- **A policy engine for permissions.** Role strings cover the realistic case. Revisit if real deployments hit the ceiling.

---

## Status

Early development. The daemon lifecycle, loopback control protocol, Wafer model and structured validation, capability discovery (including a `secrets.yaml` reporting declared secret names and presence), dry-run DAG resolution (including redacted secret names and a `missing_secret` warning), the Honker durability store (with the transactional atom), step execution (the worker loop, subprocess isolation with env filtering, the dedupe contract, and cron triggers), inbound triggers (a webhook receiver for Standard Webhooks and generic HMAC, plus manual), the varlock integration (self-healing launch and per-step secret filtering), the shipped `SKILL.md` agent reference, run inspection (`servitor runs`, `servitor run <id>`, `servitor cancel`), and the release flow (`make release`) are built (`servitor run`, `stop`, `dry-run`, `capabilities`, `submit`, `update`, `enable`, `disable`, `trigger`, `runs`, `run`, `cancel`). Provider-specific webhook receivers, the `internal` trigger, and the remaining step handlers (transform, branch, foreach) are not yet built. The Singer integration (Phase 11) and the `mcp-call` step type (Phase 12, ADR-0015) are planned. Open questions, to be resolved as implementation progresses and tracked in ADRs in the `docs/adr/` directory:

- Worker concurrency limits; runs currently execute as a sequential step chain, and parallel fan-out is deferred (ADR-0012).
- The exact shape of the `dedupe_key` expression language (resolved values are supplied at enqueue time for now).
- Which expression language `transform` uses. It runs as a subprocess, so the evaluator has no host access by construction (no file, shell, or network reach); the only remaining constraint is that evaluation is bounded.
 - The trigger receiver's framing of bespoke per-provider signing schemes.
 - How the varlock parent handles termination signals: whether `varlock run` forwards SIGTERM/SIGINT to the runner child and propagates its exit code. If it does not, the runner is wrapped with a minimal init such as `tini` or `dumb-init` so signals reach it cleanly.

Contributions welcome once the initial scaffolding is in place.

## License

MIT.
