---
status: proposed
date: 2026-09-05
decision-makers: [maintainer]
consulted: []
informed: []
scope:
  - runner
  - config
  - capabilities
interface-impact: new
---

# ADR-0053: Egress control, opt-in destination allow-listing as a separate decision

## Context and problem statement

Egress is one execution parameter category of the execution surface (ADR-0052).
It is large and
self-contained enough to be its own decision. When enabled, a node's outbound
destinations must be declared values, not runtime data, and anything outside
the declared allow-list is denied. The purpose is not "we know the destinations
and list them", it is that data cannot drive where a node connects: a hardcoded
`curl https://api.github.com/...` passes, a `curl $URL` where `$URL` is runtime
data is blocked. Egress control is opt-in, the off-state is full network reach,
the permissive default. This decision fixes the semantics, the enforcement
paths, the transport, and the declaration levels.

## Decision drivers

- Data must not drive where a node connects (the data-to-egress injection
  boundary), so the destinations must be declared values, not runtime data.
- The allow-list must be declared at the config level (operator) and the Wafer
  level (author), composed through the lock model (ADR-0051).
- A per-connector scope is needed so a node using one connector does not get
  the union of every installed connector's hosts.
- Hostname semantics are required, because IP-pinning is broken for CDN and
  load-balanced destinations.
- The transport must not become a place secrets are exposed: the egress proxy
  is a blind tunnel.

## Considered options

- Option A: No egress control. Rejected: leaves data-driven connections to
  arbitrary hosts unconstrained.
- Option B: IP-pinning based enforcement. Rejected: for CDN and load-balanced
  destinations the IPs rotate continuously, often on sub-minute TTLs, so a
  startup-pinned set becomes wrong within minutes and silently blocks
  legitimate connections. Pinning is the trap to avoid.
- Option C (chosen): Declared allow-list with hostname semantics, enforced
  through the simplest path per client, with observed-DNS attribution for
  non-cooperating clients.

## Decision outcome

Chosen option: "Option C".

**Semantics of "declared"**. A destination is declared if it is a literal in
the Wafer or config, or a reference to a value declared in config (a connector
endpoint, a config constant). A destination derived from runtime input
(`{event}`, `steps`, a loop variable) is data, not declared, and is blocked.
Egress control is opt-in: disabled (the default) means unrestricted, matching
how nodes behave today.

**The egress mode**. Egress has a `mode`, declared at the same three levels as
the allow-list (config on a mechanism or flavor, config on a connector, Wafer on
a node) and governed by the same lock model. It selects how the allow-list is
enforced, with two values:

- **`default`** (the default when `mode` is omitted): the node's normal egress
  behavior. For a node whose network operation is built into Servitor (`http`,
  `mcp-http`, `email_received`), the destination is already a declared value in
  the node and the node checks it against the allow-list in-process,
  hostname-exact, no proxy. For a node that runs an external command (`shell`,
  `mcp-stdio`, `singer-tap`/`target`), the node reaches allowed destinations
  through an application proxy that reads the hostname from the handshake and
  checks it against the allow-list, hostname-exact, for tools that honor the
  proxy.
- **`fallback`**: use the network-boundary path instead of the default, for a
  node whose default behavior will not work (for example a `shell` tool that
  ignores the proxy and opens its own TCP). Setting `mode: fallback` is the
  whole act of opting in, and it works for any node type without breaking it.
  Because `mode` lives at the same three levels as `allow`, an operator can set
  `egress.mode: fallback` on a mechanism, flavor, or connector in config and it
  applies to the nodes that use it, with a Wafer node able to override unless
  the config locks it, exactly as `allow` behaves.

**The `fallback` mode uses a packet boundary**. In `fallback`, the node's
outbound traffic passes through a single packet boundary: every packet the node
emits must transit it, regardless of which syscall, protocol, or file descriptor
produced it (TCP, UDP, raw, `AF_PACKET`, inherited fds, io_uring). This is a
real, kernel-enforced, topological boundary, not a syscall interceptor, so there
is no path that bypasses it. We deliberately do not use seccomp-unotify for this
boundary: the kernel documents that seccomp-unotify cannot be used to implement
a security policy, because syscall interception leaves other egress paths
untouched, whereas a packet boundary has none.

