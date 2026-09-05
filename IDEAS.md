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

## Separate small subprocess binaries for the pure-compute mechanisms

`transform`, `switch`, and `foreach` run Servitor's own pure computation and
routing as a subprocess of the servitor binary itself (`selfexe`, ADR-0008):
the worker spawns `[servitor __transform|__switch|__foreach <expr>]` to keep
even trusted computation out of the runner's process. A dedicated small binary
(a leaner build that links only what the pure-compute subprocesses need) is the
alternative. Two considerations, the first performance, the second security.

Performance: the binary is ~17MB, but a spawn does not read it all into memory
(exec mmaps and faults pages lazily), and the dominant cost is the constant Go
runtime init (~1-3ms), which is the same whatever the binary size. So a small
binary would save, at best, that constant few ms per step, on a path that is
not hot (control flow and pure computation, not I/O). Whether the cost matters
at all is a profiling question.

Security (unclear, worth looking into): the subprocess is the entire servitor
binary, not a thin evaluator. That means every mechanism package is linked and
its `init()` runs, and the code for Honker, the secret providers, Singer, MCP,
and HTTP is all present in the process even though `__transform` only evaluates
a JSONata expression. The subprocess env is filtered to the node's declared
secrets and JSONata evaluation is bounded, so the concrete exposure today looks
small, but a dedicated small binary would carry a far smaller attack surface
than the full runner. Whether running the full binary as a pure-compute
subprocess is a real problem is an open question, not a settled one.

The other side of the trade: the self-exe pattern avoids a second artifact to
build, version, pin, and keep in sync with the runner, and ADR-0008 already
records the escape hatch that any in-process or dedicated-binary optimization
should come only after profiling demonstrates a real cost, as a measured,
reversible change behind a benchmark.

Not acting on this now. If it is revisited, the open questions are: whether the
concern is the performance hot path or the security surface (or both); a
dedicated small binary vs re-adding an in-process path; which mechanisms
qualify (likely `transform` first, then `switch` and `foreach`); and how it
stays consistent with ADR-0008's single-subprocess-mode rule.

## Sandbox the shell node on Linux: user namespaces, mount masking, and an egress allow-list

`shell` is the most powerful node and the one the runtime contains least. It is
arbitrary code running as the runner's user on the host. The subprocess
boundary (ADR-0008) isolates the runner's process from the node, not the host
from the node: a shell node can read the SQLite file, the config, and call the
decryption service or TPM to obtain any on-box secret on demand (SPEC: Secret
resolution admits exactly this). Today the real boundary around `shell` is the
trusted Wafer author and the reviewed PR pipeline, not the runtime. This idea
is the researched answer to "what OS-level isolation makes `shell` mostly
safe". It is Linux-only by choice. The research (Sept 2026) settled the core
question: containment of a same-UID child is achievable, with user namespaces
and (better) subuid mapping, and the honest ceiling is the granted secret and
the kernel, not the node's access to the box.

The natural shape is a **separate mechanism, `sandboxed-shell`, alongside
`shell`**, not a modification of it. This matches how Servitor already handles
variants: `hmac-webhook` vs `standard-webhook`, `mcp-stdio` vs `mcp-http`, and
`singer-tap` vs `singer-target` are separate mechanisms whose type name carries
the variant (SPEC: How an agent discovers capabilities). The author picks which
one a node uses, and the choice is visible in the Wafer. `shell` stays exactly
as it is for deployments that want the dangerous version, so existing Wafers
are untouched. The operator decides which exist: the disable idea can turn
`shell` off while keeping `sandboxed-shell`, or delete the `core/shell/` folder
outright (the mechanism folder is the unit of deletion, ADR-0048).

