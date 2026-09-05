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

**Depends on:** the execution surface idea (it is the proxy mode of the
secrets category) and its blind-tunnel egress rule (the credential proxy is the
deliberate exception to that rule, not a replacement for it).

This is the **proxy mode** of the secrets category of the execution surface: an
opt-in alternative to env delivery (ADR-0033). Today each node receives its
declared secrets in its subprocess env, per node, eliminated after. Proxy mode
goes further: keep the real value out of the node entirely, even while the node
uses it. The node holds a placeholder; the proxy swaps in the real value only
on a TLS-verified connection to an allow-listed host, and scrubs it from
responses. A compromised node cannot copy what it never held.

### The tension with the blind-tunnel egress proxy, resolved

The egress proxy of the execution surface is a **blind tunnel**: it reads only
the destination and never payloads, and that is load-bearing, so it cannot
become a place secrets are visible. The credential proxy **must** read and
rewrite payloads to inject the value. These are not the same proxy. The egress
proxy is the default path for destination control and stays blind; the
credential proxy is a separate, secret-aware component used only by nodes
explicitly opted into it (secrets category = proxy). It is the deliberate exception
to the blind-tunnel rule, engineered to hold secrets by construction:
transient (the value dies with the connection, like per-node delivery),
contained, and daemon-owned. For nodes on this path the "blind" guarantee does
not hold by design, which is acceptable because it is an explicit opt-in for a
specific node, never the default.

### What it actually buys

The one thing proxy mode adds over env delivery plus the execution surface is:
a compromised node cannot copy its own granted secret into its memory, because
the value never reaches it. That is narrow. The execution surface already
prevents the node from reaching other secrets (containment), limits it to its
own declared secrets (per-node delivery), and the honest ceiling already accepts
that a node holding a granted secret can exfiltrate it. Proxy mode closes one
specific instance of that, the node copying its own secret, for a narrow set of
nodes.

### Costs and limits

- It introduces a new secret-handling component, a place secrets are visible,
  which is exactly the surface the blind-tunnel rule protects against. Its own
  security rests on being transient, contained, and trusted, not on "never sees
  secrets".
- It only works for cooperating, HTTPS-speaking clients (it must see and
  rewrite the request). Non-cooperating nodes (`shell`, `mcp-stdio`, singer)
  cannot use wire-injection; they hold real values or the value never reaches
  them, which is a different design.
- The confused-deputy residual is unchanged: the proxy injects the value into a
  request to an allow-listed host, and if that host is fronted or redirected the
  secret goes there. It checks the destination, not the other end.
- It is HTTP/1.1 + public-CA only.

Net: the benefit is marginal against the execution surface plus env delivery,
and it costs a new secret-exposure point for the narrowest node slice. It is
kept as an idea because it is the only thing that closes the node-copies-its-
own-secret case, but it is not load-bearing and may not be worth building.

Open questions:

- Whether the marginal benefit ever justifies the new secret-handling surface,
  or whether env delivery plus the execution surface is the right ceiling
  (leaning: the latter).
- Whether proxy mode, if built, is per-node opt-in with a human-gated grant, in
  keeping with the lock model.

## Secret permission enforcement (beyond informational)

A separate idea that follows from the secrets model's v1 decision that a secret's declared permissions are **informational only** (they exist so an agent reaches for the right secret; Servitor does not verify a match). This idea is about whether Servitor could ever *enforce* that an action node's operation matches a secret's declared permissions.

Why it is hard: permission names are not standardized across services. GitHub's fine-grained permission matrix is very different from gmail's or Slack's, so a node's "needs permission X" only makes sense within a service context. Enforcing the match would require a per-service permission vocabulary that Servitor maintains and validates against, which is complex and may be impossible to do well. This is deliberately out of scope for v1.

## Isolate secret resolution from the runner (the runner's own trust boundary)

A research question that follows from the execution surface. The execution surface contains *nodes* (the untrusted subprocesses), but the **runner itself holds resolved secrets in memory** while it resolves them: per ADR-0033 the provider runs in-process, so the runner is a long-lived process that carries every secret it has resolved for its registered Wafers. If the runner is compromised, all the node containment is moot, because the runner can resolve every secret. The question is whether secret resolution should be **more isolated than "the runner just does it"**, so that a compromised runner does not automatically hold every secret's plaintext.

This is distinct from the credential proxy (which keeps values out of *nodes*) and from the TPM-unlock interaction (which is a non-conflict, the runner resolves in its own process). This is about the trust boundary of the runner itself, a different axis.

Open questions to research:

- Whether a separate, more contained resolution process (the runner talks to it, it resolves and hands values back) meaningfully reduces the blast radius, or whether it just moves the trust boundary one step without closing the gap, since the runner must still receive the values to hand them to nodes.
- Whether the honest ceiling is that the runner, as the orchestrator that must deliver secrets to nodes, is inherently a secret-holding process, and "isolate resolution" only helps against a narrow class of runner compromise (for example one that cannot call out to the provider) rather than the general case.
- How it would compose with per-node delivery (ADR-0033): the resolver would still need to hand each value to the node, so the isolation is about where the value lives between resolve and delivery.
- Whether it is worth doing at all given the execution surface already contains nodes, or whether the runner's trust boundary is simply accepted as the system's trusted base, like the kernel.

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

