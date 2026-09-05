---
status: proposed
date: 2026-09-05
decision-makers: [maintainer]
consulted: []
informed: []
scope:
  - config
  - capabilities
  - registry
interface-impact: new
---

# ADR-0054: Mechanism flavors, config-declared synthetic capabilities

## Context and problem statement

Servitor already treats variants as separate mechanisms whose type name carries
the variant (`hmac-webhook` vs `standard-webhook`, `mcp-stdio` vs `mcp-http`),
and the declared-connectors pattern (ADR-0018) lets the config declare named MCP
servers and Singer taps that a Wafer names as instances. A flavor generalizes
that: a config-declared, named, synthetic mechanism that refers to a real base
mechanism and pins a subset of its parameters. The operator gets per-deployment
constrained variants without a compiled-in type per combination or a fork. A
flavor is a real constraint rather than a wish only because the lock model
(ADR-0051) governs each pinned parameter.

## Decision drivers

- A deployment needs constrained variants (for example a shell restricted to
  operator-reviewed scripts, or a sandboxed shell) without a compiled-in type
  per combination and without a fork.
- A flavor must surface in `servitor capabilities` like any other capability, so
  an agent can discover and author against it.
- A flavor must be able to pin both function parameters and execution-surface
  parameters (ADR-0052), each with a lock value.
- A flavor is synthetic: it has no mechanism folder, it refers to a real base
  mechanism which does have a folder.

## Considered options

- Option A: A compiled-in mechanism per variant. Rejected: one type per
  combination does not scale, and it is the thing a flavor replaces.
- Option B (chosen): A generalized flavor framework: config-declared synthetic
  capabilities over a base mechanism.
- Option C: Per-mechanism config knobs only (for example a `shell` config with a
  scripts-only boolean). Rejected: it does not generalize, and it cannot pin
  execution parameter categories the way a flavor can.

## Decision outcome

Chosen option: "Option B".

A flavor names a base mechanism and pins a subset of its parameters. It is one
level: it names a base mechanism, not another flavor, so there is no stacking
and no ambiguity about what the base is. Flavors live in their own config
section (for example `flavors:` in `servitor.config.yaml`), distinct from the
declared-connectors sections: a flavor is a synthetic capability, not an
installed connector, though a flavor may reference a connector by name as one of
its pinned parameters.

A flavor inherits the things that define what it is and pins the things that
configure it. Inherited from the base: its Role (trigger, action, or flow), its
MechanismGroup (it appears under the base's group), its SideEffect and Delivery
properties, and its RunKind (it runs the same harness). These are fixed by the
base and cannot be changed by a flavor. Pinned by the flavor: the configurable
parameters, on the function surface and the execution surface, each with a lock
value.

A flavor is a bundle of per-parameter config-level locks, so no new precedence
rule is needed when a Wafer names a flavor: each parameter follows its one lock
value exactly as the lock model (ADR-0051) defines. There is no layering or
combination on a single parameter, and the flavor does not change how a
parameter's lock is applied.

Its capability is the base's schema, with the pinned parameters shown at their
config values and marked with their lock state, and every unpinned parameter
exposed to the Wafer (config-default exposed but overrideable, wafer-set exposed
and free, config-locked fixed and not overrideable). It carries its lock state
so an agent can see, for each field, what it may set and what the config has
fixed, without a second lookup into the config file.

The representation in `capabilities` mirrors how config writes it, a deliberate
extension of what is otherwise a pure-schema view: a config-locked field shows
its value plus `locked: true` next to it in the schema, a config-default field
shows its value with no locked marker, and a wafer-set field is a plain settable
property. Base mechanism files stay pure schema; the flavor's per-type file is
where the pins show.

A flavor has its own name (the base mechanism's name with the configured flavor
name appended) and is a distinct capability from the base, with its own
availability: a base mechanism can be disabled while one of its flavors stays
enabled, and each flavor is independently disableable (ADR-0055).

Concrete shell flavors that motivate the framework:

- **Scripts-only shell**: a shell flavor whose function surface is config-locked
  to call a named script from an operator-gated folder instead of an arbitrary
  inline command. The config names the folder of allowed scripts; validation
  rejects an inline command and any script name that does not resolve inside the
  folder. It closes the data-to-code injection boundary for shell: the Wafer
  names a script, the script consumes data as data, so runtime data can never
  become part of the code that runs. The script is delivered to the node like a
  secret, not read from a shared folder inside the sandbox: the node receives
  exactly the one script it is told to run, delivered per-node the way a
  declared secret is delivered, and the script lives in a runner-managed
  location the node cannot see. A script stays plain shell with no Servitor
  config embedded, and receives its `{event, steps}` input on stdin, the normal
  node contract.
- **Sandboxed shell**: a shell flavor that pins the execution surface
  (containment, egress, resources, identity) rather than the function surface,
  keeping shell's full power but running it contained (ADR-0052, ADR-0053).

The honest caveat: the sandbox changes the execution harness, an execution
parameter category the declared-connectors pattern never carried, so the flavor
framework is a real new concept, not a mechanical extension. Per BSSN
(ADR-0002), do not build the generalized framework until it has real users;
there are already two candidate flavors (scripts-only, sandboxed), which justify
building the framework.

### Consequences

- Good: constrained variants without a compiled-in type per combination, and
  agent-discoverable in `capabilities` like any other capability.
- Good: the lock model makes a flavor a real constraint, and the flavor's lock
  state is visible inline so an agent sees what it may set.
- Good: it composes with disable, a base can be disabled while its flavor stays
  enabled.
- Bad: the flavor framework is a real new concept with its own config section
  and capabilities shape, not a mechanical extension of declared connectors.
- Neutral: existing mechanisms are unaffected, a flavor is an additive capability.

### Confirmation

Tests pin that a flavor surfaces as a capability in `capabilities`, that a
config-locked pinned parameter is rejected when a Wafer tries to override it,
that inherited identity (Role, MechanismGroup, RunKind) matches the base, that a
flavor has its own name and distinct availability, and that a base can be
disabled while its flavor stays enabled. `go test ./...` stays green.

## Interface notes

Adds a `flavors:` section to the declared config (`servitor.config.yaml`), each
entry naming a base mechanism, a flavor name, and pinned parameters with lock
values. Adds new node type names to the Wafer schema: a flavor's name (base name
plus configured flavor name) is authorable as a node `type:`. The `capabilities`
output gains a flavor entry per declared flavor, with the base's schema and the
pinned fields shown at their config values with lock state. A Wafer node using a
config-locked pinned parameter is rejected at validation.

## More information

- ADR-0018 (the declared-connectors pattern a flavor generalizes)
- ADR-0051 (the lock model, which makes a flavor a real constraint)
- ADR-0052 (the execution surface, whose execution parameter categories a flavor
  can pin)
- ADR-0055 (disable, per-capability, so a base and its flavors are independent)
- SPEC: Mechanism flavors
