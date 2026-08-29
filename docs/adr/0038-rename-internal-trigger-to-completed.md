---
status: accepted
date: 2026-08-29
decision-makers: [Mathias]
consulted: []
informed: []
scope:
  - runner
  - triggers
  - wafer
interface-impact: breaking
---

# ADR-0038: Rename the `internal` trigger type to `completed`

## Context and problem statement

The `internal` trigger fires a workflow when another workflow completes
(SPEC: Triggers, `internal`, ADR-0026). It is the runner's one trigger that is
not an external event source: every other trigger waits on something inbound
(a webhook, a timer, a manual invocation, a mailbox), while `internal` is fired
by Servitor itself, from inside the runner, when a workflow's run completes.

The name `internal` is opaque: it says the event originates inside the runner,
but not *what* internal event it is. In a Wafer it reads `type: internal,
workflow: upstream`, which does not tell a reader that this means "fire when
`upstream` completes." The name records the event's origin, not its meaning.

## Decision drivers

- The Wafer is authored by agents and read by humans; a trigger type name
  should say what event fires it.
- The event this trigger reacts to is a workflow's run completing; the name
  should say that.
- Servitor has no installed base, so a breaking rename is a clean cut.

## Considered options

- **Rename `internal` to `completed` (chosen).** `type: completed, workflow:
  upstream` reads as "fire when `upstream` completes." Short and direct.
- **`workflow_completed`.** Rejected: redundant, since the `workflow:` field
  already says a workflow is involved.
- **`run_completed`.** Rejected as marginal: it is a run that completes, but the
  `workflow` field already scopes it, and the plural completion events the model
  has are all workflow runs, so `completed` is not ambiguous.
- **Keep `internal`.** Rejected: opaque; it describes the event's origin (from
  inside the runner) rather than its meaning.

## Decision outcome

Chosen option: **rename `internal` to `completed`.**

The trigger type is `completed`; its `workflow` field names the workflow whose
completion fires it. The run's event is unchanged: `{trigger: "completed",
from: <workflow name>, from_run: <completed run id>}`.

```yaml
name: downstream
triggers:
  - type: completed
    workflow: upstream
nodes:
  - type: shell
    name: notify
    command: "true"
```

`internal` is no longer accepted as a trigger type.

### Consequences

- Good: the trigger type says what event it reacts to (a completion) rather
  than where the event came from.
- Good: it stays distinct from the inbound/external triggers, which it still
  is (fired from inside the runner), but the name now conveys the meaning.
- Bad: a breaking rename, but with no installed base it is a clean cut.

### Confirmation

`go test ./...` passes. A Wafer using `type: completed` fires a downstream
workflow on an upstream run's completion; `type: internal` is no longer
accepted.

## Interface notes

Breaking Wafer-schema change. The trigger type `internal` is renamed to
`completed`; Wafers using `internal` must change to `completed`. The daemon
control protocol and the run event shape are unchanged.

## More information

- SPEC: Triggers, `internal` (now `completed`)
- ADR-0026 (the `internal` trigger's completion callback; superseded by this
  rename in its trigger type)
- ADR-0037 (the companion rename of the `on:` field to `triggers:`)
- IDEAS.md "Suspended waits" (a future resume use of this trigger, which is
  why the name matters)
