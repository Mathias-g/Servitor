---
status: accepted
date: 2026-08-25
decision-makers: [Mathias]
consulted: []
informed: []
scope:
  - triggers
  - helper
interface-impact: new
---

# ADR-0027: email_received runs as a provider-agnostic poll via a helper subprocess

## Context and problem statement

The `email_received` trigger (SPEC: Triggers) fires a workflow for inbound
email. Email is a multi-provider, multi-transport, multi-auth space, so the
trigger must stay general and must not hardcode any one provider. It also has
to fit the runner's existing architecture: every step runs as a subprocess
(ADR-0008), and the daemon reads only its own store, never files (ADR-0013).
Two questions had to be settled: where the provider-specific knowledge lives,
and how a recurring poll is scheduled and executed without the daemon doing
work in-process or reading a config file.

## Decision drivers

- Every step runs as a subprocess (ADR-0008); the daemon does no real work
  in-process. Polling a mailbox is real work, so it must run as a subprocess,
  not inside the daemon.
- A "helper" is the existing concept for provider-specific knowledge (SPEC:
  Curated integration helpers, ADR-0017). The trigger stays generic; the helper
  owns how to talk to a provider, including its transport and auth.
- The daemon reads nothing from disk at runtime (it reads its own store,
  ADR-0013). A recurring poll must be scheduled through the store, not via a
  file the daemon locates.
- Best Simple System for Now (ADR-0002): do not build a provider-registry or a
  shared declared config until a second provider or a send side
  justifies it.

## Considered options

- **Per-provider helper run as a subprocess; trigger carries its own config
  (chosen).** The `email_received` trigger carries the provider's connection
  config and a `poll` cron schedule, webhook-style. The poll is a scheduled task
  in the store whose job runs the servitor binary's hidden `__email_poll`
  command, which uses the provider's helper to connect, fetch new (unseen)
  messages, parse them, and mark them seen. The worker hands the returned emails
  to a callback, and the daemon fans out one run per email.
- **A declared email connector in `servitor.config.yaml`.** Would give
  inbound and future outbound a shared home, but it is shaped for subprocess
  connectors and would require the daemon to read a config file at runtime,
  breaking the store-only invariant. Deferred until a send side or a second
  provider exists.
- **The daemon polls the mailbox in-process.** Rejected: violates ADR-0008 and
  adds provider logic and credentials to the daemon.

## Decision outcome

Chosen option: **per-provider helper run as a subprocess; polling is a generic
mechanism and email is one kind of it.**

Polling is a general primitive, not an email-specific one: a recurring poll runs
a fetcher subprocess on a schedule, gets a list of new items, and fans out one
run per item. The core has a generic `poll` step type: the worker runs the
fetcher subprocess, reads a `kind` from the job's config, and hands the items to
an `OnPoll` callback; the receiver's `Polled(workflowID, kind, items)` dispatches
on `kind` to build each run's event. A polled source is a `kind` plus a helper
command; nothing in the worker, daemon, or trigger knows a provider.

`email_received` is the first poll kind. It takes the provider's connection
fields and an optional `poll` schedule; registering (or enabling) the workflow
schedules a poll whose job runs the hidden `__email_poll` subprocess, which uses
the named provider's helper to connect and return the new emails. The worker
hands them to the `OnPoll` callback with `kind == "email"`, and the receiver
starts one run per email with the parsed email as the event. Messages are marked
\Seen after polling so each is fired once.

The provider-agnostic core contract is the `Email` type in `internal/email`,
which the core passes around and which a provider helper produces. The first
provider is Google Workspace: the trigger carries `host`, `username`, `secret`
(a varlock secret name), and the gmail helper connects over IMAP using an app
password, via the pinned emersion/go-imap v1 library (ADR-0011). A future
provider is a new helper and whatever connection/auth fields that provider
needs, without changing the trigger's role or the core.

### Consequences

- Good: the trigger and the core stay provider-agnostic; a future provider is a
  new helper and a different connection/auth on the trigger, not a change to the
  core, and a future polled source is a new `kind` plus command.
- Good: no new runtime file dependency; the daemon still reads only its store.
- Good: the poll runs as a subprocess with a filtered secret env (SPEC:
  Varlock), so credentials never enter the runner's process.
- Neutral: the trigger carries its own mailbox credentials. A future SMTP send
  step is an independent concern: it carries its own credentials and may point
  at a different account than the one received on. Only if many accounts are
  used would a named-account registry (orthogonal to send vs receive) be worth
  introducing; not needed now (BSSN).
- Neutral: email is polled, so delivery is near-realtime but not push-instant;
  a provider push (webhook) is a possible future trigger type.

### Confirmation

`go test ./...` passes. The gmail helper (the first provider) is tested against
an in-memory IMAP server (fetch-and-parse, and mark-seen so a second poll
returns nothing). The worker test pins the generic poll-to-callback handoff, the
trigger test pins one-run-per-item and the event shape for the email kind (and
that an unknown kind passes the item through), and a daemon test confirms an
`email_received` workflow registers and unregisters a scheduled poll on
enable/disable.

## Interface notes

Additive to the Wafer schema: `email_received` trigger config gains the
provider's connection fields and an optional `poll` (default `*/5 * * * *`). For
the first provider these are `host`, `username`, and `secret` (a varlock secret
name, matching the webhook triggers). Internal (not exposed on the Wafer
surface): a generic `poll` step type and a `kind` field on the scheduled poll
job, used by the worker and receiver. No change to the CLI or daemon control
protocol. Not breaking; existing consumers are unaffected.

## More information

- ADR-0008 (every step runs as a subprocess)
- ADR-0011 (pin the Honker extension; the same pinning applies to go-imap)
- ADR-0013 (the daemon reads its store, not files)
- ADR-0017 (mechanism as the organizing principle; `helper` group)
- SPEC: Triggers, `email_received`; Curated integration helpers