There is a competing shape worth stating, from the same observation that the
sandbox is a *how* (the way a node runs) and not a *what* (a different thing
the node does). The product already declares preconfigured versions of
mechanisms in config and surfaces them as capabilities: ADR-0018's declared
connectors (`mcp/servers.yaml`, `singer/taps.yaml`) are named instances that
pin the command and env, and a Wafer names an instance, never a per-instance
type. A **mechanism flavor** would generalize that pattern from
command/url/env parameters to behavioral parameters: the config could declare
`sandboxed-shell` and `scripts-only-shell` as named preconfigurations of
`shell` that appear as capabilities, derived from the base mechanism's schema
with values pinned, the way `capabilities` already derives example fragments
from schemas. That fits the declared-config philosophy (the box advertises what
it supports, per deployment) and avoids a compiled-in type per combination.
The honest caveat: the sandbox changes the execution harness, an axis the
declared-connectors pattern never carried, so a flavor framework is a real new
concept, not a mechanical extension. BSSN says do not build the generalized
framework until it has real users; there are already two candidate flavors
(`sandboxed-shell`, `scripts-only-shell`), and the disable idea is the same
"mechanism configuration" family, so the choice is between keeping them as
distinct capability names today and generalizing to flavors when a third real
one appears.

### The key finding: same-UID containment is possible

The earlier framing ("a same-UID sandbox is not a privilege boundary") was too
absolute. It is true only without a user namespace:

- **Without a user namespace**, a same-UID child is not containable against
  the runner. On stock kernels (Yama ptrace_scope=0) it can ptrace the runner
  and read its memory; on Ubuntu/Debian defaults (Yama=1) it cannot trace the
  runner, but that protection is distribution-dependent and absent upstream,
  and it can still signal the runner and shares DAC file access.
- **With the node in its own user namespace**, cross-namespace ptrace and
  `/proc/<pid>/mem` access are denied unconditionally by the kernel's ptrace
  access-mode check, regardless of Yama. This closes "trace the runner and read
  its memory" as a kernel property, at the same UID.
- **Mapping the node to a different host UID** (subuid mapping via
  `/etc/subuid` and `newuidmap`, the rootless-container mechanism) makes
  "cannot read the runner's files" true at the DAC/filesystem level, not by
  policy: the run DB, config, and on-box secret material are simply another
  user's files to the node. This is the biggest single win and works
  unprivileged.

So "mostly safe" is a real, reachable target: not a hard wall, but blast radius
reduced to (mostly) the node's granted secrets and the kernel itself.

### The concrete stack (Linux-only, kernels ~5.13+, better on 6.12+/7.x)

Per shell node, spawn it as:

1. **A new user namespace** (identity map, or the subuid map below).
2. **A new PID namespace**: the node cannot see or signal the runner, and PID
   1 reaps zombies.
3. **A new mount namespace with an empty tmpfs root**, read-only binds of
   exactly what the node needs (its command and runtime libs, any granted
   data), a read-write scratch/output dir, fresh `/tmp` and `/proc`, and
   nothing else: no path to the run DB, config, TPM socket, or key material.
4. **A new network namespace with only loopback**, so all egress goes through a
   runner-side proxy that enforces a per-connection host(+port) allow-list,
   checking each new connection (and each redirect) as it happens.
5. **seccomp**: deny the dangerous set outright (ptrace, process_vm_readv/
   writev, mount, pivot_root, bpf, perf_event_open, keyctl, kexec) and deny
   `io_uring_setup` and the io_uring family unconditionally. io_uring executes
   work in kernel worker threads that never make syscalls, so seccomp does not
   cover it and it bypasses the filter.
6. **Capability bounding set dropped to empty** plus `no_new_privs`, so the
   node can never exec its way back up.
7. **Landlock** (kernel 6.7+ for TCP rules, 6.12+ for scopes) as a deny-by-
   default filesystem and port backstop layered over the mount namespace. It is
   the newest and least battle-tested layer; treat it as defense in depth, not
   the primary boundary.
