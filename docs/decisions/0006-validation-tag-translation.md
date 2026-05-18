# 0006. Translate a fixed subset of validator tags to zod

**Status:** Accepted
**Date:** 2026-05-17
**Amended:** 2026-05-18 — Consequences: `oneof` partial-implementation
empirical finding (milestone 14). Decision unchanged.

## Context

go-playground/validator has a large tag vocabulary; zod cannot express all of
it, and translating everything is open-ended. The README lists the supported
subset and says validator tags are translated to zod "where possible";
`expected/client/schemas.ts` shows `.email()`, `.min(1)`, etc. The IR carries
parsed `ValidationRule`s so generators decide what to emit.

## Decision

Translate this subset of go-playground/validator tags to zod: `required`,
`email`, `url`, `min`, `max`, `len`, `oneof`. Other tags are silently ignored
on the client (they do not appear in the generated zod schema) but are
intended to still run server-side via validator. v0.1.

## Consequences

- Easy: client zod schemas cover the common cases with a small, predictable
  translation table.
- Hard / giving up: the client does not enforce unsupported rules, so a server
  rule the client schema lacks is a silent drift.
- **Tension with [0007](0007-loud-failure-on-unsupported-input.md):** that ADR
  forbids silent skipping. The intended boundary is: 0007 governs *type
  representability* (hard error), 0006 governs *validation richness* (degrade
  silently). Whether an unknown `validate:` tag should at least warn is
  TBD — discuss.
- The v0.1 golden adapter (`expected/go/goduct_routes.go`) does not currently
  invoke the validator, so "still run server-side" is the intended behavior
  but is not yet reflected in golden output — TBD — discuss.

### Empirical finding (post-implementation, milestone 14)

The v0.1 implementation translates `required` (as a no-op; presence
handled via `.optional()`), `email`, `url`, `min`, `max`, and `len`. The
`oneof` translation specified in the Decision section was not implemented:
`oneof` tags on validated fields are silently ignored by the zod generator
and pass through unaffected.

This is a partial implementation, not a reversal of the decision. The
decision to support `oneof` stands; implementation is deferred to v0.2.
The README's "What's supported" section accurately describes shipped
reality (`oneof` listed as deferred); the Decision section above describes
the full v0.1 design intent and is preserved for the historical record.

Rationale for deferral: chi-basic exercises no `oneof` tags (the
`UserStatus` enum is its own `TypeEnum`, not a string field with `oneof`),
so the `oneof` path was never golden-tested. Shipping it untested under
the loud-failure principle ([0007](0007-loud-failure-on-unsupported-input.md))
felt worse than explicitly deferring it. v0.2 will implement and
golden-test.

## Alternatives considered

- Error on any untranslatable `validate:` tag — rejected for v0.1 as too
  aggressive (but see the 0007 tension above).
- Translate nothing, trust the server — rejected: loses the runtime safety
  zod schemas exist to provide.
