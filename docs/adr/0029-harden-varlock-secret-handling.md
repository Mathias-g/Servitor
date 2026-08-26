---
status: accepted
date: 2026-08-26
decision-makers: [Mathias]
consulted: []
informed: []
scope:
  - runner
  - varlock
interface-impact: none
---

# ADR-0029: Harden varlock secret handling

## Context and problem statement

ADR-0014 wired varlock into the runner with a self-healing launch. Revisiting
that integration against varlock's documented behavior surfaced four gaps in
how secrets are carried and how the process is supervised (SPEC: Varlock,
Step execution). Each is a small change, but together they harden the secret
handling and process topology that ADR-0014 established.

- The self-heal invoked varlock with its default injection, which puts the
  entire resolved secret graph into a single `__VARLOCK_ENV` environment
  variable on the long-lived daemon process, visible via the process
  environment and carried into core dumps.
- The self-heal used `cmd.Run` (spawn + wait), leaving a secret-free wrapper
  process above varlock. The wrapper had no signal handler, so a terminating
  signal sent to it did not reach varlock or the runner, orphaning the daemon
  and leaving it holding the SQLite write connection.
- A step's captured output was not scrubbed, so a step that echoed a secret it
  had been granted could carry that value back into the runner's persisted
  state and logs.
- Whether the daemon refuses or warns when steps declare secrets it cannot
  resolve was left to each step handler.

## Decision drivers

- The subprocess-per-step model (ADR-0008) is the isolation boundary; the
  daemon process holding resolved secrets is acceptable only if those secrets
  stay out of subprocesses, out of the daemon's environment dump, and out of
  captured step output.
- The operator should keep a clean process tree: the process they launch
  should be the one that handles signals, not a dead wrapper above varlock.
- Best Simple System for Now: no new mechanism where an existing one suffices.

## Considered options

- **`--inject vars` instead of default injection (chosen).** Varlock injects
  only individual secret env vars and omits the `__VARLOCK_ENV` blob, so the
  full secret graph is not carried in one variable. Individual vars are still
  injected, so resolution is unchanged; the runner's secret map is unchanged.
- **Keep default injection, ignore the blob.** The runner already reads the
  process env into its secret map and does not parse the blob, so this is the
  status quo ADR-0014 accepted. Rejected: it keeps an unused second copy of
  every secret in the daemon environment.
- **True exec instead of spawn (chosen).** `syscall.Exec` replaces the
  current process image with varlock, so the launched process becomes varlock
  and there is no lingering wrapper above it. Signals sent to the launched
  process reach varlock, which forwards them to the runner.
- **Keep spawn + wait.** The wrapper sits above varlock, dies on a signal
  without forwarding, and orphans the daemon. Rejected: it breaks graceful
  shutdown (SPEC: Graceful shutdown) and can leave a second daemon holding the
  same SQLite file.
- **Redact declared secret values from captured step output (chosen).** The
  exec package scrubs the values present in the step's filtered env from its
  captured stdout and stderr before the result is returned or persisted.
- **Leave step output unredacted.** Rejected: it lets a step's output leak a
  secret it was granted back into persisted state and logs.
- **Refuse to boot when any step declares an unresolvable secret (chosen).**
  Each step handler already refuses to run a step that declares a secret the
  runner does not have; the cli warns when varlock is absent. No new boot gate
  is added beyond what the handlers already enforce.
- **Fail hard at boot when varlock is absent.** Rejected in ADR-0014 and
  unchanged here: it breaks local development and deployments whose secrets
  come from elsewhere.

## Decision outcome

Chosen: the self-heal execs `varlock run --inject vars -- <self>`, the exec
package redacts declared secret values from captured step output, and the
existing per-step refusal to run on unresolvable secrets is confirmed as the
boot guard. ADR-0014 is superseded where it described the default injection
and the spawn-based self-heal.

### Consequences

- Good: the daemon no longer carries the full `__VARLOCK_ENV` secret graph in
  its environment; only the individual resolved vars are present.
- Good: the process tree is `manager -> varlock -> runner`; a terminating
  signal reaches the runner through varlock, so graceful shutdown and clean
  release of the SQLite write connection work, and the exit code propagates.
- Good: a step's captured output cannot carry a secret it was granted back
  into persisted state or logs.
- Good: the subprocess isolation contract (ADR-0008) is unchanged; steps still
  see only declared secrets plus PATH.
- Bad: `syscall.Exec` is Unix-only, so self-healing launch is not portable to
  Windows. Acceptable: the runner targets Linux servers, binds loopback only,
  and already requires cgo and a Unix-shaped build (ADR-0004).
- Neutral: exec means the self-heal no longer returns after the child exits;
  on success it never returns, and varlock propagates the exit code instead.

### Confirmation

`go test ./...` passes. Tests cover that a declared secret is redacted from a
step's stdout and stderr, that PATH is not treated as a secret, that `--inject
vars` is passed, and that the sentinel and secret resolution still work under
a real `varlock run`.

## Interface notes

No change to the Wafer schema, CLI surface, or daemon control protocol. The
self-heal invocation and process topology are internal; the CLI `run` behavior
and the documented varlock integration are unchanged from the operator's view.

## More information

- SPEC: Varlock, Step execution, Graceful shutdown
- ADR-0014 (superseded for the default-injection and spawn details)
- ADR-0008 (subprocess-per-step isolation, the boundary this relies on)
