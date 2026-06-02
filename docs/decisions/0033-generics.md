# 0033. Generics in request/response types (v0.3)

**Status:** Accepted
**Date:** 2026-06-02

## Context

v0.1 / v0.2 rejected generic type instantiations via the analyzer's
C1 loud-fail ("generic type instantiation … is deferred to v0.2").
Real Go API code reaches for generics for unsurprising reasons:

```go
type Page[T any] struct {
    Items      []T    `json:"items"`
    NextCursor string `json:"nextCursor,omitempty"`
}

type Result[T any, E any] struct {
    OK    *T `json:"ok,omitempty"`
    Error *E `json:"error,omitempty"`
}

// Used across many handlers:
func ListUsers(...)   (*Page[User], error)
func ListProjects(...) (*Page[Project], error)
```

Without generics support users either flatten by hand (duplicate
`UserPage`, `ProjectPage`, `OrderPage`) or reach for `any`/`interface{}`
which goduct already rejects (B2). v0.3 fixes this.

## Decision

### 1. Scope (what's in)

- **Generic struct types** (the vast majority of real use).
  `type Page[T any] struct{...}` and `type Result[T, E any] struct{...}`.
- **Multi-parameter generics.** `[K, V]`-shaped types (e.g. `Map[K, V]`,
  `Result[T, E]`) are first-class. Param arity is whatever the type
  declares.
- **`any` constraint only.** `[T any]` is supported; `[T Stringer]` or
  any other constraint loud-fails per ADR 0007 with a message naming
  the constraint and pointing at the v0.4 lift.

### 2. Scope (what's out)

- **Generic handlers themselves.** Handlers stay concrete
  `func(ctx context.Context, T) (*U, error)` per ADR 0014. The
  arguments and return type may be **instantiations** of generic types
  (`*Page[User]`); the function itself cannot have type params.
- **Generic enums and aliases.** `type Status[T any] string` /
  `type Opt[T any] = *T` are out of scope. Rare in practice; lifts
  in v0.4+ if a real use case surfaces. Loud-fail with a clear
  message when encountered.
- **Non-`any` constraints.** A handler returning `*List[T Stringer]`
  loud-fails. Justification: rendering constraint-aware TS / zod is
  substantial extra work for a feature most users don't reach for in
  HTTP API types. v0.4+ when motivated.

### 3. IR shape — additive (per ADR 0027's post-v0.1 stance)

```go
type TypeDef struct {
    // ... existing fields ...

    // TypeParams names the generic type parameters declared on this
    // type (e.g. ["T"] for Page, ["K","V"] for Map). nil/empty for
    // non-generic types. Order matches the source declaration.
    TypeParams []string
}

type TypeRef struct {
    // ... existing fields ...

    // TypeParam is the param name when Kind == KindTypeParam (a
    // reference to "T" inside the generic's field list).
    TypeParam string

    // TypeArgs carries the concrete type arguments for a generic
    // instantiation. Non-empty only when Kind == KindNamed AND
    // Named refers to a generic type. Position matches the named
    // type's TypeParams order.
    TypeArgs []*TypeRef
}

const (
    KindBuiltin TypeKind = iota
    KindNamed
    KindSlice
    KindMap
    KindTypeParam // ADR 0033: reference to a type parameter inside
                  // a generic type's field list
)
```

