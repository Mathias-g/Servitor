# THREATS.md

Open, unsettled attack surfaces and things to investigate. This is the home for
security concerns that are not yet decisions and not yet invariants. It is not
a security-practices guide (that is not this file) and it is not a backlog of
planned features (that is IDEAS.md).

## What lives here

- Attack surfaces we know exist but have not decided how to address.
- Questions that need investigation before they can become a decision, an
  invariant, or a spec change.

## What happens when one is resolved

Where a resolved item goes depends on what kind of thing the resolution turned
out to be, not on the fact that it was a threat:

- **Behavior that can be asserted?** A test that would fail if the behavior
  regressed. Tests are enforced; prose is not, so this wins whenever possible.
- **A change to the product's behavior or interface** (Wafer schema, CLI,
  daemon protocol)? The relevant section of `SPEC.md`, not Gotchas.
- **A cross-cutting intent or non-obvious constraint** that no single section
  covers and no test can capture? A line in the Gotchas section of `SPEC.md`.
- **A genuine decision with real alternatives someone might later reverse?** An
  ADR in `docs/adr/`, with the test that pins the behavior as part of recording
  it.
- **Investigated and found to be a non-issue?** Discard it here.

Most resolutions are the first two kinds. An ADR is the exception, not the
default: only a real contested choice needs one.

Until then it stays here, as an open item. Keeping it here does not commit the
project to fixing it; it records that the surface exists and is unresolved.

---

## Open items

### Data-to-code injection at interpreter boundaries

The channel between nodes is JSON only, so no raw executable content crosses a
node boundary on its own. But injection risk is not about the wire format; it is
about each place where a step's static config meets runtime data and treats part
of that data as code. These are the boundaries to examine:

- **`transform` (JSONata).** The expression is static (authored in the Wafer,
  validated at dry-run) and runs over runtime JSON. If the evaluator supports
  `$eval` or dynamic function calls, threaded data can select or construct the
  code that runs. Investigate whether gnata's surface allows this and whether it
  can be restricted so data cannot become a callable.
- **`shell`.** The command is a static template with data interpolated into it.
  Defense is typed, escaped interpolation (argument-array form, never string
  concatenation). Dry-run is the place to statically flag a command that
  interpolates an unescaped value.
- **`mcp-call` / `singer-target`.** Data becomes tool or API arguments. Defense
  is schema-typed inputs (MCP tool schemas are already surfaced by discovery)
  with validation that input values conform before they are passed.

The common shape: data is data unless a boundary explicitly treats it as code,
and validation enforces which boundaries may do that. Most of this is static and
checkable at dry-run, which is where the defense belongs.

### The declared-config "load once at boot" pattern and change detection

Both the secret resolver and (planned) the MCP connector lookup load the
declared config (`servitor.config.yaml`) into an in-memory registry once at
daemon boot, then resolve by name at runtime without re-reading the file
(ADR-0027's store-only invariant). A one-time boot load means a change to the
config file (a new server, a changed URL, a rotated secret source, a renamed
tap) is invisible to a running daemon until it restarts. Investigate how the
resolver and connector lookup should learn about config changes: reload the
file on a change (file mtime / inotify), re-resolve at a bounded cadence, or
treat config as immutable-per-daemon-process and require a restart by design.
Whatever the choice, it applies to both the secret resolver and the MCP
connector registry, so it should be settled as one pattern, not two. This is
unresolved; it is not yet a decision or an invariant.

Naming: where more than one resolver/registry exists, each must be named for
what it resolves or holds, not left generic. The existing one is the secret
resolver; a connector registry that resolves MCP URLs (and Singer tap/target
commands) by name should be named for connectors (for example a connector
resolver or connector registry), not generically "the resolver". The generic
name is exactly the ambiguity this project has been removing ("what is an
integration", "which config"). Apply the explicit name when the connector
lookup is built.

### Redaction bypassed by derived forms of a secret

Redaction scrubs a secret value from captured subprocess output only when it
appears verbatim (a literal substring of stdout/stderr, `exec.go`'s
`strings.ReplaceAll`). A node that transforms a secret before emitting it, for
example base64, hex, hashing, substring, or concatenation, produces a derived
form that redaction does not recognize, so it is not scrubbed from the result
or logs. This is pre-existing, not introduced by any new secrets model.

The boundary to be honest about: redaction defends against the *accidental*
echo of an exact granted value, not against a node deliberately transforming or
exfiltrating a secret. A node with the value plus network access does not need
stdout to exfiltrate; it can POST the value out directly, and a derived form is
byte-for-byte indistinguishable from a node legitimately computing data from a
secret. Closing the deliberate case is the job of the subprocess-isolation
boundary and the credential-proxy idea, not of output redaction. Open question:
whether the SPEC should state this limit explicitly (redaction is verbatim-only,
not a defense against transformation or deliberate exfiltration) so the
guarantee is not overstated.
