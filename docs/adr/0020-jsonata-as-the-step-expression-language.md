---
status: accepted
date: 2026-08-25
decision-makers: [Mathias]
consulted: []
informed: []
scope:
  - runner
  - wafer
  - dedupe
interface-impact: new
---

# ADR-0020: JSONata is the step expression language, backed by gnata

## Context and problem statement

Servitor has two open questions about how agents write expressions in a Wafer
(SPEC: open questions): which language `transform` uses to reshape JSON between
steps, and which language `dedupe_key` uses to derive a step's idempotency key
from its inputs. Both are the same underlying need: a bounded, declarative,
YAML-embeddable way to select, map, filter, aggregate, and restructure JSON,
authored by an AI agent. A step runs as a subprocess with no host access
(ADR-0008), so sandboxing is not the concern; only bounded evaluation is. We
need one language that answers both questions and one Go implementation behind
it that is cheap to swap if the implementation proves unfit.

## Decision drivers

- One expression language for both `transform` and `dedupe_key`, so agents
  learn one thing and we pin one dependency.
- Bounded evaluation: no unbounded loops or unbounded memory; the SPEC requires
  this explicitly for `transform`.
- Declarative and YAML-embeddable: the expression is a string in the Wafer, so
  it must be authorable and readable as data, and generatable by an agent.
- The implementation must be replaceable without touching Wafers already
  written: a Wafer stores expression strings, never library internals.
- The runner is Go, so a mature Go implementation keeps the dependency surface
  small; but we run steps as subprocesses (ADR-0008), so a subprocess runtime
  is also acceptable if it is the only way to get a faithful implementation.

## Considered options

- **jq via itchyny/gojq.** The most mature, most battle-tested pure-Go
  transformer (full jq, active maintainer). Powerful but with a steeper syntax
  for agents than JSONata, and it is not the language the workflow-automation
  ecosystem is converging on.
- **JSONata via gnata (chosen).** A complete pure-Go JSONata 2.x implementation
  validated against the official jsonata-js test suite, with explicit bounded
  evaluation (`WithStack`, `WithTimeout`, `WithSequence`). JSONata is the
  expression language several workflow engines (AWS Step Functions, Node-RED,
  Kestra, Stedi) chose for declarative JSON reshaping, so it is the best fit
  for agent-authored Wafers. gnata is young (pre-1.0, single maintainer), which
  is the risk that the replaceable-wrapper decision below mitigates.
- **JSONata via the reference jsonata.js embedded in goja.** Guaranteed JSONata
  parity, but bundles a JS engine and a ~100KB library for one feature, which is
  heavier than BSSN wants.
- **A general-purpose language (Starlark, Tengo, Expr) as the transform
  language.** Strong Go support and bounded, but they are imperative scripting
  or general expression engines, not declarative JSON reshape languages; they
  undercut the YAML-embeddable, agent-generable one-liner we want.
- **Keep the languages separate (JSONata for transform, a selection language
  for dedupe_key).** Rejected: it doubles the dependency surface and what agents
  must learn for no benefit, since dedupe_key is a subset of transform's need
  (select/extract/stringify).

## Decision outcome

Chosen option: **JSONata via gnata**, as the single step expression language
for both `transform` and `dedupe_key`.

`transform` runs the Wafer's `expression` field through the JSONata evaluator
against the step's input and returns the result. `dedupe_key` runs the Wafer's
`dedupe_key` expression through the same evaluator against the step's inputs
and stringifies the result to form the key. Both calls go through one small
`internal/expression` package that exposes only a `Compile`/`Eval` surface and
never leaks gnata types into the rest of the runner. Because a Wafer stores
expression strings and not library internals, swapping the backend for any
other JSONata-2.x-compatible implementation is a change to that one package,
not to the Wafer contract.

gnata is pinned (versioned) in go.mod. Its bounded-evaluation options
(`WithStack`, `WithTimeout`, `WithSequence`) are set by the runner so a single
step cannot loop or grow unboundedly.

### Consequences

- Good: one language and one dependency answer both open questions.
- Good: JSONata is the ecosystem's choice for declarative workflow transforms,
  so the authoring experience (by agents especially) is a known quantity.
- Good: the replaceable wrapper keeps the Wafer contract (a JSONata expression
  string) independent of the implementation, so a future swap to another
  JSONata engine is transparent to Wafers already written.
- Good: bounded evaluation is a first-class option on gnata, matching the SPEC
  requirement.
- Bad: gnata is young and pre-1.0; its API could change. The wrapper and the
  pinned version contain the blast radius, and gojq remains a fallback if
  gnata proves unfit.
- Neutral: adds a Go dependency (`github.com/recolabs/gnata`, plus its single
  transitive dependency `tidwall/gjson`).
- Neutral: the `dedupe_key` language is now settled, but the separate question
  of *when* it is evaluated (currently at enqueue time per SPEC, versus
  transform's at-execution time) is unchanged by this decision.

### Confirmation

`go test ./...` passes. The `internal/expression` wrapper is pinned by a test
that evaluates a representative JSONata expression (including a map/filter/
aggregate) and asserts the bounded-evaluation options are applied. The
`transform` executor and the `dedupe_key` evaluator, when built, each carry
tests that use the wrapper and would fail if the backend were swapped for a
non-compliant engine.

## Interface notes

Additive to the Wafer schema: the `transform` step type gains a defined
`expression` field (JSONata) and `dedupe_key` gains a defined expression
language (JSONata). Both already exist as fields; this ADR fixes their language
and semantics. No change to the CLI surface or the daemon control protocol.

## More information

- ADR-0008 (subprocess-per-step isolation; the bounded-evaluation requirement)
- SPEC: Step types (transform), Idempotency (dedupe_key), Open questions
- Discussion: ecosystem survey of expression languages in workflow automation
  (AWS Step Functions, Node-RED, Kestra, Stedi adopting JSONata; jq vs JSONata
  tradeoffs) and a survey of Go implementations (gojq, gnata, goja+jsonata.js,
  Starlark, Tengo, Expr).
