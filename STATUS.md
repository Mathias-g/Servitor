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

## What works

A runner daemon owns a WAL SQLite file (Honker extension loaded, single write
connection) and is driven over a loopback control protocol; every node runs as
a subprocess with a filtered environment, and each node's completion commits
its result, dedupe record, downstream enqueues, and claim ack as one
transaction (for Singer taps the bookmark is part of that same commit).

- **Node types.** `shell`, `http` (outbound request), `transform` (JSONata),
  and the flow nodes `switch` (route to one branch), `foreach` (fan a body out
  over a list, collect at a rejoin), `wait` (park the run and resume on a timer
  or a named signal, including a `wait` inside a `foreach` body), `send-signal`
  (wake a parked run in another workflow), and `rerun-failed` (re-run a failed
  run).
- **Integrations.** `singer-tap` and `singer-target` (with bookmark state and
  schema discovery), `mcp-stdio` and `mcp-http`, and the `email_received`
  trigger (Gmail polling).
- **Triggers.** `manual`, `cron`, `completed`, `failed`, and inbound webhooks
  via the `hmac-webhook` and `standard-webhook` mechanisms, with receivers
  declared in `servitor.config.yaml` and delivering the raw body.
- **Secrets.** Per-node, per-subprocess delivery through pluggable providers
  (`env`, `varlock`, `onbox`), a declared-secrets config with the `servitor
  secret` CLI, and secret invalidity/rotation semantics (missing fails fast,
  source-unreachable retries, stale retries with a fresh resolve).
- **CLI.** `servitor run`, `stop`, `dry-run`, `capabilities`, `submit`,
  `update`, `enable`, `disable`, `trigger`, `runs`, `run`, `cancel`, `resume`,
  `rerun`, `secret`, `webhook`, `mcp`, `tap`, `target`. `submit`/`update`/
  `dry-run` return structured validation errors (stable `code`, JSON Pointer
  `path`, `suggestion`, multiple errors at once, plus `warnings`), and
  `dry-run` resolves the run's dependency DAG and reports secret names redacted
  (`<redacted:name>`) with a `missing_secret` warning when one is absent.
- **Control plane for agents.** A shipped `SKILL.md` teaches the
  discover-author-dry-run-PR workflow; `capabilities` writes per-mechanism
  schemas and examples plus `secrets.yaml`, `singer/taps.yaml`, `mcp/servers.yaml`,
  and `webhook/receivers.yaml`.
- **Packaging.** Single Go binary, `make build` / `make release`; the only
  runtime dependency is the operator-supplied Honker extension.

## Not yet

- Curated helpers: `grist`, `slack`, `github`, and an `email` send node (SMTP).
- Grist and Atomic webhook receivers as config entries (their senders' schemes
  are not yet declared).
- The `onbox` secret provider's TPM/KMS unlock tier (sealing the key so it is
  non-exportable; the non-TPM local-key tier works today).
- Worker concurrency: branches of a DAG run sequentially, not in parallel.
- An agent authoring reference of committed Wafer examples (deferred until the
  type set stabilizes).

Contributions welcome once the initial scaffolding is in place.