## The execution surface: isolation and runtime policy as generalized primitives

**Depends on:** the lock model idea (the execution parameters are what the lock
model governs). It does not depend on the credential-proxy idea: that idea is
merely the proxy mode of the secrets category, a mention, not a load-bearing
dependency, and the execution surface stands without it. It is otherwise
foundational.

Every mechanism's node runs as a subprocess (ADR-0008), and how a node is
allowed to run is a set of orthogonal, mechanism-independent **execution
parameters**, grouped into **execution parameter categories** (containment,
egress, resources, secrets, identity, data flow). The term "category" in this
idea means an execution parameter category, not a capability's Role (which the
registry also calls a category) and not a mechanism group. Today the runtime
implements only a few of them (subprocess boundary, per-node secret delivery,
output capture and redaction). This idea makes the whole set a generalized
feature: any current or future mechanism can be hardened without per-mechanism
work, because the enforcement machinery is shared. It grew out of asking what
it would take to make the shell node safe, but the answer is not a shell
feature, it is a runtime primitive.

Terminology: a mechanism has **fields**, the existing term for a settable
attribute (the `Field` type in the registry, "fields" in the SPEC). A
**function parameter** is a field on the function surface (what the node does,
such as `url` or `command`); an **execution parameter** is a field on the
execution surface (how the node is allowed to run). `properties` is only the
JSON Schema keyword for rendering fields, not a concept. So "parameter" is a
subclass of "field", not a rival word, and "execution parameter" is precisely a
field on the execution surface.

The execution parameter categories (each independent; a node is set on each
separately). A named bundle of values across these execution parameters is an
**execution profile**, which is what a flavor pins and a node applies. A profile
is declared in config and **referenced by name** in the Wafer (a node names the
profile it uses), not spelled out inline, because an inline bundle becomes
unreadable once long. A node whose requested profile cannot be satisfied (the
host lacks a prerequisite such as unprivileged user namespaces or subuid
ranges) must **fail loudly at validation or submit**, never silently degrade to
a weaker configuration.

- **containment**: filesystem and process isolation. What the node can reach
  and trace on the box. Mount masking, user namespace, PID namespace, subuid
  mapping, seccomp, capability drop.
- **egress**: network reach, opt-in. When enabled, a node's outbound
  destinations must be **declared values**, not runtime data, and it is denied
  anything outside the declared allow-list. "Declared" means a literal in the
  Wafer or config, or a reference to a value declared in config (a connector
  endpoint, a config constant). A destination derived from runtime input
  (`{event}`, `steps`, a loop variable) is data, not declared, and is blocked.
  The point is not "we know the destinations and list them", it is that data
  cannot drive where a node connects: a hardcoded
  `curl https://api.github.com/...` passes, a `curl $URL` where `$URL` is
  runtime data is blocked. Disabled (the default) means unrestricted, matching
  how nodes behave today.
- **resources**: memory, cpu, pids, time. cgroup limits and timeout. A
  robustness dial (long-running or runaway-prone work), not a confidentiality
  one.
- **secrets**: how a secret reaches the node. Env (value handed to the
  subprocess, per ADR-0033) is the default. Proxy (value never reaches the
  node) is an optional, marginal mode, the credential-proxy idea, not a
  load-bearing part of this category; none is the case of no secrets. The
  credential-proxy idea lives in this category, not in containment.
- **identity**: the UID / subuid the node runs as and the capabilities it
  holds.
- **data flow** (already exists): capture and redaction of node output
  (ADR-0050).

Why "isolation levels" is the wrong frame: there is no single isolation ladder.
The credential-proxy never fit as a "level" because it is not a higher
containment tier, it is a different parameter (how secrets are delivered) that
also happens to reduce what a node can exfiltrate.

### The default rule: an execution parameter category is on by default only when it costs nothing and breaks nothing

Whether a category is a choice, or on by default, follows one rule: **a
category should be default-on if and only if it costs the user nothing to gain
the benefit and has no side effect that would make a legitimate node stop
working. If there is zero reason for it not to be on, it is not a choice, it is
just on.** A category that restricts what a node can reach, or that can break a
legitimate workload, or that depends on a host capability not every deployment
has, is a choice, because turning it on has a real cost.

Applying the rule to the categories:

- **On by default, not a choice:** `data flow` (capture and redaction, ADR-0050)
  costs nothing and breaks nothing, so it is always on, as it is today. The
  `secrets` env mode is likewise the working default, not a choice; it is how
  secrets reach a node at all.
- **A choice, because it restricts reach:** `containment` and `identity` change
  what a node can see and trace on the box, which can break a node that
  legitimately needs host access, and they need unprivileged user namespaces,
  subuid ranges, and cgroups that not every deployment has. `egress` restricts
  which destinations a node may reach, which can break a node that legitimately
  reaches arbitrary hosts, and its off-state is full network reach, the
  permissive default.
