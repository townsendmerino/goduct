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

## [ ] Analyzer error-message format harmonization

Two error formats currently coexist in the analyzer:

- **Route discovery** (`internal/analyzer/routes.go`) emits a single line:
  `goduct: <file>:<line>:<col>: <message>`.
- **Type traversal** ([ADR 0018](0018-type-traversal-failure-boundaries.md))
  mandates a three-line form:
  `goduct: <file>:<line>:<col>: <category-id>: <description>` /
  `in <qualified-field-name> (<Go-type>)` / `hint: <one-line remediation>`.

**Decision to make (then reconcile docs):** either

1. **Harmonize** — backport the category-id + `in …` + `hint:` shape to
   route-discovery errors so the whole analyzer speaks one format
   (route-discovery cases would need category IDs assigned), **or**
2. **Accept the divergence** — keep route discovery single-line and document
   that the richer format is type-traversal-specific.

Whichever is chosen, record it in an ADR and update any ADR/prompt text that
implies a single consistent format. Do this before v0.1 so error UX is
settled, not retrofitted after users see it.