8. **A per-node cgroup v2** with memory/pids/cpu limits and a BPF device
   filter (no devices; the node's `/dev` is fresh), killed wholesale via
   `cgroup.kill`.

Each layer closes a specific hole: userns closes tracing, PID ns closes signals
and process visibility, mount ns closes file and store access, net ns + proxy
closes arbitrary egress, seccomp shrinks the kernel attack surface, subuid
closes DAC-level access to the runner's files.

### What the runner must do

- Spawn the node through a **launcher** (bwrap or nsjail, or `systemd-run`
  with the equivalent systemd.exec options) that performs the namespace, mount,
  mapping, and seccomp setup before exec, then execs the shell. Go's `os/exec`
  handles user namespaces and UID/GID mapping natively via `SysProcAttr`
  (`Cloneflags`, `UidMappings`, `GidMappings`), but the mount tree and Landlock
  need a launcher: Go's runtime is multithreaded and `os/exec` has no pre-exec
  mount hook.
- Pass the node a small structured **grants descriptor** (read-only paths,
  writable scratch dir, egress allow-list, seccomp profile, desired subuid).
  The launcher, not the node, translates grants into mounts and rules, so the
  node never sees the mechanism.
- The existing data channels are unchanged and work across every boundary: the
  filtered env (PATH + declared secrets) survives exec and all namespaces, and
  stdin/stdout/stderr pipes work across UID and namespace boundaries. Per-node
  secret delivery (ADR-0033) composes with the sandbox unchanged; the sandbox
  hides everything else.
- Teardown via SIGKILL through the PID namespace or `cgroup.kill`, then clean
  the scratch dir.

### Host requirements

- **Unprivileged user namespaces enabled.** The one real friction point:
  Ubuntu 23.10+ and 24.04+ restrict unprivileged userns by default via
  AppArmor (`kernel.apparmor_restrict_unprivileged_userns=1`), which blocks the
  bwrap/nsjail userns path for unconfined processes. The fix is an AppArmor
  profile for the Servitor daemon carrying the `userns` rule, or the sysctl set
  to 0. This is an install prerequisite to call out.
- For subuid mapping: `/etc/subuid` and `/etc/subgid` entries for the runner's
  user, and the `newuidmap`/`newgidmap` setuid helpers (shadow-utils).
- For cgroups: a cgroup v2 mount with delegated controllers (systemd's
  `Delegate=` handles it).
- A kernel with userns, PID, mount, and net namespaces plus seccomp, and
  (optional) Landlock; distro kernels since roughly 2024 qualify.

### The honest ceiling (what no combination stops)

- **A granted secret can still be exfiltrated.** The node holds its declared
  secrets in env and can copy them into its JSON result or, with any granted
  egress, send them to an allowed host. No sandbox stops this; only the
  credential-proxy idea (keeping the value out of the node entirely) does. This
  is the load-bearing reason that idea stays separate and is still worth its
  own engineering.
- **Kernel bugs.** All of this trusts the host kernel; a vulnerability in any
  allowed syscall or reachable driver is an escape to the runner's UID.
  Removing that residual needs gVisor or a VM, both out of proportion for
  per-node shell spawns today.
- **The allow-list's confused deputy.** An allowed host that is redirected or
  fronted can still receive a secret; the allow-list checks the destination,
  not the other end.
- The sandbox setup itself (launcher, mount recipe, mappings) is trusted code;
  a bug there silently downgrades the sandbox.

### Build order (BSSN, each step independently valuable)

1. Mount namespace masking alone (empty root, read-only binds, fresh `/tmp`
   and `/proc`): the highest-value reduction for the least machinery, works on
   every kernel.
2. Add userns + PID + net namespaces: closes tracing, signals, process
   visibility, and raw egress.
3. Add the egress allow-list proxy (per-connection).
4. Add the seccomp deny-list, capability drop, `no_new_privs`, and a per-node
   cgroup.
5. Move to subuid mapping for real DAC separation (host prerequisite: subuid
   ranges + newuidmap).
6. Landlock as a final deny-by-default layer once the others are proven.

None of the sandbox machinery changes an existing capability: as a new
mechanism this adds one node type (`sandboxed-shell`) and its schema to the
Wafer surface and `capabilities`, and `shell` and every other node behave
exactly as they do. The sandbox itself is confined to how that node's
subprocess is spawned, so it composes with per-node secret delivery and the
subprocess isolation model.

Why it is separate: it is not part of the secret-resolution spine. It is a
runtime-boundary question with its own engineering (host kernel features,
profiles) and its own honest ceiling, and it is the shell half of the
credential-proxy idea's open question about nodes that cannot speak proxied
HTTPS (see its entry). It matters most for `shell`, which is why it is tracked
here on its own.

Open questions:

- Launcher choice: bwrap vs nsjail vs `systemd-run` (`systemd-run` gives a full
  maintained sandbox but couples the runner to systemd; bwrap/nsjail keep it
  portable).
- The separate-mechanism shape resolves the earlier per-node-flag question: the
  opt-in is the author choosing `sandboxed-shell` over `shell`, and the
  operator's control is which of the two exist (the disable idea, or folder
  deletion). A per-node flag on `shell` would be a second, overlapping way to
  say the same thing; BSSN leans to one mechanism. Because the sandbox has host
  prerequisites (unprivileged user namespaces, subuid ranges, cgroups) not
  every deployment has, `sandboxed-shell` is naturally opt-in per deployment: a
  box without the prerequisites does not expose it. The load-bearing rule is
  that a Wafer using `sandboxed-shell` on a host that cannot sandbox fails
  loudly (at validation or submit), never silently degrades to unsandboxed.
