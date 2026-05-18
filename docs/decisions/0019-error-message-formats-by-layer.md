# 0019. Error message formats by layer

**Status:** Accepted
**Date:** 2026-05-17

## Context

Route discovery ([0014](0014-handler-signature-strictness.md)) emits
single-line errors of the form `goduct: file:line:col: message`. Type
traversal ([0018](0018-type-traversal-failure-boundaries.md)) mandates a
3-line categorized format that includes a category ID, a qualified field
name, and a remediation hint. These two formats coexist in the analyzer.
Without an explicit rule, future analyzer layers and prompt iterations will
drift, and existing layers will be pressured toward false consistency.

## Decision

The analyzer uses **two distinct error message formats**, selected by what
the error is about.

**Format A (single-line)** — for errors about a Go construct as a whole:

```
goduct: <file>:<line>:<col>: <message>
```

Used for:

- Handler signature errors (route discovery)
- Annotation parsing errors (`annotations.go`)
- Loader errors (`loader.go`'s existing format)
- Any future error that is about "this declaration is wrong" as a unit,
  with no internal field structure to point at.

**Format B (3-line categorized)** — for errors about a specific field
within a struct:

```
goduct: <file>:<line>:<col>: <category-id>: <description>
        in <qualified-field-name> (<Go-type>)
        hint: <one-line remediation>
```

Used for:

- Type-traversal errors (per [0018](0018-type-traversal-failure-boundaries.md)'s
  categorized table)
- Any future error that is about "this field within this struct fails this
  specific categorized rule."

The category ID requirement applies **only** to Format B. Format A errors do
not have category IDs because the rules they enforce are not categorized —
each is sui generis.

## Rationale

The two formats serve different needs:

- Format A is read by users debugging one declaration at a time. The error
  is local and self-contained.
- Format B is read by users auditing a type tree, often grepping for all
  instances of a specific failure category (e.g. "show me every interface
  field in my API"). The category ID and qualified name support this
  workflow.

Forcing one format on both layers either bloats Format A errors with empty
category fields, or strips Format B errors of the grep-ability they were
designed for.

## Consequences

- Two formats to test, two formats to document.
- When a new analyzer layer is added (e.g. orchestration in a future
  milestone), the prompt for that milestone must declare which format(s) it
  emits and why, referencing this ADR.
- User-facing docs (eventually a `TROUBLESHOOTING.md` or similar) should
  explain both formats. Out of scope for this ADR.
- Programmatic consumers of analyzer errors (none yet) will see a union of
  the two formats. We accept this; the alternative is a structured error
  type, which is over-engineering for v0.1.

## Alternatives considered

- Harmonize toward Format B (categorize handler errors) — rejected: would
  require inventing categories that don't naturally exist; adds noise.
- Harmonize toward Format A (drop categories from type traversal) —
  rejected: loses the grep-ability that motivated Format B.
- Structured error type with format-independent fields, rendered by a
  printer — rejected: over-engineering for v0.1; possible v0.2 refactor.

## Implementation note

No code changes are required by this ADR. Route-discovery errors already
emit Format A; the type-traversal milestone implements Format B per
[0018](0018-type-traversal-failure-boundaries.md). Annotation and loader
errors are single-line and belong to the Format A *category*, but their
existing prefixes/positions are not byte-identical to the Format A template
(see TODO.md) — normalizing them is a pre-v0.1 cleanup, not a change this
ADR requires.
