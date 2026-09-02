---
status: accepted
date: 2026-08-29
decision-makers: [Mathias]
consulted: []
informed: []
scope:
  - control-plane
  - capabilities
  - secrets
interface-impact: new
---

# ADR-0035: Declare secrets in the integrations config, discoverable by agents

## Context and problem statement

The new secrets model resolves secrets per node through a provider (ADR-0032,
ADR-0033), but nothing yet tells an agent what secrets exist to name in a Wafer,
or the operator which secret sources are configured. Today `servitor
capabilities` renders a `secrets.yaml` reporting the declared secret names and
whether each is present, sourced from the varlock schema (SPEC: How an agent
disovers integrations). Under the new model the secret sources a deployment uses
(which mechanisms and stores, and which secret names exist) are declared by the
operator, following the declared-config pattern (ADR-0018), so they belong
in the same config and the same management-CLI pattern.

## Decision drivers

- The secret set a deployment uses is the operator's, declared through CI/CD,
  not authored in Wafers (declared-config pattern, ADR-0018).
- An agent authoring a Wafer must be able to discover which secret names are
  available, which account each belongs to, what each is for, and when it
  expires, so it reaches for the right one (for example `GMAIL_SEND_TOKEN` for
  `billing@acme.com` vs for `support@acme.com`).
- Values never appear in the declared config or in what the agent reads; only
  names and metadata do.
- The Wafer keeps naming secrets by secret name; the operator/CI owns which
  sources, account names, permissions, and expiries exist.
- A secret referenced by a Wafer but not declared must be a hard error, because
  the run could never complete.

## Considered options

- **A `secrets:` section in the declared integrations config, with a
  `servitor secret` CLI and capabilities rendering (chosen).** Declared secrets
  live beside the mcp/tap entries, managed by `servitor secret add/list/remove`
  alongside `servitor mcp`/`tap`/`target`. `capabilities` renders them into
  `secrets.yaml` for agents.
- **Keep secrets in the varlock schema.** Reject: varlock is no longer the
  mechanism (ADR-0034), and the schema-driven declaration should move to the
  declared config it belongs to.
- **Author secrets in Wafers.** Reject: the operator, not the Wafer author,
  owns which secret sources exist (ADR-0018 pattern); authoring them in Wafers
  scatters the declaration.

## Decision outcome

Chosen option: **a `secrets:` section in the declared integrations config, a
`servitor secret` CLI, and capabilities rendering.**

The declared integrations config gains a `secrets:` section. Each entry carries
a **secret name** and a **source**, and optionally an **account name** (for
example the gmail address or GitHub org), **permissions** the operator declared
it is authorized for, and an **expiry**. Only the secret name and source are
required; the rest are optional. `servitor secret add/list/remove` manages this
section. `servitor capabilities` renders these into `secrets.yaml` (secret name
+ account name + permissions + expiry, never values), so an agent authoring a
Wafer can discover the available secret names and pick the right one.

In v1 the account name, permissions, and expiry are **informational only**: they
exist so the agent reaches for the right secret. Servitor does not verify that an
action node's operation matches a secret's declared permissions or that a secret
is within its declared expiry (that enforcement is a separate idea). A secret
declared but used by no Wafer is a validation warning; a secret referenced by a
Wafer but not declared is a hard error that refuses to submit or run the Wafer.
A declared secret whose value is not present in the store at execution time
fails fast at the node that needs it (SPEC: Secret invalidity and rotation).

### Consequences

- Good: the secret set is discoverable by agents (the agent-first goal), with
  enough metadata to pick the right secret.
- Good: the declaration follows the established declared-config pattern
  and management-CLI shape; it does not invent a new one.
- Good: values never reach the agent or the config; only names and metadata do.
- Bad: adds a config section, a CLI surface, and a validation rule (declared-but
  -unused warns, used-but-undeclared errors).
- Neutral: a `secrets:` entry is a different shape from an mcp/tap entry, but it
  is the same file and the same management-CLI pattern.

### Confirmation

`go test ./...` passes. Tests assert that `servitor secret add/list/remove`
manages the section, that `capabilities` renders `secrets.yaml` with name +
account + permissions + expiry and never a value, that a declared-but-unused
secret warns, and that a used-but-undeclared secret refuses to submit or run.

## Interface notes

New public surface. A `secrets:` section is added to the declared integrations
config; a `servitor secret add/list/remove` CLI group is added; and the
`capabilities` `secrets.yaml` output is enriched from a name+presence report to
name + account + permissions + expiry (never values). The Wafer schema is
unchanged: nodes still declare `secrets:` by secret name. Consumers reading the
old `secrets.yaml` shape must adapt to the richer one.

## More information

- SPEC: Secret resolution, How an agent discovers integrations
- ADR-0018 (declared-config file, the pattern this extends)
- ADR-0032 (provider interface), ADR-0034 (varlock's demotion)
- ADR-0036 (the secret-resolution mechanism group and secret role)
- IDEAS.md (the exploration this decision grew from)
