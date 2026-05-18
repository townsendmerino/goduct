# 0002. Support chi only for v0.1

**Status:** Accepted
**Date:** 2026-05-17

## Context

The `--go-adapter` generator emits router wiring, which is framework-specific
(route registration calls, path-param syntax). The README's "What's supported
(v0.1)" lists chi as the only framework; the roadmap defers gin, echo, and the
std `net/http` mux to v0.2. One framework is enough to prove the
analyzer → IR → generator pipeline end to end, and chi-basic is the single
canonical golden fixture. Why chi specifically rather than gin or echo was not
discussed.

## Decision

Support **chi only** for v0.1. gin, echo, and `net/http` mux are v0.2+.

## Consequences

- Easy: exactly one router syntax to translate (annotation `:id` →
  chi `{id}`), one golden fixture, a small test surface.
- Hard / giving up: non-chi users cannot adopt v0.1; the generated adapter
  carries a chi dependency.
- The IR keeps `Path` framework-neutral (colon params; see
  [0003](0003-generators-as-pipeline.md)), so additional frameworks become a
  per-generator translation rather than an IR change.

## Alternatives considered

- net/http mux only — TBD — discuss (why chi over the stdlib mux was not
  discussed).
- Multiple frameworks in v0.1 — rejected: multiplies golden fixtures and test
  surface before the core pipeline is proven.
- Why chi specifically vs gin/echo — TBD — discuss.
