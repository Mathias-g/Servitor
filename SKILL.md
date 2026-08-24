---
name: servitor
description: "Use this skill when the user wants to drive Servitor, a self-hosted workflow automation runtime for the agentic stack. Triggers include any mention of 'servitor', a Wafer, a workflow in this repo, submitting or dry-running a workflow, discovering capabilities, or wiring up automation. It teaches the agent to discover what a runner supports, author a Wafer (workflow YAML), dry-run it, and open a reviewed pull request for it."
---

# Servitor: author and operate workflows

Servitor is a self-hosted workflow automation runtime. Workflows are declared as
YAML files called **Wafers**. A long-lived **runner daemon** executes them
durably; a CLI control plane exposes the runner for humans and agents alike. You
drive Servitor entirely through its CLI.

This skill teaches you the agent workflow: discover what the runner supports,
author a Wafer, dry-run it before touching anything, and ship it through a
reviewed pull request.

## The artifact: a Wafer

A Wafer is a YAML file with two parts: **triggers** (`on:`) that start the
workflow, and **steps** (`steps:`) that run once it does. The file is the only
place workflow state lives; there is no UI or database row to edit.

```yaml
name: notify_on_new_lead
on:
  - type: grist_webhook
    path: /hooks/leads
steps:
  - type: shell
    name: notify
    secrets: [SLACK_TOKEN]
    command: "curl -s -X POST -H \"Authorization: Bearer $SLACK_TOKEN\" https://api.slack.com/... "
```

- `name` is required and unique.
- Each step has a `type` (for example `shell`, `http`, `transform`, `branch`,
  `foreach`). A step may have a `name` for referencing from other steps and
  `depends_on` to order it after other steps.
- `secrets` lists the secret names this step needs; the runner passes *only*
  those to the step's subprocess (OS-level isolation).
- `dedupe_key` makes a side-effecting step run at most once per value. Add it to
  any step with external side effects; the validator warns if you omit it.

## The CLI

All commands are loopback to the runner and safe to run from an agent. Exit
codes: `0` ok, `1` operation failed, `2` usage error, `3` daemon not running.

| Command | Purpose |
|---|---|
| `servitor capabilities [dir]` | Write step/trigger schemas + examples to files |
| `servitor dry-run <wafer>` | Validate and resolve without running anything (`--json` for structured) |
| `servitor submit <wafer>` | Validate and register a workflow |
| `servitor update <wafer>` | Replace a registered workflow's definition |
| `servitor enable <name>` / `servitor disable <name>` | Toggle a workflow's triggers |
| `servitor trigger <name> [json]` | Fire a manual run with optional JSON inputs |
| `servitor run` | Boot the runner daemon (self-heals under varlock) |
| `servitor stop` | Drain and shut the daemon down |
| `servitor runs` | List run history |
| `servitor run <id>` | Inspect one run and its step outcomes |
| `servitor cancel <id>` | Stop an in-flight run |

`update` replaces a registered workflow's definition (submit without one first
errors, pointing you to submit).

## Agent workflow

### 1. Discover what the runner supports

Do not guess what a step or trigger type takes. Materialize the capability set
and read the schemas:

```
servitor capabilities ./capabilities
```

This writes, per integration, one file per step and trigger type containing its
JSON Schema and a derived example fragment, plus an `index.yaml` listing the
integrations. Read `./capabilities/index.yaml` to see what exists, then read the
schema for the specific type you need (for example
`./capabilities/core/shell.yaml`) to learn its required fields and a valid
example. The example is generated from the schema, so it cannot drift from it.

> A committed `capabilities/` directory is a materialized snapshot you can read
> from the repo without a running daemon (ADR-0009). If a runner is reachable,
> prefer running `capabilities` fresh, since the answer is per-server.

### 2. Author the Wafer

Write the Wafer as YAML, using the schemas and examples you read. Keep secrets
as names in `secrets:`, never inline values (they come from varlock, resolved at
the runner).

### 3. Dry-run before submitting

Always dry-run before submitting, and read the structured result:

```
servitor dry-run ./wf.yml
servitor dry-run --json ./wf.yml
```

The readable form shows the workflow, triggers, and the resolved run order (the
DAG). The `--json` form returns structured validation: `errors`, `warnings`
(each with a JSON Pointer `path` and a stable `code`), and the `dag`. Fix any
blocking errors; treat warnings (for example `missing_dedupe_key`) as things to
resolve for side-effecting steps. Nothing runs and nothing is persisted.

### 4. Ship through a reviewed pull request

A Wafer that changes behavior goes through the gated deploy path: open a pull
request containing the Wafer (and this file's changes), have it reviewed, and
let the pipeline apply it on the box with `servitor submit`/`update`/`enable`.
Do not submit directly to a remote runner unless you are on the box itself or
its local pipeline (ADR-0009).

Where the runner is local to you, `submit` registers it: `servitor submit
./wf.yml`, then `servitor enable <name>` to arm its triggers, and
`servitor trigger <name> '{"key":"value"}'` for a manual run.

## Secret handling

Secrets live in varlock, resolved into the runner's environment at boot (the
runner re-execs itself under `varlock run`). A step sees only what it declares in
`secrets:`. Never put a real secret value in a Wafer or in this conversation; if
you need to check a step's env, the step itself reads it from its declared
secrets.
