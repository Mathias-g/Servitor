---
status: accepted
date: 2026-08-25
decision-makers: [Mathias]
consulted: []
informed: []
scope:
  - runner
  - singer
interface-impact: none
---

# ADR-0016: Singer taps and targets are invoked with file flags

## Context and problem statement

A Singer tap is a CLI that emits records from a source; a target is a CLI that
consumes records. Servitor runs every step as a subprocess with a filtered
secret env (ADR-0008). To run a tap or target we must decide how config and the
prior incremental-sync bookmark reach the subprocess, and how the next bookmark
comes back. The ecosystem is built on singer-python and the Meltano SDK, whose
taps and targets take config as a file path on the command line (`--config
<file>`), with `--state <file>` and `--catalog <file>` for a bookmark and
stream selection. The execution model already writes a step's input to the
subprocess stdin as JSON and reads its output from stdout (SPEC: Step
execution), so there are two workable shapes.

## Decision drivers

- Keep the subprocess-per-step isolation model (ADR-0008) and the filtered-secret
  trust boundary: a step sees only what it declared.
- Run the real ecosystem unmodified: singer-python taps and targets take file
  flags, not a stdin invocation, so matching their contract removes any per-tap
  shim.
- State must round-trip: the prior bookmark goes in, the next bookmark comes
  back and persists with the step's result (SPEC: Execution model step 8).
- Best Simple System for Now: no wrapper scripts or per-deployment adapters.

## Considered options

- **Write temp files and pass file flags (chosen).** The executor writes the
  tap's config (and prior state, and a selected-stream catalog, when present) to
  temp files and invokes `tap --config <file> [--state <file>] [--catalog
  <file>]`. A target receives its config the same way (`--config <file>`) and
  the records on stdin. The next bookmark comes back as the tap's last STATE
  message on stdout.
- **Feed the whole invocation on stdin.** Write one JSON object
  (`{"config": ..., "streams": [...], "state": ...}`) to stdin. Rejected:
  real singer-python taps do not read an invocation on stdin, so this would
  require a per-tap shim to translate stdin to the file flags the tap actually
  wants.

## Decision outcome

Chosen option: **write temp files and pass file flags.**

The tap executor writes config (and state/catalog, when present) to temp files
and invokes the tap with `--config`, `--state`, and `--catalog`; it reads the
newline delimited Singer messages on stdout and returns the records and the
last STATE value as the next bookmark. `--state` is input only: the next
bookmark comes back on stdout, matching how singer orchestrators round-trip
state. A target receives its config via `--config <file>` and the records to
consume on stdin as newline delimited RECORD messages. Temp files are created
with 0600 perms because the config may carry secrets, and are removed after the
run. The bookmark is persisted in the same SQLite transaction as the step's
result (SPEC: Execution model step 8).

The catalog is authored into the Wafer, not re-discovered at execution: a
`singer-tap` step takes a `catalog` field holding the streams to sync, copied
verbatim from `servitor capabilities`. Discovery runs once, at a capabilities
refresh, and the report emits the catalog in the exact shape the Wafer accepts,
so what an agent sees is what runs. The executor just writes that catalog to a
temp file and passes `--catalog`; it never runs `--discover` itself. Omitting
`catalog` syncs all the tap's streams.

### Consequences

- Good: runs real singer-python and Meltano taps and targets unmodified, with no
  per-tap shim.
- Good: the transactional atom (result + bookmark + downstream + claim ack in
  one commit) is directly expressible, since the next bookmark comes back on
  stdout.
- Good: discovery happens exactly once, at a capabilities refresh; the executor
  never re-discovers, matching the cached-discovery intent of SPEC: Capability
  discovery.
- Bad: the executor manages temp files on the host (create, clean up), a small
  cost in exchange for not shipping a shim.
- Neutral: stream selection (`--catalog`) requires the agent to paste the
  selected streams from capabilities into the Wafer, making the Wafer slightly
  longer, in exchange for no per-run discovery cost.
- Neutral: the contract is validated against a fake tap fixture that speaks the
  Singer stdout protocol and the file-flag convention, so the protocol parsing,
  the run-and-read shape, and the bookmark round-trip are pinned without any
  real tap installed.

### Confirmation

`go test ./...` passes. The tap and target executors and the bookmark
round-trip (passed back as a `--state` file on the next invocation) are each
pinned by tests.

## Interface notes

No change to the Wafer schema, the CLI surface, or the daemon control protocol.
The `singer-tap` and `singer-target` step types already exist (SPEC: Singer,
data movement integrations); this ADR fixes how their subprocesses are invoked.

## More information

- ADR-0008 (subprocess-per-step isolation)
- SPEC: Singer, data movement integrations; Execution model step 8; Idempotency
