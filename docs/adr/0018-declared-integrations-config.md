---
status: accepted
date: 2026-08-25
decision-makers: [Mathias]
consulted: []
informed: []
scope:
  - runner
  - capabilities
  - singer-integration
  - mcp-integration
interface-impact: new
---

# ADR-0018: Declared integrations config replaces PATH-prefix discovery

## Context and problem statement

Servitor runs subprocess integrations (MCP servers, Singer taps and targets)
that are installed on the box as separate executables. To tell an authoring
agent what is available, `servitor capabilities` must enumerate them. One
approach, which the current build used, is to scan PATH for name prefixes
(`mcp-*`, `tap-*`, `target-*`). That is fragile: it relies on an unenforced
naming convention, so a server shipped as `atomic-server` (with no `mcp-`
alias) is never found, a service whose name happens to contain `mcp` is
misidentified, and a rename silently changes discovery. It is also
self-referential: the runner labels a folder `mcp/` and scans for `mcp-*`
names, both of which it invented, so nothing external verifies the guess. The
industry-standard alternative, used by opencode (`opencode.json`), Claude
Desktop, VS Code, and JetBrains, is to declare each local server in a config
with its exact command, rather than infer it from a filename.

## Decision drivers

- Discovery must not depend on an unenforced naming convention that a rename or
  an arbitrary vendor name can break.
- The box should declare what it has rather than the runner guessing from
  filenames; the config is the non-circular source of truth.
- Execution does not need discovery at all: a `mcp-call` or `singer-tap` step
  already names the exact server/tap in the Wafer, so the runner just spawns it.
  Discovery exists only to tell an author what is available.
- Installation must be easy for the operator, so a management CLI is wanted.
- Best Simple System for Now: no discovery of servers the operator did not
  choose to expose.

## Considered options

- **Declared integrations config (chosen).** One config file, with a section
  per mechanism, lists each integration and its exact command and env. A
  management CLI (for example `servitor mcp add`) writes entries; the actual
  software install is delegated to the ecosystem's package managers (npx, pipx,
  uv, Meltano). `capabilities` reports only what is declared.
- **PATH-prefix scan.** Enumerate `mcp-*`/`tap-*`/`target-*` on PATH. Rejected:
  fragile naming convention, self-referential, breaks on rename or arbitrary
  vendor names.
- **Declared + still surface PATH taps.** Keep scanning as a convenience beside
  declared entries. Rejected: two sources of truth and the fragility returns.
- **Wafer-declared only.** Discover only servers referenced in submitted Wafers.
  Rejected: no way to discover a server's tools before authoring a Wafer that
  names it (chicken-and-egg for first authoring).

## Decision outcome

Chosen option: **a single declared integrations config, declared-only.**

One config file lists every subprocess integration, with a section per
mechanism so the mechanism is explicit and nothing is ambiguous:

```yaml
mcp:
  atomic:
    command: [atomic-server]
    env: [ATOMIC_TOKEN]
singer:
  taps:
    stripe:
      command: [tap-stripe]
      env: [STRIPE_KEY]
  targets:
    grist:
      command: [target-grist]
```

- The config is the sole source of truth for what is available; there is no PATH
  scan. `capabilities` reports only declared integrations, probing each once at
  refresh for its schemas (Singer via `--about`/`--discover`; MCP via
  `tools/list` with the detected protocol mode).
- A management CLI (`servitor mcp add`, `servitor tap add`, and their
  list/remove counterparts) makes installing easy: it writes the config entry
  and guides the operator. The actual software install is delegated to the
  ecosystem's package managers.
- Only local stdio integrations are covered for now; remote MCP servers (URLs)
  are a future addition and not part of this decision.

This decision establishes how integrations are discovered and enumerated,
which ADR-0017 leaves to this ADR. It is independent of ADR-0017's mechanism
grouping, which is unchanged: `capabilities` still groups its output by
mechanism.

### Consequences

- Good: no naming convention to break; renames and arbitrary vendor names are
  fine because the config names the exact command.
- Good: the config is the non-circular, declared source of truth; the box
  advertises what it has.
- Good: a management CLI makes install easy without hand-editing.
- Bad: adds a config surface and an install workflow that the prefix scan
  avoided; the operator must declare an integration before an agent can
  discover it.
- Neutral: execution is unaffected (the Wafer already names the exact
  server/tap); only discovery changes.

### Confirmation

`go test ./...` passes. `capabilities` reports only declared integrations and
groups output by mechanism (`core/`, `webhook/`, `singer/`, `mcp/`, `helper/`),
each pinned by tests. A test asserts a declared integration appears in the
report and a non-declared executable does not.

## Interface notes

Additive and breaking. The `servitor capabilities` output changes: the
discovered reports (`singer/taps.yaml`, `mcp/servers.yaml`) are now sourced
from the declared config rather than a PATH scan. A new integrations config file
and a new management CLI surface (`servitor mcp` / `servitor tap` subcommands)
are added. The `mcp-call` step type (ADR-0015) and the Singer contract
(ADR-0016) are unchanged.

## More information

- ADR-0017 (mechanism as organizing principle for capabilities)
- ADR-0015 (mcp-call step type)
- ADR-0016 (Singer invocation contract)
- SPEC: How an agent discovers integrations, What counts as an integration
- IDEAS.md (the exploration this decision grew from)
