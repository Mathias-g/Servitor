---
status: accepted
date: 2026-08-25
decision-makers: [Mathias]
consulted: []
informed: []
scope:
  - runner
  - varlock
interface-impact: none
---

# ADR-0014: Resolve secrets by self-healing under `varlock run`, exposing the process env

## Context and problem statement

This wires varlock into the runner (SPEC: Varlock). The runner's steps
already filter the subprocess environment to declared secrets, and the webhook
receiver already reads signing secrets from a map; what was missing was a way to
*get* the resolved secrets into the runner and to start it in a way that cannot
silently run without them. The SPEC planned a self-healing launch: the runner
re-execs under varlock when the sentinel is absent. Two details had to be
settled: how the resolved secrets reach the runner, and what happens when
varlock is not installed.

## Decision drivers

- The operator should just run `servitor`; the runner must converge on a
  varlock-wrapped boot without the operator learning an inner command.
- The subprocess-per-step model (ADR-0008) is already the isolation boundary,
  so the runner process holding the full resolved set is safe: no subprocess
  ever sees a secret it did not declare.
- Best Simple System for Now: prefer the simplest plumbing over a more
  structured one that buys nothing at this scale.

## Considered options

- **Self-heal under `varlock run`, expose the process env (chosen).** When
  `__VARLOCK_RUN` is absent, the runner re-execs itself as `varlock run -- <self>
  <args>`. Varlock injects resolved secrets as individual env vars (the default
  `--inject all`); the runner reads the process env into its secret map. Per-step
  filtering (exec.FilteredEnv) keeps any undeclared value out of subprocesses.
- **Parse the `__VARLOCK_ENV` blob.** Varlock also injects a structured blob of
  the resolved set; parsing it gives a precise secret-only map rather than the
  whole env. More precise, but more coupling to the blob format, and the whole
  env is already safe given per-step filtering.
- **Fail hard when varlock is absent.** Refuse to boot the runner without
  varlock, matching an absolute "no secrets, no boot" guarantee. Rejected: it
  breaks local development and any deployment where secrets come from elsewhere.

## Decision outcome

Chosen option: **self-heal under `varlock run`, expose the process env.**

The runner re-execs under `varlock run -- <self> <args>` when `__VARLOCK_RUN` is
absent; the resolved env is the daemon's secret map; per-step filtering is the
isolation boundary. When varlock is not installed, the runner boots and prints a
warning that secret resolution is off, and steps that declare secrets fail
visibly, which is the signal that varlock is missing. (The SPEC's earlier
`--no-inject-graph` flag no longer exists in varlock 1.17; default injection is
used.)

### Consequences

- Good: `servitor` and a directly typed `servitor run` converge on the wrapped
  path; no inner command to learn.
- Good: no coupling to varlock's `__VARLOCK_ENV` blob format.
- Good: the isolation guarantee is unchanged; the subprocess env is still
  filtered to declared secrets (ADR-0008).
- Bad: the runner process holds the whole resolved environment (including any
  non-secret runner env vars). Acceptable because subprocesses cannot reach it.
- Neutral: varlock must be on PATH for the re-exec; without it the runner runs
  with a warning rather than failing.

### Confirmation

`go test ./...` passes. The varlock integration test (`TestSelfHealResolvesSecrets`)
runs a helper under a real `varlock run` with a schema and asserts the sentinel
and a declared secret are present; the end-to-end manual check confirmed a
declared secret reaches the step subprocess. The `wafer`/`runner` tests pin that a
step's `secrets:` list flows into the StepJob.

## Interface notes

No change to the Wafer schema, CLI surface, or daemon control protocol. The step
schema gains the `secrets` array (additive), and `servitor run`'s documented
behavior now includes the self-healing launch.

## More information

- SPEC: Varlock, Step execution, Execution model
- ADR-0008 (subprocess-per-step isolation, the boundary this relies on)
