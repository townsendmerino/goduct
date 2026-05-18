# 0015. Query/header parameter optionality rule

**Status:** Accepted
**Date:** 2026-05-17

## Context

During route discovery (the [0014](0014-handler-signature-strictness.md)
implementation) the question arose of whether non-`required` validators
(`min`, `max`, `len`, …) imply field presence. The milestone prompt was
internally contradictory on this point; this ADR pins the rule permanently.

## Decision

A query or header parameter is **required if and only if** its struct field
has `validate:"required"` (possibly among other validators). Other
validators constrain the value when present but do not imply presence.

Pointer-ness on a query/header field independently means optional; an
explicit `required` on a pointer field wins — the field is required, and the
pointer merely lets the handler distinguish "zero value sent" from
"omitted".

Path parameters are always required: they cannot be made optional via tags
or pointers, and a pointer path param is rejected at route discovery
([0014](0014-handler-signature-strictness.md)).

## Consequences

- Client-side TS type generators use this rule to choose `name: T` vs
  `name?: T`.
- Adapter-generated code uses it to decide whether a missing query string is
  a 400 or a zero-value default.
- Body (json-tagged) fields follow a separate rule, owned by the
  type-traversal milestone: optional iff pointer OR `omitempty` in the tag.
  Its ADR lands with that milestone.

## Alternatives considered

- Treat any validator as "required" — rejected: conflates value constraints
  with presence.
- Require an explicit `optional` tag — rejected: redundant with pointer-ness
  and against Go convention.
