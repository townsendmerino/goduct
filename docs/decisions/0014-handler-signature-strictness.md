# 0014. Handler signature strictness (idiomatic mode)

**Status:** Accepted
**Date:** 2026-05-17

## Context

We need to decide how strictly the analyzer validates goduct-annotated
handler signatures in idiomatic mode (raw mode is v0.2 per
[0001](0001-handler-signature-convention.md)).

## Decision

A handler must match exactly one of these two shapes:

```
func(context.Context, T) (*U, error)
func(context.Context, T) error
```

where `T` is a named struct type and `U` is a named struct type, both
declared in the same package as the handler (no cross-package
request/response types in v0.1). The second form is only valid when the
route's `goduct:status` is 204, OR when no status is declared AND the method
is DELETE (in which case 204 is the default).

## Consequences

- `DeleteUser`-style "no response body" handlers stay idiomatic (return
  `error` only).
- Returning a non-pointer response struct, a value of an interface type, or
  any non-error second return is a load error.
- Cross-package request/response types are deferred to v0.2; the
  loud-failure principle ([0007](0007-loud-failure-on-unsupported-input.md))
  means we error clearly rather than silently mishandling them.

## Alternatives considered

- Strict-strict (always require `*U` return): cleaner rule but forces ugly
  `*struct{}` in DELETE handlers. Rejected.
- Allow any return shape: silent ambiguity, against
  [0007](0007-loud-failure-on-unsupported-input.md). Rejected.