- **A choice, because it can break a workload:** `resources` imposes limits
  that can break a legitimate long-running or memory-heavy node, and a good
  default cap is workload-dependent, so it cannot be a universal default.

So the hardening categories are opt-in because every one of them has a nonzero
cost or side effect, and the costless ones are not choices at all. This is the
load-bearing rule for why any category is or is not a default.

### The researched baseline for containment (Linux-only)

Research (Sept 2026) settled the core containment question: containing a
subprocess that runs as the same UID as the runner is achievable, and the
honest ceiling is the granted secret and the kernel, not the node's access to
the box.

The earlier assumption ("a same-UID sandbox is not a privilege boundary") is
true only without a user namespace:

- **Without a user namespace**, a same-UID child is not containable against
  the runner. On stock kernels it can ptrace the runner and read its memory; on
  Ubuntu/Debian defaults the protection exists but is distribution-dependent,
  and it can still signal the runner and shares DAC file access.
- **With the node in its own user namespace**, cross-namespace ptrace and
  `/proc/<pid>/mem` access are denied unconditionally by the kernel, regardless
  of distro. This closes "trace the runner and read its memory" as a kernel
  property, at the same UID.
- **Mapping the node to a different host UID** (subuid mapping via
  `/etc/subuid` and `newuidmap`, the rootless-container mechanism) makes
  "cannot read the runner's files" true at the DAC/filesystem level, not by
  policy. This is the biggest single win and works unprivileged.

The concrete stack per contained node: a user namespace, a PID namespace (can't
see or signal the runner), a mount namespace with an empty root and read-only
binds of only what the node needs (fresh `/tmp` and `/proc`, no path to the run
DB, config, or secret material), a network namespace with only loopback so
egress is controlled per the egress section below, a seccomp deny-list
(including `io_uring_setup`, which executes work in kernel threads that never
make syscalls and so bypasses seccomp), an empty capability bounding set plus
`no_new_privs`, Landlock as a deny-by-default backstop, and a per-node cgroup.
Each layer closes a specific hole; none is optional if the goal is the full
reduction.

This composes cleanly with the secret model's TPM-unlock tier: containment
hides the TPM (and the rest of `/dev`) from nodes, which is correct, and the
runner still reaches it because secret resolution runs in the runner's own
process (per ADR-0033), never inside a node's containment. The two do not
conflict.

The runner spawns the node through a launcher (bwrap, nsjail, or `systemd-run`)
that performs the namespace and mount setup before exec; Go's `os/exec` handles
user namespaces and UID/GID mapping natively but not the mount tree or Landlock.
The launcher receives a small grants descriptor (paths, scratch dir, egress
allow-list, seccomp profile, subuid) and translates it into mounts and rules, so
the node never sees the mechanism. The existing data channels are unchanged:
filtered env and stdin/stdout/stderr work across every boundary, and per-node
secret delivery (ADR-0033) composes unchanged.

### How egress enforcement works (the shape that works)

When egress control is enabled, the mechanism that works depends on who owns
the client, and Servitor can use the simplest one for each node kind:

- **Servitor owns the client** (`http`, `mcp-http`, `email_received`): the
  destination is already a declared value in the node (`http` carries the URL
  in argv, `mcp-http` looks it up from the declared connector, email has its
  IMAP host). The node checks its own destination against the allow-list before
  connecting. Hostname-exact, no proxy or syscall needed.
- **A cooperating third-party client** (Singer taps/targets, and any MCP
  server or shell tool that honors `ALL_PROXY`/`http_proxy`): route it through
  an application proxy that sees the hostname in the handshake and checks it
  against the allow-list. Hostname semantics, proxy resolves per connection.
- **A non-cooperating client** (an MCP server or shell tool that ignores proxy
  configuration and opens its own TCP): enforce at the syscall level using
  **seccomp-unotify** (`SECCOMP_RET_USER_NOTIF`, kernel 4.14+, with fd injection
  via `SECCOMP_IOCTL_NOTIF_ADDFD` since 5.9). A filter returns USER_NOTIF on
  `connect()`, the supervisor (Servitor) reads the destination address from the
  syscall arguments, checks it against the allow-list, and either denies it or
  performs the connect and injects the connected fd back into the node. This
  works for any binary whether or not it cooperates. Because it intercepts
  after resolution, it sees an IP, which is why the controlled-DNS attribution
  above is needed for hostname semantics. Treat seccomp-unotify as the
  enforcement layer over the netns, not the boundary itself; the kernel docs
  warn it must not be relied on as the sole security mechanism (its argument
  inspection is TOCTOU-racy if done carelessly).

The allow-list is declared at three levels that compose through the lock model
(see the lock-model idea):

- **Config level, on a mechanism or flavor** (operator): the deployment's
  policy, the answer to "this shell may reach only these domains". It survives
  across Wafers.
