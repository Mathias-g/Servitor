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

# ADR-0032: Resolve secrets through a pluggable provider, not the process environment

## Context and problem statement

The current model (ADR-0014, ADR-0029, both since superseded) resolves the whole secret set into the
runner process at boot: varlock injects resolved values as process environment
variables, the daemon reads them into a global secret map, and node subprocesses
are filtered to the subset a node declared. This has two limits. The long-lived
daemon holds every secret in memory for its whole life, so a compromised daemon,
a core dump, or a memory read exposes the full set. And the mechanism is tied to
a single backing store (whatever varlock points at), resolved once at boot, so a
deployment that wants a different store or per-node resolution has no seam to
do it through (SPEC: Secret resolution).

The goal is to move to a model where where a secret comes from is pluggable and
when it is resolved is per node (SPEC: Secret resolution). This ADR records the
generalization half: the provider interface. The per-node delivery half is
ADR-0033; the two are deliberately separate decisions.

## Decision drivers

- The source of secrets must not be fixed by Servitor; a deployment keeps the
  secret store it already has (Bitwarden, 1Password, Vault/OpenBao, AWS Secrets
  Manager, a TPM-sealed file, plain env).
- No universal plugin registry up front (ADR-0002): add providers only as they
  are actually needed, not a registry to hold them all.
- A provider must be able to resolve on demand and per node, fast enough for
  that to be the normal path, so it is an in-process interface, not a per-node
  CLI spawn.
- Failure semantics must distinguish the source being unreachable, the secret
  being missing, and the secret being stale or invalid, because those are
  handled differently (SPEC: Secret invalidity and rotation).

## Considered options

- **A narrow provider interface, in-process (chosen).** A contract roughly
  `Resolve(ctx, nodeName, secretName) -> value`, with caching and expiry as
  provider properties and the three-way failure semantics above. A provider
  encapsulates its own mechanism (its own on-box unlock: a local key,
  TPM/vTPM, or an off-box KMS call; or a store credential for the pull
  arrangements). Multiple providers coexist, with per-secret routing and
  optional failover.
- **Keep the process-environment model.** Resolve everything at boot into env
  vars, read them into a global map. Rejected: fixes the mechanism to one store
  and one time, holds the full set for the daemon's life, and offers no seam for
  per-node resolution.
- **Per-node CLI resolution.** Spawn a CLI per secret, per node, at execution.
  Rejected in ADR-0033's scope: the mechanism it replaces boots in ~290ms per
  invocation, making per-node resolution infeasible; the provider must be
  in-process (milliseconds).
- **A universal plugin registry.** A full plugin system for every conceivable
  provider up front. Rejected by ADR-0002 (BSSN): no registry before there is
  more than one provider to register.

## Decision outcome

Chosen option: **a narrow, in-process secret-provider interface.**

The provider is a Go interface (roughly `Resolve(ctx, nodeName, secretName) ->
value`). It slots in where the process-environment secret map is read today. A
provider encapsulates its own mechanism: its own on-box unlock (local key,
TPM/vTPM, or off-box KMS) for the on-box arrangements, or a store credential for
the pull arrangements. Caching and expiry are provider properties, not part of
the contract. Failure returns distinguish the source being unreachable, the
secret being missing, and the secret being stale or invalid (SPEC: Secret
invalidity and rotation). Multiple providers coexist with per-secret routing
and optional failover; the recommendation is a single provider backed by the
push-delivered on-box at-rest store (SPEC: Secret resolution).

The three axes a provider bundles (ingress, storage, unlock) are composed
*within* a provider in code; they are not independently configured at runtime,
and they are not runtime-pluggable seams. Shared components (one TPM unlock, one
KMS call, one on-box ciphertext store) live in a shared internal library so a
provider does not reimplement them (SPEC: Secret resolution).

At-rest protection is part of the on-box mechanisms. Secrets must never be
plaintext on disk: TPM is the primary unlock tier, with a non-TPM fallback (an
off-box KMS key or a strong local-key file) that still holds the line against
plaintext. The key-custody distinction that makes this a real security
difference is that an **off-box or hardware-bound key is non-exportable** (a KMS
key or a TPM seal cannot be copied off), so a thief who steals the disk or a
backup gets only ciphertext and cannot decrypt it anywhere else. That is the
genuine win over the peers, who keep a *copyable* key in the same environment
as the ciphertext. It is not a complete boundary: it does not protect the value
in the runtime window (the plaintext is in the runner's memory and the
subprocess either way), and it does not stop code already running as the
runner's user from calling the decryption service or TPM on demand. So at-rest
key custody protects against disk/backup theft, not against a compromised
daemon (SPEC: Secret resolution).

Recoverability follows from the same arrangement: the box holds only derived
ciphertext, and the origin (the store, or the material CI/CD pushes) lives
elsewhere, so losing the box costs nothing durable, you simply run the setup
again. The non-exportable key protects only against the thief who steals the
disk, not against a lost box.

### Consequences

- Good: the source of secrets is the operator's choice, not Servitor's; a
  deployment keeps the store it already has.
- Good: per-node, on-demand resolution is the normal path (ADR-0033), which
  directly addresses the "daemon holds the full set" gap.
- Good: failure semantics give each of the three cases a distinct, correct
  handling rather than one blanket failure.
- Bad: introduces an abstraction where the current model has none; the provider
  interface and at least one implementation must be built.
- Neutral: the first provider (the on-box at-rest store) is the recommended
  default; additional pull providers are added as deployments need them, not up
  front.

### Confirmation

`go test ./...` passes. A test asserts that a provider is consulted per node at
execution time, that the three failure semantics map to their distinct errors,
and that a provider returns the value for the node's declared secret.

## Interface notes

No change to the Wafer schema, CLI surface, or daemon control protocol. The
provider is an internal runner interface; the Wafer continues to name secrets by
secret name (the node `secrets:` list is unchanged). The daemon's secret map is
replaced by the provider.

## More information

- SPEC: Secret resolution, Secret invalidity and rotation
- ADR-0033 (per-node per-subprocess delivery, the other half of this model)
- ADR-0034 (varlock's demotion to an optional pull provider)
- ADR-0035 (declared secrets config and the operator-facing surface)
- ADR-0036 (secret-resolution mechanism group and secret role)
- ADR-0008 (subprocess-per-step isolation, which per-node delivery relies on)
- IDEAS.md (the exploration this decision grew from)
