# 0010. Name the project "goduct"

**Status:** Accepted
**Date:** 2026-05-17

## Context

The project needs a stable name used consistently across the README, docs, and
the Go module path (`github.com/townsendmerino/goduct`). The name is a portmanteau of
**Go** + **conduit**, matching the README tagline. "goduct" is not obvious to
pronounce on sight, so the intended pronunciation is recorded here to keep
external communication consistent.

## Decision

The project name is **goduct** (Go + conduit). Pronounced **"GO-duct"** (rhymes
with "product"). Tagline: *"A typed conduit between your Go API and your
TypeScript client."*

## Consequences

- Easy: one name and tagline reused verbatim across README, docs, and the
  module import path; the pronunciation is no longer ambiguous in talks or
  issues.
- Hard / giving up: "goduct" reads ambiguously without the guide (the reason
  this ADR exists). Name uniqueness / search collision was not assessed —
  TBD — discuss.

## Alternatives considered

- TBD — discuss (no alternative names were proposed in discussion).
