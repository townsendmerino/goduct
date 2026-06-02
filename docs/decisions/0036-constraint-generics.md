# 0036. Constraint generics (v0.4)

**Status:** Accepted
**Date:** 2026-06-02

## Context

[ADR 0033](0033-generics.md) §1 capped v0.3 generics at the `any`
constraint:

> **`any` constraint only.** `[T any]` is supported; `[T Stringer]`
> or any other constraint loud-fails per ADR 0007 with a message
> naming the constraint and pointing at the v0.4 lift.

Real Go API code uses union constraints often enough to be worth
supporting (`[T int | int64 | float64]`, `[T int | string]` etc.).
Method-bearing interface constraints (`[T Stringer]`) and the
`comparable` constraint don't have a clean wire mapping — methods
don't survive JSON; `comparable` is a Go-typesystem-only contract.

## Decision

### 1. Scope: type-union constraints

Lift the v0.3 `any`-only cap for **type-union constraints** —
constraints whose underlying interface has only embedded *types*
(no methods, no `comparable`):

```go
type Box[T int | int64 | float64] struct { V T `json:"v"` }
type Either[T int | string]      struct { V T `json:"v"` }
```

Single-type union (`[T int]`) is the degenerate single-term form
and is also supported.

**Still rejected** (loud-fail with the same C1 category, updated
message):

- **Method-bearing interface constraints** (`[T fmt.Stringer]`,
  `[T io.Reader]`). Methods don't survive JSON serialization;
  there's no honest wire shape to emit.
- **`comparable`**. Go-typesystem-only; no wire meaning.
- **Constraint elements with `~` underlying-type approximation**
  (`[T ~int | ~string]`). Honoring `~` requires propagation rules
  goduct doesn't have a use for. Loud-fail with a v0.5 deferral.

### 2. IR shape — additive

```go
type TypeKind int

const (
    KindBuiltin TypeKind = iota
    KindNamed
    KindSlice
    KindMap
    KindTypeParam
    KindUnion // ADR 0036: a union-of-types, used in constraint refs.
)

type TypeRef struct {
    // ... existing fields ...

    // UnionTerms is set when Kind == KindUnion. Order matches the
    // source-declaration order of the union's terms. Each term is
    // itself a TypeRef — typically KindBuiltin (int, string, etc.)
    // or KindNamed (a user type referenced via an embed). v0.4 per
    // ADR 0036.
    UnionTerms []*TypeRef
}

type TypeDef struct {
    // ... existing fields ...

    // TypeParamConstraints is parallel-indexed to TypeParams: the
    // constraint applying to TypeParams[i] is TypeParamConstraints[i],
    // or nil for an `any` constraint. A single-term constraint is a
    // bare TypeRef (e.g. {Kind: KindBuiltin, Builtin: "int"}); a
    // multi-term union is {Kind: KindUnion, UnionTerms: [...]}. v0.4
    // per ADR 0036.
    TypeParamConstraints []*TypeRef
}
```

`TypeParamConstraints` is additive (zero-length / nil keeps the
v0.3 `any`-implies-no-constraint behavior). `KindUnion` is a new
`TypeKind` value, additive at the end of the iota block.

### 3. Analyzer behavior

In `internal/analyzer/types.go`'s constraint check:

