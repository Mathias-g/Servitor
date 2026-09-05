---
status: proposed
date: 2026-09-05
decision-makers: [maintainer]
consulted: []
informed: []
scope:
  - config
  - registry
  - capabilities
  - validation
interface-impact: new
---

# ADR-0055: Disable mechanisms per deployment, a config-level off switch

## Context and problem statement

The mechanism registry is the compiled-in set (ADR-0045, ADR-0048): every
capability a deployment can use is there unless its folder is deleted, which
means a rebuild and a permanent fork. This decision gives the operator the
per-deployment alternative: disable any mechanism in `servitor.config.yaml`, so
a deployment can, for example, turn off `core/shell` and make that capability
impossible to use on this Servitor without touching the binary.

## Decision drivers

- The operator must be able to make a capability unavailable on a deployment
  without a rebuild or a fork.
- Disabled must mean impossible to use, not merely unlisted: validation rejects
  a Wafer that uses it, and its run handler is unreachable.
- Disable is per capability, not per mechanism tree: a base mechanism can be
  disabled while one of its flavors stays enabled (ADR-0054).
- Servitor is agent-first, so a disabled capability must stay visible as
  disabled, not vanish, so an agent can explain why a Wafer fails and point at
  the alternative.
- It composes with the rest of the declared config (receivers, connectors,
  secret sources).

## Considered options

- Option A: Delete the mechanism's folder. Rejected: it is a rebuild and a
  permanent fork, which is exactly what this feature replaces.
- Option B (chosen): Disable in `servitor.config.yaml`, a blocklist filter over
  the compiled-in registry at load.
- Option C: An allowlist (enable only these, everything else off). Rejected as
  the mechanism: blocklist plus group disablement already expresses an
  allowlist posture (disable every capability not wanted), with no separate
  allowlist mode.

## Decision outcome

Chosen option: "Option B".

Disable is not a lock on a fake availability parameter. It is a config-level
decision that a capability does not exist on this deployment. A disabled
capability is impossible to use: validation rejects any Wafer that uses it, its
run handler is unreachable, and `capabilities` surfaces it as disabled.

Disable is per capability, not per mechanism tree. Each capability, the base
mechanism and each of its flavors, is independently disableable. A base mechanism
can be disabled while one of its flavors stays enabled, which is the point of
flavors: an operator who thinks "shell is dangerous, I want a more constrained
version" makes a flavor and disables the base, leaving the flavor as the only
shell available. Validation checks the specific capability named in the Wafer,
so `type: shell` fails while `type: shell-scripts-only` passes.

How it behaves:

- The registry stays the compiled-in set; the config filters it at load. A
  disabled mechanism is still registered but marked disabled. Disable is a
  blocklist: the operator disables the specific capabilities they do not want.
- Disable applies per capability, and a mechanism group is disabled by disabling
  every capability in it (for example disable all of `webhook`). Group
  disablement matters so that a future mechanism added to a disabled group is
  not silently left enabled: disabling the group means everything in it, now and
  later, is off.
- `capabilities` reports a disabled capability explicitly (for example a
  `disabled: true` marker on the entry and in the index), rather than letting it
  vanish. This is critical because Servitor is agent-first: "this exists here but
  is off" is different information from "this server does not have it", and an
  agent that sees the capability listed can tell the user why a Wafer using it
  fails and point at the available alternative.
- Validation rejects a Wafer that uses a disabled mechanism, at dry-run and at
  submit, with a clear error naming the disabled mechanism and the config entry.
  A Wafer cannot be registered with a disabled node or trigger type. On top of
  validation, a disabled capability's run handler is also unreachable, defense
  in depth, so even if validation were bypassed the handler could not run.
- The declared config is loaded once at boot, so toggling a disable takes effect
  on daemon restart, consistent with the existing load-once-at-boot pattern. No
  change-detection machinery is added now.
- The disabled state composes with the rest of the declared config: a webhook
  receiver whose mechanism is disabled, a declared MCP server or Singer tap
  whose mechanism is disabled, and a secret whose `source` mechanism is
  disabled. A dependency on a disabled mechanism fails at config load with a
  clear error, rather than failing later at use: a broken deployment is caught
  early, at load, not at run time.

### Consequences

- Good: the trust boundary is operator-declarable, a deployment that never wants
  `shell` can disable it rather than "be careful" or fork.
- Good: the flavor version means "replace it with a constrained one" is
  available too, and the two are independent per capability.
- Good: agent-first visibility, a disabled capability is reported as disabled,
  not removed.
- Bad: toggling a disable takes effect on restart, not live; change detection is
  deliberately not added now.
- Neutral: the registry is unchanged, disable is a config filter at load.

### Confirmation

Tests pin that validation rejects a Wafer using a disabled mechanism at dry-run
and submit, that `capabilities` marks the capability disabled rather than
removing it, that a base can be disabled while its flavor stays enabled (the
flavor's Wafer passes, the base's fails), that a group disablement covers a
future mechanism added to the group, and that a dependency on a disabled
mechanism fails at config load. `go test ./...` stays green.

## Interface notes

Adds to the declared config (`servitor.config.yaml`): a disable surface listing
the capabilities and mechanism groups to disable. Adds to the `capabilities`
output: a `disabled: true` marker on a disabled capability's entry and in the
index. Adds to validation: a clear error when a Wafer uses a disabled mechanism,
and a config-load error when a dependency (a receiver, a connector, or a secret
source) is disabled. Existing Wafers and deployments are unaffected unless a
capability they use is disabled.

## More information

- ADR-0045 (self-registering mechanism packages; the compiled-in registry this
  filters)
- ADR-0048 (mechanism folders, the unit of deletion this feature replaces)
- ADR-0054 (flavors, which a base mechanism can be disabled beside)
- ADR-0051 (the lock model, which disable composes with)
- SPEC: Disable mechanisms
