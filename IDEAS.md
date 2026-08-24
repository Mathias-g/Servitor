# Ideas

Catch-all for promising directions that are not yet decided or built. These are
possibilities, not commitments: nothing here is a plan or a decision, and most
of it will be discarded. When an idea becomes a real decision it gets an ADR and
moves into the SPEC/PLAN; until then it lives here so it is not lost.

## Dogfooding: let Servitor publish its own capabilities

How a remote agent gets capabilities is currently described in the SPEC as "the
pipeline runs `servitor capabilities` and commits the generated directory to
git." But Servitor is itself a workflow automation system, so the natural
refinement is to make that publication a **Wafer**, not a bespoke CI step:

- A Wafer (say `publish-capabilities`) triggered on deploy, on demand, or on a
  slow cron, runs `servitor capabilities <dir>` and then commits and pushes the
  result to the repo the agent already reads.
- Remote agents read the committed capabilities from the repo exactly as they
  do today; the only change is that the runner does the publishing.

Why this is attractive:

- It eats Servitor's own dogfood: the first real end-to-end workflow is the one
  that makes Servitor usable by remote agents.
- It is the canonical demonstration for Phase 9 ("validate the agent workflow"),
  since an agent goes from `capabilities` to an applied Wafer.
- It is compatible with the current SPEC wording (the "pipeline" is just
  realized as a Wafer), not a contradiction.

Not buildable until:

- Step execution exists (Phase 6) so a Wafer can actually run.
- A `commit-and-push-to-git` capability exists, and varlock (Phase 8) supplies
  the git credentials.
- Triggers that fire it on a schedule (cron) or on deploy exist (Phase 7).

Open question for later: decide between a Wafer-driven publish and a plain CI
step. The Wafer version is the more interesting default; a plain CI step is the
simpler fallback.

## (Add more ideas here as they come up; delete them when they become ADRs or
## are discarded.)
