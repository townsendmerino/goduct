# 0008. Defer React Query hooks to v0.2

**Status:** Accepted
**Date:** 2026-05-17

## Context

The top-level README lists `--hooks` (React Query) among the generators and in
the `gen` example, but the roadmap and the chi-basic README both place React
Query hooks in v0.2, and `examples/chi-basic/expected/` contains no
`hooks.ts`. This inconsistency was flagged earlier in this conversation: the
README oversells v0.1. A decision is needed on which source of truth wins.

## Decision

v0.1 ships `--types`, `--zod`, `--client` (fetch), and `--go-adapter`.
`--hooks` (React Query) is deferred to v0.2, keeping v0.1 focused and the
frontend output framework-agnostic.

## Consequences

- Easy: v0.1 frontend output works with any TS frontend (types, zod, and a
  plain fetch client have no UI-framework dependency); the golden surface
  stays small.
- Hard / giving up: React users get no generated hooks until v0.2.
- The README still advertises `--hooks` under v0.1 and must be reconciled with
  this decision. That reconciliation was explicitly deferred earlier in this
  conversation — tracked here, not yet done.

## Alternatives considered

- Ship hooks in v0.1 — rejected: ties v0.1 to React and enlarges the golden
  surface before the core pipeline is proven.
- A generic framework-agnostic data layer instead of React Query —
  TBD — discuss.