- Whether the egress allow-list is host(+port) or CIDR, and how it is declared
  (config vs Wafer).
- Whether subuid mapping is the default or an option (it changes the
  file-ownership story for node scratch dirs).
- Interaction with the TPM-unlock tier of the secret model: if the node's
  `/dev` hides the TPM, a shell node simply cannot reach the unlock material,
  which is a strengthening, but the runner still must reach it.
- Whether this also covers the other node types. The machinery lives at the
  subprocess-spawn boundary (ADR-0008), so it wraps any node at the same
  plumbing cost, but the value and cost are not uniform and line up favorably.
  The stack is really two halves that benefit different mechanisms:
  - **Host-containment** (mount masking, userns, PID ns, subuid, seccomp,
    caps drop) is worth as much as the executed code is untrusted. It matters
    most for `shell` and the third-party connectors (`mcp-stdio`, `singer-tap`/
    `target`, and the `email_received` fetcher): operator-installed or
    author-authored arbitrary executables, where the threat is malicious or
    broken code. It adds little for Servitor's own compiled binary, where the
    threat is a bug in vetted code, not malice.
  - **Egress control** (netns + per-connection allow-list proxy, Landlock
    ports) is worth as much as the egress is declarable. `http`, `mcp-http`,
    and the `email_received` fetcher get it almost for free and high value:
    each has exactly one declared destination, so the allow-list is trivially
    correct, blocks SSRF and redirect-based exfiltration, and costs near
    nothing. `shell`, `mcp-stdio`, and the singer nodes need
    operator-configured per-connector host lists, because Servitor cannot know
    in advance which hosts a tap or MCP server reaches.
  - **Per-mechanism verdict**: full treatment for `shell`, `mcp-stdio`, and
    `singer-tap`/`target` (untrusted code, both halves); cheap exact-egress
    allow-list plus light masking for `http`, `mcp-http`, and `email_received`;
    skip `transform`, `switch`, and `foreach` (hold no secrets, no egress, and
    sit on the hot loop where per-spawn overhead is felt); not applicable to
    `wait`, `send-signal`, `rerun-failed`, and the triggers, which are
    worker-handled or in-daemon, not subprocess nodes.
  - **Resource limits follow a different axis.** cgroup limits pay off for
    anything long-running or runaway-prone: `singer-tap` (streaming),
    `mcp-stdio` (can hang), `shell` (a command can loop), and the `email`
    poller. Short-lived `http` and compute nodes barely need them.
  - The third-party connectors are declared in `servitor.config.yaml`
    (ADR-0018), so their egress allow-lists are a natural per-connector config
    field, and that composes with the disable-mechanisms idea.

## A scripts-only mode for shell (an operator-gated scripts folder)

A middle ground between `shell` enabled and `shell` disabled, for deployments
that want shell's power but not its open-endedness. The config declares a
folder of allowed shell scripts, and a Wafer's shell node may not contain an
arbitrary inline command: it may only call a named script from that folder.
Whatever is not in the folder cannot run.

How it would behave:

- The config names the folder (a setting on the shell mechanism in
  `servitor.config.yaml`).
- A shell node in this mode has a `script: <name>` field instead of a free
  `command:` string. Validation rejects an inline command and any script name
  that does not resolve inside the folder.
