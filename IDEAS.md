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

## A stronger secrets model (secret resolution: provider + per-node delivery)

An aspirational vision for how Servitor handles secrets, for when we want to move past the current model. Not a plan or a decision; the trade-offs are real, and we are deliberately recording the shape before committing to it.

### Why the current model is not enough

The current model: `varlock run` resolves the whole secret set into the daemon at boot, and `FilteredEnv` hands each node subprocess only the secrets it declared (exec package). This is good: nodes are scoped to their declared secrets, subprocesses are isolated (ADR-0008), and output is redacted (ADR-0029). But the long-lived daemon process holds every secret in memory for its whole life. A compromised daemon, a core dump, or a memory read exposes the full set. That is not a varlock defect to fix; the daemon legitimately needs values to run nodes. The gap is that it holds *all* of them, for its whole life, from a mechanism that is also too slow to do better (the varlock Node CLI boots in ~290ms per invocation, making per-node resolution infeasible).

The peers (n8n, Activepieces, Windmill) do worse: they encrypt credentials in their own DB with a symmetric key kept in the same runtime env as the ciphertext, and they resolve-and-hold all credentials at the worker level. Servitor should aim well above this, and this vision is that higher bar.

### The organizing principle

Two decisions, deliberately different in how opinionated we are:

- **Generalize the source of secrets (the secret provider).** No one secret store fits everyone (Bitwarden, 1Password, Vault/OpenBao, AWS Secrets Manager, KMS, a local-TPM-sealed file, plain env). Make the store a narrow, pluggable provider behind a single interface, so a deployment keeps the secret store it already has. Do not build a universal plugin registry up front (ADR-0002); add providers as they are actually needed.
- **Be strict about delivery (the invariant).** Secrets are delivered per node, per subprocess, at the point of execution, scoped to exactly that node's declared names. The daemon does not hold a node's resolved secret past that node's subprocess: the value is gone once the subprocess it was handed to completes. This is a security invariant, not a preference, so it is not a per-provider option: it is the contract Servitor enforces regardless of which provider sits underneath.

This generalizes the thing that should vary (where secrets come from) and pins down the thing that should not (that a subprocess sees only its own secrets, at the moment it needs them, and nothing is held in the daemon).

Generalization happens at several distinct levels, each pluggable so a deployment keeps what it already has:

- **The source of secrets** (which store/provider): Bitwarden, 1Password, Vault/OpenBao, AWS Secrets Manager, KMS, a local-TPM-sealed file, plain env. This is the provider interface (piece 1).
- **At-rest key custody** (where the decryption capability lives): off-box KMS, on-box TPM/vTPM, or a local key. Each is a supported way to protect secrets at rest, chosen per deployment (piece 3).
- **The three orthogonal axes** (ingress, storage, unlock): how material reaches the box, where it lives at rest, and how it is unlocked. Any arrangement of these is a valid mechanism under the `secret-resolution` group (capability-model mapping).
- **Store topology**: one store, or several used simultaneously with per-secret routing and optional failover (piece 1).

The per-node delivery invariant is the one thing *not* generalized; it is fixed for every source, custody, and mechanism.

### How this maps onto the capability model

A secret capability is a distinct **role**, `secret` (a capability that supplies secret material to nodes, not a node or trigger itself). The distinct ways Servitor obtains a secret value at runtime are **mechanisms** under a `secret-resolution` **mechanism group**.

A secret-resolution **mechanism** is one concrete way Servitor obtains a secret value: it is an **arrangement** of the three axes, one option picked on each. The axes are composed *within* a mechanism, in code; they are not independently configured at runtime. A mechanism bundles them into a single, complete resolver.

- **Ingress** — how the secret material reaches the box: **push** (CI/CD delivers it during deploy) or **pull** (Servitor or a provider fetches it from an external store).
- **Storage** — where the value lives at rest: in an **external store**, as **on-box ciphertext**, or as plaintext in the **environment**.
- **Unlock** — how the plaintext value is obtained when used: a **store credential** (the store authenticates and returns plaintext, no on-box decryption), a **local key** or **on-box TPM/vTPM** (an on-box key decrypts or unseals on-box ciphertext), an **off-box KMS** call (a credential authorizes the remote KMS to decrypt on-box ciphertext with its non-exportable key), or **none** (already plaintext in the environment).