- **Config level, on a connector** (operator): a declared connector (an MCP
  server, a Singer tap or target, as the spec defines) carries its own outbound
  scope beside its command and env, the answer to "this Stripe tap may reach
  only its own API". Without this, a mechanism using any connector would have
  to allow the union of every installed connector's hosts, giving a node
  access to hosts it should not reach.
- **Wafer level** (author): on the node itself. This is the per-run
  declaration, the specific destination a node needs (an `http` node already
  carries its `url`; a shell node declares the domains its command needs).

The lock value decides which governs, per the lock model. When egress is
**config-locked**, the config list is authoritative and the Wafer cannot
override it; the node runs within that policy and its own destination must fall
inside it. When **config-default**, the config sets the default but the Wafer
may narrow or extend it per node. When **wafer-set**, the config does not
constrain it and the Wafer's declaration governs. The egress on/off and the
static allow-list follow the egress rules already defined; both are settable at
each level. A reviewed shell script's destinations are knowable (the script is
reviewed), so egress control can stay meaningful for shell.

For `mcp-stdio`, the server is a **local subprocess** that Servitor spawns and
controls (its command, env, and stdio), and in Servitor's model each declared
server is a bounded integration with a known destination, one API per server
like a Singer tap. So the stdio server itself is local and its process is
Servitor's to constrain, but the APIs its tools reach are remote, and the
`mcp-http` mechanism connects to a remote server outright. Either way the
destination it may reach is declarable per declared server, exactly like a
Singer tap's endpoint. The one caveat is a
server whose tool fetches a URL supplied as an argument, which makes the
destination data-derived and therefore subject to the same egress restriction
as any data-driven destination.

#### Hostname semantics versus IP: what the allow-list actually checks

The allow-list is written as hostnames, but for the non-cooperating syscall
path the check happens after the program has resolved the name, so it sees an
IP, not the hostname. Enforcing hostnames there requires controlling the
program's resolution: route its DNS through a resolver Servitor observes,
record each hostname it resolves, and attribute each connect IP back to the
hostname that was allowed, denying any IP with no observed allowed resolution.
Do not pin a fixed set of IPs: for any CDN or load-balanced destination the IPs
rotate continuously (often on sub-minute TTLs), so a startup-pinned set becomes
wrong within minutes and then silently blocks legitimate connections. Pinning
is the trap to avoid; observed-resolution attribution is the working path.
Even the observed-resolution path is best-effort, not a hard boundary: a
compromised client or resolver can transiently point an allowed name at a
disallowed IP (DNS rebinding), so it should not be documented as a
cryptographic guarantee. The owned and cooperating paths see the hostname
directly and need none of this.

#### How a loopback-only netns node reaches the proxy (the transport)

A node in a network namespace with only loopback has no route out to a
host-side proxy, so the proxy cannot live on the host and be reached by
address. The transport that works is a **UNIX domain socket bind-mounted into
the node's mount namespace**: a filesystem-path UNIX socket is scoped by the
mount namespace, so bind-mounting the proxy's socket file into the node's view
makes it reachable even though the netns has only loopback. The node's only way
out is that one socket, and every connection through it reaches the proxy,
which checks the allow-list. The mount namespace and the netns work together:
the mount ns makes the proxy socket reachable, the netns guarantees there is no
other route. The socket must exist and be reachable at a path the node sees
before the node runs, and it must be accessible to the node's UID. The socket's
path must live in a Servitor-owned, non-world-writable directory (the runner's
state directory, never `/tmp`): a UNIX socket is a filesystem object, so its
protection is the directory's permissions, and a socket in a world-writable
directory like `/tmp` is attackable through that shared path. Bind it in the
runner's own directory with owner-only permissions, and only the node that is
explicitly granted it (via the mount namespace) can reach it. A pathname
socket file does not remove itself when the process exits: the runner must
unlink a stale socket file before binding (or at startup), or a crash followed
by a restart fails with `EADDRINUSE` even though nothing is listening. Abstract
sockets avoid the cleanup problem but are the wrong choice here: they are
scoped by the network namespace and have no filesystem access control, so
anything in the node's netns could reach them. A pathname socket gets its
access control from the filesystem for free; an abstract socket would force
per-connection `SO_PEERCRED` checks on the server side instead, which is more
code and easier to get wrong, and it still would not give the mount-namespace
scoping the transport relies on.

The socket is **`SOCK_STREAM`** over `AF_UNIX`. The node-to-proxy leg speaks a
stream protocol (SOCKS5 or HTTP CONNECT), which has its own framing, so a
byte-stream socket matches it; `SOCK_SEQPACKET` would only fit a message-
boundary-preserving protocol, which this is not, and `SOCK_DGRAM` is
connectionless and unreliable, wrong for a tunnel. Both ends must agree on the
socket type, or the kernel reports a confusing protocol-mismatch error.

### Lifecycle: one proxy, per-node socket, nothing for the user to configure

