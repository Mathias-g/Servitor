# Ideas

Catch-all for promising directions that are not yet decided or built. These are possibilities, not commitments: nothing here is a plan or a decision, and most of it will be discarded. When an idea becomes a real decision it gets an ADR (the "why") and its behavior is written into the SPEC (the "what"), and the work moves into PLAN; until then it lives here so it is not lost.

## Dogfooding: let Servitor publish its own capabilities

How a remote agent gets capabilities is currently described in the SPEC as "the pipeline runs `servitor capabilities` and commits the generated directory to git." But Servitor is itself a workflow automation system, so the natural refinement is to make that publication a **Wafer**, not a bespoke CI step:

- A Wafer (say `publish-capabilities`) triggered on deploy, on demand, or on a slow cron, runs `servitor capabilities <dir>` and then commits and pushes the result to the repo the agent already reads.
- Remote agents read the committed capabilities from the repo exactly as they do today; the only change is that the runner does the publishing.

Why this is attractive:

- It eats Servitor's own dogfood: the first real end-to-end workflow is the one that makes Servitor usable by remote agents.
- It is the canonical demonstration for Phase 9 ("validate the agent workflow"), since an agent goes from `capabilities` to an applied Wafer.
- It is compatible with the current SPEC wording (the "pipeline" is just realized as a Wafer), not a contradiction.

Not buildable until:

- Step execution exists (Phase 6) so a Wafer can actually run.
- A `commit-and-push-to-git` capability exists, and varlock (Phase 8) supplies the git credentials.
- Triggers that fire it on a schedule (cron) or on deploy exist (Phase 7).

Open question for later: decide between a Wafer-driven publish and a plain CI step. The Wafer version is the more interesting default; a plain CI step is the simpler fallback.

## Adopt honker's `ExtensionPath()` locator when a newer binding is published

