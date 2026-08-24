---
status: accepted
date: 2026-08-24
decision-makers: [Mathias]
consulted: []
informed: []
scope:
  - runner
interface-impact: breaking
---

# ADR-0008: Run every step as a subprocess

## Context and problem statement

A step execution mode is a choice about where a step's code runs. One option is
to run some steps inside the runner's own process (avoiding subprocess startup
cost), the other is to run every step as a subprocess. Because the runner is
written in Go (ADR-0004), where a subprocess spawn costs roughly a millisecond,
the startup-cost argument for running steps in-process is weak. We need to
decide: does any step run inside the runner's process, or does every step run as
a subprocess?

## Decision drivers

- The main argument for in-process execution is subprocess startup cost; that
  cost is negligible in Go.
- Running code in-process means it shares the runner's process memory: the
  resolved-secret cache, the SQLite write connection, everything. That is a
  security liability unless the code is provably benign.
- Best Simple System for Now: delete complexity that is no longer justified,
  especially a security-sensitive code path.
- The change must be reversible in the easy direction if a real hot path later
  justifies re-adding an optimization.

## Considered options

- **Run pure-computation steps in-process** (`transform`, `branch`, `foreach`),
  avoiding even cheap spawns on a hot `foreach`-over-`transform` loop, and
  giving simpler in-process control flow than a subprocess protocol.
- **Run every step as a subprocess** (chosen): the subprocess is the isolation
  boundary; no code runs inside the runner's process.

## Decision outcome

Chosen option: **every step runs as a subprocess.**

Subprocess startup is cheap in Go, so there is no strong reason to run steps
in-process. Running everything as a subprocess removes an entire
security-sensitive code path and its "not a sandbox" risk surface, and it does so
for free: a `transform`, `branch`, or `foreach` step cannot request secrets, so
its subprocess environment contains nothing worth stealing, and the subprocess
is a genuine containment boundary rather than an explicitly-unsandboxed one.

Keeping an in-process path would preserve a narrow hot-loop optimization (a heavy
`foreach` over `transform` at high iteration counts) at the cost of permanently
carrying an in-process execution path whose isolation is admitted not to exist.
That cost is paid by everyone who reads or changes the runner, forever; the
benefit serves a rare, not-yet-measured case.

### Consequences

- Good: no code executes inside the runner's process, so there is no "not a
  sandbox" surface; the subprocess is the isolation boundary for every step.
- Good: `transform`/`branch`/`foreach` need no special in-process guarantees;
  they are ordinary subprocess steps like everything else.
- Good: the execution model is uniform, one mode (`subprocess`), with no
  qualification rules or review burden for special in-process types.
- Neutral: the step execution model is now uniform (one mode, `subprocess`).
- Bad: a pure-computation step pays a small subprocess spawn cost. Negligible
  against real I/O-bound steps; if profiling ever shows a hot pure-transform
  loop where it matters, re-adding an in-process optimization is a measured,
  reversible change behind a benchmark (see Confirmation).

### Confirmation

`SPEC.md`'s Step execution section states a single `subprocess` mode. Any future
in-process optimization must be introduced only after profiling demonstrates a
real cost, and only as a new ADR.

## Interface notes

The step execution model is a single mode: every step runs as a subprocess.
There is no `execution_mode` field on step types. Workflow authors are
unaffected; `transform`, `branch`, and `foreach` are ordinary subprocess steps.

## More information

- ADR-0004 (adopt Go, which removes the subprocess-startup-cost argument)
- ADR-0002 (Best Simple System for Now)