The egress proxy is a **single daemon-owned process**, started and owned by the
runner, used by every sandboxed node. It is not spun up per node: spawning a
proxy per node multiplies process count, socket setup, and connection state for
no benefit, and the allow-list logic is identical. Each sandboxed node gets its
**own socket path** (created in the runner's state directory, bind-mounted into
that node's mount namespace, and cleaned up when the node ends). A per-node
socket path is what lets the proxy know which node a connection belongs to and
apply that node's allow-list; a shared socket would force the proxy to
identify the node some other way, which is more machinery and more room for
error.

Operationally, the user does not configure any of this. The proxy, launcher,
namespaces, subuid, and cgroups are daemon-internal and default-off: a user who
does nothing gets today's behavior, and egress turns on only when they opt in
with a single allow-list. The only user-facing setup is the one-time host
prerequisites (unprivileged user namespaces, subuid ranges, a cgroup mount),
which are install-time, not per-workflow. There is no per-node or per-workflow
proxy or socket configuration to do.

The proxy is a **blind tunnel**: it reads only the destination from the
handshake and then bridges bytes, and it must never inspect, log, or filter
payloads. This is a load-bearing rule, not a style preference. The whole
secrets architecture keeps a granted secret inside its node's subprocess
(per-node env delivery, redaction, isolation); if the proxy started reading
payloads, it would become a new place where those secrets are visible outside
the node, undoing the isolation. And for TLS traffic (which carries most
secrets) the proxy cannot read payloads anyway without becoming a man-in-the-
middle CA, which must never be built. The proxy provides destination control,
not confidentiality: a node that is allowed to reach a host can send its
granted secret to that host, and the proxy will correctly let it through,
because the destination is allowed (the confused-deputy caveat in the honest
ceiling). No transport stops that; it is the node choosing to send a secret to
a permitted destination.

#### Where the check runs (the enforcement location)

The allow-list check physically runs in one of three places depending on the
client, and knowing which is the difference between correct and a silent gap:

- **Owned client**: the check runs in the Servitor process that makes the
  request (the `__http`/`__mcp_http` subprocess, or the worker before spawn),
  which compares the node's declared destination to the allow-list it was
  handed.
- **Cooperating client**: the check runs in the proxy, which reads the
  destination from the handshake.
- **Non-cooperating client**: the check runs in the seccomp-unotify supervisor,
  against the connect destination.

The allow-list must be delivered to wherever the check runs: to the owned
subprocess, to the proxy, or to the connect supervisor. If it is not handed to
the right place, the check does not happen and the allow-list is silently
inert, which is the implementation failure to avoid.

### The honest ceiling (what no combination stops)

- **A granted secret can still be exfiltrated.** A node holds its declared
  secrets in env and can copy them into its JSON result or, with any granted
  egress, send them to an allowed host. No sandbox stops this. The secrets
  category's proxy mode (the credential-proxy idea, keeping the value out of the
  node entirely) would close the node-copies-its-own-secret case, but it is a
  marginal, optional mode with its own secret-handling surface (see that
  idea); the accepted state is that a node holding a granted secret can
  exfiltrate it.
- **Kernel bugs.** All of this trusts the host kernel; a vulnerability in any
  allowed syscall or reachable driver is an escape to the runner's UID.
  Removing that residual needs gVisor or a VM, both out of proportion for
  per-node spawns today.
- **The allow-list's confused deputy.** An allowed host that is redirected or
  fronted can still receive a secret; the allow-list checks the destination,
  not the other end.
- The sandbox setup itself (launcher, mount recipe, mappings) is trusted code;
  a bug there silently downgrades the sandbox.

### Which mechanisms benefit from which execution parameter categories

The machinery sits at the shared subprocess-spawn boundary, so it wraps any
node at the same plumbing cost, but the value and cost are not uniform:

- **Host-containment** (mount masking, userns, PID ns, subuid, seccomp, caps
  drop) is worth as much as the executed code is untrusted. It matters most for
  `shell` and the third-party connectors (`mcp-stdio`, `singer-tap`/`target`,
  and the `email_received` fetcher): operator-installed or author-authored
  arbitrary executables. It adds little for Servitor's own compiled binary,
  where the threat is a bug in vetted code, not malice.
- **Egress control** is worth as much as the destinations are declared and
  static. Every node's destination is knowable once its content is known:
  `http`/`mcp-http`/`email` have it in the node, a Singer tap has its config
  endpoint, a bounded MCP server reaches one API, and a reviewed shell script
  reaches known domains. Where it gets its value is blocking data-driven
  destinations (a shell command that interpolates a URL from runtime data), so
  the allow-list is what stops random links going where they were not declared
  to go.
- **Per-mechanism verdict**: full treatment (containment plus egress) for
  `shell`, `mcp-stdio`, and `singer-tap`/`target`, where the executed code is
  not Servitor's own and egress is enforced via proxy or syscall; cheap
  exact-egress (destination checked in the node itself) for `http`, `mcp-http`,
  and `email_received`; skip `transform`, `switch`, and `foreach` (hold no
  secrets, no egress, and sit on the hot loop where per-spawn overhead is
  felt); not applicable to `wait`, `send-signal`, `rerun-failed`, and the
  triggers, which are worker-handled or in-daemon, not subprocess nodes.
