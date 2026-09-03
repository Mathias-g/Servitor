<img src="servitorLogo.png" alt="Servitor" width="400">

Workflow automation for the agentic stack. Self-hosted. MIT-licensed.

Servitor is a self-hosted headless workflow automation runtime designed from the ground up for AI agents to author and operate. Workflows are declared as YAML files (called **Wafers**); a long-lived runner daemon executes them durably; a CLI control plane exposes the whole thing for humans and agents alike.

The workflow is fully defined by the Wafer file, nowhere else. There is no built-in web UI; if a UI exists someday, it generates Wafers and submits them through the same interface agents use.

## Why it exists

If you have a stack of business tools (Grist for accounting and CRM, Atomic for knowledge, Slack for chat, GitHub for code, email somewhere else, plus the other things real companies use) you eventually want them to talk to each other. The existing options don't quite fit: hosted SaaS like Zapier locks your workflows and data into someone else's platform; n8n and friends are open-core, with the parts you eventually need behind an enterprise tier; Temporal targets application developers, not operators wiring up SaaS tools.

Servitor is an opinionated take: a small, code-first, agent-friendly workflow runner for connecting the tools a small company actually uses. One process, one SQLite file, genuinely open source (MIT, no open core, no enterprise tier).

## Why agent-first changes the design

Most workflow tools were designed for humans clicking through a builder, with an API bolted on. An agent using such a tool is a second-class citizen: it has to reverse-engineer what the UI assumes, guess at validation rules, and recover from opaque errors. Servitor is designed for agents first:

- **The artifact is the Wafer, not a database row.** A Wafer is the YAML file that defines a whole workflow: triggers (`triggers:`) that start the run and nodes (`nodes:`) that do the work. Every capability is a trigger, an action node (does work), or a flow node (routes or fans out). Agents read, write, diff, and version-control the same file a human would; there is no "form state" the agent can't see.
- **Capability discovery is a first-class operation.** `servitor capabilities` returns every capability (trigger, action node, or flow node, with its role and delivery), every declared secret, and every Singer tap available, each with its JSON Schema and an example rendered from that schema. An agent never has to guess what fields a node takes.
- **Validation errors are structured, not stringified.** Errors come back as JSON with paths, codes, and suggestions, so a typo like `type: slak` is flagged as `unknown_node_type` with `suggestion: slack`, the way an IDE would catch it.
- **Dry-run is a real primitive.** `servitor dry-run` resolves the entire workflow and shows the DAG the runner *would* execute, and warns when a declared secret is not resolvable from its source. Nothing runs, nothing is persisted.
- **The same CLI serves humans and agents.** No private API the agent doesn't have access to; if a future UI exists, it talks to the same control plane.

These are not nice-to-haves bolted on after the fact; they are why Servitor exists as a separate thing rather than a fork of an existing runner.

## How it works

A workflow is a YAML file (a Wafer) declaring triggers (what causes it to run) and nodes (what it does). The runner reads the Wafer, validates it, registers its triggers, and waits. When an event arrives, it enqueues a run, workers execute nodes, results are persisted, and downstream nodes fire as their dependencies complete.

```yaml
name: notify_on_new_lead
triggers:
  - type: hmac-webhook
    path: /hooks/grist-leads
nodes:
  - name: post_to_slack
    type: transform
    expression: |
      $body := $json($event.body);
      {"text": "New lead: " & $body.name}
```

Submit it, enable it, and the next time an HMAC-signed request hits
`/hooks/grist-leads` (a receiver you declare in `servitor.config.yaml`,
ADR-0049), a run fires and its `transform` parses the raw body.

## System requirements

Servitor is a single Go binary, but building and running it need a few things on
the box:

- **Linux or macOS** (cgo; see below). Windows is not currently supported.
- **Go** (the version in `go.mod`), `make`, and a **C compiler** (gcc/clang). The
  build uses cgo: the Honker SQLite extension requires the cgo `mattn/go-sqlite3`
  driver, so the binary is not fully static (ADR-0004).
- **A way to deliver secrets.** Secrets resolve per node through a pluggable
  provider; there is no boot-time secret dependency. The recommended mechanism
  is a push-based on-box ciphertext store, and a plain environment fallback and
  varlock as an optional pull source are also built in (SPEC: Secret
  resolution). See step 2 of Getting started.
