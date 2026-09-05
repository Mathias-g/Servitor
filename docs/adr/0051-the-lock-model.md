---
status: proposed
date: 2026-09-05
decision-makers: [maintainer]
consulted: []
informed: []
scope:
  - config
  - runner
  - capabilities
interface-impact: new
---

# ADR-0051: The lock model, who sets a parameter, config or Wafer

## Context and problem statement

Every parameter on a mechanism, on every execution-surface category (SPEC:
Execution surface), has one cross-cutting question: who gets to set it? The
answer decides whether a flavor is a real constraint or a wish, whether egress
policy is operator-enforceable, and whether a disabled capability is a hard off
switch. There is no single, reusable answer today, so each of those features
would have to invent its own rule, and a future parameter would inherit nothing.
We need one generalized answer that any parameter, on any category, in any
mechanism, follows.

## Decision drivers

- The operator must be able to hard-constrain a parameter for security
  (the config pins it, the Wafer cannot override) or provide a convenience
  default (the Wafer may override).
- The Wafer author must keep freedom where the operator has not constrained.
- The rule must be easy to read and write for both humans and agents, in the
  config file and in the Wafer, and there must be nothing extra to remember.
- One generalized primitive, not a per-feature rule, so every future parameter
  inherits it.

## Considered options

- Option A: Three explicit lock values, each a separate field an operator
  writes (for example `lock: locked`, `lock: default`, `lock: wafer`).
  Rejected: it forces an operator to remember a lock-mode field and invite
  drift between the value and its stated mode.
- Option B (chosen): The lock value is implicit in how a value is written in
  the config, with one optional per-field marker. Absent means the Wafer sets
  it, a plain value means a default the Wafer may override, a value marked
  `locked: true` means the config pins it.
- Option C: No lock model, rely on prose. Rejected: it leaves the security
  boundary unenforceable and unverifiable.

## Decision outcome

Chosen option: "Option B".

A parameter has one of three lock values, named for where it is set:

- **config-locked**: the config pins the value and the Wafer cannot override
  it. The security-hard case.
- **config-default**: the config sets a default and the Wafer may override it.
  The convenience case.
- **wafer-set**: the config does not constrain the parameter at all, so the
  value comes from the Wafer. The config is not forbidden from setting a
  value, it simply does not, which is what distinguishes wafer-set from
  config-default.

The lock value is derived from how the parameter appears in the config, not
from a separate lock-mode setting:

- Not in the config at all is wafer-set.
- In the config as a plain value, not marked locked, is config-default.
- In the config marked `locked: true` is config-locked.

The lock is expressed per field as a `locked: true` marker beside that field.
For example:

```yaml
shell:
  egress:
    allow: [github.com]
    locked: true
```

Here egress is config-locked to `[github.com]`. A plain value without
`locked: true` would be config-default, and an absent entry is wafer-set. The
operator makes one decision per parameter they write: do they also mark it
locked. Writing a plain value is a default, writing it and locking it is a hard
constraint, not writing it at all leaves it to the Wafer.

The precedence rule, applied per parameter:

- config-locked: the config value governs, the Wafer's value is rejected at
  validation.
- config-default: the config value applies unless the Wafer overrides it.
- wafer-set: the Wafer's value applies; if the Wafer omits it, the parameter
  is unset.

A parameter that is wafer-set and omitted by the Wafer is simply unset: an
optional parameter that is not set behaves as off or absent. There is no
separate "built-in default" concept in the mechanism metadata, and none is
introduced. One parameter is governed by exactly one lock value; there is no
layering of locks within a single parameter.

The lock state is surfaced in `capabilities` for flavors (SPEC: Mechanism
flavors), so an agent sees, for each field, what it may set and what the config
has fixed, without a second lookup into the config file.

### Consequences

- Good: one generalized rule every parameter follows, so flavors, egress, and
  disable all rest on the same foundation and a future parameter inherits it.
- Good: nothing extra to remember, a plain config value is the common,
  least-surprising default, and locking is the deliberate extra step for a
  security constraint.
- Good: the lock state is visible to agents in `capabilities`, keeping the
  "is this settable" question answerable without reading the config file.
- Bad: the security posture is opt-in. An operator who intends config-locked
  but forgets the marker gets config-default, which is weaker than intended;
  the marker is the whole difference.
- Neutral: `capabilities` for a base mechanism stays a pure schema, the lock
  state shows on flavors.

### Confirmation

Tests pin the precedence behavior: a config-locked parameter rejects a Wafer
override at validation, a config-default parameter is overridden by a Wafer
value, a wafer-set parameter is free, and an omitted wafer-set parameter is
unset. A test pins the implicit derivation (absent, plain, locked map to the
three values). `go test ./...` stays green.

## Interface notes

Adds to the declared config (`servitor.config.yaml`): a `locked: true` marker
on a field, turning that field from config-default into config-locked. The
Wafer schema itself does not change, the fields are the same, but the effective
validation changes because a config-locked parameter cannot be overridden. For
flavors, the lock state is rendered in `capabilities` (SPEC: Mechanism
flavors). Operators writing config and agents authoring Wafers must understand
that a locked value cannot be overridden.

## More information

- ADR-0033 (per-node secret delivery, an execution parameter the lock model
  governs)
- ADR-0018 (the declared config file this lock syntax extends)
- SPEC: Execution surface
- SPEC: Mechanism flavors
- SPEC: Egress control