A generic *declaration* (Page itself, the user's `type Page[T any]
struct{...}`) appears in `api.Types` exactly once with `TypeParams: ["T"]`
and field types referencing `KindTypeParam{TypeParam: "T"}` where T
appears.

A generic *instantiation* (the field type `*Page[User]`) is a
`TypeRef` with `Kind: KindNamed, Named: "<pkg>.Page", TypeArgs: [User]`.

### 4. Analyzer behavior

`fieldtypes.fieldTypeRef` is extended:

- `*types.TypeParam` → emit `TypeRef{Kind: KindTypeParam, TypeParam:
  tp.Obj().Name()}`. Encountered when walking a generic type's field
  list. No recursion.
- `*types.Named` with `TypeParams().Len() > 0 && TypeArgs().Len() == 0`
  (raw generic reference, e.g. `Page` without args): loud-fail. v0.3
  doesn't support naming a generic type uninstantiated; if encountered
  the user has a bug or hit a corner case worth flagging.
- `*types.Named` with `TypeArgs().Len() > 0` (instantiation): emit
  `TypeRef{Kind: KindNamed, Named: <qname>, TypeArgs: [<recursive
  fieldTypeRef on each arg>]}`. The C1 panic is gone.

`types.DiscoverTypes` traverses the *generic origin* (`n.Origin()`)
once when adding a generic to `api.Types`, not each instantiation. The
TypeDef carries `TypeParams` populated from
`origin.TypeParams()`. Field types in the TypeDef are taken from the
generic's underlying struct as-declared, so `KindTypeParam` refs land
naturally.

**Constraint check**: for each type param, the analyzer asserts the
constraint is exactly the empty interface (`any` / `interface{}`).
Anything else returns a C-category error naming the constraint, the
param, and the v0.4 deferral pointer.

### 5. Generator rendering

**tstypes:**
- Declaration: `export interface Page<T> { items: T[]; nextCursor?: string }`.
  Multi-param: `<K, V>`.
- Reference: `Page<User>`. Multi-arg: `Map<string, User>`.
- A `KindTypeParam{TypeParam: "T"}` renders as the bare `T`.

**zod:**
- Declaration as a factory function:
  ```ts
  export const Page = <T extends z.ZodTypeAny>(t: T) =>
    z.object({ items: z.array(t), nextCursor: z.string().optional() });
  export type Page<T extends z.ZodTypeAny> = ReturnType<typeof Page<T>>;
  ```
  Multi-param: `<K extends z.ZodTypeAny, V extends z.ZodTypeAny>`.
- Reference: `Page(User)` (invocation form). Multi-arg: `Map(z.string(), User)`.
- `KindTypeParam` renders as the param name (`t` for "T", `k` / `v`
  for "K" / "V"). The wire-shape table doesn't apply at the param
  layer; the *caller* supplies the concrete zod schema.

  Note: zod has no native generics. The factory pattern is the
  established workaround. Users importing `Page` get
  `(t) => z.object(...)`; they invoke `Page(User)` to get the
  schema for that instantiation. tsclient + hooks call this form
  inline for `.parse(data)`.

**tsclient / hooks:**
- Method return type: `Promise<Page<User>>` (TS type lookup; same
  rendering as tstypes).
- Body: parsed via `schemas.Page(schemas.User).parse(data)` instead
  of `schemas.PageUser.parse(data)`. The composition reaches
  arbitrarily-deep instantiations (`Page<Result<User, Error>>` →
  `schemas.Page(schemas.Result(schemas.User, schemas.Error)).parse(...)`).

**goadapter:** untouched. Generics are a wire-shape concern; the user
already wrote `*Page[User]` in their Go signature, and `encoding/json`
handles the serialization. The Go adapter wrapper passes through
without needing to know.

### 6. Coverage approach for v0.3 ship

Adding a generic type to chi-basic would touch every existing golden
(types.ts, schemas.ts, client.ts, hooks.ts, all 4 framework adapters).
v0.3 ships with **synthetic-test coverage** (a `Page[T]` fixture +
analyzer + generator assertions), same pattern as ADR 0031 (raw
HandlerFunc) and ADR 0032 (custom adapters). A chi-basic refactor
(e.g. replace `ListUsersResponse` with `Page[User]`) is queued as a
follow-up TODO; the integration is mechanical once the analyzer +
generators are byte-stable.

## Consequences

**Easy / unblocked:**

- `Page[T]`, `List[T]`, `Optional[T]`, `Result[T, E]` patterns work
  natively across many handlers without per-instantiation flat types.
- IR additions are zero-impact for users who don't use generics: nil
  TypeParams, nil TypeArgs, zero-value TypeParam — pre-v0.3 codepaths
  unchanged.
- The same generic shape is declared once in `types.ts` /
  `schemas.ts` and reused for every instantiation: no code bloat.

**Hard / giving up:**

- zod has no native generics; the factory-function pattern is more
  verbose at call sites than the v0.2 const-and-`.parse` form.
  Accepted — it's the only working zod idiom and is what real zod
  users already do.
- Constraint support is `any`-only. Users with type-parameter
  constraints get a loud-fail with the v0.4 deferral message.
- Generic enums / aliases / generic handler signatures loud-fail.
  The scope cap is explicit; lift in v0.4+ when real usage demands.
- chi-basic stays free of generics in this ship. The integration is
  TODO-queued.

## Alternatives considered

- **Eagerly instantiate every generic use at analyzer level**
  (Page[User] becomes a flat TypeDef "PageUser") — rejected.
  Produces TS code with N copies of the same shape; loses the
  `Page<User>` syntactic relationship that TS users expect.
- **Generate per-instantiation zod consts (`PageOfUser = Page(User)`)**
  alongside the factory — rejected for v0.3. Adds noise to the
  schemas.ts file for a saving callers don't measurably feel
  (`Page(User).parse(...)` works fine inline).
- **Support generic handlers themselves** (`func GetItem[T any](...)`)
  — rejected. Concrete entry points are easier to route, easier to
  reason about, and ADR 0014 already pins this. Users who want
  parameterized behavior write a small per-T concrete wrapper.
- **Support non-`any` constraints** in v0.3 — rejected. Constraint-
  aware TS rendering is meaningful extra work (TS `<T extends X>` is
  one thing; matching to the goduct type system another). Defer to
  v0.4+; loud-fail with remediation in the meantime.
- **Carry both the generic origin and per-instantiation TypeDefs in
  `api.Types`** — rejected. Bloats the IR; the origin is enough,
  generators compute the rest at emit time.

## Cross-references

- [0014](0014-handler-signature-strictness.md) — handler signature
  stays concrete; generics live in the request/response *types*, not
  in the handler.
- [0017](0017-special-stdlib-types.md) — special types take precedence
  over generic recognition (the `time.Time` etc. table runs first).
- [0022](0022-generator-conventions.md) §6 — type-string translation
  generator-local; each generator's rendering of `<T>` etc. is its
  own concern.
- [0027](0027-enrich-ir-for-go-side-codegen.md) — additive-only
  post-v0.1 IR contract; this ADR's IR fields ride under it.
- [0032](0032-custom-type-adapters.md) — adapter precedence: a
  custom-adapted qname is recognized as `KindBuiltin` and never
  reaches the generic-instantiation path; the two surfaces don't
  collide.