A few representative arrangements (each one a mechanism) make this concrete:

- **Pull-based external store** — pull ingress, external storage, credential unlock. The store returns plaintext after authenticating; Servitor hands it straight to the node's subprocess (no on-box decryption). Intended for stores that are fast enough to pull from directly per node (for example AWS Secrets Manager).
- **Push-based on-box ciphertext** — push ingress, on-box ciphertext, unlock by local key, TPM/vTPM, or off-box KMS. CI/CD delivers the material during deploy; Servitor decrypts locally when a node needs it. The recommended option, with TPM/vTPM the preferred unlock.
- **Pull-based on-box ciphertext** — pull ingress, on-box ciphertext, unlock by local key / TPM/vTPM / off-box KMS. Intended for stores that are too slow to pull from directly per node (for example varlock or Bitwarden, which make slow network round-trips): fetch each value once into the on-box at-rest store, then resolve per node from the local copy so per-node latency stays low. Optionally delete the credential used to pull after the fetch is done.
- **Environment** — env storage, no unlock. A pragmatic testing/dev fallback, or for platforms that inject secrets as env vars; plaintext, so not the secure ideal.

Preference: the recommended option is **push-based on-box ciphertext with TPM/vTPM unlock** (strongest key custody, no store credentials or runtime store-dependency on the runner). The other arrangements are supported for their niches; ranking their relative security is left to design time, not settled here. Environment is a dev/testing fallback.

The axis options are shared internal components, not per-mechanism reimplementations. A mechanism stays the deployable unit (one provider), but each mechanism calls the same components for the options it uses: one TPM unlock, one KMS call, one on-box ciphertext store, and so on, all in a shared internal library. This keeps the code DRY without making the axes runtime-pluggable seams (which would force maintaining every combination).

The group name carries "secret" so it cannot be mistaken for a general-purpose resolver of non-secret things.

### The pieces

1. **Secret-provider interface (the generalization).** A narrow contract, roughly `Resolve(ctx, nodeName, secretName) -> value`, with caching and expiry as provider properties, and failure semantics that distinguish the source being unreachable, the secret being missing, and the secret being stale/invalid. A provider encapsulates its own mechanism (its own on-box unlock: a local key, TPM/vTPM, or an off-box KMS call; or, for the pull arrangements, a store credential). The recommended provider resolves from the push-delivered on-box at-rest store; pull providers (direct from a fast store, or fetching into the on-box store from a slow one) are additional implementations for those arrangements. The Wafer's existing `secrets:` list already declares names, so the interface slots in where `varlock.ResolvedSecrets()` returns a map today. Multiple providers coexist (per-secret routing, with optional failover).

2. **Strict per-node / per-subprocess delivery and egress (the invariant).** The daemon resolves each secret at the moment its node runs, hands it to that one subprocess's filtered env, and does not hold it past that subprocess: the value is gone once the subprocess completes. A node's secret dies with its subprocess. The egress rule is the same invariant from the other side: a resolved secret may flow only to the declaring node's subprocess, or to an external provider for the purpose of authenticating, and must be eliminated after. The value cannot go anywhere else. This narrows the runtime window to almost nothing and directly closes the "compromised daemon holds the full set" gap. The mechanism must be in-process (a provider/Go SDK), not a per-node CLI, so it is fast (milliseconds) rather than the ~290ms varlock boot.

   One honest limit on this invariant: "gone once the subprocess completes" means the daemon no longer holds a reference and the value dies with the subprocess, not that the bytes are wiped from physical memory. Go strings are immutable and the garbage collector does not zero memory, so there is no way to force a secret's bytes out of RAM; they remain in the heap until the GC reclaims and reuses the region, and are not guaranteed zeroed. What matters for security is reachability: once the daemon drops its reference, no running code in the daemon can reach the value, and the subprocess that held it is gone. A fully memory-compromised process (a core dump or a read of the daemon's heap in that window) could still find stale bytes, but an attacker with that level of memory access can already read the next resolve in plaintext anyway, so this is defense-in-depth we cannot provide in Go, and it is out of scope for the model.

