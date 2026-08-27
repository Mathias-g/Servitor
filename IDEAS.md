# Ideas

Catch-all for promising directions that are not yet decided or built. These are possibilities, not commitments: nothing here is a plan or a decision, and most of it will be discarded. When an idea becomes a real decision it gets an ADR and moves into the SPEC/PLAN; until then it lives here so it is not lost.

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

## A stronger secrets model (secret resolution: provider + per-node delivery)

An aspirational vision for how Servitor handles secrets, for when we want to move past the current model. Not a plan or a decision; the trade-offs are real, and we are deliberately recording the shape before committing to it.

### Why the current model is not enough

The current model: `varlock run` resolves the whole secret set into the daemon at boot, and `FilteredEnv` hands each node subprocess only the secrets it declared (exec package). This is good: nodes are scoped to their declared secrets, subprocesses are isolated (ADR-0008), and output is redacted (ADR-0029). But the long-lived daemon process holds every secret in memory for its whole life. A compromised daemon, a core dump, or a memory read exposes the full set. That is not a varlock defect to fix; the daemon legitimately needs values to run nodes. The gap is that it holds *all* of them, for its whole life, from a mechanism that is also too slow to do better (the varlock Node CLI boots in ~290ms per invocation, making per-node resolution infeasible).

The peers (n8n, Activepieces, Windmill) do worse: they encrypt credentials in their own DB with a symmetric key kept in the same runtime env as the ciphertext, and they resolve-and-hold all credentials at the worker level. Servitor should aim well above this, and this vision is that higher bar.

### The organizing principle

Two decisions, deliberately different in how opinionated we are:

- **Generalize the source of secrets (the secret provider).** No one secret store fits everyone (Bitwarden, 1Password, Vault/OpenBao, AWS Secrets Manager, KMS, a local-TPM-sealed file, plain env). Make the store a narrow, pluggable provider behind a single interface, so a deployment keeps the secret store it already has. Do not build a universal plugin registry up front (ADR-0002); add providers as they are actually needed.
- **Be strict about delivery (the invariant).** Secrets are delivered per node, per subprocess, at the point of execution, scoped to exactly that node's declared names. The daemon holds nothing long-lived. This is a security invariant, not a preference, so it is not a per-provider option: it is the contract Servitor enforces regardless of which provider sits underneath.

This generalizes the thing that should vary (where secrets come from) and pins down the thing that should not (that a subprocess sees only its own secrets, at the moment it needs them, and nothing is held in the daemon).

Generalization is not a single axis; we agreed on several distinct levels, each pluggable so a deployment keeps what it already has:

- **The source of secrets** (which store/provider): Bitwarden, 1Password, Vault/OpenBao, AWS Secrets Manager, KMS, a local-TPM-sealed file, plain env. This is the provider interface (piece 1).
- **At-rest key custody** (where the decryption capability lives): off-box KMS, on-box TPM/vTPM, or a local key. Each is a supported way to protect secrets at rest, chosen per deployment (piece 3).
- **The three orthogonal axes** (ingress, storage, unlock): how material reaches the box, where it lives at rest, and how it is unlocked. Any arrangement of these is a valid mechanism under the `secret-resolution` group (capability-model mapping).
- **Store topology**: one store, or several used simultaneously with per-secret routing and optional failover (piece 1).

The per-node delivery invariant is the one thing *not* generalized; it is fixed for every source, custody, and mechanism.

### How this maps onto the capability model

