---
status: accepted
date: 2026-08-29
decision-makers: [Mathias]
consulted: []
informed: []
scope:
  - capabilities
  - registry
  - secrets
interface-impact: new
---

# ADR-0036: A `secret` role and a `secret-resolution` mechanism group

## Context and problem statement

The capability model groups capabilities into mechanism groups (ADR-0031) and
gives each a role that says where it may be used and how it is treated. Today the
roles are `trigger`, `action`, and `flow`. The new secrets model (ADR-0032,
ADR-0033, ADR-0035) introduces a capability that supplies secret material to
nodes rather than being a node or a trigger itself. That is a different kind of
thing in the capability model, and it needs a role and mechanism group of its
own, for a reason this ADR makes explicit.

The other mechanism groups (`core`, `webhook`, `singer`, `mcp`, `helper`) each
contain **node capabilities**: types an agent authors a Wafer against, each with
a JSON Schema. A secret provider is not that. An agent does not write a Wafer
node for it; instead, in `secrets.yaml`, an agent picks the **`source`** of a
secret (ADR-0035), and that `source` names which provider the secret resolves
through. So the `secret-resolution` group exists to enumerate the available
secret sources (providers), which is what an agent consults to know what values
`source` can validly take. It is a discovery surface for the `source` field, not
a directory of Wafer node types. This is why it has its own role, `secret`, and
is not folded into `core`.

## Decision drivers

- A secret capability is a distinct role: it supplies secret material to nodes,
  it is not itself a node or trigger.
- An agent authoring a `secrets.yaml` entry needs to know what values `source`
  can take; the group is the enumeration of available providers (mechanisms),
  so the agent is not guessing at a string.
- The distinct ways Servitor obtains a secret value at runtime are mechanisms
  under a `secret-resolution` mechanism group, matching the mechanism-group
  model (ADR-0031).
- The group name carries "secret" so it cannot be mistaken for a general-purpose
  resolver of non-secret things.

## Considered options

- **A `secret` role and a `secret-resolution` mechanism group (chosen).** The
  registry gains a `secret` role and a `secret-resolution` mechanism group; a
  secret-resolution mechanism is one concrete provider (a valid `source` value),
  and the group enumerates them for agents.
- **No separate role or group; secrets stay outside the capability model.**
  Reject: secrets are part of the agent-facing surface (ADR-0035), and an agent
  needs the group to discover the valid `source` values; without it the agent
  guesses at provider names.
- **A `secret-resolution` group with no distinct `secret` role.** Reject: the
  role says what a capability is and where it is used; a secret provider is
  neither a node nor a trigger, so it needs its own role.

## Decision outcome

Chosen option: **a `secret` role and a `secret-resolution` mechanism group.**

The capability model gains a `secret` role (a capability that supplies secret
material to nodes, not a node or trigger itself) and a `secret-resolution`
mechanism group. A secret-resolution mechanism is one concrete way Servitor
obtains a secret value: it is a provider, and it is a valid `source` value in
`secrets.yaml`. The group enumerates the available providers, which is what an
agent consults to know what to put in `secrets.yaml`'s `source` field. It is
distinct from the node-capability groups: those are directories of Wafer node
types, this is the set of available secret sources. The exact names `secret` and
`secret-resolution` are settled by this decision; they were left open in the
exploration and are now fixed.

### Consequences

- Good: secrets are first-class, discoverable capabilities, not an afterthought;
  an agent knows the valid `source` values instead of guessing.
- Good: the mechanism-group model (ADR-0031) is extended with a group whose
  purpose is explicitly different from the node-capability groups.
- Good: the name carries "secret" so it is not mistaken for a general resolver.
- Bad: adds a role and a group to the registry and capabilities output.
- Neutral: no mechanism in the group exists until a real secret provider is
  built; the group is added now for the model, not for a built mechanism. This
  is an explicit exception to ADR-0031's "a group is added only when a
  capability in it actually exists", justified because the group exists to
  enumerate the `source` values an agent needs, not to hold a node capability.

### Confirmation

`go test ./...` passes. A registry lint rule accepts a capability with the
`secret` role and `secret-resolution` mechanism group, and `capabilities` renders
the group and role in its output, showing the available secret sources for
`secrets.yaml`.

## Interface notes

New public surface. The capabilities output gains a `secret-resolution` group and
the `secret` role, enumerating the available secret sources (providers) that an
agent uses as `source` values in `secrets.yaml`. No change to the Wafer schema: a
node still declares `secrets:` by secret name.

## More information

- SPEC: Secret resolution, How an agent discovers integrations
- ADR-0031 (mechanism group as a family of mechanisms; this is the explicit
  exception to its "only when a capability exists" rule)
- ADR-0032 (provider interface), ADR-0035 (declared secrets config)
- IDEAS.md (the exploration this decision grew from)