Honker 0.5.0 (PR #100, "extension reach for every binding") added a Go locator, `honker.ExtensionPath()`, that resolves the extension from `HONKER_EXTENSION_PATH` (falling back to next to the binary and the working directory). We renamed our env var to match (`HONKER_EXTENSION_PATH`), but the newest `honker-go` on the Go proxy is still the pre-0.5.0 version we pin, which has no `ExtensionPath()`.

Next time we check in on honker-go versions, if a newer binding is published that has `ExtensionPath()`, bump to it and swap our hand-rolled path resolution in `internal/honker` for the locator. The env var already matches, so the change should be small and local to that package. This also pulls in the 0.5.0 WAL-open retry fix (#102).

How to check later: `go list -m -u github.com/russellromney/honker-go`, or look for a tagged honker-go release newer than `v0.0.0-20260502020136-bdbe80df13ef` that ships `extension.go`.

## OpenAPI-backed integration steps (parked, not a step type in v1)

Discussed alongside the `mcp-call` decision (ADR-0015) and deliberately not built. The idea: an OpenAPI document already carries an operation id, a request schema, and a response shape, so a step type could drive integrations from a published spec instead of a hand-written helper.

Why it was rejected for v1: an OpenAPI document is not an executable. Singer and MCP multiply integrations because each has a prebuilt subprocess to run (a tap, a server); OpenAPI has none, so it does not multiply integrations, it multiplies the "call-and-map glue" the operator must write per operation. It also breaks the subprocess isolation model (ADR-0008), since there is no subprocess to run the call in. It largely overlaps the curated helpers' niche with worse ergonomics.

What remains attractive, cheap, and worth doing independently: OpenAPI 3.1 added a top-level `webhooks` object describing the shape of a payload a service sends you, in the same format as a regular operation. That does not replace Standard Webhooks (which verifies the envelope) but is a standard place to describe what is inside one, for services that publish it. This could enrich `capabilities` without a new executor.

## Adopt the official Standard Webhooks Go library for signature verification

Servitor currently hand-rolls Standard Webhooks signature verification in `internal/trigger` (reads `webhook-id`/`webhook-timestamp`/`webhook-signature`, checks the timestamp within tolerance, HMAC-SHA256s `id.timestamp.body`, and compares against the `v1,<sig>` list). The Standard Webhooks project publishes an official Go library (`standard-webhooks/libraries/go`) that does exactly this, maintained by the technical steering committee and guaranteed spec-compliant.

Why to consider it: we never drift from the spec, drop the hand-rolled verification, and inherit any upstream fixes. It fits the SPEC's "delegate hard problems to maintained tools."

Why to hold off (the current leaning): the verification is a small, correct, tested function. Adding a dependency for roughly forty lines is the kind of thing BSSN (ADR-0002) would question, and the SPEC's "delegate" principle is aimed at big problems (identity, secrets, webhook signing as a class) rather than this tiny instance. We already handle it correctly.

The other Standard Webhooks tools (Verify Webhook, Simulate Request, the receiving-webhooks AI skill) are interactive debugging aids or agent-guidance, not libraries to integrate; the Go library is the only real candidate.

## Safety primitives and mechanisms (emergency / decommission)

A separate idea that follows from the stronger secrets model, for when an operator detects unauthorized access and needs to react. The point is not to ship a baked-in "delete all secrets" button; it is to provide **primitives and mechanisms** the operator composes into whatever safety behavior fits their keystore setup. This is the same provider/mechanism philosophy as the secrets model, and it slots into that model rather than standing apart from it.

The granular, store-agnostic **primitives**:

- Wipe a locally-resolved secret value (the on-box copy; the remote store keeps the authoritative value).
- Forget the local credential Servitor uses to talk to the remote store, cutting the runner off until an operator re-provisions it.
- Stop resolving / halt the runner.
- Ship a log backup off-box and wipe the local logs. Secret values are already stripped from captured output, but logs still hold run inputs, event payloads, and other data that can be a privacy concern or otherwise sensitive, so on suspected access an operator may want to preserve an audit copy somewhere and scrub the local logs so they cannot be exfiltrated. The "ship a backup somewhere" part is itself an outbound transfer and needs a trusted destination, which is a real design point (below).
- Invoke a configured keystore operation (the one store-specific primitive): telling the keystore to revoke or rotate a credential so that even an exfiltrated value no longer authenticates. Whether this exists depends on how the keystore is set up.

A **mechanism** is a composition of primitives a deployment defines, e.g. "Panic" = wipe all local values + ship a log backup off-box and wipe local logs + forget the store credential + stop resolving; "Cut off" = forget the store credential + halt; "Revoke and wipe" = invoke the store's revoke + wipe local. The store-specificity is confined to the one "invoke a store operation" primitive; the composition lives with the operator.

Why it is separate: it is not part of the secret-resolution spine. It depends on the provider/mechanism vocabulary of the stronger secrets model (which has not been built), so it is anchored there, not to the current varlock model. It is independently buildable once the secrets model exists.

Open questions:

- How a safety mechanism is invoked and authenticated. An emergency action needs a strong, human-gated (break-glass) authorization path, not the ordinary app session; a credential that is too easy to trigger, or that a compromised app session could fire, is worse than none. This is the load-bearing question.
- The revocation primitive's bootstrap problem: revoking the store credential needs another way to authenticate to the store (an admin/break-glass credential, or a human in the store's UI), since the credential being revoked is often the only one the runner holds.
- The log-backup primitive's trusted-destination problem: shipping a log backup somewhere is an outbound transfer that must reach a trusted destination and must not itself become an exfiltration vector. It needs its own authenticated, configured target, distinct from the emergency path that triggered it.
- How the primitives surface to an operator (the control-plane GUI is a natural host for a button wired to a mechanism, but the mechanisms are usable from the CLI too).
- The escalation ladder: wiping local values is recoverable (re-resolve from the store); forgetting the store credential needs re-provisioning; revoking at the store is the strongest and least portable, because it depends on the keystore.

## Credential proxy + OS sandbox for nodes (a stronger runtime boundary)

A separate, more ambitious idea split out of the secrets model. The provider + per-node delivery model still hands each node its resolved secret value (narrowly, per node, eliminated after). This idea goes further: keep the real value out of the untrusted node process entirely, even while the node uses it.

Each node subprocess runs in a sandbox whose only egress is a credential proxy. The node holds a placeholder; the proxy swaps in the real value only on a TLS-verified connection to an allowlisted host, and scrubs it from responses. A prompt-injected or compromised node cannot leak what it never held. This is the only approach that directly closes node exfiltration of secrets it legitimately needs to *use*.

Why it is separate: it is not part of the secret-resolution spine (provider + per-node delivery). It is independently buildable, probably should not gate the core model, and has its own deep engineering and open questions. It needs a local HTTPS-interception proxy (Servitor's own, or an adopted broker such as varlock's preview proxy) plus sandboxing each node (the subprocess model of ADR-0008 is the natural base).

Open questions:

- The proxy only covers proxied HTTPS to public-CA hosts. What to do for `shell`, `singer-tap`/`singer-target`, and `mcp-call` (stdio) nodes, which do not speak proxied HTTPS: give them a sandbox without the wire-injection, or accept they hold real values?
- Whether the proxy is Servitor's own implementation or an adopted broker, and how it composes with per-node sandboxing.
- It is HTTP/1.1 + public-CA only, which constrains non-HTTP node types.

A caution against this idea: an egress allowlist does not need a proxy to exist; a sandbox-level egress allowlist provides that on its own. The proxy's only distinctive job is keeping the real value out of the node's memory, so a compromised node cannot copy it. It does **not** stop a node that, given egress to an allowed host, causes a request containing the credential to go to a destination it effectively controls (an allowlisted host that has been redirected or that the attacker fronts). For `shell` that is the same hole an allowlist-only boundary has, so the proxy adds little over a plain sandbox + allowlist. It is worth asking whether the proxy is justified for any node type, or whether per-node delivery plus a sandbox-level egress allowlist is the right ceiling.

## Secret permission enforcement (beyond informational)

A separate idea that follows from the secrets model's v1 decision that a secret's declared permissions are **informational only** (they exist so an agent reaches for the right secret; Servitor does not verify a match). This idea is about whether Servitor could ever *enforce* that an action node's operation matches a secret's declared permissions.

Why it is hard: permission names are not standardized across services. GitHub's fine-grained permission matrix is very different from gmail's or Slack's, so a node's "needs permission X" only makes sense within a service context. Enforcing the match would require a per-service permission vocabulary that Servitor maintains and validates against, which is complex and may be impossible to do well. This is deliberately out of scope for v1.

## A control plane for Servitor (web or native, a separate project)

A dedicated app, "Servitor Control Plane", that an operator uses to view what Servitor is doing and, in narrow safety-only cases, act on it: run history, step outcomes, events, the state of registered workflows, and a "see what is running" view across one or more Servitor deployments. It is a **separate project from Servitor**, not built into it. The natural default is a **web app hosted on its own server**, reached in a browser; a **native app** (a Wails desktop shell) is an optional way to package the same frontend as a standalone download, not a separate product.

The key architectural decision: **the app does not talk to the daemon's control protocol.** Instead it consumes the data Servitor publishes through the dogfooding idea (see its own entry): Servitor publishes its capabilities and run data to a repo, and the app reads that. This keeps Servitor's host locked down (no exposed control-plane endpoint, no network surface on the runner box), and it lets one app serve **multiple Servitor servers**, since each publishes its data to a known place.

The app is a **control plane**: it reads (displays) the runner's data, and it can reach into a runner only for narrow, operator-chosen actions. In its current incarnation that is read-only plus safety-only actions; general operation (submit, cancel, enable, disable) is a larger step that needs its own decision. Any action path is one-way and authenticated, never a general read/write surface.

Why this is attractive:

- Run inspection today is CLI-only (`servitor runs`, `servitor run <id>`). A GUI would make Servitor more approachable for operators who prefer a visual view of what the runner did.
- Because it reads published data rather than the daemon protocol, the runner's host stays closed; the app is an independent consumer, not an in-band client.
- One app can aggregate several Servitor deployments, which a per-daemon client cannot.

Open questions to settle if this moves toward a decision:

- **Web first, native optional.** The app is a hosted web app reached in a browser; a Wails desktop shell is an optional packaging of the same frontend for a single-operator download, not a separate product. The frontend cost (React + bundler) is the same either way (see the React Flow section), and the data source is the same regardless of packaging.
- **How the app gets the data.** The dogfooding idea publishes capabilities (and the monitoring idea wants run/health data published too). The app consumes that published data, so the shape of "what Servitor publishes" (a repo, a signed artifact, a stream) is a real design point, not a given. It must be safe for Servitor to publish (no secrets, redacted, per the existing redaction invariants).
- **Which Servitor instances it connects to**, and how the app discovers/authenticates their published data sources when there are several.
- **Access control.** A multi-user web app wants auth and role-based access control: who can see which Servitor's runs, which nodes, which run payloads. Keycloak (or similar) is a candidate for the identity/authorization layer. This only matters for the web form; a single-operator native app can skip it.
- **Which actions it can take.** General operation (submit, enable, disable, trigger, cancel) needs a path back into the runner that the "consume published data only" model does not provide. Safety-only actions are a narrower, one-way control channel; see the emergency primitives idea.
- **How far to take editing.** The Wafers-as-a-diagram create tier (below) implies writing a Wafer back, which again needs a path into Servitor beyond reading published data.
- Whether the GUI and the monitoring idea (see its own entry) share the published data source for the "see what is running" view.

### Wafers as a diagram

A first-class feature of the app should be rendering Wafers as a visual diagram, the way people are used to seeing workflows on Zapier or similar, built on an existing open-source graph/diagram library rather than from scratch. A Wafer is already a dependency DAG (ADR-0023), so it maps naturally onto a node graph: triggers (the `triggers:` entries) at the top, steps as nodes, edges for `depends_on`, and the branch/loop structure for `switch` and `foreach`.

Two tiers, in order of priority:

1. **Inspect (primary).** Read a Wafer (from the published data of one of the Servitor instances the app tracks, or a local file) and render it as an editable-layout diagram: nodes per step, edges for dependencies, and clear visuals for switch branches and foreach fan-out. This is read-only and the natural first cut, consistent with Servitor being agent-first (the artifact, not a builder, is the source of truth). This tier works identically in a native or web delivery.
2. **Create (secondary).** Let a user assemble a Wafer in the diagram and save it back as YAML. Secondary because Servitor is agent-first: agents author Wafers via the CLI/skill, so the visual builder is a convenience for humans, not the primary authoring path. If built, it must round-trip losslessly to the Wafer YAML (the artifact stays authoritative; the diagram generates and edits that YAML, never a divergent database row). Because the read-only app consumes published data only, creating a Wafer needs a separate path to get it into a runner, which the diagram-first (inspect) tier deliberately does not have.

The diagram-first direction leans on the same "the Wafer is the artifact" rule as the rest of Servitor (SPEC: The Wafer): the app renders and edits the YAML, it never becomes a second source of truth.

### Library: React Flow

Leaning toward **React Flow** (`@xyflow/react`, MIT) for the diagram, the same library n8n uses, over the other candidates researched (Cytoscape.js, AntV G6, vis-network, JointJS). Cytoscape.js + cytoscape-dagre was considered first as the lighter, zero-dependency, vanilla-JS fit for an embedded page (such as a Wails webview), but its demo is too bare bones for the richer node-editor experience wanted, and editing is a real (if secondary) goal. React Flow is the strongest node-based editor and handles both inspect and create well.

The cost: React Flow needs **React + a bundler** (for example Vite), so the frontend is a small React app (hosted on its own server in the web case, or bundled into a Wails window in the optional native packaging). It has **no built-in auto-layout**; pair it with **dagre** (MIT) or elkjs for the automatic DAG layout (which is what read-only inspection relies on).

Revisit the choice when actually building: if it turns out read-only inspection is the whole need and editing is never wanted, Cytoscape.js's simplicity becomes attractive again. For now, with editing in mind, React Flow is the leaning.

Not buildable until Servitor actually publishes the data the app reads and the shape of that published data is defined. The dogfooding idea covers publishing capabilities, and the monitoring idea wants a "see all runs" view, but neither yet specifies a signed, redacted, external-readable feed of run history and outcomes for an app to consume; that feed is the missing prerequisite. Until then this is just a promising direction, kept here so it is not lost.

## Multiple concurrent parks per run (foreach-body waits)

A possible future extension of "Suspended waits" (ADR-0040 through ADR-0043).
Today a run can park only one `wait` at a time: the continuation is keyed by
`run_id`, so a second park in the same run overwrites the first. The only shape
that needs more is a `wait` inside a `foreach` body, where several iterations
park at once and all must be resumed before the fan-in rejoin completes (for
example "send N approval requests, wait for all N to sign off, then ship").

Not decided, not in scope; the current one-per-run model is the deliberate
BSSN choice and covers the common cases. This entry records the shape in case
that changes.

### The use case, narrowly

Sequential waits never collide: a wait parks, resumes, then the next waits, so
one-per-run is perfect for a linear chain, a wait before a fan-in, or a wait
with a fan-out after it. Multiple simultaneous parks come from exactly one
place: a `foreach` body containing a `wait`, where N iterations park and each
must be resumed before the rejoin. It is the "wait for all N" fan-out shape.

### The alternative that avoids it

"Wait for N" can be modeled without multiple parks per run, the way Temporal
recommends child workflows: fan out to N separate runs (each run is one unit
with one wait), then chain them together with the `completed` trigger or a
collector. Each run keeps its single wait, reusing machinery Servitor already
has. This is the first answer to the use case.

### What it would take to support multiple parks

Not a one-line fix; it touches the coupled pieces a single park already
navigates (ADR-0040):

- **Per-park continuation key.** Key `suspended_continuations` by
  `(run_id, park_id)` (or `(run_id, node_id, iteration)`) instead of `run_id`
  alone, so each parked iteration gets its own row instead of overwriting.
- **Per-park status vs run status.** Today the run has one `waiting` status and
  `checkRunComplete` guards on `status != waiting`. With several parks the run
  is `waiting` while any iteration is parked, and each park/resume must manage
  the shared status correctly rather than flipping it wholesale.
- **Iteration-scoped signals.** A named signal must address a specific parked
  iteration, not "the run parked on this name". The effective signal name would
  have to encode the iteration, or the signal would carry a park id.
- **`run_deps` and pending consistency.** When one iteration resumes and others
  stay parked, the fan-in counters and pending count must stay consistent; the
  rejoin must wait for all iterations to both run *and* resume. This is the
  subtle part, the same coupling a single park has, multiplied by N.

Why it is separate: it is not part of the current suspend/resume spine. It is
independently buildable once the "wait for N" case is real enough to justify
the coupling, and it is not the recommended first answer to that case (the
N-runs modeling above is).

## Agent node (an LLM-driven action node)

An action node that delegates work to an LLM agent rather than to a fixed integration, for example a `call-llm` or `agent` node that sends a prompt (and prior `{event, steps}` context) to a hosted model (say OpenRouter) and returns its completion. The node is durable like any other: it runs as a subprocess (ADR-0008), its output is committed and threaded forward as a step result, and a `dedupe_key` guards a retry from re-invoking the model.

The realistic first cut is a single round trip (prompt in, text/JSON out), not a full agent loop. It is the "do something with a model" primitive, the kind of thing people reach for when they want an AI step in a workflow (the way Zapier and n8n bolt an OpenAI step onto their builders).

Open questions to settle if it moves toward a decision:

- **Where the model call lives.** It could be a curated helper wrapping an official SDK, or (more in keeping with Servitor's integration model) the model provider as a declared integration invoked through a subprocess (mirroring `mcp-call`), or even just `shell`/`http` calling an API today. The shape decides how `capabilities` surfaces it and how secrets are declared.
- **Whether it is one-shot or a real loop.** Single prompt/response is the simple default. A loop (tool use, multiple turns, agent frameworks) is a much bigger surface and probably out of scope for a v1 node.
- **Provider choice and key custody.** OpenRouter is one option; the node should be provider-agnostic in spirit (varlock holds the API key). Whether the node is named for a specific provider or a generic "call a model" is unsettled.
- **Return shape.** Free text, or structured JSON (for example via a response-format/schema) so downstream `transform`/`switch` nodes can route on it. Structured is the more Servitor-idiomatic answer.

Why it is an action node: it does work mid-run and produces a result that feeds downstream nodes, so it is a first-class action node, not a flow node and not a trigger.

## Code node (structured arbitrary code as an action node)

An action node that runs arbitrary code in a real language (say Python or JS) with Servitor's structured input/output contract, distinct from both `shell` and `transform`. The honest framing matters here: `shell` (runner.go) *already* runs arbitrary code as a subprocess, and `transform` already covers pure computation with structured in/out. So this node is not "run any code" (that exists); it is a specific ergonomic niche between the two.

### The niche it fills

- `transform` is structured (`{event, steps}` in, JSON out) but limited to JSONata expression power, no control flow, no side effects.
- `shell` is full-power but unstructured: hand-rolled JSON on stdout, quoting hell, no shape contract on the result.

A code node takes the **full language power of `shell` and the structured contract of `transform`**: the node receives `{event, steps}` as input and must emit structured JSON to stdout, in a real language, without shell quoting. That is the defensible reason to exist. If that niche is not compelling, it is just `shell`, and BSSN (ADR-0002) says do not build it.

### How it would run

It must follow the subprocess model (ADR-0008): a subprocess with only its declared secrets in the filtered env, structured JSON to stdout. No new security surface beyond `shell`, since the subprocess env is already the boundary. Three mechanism options:

- A hidden `__eval` subcommand of the servitor binary with a baked-in interpreter (the pattern `transform` uses; minimal deps, but bloats the binary).
- A **declared runtime integration** (like Singer and MCP, ADR-0018): the operator pins the runtime (`python3`, `node`, and so on) and the node invokes it, keeping the binary lean and the runtime choice the operator's.
- Not build it and rely on `shell` today (the simplest fallback).

### Open questions to settle if it moves toward a decision

- Whether the niche over `shell`/`transform` is worth it, or whether it is a duplicate that BSSN would reject.
- The mechanism (baked-in interpreter vs. declared runtime integration) and which language(s) to support.
- The result-shape contract: always JSON to stdout, or allow other types (and how errors are surfaced in the structured error format).
- Determinism does not apply here (Servitor checkpoints data, not a replayed program), so a code node is just a one-shot subprocess like any other node.

## Mobile push notification node (APNs / FCM)

An action node that sends a push notification to a mobile device or app, covering both of the dominant mobile push services: Apple Push Notification service (APNs) for iOS and Firebase Cloud Messaging (FCM) for Android (and to some extent iOS/web). Like the curated helpers, it wraps an official SDK and authenticates with a varlock-injected secret, so it slots into the existing action-node model: it runs as a subprocess (ADR-0008), its output is committed and threaded forward as a step result, and a `dedupe_key` guards a retry from re-sending a notification.

The realistic first cut is a single "send a notification" action: device tokens (from a prior step or the event) in, delivery status out. It is the outbound counterpart to the inbound `email_received` trigger: a way for a workflow to reach a person on their phone.

### How it would be shaped

Two related choices, in the same spirit as the model node:

- **One node type or two?** APNs and FCM are different wire protocols with different SDKs, but the node-level shape is the same (send to a device token, optionally with a title/body/payload). Options are a single `push-notify` node that dispatches by service, or separate `apns` / `fcm` nodes.
- **Curated helper vs declared integration.** APNs and FCM are best reached through an official SDK (a curated helper, the `slack`/`github`/`email` pattern), rather than through the `mcp-call`/Singer subprocess mechanisms, which do not fit a push send. So the natural home is a curated helper with its action surfaced via `servitor capabilities`.

### Open questions to settle if it moves toward a decision

- **Credentials and secrets.** APNs authenticates with a token or key (a `.p8` key, or a provider token), FCM with a service-account credential. Both are secrets that varlock would hold; the exact secret shape (and whether device tokens are secrets too, or just data threaded from a prior step) needs settling.
- **One node vs two**, per above.
- **Delivery confirmation.** Whether the node reports just "accepted" or returns per-device delivery status, and how failures (invalid/unregistered device tokens) are surfaced in the structured error format.
- **Whether it is just a curated helper today** (the simplest fallback, since `http` could already call the FCM/APNs REST endpoints directly with a secret).

## Servitor monitoring: watch for failures and see what is running

An idea for monitoring Servitor, aimed at a human operator. It has two halves that are really one concern (knowing what is going on) split by whether you wait for a problem or go looking:

- **Reactive alerting** — tell the operator when something is wrong: a run or node failed, a run is stuck, the daemon is down, a webhook trigger stopped matching, a secret is near expiry or failing to resolve, the queue is backing up.
- **Passive observability** — let the operator see what is happening: all runs, their outcomes, durations, retries, and the health of the runner. Much of this data already exists (`servitor runs`, `servitor run <id>`) but there is no unified view over it.

### The reactive half is dogfooded (a Wafer)

Servitor already has the primitives: cron and internal-event triggers, run data, and outbound notify nodes (slack, email, the mobile-push node). So reactive monitoring is naturally a **Wafer**, consistent with the dogfooding idea (Servitor watches itself using its own workflow mechanism) and with "the Wafer is the artifact": the alerting logic is a normal workflow, not bespoke machinery.

The load-bearing question is how the health/failure signals get *into* the Wafer's reach, since today a Wafer only sees external events and its own `{event, steps}`. Two signals, likely both:

- **Internal failure events.** Emit an event when a run/node fails (and on daemon events such as start), so a monitoring Wafer subscribes and alerts immediately. Event-driven, no polling, no new storage.
- **A health read.** A `servitor health`-style command (or status file) a cron-triggered Wafer polls for daemon liveness and queue depth. Needed because a dead daemon cannot emit events, so liveness needs a heartbeat.

The product surface stays small: expose the signals, and the operator composes them into an alerting Wafer with existing notify nodes.

### The passive half shares run data with the native app

Seeing "all the runs" is human-facing and read-only, the same territory as the native-app idea (browse runs). It is distinct from the reactive half (active alerting vs passive browsing) and does not depend on the app, but the two can share the same run/outcome data, and the app is a natural consumer of it. If only one is built first, reactive alerting is the smaller, more clearly valuable slice.

### Open questions to settle if it moves toward a decision

- Whether a monitoring Wafer is one shipped Wafer, or a set of exposed signals the operator composes into their own Wafer (leaning toward the latter, in the agent-first spirit).
- Which internal events and health fields to expose first, and how a Wafer reads them.
- How much of the passive half belongs to monitoring vs the native app, and whether they share a data source.

## Compare the Wafer format against Home Assistant's automation YAML before release

Before Servitor goes public, sanity-check the Wafer schema against the automation YAML format used by Home Assistant (HA), the most widely adopted declarative automation syntax there is. HA's format is a useful baseline because it solved, at scale, the same problem we are solving: expressing "when this happens, if this holds, do this" in a flat, human-and-agent-readable YAML file. The goal is not to copy HA, it is to find any place where our Wafer is harder than it needs to be while there is still no installed base to break.

HA's automation is built on three top-level lists, in order:

```yaml
alias: "Turn on the hall light on motion at night"
triggers:
  - trigger: state
    entity_id: binary_sensor.motion_hall
    to: "on"
conditions:
  - condition: time
    after: "20:00"
    before: "06:00"
actions:
  - action: light.turn_on
    target:
      entity_id: light.hall
```

The properties worth comparing against (not all of which are automatically better than ours):

- **Triggers, conditions, and actions as sibling lists.** HA separates *when* (triggers), *if* (conditions), and *do* (actions) into three explicit top-level keys, rather than folding them into a single trigger/step chain. Ours uses trigger + DAG of nodes; the question is whether the condition/if boundary is as legible in a Wafer as it is in HA.
- **A named `alias` for the automation**, human-first, distinct from any id, so a person or agent can refer to it by a friendly name. Worth comparing to how a Wafer is named.
- **`mode`**: single, queued, parallel, restart. HA lets the author say what happens when a trigger fires while the previous run is still going (drop it, queue it, run concurrently, or restart it). Servitor's trigger and queue semantics are a real design surface, and HA's four-way split is a mature vocabulary for it, even if we do not adopt all of it.
- **`choose` / `if`** for branching in actions, a declarative alternative to our `switch` step. Worth confirming ours is at least as ergonomic.
- **`id` on triggers and actions** so an automation can refer to a specific trigger/action (for rerun-this-trigger or to remove a previous action's effect). A small but real feature for recovery/cleanup semantics.
- **Everything is a list, order matters within it.** HA's lists are ordered and every item is self-describing (each starts with a `trigger:`/`condition:`/`action:` discriminator). That is the same "list of discriminated nodes" shape our DAG uses, so it is likely fine; the comparison is about whether the three-part trigger/condition/action separation reads better for an agent than a single node graph.

Why it matters before release: the Wafer is the artifact, the thing agents and humans author and read, so its ergonomics are the product's ergonomics, and the cost of getting them wrong goes up sharply once there is an installed base. HA is the strongest prior art for "declarative automation YAML that non-experts actually write," so a point-by-point comparison is cheap insurance. It is a review exercise, not a commitment to adopt HA's shapes.

Open questions to settle if it moves toward a decision:

- Whether HA's three-part trigger/condition/action separation is worth any of our structure, or whether our trigger + DAG already covers it (and `conditions` are just a node that gates).
- Whether to adopt an `alias` (or keep `id` and add a separate friendly name).
- Whether `mode` (single/queued/parallel/restart) is a gap in our trigger/queue semantics worth naming explicitly.
- Whether `id` on nodes for cleanup/recovery is a gap.

## Failure modes and rerun policy

A possible extension of the rerun feature (ADR-0044). The rerun machinery lets
any failed run be `continue`d / `restart`ed / `discard`ed, with the mode chosen
by the operator or a `rerun-failed` node. Today the cause of the failure is not
what drives that choice: the worker classifies only the secret-failure causes
(`missing_secret`, `secret_source_unreachable`, `secret_auth_failed`) to decide
retry vs fail-fast, and every other failure falls through to the generic retry
path. This idea asks whether a fuller failure taxonomy should exist, and whether
the rerun policy should key off it automatically. (The rerun mechanism itself,
ADR-0044, is built; this is the policy layer on top of it.)

Why it is separate: it is not needed for the general-rerun build (that is one
mode at a time, decided by the operator). It has real alternatives and design
questions, so it is a decision in its own right.

### The draft taxonomy

Different causes want different responses; a taxonomy makes the response
explicit rather than a blanket mode:

- **Transient infrastructure** (source unreachable, 5xx, network): retry with
  backoff, then auto-`continue` if it stays down, because it is likely to
  succeed on a later run. The worker already retries source-unreachable with
  backoff today.
- **Auth / stale secret**: operator fixes the value, then `continue` from the
  failed node. This is the case the current SPEC frames resume-from-failure
  around.
- **Permanent / config error** (missing secret, bad expression, unknown node
  type): retrying is pointless; `discard` or fail fast. The worker already
  treats `missing_secret` as fail-fast.
- **Business-logic failure** (a shell command genuinely failed, an http call
  returned a permanent 4xx): `continue` may be fine; `restart` only when
  side-effecting nodes declare `dedupe_key`.

### How it would compose

A fuller classification in the worker's failure path (extending what
`recordFailure` already does for secrets) would feed the rerun modes: the mode
could be chosen automatically from the cause, or the cause could be surfaced so
an operator or a `rerun-failed` node makes an informed choice.

### Open questions

- How granular the taxonomy: the four above, or finer (per-node-type)?
- Whether the policy is **automatic** (the runner picks the mode from the
  cause) or **advisory** (the cause is surfaced, the operator or node chooses).
- How it composes with `dedupe_key` (a `restart` re-does side effects unless
  guarded).
- Whether to auto-`continue` transient failures, and with what backoff/bound,
  or whether that reintroduces the retry loop the rerun feature is meant to
  give the operator control over.

## Research the real-world webhook signing landscape to validate the two mechanisms

Phase 18 (ADR-0049) split inbound webhooks into two mechanisms by verification
scheme: `hmac-webhook` (HMAC-SHA256 over the raw body, or a timestamped,
replay-bounded form; the signature header, encoding, version prefix, and
timestamp header are all receiver config) and `standard-webhook` (the Standard
Webhooks envelope). This is a bet that two primitives plus config cover the
actual webhook signing schemes in the wild. Before the trigger surface gets an
installed base, a thorough research pass over real providers would check that
bet.

### What the research is

Survey a broad set of webhook-sending services and classify how each signs its
requests. For each, answer: what is signed (the raw body, a timestamped string,
a structured envelope), in which header, in which encoding (hex or base64),
with what version prefix, and with what replay bounds. Then map each provider
onto the two mechanisms plus their config fields and see whether any does not
fit. Providers worth covering: the major SaaS that webhooks are built for
(GitHub, Slack, Stripe, Twilio, Discord, Linear, Notion, Grist, PagerDuty,
Shopify, GitLab, Bitbucket, Jira, Zendesk, Intercom, Box, Dropbox, outgoing
hooks from automation tools), the Standard-Webhooks-envelope senders (OpenAI,
Anthropic, Supabase, and Twilio's SW flavor), and the no-signing (open) and
non-HMAC-authenticated (static header, OAuth-verified) cases.

### Why it is worth doing now

- The trigger surface just changed (breaking): the per-service types were
  removed and replaced by the two mechanisms. The cost of discovering that a
  primitive is wrong is higher once Wafers in the wild depend on it.
- The bet is falsifiable and cheap to check: each provider is either a config
  entry against `hmac-webhook` or `standard-webhook`, or it is a
  counterexample.
- It sharpens the AI-authoring story: if every provider maps cleanly, then
  `webhook/receivers.yaml` genuinely lets an agent author a trigger without
  knowing a sender's scheme from memory.

### What the outcome would be

- If everything maps to the two mechanisms as config: the primitives are
  validated, and the research record is discarded or distilled to a short note.
- If a real scheme does not fit (for example a provider that signs a
  materially different string, or one that needs a different verification
  primitive): that is a genuine finding, recorded as a decision (a new ADR and
  a SPEC change, possibly a new mechanism or a new config field), not an
  afterthought.
- If a class of providers is simply unverifiable this way (static-header auth,
  no signing): it is a documented limitation of the raw-body-delivery model,
  not a mechanism gap.

### Open questions

- How broad the survey must be to be convincing, and how to keep it honest
  (primary sources: the provider's own docs, not folklore).
- Whether the findings deserve a committed reference table in the repo, or
  whether they only feed decisions and then get discarded (BSSN leans to the
  latter: the map is validation, not a living artifact).
- Whether a scheme that appears often enough deserves to graduate from config
  to a documented preset (for example a `github` receiver the CLI can scaffold
  with the right header, encoding, and prefix), without becoming a mechanism.

## (Add more ideas here as they come up; delete them when they become ADRs or
## are discarded.)
