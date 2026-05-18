# 0016. Field source in the IR

**Status:** Accepted
**Date:** 2026-05-17

## Context

A request struct's fields are tagged path / query / header / json (mutually
exclusive per route discovery). In the IR the full request struct is the
body type, but downstream generators need filtered views:

- The TS body type and zod body schema must include only json-tagged fields.
- The Go adapter must know which fields come from the URL path, the query
  string, the headers, and the body respectively.
- The body type may also be referenced as a **nested** type elsewhere
  (uncommon but possible), in which case its non-json fields would not exist
  on the wire at all.

This information must live in the IR; otherwise every generator re-derives
it from struct tags, which couples generators to the Go source and breaks
the "the IR is the contract" rule ([0003](0003-generators-as-pipeline.md)).

## Decision

`ir.Field` gains a `Source` field of type `ir.FieldSource`:

```go
type FieldSource int

const (
	FieldSourceJSON   FieldSource = iota // wire body (default for non-request types)
	FieldSourcePath                       // URL path
	FieldSourceQuery                      // URL query string
	FieldSourceHeader                     // HTTP header
	FieldSourceNone                       // untagged; not on the wire
)
```

Rules:

- **Request types:** `Source` is set per the field's tag. Untagged fields
  are `FieldSourceNone` (ignored by all generators).
- **Non-request types** (response types and transitively-referenced types):
  every field is `FieldSourceJSON` or `FieldSourceNone`. A path/query/header
  tag on a non-request type's field is a **load error** — per the
  loud-failure principle ([0007](0007-loud-failure-on-unsupported-input.md)),
  this means the author misunderstood what those tags mean.
- The type-traversal milestone owns implementing this. Route discovery
  already extracts top-level params and is left unchanged; the traversal
  layer replicates that work into `Field.Source` for consistency.

## Consequences

- Generators have a single source of truth for "does this field appear in
  the wire body?": `Source == FieldSourceJSON`.
- The IR change is additive: the zero value is `FieldSourceJSON`, so
  existing IR consumers compile unchanged.
- A response type's fields cannot carry path/query/header tags (loud
  failure). A minor restriction users are unlikely to hit.

## Alternatives considered

- Two parallel `Field` slices on `TypeDef` (`BodyFields`, `ParamFields`) —
  rejected: duplicates field info and is awkward for transitively-referenced
  types.
- Compute on demand from the struct tag in each generator — rejected:
  breaks the IR-as-contract principle ([0003](0003-generators-as-pipeline.md)).
