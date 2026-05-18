# 0012. Hold generated Go to the module's vet/modernize bar

**Status:** Accepted
**Date:** 2026-05-17

## Context

Bumping the module to `go 1.26` (and running `go fix`) rewrote a generic
pointer helper in the analyzer test to Go 1.26's `new(value)` builtin. That
raised the question: does the generator's golden output also need
modernizing? An empirical scan of both the generated adapter
(`examples/chi-basic/expected/go/goduct_routes.go`) and the hand-written
example input (`examples/chi-basic/api/users.go`) found **zero** modernizer
triggers and clean `go vet` — no change needed today. But the general risk is
real: a code generator that emits code the user's own `go fix` immediately
rewrites is a bad tool, in the same spirit as ADR 0007's "no silent junk."
`go fix` cannot be run against the golden fixtures to check this — by ADR 0011
they do not type-check in isolation, and rewriting a spec snapshot inverts the
contract (the generator defines the golden, not the other way round).

## Decision

Generated Go must produce **zero diffs** under `gofmt`, `go vet`, and
`go fix`/modernize for the Go version declared in the consuming module. This
is enforced by the "compile + vet the generated adapter" integration test
(the test chi is deferred for in ADR 0011): it generates into a real
buildable package and asserts `go vet` and `go fix -diff` are empty. `go fix`
is never run against the golden fixtures.

## Consequences

- Easy: a future Go bump that adds a new modernizer is caught automatically by
  that test rather than by someone noticing — generated code stays first-class
  alongside hand-written code (consistent with 0007).
- Hard / giving up: the guarantee is asserted but **unverified** until that
  integration test exists (needs the buildable-package wiring and chi from
  0011). Modernizers are Go-version-dependent, so golden files may need
  regeneration on Go upgrades — accepted; that is the mechanism working, not a
  failure.
- The exact `go fix -diff` invocation and which toolchain CI pins are
  TBD — discuss when the integration test is built.

## Alternatives considered

- Run `go fix` directly on the golden fixtures — rejected: they don't compile
  in isolation (0011), and rewriting a spec snapshot inverts the contract.
- Don't hold generated code to modernize at all — rejected: emitting code the
  user's tooling instantly rewrites is exactly the bad-tool failure 0007
  exists to prevent.
- `gofmt`-only, no vet/modernize — rejected: catches formatting but not idiom
  drift across Go versions.