3. **At-rest protection (TPM when available, never plaintext).** Secrets must never be plaintext on disk. TPM is the primary tier and is more broadly available than it used to be: physical TPMs plus virtual TPMs on VPS providers (for example AWS NitroTPM, a free TPM 2.0 device with sealing and measured boot) make it the default on many of the machines Servitor targets. Still provider- and host-dependent, so keep a non-TPM fallback that also holds the line against plaintext: an off-box key (KMS / self-hosted OpenBao transit) or a strong local-key file. Aim above the peers, who put the key in the same env as the ciphertext.

   The key-custody nuance that makes this a real security difference: an **off-box or hardware-bound key is non-exportable** (a KMS key or TPM seal cannot be copied off), so a thief who steals the disk or a backup gets only ciphertext and cannot decrypt it anywhere else. That is the genuine win over the peers, who keep a *copyable* key in the same env as the ciphertext. But it is not a complete boundary: it does not protect the value in the runtime window (the plaintext is in Servitor's memory and the subprocess either way), and it does not stop code already running as Servitor's user from calling the decryption service or TPM to obtain the value on demand. So at-rest key custody protects against disk/backup theft, not against a compromised daemon.

4. **Resolve only what the Wafers use.** The provider is driven by the registered Wafers, so Servitor resolves exactly the union of secrets the workflows actually reference (node `secrets:` lists, trigger/webhook secrets). If the last workflow using `GITHUB_TOKEN` is removed, Servitor stops resolving it. Works naturally with per-node delivery: read only what a node needs when it needs it.

5. **Declared secrets config, discoverable by agents.** The secret sources a deployment uses (which mechanisms/stores, and which secret names exist) are declared by the operator through CI/CD, following the declared-integrations pattern (ADR-0018) rather than authored in Wafers. They live in the same declared-integrations config as a `secrets:` section (a `secrets:` entry is a different shape from an mcp/tap entry, but it is the same file and the same management-CLI pattern; splitting it into a separate file later is easy if it ever outgrows this one). Each declared secret carries its **secret name**, source, and optionally the **account name** it belongs to (for example the gmail address or GitHub org), the **permissions** the operator declared it is authorized for (a secret may have several, for example a gmail send token versus a gmail read token), and an **expiry** (when the value rotates or expires). Only the secret name and source are required; the account name, permissions, and expiry are all optional. `servitor capabilities` renders these into `secrets.yaml` (secret name + account name + permissions + expiry, never values), so an agent authoring a Wafer can discover exactly which secret names are available, which account each belongs to, what each is for, and when it expires, and pick the right one (for example `GMAIL_SEND_TOKEN` for `billing@acme.com` vs for `support@acme.com`). In v1 the account name, permissions, and expiry are **informational only**: they exist so the agent reaches for the right secret, and Servitor does not try to verify that an action node's operation matches a secret's declared permissions or that a secret is within its declared expiry. The Wafer keeps naming secrets by secret name; the operator/CI owns which sources, account names, permissions, and expiries exist. A `servitor secret add/list/remove` CLI manages this section, alongside `servitor mcp`/`tap`/`target`. The authored config is validated against the registered Wafers, warning on drift: a secret declared but used by no Wafer. The other direction is a hard error, not a warning: a secret referenced by a Wafer but not declared in the config refuses to submit or run the Wafer, because the run could never complete. A declared secret whose value is not present in the store at execution time is a separate case: it fails fast at the node that needs it (see "Secret invalidity and rotation").

For example, an entry in the declared secrets config might look like:

```yaml
secrets:
  - secret name: GMAIL_SEND_TOKEN
    source: external-store
    account name: billing@acme.com
    permissions: [send]
    expiry: 2026-09-30
  - secret name: GMAIL_READ_TOKEN
    source: external-store
    account name: support@acme.com
    permissions: [read]
```

`servitor capabilities` renders the same fields (name + account name + permissions + expiry, never the value) into `secrets.yaml` for the agent to read.

A note on **least-privilege credentials**: operators should provision per-integration restricted-scope tokens, short-lived and rotated, rather than one master token. Mostly store-provided (Bitwarden Secrets Manager scoped access tokens, 1Password service accounts, Vault policies). Cheap and always worth doing, but it is operator credential-hygiene advice, not a distinct mechanism of this model.

### How the pieces hold together

1 and 2 are the spine: a pluggable source behind a strict per-node delivery + egress invariant. 3 protects at rest; 4 keeps the resolved set minimal and honest with the Wafers; 5 makes the secret set discoverable by agents. Keeping varlock's non-security properties matters too: a schema-driven, agent-visible, file-as-artifact secret definition (names and shapes readable by agents, values never), and structured validation. Those are as much a reason to keep the varlock model as the security is, and they should survive the move off the Node CLI. The credential-proxy + sandbox runtime boundary is a separate, more ambitious idea (see its own entry), not part of this core model.

### What this means versus today

Compared to the current model, the daemon no longer holds the full secret set; each secret is resolved per node (cheaply, in-process) and dies with its subprocess. Compared to the peers, this is a strictly higher bar: they co-locate the key with the ciphertext and resolve-and-hold all credentials at the worker level.

This is also a demotion for varlock. Today varlock resolves the whole secret set into the daemon at boot, which is exactly the mechanism this idea replaces. Under the new model varlock is not on the default path at all (the recommended scheme is push-based on-box ciphertext, which skips it entirely); at most it survives as one optional slow pull origin, fetched once into the on-box at-rest store and then resolved per node from the local copy. That is a real architectural change to a system Servitor currently relies on, so it is a decision to be recorded in the ADR, not a silent byproduct.

### Secret invalidity and rotation

A secret can become invalid at any time, whether it is in active use or idle: it can expire, be revoked, or be rotated to a new value while nothing is running. The per-node delivery invariant makes fresh values free: each node resolves fresh and its value dies with its subprocess, so once a store holds a new value the next resolve picks it up. Two cases remain. A secret can go bad *while idle*: the next node that needs it fails at the moment of use, and a fresh resolve returns the new value once the store is updated. This only needs the resume-from-failure behavior below. The harder case is a long-lived holder (a persistent node connection such as a websocket, and the pull-provider credentials the daemon uses to authenticate out): a value is held across a connection's life, and a fresh resolve cannot reach an already-open connection, so the holder must actively react. Invalidity is handled reactively, on failure rather than on a schedule:

- A node whose auth fails (a 401/403, a dropped or rejected connection) fails and reports it to the daemon. The daemon respawns the node's subprocess with a freshly resolved secret, up to the configured retry count. Each retry is a new subprocess spawn with a new resolve; the failed subprocess's value dies with it (the invariant). If the store value rotated, a fresh resolve gets the new one and the retried request succeeds.

   This composes safely with `dedupe_key` only under a contract we must record: a node's secret-authenticating call is its first outbound call and fails before any side effect. If auth were hit only on a later call, a retry could redo a side effect the failed subprocess already caused, which is exactly what `dedupe_key` exists to guard against. So the contract is that the auth failure precedes any side effect; retries then never redo one, and a side-effecting node still declares `dedupe_key` as a belt-and-suspenders guard.
- Retries are bounded: a configured number of attempts before the node fails, so a genuinely bad secret does not loop forever. Initially this is a single global default in the servitor config (for example `secret_retry_count: 3`), applied to all nodes; a per-node or per-secret override can come later if it is ever needed. When retries are exhausted, the node fails with a distinct error, visible in `servitor run <id>` with the same structured `path`/`code` shape as other node errors (a code like `secret_auth_failed`, distinct from `missing_secret`), and written to the run's log. That failure is also emitted as an event a workflow can trigger on, reusing the `internal` trigger's completion-callback plumbing (ADR-0026), so the operator can wire up their own notification through whatever integration they choose: a Slack message, a text, an email, or anything else. The failed-secret event is not a hardcoded notification; it is just another event an agent can react to.

The three failure semantics a provider can return (piece 1) are not all the same, so they are not handled the same way:

- **Stale/invalid (auth fails on use)** is the case above: reactive retry with a fresh resolve, bounded. It is transient in the sense that a fresh resolve may get a rotated value, so it is worth retrying.
- **Source unreachable** (the store/provider is down) is a transient infrastructure failure. Retry with exponential backoff (for example 1s, 2s, 4s, capped) before failing, since the source may come back. If it stays down, fail with a distinct error.
- **Secret missing** (declared but no value in the store) is not transient, so retrying is pointless; fail fast, no retry, with a `missing_secret`-style error. The operator adds the value and resumes from the failed node.

The webhook receiver is not one of the provider failure semantics, because a webhook signing key is used only to verify inbound messages: it is never used to make an outgoing call, so the reactive "auth fails on use" mechanism does not apply to it. It is still a secret: a stolen key lets an attacker forge signed events and drive the workflows they trigger, so it gets the same at-rest protection as any other secret. It is also resolved per use like any other secret: the receiver resolves the current signing key fresh each time it verifies a message, and the value is held only for that one verification, not for the daemon's life. There is no rollover window (the receiver does not accept an old key during rotation): a message that does not verify with the current key is rejected and logged, and there is no retry, because an inbound webhook is sent once and a rejection is the receiver's only action. Rotation just means the store holds the new value and the next verification picks it up, exactly like a node secret. This removes the only extended-hold window in the model: the daemon's only life-long-held secrets are the pull-provider store/KMS credentials it needs to authenticate outbound.

This keeps the invariant intact: the model never proactively polls for rotation and never holds a value longer than it must. It only re-resolves when a failure makes a fresh value legitimate, which is exactly what the egress rule already permits.

Because Servitor does not pre-check that every node's auth will work before a run starts, a secret can be bad from the start of a run or go bad silently at any point, and it is not known until the DAG reaches the node that needs it. A run can therefore fail partway through its DAG with some nodes completed and others not. Supplying the new secret should resume the run from the failed node, not restart it from the top: restarting would re-run the already-completed nodes, redoing their side effects (and, for nodes without a `dedupe_key`, redoing them unsafely). Resuming from the failure point means the completed nodes are left as they are and only the failed node and its remaining successors run. This reuses the suspend/resume machinery already sketched for parked runs (see "Suspended waits": a continuation holds the next node's `{event, steps}` input, and resuming re-enqueues it), here triggered by a failed node being resupplied with a fresh secret rather than by a `wait` node. The failed node is the continuation point.

