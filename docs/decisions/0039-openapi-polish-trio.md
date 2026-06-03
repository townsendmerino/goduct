# 0039. OpenAPI polish trio: examples, security schemes, per-status responses (v0.4)

**Status:** Accepted
**Date:** 2026-06-02

## Context

v0.3's OpenAPI export ([ADR 0034](0034-openapi-export.md)) is
structurally complete — it round-trips through every OpenAPI 3.1
consumer goduct has been tested against. But it lacks three
expressiveness features that users routinely hand-add to their
specs after the fact:

1. **Examples.** OpenAPI's `example` keyword shows a realistic
   payload alongside each schema. Without it, generated docs show
   the type structure but not a sample value, which is the first
   thing humans look for.
2. **Security schemes.** Most non-toy APIs require auth (bearer
   token, API key header, etc.). OpenAPI describes these via
   `components.securitySchemes` and operation/document-level
   `security` requirements. The v0.3 generator emits neither.
3. **Per-status-code responses.** A handler that returns 200 on
   success may also return 400 (validation), 404 (not found), or
   503 (upstream down). The v0.3 generator emits only the success
   status and a generic `default` → `GoductError`; structured
   alternative responses cannot be declared.

These three are bundled because they have the same shape (small
extensions to existing surfaces — one directive each plus a config
block) and the same audience (anyone who ships their OpenAPI to a
public consumer). Shipping them together avoids three minor
follow-ups across the v0.5 cycle.

## Decision

### 1. `goduct:example` directive

A new handler-level directive declares one example for the
response body:

```go
// goduct:route GET /users/:id
// goduct:example {"id":"u-1","name":"Alice","status":"active"}
func GetUser(ctx context.Context, req GetUserRequest) (*User, error) { ... }
```

- **Form:** `goduct:example <json-literal>`. The argument is the
  rest of the directive line; goduct does not parse the JSON
  beyond UTF-8 validation. A malformed JSON example is captured
  verbatim and surfaced to OpenAPI consumers (which will flag it);
  this is intentional — goduct is not a JSON linter.
- **Cardinality:** at most one per handler. Repeats are a
  loud-fail (consistent with every other goduct directive).
- **Scope:** the example is attached to the success response body
  schema only. Request-body / error-response examples are
  deferred (out of v0.4 scope).
- **IR:** `Route.Example string` (empty when absent). Additive per
  [ADR 0027](0027-enrich-ir-for-go-side-codegen.md).
- **OpenAPI emission:** the example becomes
  `responses["<successStatus>"].content["application/json"].example`
  (raw JSON value, not a string). The generator parses the
  captured literal once into `any` and emits the parsed value so
  the spec stays valid JSON. A parse failure is a loud-fail at
  generate time, with file:line context — the captured example
  is malformed and the OpenAPI doc would be invalid.

### 2. Security schemes (goduct.json `security` block)

A new optional `security` block in goduct.json declares named
schemes and the global requirement list:

```json
{
  "security": {
    "schemes": {
      "bearerAuth": {"type": "http", "scheme": "bearer", "bearerFormat": "JWT"},
      "apiKey":     {"type": "apiKey", "in": "header", "name": "X-API-Key"}
    },
    "requirements": [{"bearerAuth": []}]
  }
}
```

- **schemes** is a `map[string]any` — the value is whatever the
  OpenAPI 3.1 `SecurityScheme` shape requires. goduct does not
  validate the inner structure; it emits it as-is under
  `components.securitySchemes.<name>`. This keeps goduct's
  per-scheme-type surface flat (no enum of supported schemes; no
  bespoke validation of bearer/apiKey/oauth2 fields). A malformed
  scheme surfaces when the user's OpenAPI consumer rejects it.
- **requirements** is a `[]map[string][]string` — verbatim the
  OpenAPI 3.1 top-level `security` array. Emitted at document
  scope; v0.4 has no per-operation override.
- **IR:** `Meta.Security map[string]any` and
  `Meta.SecurityRequirements []map[string][]string`. Stamped onto
  `api.Meta` by the CLI after Analyze, same path as the existing
  OpenAPI metadata fields ([ADR 0038](0038-project-config-file.md)).
- **OpenAPI emission:** if non-empty, emit
  `components.securitySchemes` and a top-level `security`. When
  both are empty (the v0.3 default), the document shape is
  unchanged so existing consumers see no regression.

Rejected: defining a per-handler `goduct:security <scheme>`
directive that overrides the global. Most real APIs use one
scheme everywhere; the override is a v0.5 if anyone asks.

### 3. `goduct:errorresponse` directive (repeatable per-status responses)

A new repeatable handler-level directive declares additional
response shapes keyed by status code:

```go
// goduct:route POST /users
// goduct:errorresponse 400 ValidationError
// goduct:errorresponse 409 ConflictError
func CreateUser(ctx context.Context, req CreateUserRequest) (*User, error) { ... }
```

