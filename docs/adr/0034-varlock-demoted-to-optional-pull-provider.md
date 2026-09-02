---
status: accepted
date: 2026-08-29
decision-makers: [Mathias]
consulted: []
informed: []
scope:
  - runner
  - varlock
interface-impact: none
---

# ADR-0034: Varlock becomes an optional pull provider, not the boot mechanism

## Context and problem statement

Varlock is currently the runner's secret mechanism: `servitor run` self-heals
under `varlock run --inject vars`, varlock resolves the whole set into the
process environment, and the daemon reads it into a global secret map
(ADR-0014, ADR-0029). The new secrets model replaces that: secrets resolve
per node through an in-process provider (ADR-0032, ADR-0033). Varlock's Node CLI
is slow to boot (~290ms per invocation), which is exactly why it cannot serve
per-node resolution, and the recommended secret mechanism does not need it.

## Decision drivers

- Per-node resolution must be fast, so the mechanism is in-process (ADR-0033);
  a per-node CLI boot at ~290ms is infeasible.
- The recommended secret mechanism (push-based on-box ciphertext) does not use
  varlock at all; varlock is only one possible slow origin.
- Keep varlock where it is still useful: a slow external store is legitimately
  one pull source, fetched once into the on-box at-rest store and then resolved
  per node from the local copy.

## Considered options

- **Demote varlock to an optional pull provider (chosen).** Varlock is no longer
  the boot mechanism. It survives, if a deployment wants it, as one pull
  provider that fetches each value once into the on-box at-rest store, from
  which nodes resolve per node. It is absent from the default mechanism.
- **Keep varlock as the boot mechanism.** Reject: it re-commits to the
  resolve-everything-at-boot model (ADR-0033) and to a per-node CLI that is too
  slow for per-node resolution.
- **Drop varlock entirely.** Reject: it is a legitimate slow pull source for
  deployments that use it, and its schema-driven, agent-visible secret definition
  is worth keeping (ADR-0035).

## Decision outcome

Chosen option: **varlock is demoted to an optional pull provider.**

Varlock is no longer the runner's boot mechanism; the self-healing launch under
`varlock run` is removed. It remains available, if a deployment chooses it, as
one pull provider: fetch each value once into the on-box at-rest store, then
resolve per node from the local copy. It is not part of the recommended default
mechanism (SPEC: Secret resolution). This is a deliberate architectural change
to a system the runner currently relies on, recorded as a decision rather than a
silent byproduct.

### Consequences

- Good: the runner no longer resolves the full secret set into the process
  environment at boot (ADR-0033).
- Good: the default mechanism no longer depends on varlock; deployments that
  push their secrets skip it entirely.
- Good: varlock's schema-driven, agent-visible secret definition survives as the
  declared-secrets surface (ADR-0035).
- Neutral: varlock remains usable as a pull source for deployments that want it.

### Confirmation

`go test ./...` passes. Tests assert that the runner can boot and resolve
without varlock, that a varlock pull provider (when used) resolves into the
on-box store and is then served per node, and that the self-healing `varlock
run` invocation is no longer part of the default boot path.

## Interface notes

No change to the Wafer schema or daemon control protocol. The CLI `servitor run`
surface is unchanged, but its documented behavior changes: it no longer
self-heals under `varlock run`.

## More information

- SPEC: Secret resolution, Varlock (removed/replaced)
- ADR-0032 (provider interface), ADR-0033 (per-node delivery)
- ADR-0014 (superseded: the self-healing boot it established is removed)
- ADR-0029 (superseded: its boot-mechanism decisions are overruled here; its
  surviving redaction decision is re-homed in ADR-0050)
- IDEAS.md (the exploration this decision grew from)