- **Resource limits follow a different category.** They pay off for anything
  long-running or runaway-prone: `singer-tap` (streaming), `mcp-stdio` (can
  hang), `shell` (a command can loop), and the `email` poller. Short-lived
  `http` and compute nodes barely need them.
- The third-party connectors are declared in `servitor.config.yaml`
  (ADR-0018), so their egress allow-lists are a natural per-connector config
  field, and that composes with the flavors and disable ideas.

### Host requirements

- **Unprivileged user namespaces enabled.** The one real friction point: Ubuntu
  23.10+ and 24.04+ restrict them by default via AppArmor, which blocks the
  launcher's userns path for unconfined processes. The fix is an AppArmor
  profile for the Servitor daemon carrying the `userns` rule. This is the only
  recommended path, not one of two: it grants just the daemon the right to
  create namespaces and leaves the system-wide restriction in place for
  everything else. Do not use the `kernel.apparmor_restrict_unprivileged_userns=0`
  sysctl as the fix, it disables the restriction system-wide for every process
  on the box, weakening the whole host to benefit one service. The profile is a
  one-time, install-time setup, like the bwrap AppArmor profile a
  sandboxed-browser tool needs on the same distros: the operator sets it up
  once when deploying Servitor, and it applies to everything the daemon runs
  from then on. It is never per-node or per-workflow, and changing Wafers does
  not require touching it.
- For subuid mapping: `/etc/subuid` and `/etc/subgid` entries and the
  `newuidmap`/`newgidmap` setuid helpers.
- For cgroups: a cgroup v2 mount with delegated controllers.
- A kernel with userns, PID, mount, and net namespaces plus seccomp, and
  (optional) Landlock; distro kernels since roughly 2024 qualify.

### Build order (BSSN, each step independently valuable)

1. Mount namespace masking alone: the highest-value reduction for the least
   machinery, works on every kernel.
2. Add userns + PID + net namespaces: closes tracing, signals, process
   visibility, and raw egress.
3. Add egress control per the egress section: the owned-client check, the
   cooperating proxy path, and the seccomp-unotify path for non-cooperating
   clients.
4. Add the seccomp deny-list, capability drop, `no_new_privs`, and a per-node
   cgroup.
5. Move to subuid mapping for real DAC separation (host prerequisite: subuid
   ranges + newuidmap).
6. Landlock as a final deny-by-default layer once the others are proven.

## The lock model: who sets a parameter, config or Wafer

**Depends on:** nothing. It is a standalone generalized primitive; the
execution-surface categories are the execution parameters it governs, and the
flavors, egress, and disable ideas consume it.

Every mechanism parameter, on every execution-surface category, is governed by
one cross-cutting question: **who gets to set it?** The lock model answers
that. It applies to any parameter, on any category, in any mechanism, and it is
what makes a
flavor a real constraint rather than a wish, what makes egress policy
operator-enforceable, and what makes disable a hard off switch. It is a
generalized primitive in its own right, defined here once and referenced by the
flavors, egress, and disable ideas.

A parameter has one of three lock values, named for where it is set:

- **config-locked**: the config pins the value and the Wafer cannot override
  it. The security-hard case.
- **config-default**: the config sets a default and the Wafer may override
  it. The convenience case.
- **wafer-set**: the config does not constrain the parameter at all, no pin
  and no default, so the value comes from the Wafer. It is not that the config
  is forbidden from setting a value, it is that the config simply does not,
  which is what distinguishes it from config-default.

A parameter that is wafer-set and omitted by the Wafer is simply unset: an
optional parameter that is not set behaves as off or absent, with no hidden
default. There is no separate "built-in default" concept in the mechanism
metadata today, and none is introduced here.

### The lock value is implicit in how a value is written, not a separate setting

There is no lock-mode field to remember to set. The lock value is derived from
whether and how a parameter appears in the config:

- **Not in the config at all** = wafer-set. Nothing constrains it, the Wafer
  decides.
- **In the config, as a plain value, not marked locked** = config-default.
  The config provides the starting value, the Wafer may override it. This is
  the common case and the least surprising: a user who just wants a sensible
  default writes the value and does nothing else.
- **In the config, marked locked** = config-locked. The config pins it, the
  Wafer cannot override. Locking is the deliberate extra step for a security
  constraint.

The lock is expressed per field, as a `locked: true` marker on that field in
the config. For example:

```yaml
shell:
  egress:
    allow: [github.com]
    locked: true
```

Here egress is config-locked to `[github.com]`. A plain value without
`locked: true` would be config-default, and an absent entry is wafer-set.

So the operator makes one decision per parameter they write: do they also mark
it locked? Writing a plain value is a default; writing it and locking it is a
hard constraint; not writing it at all leaves it to the Wafer. There is nothing
else to remember.

### The precedence rule

When a Wafer names a flavor (or sets a parameter on a node), the effective
value is decided by the lock on each parameter:

- config-locked: the config value governs, the Wafer's value is rejected at
  validation.
- config-default: the config value applies unless the Wafer overrides it.
- wafer-set: the Wafer's value applies; if the Wafer omits it, the parameter
  is unset.

