# Pre-v0.1-release reconciliations

A running checklist of follow-ups to settle **before tagging v0.1**. This is
not an ADR — ADRs record decisions; this records work the decisions implied
but that has not been done yet. Remove an item when it is reconciled (and,
if it required a decision, record that decision in an ADR).

## [ ] README "What's supported" vs ADR 0017

`README.md` → "What's supported (v0.1)" lists only `time.Time` and `[]byte`
as rich/special types. [ADR 0017](0017-special-stdlib-types.md) also blesses
`time.Duration`, `json.RawMessage`, and `github.com/google/uuid.UUID`.

**Action:** update the README's supported-types list to match ADR 0017's
table (and its explicit out-of-scope list) so the advertised feature set and
the decisions agree. Pure docs change; no ADR needed.

## [ ] Normalize Format A error prefixes

The A-vs-B harmonization question is **settled** by
[ADR 0019](0019-error-message-formats-by-layer.md): two formats by layer
(Format A single-line for whole-construct errors; Format B 3-line
categorized for field errors), divergence accepted by design. One residual
remains — not every Format A emitter matches the template byte-for-byte:

- `annotations.go` currently emits: `goduct: <msg> (line N): <src>`
- `loader.go` currently emits: `<pkgpath>: [<kind>] <file:line:col>: <msg>`
- ADR 0019 establishes the Format A template as:
  `goduct: <file>:<line>:<col>: <msg>`
- Route discovery (`internal/analyzer/routes.go`) already matches it.

**Decision (per ADR 0019, still open):** for v0.1, either keep the two
layers' existing prefixes as-is (they are Format-A *category*, just not
byte-identical), **or** normalize them to the template. Pre-v0.1 release
work; if normalized it is a code change — record the choice in ADR 0019's
Implementation note (or a short follow-up ADR).
