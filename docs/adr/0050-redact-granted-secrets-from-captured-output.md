---
status: accepted
date: 2026-09-03
decision-makers: [maintainer]
consulted: []
informed: []
scope:
  - runner
  - secrets
interface-impact: none
---

# ADR-0050: Redact granted secret values from a node's captured output

## Context and problem statement

A node runs as a subprocess with only its declared secrets in its filtered
environment (ADR-0008). The subprocess can echo a value it was granted back on
stdout or stderr, and the runner captures that output and persists it as the
node's result (SPEC: Execution model). Without scrubbing, a node's captured
output could carry a secret it was legitimately granted back into the runner's
persisted state and logs. This decision records the defense: redact a granted
secret value from captured output before it is returned or persisted.

The decision originally lived inside ADR-0029 (which also decided the varlock
boot mechanism, now removed). It is re-homed here as its own decision so that
ADR-0029 can be superseded without the surviving redaction decision being
discarded. This ADR is the surviving decision's new home; it records the
decision and its rationale, not a change of behavior.

## Decision drivers

- The subprocess is the isolation boundary (ADR-0008); the runner persists
  what a subprocess emits, so the boundary is only as good as what is captured.
- A node should not be able to leak a secret it was granted by simply printing
  it; the accidental echo should not become persisted state or logs.
- Redaction must not create a new mechanism: it scrubs exactly the values the
  node was granted, from the same filtered env that delivered them.
- Best Simple System for Now (ADR-0002): a verbatim scan of the granted values
  over the captured output, not a policy engine.

## Considered options

- **Redact granted secret values from captured output (chosen).** The exec
  package scans a node's captured stdout and stderr for each value present in
  the node's filtered env and replaces a match with a placeholder such as
  `<redacted:<name>>`, before the output is returned or persisted.
- **Leave captured output unredacted.** Rejected: it lets a node's output
  carry a granted secret back into persisted state and logs, defeating the
  isolation boundary.
- **Redact from a global secret map.** Rejected: under per-node delivery
  (ADR-0033) there is no global secret map; redaction operates on the running
  node's filtered env, which is exactly the set of values the node was granted.

## Decision outcome

Chosen option: **redact granted secret values from captured output.**

The runner scrubs a granted secret value from a node's captured output by
scanning the node's filtered env and replacing any matching substring with a
placeholder, before the output is returned or persisted. Redaction operates on
the running node's filtered env, so it only ever scrubs values the node was
granted (ADR-0033), and it never needs a global secret map. Under per-node
delivery the filtered env is the node's granted set, which is exactly the
window redaction needs.

Two honest limits are part of this decision, not caveats to hide:

- **Redaction is verbatim-only, not a defense against transformation or
  deliberate exfiltration.** It scrubs a value only when it appears literally
  in the output. A node that transforms a secret before emitting it (base64,
  hex, hashing, substring, concatenation) produces a derived form redaction
  does not recognize, so it is not scrubbed. This defends against the
  *accidental* echo of an exact granted value, not against a node deliberately
  transforming or exfiltrating a secret. Closing the deliberate case is the job
  of the subprocess-isolation boundary (ADR-0008) and the credential-proxy idea
  (IDEAS.md), not of output redaction.
- **Redaction does not erase values from memory.** Scrubbing changes what is
  captured and persisted; it does not zero the bytes a node held in its own
  memory (see ADR-0033's honest limits on "gone after use").

### Consequences

- Good: a node's captured output cannot accidentally carry a granted secret
  back into persisted state or logs.
- Good: it composes with per-node delivery (ADR-0033): the filtered env is the
  granted set, so redaction scrubs exactly the values that were handed out.
- Good: no new mechanism or policy layer; it is a verbatim scan over the
  granted values.
- Bad: it does not stop a node that deliberately transforms or exfiltrates a
  secret; that is honestly scoped out above.

### Confirmation

`go test ./...` passes. Tests assert that a declared secret is redacted from a
node's captured stdout and stderr, and that PATH is not treated as a secret
(the filtered env scan covers only granted values, not the whole environment).

## Interface notes

No change to the Wafer schema, CLI surface, or daemon control protocol. The
exec package's behavior is internal.

## More information

- SPEC: Secret resolution (redaction invariant), Node execution
- ADR-0008 (subprocess-per-step isolation, the boundary this relies on)
- ADR-0033 (per-node delivery, which defines the filtered env redaction scans)
- ADR-0029 (superseded; this ADR re-homes its surviving redaction decision)
- IDEAS.md (the credential-proxy idea, the deliberate-exfiltration limit)
