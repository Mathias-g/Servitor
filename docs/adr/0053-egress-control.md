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

Egress is one category of the execution surface (ADR-0052). It is large and
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

**Enforcement paths, keyed on who owns the client**:

- Servitor owns the client (`http`, `mcp-http`, `email_received`): the
  destination is already a declared value in the node. The node checks its own
  destination against the allow-list before connecting. Hostname-exact, no
  proxy or syscall needed.
- A cooperating third-party client (Singer taps and targets, and any MCP server
  or shell tool that honors `ALL_PROXY`/`http_proxy`): route it through an
  application proxy that sees the hostname in the handshake and checks it
  against the allow-list. Hostname semantics, the proxy resolves per
  connection.
- A non-cooperating client (an MCP server or shell tool that ignores proxy
  configuration and opens its own TCP): enforce at the syscall level using
  seccomp-unotify (`SECCOMP_RET_USER_NOTIF`, kernel 4.14+, with fd injection
  via `SECCOMP_IOCTL_NOTIF_ADDFD` since 5.9). A filter returns USER_NOTIF on
  `connect()`, the supervisor reads the destination address from the syscall
  arguments, checks it against the allow-list, and either denies it or performs
  the connect and injects the connected fd back into the node. Because it
  intercepts after resolution, it sees an IP, so hostname semantics require
  observed-DNS attribution. Treat seccomp-unotify as the enforcement layer over
  the netns, not the boundary itself; its argument inspection is TOCTOU-racy if
  done carelessly.

**Hostname semantics versus IP**. The allow-list is written as hostnames, but
for the non-cooperating syscall path the check happens after the program has
resolved the name, so it sees an IP. Enforcing hostnames there requires
controlling the program's resolution: route its DNS through a resolver Servitor
observes, record each hostname it resolves, and attribute each connect IP back
to the hostname that was allowed, denying any IP with no observed allowed
resolution. Do not pin a fixed set of IPs. Even the observed-resolution path is
best-effort, not a hard boundary: a compromised client or resolver can
transiently point an allowed name at a disallowed IP (DNS rebinding), so it
should not be documented as a cryptographic guarantee. The owned and cooperating
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

**Where the check runs**. The allow-list check runs in one of three places
depending on the client: the Servitor process that makes the request (owned
client), the proxy (cooperating client), or the seccomp-unotify supervisor
(non-cooperating client). The allow-list must be delivered to wherever the check
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

**Per-mechanism mapping**: cheap exact-egress for `http`, `mcp-http`, and
`email_received` (destination checked in the node itself); full treatment for
`shell`, `mcp-stdio`, and `singer-tap`/`target` (enforced via proxy or syscall).
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
- Bad: the non-cooperating path is best-effort, not a hard boundary (DNS
  rebinding), and the observed-DNS attribution is real engineering.
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

Adds to the declared config (`servitor.config.yaml`): an egress allow-list on a
mechanism or flavor, and on a declared connector beside its command and env,
each governed by the lock model (ADR-0051). Adds to the Wafer: an egress
declaration on a node. Adds to the execution surface (ADR-0052): the `egress`
category, opt-in, default-off. The capabilities output surfaces a connector's
declared egress scope. A destination derived from runtime data is rejected when
egress control is enabled.

## More information

- ADR-0051 (the lock model, which governs the egress declaration levels)
- ADR-0052 (the execution surface, of which egress is one category)
- ADR-0033 (per-node secret delivery, which the blind-tunnel rule protects)
- SPEC: Egress control