A secret capability is a distinct **role**, `secret` (a capability that supplies secret material to nodes, not a node or trigger itself). The distinct ways Servitor obtains a secret value at runtime are **mechanisms** under a `secret-resolution` **mechanism group`.

It is tempting to list "the combinations" as a flat set, but the space is really three **orthogonal axes** that any specific arrangement combines:

- **Ingress** — how the secret material reaches the box: **push** (CI/CD delivers it during deploy) or **pull** (Servitor or a provider fetches it from an external store).
- **Storage** — where the value lives at rest: in an **external store**, as **on-box ciphertext**, or as plaintext in the **environment**.
- **Unlock** — how the plaintext value is obtained when used: a **store credential** (the store authenticates and returns plaintext, no on-box decryption), a **local key** or **on-box TPM/vTPM** (an on-box key decrypts or unseals on-box ciphertext), an **off-box KMS** call (a credential authorizes the remote KMS to decrypt on-box ciphertext with its non-exportable key), or **none** (already plaintext in the environment).

A few representative arrangements make this concrete:

- **Pull-based external store** — pull ingress, external storage, credential unlock. The store returns plaintext after authenticating; Servitor hands it straight to the node's subprocess (no on-box decryption). Intended for stores that are fast enough to pull from directly per node (for example AWS Secrets Manager).
- **Push-based on-box ciphertext** — push ingress, on-box ciphertext, unlock by local key, TPM/vTPM, or off-box KMS. CI/CD delivers the material during deploy; Servitor decrypts locally when a node needs it. The recommended option, with TPM/vTPM the preferred unlock.
- **Pull-based on-box ciphertext** — pull ingress, on-box ciphertext, unlock by local key / TPM/vTPM / off-box KMS. Intended for stores that are too slow to pull from directly per node (for example varlock or Bitwarden, which make slow network round-trips): fetch each value once into the on-box at-rest store, then resolve per node from the local copy so per-node latency stays low. Optionally delete the credential used to pull after the fetch is done.
- **Environment** — env storage, no unlock. A pragmatic testing/dev fallback, or for platforms that inject secrets as env vars; plaintext, so not the secure ideal.

Preference: the recommended option is **push-based on-box ciphertext with TPM/vTPM unlock** (strongest key custody, no store credentials or runtime store-dependency on the runner). The other arrangements are supported for their niches; ranking their relative security is left to design time, not settled here. Environment is a dev/testing fallback.

The group name carries "secret" so it cannot be mistaken for a general-purpose resolver of non-secret things.

### The pieces

1. **Secret-provider interface (the generalization).** A narrow contract, roughly `Resolve(ctx, nodeName, secretName) -> value`, with caching and expiry as provider properties, and failure semantics that distinguish the source being unreachable, the secret being missing, and the secret being stale/invalid. A provider encapsulates its own mechanism (its own on-box unlock: a local key, TPM/vTPM, or an off-box KMS call; or, for the pull arrangements, a store credential). The recommended provider resolves from the push-delivered on-box at-rest store; pull providers (direct from a fast store, or fetching into the on-box store from a slow one) are additional implementations for those arrangements. The Wafer's existing `secrets:` list already declares names, so the interface slots in where `varlock.ResolvedSecrets()` returns a map today. Multiple providers coexist (per-secret routing, with optional failover).

2. **Strict per-node / per-subprocess delivery and egress (the invariant).** The daemon resolves each secret at the moment its node runs, hands it to that one subprocess's filtered env, and holds nothing long-lived. A node's secret dies with its subprocess. The egress rule is the same invariant from the other side: a resolved secret may flow only to the declaring node's subprocess, or to an external provider for the purpose of authenticating, and must be eliminated after. The daemon holds nothing and the value cannot go anywhere else. This narrows the runtime window to almost nothing and directly closes the "compromised daemon holds the full set" gap. The mechanism must be in-process (a provider/Go SDK), not a per-node CLI, so it is fast (milliseconds) rather than the ~290ms varlock boot.

3. **At-rest protection (TPM when available, never plaintext).** Secrets must never be plaintext on disk. TPM is the primary tier and is more broadly available than it used to be: physical TPMs plus virtual TPMs on VPS providers (for example AWS NitroTPM, a free TPM 2.0 device with sealing and measured boot) make it the default on many of the machines Servitor targets. Still provider- and host-dependent, so keep a non-TPM fallback that also holds the line against plaintext: an off-box key (KMS / self-hosted OpenBao transit) or a strong local-key file. Aim above the peers, who put the key in the same env as the ciphertext.

   The key-custody nuance that makes this a real security difference: an **off-box or hardware-bound key is non-exportable** (a KMS key or TPM seal cannot be copied off), so a thief who steals the disk or a backup gets only ciphertext and cannot decrypt it anywhere else. That is the genuine win over the peers, who keep a *copyable* key in the same env as the ciphertext. But it is not a complete boundary: it does not protect the value in the runtime window (the plaintext is in Servitor's memory and the subprocess either way), and it does not stop code already running as Servitor's user from calling the decryption service or TPM to obtain the value on demand. So at-rest key custody protects against disk/backup theft, not against a compromised daemon.

4. **Resolve only what the Wafers use.** The provider is driven by the registered Wafers, so Servitor resolves exactly the union of secrets the workflows actually reference (node `secrets:` lists, trigger/webhook secrets). If the last workflow using `GITHUB_TOKEN` is removed, Servitor stops resolving it. Works naturally with per-node delivery: read only what a node needs when it needs it.

5. **Declared secrets config, discoverable by agents.** The secret sources a deployment uses (which mechanisms/stores, and which secret names exist) are declared by the operator through CI/CD, following the declared-integrations pattern (ADR-0018) rather than authored in Wafers. Each declared secret carries its **secret name**, source, the **account name** it belongs to (for example the gmail address or GitHub org), and the **permissions** the operator declared it is authorized for (a secret may have several, for example a gmail send token versus a gmail read token). `servitor capabilities` renders these into `secrets.yaml` (secret name + account name + permissions, never values), so an agent authoring a Wafer can discover exactly which secret names are available, which account each belongs to, and what each is for, and pick the right one (for example `GMAIL_SEND_TOKEN` for `billing@acme.com` vs for `support@acme.com`). In v1 the account name and permissions are **informational only**: they exist so the agent reaches for the right secret, and Servitor does not try to verify that an action node's operation matches a secret's declared permissions. The Wafer keeps naming secrets by secret name; the operator/CI owns which sources, account names, and permissions exist.

For example, an entry in the declared secrets config might look like:

```yaml
secrets:
  - secret name: GMAIL_SEND_TOKEN
    source: external-store
    account name: billing@acme.com
    permissions: [send]
  - secret name: GMAIL_READ_TOKEN
    source: external-store
    account name: support@acme.com
    permissions: [read]
