# Ideas

Catch-all for promising directions that are not yet decided or built. These are
possibilities, not commitments: nothing here is a plan or a decision, and most
of it will be discarded. When an idea becomes a real decision it gets an ADR and
moves into the SPEC/PLAN; until then it lives here so it is not lost.

## Dogfooding: let Servitor publish its own capabilities

How a remote agent gets capabilities is currently described in the SPEC as "the
pipeline runs `servitor capabilities` and commits the generated directory to
git." But Servitor is itself a workflow automation system, so the natural
refinement is to make that publication a **Wafer**, not a bespoke CI step:

- A Wafer (say `publish-capabilities`) triggered on deploy, on demand, or on a
  slow cron, runs `servitor capabilities <dir>` and then commits and pushes the
  result to the repo the agent already reads.
- Remote agents read the committed capabilities from the repo exactly as they
  do today; the only change is that the runner does the publishing.

Why this is attractive:

- It eats Servitor's own dogfood: the first real end-to-end workflow is the one
  that makes Servitor usable by remote agents.
- It is the canonical demonstration for Phase 9 ("validate the agent workflow"),
  since an agent goes from `capabilities` to an applied Wafer.
- It is compatible with the current SPEC wording (the "pipeline" is just
  realized as a Wafer), not a contradiction.

Not buildable until:

- Step execution exists (Phase 6) so a Wafer can actually run.
- A `commit-and-push-to-git` capability exists, and varlock (Phase 8) supplies
  the git credentials.
- Triggers that fire it on a schedule (cron) or on deploy exist (Phase 7).

Open question for later: decide between a Wafer-driven publish and a plain CI
step. The Wafer version is the more interesting default; a plain CI step is the
simpler fallback.

## Adopt honker's `ExtensionPath()` locator when a newer binding is published

Honker 0.5.0 (PR #100, "extension reach for every binding") added a Go locator,
`honker.ExtensionPath()`, that resolves the extension from `HONKER_EXTENSION_PATH`
(falling back to next to the binary and the working directory). We renamed our
env var to match (`HONKER_EXTENSION_PATH`), but the newest `honker-go` on the Go
proxy is still the pre-0.5.0 version we pin, which has no `ExtensionPath()`.

Next time we check in on honker-go versions, if a newer binding is published
that has `ExtensionPath()`, bump to it and swap our hand-rolled path resolution
in `internal/honker` for the locator. The env var already matches, so the change
should be small and local to that package. This also pulls in the 0.5.0 WAL-open
retry fix (#102).

How to check later: `go list -m -u github.com/russellromney/honker-go`, or look
for a tagged honker-go release newer than `v0.0.0-20260502020136-bdbe80df13ef`
that ships `extension.go`.

## OpenAPI-backed integration steps (parked, not a step type in v1)

Discussed alongside the `mcp-call` decision (ADR-0015) and deliberately not
built. The idea: an OpenAPI document already carries an operation id, a request
schema, and a response shape, so a step type could drive integrations from a
published spec instead of a hand-written helper.

Why it was rejected for v1: an OpenAPI document is not an executable. Singer and
MCP multiply integrations because each has a prebuilt subprocess to run (a tap,
a server); OpenAPI has none, so it does not multiply integrations, it multiplies
the "call-and-map glue" the operator must write per operation. It also breaks
the subprocess isolation model (ADR-0008), since there is no subprocess to run
the call in. It largely overlaps the curated helpers' niche with worse
ergonomics.

What remains attractive, cheap, and worth doing independently: OpenAPI 3.1 added
a top-level `webhooks` object describing the shape of a payload a service sends
you, in the same format as a regular operation. That does not replace Standard
Webhooks (which verifies the envelope) but is a standard place to describe what
is inside one, for services that publish it. This could enrich `capabilities`
without a new executor.

## Adopt the official Standard Webhooks Go library for signature verification

Servitor currently hand-rolls Standard Webhooks signature verification in
`internal/trigger` (reads `webhook-id`/`webhook-timestamp`/`webhook-signature`,
checks the timestamp within tolerance, HMAC-SHA256s `id.timestamp.body`, and
compares against the `v1,<sig>` list). The Standard Webhooks project publishes an
official Go library (`standard-webhooks/libraries/go`) that does exactly this,
maintained by the technical steering committee and guaranteed spec-compliant.

Why to consider it: we never drift from the spec, drop the hand-rolled
verification, and inherit any upstream fixes. It fits the SPEC's "delegate hard
problems to maintained tools."

Why to hold off (the current leaning): the verification is a small, correct,
tested function. Adding a dependency for roughly forty lines is the kind of
thing BSSN (ADR-0002) would question, and the SPEC's "delegate" principle is
aimed at big problems (identity, secrets, webhook signing as a class) rather
than this tiny instance. We already handle it correctly.

The other Standard Webhooks tools (Verify Webhook, Simulate Request, the
receiving-webhooks AI skill) are interactive debugging aids or agent-guidance,
not libraries to integrate; the Go library is the only real candidate.

## (Add more ideas here as they come up; delete them when they become ADRs or
## are discarded.)