- **The Honker SQLite extension** (`libhonker_ext.so`). It is not committed to
  the repo; you download a pinned, checksummed build (ADR-0011). See step 3 of
  Getting started.

## Getting started

Build it, download the Honker extension, declare your secrets, then run it.

1. **Build.** Requires Go (the version in `go.mod`), `make`, and a C compiler
   (for cgo):

       make build        # produces bin/servitor

   For a release build with a version stamped in, use
   `make release <new-version>` (for example `make release 0.2.0`), which bumps
   `VERSION`, rebuilds, and prints the git tag/push commands.

2. **Declare secrets.** Secrets are resolved per node through a pluggable
   provider (SPEC: Secret resolution): the Wafer names a secret, and the runner
   obtains it from that secret's declared source at the moment its node runs.
   Declare the secrets your connectors need:

       ./bin/servitor secret add SLACK_TOKEN onbox
       ./bin/servitor secret add GH_TOKEN env

   `servitor secret add <name> <source>` writes `servitor.config.yaml` in
   the working directory. Optional metadata (`--account`, `--permissions`,
   `--expiry`) shows up in `servitor capabilities`, so an agent can reach for
   the right secret. Three sources are built in:

   - `onbox` (recommended): push-based on-box ciphertext. Seal each value to
     the box with `servitor secret seal <name>`, reading it from stdin so it
     never sits in a file or your shell history:

         echo -n "xoxb-..." | ./bin/servitor secret seal SLACK_TOKEN

     The value is stored as ciphertext under `.servitor/secrets`, never
     plaintext on disk.
   - `env`: a plain environment fallback, for development and testing. The
     variable just needs to be set in the runner's environment.
   - `varlock`: an optional pull source backed by
     [varlock](https://varlock.dev); the runner fetches each value once and
     serves it per node.

   Run the runner from the same working directory these live in. Servitor
   itself assumes no particular store; it only resolves what the box declares.

3. **Download the Honker extension.** It is a loadable SQLite extension, pinned
   and checksummed (ADR-0011). The Linux x64 build for the version we pin:

       mkdir -p /opt/servitor
       curl -sL -o /tmp/honker-ext.tgz \
         https://github.com/russellromney/honker/releases/download/ext-v0.5.0/honker-ext-linux-x64.tar.gz
       (cd /opt/servitor && tar xzf /tmp/honker-ext.tgz)   # yields libhonker_ext.so

   Point the runner at it with `HONKER_EXTENSION_PATH`. (Other platforms: build
   or fetch the extension for your OS per the Honker docs.)

4. **Run the runner.** `servitor` boots the daemon directly, resolves secrets
   per node from their declared sources, and owns its SQLite file:

       HONKER_EXTENSION_PATH=/opt/servitor/libhonker_ext.so \
         ./bin/servitor run --db ./servitor.db --webhook-addr :8080

   `--db` is required: it enables the durable store, the worker/queue, and cron
   triggers. `--webhook-addr` enables the inbound webhook receiver; omit it if
   you only use `manual`/`cron` triggers.

5. **Author and submit a Wafer.**

       ./bin/servitor dry-run ./my-workflow.yml   # validate first
       ./bin/servitor submit ./my-workflow.yml    # register it
       ./bin/servitor enable <name>               # arm its triggers
       ./bin/servitor trigger <name>              # fire a manual run

Inspect results with `servitor runs` and `servitor run <id>`. `servitor update`
replaces a Wafer's definition, `servitor resume <signal-name>` resumes a run
parked at a `wait` node, `servitor rerun <run-id>` re-runs a failed run,
`servitor cancel <id>` stops an in-flight run, and `servitor stop` drains and
shuts the daemon down.

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

## The docs

- **SPEC.md**: the full product and behavior spec: what Servitor is, the control-plane (CLI) surface, the Wafer format, and how it works end to end. The source of truth for what to build and why.
- **STATUS.md**: what works today (the current-state snapshot), distinct from what is aspirational.
- **PLAN.md**: the implementation plan: build phases in order, dependencies, and what "done" means for each.
- **AGENTS.md**: how an agent (or developer) works in this repository: where context lives, the decision log, and the process.
- **docs/adr/**: the decision log. Each significant decision recorded as a numbered, immutable ADR.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for setup and the day-to-day loop, and [AGENTS.md](AGENTS.md) for how context is kept in the codebase.

## License

MIT.
