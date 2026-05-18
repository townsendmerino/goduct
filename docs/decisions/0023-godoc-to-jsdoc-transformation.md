# 0023. godoc → JSDoc transformation

**Status:** Accepted
**Date:** 2026-05-18

## Context

The IR stores raw godoc text in `TypeDef.Doc` and `Route.Doc`.
TS-target generators (tstypes, zod, tsclient) need JSDoc-format comments
in their output. The chi-basic golden establishes specific
transformations that the IR does not encode:

- The leading identifier (type name or handler name) is stripped.
- A copula following the identifier is stripped.
- The result has its first letter uppercased.
- Multi-sentence docs are truncated to the first sentence.

These rules belong in a shared generator helper, not in the analyzer
(which keeps godoc faithfully per the loud-failure principle) and not in
per-generator code (which would drift, against ADR 0022 §8).

## Decision

A helper implements the transformation:

```go
// JSDoc transforms a raw godoc comment for a Go identifier named
// typeName into a JSDoc-friendly summary string. Returns the body
// (without /** ... */ markers), or "" if no JSDoc should be emitted.
func JSDoc(typeName, rawDoc string) string
```

Algorithm:

1. Trim whitespace. Empty → return `""`.
2. Take the first sentence via `go/doc.Synopsis`. This handles
   version numbers (`v1.2`) and other dot-containing tokens correctly
   via the stdlib's own heuristics, rather than a naive
   "first period" split.
3. Tokenize the synopsis on whitespace.
4. If `token[0] == typeName` (case-sensitive), drop it.
5. If the next token is in `{is, are, was, were, represents,
   represent}`, drop it.
6. Empty result → return `""`.
7. Rejoin with single spaces; uppercase the first rune.
8. Return the result. The caller wraps it in `/** ... */`.

The copula set is small and English-only by design. Non-English godoc
and unusual phrasings produce a transformation that strips the
identifier but leaves the rest unchanged — degraded but not broken.

## Consequences

- JSDoc summaries are one-line. Multi-paragraph godoc loses detail in
  the generated output; users who want full docs read the Go source.
  Acceptable for v0.1.
- The transformation is heuristic, not parser-based. Edge cases exist
  (godoc not starting with the identifier, unconventional phrasing);
  they produce suboptimal-but-readable output, never broken output.
- Verified `go/doc.Synopsis` behavior (Go 1.26): every chi-basic golden
  doc is a single sentence and is returned whole (correct);
  `"… v1.2 API."` is not truncated (correct). One imperfection: when
  the first sentence ends in a single capital letter + period (e.g.
  `"… does X. More …"`), `Synopsis` treats `X.` as an
  abbreviation/initial and does NOT split there, over-including the
  next sentence. Not exercised by chi-basic; consistent with the
  heuristic-not-parser stance above.
- `go/doc.Synopsis` is deprecated since Go 1.19 (in favour of
  `(*doc.Package).Synopsis`) but remains present and functional in Go
  1.26; `go vet` does not flag it. Acceptable for v0.1; revisit only if
  the stdlib removes it (it won't soon) or a linter gate rejects
  deprecated stdlib calls.
- Same helper used by tstypes, zod, tsclient. goadapter emits Go and
  uses the raw godoc verbatim — no transformation.

## Alternatives considered

- Encode the transformation in the IR — rejected; the IR keeps
  source-faithful data per the loud-failure principle.
- Per-generator implementations — rejected per ADR 0022 §8 (no
  cross-generator drift).
- More sophisticated NLP (proper sentence splitting, language
  detection) — over-engineered for v0.1.
- The naive "first period followed by whitespace" splitter — rejected;
  breaks on `v1.2`-style version numbers, which `go/doc.Synopsis`
  handles correctly.
- Punt entirely (emit raw godoc) — rejected; the golden requires the
  transformation, and unstripped identifiers in JSDoc read badly.

## Cross-references

- Used by tstypes (current milestone), zod, tsclient. Lives in
  `internal/gen/` and is listed in [0022](0022-generator-conventions.md)
  §6.
- Not used by goadapter (Go output keeps raw godoc).