**No classification is required**. Servitor does not detect or label a node as
built-in or external-command, and the operator does not configure
which enforcement mechanism a node uses beyond the single `mode` field. The
`default` behavior is chosen by the node type. Neither the `default` nor the
`fallback` path is auto-detected: detection is not used because a non-cooperating
binary can only be recognized by running it and watching whether it honors the
proxy, but by then it has already made the connection it should not have, so
detection cannot be the enforcement. Setting `mode: fallback` is the explicit,
per-node choice that grants the packet-boundary path.

**Hostname semantics versus IP**. The allow-list is written as hostnames, but
for the `fallback` mode the check happens on resolved
packets, so it sees IPs, not hostnames. Enforcing hostnames there requires
controlling the node's resolution: route its DNS through a resolver Servitor
observes, record each hostname it resolves, and maintain the current IP set for
each allowed hostname at the packet boundary (refreshed on TTL, so CDN and
load-balanced destinations keep working), denying any IP with no observed
allowed resolution. Do not pin a fixed set of IPs. For this to hold, the node
must not control its own resolution: a `fallback` node's DNS is forced through
the Servitor-observed resolver by blocking or redirecting outbound DNS from the
node (port 53), so a node speaking to an attacker-chosen resolver, a hardcoded
resolver IP, or DoH cannot make the boundary see an "observed allowed
resolution" for a destination it was not allowed. Even so the path is
best-effort, not a hard boundary: a compromised client or resolver can
transiently point an allowed name at a disallowed IP (DNS rebinding), so it
should not be documented as a cryptographic guarantee. The built-in and default
paths see the hostname directly and need none of this.