What "run it again" means is a configurable behavior, settable globally in the servitor config and per Wafer, with the CLI able to override it for a specific run. The modes:

- **continue**: resume from the failed node, leaving completed nodes and their side effects as they are. The default, and the safe choice for the secret-invalidity case.
- **restart**: re-run from the top. Redoes completed side effects, so it is only safe for a Wafer whose side-effecting nodes all declare a `dedupe_key`.
- **discard**: drop the failed run entirely and do not re-run it, cleaning up any partial state.

### Carried forward to the ADR / SPEC

When this idea becomes a real decision and turns into specification, settle the naming: the role is `secret` and the group is `secret-resolution`, but whether those exact names hold once the first real secret capability is built is not yet settled. They will be decided in the ADR that records the decision.

A few points were settled while shaping this entry and should carry into the ADR/SPEC unchanged:

- **Recoverability is not Servitor's problem.** The box holds only derived ciphertext; the origin (the store, or the material CI/CD pushes) lives elsewhere, so losing the box costs nothing durable, you just re-run setup. The non-exportable key protects only against the thief who steals the disk, not against a lost box.
- **Single server.** Servitor runs on one host, so per-host TPM binding is a non-issue: the ciphertext and the TPM that seals it share the box.
- **Provisioning is the operator's job.** Setting up TPM/KMS/vTPM is one-time host setup the operator owns, not workflow behavior. `servitor secret add` is management-only metadata (name, source, account, permissions, expiry); value delivery is CI/CD (push) or the store (pull), never a provider-agnostic local seal.
- **varlock is demoted.** It is not on the default path; at most an optional slow pull origin fetched once into the on-box store. See "What this means versus today".
- **Webhook key is per-use; no rollover.** The receiver resolves the current signing key fresh per verification. No old-key acceptance window (dropped as over-engineering); the only daemon-held secrets are pull-provider store/KMS credentials.
- **The zeroization limit.** "Gone after use" means no longer reachable by the daemon, not bytes wiped from RAM (Go cannot zero memory). See piece 2.
- **The auth-before-side-effect contract.** Retries compose with `dedupe_key` only because a node's auth call is its first outbound call. See "Secret invalidity and rotation".
- **Redaction composes with per-node delivery.** Redaction scrubs a granted secret value from a node's captured output by scanning that node's filtered env (exec package). Per-node delivery holds a value only while its node runs, which is exactly the window redaction needs, and redaction only ever scrubs values the node was granted. So the new model must keep redaction operating on the running node's filtered env, not on a global secret map, because under per-node delivery there is no global map to redact from. The verbatim-only limit (a transformed secret is not scrubbed) is an open attack surface recorded in THREATS.md, and belongs to the credential-proxy idea, not the core model.

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

