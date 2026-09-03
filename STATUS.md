# Servitor status

This is the current-state snapshot: what actually works in Servitor today, in
product terms, as opposed to what is still aspirational. It is the single
answer to "can I do X yet?"

Each doc in the pipeline answers one question:

- [IDEAS.md](IDEAS.md): what might we build?
- [docs/adr/](docs/adr/): why did we decide what we decided?
- [SPEC.md](SPEC.md): what is the behavior contract (the target)?
- [PLAN.md](PLAN.md): how is the build sequenced, and what is each phase's scope?
- **STATUS.md (this file): what works today.**

Why a separate file: PLAN.md is append-only and keeps superseded phases as a
record (for example the varlock boot path was built then removed), so its
checkboxes answer "is this build task done" but not "what is the current state"
at a glance. STATUS.md is a freely-rewritten snapshot reflecting only the
present. It also keeps SPEC.md pure: status annotations do not belong in the
behavior contract.

STATUS.md is not the build ledger (PLAN.md is), not the behavior contract
(SPEC.md is), not decisions (ADRs are), and not ideas (IDEAS.md is). Keep it in
product language grouped by behavior, not by build phase. Keep it current in
the same change as the work that ships: a behavior change that lands updates
this file in that change.

## Current state

Early development, but the enforcement gates are wired (`make check`, adrlint, pre-commit, CI). The daemon lifecycle, loopback control protocol, Wafer model and structured validation, capability discovery (including a `secrets.yaml` reporting declared secret names, account, permissions, and expiry, a `secret-resolution` group enumerating the available secret sources, a `singer/taps.yaml` reporting declared Singer taps, and a `mcp/servers.yaml` reporting declared MCP servers; grouped by mechanism group per ADR-0031, sourced from the declared config per ADR-0018), dry-run DAG resolution (including redacted secret names and a `missing_secret` warning), the Honker durability store (with the transactional atom: {result, dedupe_record, downstream_enqueues, claim_ack} in one commit, a tested primitive; the daemon owns a WAL SQLite file with the Honker extension loaded), node execution (the worker loop, subprocess isolation with env filtering, and the dedupe contract), the secret-resolution model (a pluggable provider with per-node, per-subprocess delivery; the `env` and `varlock` providers; a declared-secrets section in the config and the `servitor secret` CLI; submit rejects a Wafer that references an undeclared secret; the varlock boot path is removed, ADR-0032 through ADR-0036), Singer (the `singer-tap` and `singer-target` executors, bookmark state committed with each tap node's result, and schema discovery; ADR-0016), MCP (the `mcp-stdio` node type with a client-mode executor, both classic and stateless protocol support, and structured error mapping; ADR-0015; the `mcp-http` Streamable HTTP executor, ADR-0047), inbound triggers (webhook receivers declared in `servitor.config.yaml` and verified by scheme, the `hmac-webhook` and `standard-webhook` mechanisms delivering the raw body, plus cron and manual), the shipped `SKILL.md` agent reference, run inspection (`servitor runs`, `servitor run <id>`, `servitor cancel`), and the release flow (`make release`) are built (`servitor run`, `stop`, `dry-run`, `capabilities`, `submit`, `update`, `enable`, `disable`, `trigger`, `runs`, `run`, `cancel`, `resume`, `rerun`, `secret`, `webhook`), the `transform` node handler and `dedupe_key` evaluation (JSONata via gn... (line truncated to 2000 chars)

## Not yet functional

- Webhook receivers for Grist and Atomic as config entries (their senders'
  schemes are not yet declared).
- The curated helpers (grist, slack, github, email send, the send side of
  email included).

## Open questions

Open questions, to be resolved as implementation progresses and tracked in ADRs in the `docs/adr/` directory:

- Worker concurrency limits; runs execute as a dependency DAG with fan-out (ADR-0023), but branches run sequentially rather than in parallel.

## Built

**Suspended waits: built.** The durable `wait` flow node (ADR-0040 through ADR-0043) parks a run and resumes it later via a timer (Honker queue `RunAt`, `timer.after` / `timer.at`) or a named signal (an author-defined JSONata `signal` name; senders are a `send-signal` node, `servitor resume <signal-name>`, or a webhook-triggered broker workflow). The run parks as `waiting`, shows in `servitor runs` / `servitor run <id>`, and `servitor cancel` drops the parked continuations. Race rules are pinned: a signal that arrives before the park is buffered, a repeat resume is a no-op, and a signal naming more than one parked wait is rejected as ambiguous. A wait inside a `foreach` body is supported: each iteration parks its own continuation, the run stays `waiting` until the last one resumes, and the rejoin collects all the wait results in input order.

**Rerun: built.** A dead-lettered node saves its self-contained job as a failed continuation and the run is marked failed (ADR-0044). `servitor rerun <run-id> [--mode ...]` re-runs it (`continue` from the failed node, `restart` from the top, `discard` to drop it), and a `rerun-failed` node lets one workflow re-run another (defaulting to `event.from_run`). A per-Wafer `on_failure` field sets the default mode. Rerun is general, applying to any failed run regardless of cause. The global-config layer for the mode is deferred until a servitor config file exists.

**Secrets model: largely implemented.** The Secret resolution section of SPEC.md describes the target secret model (ADR-0032 through ADR-0036). The provider interface, per-node delivery, the `env`, `varlock`, and `onbox` (push-based on-box ciphertext, sealed with `servitor secret seal`) providers, the declared-secrets config and `servitor secret` CLI, the capabilities surface, the varlock boot path removal, the secret-failure semantics (missing fails fast, source-unreachable retries with backoff, stale retries with a fresh resolve then fails with `secret_auth_failed`), the failed-run event (a dead-lettered node marks its run failed and fires the `failed` trigger, ADR-0039), and the resume-from-failure modes (continue/restart/discard, `servitor rerun`, a `rerun-failed` node, and a per-Wafer `on_failure` default; ADR-0044) are built. The `onbox` provider uses the non-TPM local-key unlock tier; TPM/KMS sealing of the key (the non-exportable tier) is future work.

Contributions welcome once the initial scaffolding is in place.
