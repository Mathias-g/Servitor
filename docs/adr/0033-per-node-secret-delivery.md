---
status: accepted
date: 2026-08-29
decision-makers: [Mathias]
consulted: []
informed: []
scope:
  - runner
  - secrets
interface-impact: none
---

# ADR-0033: Deliver secrets per node, per subprocess, and hold nothing past it

## Context and problem statement

ADR-0032 records the generalization half of the new secrets model: a pluggable
provider in place of the process environment. This ADR records the other half,
the security invariant: secrets are delivered to a node only at the moment it
runs, in that node's subprocess, and the runner does not hold the value past
that subprocess (SPEC: Secret resolution). This is what actually closes the
"long-lived daemon holds the full secret set in memory" gap that motivates the
model (ADR-0014, ADR-0029, and the peers' resolve-and-hold behavior all hold
values at the worker level).

## Decision drivers

- A node's subprocess sees only its own declared secrets (ADR-0008); that
  isolation is the boundary and is unchanged.
- The daemon should not hold a secret it does not currently need; each node's
  value should die with its subprocess.
- A resolved secret may flow only where it must: to the declaring node's
  subprocess, or to an external provider for the purpose of authenticating.
- The mechanism must be in-process (the provider of ADR-0032), because per-node
  resolution must be milliseconds, not the ~290ms boot of the CLI it replaces.

## Considered options

- **Per-node, per-subprocess delivery with egress rule (chosen).** The runner
  resolves each secret at the moment its node runs, hands it to that one
  subprocess's filtered env, and drops the reference when the subprocess
  completes. The value is gone once the subprocess completes.
- **Hold all secrets for the daemon's life (status quo).** Resolve everything
  at boot and filter per node. Rejected: this is exactly the gap being closed;
  the daemon holds the full set for its whole life.
- **Resolve per node but cache in the daemon.** Hold resolved values between
  node runs to avoid re-resolution. Rejected as the default: it re-introduces
  the daemon holding the set. Caching with expiry is a provider property
  (ADR-0032), not a daemon behavior, so it stays under the provider's control.

## Decision outcome

Chosen option: **per-node, per-subprocess delivery with an egress rule.**

The runner resolves each secret when its node runs, hands it to that node's
subprocess through the filtered env, and holds nothing past that subprocess: the
value is gone once the subprocess completes. A resolved secret may flow only to
the declaring node's subprocess, or to an external provider for the purpose of
authenticating, and is eliminated after. It cannot go anywhere else. The runner
resolves exactly the union of secrets the registered Wafers reference, so if the
last Wafer using a secret is removed, the runner stops resolving it.

Two honest limits are part of this decision, not caveats to hide:

- **"Gone after use" means no longer reachable, not erased from memory.** Go
  strings are immutable and the garbage collector does not zero memory, so there
  is no way to force a secret's bytes out of RAM. What the invariant provides is
  reachability: once the runner drops its reference, no running code in the
  runner can reach the value, and the subprocess that held it is gone. A fully
  memory-compromised process (a core dump or a read of the runner's heap in that
  window) could still find stale bytes, but an attacker with that level of
  memory access can read the next resolve in plaintext anyway, so this is
  defense-in-depth the runner cannot provide, and it is out of scope.
- **Redaction keeps operating on the running node's filtered env.** Redaction
  (ADR-0029) scrubs a granted secret value from captured output by scanning the
  node's filtered env. Per-node delivery holds a value only while its node runs,
  which is exactly the window redaction needs, and redaction only ever scrubs
  values the node was granted. So the new model must keep redaction operating on
  the running node's filtered env, not on a global secret map, because under
  per-node delivery there is no global map to redact from. This is what makes
  per-node delivery compose with redaction instead of breaking it.

### Consequences

- Good: the daemon no longer holds the full secret set; each value dies with its
  subprocess, closing the core gap.
- Good: the isolation contract (ADR-0008) is unchanged; a subprocess still sees
  only its declared secrets.
- Good: resolve-only-what-Wafers-use keeps the resolved set minimal and honest
  with the Wafers.
- Bad: per-node resolution means the value must be obtainable in milliseconds;
  this is what pushes the provider to be in-process (ADR-0032) and rules out a
  slow per-node CLI.
- Neutral: the zeroization and redaction limits above are stated honestly rather
  than promising more than the runtime can do.

### Confirmation

`go test ./...` passes. Tests assert that a node's subprocess receives only its
declared secrets, that the runner does not retain a node's resolved value after
the subprocess completes, that redaction still scrubs a granted value from a
node's captured output, and that resolve-only-what-Wafers-use stops resolving a
secret whose last Wafer is removed.

## Interface notes

No change to the Wafer schema, CLI surface, or daemon control protocol. The node
`secrets:` list and per-node env filtering are unchanged; the change is in when
and how the runner obtains the values.

## More information

- SPEC: Secret resolution, Execution model
- ADR-0032 (the provider this delivery relies on)
- ADR-0008 (subprocess-per-step isolation, the boundary this relies on)
- ADR-0029 (redaction, which composes as described above)
- IDEAS.md (the exploration this decision grew from)
