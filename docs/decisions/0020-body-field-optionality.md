# 0020. Body-field optionality

**Status:** Accepted
**Date:** 2026-05-17

## Context

[ADR 0015](0015-query-header-optionality-rule.md) established optionality
rules for query/header parameters but explicitly deferred the rule for body
(json-tagged) fields to the type-traversal milestone. This ADR closes that
forward reference.

## Decision

A field with `FieldSourceJSON` is `Optional` iff:

1. The field's Go type is a pointer, OR
2. The field's `json:` tag contains the `omitempty` token (any position
   after the name).

Validation tags (`validate:"required"` etc.) on json-tagged fields
constrain the value when present; they do **not** affect `Optional`. The
wire contract is what is modeled here; server-side validation rejects
invalid values at the handler boundary.

Path fields are never `Optional`. Query/header fields follow
[ADR 0015](0015-query-header-optionality-rule.md)'s rule.

## Consequences

- Generators emit `field?: T` in TS exactly when `Optional` is true.
- A POST handler that wants "may be omitted" sends a pointer field with
  `omitempty`, matching idiomatic Go.
- `validate:"required"` on a json field affects server-side behavior but is
  invisible to the client schema.

## Alternatives considered

- Treat `validate:"required"` as a presence requirement — rejected for the
  same reasons as [ADR 0015](0015-query-header-optionality-rule.md):
  validator tags are about value validity, not field presence.
- Make all json fields optional by default — rejected; matches neither
  `encoding/json` nor user intuition.

## Cross-references

- Completes the forward reference from
  [ADR 0015](0015-query-header-optionality-rule.md).
- Companion to ADR 0015's query/header rule.
