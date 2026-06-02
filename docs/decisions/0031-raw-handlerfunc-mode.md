# 0031. Raw `http.HandlerFunc` mode (v0.2)

**Status:** Accepted
**Date:** 2026-06-02

## Context

[ADR 0001](0001-handler-signature-convention.md) defined two handler
styles — idiomatic (`func(ctx, T) (*U, error)`) and raw
(`func(w, r)` with `goduct:request`/`goduct:response` annotations) —
and deferred raw to v0.2.
[ADR 0014](0014-handler-signature-strictness.md) tightened the
idiomatic signature in v0.1. `ir.ModeRaw` exists as a constant but no
analyzer code path populates it.

v0.2 fulfills the raw-mode deferral: users with an existing
`http.HandlerFunc` codebase, or who need finer wire control than the
idiomatic shape allows, can opt into raw mode per handler.

## Decision

### 1. Detection

A function is a goduct **raw handler** iff:

- It carries a `goduct:route METHOD PATH` annotation, AND
- Its signature is **exactly**
  `func(http.ResponseWriter, *http.Request)`, AND
- It carries both `goduct:request <Type>` and `goduct:response <Type>`
  annotations (or only `goduct:request` if the route returns no body).

A function with a raw signature but missing `goduct:request` /
`goduct:response` is an error (ADR 0007 loud-failure): the analyzer
can't infer request/response types from a `(w, r)` signature.

A function with `goduct:route` and the idiomatic signature is handled
by the v0.1 idiomatic path; raw signature is the new branch.

`goduct:request` / `goduct:response` directives are **forbidden on
idiomatic handlers** (the types are already in the signature; the
annotation would be a duplicate source of truth, and a clash is a
loud-fail).

### 2. IR shape

Raw routes populate `ir.Route` with:

- `Mode = ir.ModeRaw`
- `RequestType` — resolved from the `goduct:request` annotation's
  type name, looked up in the handler's package scope. Required.
- `ResponseType` — resolved from `goduct:response`, same way. `nil`
  when the route has no response body (e.g. DELETE with 204).
- `BodyType` — for body-allowed methods (POST/PUT/PATCH), set to the
  same `*TypeRef` as `RequestType` (the request type IS the body
  type in raw mode — the user decodes it from JSON themselves). For
  GET/DELETE, `nil`.
- `PathParams` / `QueryParams` / `HeaderParams` — extracted from the
  RequestType's tags exactly as in idiomatic mode. Raw mode does not
  exempt the user from declaring tags; the TS client/zod/types
  generators still depend on them.
- `SuccessStatus` — from `goduct:status` if present, else the ADR 0014
  default for the method.

### 3. Generator behavior

**TS generators** (tstypes, zod, tsclient, hooks): **unaffected**.
They read the IR; whether a route is idiomatic or raw is invisible to
them. Same request/response types, same client surface.

**goadapter** (all four frameworks): raw routes register the user's
function **directly** — no wrapper synthesis:

```go
// idiomatic: handleGetUser is a generated wrapper
r.GET("/users/:id", handleGetUser)

// raw: GetUser IS the http.HandlerFunc
r.GET("/users/:id", GetUser)
```

No `handle<Name>` function is emitted for raw routes. The Register
function references the user's handler by name; the user is
responsible for body decoding, path-param extraction, validation,
response writing — that is the point of raw mode.

For frameworks whose router wants a framework-specific signature
(gin's `func(c *gin.Context)`, echo's `func(c echo.Context) error`),
raw routes are an awkward fit: the user wrote `func(w, r)` but gin
wants `func(c)`. **v0.2 ships raw mode for chi and mux only**
(both use the `http.HandlerFunc` shape natively). gin and echo
ModeRaw routes loud-fail at gen time with a clear message: "raw
HandlerFunc mode is not supported on gin/echo in v0.2; use the
idiomatic handler signature." Tracked as a future extension.

### 4. Cross-package and other limitations

- **Same-package** request/response types only, matching ADR 0014's
  idiomatic constraint. Cross-package raw types are deferred.
- **No middleware composition** at the goduct layer — the user's raw
  handler may chain middleware itself; goduct just registers the
  final handler.

### 5. Coverage approach for v0.2

Adding a raw handler to chi-basic would touch every TS golden + every
framework adapter golden. To keep this ADR's shipment surgical, v0.2
ships raw-mode **implementation and unit tests** without touching
chi-basic. A new `examples/raw-basic/` coverage example, OR an
extension to chi-basic, is queued as a follow-up TODO. Spec-trust
applies: the analyzer + adapter paths are implemented to spec but
not yet exercised by a golden in this commit train.

## Consequences

**Easy / unblocked:**

- Existing `http.HandlerFunc` codebases can adopt goduct one handler
  at a time without rewriting.
- The TS generators get a free pass — they see typed requests and
  responses identically across modes.

**Hard / giving up:**

- Goduct can't verify the raw handler's runtime behavior matches its
  annotations (the annotations are user-asserted, not type-derived).
  This is the explicit tradeoff of raw mode. README's existing §`Raw
  http.HandlerFunc` callout already says this.
- gin/echo raw routes are not supported in v0.2. Future work.
- v0.2 ships without a chi-basic golden change exercising raw mode.
  Spec-trust coverage gap; follow-up TODO.

## Alternatives considered

- **Auto-detect request/response types from handler body** (look for
  `var req Foo` / `json.NewDecoder(r.Body).Decode(&foo)` patterns)
  — rejected. Brittle, magic, hard to debug.
- **Make `goduct:request`/`response` optional in raw mode** — rejected.
  The user must declare the wire types; the TS generators have no
  other source.
- **Support gin/echo raw mode by wrapping** — rejected for v0.2.
  Each framework has its own context type; converting an
  `http.HandlerFunc` to a `func(c *gin.Context)` requires a wrapper
  that adapts the writer/request. That's possible, but it's exactly
  the wrapper synthesis the user opted out of by going raw.
- **Make raw mode the default for unknown signatures** — rejected.
  Silent surprises. Loud-fail on ambiguous shapes (ADR 0007).

## Cross-references

- [0001](0001-handler-signature-convention.md) — defined raw mode,
  deferred to v0.2. This ADR is its fulfillment.
- [0007](0007-loud-failure-on-unsupported-input.md) — informs the
  "missing annotation" and "gin/echo raw" error behaviors.
- [0014](0014-handler-signature-strictness.md) — idiomatic
  constraints; raw mode is the orthogonal path.
- [0030](0030-framework-adapter-selection.md) — gin/echo raw
  limitation lives in the framework-adapter layer.