A first-class feature of the app should be rendering Wafers as a visual diagram, the way people are used to seeing workflows on Zapier or similar, built on an existing open-source graph/diagram library rather than from scratch. A Wafer is already a dependency DAG (ADR-0023), so it maps naturally onto a node graph: triggers (the `on:` entries) at the top, steps as nodes, edges for `depends_on`, and the branch/loop structure for `switch` and `foreach`.

Two tiers, in order of priority:

1. **Inspect (primary).** Read a Wafer (from the published data of one of the Servitor instances the app tracks, or a local file) and render it as an editable-layout diagram: nodes per step, edges for dependencies, and clear visuals for switch branches and foreach fan-out. This is read-only and the natural first cut, consistent with Servitor being agent-first (the artifact, not a builder, is the source of truth). This tier works identically in a native or web delivery.
2. **Create (secondary).** Let a user assemble a Wafer in the diagram and save it back as YAML. Secondary because Servitor is agent-first: agents author Wafers via the CLI/skill, so the visual builder is a convenience for humans, not the primary authoring path. If built, it must round-trip losslessly to the Wafer YAML (the artifact stays authoritative; the diagram generates and edits that YAML, never a divergent database row). Because the read-only app consumes published data only, creating a Wafer needs a separate path to get it into a runner, which the diagram-first (inspect) tier deliberately does not have.