One parameter is governed by exactly one of these; there is no layering of
locks within a single parameter.

## Mechanism flavors: config-declared synthetic capabilities

**Depends on:** the lock model idea (a flavor is a base mechanism plus pinned
parameters, and the pinning is exactly the lock model) and the execution
surface idea (a flavor can pin execution-surface categories, not just function
parameters).

A flavor is a config-declared, named, **synthetic** mechanism: it has no
mechanism folder of its own, it refers to a real base mechanism (which does
have a folder) and pins a subset of its parameters. It surfaces in `servitor
capabilities` like any other capability. It generalizes the existing
declared-connectors pattern (ADR-0018), where the config declares named MCP
servers and Singer taps and a Wafer names an instance, from command/url/env
parameters to any parameter, including the execution surface of the first idea.

The part that makes a flavor a real constraint rather than a wish is the lock
model (see the lock-model idea). A flavor pins each of its parameters with one
of the three lock values, and a flavor is a mix of them per parameter: "Shell,
scripts-only, hardened execution profile, timeout locked at 5m" is a flavor
where the function surface is partly config-locked (scripts-only), some
execution categories are locked (hardened), and the timeout is config-default.
The distinction between a security constraint and a convenience default is
exactly the lock value on each parameter.

### How a flavor is shaped

A flavor names a base mechanism and pins a subset of its parameters. It is one
level: it names a base mechanism, not another flavor, so there is no stacking
and no ambiguity about what the base is. Flavors live in their **own config
section** (for example `flavors:` in `servitor.config.yaml`), distinct from the
declared-connectors sections: a flavor is a synthetic capability, not an
installed connector, so it is not an extension of the connector sections,
though a flavor may reference a connector by name as one of its pinned
parameters.

A flavor **inherits** the things that define what it is, and **pins** the things
that configure it:

