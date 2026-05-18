# 0017. Special standard-library (and well-known) types

**Status:** Accepted
**Date:** 2026-05-17

## Context

Type traversal (next milestone) walks Go struct fields recursively to
populate `ir.API.Types`. Some standard-library types have a well-known JSON
wire representation that does not match their Go structure: `time.Time`
serializes as an ISO 8601 string, not as its underlying struct of
seconds/nanos. If we traversed `time.Time` naively we would emit a TS
interface with `wall`/`ext`/`loc` fields, which is wrong on the wire and
useless to clients.

The analyzer must recognize a fixed set of these types and treat them as
opaque builtins with a known wire shape, NOT recurse into their Go field
structure.

## Decision

The following Go types are recognized by the analyzer as "special
builtins". They appear in `ir.TypeRef` as `Kind: KindBuiltin` with the
listed `Builtin` name. Generators are responsible for rendering each one in
their output's idioms.

| Go type | `ir.Builtin` value | Wire / generator guidance |
| --- | --- | --- |
| `time.Time` | `"time.Time"` | JSON: ISO 8601 string.<br>TS types: `string`.<br>Zod: `z.string().datetime({ offset: true })`.<br>Go adapter: no special handling (`encoding/json` already does the right thing). |
| `time.Duration` | `"time.Duration"` | JSON: int64 nanoseconds (Go's default).<br>TS types: `number`.<br>Zod: `z.number().int()`.<br>Note: this is Go's `encoding/json` default. Users who want `"30s"`-style strings need a custom type with `MarshalJSON`, which is unsupported in v0.1. |
| `[]byte` | `"[]byte"` | JSON: base64 string (Go's default).<br>TS types: `string`.<br>Zod: `z.string().base64()` if the zod version supports it, else `z.string()` with a comment.<br>NOTE: `[]byte` is a SLICE in Go's type system but the analyzer must detect it BEFORE the generic slice-handling code path. |
| `json.RawMessage` | `"json.RawMessage"` | JSON: arbitrary JSON value (passthrough).<br>TS types: `unknown`.<br>Zod: `z.unknown()`.<br>Used for genuine "we don't know the shape" cases. |
| `uuid.UUID` | `"uuid.UUID"` | JSON: string (the `github.com/google/uuid` type marshals to a hyphenated string by default).<br>TS types: `string`.<br>Zod: `z.string().uuid()`.<br>Only the `google/uuid` package is recognized in v0.1; other UUID libraries (gofrs, satori) are not detected and will produce a loud-failure error per [0007](0007-loud-failure-on-unsupported-input.md). |

### Recognition rules

- Recognition is by exact qualified type name: package import path + `.` +
  type name (e.g. `"time.Time"`, `"encoding/json.RawMessage"`,
  `"github.com/google/uuid.UUID"`). Aliases of these types in the user's
  code are NOT recognized syntactically; we match the underlying named type
  via `go/types`, not the spelling.
- `[]byte` is recognized specifically (not generalized to "any `[]T` where
  `T`'s `MarshalJSON` produces a string"). This is a deliberate scope cap.
- When the analyzer encounters a struct field whose type is one of these, it
  emits a `TypeRef` with `Kind: KindBuiltin` and the listed `Builtin` value.
  It does NOT recurse into the Go structure of these types.
- When the analyzer encounters a struct field whose type is SOME OTHER
  stdlib type (e.g. `net/url.URL`, `big.Int`, `time.Location`) that this ADR
  does not list, it must error per [0007](0007-loud-failure-on-unsupported-input.md)
  with a clear message: `goduct: type %s is not supported in v0.1. Use a
  string field with manual conversion, or open an issue if this type should
  be added to ADR 0017.`

## Consequences

- The supported "rich type" surface is tiny and explicit. Users get a clear
  error rather than silently-wrong wire types.
- Adding a new special type later is one ADR amendment + a few lines in the
  analyzer's recognizer + one new case in each generator's renderer. We
  accept this cost in exchange for explicit control.
- The "`json.Marshaler`-aware" rabbit hole (detecting any type with a
  `MarshalJSON` method and asking the user to declare its wire shape) is
  explicitly out of scope. v0.2 may revisit;
  [0014](0014-handler-signature-strictness.md) already defers cross-package
  request/response types.
- `decimal.Decimal`, `civil.Date`, `big.Int`, `big.Float`,
  `net/mail.Address`, `net/url.URL`, `language.Tag` — all out of scope for
  v0.1. Users who need these wrap them in a string field and convert at the
  handler boundary.

## Alternatives considered

- Recurse into the Go structure of `time.Time` and emit whatever falls out
  — rejected: produces nonsense wire types.
- Detect `json.Marshaler` implementations and ask the user to declare wire
  shape via a comment annotation — rejected for v0.1 as too much surface
  area for a first release. Possibly v0.3.
- Make the special-types list user-extensible via a config file — rejected
  for v0.1; users get a loud failure and can request additions via issue.
  Possibly v0.2.
