# 0001. Support two handler modes, idiomatic-only in v0.1

**Status:** Accepted
**Date:** 2026-05-17

## Context

goduct's value depends on knowing a handler's request and response types. Two
shapes are common in Go HTTP code: a typed function the tool can fully infer
from (`func(ctx, Req) (*Resp, error)`), and a standard `http.HandlerFunc`
where types are invisible to the analyzer and must be declared in annotations.
The README documents both styles; raw mode exists "for existing code or finer
control," but goduct cannot verify that raw-mode annotations match what the
handler actually does. The chi-basic golden example deliberately exercises
idiomatic mode only, and the roadmap places raw mode in v0.2.

## Decision

Support two handler modes: **idiomatic** (`func(ctx, Req) (*Resp, error)`,
everything inferred from types) and **raw** (`http.HandlerFunc` plus
`goduct:request` / `goduct:response` annotations). v0.1 ships idiomatic only;
raw mode is v0.2. The annotation parser already accepts and records the
raw-mode directives without enforcing mode, so raw support is additive later.

## Consequences

- Easy: v0.1 has a single inference path to build and golden-test; the
  analyzer can trust the Go type system rather than unverifiable annotations.
- Easy: raw mode lands later as an additive code path — the IR already has a
  `Mode` field and the parser already reads the raw directives.
- Hard / giving up: existing `net/http` codebases cannot adopt v0.1 without
  rewriting handlers into the typed signature. Raw-mode annotations, when they
  land, are unverifiable by design — the README already calls this out.

## Alternatives considered

- Idiomatic only, forever — rejected: excludes existing codebases the README
  explicitly positions raw mode for.
- Raw only — rejected: loses type inference and the typed-client guarantee
  that is the whole point.
- Ship both in v0.1 — deferred to keep v0.1 scope tight (chi-basic is kept
  deliberately minimal so the golden diff stays useful).
