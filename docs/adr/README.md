# Architecture Decision Records

This directory is the single decision log for Servitor. Every architecturally
significant decision lives here as a numbered, immutable Markdown file.

## Why a central log

- Significant decisions span the whole project (language, control-plane shape,
  dependency rules, packaging). They have no single home in the code.
- Code moves: packages get split, merged, renamed. ADRs are immutable records
  that must outlive the current shape of the code.
- One global sequence means one greppable decision log and compatibility with
  ADR tooling (MADR, adr-tools, log4brains), which all assume one directory.

## Conventions

- Filenames: `NNNN-short-kebab-title.md`, zero padded, e.g.
  `0004-adopt-go-as-the-runner-language.md`.
- Numbers are assigned sequentially and never reused.
- ADRs are immutable. To reverse or change a decision, write a new ADR and set
  the old one's status to `superseded by ADR-NNNN`.
- `0000-adr-template.md` is the template. Copy it, do not edit it in place.

## Status lifecycle

`proposed` -> `accepted` -> (`deprecated` | `superseded by ADR-NNNN`)

## Servitor-specific fields

- `scope`: the parts of Servitor the decision touches — e.g. `runner`,
  `control-plane`, `honker-integration`, `varlock-integration`,
  `singer-integration`, `webhooks`, `packaging`.
- `interface-impact`: whether the decision changes a public contract — the
  **Wafer schema**, the **CLI surface**, or the **daemon control protocol**.
  `none`, `new`, or `breaking`. This is the field that matters most, because a
  public-contract change ripples to every workflow and every agent that uses
  Servitor, while an implementation change behind a stable interface stays local.

## Enforcement

Where a decision can be checked by tooling, record how in the ADR's
`Confirmation` section. Lean on automated checks (`go test`, `go vet`, lint,
pre-commit) rather than prose where you can.
