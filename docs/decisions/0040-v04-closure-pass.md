# 0040. v0.4.1 closure pass: deferred items from ADRs 0030/0039 + lingering v0.2 spec-trust (v0.4.1)

**Status:** Accepted
**Date:** 2026-06-02

## Context

Three deferrals from v0.4 and four spec-trust caveats from v0.2
have accumulated:

**Fresh (v0.4 → ADR 0039):**

1. **chi-basic errorresponse coverage.** ADR 0039's
   empirical-finding note kept `goduct:errorresponse` out of the
   chi-basic golden because the cascade (six regenerated goldens
   for one demonstrated feature) was disproportionate at the time
   the v0.4 commit train was already in flight. A closure pass
   that's expecting cascade anyway is the right home for it.

2. **Request-body examples.** ADR 0039 §1 capped `goduct:example`
   at the response body. Request examples are the same shape —
   one JSON literal per handler, one OpenAPI `example` keyword —
   and the asymmetry is a wart.

3. **Per-handler security override.** ADR 0039 §2 made security
   document-level only. APIs that mix public and authenticated
   endpoints currently can't represent that fact in the generated
   spec without hand-editing.

**Stale (v0.2 → README "Spec-trust caveats"):**

4. **`url` validator** is implemented in zod translation but no
   chi-basic field exercises it.

5. **`len=N` validator** — same: implemented, untested in the
   end-to-end golden.

6. **Combined path+query argument object.** The typed TS client's
   handling of routes with BOTH path and query params (one merged
   `params` object, not two args) has no chi-basic route to
   exercise it — every route in chi-basic has path OR query, never
   both.

7. **`bool` / `float` query param conversion.** The Go adapter
   emits `strconv.ParseBool` / `strconv.ParseFloat` for these
   types, but chi-basic's `ListUsersRequest` only uses `int` and
   `string` query params.

Items 4–7 have been spec-trust since v0.2 — i.e. ~four months at
this point. The closure pass collapses them with the v0.4
deferrals because the cascade is paid once.

## Decision

### 1. Per-handler `goduct:security` directive

```go
// goduct:route GET /healthz
// goduct:security none
func Healthz(...) { ... }

// goduct:route GET /accounts/:id
// goduct:security bearerAuth
// goduct:security apiKey
func GetAccount(...) { ... }
```

- **Form:** `goduct:security <scheme-name>`. `<scheme-name>` is
  either a name declared in `goduct.json`'s
  `security.schemes` (validated at OpenAPI generate time — an
  unknown name surfaces only when the spec is consumed; goduct
  does not cross-check, matching ADR 0039's pass-through stance
  on scheme shapes), or the literal `none` to declare an explicit
  unauthenticated endpoint.
- **Cardinality:** repeatable; each line is one OpenAPI security
  requirement. Multiple lines compose as OR (any one requirement
  satisfies the operation). `none` and a named scheme on the same
  handler is a loud-fail (contradictory).
- **Scopes:** scope arguments (OAuth2's `read:users`,
  `write:users` etc.) are out of scope for v0.4.1 — every emitted
  requirement uses an empty scope list. Scope support is a v0.5
  question if anyone asks.
- **IR:** `Route.Security []SecurityRequirement` where
  `SecurityRequirement` is `{Schemes []string}` — empty schemes
  on a single requirement means the `none` form. Additive per
  ADR 0027. Nil/empty `Route.Security` means "inherit document
  default" (the existing behavior).
- **OpenAPI emission:** when `Route.Security` is non-nil, emit
  `operation.security = [...]` per requirement. Document-level
  `security` from goduct.json continues to apply to operations
  that don't override.

### 2. `goduct:requestexample` directive

```go
// goduct:route        POST /users
// goduct:requestexample {"email":"alice@example.com","name":"Alice","role":"member"}
func CreateUser(...) { ... }
```

- **Form:** `goduct:requestexample <json-literal>`. Same parse
  contract as `goduct:example` (rest-of-line capture, JSON
  validated at OpenAPI generate time).
- **Cardinality:** single-shot per handler. Repeats loud-fail.
- **IR:** `Route.RequestExample string`. Additive.
- **OpenAPI emission:** attach to
  `requestBody.content["application/json"].example`. No-op for
  routes that have no body (GET/DELETE).
- **Naming:** a separate directive (rather than overloading
  `goduct:example` with an optional `request`/`response` target)
  to keep argument parsing unambiguous — `goduct:example` already
  treats its entire argument as the JSON literal, and a leading
  `request` / `response` token would force special-case parsing.

### 3. chi-basic source extensions

To exercise the seven items end-to-end, `examples/chi-basic/api/users.go`
gains the following — all six chi-basic goldens
(types/schemas/client/hooks/openapi/postman + the four
framework adapter goldens) regenerate accordingly.