```

`servitor capabilities` renders the same fields (name + account name + permissions, never the value) into `secrets.yaml` for the agent to read.

A note on **least-privilege credentials**: operators should provision per-integration restricted-scope tokens, short-lived and rotated, rather than one master token. Mostly store-provided (Bitwarden Secrets Manager scoped access tokens, 1Password service accounts, Vault policies). Cheap and always worth doing, but it is operator credential-hygiene advice, not a distinct mechanism of this model.

### How the pieces hold together

1 and 2 are the spine: a pluggable source behind a strict per-node delivery + egress invariant. 3 protects at rest; 4 keeps the resolved set minimal and honest with the Wafers; 5 makes the secret set discoverable by agents. Keeping varlock's non-security properties matters too: a schema-driven, agent-visible, file-as-artifact secret definition (names and shapes readable by agents, values never), and structured validation. Those are as much a reason to keep the varlock model as the security is, and they should survive the move off the Node CLI. The credential-proxy + sandbox runtime boundary is a separate, more ambitious idea (see its own entry), not part of this core model.

### What this means versus today

Compared to the current model, the daemon no longer holds the full secret set; each secret is resolved per node (cheaply, in-process) and dies with its subprocess. Compared to the peers, this is a strictly higher bar: they co-locate the key with the ciphertext and resolve-and-hold all credentials at the worker level.

### Open questions / tensions

- How a provider caches/rotates without broadening the runtime window or co-locating an off-box key.
- For 4: the union across all *registered* Wafers is a boot-time computation; following Wafers as they change drifts toward on-demand resolution.
- For 4: generate the schema from Wafers, or validate the authored schema against them and warn on unused/missing entries.
- Whether the daemon, being trusted Servitor code, still needs a provider that resolves in-process at all.
- Naming: the role is `secret` and the group is `secret-resolution`, but whether those exact names hold once the first real secret capability is built is not yet settled (they will be decided in an ADR when this leaves the idea stage).
- Whether a secret's declared permissions (5) are purely operator-authored or partly derived from the store (which can know a token's real scopes) is unsettled.
- Whether the declared secrets config (5) is a separate file or folds into the existing declared-integrations config (ADR-0018), and how it is managed via the CLI (`servitor mcp`/`tap`/`target` analog).

## Credential proxy + OS sandbox for nodes (a stronger runtime boundary)

A separate, more ambitious idea split out of the secrets model. The provider + per-node delivery model still hands each node its resolved secret value (narrowly, per node, eliminated after). This idea goes further: keep the real value out of the untrusted node process entirely, even while the node uses it.

Each node subprocess runs in a sandbox whose only egress is a credential proxy. The node holds a placeholder; the proxy swaps in the real value only on a TLS-verified connection to an allowlisted host, and scrubs it from responses. A prompt-injected or compromised node cannot leak what it never held. This is the only approach that directly closes node exfiltration of secrets it legitimately needs to *use*.

Why it is separate: it is not part of the secret-resolution spine (provider + per-node delivery). It is independently buildable, probably should not gate the core model, and has its own deep engineering and open questions. It needs a local HTTPS-interception proxy (Servitor's own, or an adopted broker such as varlock's preview proxy) plus sandboxing each node (the subprocess model of ADR-0008 is the natural base).

Open questions:

- The proxy only covers proxied HTTPS to public-CA hosts. What to do for `shell`, `singer-tap`/`singer-target`, and `mcp-call` (stdio) nodes, which do not speak proxied HTTPS: give them a sandbox without the wire-injection, or accept they hold real values?
- Whether the proxy is Servitor's own implementation or an adopted broker, and how it composes with per-node sandboxing.
- It is HTTP/1.1 + public-CA only, which constrains non-HTTP node types.

## Secret permission enforcement (beyond informational)

A separate idea that follows from the secrets model's v1 decision that a secret's declared permissions are **informational only** (they exist so an agent reaches for the right secret; Servitor does not verify a match). This idea is about whether Servitor could ever *enforce* that an action node's operation matches a secret's declared permissions.

Why it is hard: permission names are not standardized across services. GitHub's fine-grained permission matrix is very different from gmail's or Slack's, so a node's "needs permission X" only makes sense within a service context. Enforcing the match would require a per-service permission vocabulary that Servitor maintains and validates against, which is complex and may be impossible to do well. This is deliberately out of scope for v1.

## Native app for browsing runs and the like

A dedicated native app, called "Servitor Desktop", that a human can connect to a Servitor runner "somehow" to browse runs and things like that: run history, step outcomes, events, and the state of registered workflows. The connection mechanism is deliberately left undefined for now (it could be a loopback or remote adapter over the daemon control protocol, or some future transport).

Why this is attractive:

- Run inspection today is CLI-only (`servitor runs`, `servitor run <id>`). A GUI would make Servitor more approachable for operators who prefer a visual view of what the runner did.
- The daemon already exposes a control protocol, so a client is a natural consumer of an existing surface rather than a whole new backend.

Open questions to settle if this moves toward a decision:

- How the app connects to the runner. The control plane is deliberately loopback-only and operator-gated (ADR-0009), so the connection path is a real design decision, not a given.
- Whether the app is read-only (browse runs) or can also operate the runner (submit, enable, disable, trigger, cancel).
- The earlier "no MCP in v1" decision (ADR-0005) deferred a remote interface for agents; a native app is a different consumer and would need its own decision.

### Wafers as a diagram

A first-class feature of Servitor Desktop should be rendering Wafers as a visual diagram, the way people are used to seeing workflows on Zapier or similar, built on an existing open-source graph/diagram library rather than from scratch. A Wafer is already a dependency DAG (ADR-0023), so it maps naturally onto a node graph: triggers (the `on:` entries) at the top, steps as nodes, edges for `depends_on`, and the branch/loop structure for `switch` and `foreach`.

Two tiers, in order of priority:

1. **Inspect (primary).** Read a Wafer (from the connected runner's registered workflows, or a local file) and render it as an editable-layout diagram: nodes per step, edges for dependencies, and clear visuals for switch branches and foreach fan-out. This is read-only and the natural first cut, consistent with Servitor being agent-first (the artifact, not a builder, is the source of truth).
2. **Create (secondary).** Let a user assemble a Wafer in the diagram and save it back as YAML. Secondary because Servitor is agent-first: agents author Wafers via the CLI/skill, so the visual builder is a convenience for humans, not the primary authoring path. If built, it must round-trip losslessly to the Wafer YAML (the artifact stays authoritative; the diagram generates and edits that YAML, never a divergent database row).

The diagram-first direction leans on the same "the Wafer is the artifact" rule as the rest of Servitor (SPEC: The Wafer): the app renders and edits the YAML, it never becomes a second source of truth.

### Library: React Flow

Leaning toward **React Flow** (`@xyflow/react`, MIT) for the diagram, the same library n8n uses, over the other candidates researched (Cytoscape.js, AntV G6, vis-network, JointJS). Cytoscape.js + cytoscape-dagre was considered first as the lighter, zero-dependency, vanilla-JS fit for a Wails webview, but its demo is too bare bones for the richer node-editor experience wanted, and editing is a real (if secondary) goal. React Flow is the strongest node-based editor and handles both inspect and create well.

The cost: React Flow needs **React + a bundler** (for example Vite), so the frontend stops being a vanilla embedded page like Playwrap's launcher and becomes a small React app bundled into the Wails window. It has **no built-in auto-layout**; pair it with **dagre** (MIT) or elkjs for the automatic DAG layout (which is what read-only inspection relies on).

Revisit the choice when actually building: if it turns out read-only inspection is the whole need and editing is never wanted, Cytoscape.js's simplicity becomes attractive again. For now, with editing in mind, React Flow is the leaning.

Not buildable until the connection mechanism (or a local-file path for inspection) is defined. Until then this is just a promising direction, kept here so it is not lost.

## Suspended waits: durable wait between nodes (timer and signal)

A durable `wait` flow node so a run can pause mid-way and resume much later, without adopting a durable-execution/replay architecture. The motivation and boundary are compared against Temporal in the context of long-lived workflows; the short version is that Servitor already checkpoints data (each node's `{event, steps}` input and the pending counter), not a running program, so "months later" is just "a queued job with a persisted input" rather than replaying code from an event history.

### The shape

A `wait` node with two sources:

- `wait.timer`: resume after a duration or at a cron time. Honker's scheduler is already durable in the same SQLite file and survives restarts, so a one-shot "resume at T" job is the natural mechanism.
- `wait.signal`: resume when an external event arrives, via one of several reuse paths: a per-run resume webhook (existing webhook receiver + varlock secret), extending the `internal` trigger (which today only *starts* a run) to *resume* a parked run id, and/or `servitor resume <run-id> [payload]` for manual/human input.

### Why the code already supports it

The worker's model makes this small. A `NodeJob` is fully self-contained (worker.go), the completion signal is just a `pending` counter reaching zero (honker runs.go, `checkRunComplete` in worker.go), and all writes go through one atomic `CommitNodeAtom` (honker tx.go). So:

- Processing a `wait` node parks the run in one transaction: write a `suspended_continuations` row holding the next node id + its `{event, steps}` input, set run status to a new `waiting`, and ack the wait job's claim.
- `checkRunComplete` must not complete a parked run, so its guard becomes `pending == 0 && status != waiting`.
- On resume: re-enqueue the continuation node (pending +1), flip status to `running`, delete the row. The run continues to completion normally.

This slots into the existing transactional discipline as a new kind of atom.

### What it would take to build

Roughly: one flow node type, one new table, one run status, a guard change, the resume wake-up path(s), and inspection surface (`waiting` shown in `servitor runs` / `servitor run <id>`; `cancel` also drops parked continuations). That is a phase-sized chunk, not an architectural rewrite.

### The one real decision it forces

**Wafer version drift.** A run parked for months, then the Wafer redeployed with changed nodes. The simplest honest answer, consistent with the self-contained-job model: freeze the continuation in the job payload at park time, so a parked run resumes with its original definition and new runs use the new wafer.

### The boundary that remains

Suspend is between nodes only, never inside arbitrary code. Compute still runs to completion in one subprocess and crashes re-run it (that is what `dedupe_key` is for). So "mid-computation, wait three days and resume with local variables intact" stays out of reach. But the common long-lived cases (timed holds, approval waits, saga compensation, external-callback waits between discrete steps) are expressible, because they are waits between steps, not inside a step.

Open questions:

- Whether the resume wake-up path is webhook, `internal` trigger, CLI, or some combination, and how the one-shot resume key is scoped.
- Whether `wait.timer` uses the existing Honker scheduler or a dedicated delayed-queue construct.
- How parked runs interact with graceful shutdown / drain (a parked run holds no live work, so it should be trivially drain-safe, but worth confirming).

Not a decision, not in scope yet; recorded here so the shape is not lost.

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

## (Add more ideas here as they come up; delete them when they become ADRs or
## are discarded.)
