---
status: proposed
date: 2026-09-05
decision-makers: [maintainer]
consulted: []
informed: []
scope:
  - runner
  - capabilities
interface-impact: new
---

# ADR-0052: The execution surface, how a node is allowed to run, as a generalized primitive

## Context and problem statement

Every mechanism's node runs as a subprocess (ADR-0008), and how a node is
allowed to run, filesystem, network, resources, secrets, identity, and data
flow, is currently only partially implemented and not generalized. The runtime
implements a few pieces (the subprocess boundary, per-node secret delivery,
output capture and redaction), but each is its own mechanism-specific or
ad-hoc decision. Asking "what would it take to make the shell node safe"
revealed that the answer is not a shell feature. It is a runtime primitive: a
set of orthogonal, mechanism-independent parameters every node shares, so that
any current or future mechanism can be hardened without per-mechanism work.

## Decision drivers

- Any mechanism must be hardenable without per-mechanism code, because the
  enforcement machinery sits at the shared subprocess-spawn boundary.
- The word "isolation levels" is the wrong frame: the parameters are not a
  single ladder, they are independent execution parameter categories a node is
  set on separately.
- An execution parameter category is on by default only when it costs nothing
  and breaks nothing. One that restricts reach, or can break a legitimate
  workload, or depends on a host capability not every deployment has, is a
  choice.
- The honest ceiling must be stated, so the feature is not overpromised.

## Considered options

- Option A: Per-mechanism hardening (for example a shell-only sandbox).
  Rejected: every future mechanism re-derives the work, and hardening is not
  shared.
- Option B (chosen): A generalized execution surface: a set of execution
  parameters grouped into execution parameter categories, enforced by shared
  machinery at the spawn boundary, applied to any node through an execution
  profile.
- Option C: Nothing. Rejected: leaves nodes unconstrained beyond the current
  subprocess boundary.

## Decision outcome

Chosen option: "Option B".

A mechanism has **fields**, the existing term for a settable attribute (the
`Field` type in the registry, "fields" in the SPEC). A **function parameter** is
a field on the function surface (what the node does, such as `url` or
`command`); an **execution parameter** is a field on the execution surface (how
the node is allowed to run). "Parameter" is a subclass of "field", and
"execution parameter" is precisely a field on the execution surface. The JSON
Schema keyword `properties` is only the rendering of fields, not a concept.

A capability's **Role** is what kind of capability it is, trigger, action, or
flow; it determines where the capability can be used (under `triggers:` or
`nodes:`) and how it is treated. An **execution parameter category** is a
dimension of how a node is allowed to run; the execution parameter categories
are containment, egress, resources, secrets, identity, and data flow, and each
groups the execution parameters for that dimension. A Role describes the
capability itself; an execution parameter category describes the runtime of a
node of that capability.

The execution parameter categories are **independent groupings**: a node's
runtime configuration carries a value for each execution parameter, and setting
one execution parameter does not force or constrain the others. The execution
parameter categories are:

- **containment**: filesystem and process isolation. What the node can reach
  and trace on the box. Mount masking, user namespace, PID namespace, subuid
  mapping, seccomp, capability drop.
- **egress**: network reach, opt-in. A separate decision, ADR-0053.
- **resources**: memory, cpu, pids, time. cgroup limits and timeout. A
  robustness dial, not a confidentiality one.
- **secrets**: how a secret reaches the node. Env is the default, per-node
  delivery (ADR-0033). Proxy mode is an optional marginal mode (the
  credential-proxy idea), not load-bearing; none is the case of no secrets.
- **identity**: the UID / subuid the node runs as and the capabilities it
  holds.
- **data flow**: capture and redaction of node output, already exists
  (ADR-0050).

A named bundle of values across these execution parameters is an **execution
profile**. A profile is declared in config and referenced by name in the Wafer
(a node names the profile it uses), not spelled out inline, because an inline
bundle becomes unreadable once long. A node whose requested profile cannot be
satisfied (the host lacks a prerequisite such as unprivileged user namespaces
or subuid ranges) must fail loudly at submit (and at first run), when the
daemon checks the host, never silently
degrade to a weaker configuration. An unknown profile name is a Wafer-syntax
error, rejected at validation.

**The default rule**: an execution parameter is on by default if and only if it
costs the user nothing to gain its benefit and has no side effect that makes a
legitimate node stop working. If there is zero reason for it not to be on, it is
not a choice, it is just on. The rule is applied **per execution parameter, not
per execution parameter category**: a category only groups parameters, and
different parameters in the same category can have different defaults (for
example the `resources` memory cap and timeout are separate parameters, and the
`containment` mount masking and subuid mapping are separate parameters).

Applying the rule to the parameters: the `data flow` capture-and-redaction
parameter costs nothing and breaks nothing, so it is on by default, as it is
today, not a choice. The `secrets` env-delivery parameter is likewise the
working default, not a choice; it is how secrets reach a node at all. The
containment and identity parameters are choices: each changes what a node can
see and trace on the box, which can break a node that legitimately needs host
access, and each needs unprivileged user namespaces, subuid ranges, or cgroups
that not every deployment has. Each egress parameter is a choice: it restricts
which destinations a node may reach, which can break a node that legitimately
reaches arbitrary hosts, and its off-state is full network reach, the permissive
default. Each resource parameter is a choice: a limit can break a legitimate
long-running or memory-heavy node, and a good default cap is workload-dependent,
so it cannot be a universal default.