| Item | chi-basic change |
| --- | --- |
| (1) errorresponse | New `ValidationError` type; `goduct:errorresponse 400 ValidationError` on `CreateUser` |
| (4) `url` validator | New `Website string \`json:"website,omitempty" validate:"omitempty,url"\`` field on `CreateUserRequest` |
| (5) `len=N` validator | New `ReferralCode string \`json:"referralCode,omitempty" validate:"omitempty,len=8"\`` field on `CreateUserRequest` |
| (6) path+query route | New `Include string \`query:"include"\`` field on `GetUserRequest` |
| (7a) `bool` query | New `Active *bool \`query:"active"\`` field on `ListUsersRequest` |
| (7b) `float` query | New `MinScore *float64 \`query:"minScore"\`` field on `ListUsersRequest` |

A `goduct:requestexample` is added to `CreateUser` to exercise
(2). Security (3) stays unit-test-only because chi-basic has no
`goduct.json`; adding one would couple the e2e test to a config
file path-discovery convention chi-basic was deliberately
designed to avoid.

The handler bodies are stub implementations that ignore the new
fields; the golden contract cares about generated output, not
runtime behavior of the fixture.

### 4. Coverage strategy

- New analyzer tests in `annotations_test.go` for `goduct:security`
  (single, multi, `none`, contradictory `none + named`) and
  `goduct:requestexample` (capture, missing-arg loud-fail,
  duplicate loud-fail).
- New openapi tests for operation-level security emission (a
  synthetic API with one `none`-overridden route and one
  multi-scheme route), and for request-body example rendering
  (synthetic API with both body example AND response example to
  prove they coexist).
- chi-basic golden regen — every affected golden's diff is bounded
  by the source changes above; no incidental regen.
- README updates: the four v0.2 spec-trust caveats are removed
  (now covered by chi-basic); the v0.4 deferrals are marked
  closed in the relevant ADR cross-references.

## Consequences

**Easy / unblocked:**

- Mixed-auth APIs (public health check, authenticated
  everything-else) describe their security posture in source
  rather than via a post-processing edit.
- Request examples symmetry with response examples; the OpenAPI
  doc is more useful without leaving goduct.
- Four long-running spec-trust caveats close in one pass; the
  README's "What's supported" section stops listing v0.2-era
  unfinished business as v0.4.1 ships.
- chi-basic, after this pass, demonstrates every v0.4 directive in
  one place — newcomers reading the example see the full surface.

**Hard / giving up:**

- The chi-basic golden diff is large. Six TS goldens, four
  framework adapter goldens, and the OpenAPI/Postman pair all
  shift in the same commit. Mitigated by the changes being a
  bounded, ADR-authorized set; a reviewer can verify by re-running
  `goduct gen --all` and byte-diffing.
- Per-operation security scope arguments deferred — the OAuth2
  use case still requires hand-editing.
- `requestBody.examples` (plural, named examples) deferred — only
  the singular `example` form ships. If multiple examples per
  request are needed, a follow-up ADR adds a repeatable directive
  with a name argument.

## Alternatives considered

- **Ship the v0.4 deferrals alone, leave v0.2 caveats.** Rejected:
  the cascade cost is paid once whether one or seven items land
  together. Spreading the regen across multiple tags doubles the
  golden-diff review effort.
- **Overload `goduct:example` with a target argument.** Rejected:
  forces special-case argument parsing since the directive
  currently captures the entire rest-of-line as the JSON literal.
- **Allow scope arguments on `goduct:security` now.** Rejected:
  OAuth2 scope semantics across nested OpenAPI security
  requirements get complex; defer until a real user need.

## Cross-references

- [0007](0007-loud-failure-on-unsupported-input.md) — directive
  parsing rejections.
- [0027](0027-enrich-ir-for-go-side-codegen.md) — IR additions
  are additive.
- [0030](0030-framework-adapter-selection.md) — chi-basic's
  framework-adapter goldens regenerate per framework as a result
  of the source changes; no per-framework decision changes.
- [0034](0034-openapi-export.md) / [0035](0035-openapi-sibling-generators.md)
  — openapi.json, swagger-ui.html, postman_collection.json all
  regenerate from the source changes; no decision changes.
- [0038](0038-project-config-file.md) — security overrides
  reference the schemes declared in goduct.json's `security`
  block.
- [0039](0039-openapi-polish-trio.md) — closes the three deferrals
  recorded in §1 (errorresponse coverage), §1 (request-body
  examples), §2 (per-handler security override). The "spec-trust"
  note added to ADR 0039's Consequences during v0.4 is satisfied
  by this pass.