The diagram-first direction leans on the same "the Wafer is the artifact" rule as the rest of Servitor (SPEC: The Wafer): the app renders and edits the YAML, it never becomes a second source of truth.

### Library: React Flow

Leaning toward **React Flow** (`@xyflow/react`, MIT) for the diagram, the same library n8n uses, over the other candidates researched (Cytoscape.js, AntV G6, vis-network, JointJS). Cytoscape.js + cytoscape-dagre was considered first as the lighter, zero-dependency, vanilla-JS fit for an embedded page (such as a Wails webview), but its demo is too bare bones for the richer node-editor experience wanted, and editing is a real (if secondary) goal. React Flow is the strongest node-based editor and handles both inspect and create well.

The cost: React Flow needs **React + a bundler** (for example Vite), so the frontend is a small React app (hosted on its own server in the web case, or bundled into a Wails window in the optional native packaging). It has **no built-in auto-layout**; pair it with **dagre** (MIT) or elkjs for the automatic DAG layout (which is what read-only inspection relies on).

Revisit the choice when actually building: if it turns out read-only inspection is the whole need and editing is never wanted, Cytoscape.js's simplicity becomes attractive again. For now, with editing in mind, React Flow is the leaning.

Not buildable until Servitor actually publishes the data the app reads and the shape of that published data is defined. The dogfooding idea covers publishing capabilities, and the monitoring idea wants a "see all runs" view, but neither yet specifies a signed, redacted, external-readable feed of run history and outcomes for an app to consume; that feed is the missing prerequisite. Until then this is just a promising direction, kept here so it is not lost.

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

## (Add more ideas here as they come up; delete them when they become ADRs or
## are discarded.)
