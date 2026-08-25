<img src="servitorLogo.png" alt="Servitor" width="400">

Workflow automation for the agentic stack. Self-hosted. MIT-licensed. X integrations.

Servitor is a self-hosted workflow automation runtime designed from the ground up for AI agents to author and operate. Workflows are declared as YAML files (called **Wafers**); a long-lived runner daemon executes them durably; a CLI control plane exposes the whole thing for humans and agents alike.

The workflow is fully defined by the Wafer file, nowhere else. There is no built-in web UI; if a UI exists someday, it generates Wafers and submits them through the same interface agents use.

## Why it exists

If you have a stack of business tools (Grist for accounting and CRM, Atomic for knowledge, Slack for chat, GitHub for code, email somewhere else, plus the other things real companies use) you eventually want them to talk to each other. The existing options don't quite fit: hosted SaaS like Zapier locks your workflows and data into someone else's platform; n8n and friends are open-core, with the parts you eventually need behind an enterprise tier; Temporal targets application developers, not operators wiring up SaaS tools.

Servitor is an opinionated take: a small, code-first, agent-friendly workflow runner for connecting the tools a small company actually uses. One process, one SQLite file, genuinely open source (MIT, no open core, no enterprise tier).

## Why agent-first changes the design

Most workflow tools were built for humans clicking through a builder, with an API bolted on. An agent using such a tool is a second-class citizen. Servitor is designed for agents first:

- **The artifact is the Wafer, not a database row.** Agents read, write, diff, and version-control the same file a human would.
- **Capability discovery is a first-class operation.** The CLI returns every step type, trigger type, declared secret, and available Singer tap, with full JSON Schemas. An agent never has to guess.
- **Validation errors are structured, not stringified.** Errors come back as JSON with paths, codes, and suggestions.
- **Dry-run is a real primitive.** It resolves the whole workflow, including secret references, and shows the DAG the runner *would* execute. Nothing runs, nothing is persisted.
- **The same CLI serves humans and agents.** No private API the agent doesn't have access to.

## How it works

A workflow is a YAML file (a Wafer) declaring triggers (what causes it to run) and steps (what it does). The runner reads the Wafer, validates it, registers its triggers, and waits. When an event arrives, it enqueues a run, workers execute steps, results are persisted, and downstream steps fire as their dependencies complete.

```yaml
name: notify_on_new_lead
on:
  grist_webhook:
    table: Leads
    event: row_added
steps:
  - name: post_to_slack
    type: slack
    action: post_message
    channel: "#sales"
    text: "New lead: ${trigger.payload.fields.name}"
```

Submit it, enable it, and the next time a row is added to your Grist `Leads` table, a Slack message goes out.

## Getting started

Servitor is a single Go binary. Build it, then run it; the runner re-execs
itself under varlock to resolve secrets and boots the daemon.

1. **Build.** Requires Go (the version in `go.mod`) and `make`:

       make build        # produces bin/servitor

   For a release build with a version stamped in, use
   `make release <new-version>` (for example `make release 0.2.0`), which bumps
   `VERSION`, rebuilds, and prints the git tag/push commands.

2. **Declare and populate secrets.** Write a `.env.schema` declaring the secrets
   your integrations need, and populate the values via
   [varlock](https://varlock.dev) from whichever backing store you use.

3. **Run the runner.** `servitor` re-execs itself under varlock to resolve
   secrets into its environment, then boots the daemon and owns its SQLite file:

       HONKER_EXTENSION_PATH=/path/to/libhonker_ext.so \
         ./bin/servitor run --db ./servitor.db --webhook-addr :8080

   `HONKER_EXTENSION_PATH` points at the Honker SQLite extension (a loadable
   `.so`; see [ADR-0011](docs/adr/0011-honker-go-and-pinned-extension.md)).
   `--webhook-addr` enables the inbound webhook receiver; omit it if you only
   use `manual`/`cron` triggers.

4. **Author and submit a Wafer.**

       ./bin/servitor dry-run ./my-workflow.yml   # validate first
       ./bin/servitor submit ./my-workflow.yml    # register it
       ./bin/servitor enable <name>               # arm its triggers
       ./bin/servitor trigger <name>              # fire a manual run

Inspect results with `servitor runs` and `servitor run <id>`, and stop the
daemon with `servitor stop`.

## Deploying a Wafer

Workflow changes are CI/CD-gated (ADR-0009, ADR-0019): an agent authors a Wafer
and submits it as a reviewed pull request; the apply happens on the box via the
CLI. Servitor ships the operations but not a deploy pipeline, because reaching
the box is your existing SSH/VPN access. In your own pipeline (or a cron Wafer),
run the apply on the box:

    ./bin/servitor dry-run ./my-workflow.yml   # validate against this box
    ./bin/servitor submit ./my-workflow.yml    # register / replace it
    ./bin/servitor enable <name>               # arm its triggers
    ./bin/servitor disable <name>              # disarm without deleting

The control plane stays loopback-only; only a process already on the box (your
pipeline or an operator) can reach it.

## Connecting your agent

Servitor is designed to be driven by your coding agent. It ships a skill (a `SKILL.md`) that teaches the agent the CLI, so agents consume it through the skill and the CLI rather than an MCP server. The daemon protocol is kept transport-agnostic so an MCP adapter could sit beside the CLI later without a rewrite.

## Building blocks

The runner composes well-defined, maintained pieces, each doing a narrow job:

- **Honker** (Rust, a SQLite extension): durable queue, scheduler, and NOTIFY/LISTEN semantics. One `.db` file is the whole system; no Redis.
- **Varlock** (JavaScript): typed, schema-validated secrets with runtime log redaction. The runner only ever sees resolved env vars.
- **Singer**: record-stream integration. Taps emit records from a source, targets consume them into a destination, with bookmark state.
- **Standard Webhooks**: modern webhook signing and verification, one library for any compliant producer.

## The docs

- **SPEC.md**: the full product and behavior spec: what Servitor is, the control-plane (CLI) surface, the Wafer format, and how it works end to end. The source of truth for what to build and why.
- **PLAN.md**: the implementation plan: build phases in order, dependencies, and what "done" means for each.
- **AGENTS.md**: how an agent (or developer) works in this repository: where context lives, the decision log, and the process.
- **docs/adr/**: the decision log. Each significant decision recorded as a numbered, immutable ADR.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for setup and the day-to-day loop, and [AGENTS.md](AGENTS.md) for how context is kept in the codebase.

## License

MIT.
