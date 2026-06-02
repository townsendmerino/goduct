# 0032. Custom type adapters (v0.2)

**Status:** Accepted
**Date:** 2026-06-02

## Context

[ADR 0017](0017-special-stdlib-types.md) hardcoded five "special"
types (`time.Time`, `time.Duration`, `[]byte`, `json.RawMessage`,
`github.com/google/uuid.UUID`) — types whose JSON wire shape differs
from their Go field structure. Every other named, non-stdlib type
hits the analyzer's loud-fail with "use a string field with manual
conversion, or open an issue if this type should be added to ADR
0017."

That set is fine for v0.1 but quickly becomes hostile in real
projects:

- `github.com/shopspring/decimal.Decimal` — JSON-marshals as a
  string by default.
- `cloud.google.com/go/civil.Date` — date-only ISO 8601 string.
- `math/big.Int`, `math/big.Float` — string-marshaled.
- `net/url.URL` — string.
- `golang.org/x/text/language.Tag` — string.
- `github.com/gofrs/uuid.UUID` / `github.com/satori/go.uuid.UUID` —
  string (the user is on a non-google UUID library).

Every one of these is a one-line "this Go type is a JSON string"
declaration. Forcing users to either wrap fields in `string` or
amend ADR 0017 per new type is not the right user surface.

v0.2 lets users declare these mappings themselves at gen time.

## Decision

### 1. Declaration: `--adapter <qname>=<wire>` (repeatable CLI flag)

Users declare custom type adapters on the command line:

```
goduct gen ./api --out ./web --all \
  --adapter github.com/shopspring/decimal.Decimal=string \
  --adapter cloud.google.com/go/civil.Date=string \
  --adapter math/big.Int=string
```

`--adapter` is a repeatable flag (Go stdlib `flag.Value`
implementation; one declaration per invocation).

**Format**: `<qname>=<wire>`, split on the *first* `=` (qnames can
contain `=` only by accident; treating only the first as the
separator handles it).

- **`qname`**: full Go qualified type name, `<import-path>.<TypeName>`.
  Same form the analyzer uses as `ir.TypeRef.Builtin` for special
  types (e.g. `time.Time`, `github.com/google/uuid.UUID`).
- **`wire`**: one of `string`, `number`, `boolean`, `unknown`. This is
  the **JSON-wire** type produced by the user's `MarshalJSON` (or
  default encoding/json behavior for a primitive-shaped type).

Project config files (`goduct.toml` etc.) are **not** part of v0.2.
The CLI flag form is composable with bash/Makefile/justfile/CI;
config-file ergonomics is a follow-up if real projects accumulate
enough adapters to motivate it. TODO entry tracks this.

### 2. Precedence

Highest to lowest:

1. **Built-in** (ADR 0017): a user `--adapter time.Time=string` is
   redundant but not erroneous; the built-in wins, the user's
   declaration is silently ignored. (Erroring would be hostile: many
   users will add `--adapter time.Time=string` as a "safety" line
   not realizing it's already built in.)
2. **User-declared** (`--adapter`): the analyzer recognizes the qname
   and emits `ir.TypeRef{Kind: KindBuiltin, Builtin: qname}`;
   generators render per wire shape.
3. **Loud-fail** ([ADR 0007](0007-loud-failure-on-unsupported-input.md)):
   no built-in or user declaration matches → existing C3
   "type is not supported" error. The message gains a remediation
   pointer to `--adapter`.

### 3. IR shape

Additive (per [ADR 0027](0027-enrich-ir-for-go-side-codegen.md) the
IR is additive-only post-v0.1):

```go
type API struct {
    Routes      []Route
    Types       map[string]TypeDef
    SourceDirs  map[string]string
    CustomAdapters map[string]string // qname → wire shape, from LoadOptions
}
```

`analyzer.LoadOptions` gains a matching field:

```go
type LoadOptions struct {
    Tests      bool
    BuildTags  []string
    Dir        string
    CustomAdapters map[string]string  // qname → wire shape
}
```

The CLI parses `--adapter` flags into this map and passes it through
to `analyzer.Analyze`. `Analyze` copies it onto `api.CustomAdapters`
so generators have access.

### 4. Analyzer recognition

`fieldtypes.isSpecialBuiltin(t)` returns `(name, true)` when `t` is
one of ADR 0017's hardcoded set. A new sibling helper
`isAdaptedBuiltin(t, adapters)` returns `(qname, true)` when `t`'s
qualified name is in `adapters`. The two are consulted in
precedence order (built-in first) before the existing fall-through
checks (slice → element walk, struct → KindNamed, etc.).

Both helpers are called from the same two sites (`fieldTypeRef` and
`structfields`'s named-type gate). The adapters map is threaded
through via the existing `LoadOptions`-derived plumbing — the
analyzer already passes per-package context through; the adapters
ride on it.

### 5. Generator rendering

Per [ADR 0022](0022-generator-conventions.md) §6, target-language
type strings are generator-local. Each TS generator's `tsType` (or
equivalent) keeps its hardcoded switch for known builtins; the
`default:` case becomes:

```go
default:
    if wire, ok := api.CustomAdapters[ref.Builtin]; ok {
        return wireToTS(wire) // string→"string", number→"number", ...
    }
    panic("...: unknown builtin " + ref.Builtin)
```

The wire-to-TS / wire-to-zod mapping is a 4-entry table:

| Wire | TS | Zod |
| --- | --- | --- |
| `string` | `string` | `z.string()` |
| `number` | `number` | `z.number()` |
| `boolean` | `boolean` | `z.boolean()` |
| `unknown` | `unknown` | `z.unknown()` |

This 4-entry table is shared via `internal/gen` (per
[ADR 0022](0022-generator-conventions.md) §8 cross-generator helpers
go there): `gen.AdapterWireTS(wire)` and `gen.AdapterWireZod(wire)`.

**Go adapter** (goadapter): no rendering needed. The user's type
implements `MarshalJSON` (or it's a primitive-shaped type that
`encoding/json` handles natively); goadapter's wrapper just decodes
JSON into the request struct verbatim. The user already wrote the
import for their type in their handler file; goadapter doesn't need
to know.

### 6. Loud-fail message remediation

ADR 0017's existing error message:

> `goduct: type <X> is not supported in v0.1. Use a string field with
> manual conversion, or open an issue if this type should be added to
> ADR 0017.`

v0.2 update:

> `goduct: type <X> is not supported. Declare a custom adapter on
> the command line (e.g. --adapter <X>=string), or use a string
> field with manual conversion. See ADR 0032.`

### 7. Validation

CLI-time validation, before analysis runs:

- Wire value must be in `{string, number, boolean, unknown}`. Other
  values → exit 2 with the list of valid wires.
- Qname must contain at least one `.` (rules out obvious typos like
  `--adapter Decimal=string` without the package prefix). Other
  malformedness is caught implicitly when the analyzer fails to
  match anything against the qname (silently — no Go type with that
  name means the adapter is unused, which is fine).
- Built-in qnames are silently allowed but a no-op (precedence rule 1).

### 8. v0.2 limitations

- **Wire shape only.** No TS-type narrowing (a `decimal.Decimal`
  field is `string` in `types.ts`, not a `Decimal` class). Users
  wanting that wrap goduct's output by hand on the client.
- **No per-field overrides.** All fields of a given qname use the
  same wire shape. A field that needs different treatment than its
  type's adapter must use a different Go type.
- **No transformation hooks.** goduct doesn't generate
  encode/decode helpers — the user's `MarshalJSON` is the source of
  truth.

These are deliberate scope caps for v0.2; v0.3+ can lift any that
real usage motivates.

## Consequences

**Easy / unblocked:**

- Users with `decimal.Decimal`, `civil.Date`, `big.Int`, third-party
  `uuid.UUID` libraries, `net/url.URL`, `language.Tag` etc. drop in
  a one-line `--adapter` and goduct stops loud-failing.
- Adding ADR 0017 entries for new "stdlib + popular" types is no
  longer the only path. ADR 0017's set can stay frozen at its v0.1
  five.
- The IR + LoadOptions plumbing is small (one field on each); ADR
  0027 already pinned the additive-only stance.

**Hard / giving up:**

- CLI lines grow long in projects with many adapters. Mitigated by
  bash/Makefile aliasing; a config file is the obvious follow-up.
- Wire-shape-only means TS types stay loose for adapted fields.
  Users who care wrap on the client; v0.3 can revisit.
- An adapter that's *declared* but unused (no Go type matches the
  qname) is silently a no-op. Considered making this a hard error
  but rejected: adapters declared in a shared CI script across
  multiple projects shouldn't error on the project that happens not
  to use one.

## Alternatives considered

- **Project config file (`goduct.toml`)** — rejected for v0.2. Adds
  a parser dep; CLI flag composability is sufficient for the
  near-term user need. Revisit when projects accumulate >5 adapters
  and the CLI becomes unwieldy. Tracked as a follow-up TODO.
- **Per-field struct-tag annotation** (`\`goduct:"as=string"\``) —
  rejected. Per-field declaration is verbose for projects with
  many fields of the same type; doesn't help when the same type
  appears in many structs; and it conflates the type's wire shape
  (a type-level fact) with a field-level concern.
- **Full TS-type / zod expression in the adapter declaration** —
  rejected for v0.2 (4-cell wire-shape table is enough for the
  common cases; richer surface invites confusion about precedence
  and escaping).
- **Auto-detect MarshalJSON return-type** — rejected. Brittle (the
  return shape isn't obvious to static analysis), magic, and ties
  user code to goduct's quirks.
- **Error rather than silently ignore an adapter on a built-in
  qname** — rejected. Hostile for the common "safety" duplicate.
- **Error rather than silently ignore an unused adapter** — rejected
  for the shared-CI-script case described above.

## Cross-references

- [0007](0007-loud-failure-on-unsupported-input.md) — the loud-fail
  this ADR extends a remediation pointer to.
- [0017](0017-special-stdlib-types.md) — the built-in special-type
  table; user adapters extend it without amending it.
- [0022](0022-generator-conventions.md) — §6 (type-string translation
  generator-local) and §8 (shared helpers → `internal/gen`).
- [0027](0027-enrich-ir-for-go-side-codegen.md) — additive-only IR
  stance that `CustomAdapters` rides under.
