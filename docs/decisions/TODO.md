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

**Post-v0.1.0 polish session (2026-06-02):** four items resolved —
Format A error-prefix normalize (ADR 0019 Implementation note marked
done); `uuid.UUID` real-import test (synthesized `*types.Named`, no
new dep); `*types.Alias` audit (invariant comment recorded at the
single kind-switch in `fieldtypes.go`); v0.2 IR enrichment
(`ir.Route.RequestType` + `ir.API.SourceDirs` added per
[ADR 0027](0027-enrich-ir-for-go-side-codegen.md), which supersedes
ADR 0026 — goadapter and CLI refactored to use them, both goldens
byte-identical). Three follow-ups remain (below).

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

## [ ] Raw http.HandlerFunc mode: chi-basic golden coverage

[ADR 0031](0031-raw-handlerfunc-mode.md) ships the analyzer + goadapter
support for `ir.ModeRaw` with unit-test coverage on synthetic packages.
chi-basic stays idiomatic-only — adding a raw handler would touch every
TS golden (types.ts, schemas.ts, client.ts, hooks.ts) and all four
goadapter goldens (chi, gin, echo, mux — the latter two would also
need their loud-fail behavior confirmed end-to-end).

**Trigger / action:** add either (a) one raw handler to chi-basic with
the full golden update sweep, or (b) a focused `examples/raw-basic/`
example. Either route exercises the raw path end-to-end. Spec-trust
applies until then.

## [ ] gin/echo raw-mode support

ADR 0031 §3 defers gin/echo raw mode: their handler signatures
(`func(c *gin.Context)`, `func(c echo.Context) error`) don't match
`http.HandlerFunc`, so the user's raw handler can't be registered
directly. v0.2 loud-fails; v0.3+ could synthesize a small adapter
that converts each framework's context to `(w, r)` and calls the
user's function. **Risk: low** — most users picking raw mode are
already on chi/mux for the `http.HandlerFunc` shape.

## [ ] Custom type adapters: chi-basic golden coverage

[ADR 0032](0032-custom-type-adapters.md) ships the `--adapter` flag +
analyzer + generator support, tested via a synthetic `math/big.Int`
fixture in `internal/analyzer/adapters_test.go`. chi-basic itself has
no adapter-eligible field (no third-party rich types in the example);
adding one (e.g. `net/url.URL` with `--adapter net/url.URL=string`)
would exercise the full pipeline against goldens but touch every TS
golden + all 4 adapter goldens. Same shape as the raw-HandlerFunc
coverage gap.

**Trigger / action:** add a coverage example exercising one adapter
on a wire-visible field, OR accept the synthetic-test coverage as
sufficient and close this entry. Spec-trust applies until then.

## [ ] Custom type adapters: project config file (`goduct.toml`)

[ADR 0032](0032-custom-type-adapters.md) ships `--adapter` as a
repeatable CLI flag. Real projects with >5 adapters will want to
declare them once in a project-root config file rather than threading
them through every Makefile target / CI step.

**Trigger / action (v0.3 or whenever it bites):** add a minimal
config-file reader (TOML or hand-rolled key-value) at the project
root (e.g. `goduct.toml`); CLI `--adapter` extends/overrides the file.
Compose: file is the project default; flag is the per-invocation
override.

## [ ] goadapter: custom status-code mapping incomplete

goadapter's `http.Status*` mapping covers 200/201/204 — the only codes
the analyzer produces via ADR 0014's status defaults. An explicit
`goduct:status` (e.g. 418, 422) is unmapped and loud-fails (panic per
ADR 0022 §5), which is acceptable v0.1 behavior (ADR 0007) and
documented for users in the README "Known v0.2 polish" caveat.
**Trigger / action (v0.2):** map the full `net/http` `Status*` set, or
formalize the loud-fail in an ADR.