- Walk the type-param's `Constraint().Underlying()` as `*types.Interface`.
- If it has any methods → loud-fail (`C1`, "method-bearing constraints
  not supported in v0.4; methods can't be expressed on the wire").
- If it has `Comparable() == true` and no embedded types → loud-fail
  (`C1`, "comparable constraint not supported in v0.4").
- For each embedded `*types.Union` term: extract the term's type via
  `term.Type()`. Reject if the term has `Tilde() == true` (the `~`
  approximation form) — loud-fail with the v0.5 pointer.
- Build a `*TypeRef` per term (reusing `fieldTypeRef`-style logic so
  builtins and named types are resolved consistently).
- If exactly one term, store as a single bare TypeRef. If multiple,
  wrap in `{Kind: KindUnion, UnionTerms: [...]}`.

The `any` case stays — empty interface (no methods, no embeddeds)
produces `nil` in `TypeParamConstraints[i]`, matching the v0.3 shape.

### 4. Generator rendering

**tstypes / tsclient / hooks** (TypeScript decl + reference):

For each type-param with a constraint:
- `any` (nil constraint): render `<T>` (current v0.3 behavior).
- Single-term constraint: render `<T extends <TS-of-term>>`.
- Multi-term union: render `<T extends <ts1> | <ts2> | ...>`
  with TS-types **deduplicated** in source order. E.g.
  `[T int | int64]` → `<T extends number>` (both terms are `number`);
  `[T int | string]` → `<T extends number | string>`.

The deduplication rule is *generator-local* per ADR 0022 §6 — each
TS generator computes the dedup'd union from its own builtin
table. This means `tstypes` and `tsclient` could in principle render
different unions for the same constraint; in practice their tables
agree on the primitives that matter for constraints.

**zod factory** (declaration):

Zod has no native generics; the factory takes `z.ZodTypeAny`
regardless of the Go-side constraint. **Unchanged from v0.3.** The
factory's TS-side parameter still annotates `<T extends
z.ZodTypeAny>` — runtime-level constraint enforcement isn't
goduct's concern (zod's `.parse(...)` validates the actual data).

**OpenAPI / Postman**:

Both flatten generics per-instantiation, so the declaration's
constraint never appears in either spec. **No change.**

### 5. Coverage

Synthetic-test fixture in `internal/analyzer/generics_test.go`
extended with:

- A `[T int | int64]` union-constrained generic that successfully
  loads and produces correct `TypeParamConstraints`.
- A `[T fmt.Stringer]` method-constrained generic that loud-fails
  with the documented v0.4 message.
- A `[T comparable]` generic that loud-fails.
- A `[T ~int]` tilde-form generic that loud-fails with the v0.5
  pointer.

tstypes / tsclient / hooks tests gain unit-level cases for the
TypeParamConstraints rendering rule.

chi-basic stays generics-free; the chi-basic golden coverage gap
queued in [TODO.md](TODO.md) still applies.

## Consequences

**Easy / unblocked:**

- Real-world generic patterns like `Page[T any]` AND numeric-bounded
  generics like `Box[T int | int64 | float64]` both work.
- IR additions are additive — v0.3 IR consumers keep working
  (TypeParamConstraints is empty, KindUnion is unused).
- Generator render rule is small (`<T extends X>` formation).

**Hard / giving up:**

- Method-bearing constraints stay rejected. Users who type-program
  with `Stringer`-style constraints in their HTTP API surface have
  no path; they restructure their types.
- The TS dedup rule may produce surprising unions for heterogeneous
  primitive types (`[T int8 | uint16]` → `<T extends number>`
  collapses both to `number`; the precise Go-side distinction is
  lost). Accept — same loss tstypes already takes for any int
  variant rendering as `number`.
- `comparable` and `~` continue to loud-fail. Tracked but not
  motivated.

## Alternatives considered

- **Rewrite `TypeDef.TypeParams` from `[]string` to `[]TypeParamDef`**
  — rejected. Breaking change for v0.3 consumers (the existing
  generators all expect `[]string`). The parallel
  `TypeParamConstraints` slice is the additive shape per ADR 0027.
- **Render method-bearing constraints by emitting Stringer as a
  TS interface** — rejected. Goduct's IR doesn't model interface
  types; doing so would expand the surface considerably for a
  feature with no wire-shape payoff.
- **Render `comparable` as `<T extends string | number | boolean>`**
  — rejected as misleading. `comparable` includes struct types,
  arrays of comparables, etc.; the TS approximation would be wrong.
- **Carry the constraint into the zod factory's `<T extends
  ZodSchema<X>>` shape** — rejected. zod's runtime validates the
  actual value via `.parse`; the static constraint at the TS
  factory's signature is a courtesy more than a contract.

## Cross-references

- [0022](0022-generator-conventions.md) §6 — generator-local
  type-string rendering; each TS generator dedups the union
  per its own builtin table.
- [0027](0027-enrich-ir-for-go-side-codegen.md) — additive-only
  IR contract; this ADR's TypeParamConstraints + KindUnion ride
  under it.
- [0033](0033-generics.md) — original generics ADR; this ADR
  lifts its §1 `any`-only cap for the union-constraint shape.