**The containment baseline (Linux-only)**. Research settled that containing a
subprocess running as the same UID as the runner is achievable, with the honest
ceiling being the granted secret and the kernel, not the node's access to the
box. The earlier assumption that a same-UID sandbox is not a privilege boundary
is true only without a user namespace. With the node in its own user namespace,
cross-namespace ptrace and `/proc/<pid>/mem` access are denied by the kernel
unconditionally, and mapping the node to a different host UID (subuid mapping
via `/etc/subuid` and `newuidmap`) makes "cannot read the runner's files" true
at the filesystem level.

The concrete stack per contained node: a user namespace, a PID namespace (can't
see or signal the runner), a mount namespace with an empty root and read-only
binds of only what the node needs (fresh `/tmp` and `/proc`, no path to the run
DB, config, or secret material), a network namespace with only loopback so
egress is controlled per ADR-0053, a seccomp deny-list (including
`io_uring_setup`, which runs work in kernel threads that bypass seccomp), an
empty capability bounding set plus `no_new_privs`, Landlock as a deny-by-default
backstop, and a per-node cgroup.

This composes cleanly with the secret model's TPM-unlock tier: containment hides
the TPM (and the rest of `/dev`) from nodes, which is correct, and the runner
still reaches it because secret resolution runs in the runner's own process
(ADR-0033), never inside a node's containment. The two do not conflict.

The runner spawns the node through a launcher (bwrap, nsjail, or `systemd-run`)
that performs the namespace and mount setup before exec; Go's `os/exec` handles
user namespaces and UID/GID mapping natively but not the mount tree or Landlock.
The launcher receives a small grants descriptor (paths, scratch dir, egress
allow-list, seccomp profile, subuid) and translates it into mounts and rules, so
the node never sees the mechanism. The existing data channels are unchanged:
filtered env and stdin/stdout/stderr work across every boundary, and per-node
secret delivery composes unchanged.

**Host requirements**: unprivileged user namespaces enabled. On Ubuntu 23.10+
and 24.04+ they are restricted by default via AppArmor, and the fix is an
AppArmor profile for the Servitor daemon carrying the `userns` rule. This is the
only recommended path: it grants just the daemon the right to create
namespaces and leaves the system-wide restriction in place. The
`kernel.apparmor_restrict_unprivileged_userns=0` sysctl is not used, it disables
the restriction system-wide. The profile is a one-time, install-time setup,
like the bwrap AppArmor profile a sandboxed-browser tool needs on the same
distros. For subuid mapping: `/etc/subuid` and `/etc/subgid` entries plus the
`newuidmap`/`newgidmap` setuid helpers. For cgroups: a cgroup v2 mount with
delegated controllers. A kernel with userns, PID, mount, and net namespaces
plus seccomp, and optional Landlock, since roughly 2024.

**The honest ceiling**, what no combination stops: a granted secret can still
be exfiltrated by the node that holds it, no sandbox stops that; the whole
stack trusts the host kernel, a vulnerability in an allowed syscall or driver
is an escape to the runner's UID, removing that needs gVisor or a VM; the
allow-list's confused deputy, an allowed host that is redirected or fronted can
still receive a secret; and the sandbox setup itself is trusted code, a bug
there silently downgrades the sandbox.

**Which mechanisms benefit**: host containment is worth as much as the executed
code is untrusted, most for `shell` and the third-party connectors
(`mcp-stdio`, `singer-tap`/`target`, the `email_received` fetcher), little for
Servitor's own compiled binary. Egress control is worth as much as the
destinations are declared and static (ADR-0053). Resource limits pay off for
long-running or runaway-prone work. `transform`, `switch`, and `foreach` are
skipped for containment and egress: they hold no secrets, need no egress, and
sit on the hot loop where per-spawn overhead is felt.

### Consequences

- Good: any current or future mechanism is hardenable through shared machinery
  at the spawn boundary, with no per-mechanism code.
- Good: the honest ceiling is stated, so the feature is not overpromised, and
  the default rule explains why each execution parameter category is or is not
  on by default.
- Bad: the full stack is Linux-only and needs one-time host prerequisites
  (unprivileged user namespaces with an AppArmor profile, subuid ranges, cgroup
  mount), which not every deployment has.
- Bad: per-node sandbox setup adds spawn overhead, which is why the pure-compute
  hot-loop nodes are left unhardened.
- Neutral: an execution profile is a new config object, and the node carries a
  profile reference.

### Confirmation

Tests pin the fail-loudly rule (an unknown profile name is rejected at
validation; a profile the host cannot satisfy fails at submit, never degrades),
the profile-by-name resolution, and the default rule (which execution parameters
are on by default). Containment behavior is verified per execution parameter
category (for
example the mount-masking and namespace layers each have tests asserting the
node cannot reach the runner's process, run DB, or secret material).
`go test ./...` stays green.

## Interface notes

Adds to the declared config (`servitor.config.yaml`): execution profiles, named
bundles of values across the execution parameters, referenced by name. A node in
a Wafer may name the profile it uses. The `egress` execution parameter category
is defined in ADR-0053. Host prerequisites (unprivileged user namespaces with an AppArmor
profile, subuid ranges, cgroup mount) are documented as one-time install-time
setup, not per-workflow configuration. An unknown profile name is rejected at
validation; a node requesting a profile the host cannot satisfy fails at submit
(and first run).

## More information

- ADR-0008 (every node runs as a subprocess; the boundary this surface extends)
- ADR-0033 (per-node secret delivery, the secrets execution parameter category's env mode)
- ADR-0050 (redaction, the data-flow execution parameter category)
- ADR-0053 (the egress execution parameter category)
- SPEC: Execution surface