**Transport**. A node in a network namespace with only loopback has no route
out to a host-side proxy, so the proxy cannot be reached by address. The
transport that works is a UNIX domain socket bind-mounted into the node's mount
namespace: a filesystem-path UNIX socket is scoped by the mount namespace, so
bind-mounting the proxy's socket file into the node's view makes it reachable
even though the netns has only loopback. The node's only way out is that one
socket. The socket's path must live in a Servitor-owned, non-world-writable
directory (the runner's state directory, never `/tmp`), because a UNIX socket
is a filesystem object and its protection is the directory's permissions. The
runner must unlink a stale socket file before binding, or a crash followed by a
restart fails with `EADDRINUSE`. Abstract sockets are the wrong choice: they are
scoped by the network namespace and have no filesystem access control. The
socket is `SOCK_STREAM` over `AF_UNIX`, because the node-to-proxy leg speaks a
stream protocol (SOCKS5 or HTTP CONNECT) with its own framing; both ends must
agree on the type.

**Lifecycle**. The egress proxy is a single daemon-owned process, started and
owned by the runner, used by every sandboxed node, not spun up per node. Each
sandboxed node gets its own socket path (created in the runner's state
directory, bind-mounted into that node's mount namespace, cleaned up when the
node ends). A per-node socket path is what lets the proxy know which node a
connection belongs to and apply that node's allow-list. Operationally the user
configures none of this: the proxy, launcher, namespaces, subuid, and cgroups
are daemon-internal and default-off, and egress turns on only when the user
opts in with a single allow-list. The only user-facing setup is the one-time
host prerequisites (ADR-0052).

**The proxy is a blind tunnel**. The egress proxy reads only the destination
from the handshake and then bridges bytes, and it must never inspect, log, or
filter payloads. This is a load-bearing rule. The whole secrets architecture
keeps a granted secret inside its node's subprocess; if the proxy started
reading payloads it would become a new place those secrets are visible outside
the node, undoing the isolation. For TLS traffic the proxy cannot read payloads
anyway without becoming a man-in-the-middle CA, which must never be built. The
proxy provides destination control, not confidentiality: a node that is allowed
to reach a host can send its granted secret to that host and the proxy lets it
through, because the destination is allowed. No transport stops that.

**Where the check runs**. The allow-list check runs in one of the places the
`mode` selects: the Servitor process that makes the request (a built-in node's
`default`), the proxy (an external-command node's `default`), or the packet
boundary (the `fallback` mode). The allow-list must be delivered to wherever the check
runs. If it is not handed to the right place, the check does not happen and the
allow-list is silently inert, which is the implementation failure to avoid.

**Declaration levels**, composed through the lock model (ADR-0051):

- Config level, on a mechanism or flavor (operator): the deployment's policy,
  the answer to "this shell may reach only these domains". It survives across
  Wafers.
- Config level, on a connector (operator): a declared connector (an MCP server,
  a Singer tap or target) carries its own outbound scope beside its command and
  env, the answer to "this Stripe tap may reach only its own API". Without this,
  a mechanism using any connector would have to allow the union of every
  installed connector's hosts.
- Wafer level (author): on the node itself, the per-run declaration, the
  specific destination a node needs.

The lock value decides which governs. When config-locked, the config list is
authoritative and the Wafer cannot override it. When config-default, the config
sets the default but the Wafer may narrow or extend it per node. When wafer-set,
the config does not constrain it and the Wafer's declaration governs.

**Per-mechanism mapping**: a built-in node (`http`, `mcp-http`, `email_received`)
checks its declared destination in the node itself (its `default`); an
external-command node (`shell`, `mcp-stdio`, `singer-tap`/`target`) uses the
proxy in its `default`, or the packet boundary in `fallback`.
For `mcp-stdio`, the server is a local subprocess Servitor spawns and controls,
and in Servitor's model each declared server is a bounded integration with one
API per server, so its destination is declarable per declared server. A reviewed
shell script's destinations are knowable because the script is reviewed, so
egress control stays meaningful for shell.

### Consequences

- Good: data cannot drive where a node connects, blocking data-to-egress
  injection.
- Good: per-connector scoping means a node using one connector does not reach
  every installed connector's hosts.
- Good: the blind-tunnel rule keeps the proxy from becoming a secret-exposure
  point.
- Good: the `fallback` mode is a real, kernel-enforced, topological
  boundary, not a syscall interceptor with
  un-intercepted paths. Its residual weaknesses are semantic (hostname-vs-IP
  and DNS rebinding), not structural (traffic escaping the gate).
- Bad: the `fallback` mode needs real IP networking in the
  node (a network boundary, routing, DNS), a change from the loopback-only model, and the
  observed-DNS attribution is real engineering.
- Bad: it needs the host prerequisites and the netns from ADR-0052, so it is
  not available where those are not.
- Neutral: egress is opt-in and default-off, so existing Wafers and deployments
  are unaffected.

### Confirmation

Tests pin the declared-versus-data rule (a runtime-derived destination is
rejected when egress control is on), the per-connector scoping (a node using one
connector cannot reach another connector's hosts), the lock precedence across
the three declaration levels, and the blind-tunnel rule (the proxy must not log
or inspect payloads). Enforcement-path behavior is verified as each path is
built. `go test ./...` stays green.

## Interface notes

Adds to the declared config (`servitor.config.yaml`): an egress allow-list and
an egress `mode` (`default`/`fallback`) on a mechanism or flavor, and on a
declared connector beside its command and env, each governed by the lock model
(ADR-0051). Adds to the Wafer: an egress declaration on a node. Adds to the
execution surface (ADR-0052): the `egress` execution parameter category,
opt-in, default-off. The capabilities output surfaces a connector's declared
egress scope. A destination derived from runtime data is rejected when egress
control is enabled.

## More information

- ADR-0051 (the lock model, which governs the egress declaration levels)
- ADR-0052 (the execution surface, of which egress is one execution parameter
  category)
- ADR-0033 (per-node secret delivery, which the blind-tunnel rule protects)
- SPEC: Egress control