- **Form:** `goduct:errorresponse <status> <TypeName>`. Status is
  an integer in [100, 599]; type name is resolved against the
  handler's package scope, same as `goduct:request` /
  `goduct:response` (ADR 0031).
- **Cardinality:** repeatable; each line is one (status, type)
  pair. Two directives for the same status is a loud-fail (which
  body would win?).
- **Naming:** "errorresponse" rather than "response" because
  raw-mode (ADR 0031) already binds `goduct:response <Type>` as
  the success-body declaration. Reusing the directive name with a
  different arity would be a parser foot-gun. "altresponse" /
  "respond" were also considered; "errorresponse" wins because
  the dominant use is declaring 4xx/5xx shapes. A 2xx alternate
  (e.g. 202 Accepted) is rare and the slightly misleading
  directive name is documented as "this directive applies to
  additional, typically error-status responses."
- **IR:** `Route.ErrorResponses []ErrorResponse{Status int,
  Type *TypeRef}`. Additive per ADR 0027. Order preserved (source
  order), which the OpenAPI generator does not need but keeps the
  IR deterministic for golden tests.
- **OpenAPI emission:** each entry becomes
  `responses["<status>"].content["application/json"].schema =
  {$ref: "#/components/schemas/<TypeName>"}` with
  `description = "Error"` (consistent with the existing
  `default` synthesized entry). The type's TypeDef must be in
  `api.Types`; the type-traversal seed list (currently
  request/response/body types per route) extends to include
  ErrorResponses[i].Type so unreferenced error types still get
  rendered into `components.schemas`.

### 4. Coverage

- `analyzer/annotations.go`: new directive parsing for both
  `goduct:example` (single, with seen-map guard) and
  `goduct:errorresponse` (special-cased: not in the `seen` map;
  duplicate detection lives in a per-status check downstream).
  Unit tests for accept / reject / duplicate-status / out-of-range
  status / unknown type name.
- `analyzer/routes.go`: populate `Route.Example` and
  `Route.ErrorResponses`. Extend the type-traversal seed
  ([ADR 0018](0018-type-traversal-failure-boundaries.md)) to
  include ErrorResponses[i].Type so type discovery reaches the
  declared error types.
- `cliconfig`: extend Config with the `Security` block; tests for
  parse + verbatim pass-through.
- `openapi`: extend operation/document rendering; new tests cover
  example emission, security schemes block, and per-status
  responses. Chi-basic golden is regenerated to include one
  example and one errorresponse on a single handler so the e2e
  golden exercises the new emission paths (the alternative —
  unit-test-only coverage — leaves the golden artifact silently
  divergent from the spec's expressiveness).

## Consequences

**Easy / unblocked:**

- An end user can ship an OpenAPI doc with realistic samples and
  auth metadata using only directives + one config block — no
  post-processing tool needed.
- The Postman generator gets free `description` cues from any
  errorresponse declarations (deferred to a future ADR; this ADR
  changes only the OpenAPI generator).

**Hard / giving up:**

- Per-handler security overrides deferred to v0.5; APIs that mix
  public and authenticated endpoints declare the global
  requirement here and accept that public endpoints will appear
  protected in tooling (or hand-edit the spec post-generation).
- Examples are response-body only; request examples deferred.
- `security.schemes` is pass-through `any` — a typo in a scheme's
  inner shape surfaces only at consumer time. The alternative
  (typed validation) would mean tracking every OpenAPI scheme
  type's required fields; out of scope for v0.4.

## Alternatives considered

- **Synthesize examples from type structure** (no annotation,
  CLI flag activates placeholder generation). Rejected: the
  placeholders are generic ("string", 0, []) and worse than no
  example at all.
- **Per-route security in source via `goduct:security <scheme>`.**
  Deferred; can be added additively in v0.5 without changing the
  global mechanism.
- **Reuse `goduct:response <status> <Type>` syntax** by overloading
  arity. Rejected: shadows raw-mode's existing 1-arg form and
  forces special parser cases.
- **Put security schemes in a separate file (`goduct.security.json`)
  to keep the main config clean.** Rejected: one config file is
  the design promise of [ADR 0038](0038-project-config-file.md).

## Cross-references

- [0007](0007-loud-failure-on-unsupported-input.md) — duplicate
  directive / parse-malformed-example / out-of-range status all
  loud-fail.
- [0018](0018-type-traversal-failure-boundaries.md) — error
  response types extend the type-discovery seed list.
- [0027](0027-enrich-ir-for-go-side-codegen.md) — IR additions
  (`Route.Example`, `Route.ErrorResponses`, `Meta.Security*`) are
  additive.
- [0031](0031-raw-handlerfunc-mode.md) — `goduct:response <Type>`
  is a separate directive; `goduct:errorresponse` does not
  shadow it.
- [0034](0034-openapi-export.md) — extends the existing operation
  rendering without changing the v0.3 golden shape when no new
  directives/config are used.
- [0038](0038-project-config-file.md) — `security` block joins
  the existing `openapi` block in goduct.json; same precedence
  rules apply.
