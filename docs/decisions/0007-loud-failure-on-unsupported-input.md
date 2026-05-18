# 0007. Fail loudly on Go input the analyzer can't represent

**Status:** Accepted
**Date:** 2026-05-17

## Context

A code generator that silently skips what it doesn't understand produces a
client that is confidently wrong — the exact failure this project exists to
prevent. The README states that when goduct sees something it can't represent
it "errors loudly with a file:line pointer — no silent skipping," and the
roadmap enumerates currently-unsupported constructs (generics, custom
`MarshalJSON`, etc.). This conversation established the same fail-fast
philosophy at the parser level: duplicate directives now error rather than
silently overwrite ("leniency only serves to hide typos").

## Decision

When the analyzer encounters Go code it cannot represent (generics, custom
`MarshalJSON`, interface fields, unsupported types), it errors with a
file:line pointer. No silent skipping. Users opt in to unsupported features
through a future allowlist.

## Consequences

- Easy: no silently-wrong client; every failure is actionable with a precise
  location; consistent with the duplicate-directive fail-fast already shipped
  in the analyzer.
- Hard / giving up: one unsupported construct blocks generation for the whole
  package until it is addressed. The opt-in allowlist mechanism is unspecified
  — TBD — discuss.
- Cross-ref [0006](0006-validation-tag-translation.md): validation *richness*
  degrades silently by contrast. The boundary is "can't represent the type"
  (hard error) vs "can't translate a validation rule" (degrade).

## Alternatives considered

- Skip unsupported handlers and continue — rejected: silent partial clients
  are the failure mode goduct exists to prevent.
- Best-effort generation with warnings — TBD — discuss.
