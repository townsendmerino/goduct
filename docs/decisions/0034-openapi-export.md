# 0034. OpenAPI 3.1 export (v0.3)

**Status:** Accepted
**Date:** 2026-06-02

## Context

v0.1 — v0.2 ship a typed Go → typed TS pipeline. Non-Go/TS consumers
(mobile teams, Postman users, internal docs platforms, OpenAPI-driven
test tools) have no entry point. The roadmap promised OpenAPI 3.1
export for v0.3.

The IR is already shaped right for the job. Every `ir.Route` has a
method, path, param tables, body/response type refs, doc, success
status, tag. Every `ir.TypeDef` has a wire shape (struct/enum/alias),
fields, type params (per ADR 0033). The OpenAPI generator is the
fifth-and-a-half IR consumer alongside the four TS generators and
goadapter; per [ADR 0022](0022-generator-conventions.md) §1 it
implements the same `Generate(api *ir.API, w io.Writer) error`.

## Decision

### 1. Output: single-file JSON, `openapi.json`

OpenAPI 3.1 accepts JSON or YAML. JSON keeps the dep story clean
(`encoding/json` is stdlib; YAML would add a parser/emitter for one
generator's output). Tooling that wants YAML can convert downstream
(`yq`, Spectral, Stoplight Studio, etc. all consume JSON natively).

Filename: `openapi.json` in `--out` — matches the existing TS-
generator pattern (`types.ts`, `schemas.ts`, etc.). New CLI flag
`--openapi`. `--all` includes it.

### 2. Document shape

```jsonc
{
  "openapi": "3.1.0",
  "info":    { "title": <packageName>, "version": "0.0.0" },
  "paths":   { "/users":     { "get": {...}, "post": {...} },
               "/users/{id}":{ "get": {...}, "patch": {...}, "delete": {...} } },
  "components": {
    "schemas": { "User": {...}, "GoductError": {...}, ... }
  }
}
```

- **`info.title`** is the package name (`gen.PackageName(api)`); a v0.4
  surface could add `--openapi-title` etc.
- **`info.version`** is `"0.0.0"` for v0.3 — goduct doesn't know the
  user's release version. A `--openapi-version` flag is a follow-up.
- **`paths`** are grouped by route path (multiple routes sharing
  `/users/{id}` collapse under one entry with `get` / `patch` /
  `delete` keys). Order: alphabetical (deterministic, no per-route
  position dependency).
- **`components/schemas`** carries every IR type plus the synthesized
  `GoductError`.

### 3. Path conversion

Goduct uses `:id`-prefixed path params (same as gin/echo). OpenAPI
mandates `{id}` brace syntax. Convert at emit time — same one-liner
that goadapter's chi/mux entries use.

### 4. Per-operation shape

Each operation gets:

- `tags`: `[route.Tag]` — single-element array.
- `operationId`: `route.HandlerName` (e.g. `GetUser`).
- `summary`: first sentence of `route.Doc` via `gen.JSDoc`.
- `description`: full doc via `gen.JSDocFull` (when more than one
  sentence; omitted otherwise so single-sentence docs don't show up
  twice).
- `parameters`: path / query / header params. `required` per ADR 0015
  (path always required; query/header per `Param.Optional`).
  `schema`: the parameter's wire type (string / integer / etc.).
- `requestBody`: present when `route.BodyType != nil`. Content type
  `application/json`, schema `$ref` to the body component.
  `required: true`.
- `responses`:
  - The success status (`200` / `201` / `204`):
    - For non-204: `application/json` content with `schema: $ref`.
    - For 204: empty body (`{"description": "No Content"}`).
  - `default`: `application/json` with `$ref` to `GoductError` —
    covers the runtime's loud-failure path for any 4xx/5xx.

### 5. Schema rendering

Every `ir.TypeDef` → one `components/schemas/<Name>` entry.

| IR kind | OpenAPI schema |
|---|---|
| Builtin `string`, `time.Time`, `[]byte`, `uuid.UUID` | `{"type": "string"}` (with `format`: `date-time` / `byte` / `uuid` where applicable) |
| Builtin `bool` | `{"type": "boolean"}` |
| Builtin `int`/`int*`/`uint*`/`float*`/`time.Duration` | `{"type": "integer"}` or `{"type": "number"}` |
| Builtin `json.RawMessage` | `{}` (no type constraint — JSON Schema 2020-12 "any") |
| Named (struct) | `{"$ref": "#/components/schemas/<Name>"}` |
| Slice | `{"type": "array", "items": <inner>}` |
| Map | `{"type": "object", "additionalProperties": <value>}` |
| TypeParam (`KindTypeParam`) | NOT emitted at this layer — see §6 generics |
| Struct fields with `oneof` validators | `{"type": "string", "enum": [...]}` |
| Struct fields with `min`/`max`/`len` | adds `minLength`/`maxLength`/`minimum`/`maximum` etc. |
| Custom adapter (ADR 0032) | wire-shape table: `string` → `{"type": "string"}`, etc. |

`required` array on struct schemas: every non-optional wire-visible
field (mirrors zod's `.optional()` rule). Optional fields appear in
`properties` without being listed in `required`.

### 6. Generics: eager instantiation

OpenAPI has no generic-schema concept. Each distinct instantiation
becomes its own component schema:

- `*Page[User]` → component schema `Page_User`.
- `*Result[User, Err]` → component schema `Result_User_Err`.
- `*Page[Result[User, Err]]` → component schema
  `Page_Result_User_Err` (transitively).

Naming: **underscore-joined** (`Page_User`). OpenAPI identifiers
can't contain `[` `]` `<` `>` `,`; underscore is the conventional
escape used by OpenAPI generators across languages. The generic
*origin* (`Page` without args) is **not** emitted as a standalone
schema — every reference is to an instantiated form.

When the generator walks `api.Types`, generic origins (`TypeParams !=
nil`) are visited but NOT emitted as a top-level component. Instead,
the analyzer's existing `api.Types` walk surfaces both origins and
the args; the OpenAPI generator collects every distinct instantiation
seen across `Route.RequestType` / `BodyType` / `ResponseType` (and
their nested TypeArgs) and synthesizes the substituted schema for
each.

Field types referencing `KindTypeParam` are substituted with the
concrete arg at substitution time — never emitted as a literal "T".

### 7. GoductError synthesized component

A synthetic component schema is added to `components/schemas`:

```jsonc
"GoductError": {
  "type": "object",
  "required": ["code", "message"],
  "properties": {
    "code":    { "type": "string" },
    "message": { "type": "string" },
    "details": {}
  }
}
```

Every operation's `default` response refs it. The schema is NOT
derived from an IR type — it's a hardcoded reflection of the
runtime's `goduct.Error` shape (ADR 0004). If a user has their own
`GoductError` type in their package, it gets a different component
name (the user's would be `<pkg>.GoductError` short-named); the
synthesized one always lives at `components/schemas/GoductError`.
A real-world collision (user defines a type literally named
`GoductError`) is a v0.4 concern; v0.3 emits both and lets the
user-side win on lookup ordering (last-write wins). Tracked as a
TODO if it ever bites.

### 8. Determinism

- `paths`: alphabetical.
- `paths[x].<method>`: HTTP-method canonical order (`get`, `put`,
  `post`, `delete`, `options`, `head`, `patch`, `trace`).
- `components/schemas`: alphabetical.
- `properties` within a schema: source-declaration order (matches the
  `WireFields` order the TS generators use).
- `parameters`: declaration order (path, then query, then header,
  each in declaration order).
- `required` arrays: alphabetical (canonical order; readers don't
  depend on declaration order).

`encoding/json.MarshalIndent` sorts map keys alphabetically for free,
so the alphabetical orderings are zero-cost. The HTTP-method
canonical order needs a small explicit map (one place to maintain).
`properties` source order can't be a Go map — must be `[]Property`
encoded as a custom MarshalJSON, or a `json.RawMessage` assembly.

For implementation simplicity v0.3 uses **`json.RawMessage`
assembly** for the path-operation map, the schema-properties map,
and the schemas map. The rest of the doc uses ordinary maps with
alphabetical encoding/json sorting. This trades a bit of code-shape
clarity for not introducing a custom MarshalJSON contract on every
embedded type.

### 9. `--framework` independence

The OpenAPI spec describes the API surface, not how it's wired. The
output is **identical** regardless of `--framework chi|gin|echo|mux`.
The path conversion uses OpenAPI's `{id}` syntax in all cases.

### 10. Out of scope for v0.3

- **YAML output.** Convert with `yq`/`jq` if needed.
- **OpenAPI security schemes** (Bearer, OAuth, API keys). v0.4
  add `--openapi-security` or similar.
- **Multiple server URLs** (`servers` array). v0.3 omits the `servers`
  field; clients default to the current origin.
- **Per-route status overrides.** A handler that explicitly returns
  `404` (via `goduct.NotFound`) is documented only via the synthesized
  `default` GoductError response, not a `404` entry. Status-code-aware
  emission (`responses["404"]`) is a v0.4 polish.
- **Per-field examples.** A `--openapi-examples` flag could opt in
  later if real users surface a need.
- **OpenAPI `info` enrichment.** Title is the package name, version
  is `"0.0.0"`. Custom `--openapi-title` / `--openapi-version` /
  `--openapi-description` flags are TODO follow-ups.

## Consequences

**Easy / unblocked:**

- Mobile clients, Postman users, internal docs platforms, OpenAPI-
  driven test tooling all get a feed from the same Go source.
- Swagger UI and Postman collection generators are thin downstream
  consumers of this output (each is ~hours of work, not its own
  ADR-worthy session).
- Schema-level features (oneof → enum, min/max/len, custom adapters,
  generics-as-flattened-instantiations) come free from the existing
  IR.

**Hard / giving up:**

- Generic instantiations explode the schema component count when an
  API has many distinct uses. A v0.4 `$ref`-with-allOf composition
  could compress this; v0.3 emits the flat form and accepts the
  schema-count growth.
- The GoductError synthesized component collides with a user-defined
  `GoductError` type (rare). Tracked as TODO.
- `info.version` is hardcoded `"0.0.0"`; tools that gate on version
  will see no movement until `--openapi-version` ships.
- Determinism via `json.RawMessage` assembly is more code than
  letting `encoding/json` order maps alphabetically everywhere. We
  trade clarity for not breaking property declaration order, which is
  what users expect.

## Alternatives considered

- **YAML output by default** — rejected for v0.3. Adds a parser/
  emitter dep for one generator's output; tooling that wants YAML
  converts downstream.
- **Per-instantiation generic naming as `PageOfUser`** — rejected.
  Underscore-joined is the more common OpenAPI-generator
  convention; it's also easier to disambiguate multi-arg
  (`Result_User_Err` vs `ResultOfUserOfErr` is a mouthful).
- **`$ref` + `allOf` composition for generics** — rejected for v0.3.
  Most OpenAPI consumers don't fully implement JSON Schema 2020-12
  composition; flat instantiations are universally supported.
- **Emit generic origins as schemas with TypeParam refs as
  `$ref: "#/T"`** — rejected. Not legal JSON Schema; tools choke.
- **Per-status-code responses derived from goduct.Error helpers
  (404 from NotFound, etc.)** — rejected for v0.3. The handler
  signature doesn't declare which error helpers it uses; static
  analysis would need to walk handler bodies. Synthesized `default`
  response covers the contract.
- **Custom MarshalJSON for every schema/operation type to enforce
  property order** — rejected. `json.RawMessage` assembly at the
  three places that need ordered keys is less code overall.

## Cross-references

- [0004](0004-error-wire-format.md) — `{code, message, details?}`
  shape of GoductError, mirrored in the synthesized component.
- [0006](0006-validation-tag-translation.md) — validator → JSON
  Schema keyword mapping (min/max/len/email/oneof).
- [0014](0014-handler-signature-strictness.md) — handler params /
  return shape; the OpenAPI operation builder reads these.
- [0015](0015-query-header-optionality-rule.md) — `Param.Optional`
  drives `required` on OpenAPI parameter objects.
- [0017](0017-special-stdlib-types.md) / [0032](0032-custom-type-adapters.md)
  — built-in + custom-adapter type-to-wire mapping; OpenAPI
  schema emission reuses the wire-shape table.
- [0022](0022-generator-conventions.md) §1 / §3 — generator signature,
  determinism rules.
- [0033](0033-generics.md) — generic recognition; OpenAPI emits
  flattened per-instantiation schemas rather than parametric forms.
