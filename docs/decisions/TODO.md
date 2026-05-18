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

## [ ] Audit `*types.Type` kind switches for Alias unwrapping

Go 1.22+ alias types (`*types.Alias`, enabled by default in 1.24+) mean a
type switch on a `types.Type` can miss the real kind: `any`/`interface{}`
and `type Foo = Bar` arrive as `*types.Alias`, not `*types.Interface` /
the aliased type. `fieldtypes.go` already handles this (`types.Unalias(t)`
before switching, after pointer unwrap).

**Action:** audit every `*types.Type` kind switch for the same hazard —
`structfields.go` and any future type-walking code (notably the Part 2
traversal). Pattern: call `types.Unalias(t)` before switching on kind.
Pure code-hygiene sweep; no ADR needed.

## [ ] `uuid.UUID` detection has no real-import test

`isSpecialBuiltin`'s `github.com/google/uuid.UUID` arm
(`structfields.go`/`fieldtypes.go`) is exercised only by the
qualified-name unit dispatch, not by a real `github.com/google/uuid`
import. The dep was deliberately not added (not worth the bloat/precedent
for one three-line, branch-free switch arm).

**Action (pre-v0.1):** either synthesize a `*types.Named` in a unit test
(fake `Pkg` with path `github.com/google/uuid`, name `UUID`) and assert
`isSpecialBuiltin`, or add the dep with a real-import integration test.
**Risk: low** — the qualified-name switch is three lines with no
branching. Known, named, bounded; fix when the cost is justified by use.

## [ ] Named-alias-of-named collapses to a fresh TypeStruct

`type A B` (where `B` is a struct) currently emits as a fresh
`TypeStruct` with `B`'s resolved field set, not as `TypeAlias → B`.
`types.Named.Underlying()` peels named chains, so the type traversal
cannot syntactically distinguish `type A B` from
`type A struct { ...same fields... }`. Wire shape and generator output
are **identical** (encoding/json and the TS interface don't care); the
only loss is **dedup** — if both `A` and `B` are referenced separately,
generators emit two identical TS interfaces instead of `type A = B`.

**Action:** not user-facing-broken; a polish concern that will bite with
many aliases of one struct. Resolving needs distinguishing the syntactic
alias from a re-declaration (token/AST-level analysis, since
`Underlying()` doesn't preserve it). Investigate if it becomes a real
pain point. Tracked, not blocking; no ADR needed.

## [ ] `Generate` signature drift: value vs pointer

[ADR 0003](0003-generators-as-pipeline.md) and `README.md` state the
generator entrypoint as `Generate(ir.API, io.Writer)` (value);
[ADR 0022](0022-generator-conventions.md) §1 pins
`Generate(*ir.API, io.Writer) error` (pointer). The pointer form is
correct — it matches `Analyze`'s `*ir.API` return and avoids copying the
IR. The contract is currently stated two ways.

**Action (pre-v0.1):** reconcile the docs —
- ADR 0003: amend the Decision text to the pointer form.
- `README.md`: update any `Generate(...)` signature mentions.
Pure docs; ADR 0022 §1 is authoritative in the meantime.

## [ ] zod generator: 7 code paths are spec-only, not golden-verified

The chi-basic `schemas.ts` golden does not exercise these zod paths;
they are implemented per the Prompt 9 table + ADR 0017 (spec-trust),
not byte-verified:

1. Multi-validator chain ordering (implemented source-order; golden has
   no field with ≥2 effective validators).
2. `oneof` translation — deferred entirely (unreachable in v0.1).
3. `url` / `len` validators (never exercised).
4. `uint` builtin rendering → `z.number().int().nonnegative()`
   (no uint field emitted).
5. `int` builtins on wire-visible fields (none; `Limit` is filtered out
   by `EmitTS`).
6. Int-enum form `z.union([z.literal(...)])` (chi-basic has only a
   string enum).
7. `TypeAlias` and D5 slice/map-alias paths (none in the emitted set).

**Action (pre-v0.1):** add an `examples/coverage/` example (or extend
chi-basic) that exercises these, OR explicitly accept the v0.1 risk in
the README's "What's supported" section. Accepted as spec-trust for the
v0.1 ship; this keeps the gap visible.

## [ ] Generators: panic-on-unknown-builtin is a required shared pattern

All generators panic on an unknown `ir.TypeRef` builtin or unhandled
`Kind`. Pattern established by `tstypes.tsType` and `zod.zodExpr`;
intentional per ADR 0022 §5 (internal-invariant violation = loud
failure). When tsclient and goadapter implement their target-language
type-string functions, they MUST replicate the same pattern: panic with
a message naming the unhandled value, so an analyzer/IR bug surfaces
immediately rather than propagating into output. This is a pattern note,
not a decision; the underlying decision is ADR 0022 §5.

## [ ] tsclient: path+query argument-merge form is spec-trust

chi-basic has no route with BOTH path AND query params, so the merge
form is unverified by golden. tsclient implements path+query merged into
one `params` object (path members then query members, joined by `; `;
path required, query per `Param.Optional`). Exercised by the golden:
path-only, query-only, body-only, path+body, error-only — but NOT
path+query(+body) combined.

**Action (pre-v0.1):** add a coverage example exercising a route with
both path and query params, OR explicitly accept the gap in the
README's "What's supported" section. Accepted spec-trust for v0.1.

## [ ] v0.2: add `ir.Route.RequestType`

`ir.Route` has `BodyType` (wire body, nil for non-body routes) but no
`RequestType` (the handler's second-param type, always present).
goadapter works around this via the v0.1 naming convention in
[ADR 0026](0026-goadapter-request-type-name-convention.md). v0.2: add
`RequestType *TypeRef` to `ir.Route`, populated by `DiscoverRoutes`
(which already has the handler signature), consumed by goadapter. The
convention falls away; any handler may use any request type name.
Additive, backward-compatible IR change.
