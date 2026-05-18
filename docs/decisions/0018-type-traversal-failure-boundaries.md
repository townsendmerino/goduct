# 0018. Type-traversal failure boundaries

**Status:** Accepted
**Date:** 2026-05-17

## Context

Type traversal (next milestone) walks Go struct fields recursively to
populate `ir.API.Types`. Go's type system contains many constructs that have
no clean wire representation or that we deliberately defer for scope reasons.
[0007](0007-loud-failure-on-unsupported-input.md) establishes the principle
of loud failure; this ADR enumerates exactly when type traversal triggers
it, in what category, and with what error message shape. The goal: the
milestone prompt specifies the algorithm and this ADR specifies the
boundaries.

### Definitions

- **Hard error** — the analyzer rejects the package; no IR is produced for
  the affected route(s) and an `errors.Join` entry is returned. Generators
  never see the affected types.
- **Deferred** — same as a hard error, but the message explicitly names a
  future ADR/milestone where support is planned. Distinguishes things we
  want to do later from things we don't intend to do.
- **Silently allowed** — no error; the field is included normally. Used
  sparingly.

## Decision

Type traversal triggers errors per the categories below. Every error message
must include the field's `file:line:col`, the field's qualified Go name
(e.g. `api.User.Profile`), and a short remediation hint.

### Category A — Go types with no wire representation (HARD ERROR)

- **A1.** Channel field (`chan T`) — "channels cannot be serialized; remove
  the field"
- **A2.** Function field (`func(...)`) — "functions cannot be serialized;
  remove the field"
- **A3.** `complex64`, `complex128` — "complex numbers cannot be serialized;
  use a struct with real/imaginary float64 fields"
- **A4.** `unsafe.Pointer` — "unsafe.Pointer cannot be serialized; remove
  the field"
- **A5.** `uintptr` — "uintptr cannot be serialized; use a typed integer"

### Category B — Ambiguous or unrepresentable in target languages (HARD ERROR)

- **B1.** Map with non-string key (`map[int]T`, `map[CustomKey]T`) — "map
  keys must be string in v0.1; convert to `[]KeyValueStruct` or use a
  string-keyed representation". Exception: a defined-string-alias key
  (`type ID string`) IS allowed and emitted as `Record<string, T>` in TS.
  The recognizer for this lives in the analyzer's primitive check, not in
  this layer.
- **B2.** Interface field (`interface{}`, `any`, or any named interface) —
  "interface types are not supported in v0.1; for arbitrary JSON use
  `json.RawMessage` per [0017](0017-special-stdlib-types.md); for known
  shapes use a concrete struct"
- **B3.** Anonymous struct field (`struct {...}` inline, not via a named
  type) — "anonymous struct fields are not supported in v0.1; extract the
  struct to a named type". Rationale: anonymous structs have no name to use
  in TS, and naming-by-path (`UserAddress` for `User.Address`) leaks parent
  context into nested type names in confusing ways.

### Category C — Things we intend to support later (DEFERRED)

- **C1.** Generic type instantiation in a field's type — "`Response[T]` et
  al. are deferred to v0.2 per the project roadmap (`README.md`); use a
  concrete type for now". Detection: the field's
  `*types.Type` is a `*types.Named` whose `TypeArgs()` returns
  non-nil/non-empty.
- **C2.** Cross-package types not in [0017](0017-special-stdlib-types.md)'s
  special list — "type %s is defined in package %s; cross-package types are
  deferred to v0.2. Either move the type into the handler's package, or
  wait." Reuses [0014](0014-handler-signature-strictness.md)'s same-package
  rule applied transitively: a request type's fields may reference only
  types in the same package or ADR-0017 builtins. Same for response types
  and their transitive references.
- **C3.** Custom `MarshalJSON` method on a type — "type %s has a
  MarshalJSON method; the wire shape may differ from its Go structure, which
  goduct cannot infer in v0.1. Deferred per ADR 0017's MarshalJSON-detection
  note." Detection: `types.Implements` against the `json.Marshaler`
  interface. The ADR-0017 special types (`time.Time`, etc.) are recognized
  BEFORE this check and never trigger it.
- **C4.** Custom `UnmarshalJSON` on a request-type field's type — same as C3
  but mentions `UnmarshalJSON`. Detection mirrors C3. Rationale: a type that
  round-trips through custom (un)marshaling has a hidden wire contract.

### Category D — Silently allowed

- **D1.** Unexported struct fields — skipped entirely. `encoding/json`
  ignores them anyway; an error would be noisy for fields users don't expect
  on the wire. Documented behavior, not a quirk.
- **D2.** Fields with `json:"-"` tag — skipped entirely. Standard-library
  convention; mirroring it avoids surprise.
- **D3.** Pointer to a supported type — recursed into as the pointee; sets
  `Optional=true` on the resulting `Field`. Already handled by other rules;
  listed for completeness.
- **D4.** A type already visited in the current traversal — stop recursion
  (cycle break), emit a `TypeRef` pointing at the already-recorded
  `TypeDef`. Cycles in Go struct graphs are legal (linked-list nodes, tree
  nodes) and must work. This is a correctness rule, not a failure rule, but
  belongs here so the milestone prompt doesn't reinvent it.

### Category E — Special validation

- **E1.** A field whose tag has both `json:"-"` and another goduct-relevant
  tag (path/query/header) — allowed; the field comes from the URL/header and
  is not in the JSON body. The `json:"-"` is redundant but not harmful.
  Documented to prevent confused users.
- **E2.** A response type's field with a path/query/header tag — HARD ERROR
  per [0016](0016-field-source-in-ir.md). Repeated here so the milestone
  prompt implements it.
- **E3.** A field with multiple goduct-relevant tags — HARD ERROR per
  [0014](0014-handler-signature-strictness.md) /
  [0016](0016-field-source-in-ir.md). Repeated here.

### Error message format (consistency requirement)

```
goduct: <file>:<line>:<col>: <category-id>: <description>
        in <qualified-field-name> (<Go-type>)
        hint: <one-line remediation>
```

Example:

```
goduct: api/users.go:42:8: B2: interface types are not supported in v0.1
        in api.GetUserResponse.Metadata (interface{})
        hint: for arbitrary JSON use json.RawMessage per ADR 0017
```

## Consequences

- The milestone prompt implements exactly this table. Cases not in this ADR
  are silently allowed (the type-traversal milestone's prompt may add to
  this ADR if a case was overlooked, but does so explicitly via amendment).
- Generators trust the IR: if a type made it into `ir.API.Types`, it is
  fully expressible in every target language. Generators do not re-check for
  these categories.
- The deferred category (C) gives a clear forward path: each item is a known
  v0.2+ feature, not an open question.

## Alternatives considered

- "Best effort" traversal that emits `unknown` in TS for unsupported types
  — rejected. Silent fallback is exactly the failure mode
  [0007](0007-loud-failure-on-unsupported-input.md) prohibits.
- One catch-all error message — rejected. Categorized messages let users
  grep for "B2" and find every interface field at once.
- Allow anonymous structs by synthesizing names — rejected. Name collisions
  and confusion outweigh the convenience.
