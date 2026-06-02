# Post-v0.1 follow-ups

v0.1.0 shipped (milestone 14, 2026-05-18). The pre-v0.1 reconciliation
queue was burned down: the README and ADRs were aligned to shipped
reality (ADR 0017 type table, ADR 0006 `oneof` Consequences amendment,
ADR 0008 `--hooks` deferral, ADR 0022 §1 `Generate` signature, raw
`http.HandlerFunc` marked v0.2 per ADR 0001/0014). The items below are
the remaining **non-blocking** follow-ups — none gates a release; each
has a concrete trigger.

This is not an ADR — ADRs record decisions; this records implied work
not yet done. Remove an item when it is reconciled (and, if it required
a decision, record that decision in an ADR).

## [ ] Named-alias-of-named collapses to a fresh TypeStruct

`type A B` (where `B` is a struct) emits as a fresh `TypeStruct` with
`B`'s resolved field set, not as `TypeAlias → B`.
`types.Named.Underlying()` peels named chains, so the traversal cannot
syntactically distinguish `type A B` from `type A struct { …same… }`.
Wire shape and generator output are **identical**; the only loss is
**dedup** — if both `A` and `B` are referenced, generators emit two
identical TS interfaces instead of `type A = B`.

Documented for users in the README "Known v0.2 polish" caveat.
**Trigger / action:** not user-facing-broken; will bite with many
aliases of one struct. Resolving needs distinguishing the syntactic
alias from a re-declaration (token/AST-level, since `Underlying()`
doesn't preserve it). Investigate if it becomes a real pain point. No
ADR needed.

## [ ] Spec-trust coverage gaps (zod, tsclient, goadapter)

Implemented per spec but not exercised by the chi-basic golden.
Documented for users in the README "Spec-trust caveats". **Trigger /
action:** add an `examples/coverage/` example (or extend chi-basic)
that exercises these, then convert to golden assertions.

- **zod** (7 paths): multi-validator chain ordering; `url`/`len`
  validators; `uint` → `z.number().int().nonnegative()`; `int` on
  wire-visible fields; int-enum `z.union([z.literal(...)])`;
  `TypeAlias` and D5 slice/map-alias paths. (`oneof` is *not* here —
  it is unimplemented in v0.1, see the ADR 0006 Consequences amendment
  and the README; it is a v0.2 *feature*, not a coverage gap.)
- **tsclient:** path+query merged into one `params` object (path
  members then query, `; `-joined; path required, query per
  `Param.Optional`). Golden covers path-only, query-only, body-only,
  path+body, error-only — but not path+query(+body) combined.
- **goadapter:** `bool`/`float` query-param conversion
  (`strconv.ParseBool`, `strconv.ParseFloat(v, 64)`, messages
  `"<wire> must be a boolean"` / `"<wire> must be a number"`). Golden
  exercises only `int` (`ListUsers.Limit` via `strconv.Atoi`).

## [ ] v0.2: enrich the IR for Go-side codegen (RequestType + source dir)

Two v0.1 workarounds share one root cause: `ir.API`/`ir.Route` don't
carry enough position/identity info for Go-side code generation.

1. **Request type.** `ir.Route` has `BodyType` (nil for non-body
   routes) but no `RequestType`. goadapter works around this via the
   v0.1 naming convention in
   [ADR 0026](0026-goadapter-request-type-name-convention.md).
2. **Source directory.** The Go adapter must be written into the
   handlers' package directory (ADR 0009), but nothing on `*ir.API`
   exposes that path. `cmd/goduct/main.go` derives it by parsing
   `Route.Pos` (`"file:line:col"`).

**v0.2:** add `RequestType *TypeRef` to `ir.Route` (populated by
`DiscoverRoutes`) **and** a stable per-package source directory on
`ir.API`. goadapter then reads the request type directly (the naming
convention falls away) and the CLI reads the source dir directly (the
`Route.Pos` parse in main.go is deleted). One additive,
backward-compatible IR change fixes both.

## [ ] goadapter: custom status-code mapping incomplete

goadapter's `http.Status*` mapping covers 200/201/204 — the only codes
the analyzer produces via ADR 0014's status defaults. An explicit
`goduct:status` (e.g. 418, 422) is unmapped and loud-fails (panic per
ADR 0022 §5), which is acceptable v0.1 behavior (ADR 0007) and
documented for users in the README "Known v0.2 polish" caveat.
**Trigger / action (v0.2):** map the full `net/http` `Status*` set, or
formalize the loud-fail in an ADR.
