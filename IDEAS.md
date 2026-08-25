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

## Native app for browsing runs and the like

A dedicated native app, called "Servitor Desktop", that a human can connect to a
Servitor runner "somehow" to browse runs and things like that: run history,
step outcomes, events, and the state of registered workflows. The connection
mechanism is deliberately left undefined for now (it could be a loopback or
remote adapter over the daemon control protocol, or some future transport).

Why this is attractive:

- Run inspection today is CLI-only (`servitor runs`, `servitor run <id>`). A
  GUI would make Servitor more approachable for operators who prefer a visual
  view of what the runner did.
- The daemon already exposes a control protocol, so a client is a natural
  consumer of an existing surface rather than a whole new backend.

Open questions to settle if this moves toward a decision:

- How the app connects to the runner. The control plane is deliberately
  loopback-only and operator-gated (ADR-0009), so the connection path is a real
  design decision, not a given.
- Whether the app is read-only (browse runs) or can also operate the runner
  (submit, enable, disable, trigger, cancel).
- The earlier "no MCP in v1" decision (ADR-0005) deferred a remote interface for
  agents; a native app is a different consumer and would need its own decision.

### Wafers as a diagram

A first-class feature of Servitor Desktop should be rendering Wafers as a visual
diagram, the way people are used to seeing workflows on Zapier or similar, built
on an existing open-source graph/diagram library rather than from scratch. A Wafer
is already a dependency DAG (ADR-0023), so it maps naturally onto a node graph:
triggers (the `on:` entries) at the top, steps as nodes, edges for `depends_on`,
and the branch/loop structure for `switch` and `foreach`.

Two tiers, in order of priority:

1. **Inspect (primary).** Read a Wafer (from the connected runner's registered
   workflows, or a local file) and render it as an editable-layout diagram:
   nodes per step, edges for dependencies, and clear visuals for switch branches
   and foreach fan-out. This is read-only and the natural first cut, consistent
   with Servitor being agent-first (the artifact, not a builder, is the source
   of truth).
2. **Create (secondary).** Let a user assemble a Wafer in the diagram and save
   it back as YAML. Secondary because Servitor is agent-first: agents author
   Wafers via the CLI/skill, so the visual builder is a convenience for humans,
   not the primary authoring path. If built, it must round-trip losslessly to
   the Wafer YAML (the artifact stays authoritative; the diagram generates and
   edits that YAML, never a divergent database row).

The diagram-first direction leans on the same "the Wafer is the artifact" rule
as the rest of Servitor (SPEC: The Wafer): the app renders and edits the YAML,
it never becomes a second source of truth.

### Library: React Flow

Leaning toward **React Flow** (`@xyflow/react`, MIT) for the diagram, the same
library n8n uses, over the other candidates researched (Cytoscape.js, AntV G6,
vis-network, JointJS). Cytoscape.js + cytoscape-dagre was considered first as
the lighter, zero-dependency, vanilla-JS fit for a Wails webview, but its demo
is too bare bones for the richer node-editor experience wanted, and editing is a
real (if secondary) goal. React Flow is the strongest node-based editor and
handles both inspect and create well.

The cost: React Flow needs **React + a bundler** (for example Vite), so the
frontend stops being a vanilla embedded page like Playwrap's launcher and
becomes a small React app bundled into the Wails window. It has **no built-in
auto-layout**; pair it with **dagre** (MIT) or elkjs for the automatic DAG
layout (which is what read-only inspection relies on).

Revisit the choice when actually building: if it turns out read-only inspection
is the whole need and editing is never wanted, Cytoscape.js's simplicity becomes
attractive again. For now, with editing in mind, React Flow is the leaning.

Not buildable until the connection mechanism (or a local-file path for
inspection) is defined. Until then this is just a promising direction, kept here
so it is not lost.

## (Add more ideas here as they come up; delete them when they become ADRs or
## are discarded.)