- The folder is a deploy artifact, gated by the same reviewed PR pipeline as
  Wafers, so the trust boundary becomes "reviewed scripts" instead of
  "reviewed inline commands".
- It is a third, distinct state for shell in the disable idea: disabled /
  scripts-only / full.

Why it is attractive:

- It closes the data-to-code injection boundary for shell (THREATS.md). The
  command is no longer a static template with runtime data interpolated into
  it: the Wafer names a script, and the script consumes data as data (its
  `{event, steps}` input), so data can never become part of the code that runs.
- It keeps shell's real power (control flow, real scripts, tools) while
  removing the most dangerous surface, arbitrary inline author code, and the
  allowed set is CI/CD-reviewable like everything else.
- It composes with the sandbox: the scripts still run through
  `sandboxed-shell` when the deployment uses that mechanism.

Open questions:

- Whether a script is called with its `{event, steps}` input on stdin (the
  normal node contract) or with argv.
- Whether scripts-only is a config setting on the shell mechanism or its own
  mechanism (a restricted `shell` vs a separate type).
- Whether the folder sits inside the sandbox when `sandboxed-shell` is in use
  (it should: the folder path is bind-mounted read-only, so a script cannot
  read beyond it).
- Whether scripts-only shrinks the demand for full `shell` enough that the
  "wants the dangerous version" cases are rarer than they look.

## Disable mechanisms per deployment (a config-level off switch)

The mechanism registry is the compiled-in set (ADR-0045, ADR-0048): every
capability a deployment can use is there unless its folder is deleted, which
means a rebuild and a permanent fork. This idea gives the operator the
per-deployment alternative: disable any mechanism in `servitor.config.yaml`,
so a deployment can, for example, turn off `core/shell` and make that
capability unusable on this Servitor without touching the binary.

How it would behave:

- The registry stays the compiled-in set; the config filters it at load. A
  disabled mechanism is still registered but marked disabled.
- `capabilities` reports a disabled capability explicitly (for example a
  `disabled: true` marker on the entry and in the index), rather than letting
  it vanish. "This exists here but is off" is different information from "this
  server does not have it", and an agent that sees the capability listed can
  understand why a Wafer using it fails.
- Validation rejects a Wafer that uses a disabled mechanism, at dry-run and at
  submit (and anywhere else a Wafer is validated), with a clear error naming
  the disabled mechanism and the config entry. A Wafer cannot be registered
  with a disabled node or trigger type.
- The disabled state composes with the rest of the declared config: a webhook
  receiver whose mechanism is disabled, a declared MCP server or Singer tap
  whose mechanism is disabled, and a secret whose `source` mechanism is
  disabled (the secret-resolution group, ADR-0036) all fail at validation or
  use with the same clear error.

Why it is attractive: it makes the trust boundary operator-declarable. The
reviewed-PR pipeline vouches for a Wafer's content; this lets the operator
decide which primitives are even available on the box. For a deployment that
never wants `shell`, "disable it" beats "be careful" and beats a fork.

Why it is separate: it is a governance surface that touches the config schema,
the registry load, validation, and the capabilities output shape. It is a real
decision with alternatives (disable vs delete, blocklist vs allowlist, mark
disabled vs vanish), so it earns an ADR before building, plus tests that pin
the validation and capabilities behavior.

Open questions:

- Blocklist (disable a few, the rest on) vs allowlist (enable only these, the
  rest off). Blocklist is the direct reading of "disable any mechanism";
  allowlist is the safer default.
- Mechanisms only, or mechanism groups too (for example disable all of
  `webhook` at once).
- Whether a disabled capability is marked disabled in `capabilities` (the
  leaning, so dry-run errors are legible) or removed entirely.
- How disabled state composes with the load-once-at-boot config pattern and its
  change-detection gap (THREATS.md): does toggling a mechanism need a daemon
  restart?
- Whether a disabled mechanism's run handler is unreachable as defense in
  depth, or the gate is validation-only.
- Whether disabling a mechanism that others depend on (a secret source used by
  a node) is rejected at config load or fails at use.

## (Add more ideas here as they come up; delete them when they become ADRs or
## are discarded.)