- Inherited from the base: its `Role` (trigger, action, or flow), its
  `MechanismGroup` (it appears under the base's group), its `SideEffect` and
  `Delivery` properties, and its `RunKind` (it runs the same harness). These
  are fixed by the base and cannot be changed by a flavor.
- Pinned by the flavor: the configurable parameters, on the function surface
  and the execution surface, each with a lock value.

A flavor is a **bundle of per-parameter config-level locks**, so no new
precedence rule is needed when a Wafer names a flavor: each parameter follows
its one lock value exactly as the lock model defines (config-locked governs and
the Wafer cannot override, config-default applies unless the Wafer overrides,
wafer-set is the Wafer's choice, and an omitted wafer-set parameter is unset).
There is no layering or combination on a single parameter, and the flavor does
not change how a parameter's lock is applied.

Its capability is the base's schema, with the pinned parameters shown at their
config values and marked with their lock state, and every unpinned parameter
exposed to the Wafer (config-default exposed but overrideable, wafer-set
exposed and free, config-locked fixed and not overrideable). It carries its
lock state so an agent can see, for each field, what it may set and what the
config has fixed, without a second lookup into the config file.

The representation in `capabilities` mirrors how config writes it (a deliberate
extension of what is otherwise a pure-schema view): a config-locked field shows
its value plus `locked: true` next to it in the schema, a config-default field
shows its value with no locked marker, and a wafer-set field is a plain settable
property. This is the same notation an agent uses to write config and Wafers,
so the flavor's entry reads naturally and the fixed-versus-free split is visible
inline. Base mechanism files stay pure schema; the flavor's per-type file is
where the pins show.

A flavor has its **own name** (the base mechanism's name with the configured
flavor name appended) and is a **distinct capability** from the base, with its
own availability: a base mechanism can be disabled while one of its flavors
stays enabled, and each flavor is independently disableable (see the disable
idea).

Concrete shell flavors:

- **Scripts-only shell.** A shell flavor whose function surface is
  config-locked to "call a named script from an operator-gated folder" instead
  of an arbitrary inline command. The config names the folder of allowed
  scripts; validation rejects an inline command and any script name that does
  not resolve inside the folder. The folder is a deploy artifact gated by the
  same reviewed PR pipeline as Wafers, so the trust boundary becomes "reviewed
  scripts" instead of "reviewed inline commands". It closes the data-to-code
  injection boundary for shell (THREATS.md): the Wafer names a script, the
  script consumes data as data, so runtime data can never become part of the
  code that runs.

  The script is **delivered to the node like a secret**, not read from a shared
  folder inside the sandbox. The node never gets access to the whole scripts
  folder; it receives exactly the one script it is told to run, delivered
  per-node the way a declared secret is delivered (through the filtered env /
  the node's grant), and the script lives in a runner-managed location the node
  cannot see. The mechanism executes that one script. This mirrors the secrets
  model's trust boundary, a node gets only its own declared script, nothing
  else, and it removes the "grant the scripts folder as a read-only path" from
  the containment picture entirely.

  Because the script is reviewed, its outbound destinations are known, so
  egress control stays meaningful for shell. A script stays plain shell, with
  no Servitor config embedded in it. A script receives its `{event, steps}`
  input on **stdin, the normal node contract**, not argv: a script is a node and
  gets node input the node way, and stdin is the channel the sandbox already
  preserves across its boundaries.
- **Sandboxed shell.** A shell flavor that pins the execution surface
  (containment, egress, resources, identity) rather than the function surface.
  Keeps shell's full power but runs it contained. This is what the researched
  containment stack enables as a usable capability.

Why this shape: Servitor already treats variants as separate mechanisms
(`hmac-webhook` vs `standard-webhook`, `mcp-stdio` vs `mcp-http`) whose type
name carries the variant. A flavor is the generalization of that: instead of a
compiled-in type per combination, the config declares the combination. It fits
the declared-config philosophy (the box advertises what it supports, per
deployment) and avoids a compiled-in type per variant. The honest caveat: the
sandbox changes the execution harness, a category the declared-connectors
pattern
never carried, so the flavor framework is a real new concept, not a mechanical
extension. BSSN says do not build the generalized framework until it has real
users; there are already two candidate flavors (scripts-only, sandboxed), and
the disable idea is the same "mechanism configuration" family.

## Disable mechanisms per deployment (a config-level off switch)

**Depends on:** the flavors idea (a flavor is a distinct capability that can
be disabled independently of its base mechanism).

The mechanism registry is the compiled-in set (ADR-0045, ADR-0048): every
capability a deployment can use is there unless its folder is deleted, which
means a rebuild and a permanent fork. This idea gives the operator the
per-deployment alternative: disable any mechanism in `servitor.config.yaml`,
so a deployment can, for example, turn off `core/shell` and make that
capability impossible to use on this Servitor without touching the binary.

Disable is **not** a lock on a fake "availability parameter": it is a
config-level decision that a capability does not exist on this deployment. A
disabled capability is impossible to use: validation rejects any Wafer that
uses it, its run handler is unreachable, and `capabilities` surfaces it as
disabled.

Disable is **per capability, not per mechanism tree**. Each capability, the
base mechanism and each of its flavors, is independently disableable. A base
mechanism can be disabled while one of its flavors stays enabled, which is the
whole point of flavors: an operator who thinks "shell is dangerous, I want a
more constrained version" makes a flavor and disables the base, leaving the
flavor as the only shell available. Validation checks the specific capability
named in the Wafer, so `type: shell` fails while `type: shell-scripts-only`
passes.

How it would behave:

- The registry stays the compiled-in set; the config filters it at load. A
  disabled mechanism is still registered but marked disabled. Disable is a
  **blocklist**, not an allowlist: the operator disables the specific
  capabilities they do not want. An allowlist posture is expressible by
  disabling every capability not wanted; there is no separate allowlist mode.
- Disable applies **per capability**, and a mechanism group is disabled by
  disabling every capability in it (for example disable all of `webhook`).
  Group disablement matters so that a future mechanism added to a disabled
  group is not silently left enabled: disabling the group means everything in
  it, now and later, is off.
- `capabilities` reports a disabled capability explicitly (for example a
  `disabled: true` marker on the entry and in the index), rather than letting
  it vanish. This is critical because Servitor is agent-first: "this exists here
  but is off" is different information from "this server does not have it", and
  an agent that sees the capability listed can tell the user why a Wafer using
  it fails and point at the available alternative (for example "shell is
  disabled; use the scripts-only flavor, add your script to the folder").
- Validation rejects a Wafer that uses a disabled mechanism, at dry-run and at
  submit (and anywhere else a Wafer is validated), with a clear error naming
  the disabled mechanism and the config entry. A Wafer cannot be registered
  with a disabled node or trigger type. On top of validation, a disabled
  capability's run handler is also unreachable, defense in depth, so even if
  validation were bypassed the handler could not run.
- The declared config is loaded once at boot, so toggling a disable takes
  effect on **daemon restart**, consistent with the existing load-once-at-boot
  pattern and its change-detection gap (THREATS.md); no change-detection
  machinery is added now.
- The disabled state composes with the rest of the declared config: a webhook
  receiver whose mechanism is disabled, a declared MCP server or Singer tap
  whose mechanism is disabled, and a secret whose `source` mechanism is
  disabled (the secret-resolution group, ADR-0036). A dependency on a disabled
  mechanism **fails at config load** with a clear error, rather than failing
  later at use: a broken deployment is caught early, at load, not at run time.

Why it is attractive: it makes the trust boundary operator-declarable. The
reviewed-PR pipeline vouches for a Wafer's content; this lets the operator
decide which primitives are even available on the box. For a deployment that
never wants `shell`, "disable it" beats "be careful" and beats a fork, and the
flavor version means "replace it with a constrained one" is available too.

Why it is separate: it is a governance surface that touches the config schema,
the registry load, validation, and the capabilities output shape. It is a real
decision with alternatives (disable vs delete, blocklist vs allowlist, mark
disabled vs vanish), so it earns an ADR before building, plus tests that pin
the validation and capabilities behavior.

## (Add more ideas here as they come up; delete them when they become ADRs or
## are discarded.)
