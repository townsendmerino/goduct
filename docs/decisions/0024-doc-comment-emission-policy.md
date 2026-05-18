# 0024. Per-generator doc-comment emission policy

**Status:** Accepted
**Date:** 2026-05-18

## Context

Three TS-target generators (tstypes, zod, tsclient) consume the same IR
with the same doc-comment text on `TypeDef`s and `Route`s. Whether to
emit doc comments is a per-generator decision, not a global one. The
chi-basic golden makes this explicit:

- `types.ts`: JSDoc present on every documented type
- `schemas.ts`: no doc comments anywhere
- `client.ts`: JSDoc present on every method

Without an ADR, each generator would either re-derive the rule from its
golden or, worse, call `internal/gen.JSDoc` inconsistently. This ADR
pins the policy.

## Decision

Per-generator emission policy for doc comments:

- **tstypes:** emit JSDoc on every `TypeStruct`, `TypeEnum`, `TypeAlias`
  that has a non-empty `Doc` field. Use `internal/gen.JSDoc` for
  transformation (ADR 0023). Field-level JSDoc on individual struct
  fields per the chi-basic golden.
- **zod:** emit NO doc comments. Schemas are runtime validators; their
  accompanying types in `types.ts` carry the documentation. Duplicating
  it in `schemas.ts` adds noise without value. The chi-basic golden's
  `schemas.ts` has zero comments.
- **tsclient:** emit JSDoc on every method. Method-level `Route.Doc` is
  transformed via `internal/gen.JSDoc` with the handler name as the
  identifier-strip target. No JSDoc at the tag-group level. (To be
  verified against the `client.ts` golden when the tsclient milestone
  lands; the chi-basic golden suggests method-only.)
- **goadapter (Go):** emit raw godoc unchanged on generated registration
  functions. Go has its own godoc convention; JSDoc transformation
  doesn't apply. The chi-basic adapter golden does NOT preserve handler
  godoc on the generated wrappers (the wrappers are internal-ish), so in
  practice goadapter emits no doc comments either — to be confirmed when
  goadapter lands.

Rationale: doc comments serve different audiences in different files.
Users reading `types.ts` want to understand the data shapes; JSDoc there
helps their IDE tooltips. Users reading `schemas.ts` are either tooling
(which doesn't need docs) or debugging validation behavior (which the
source godoc already documents in the `.go` file). Users reading
`client.ts` are calling the API and want to know what each method does;
JSDoc on methods is exactly the right surface.

## Consequences

- `zod.go` does not import or call `internal/gen.JSDoc`. `zod_test.go`
  asserts no `/**` substring appears in the generated output.
- tsclient and goadapter make their own concrete choices when those
  milestones land; this ADR documents the expected answer for each but
  allows the golden to override (loud-failure discipline).
- If a future generator (svelte, swift) is added, its prompt must
  explicitly state its doc-comment policy. There is no silent default.

## Alternatives considered

- Always emit doc comments everywhere — rejected; `schemas.ts` would
  have noise, and it contradicts the chi-basic golden.
- Never emit doc comments anywhere — rejected; loses real user-facing
  value in `types.ts` and `client.ts`.
- Make it configurable — rejected; v0.1 needs no configurability and
  this is exactly the kind of "obviously right per file" decision that
  shouldn't be pushed to user config.

## Cross-references

- [0022](0022-generator-conventions.md) §6 (per-target rendering is
  generator-local)
- [0023](0023-godoc-to-jsdoc-transformation.md) (the JSDoc
  transformation itself)
