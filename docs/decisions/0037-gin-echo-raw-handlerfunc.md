# 0037. gin/echo raw `http.HandlerFunc` via context-bridge wrappers (v0.4)

**Status:** Accepted
**Date:** 2026-06-02

## Context

[ADR 0031 §3](0031-raw-handlerfunc-mode.md) shipped raw-mode for chi
and `net/http` mux, both of which use the `http.HandlerFunc` shape
natively. gin and echo were excluded with a loud-fail in
`goadapter.GenerateFramework` because their router takes a
framework-specific context type (`*gin.Context`,
`echo.Context`) rather than `(w, r)`. The ADR considered synthesizing
a context-bridging wrapper and rejected it for v0.2 on the grounds
that "that's exactly the wrapper synthesis the user opted out of by
going raw."

That framing conflates two different wrappers:

1. **The idiomatic-mode wrapper** decodes the JSON body, applies path
   and query params, calls the typed handler, and writes the typed
   response. The user opts out of this by going raw.
2. **A context-bridging wrapper** does none of that — it only adapts
   the framework's context to `(w, r)` so the user's existing
   `http.HandlerFunc` can be registered. It's three or four lines, all
   plumbing.

(1) is the synthesis raw-mode users opted out of. (2) is the cost
of using gin/echo at all; a hand-rolled raw-mode adopter on
gin/echo would write the same bridge themselves. v0.4 ships (2) so
the user doesn't have to.

## Decision

### 1. Lift the gin/echo loud-fail for raw routes

`goadapter.GenerateFramework` no longer rejects `ir.ModeRaw` routes
when `frameworkName` is `gin` or `echo`. The pre-walk early-return is
removed; raw routes are emitted on all four frameworks.

### 2. Generated registration

Per ADR 0030 §2 and ADR 0031 §3, `Register` for raw routes already
references an identifier directly (`handlerRef(rt)`). The identifier
changes per framework:

| Framework | Raw `Register` references |
|---|---|
| chi | `RawPing` (the user's function — `http.HandlerFunc` native) |
| mux | `RawPing` (same — `http.HandlerFunc` native) |
| gin | `handleRawPing` (a generated context bridge) |
| echo | `handleRawPing` (a generated context bridge) |

The chi/mux behavior is unchanged from ADR 0031. gin and echo now
also synthesize a `handle<Name>` symbol, but it is the bridge below
— not the idiomatic decode/dispatch/write wrapper.

### 3. The context-bridge wrapper

For gin, the synthesized wrapper is:

```go
func handleRawPing(c *gin.Context) {
	RawPing(c.Writer, c.Request)
}
```

For echo, it is:

```go
func handleRawPing(c echo.Context) error {
	RawPing(c.Response().Writer, c.Request())
	return nil
}
```

Three rules:

- **No body decode, no param assignment, no response writing.** The
  bridge calls the user's function and returns. The user's raw
  handler is the source of truth for everything past the framework
  boundary; goduct doesn't touch the request/response cycle inside
  the bridge.
- **Echo returns `nil` unconditionally.** `http.HandlerFunc` has no
  return value; echo wants `error`. There is no way to surface a
  late error from a plain `http.HandlerFunc` through echo's error
  type, and the user already wrote their response by the time the
  bridge returns. `return nil` is the only honest answer.
- **No goduct runtime import for raw routes.** The bridge doesn't
  use `goduct.WriteError` / `goduct.WriteJSON`. If a generated file
  contains only raw routes on gin/echo, the goduct runtime is still
  imported (the import block is built per-API, not per-route, and
  idiomatic routes typically coexist); pruning the import to be
  raw-only-aware is deferred — the cost of a never-used import is
  one line and a `_ = goduct.Error` is not required.

### 4. Where the bridge code lives

The bridge is one new helper in `internal/generators/goadapter/`:
`rawBridge(fw *framework, rt ir.Route) string`. It returns "" for
chi and mux (no bridge needed) and the gin/echo source for those
two. The top-level emit loop calls `rawBridge` for raw routes
exactly where it calls `wrapper` for idiomatic routes:

```go
for _, rt := range api.Routes {
    if rt.Mode == ir.ModeRaw {
        if s := rawBridge(fw, rt); s != "" {
            b.WriteString("\n" + s + "\n")
        }
        continue
    }
    b.WriteString("\n" + wrapper(fw, rt) + "\n")
}
```

`handlerRef(rt)` from ADR 0031 §3 is updated: for raw routes on
gin/echo it returns `"handle" + rt.HandlerName` (the bridge name);
for raw routes on chi/mux it stays `rt.HandlerName` (the user's
function). The framework instance is plumbed into `handlerRef` as
a second argument.

### 5. Coverage

A new unit test exercises gin/echo raw bridging from a synthetic
`ir.API` (the same pattern as ADR 0031's
`TestGenerateFramework_RawMode`). The test asserts:

- gin output contains `func handleRawPing(c *gin.Context) {` and
  `RawPing(c.Writer, c.Request)`.
- echo output contains `func handleRawPing(c echo.Context) error {`
  and `RawPing(c.Response().Writer, c.Request())` followed by
  `return nil`.
- gin/echo `Register` body lines reference `handleRawPing`, not
  `RawPing` directly.
- chi/mux raw output is byte-identical to its v0.3 form (no
  spurious bridge for the natively-compatible frameworks).

Adding a raw route to chi-basic's `api/` would touch all four
goadapter goldens and every TS golden, for the same coverage the
synthetic test already provides. Deferred for the same reasons
ADR 0031 §5 deferred it; the spec-trust TODO entry from ADR 0031
remains open and now also covers gin/echo.

## Consequences

**Easy / unblocked:**

- Raw-mode adopters can pick any of the four supported frameworks
  with no per-framework rewrite. The user's `http.HandlerFunc` stays
  framework-agnostic; goduct handles the four-line adaptation.
- The gin/echo deferral note in
  [ADR 0031](0031-raw-handlerfunc-mode.md) §3 is lifted.
- No IR change. ADR 0027's additive-only invariant holds.

**Hard / giving up:**

- Echo bridge always returns `nil`. A raw handler that fails after
  starting to write its response gets logged by the user's own
  middleware, not by echo's error handler. This is intrinsic to
  bridging `(w, r)` into `func(c) error`; the alternative would
  require the user to write echo-shaped raw handlers, which defeats
  the point of raw mode.
- Generated file still imports the goduct runtime on gin/echo files
  that contain *only* raw routes (rare in practice). One-line cost;
  not worth a per-route import-pruning pass.

## Alternatives considered

- **Status quo (gin/echo raw stays a loud-fail).** Users with mixed
  codebases — idiomatic handlers in the same package as one raw
  legacy handler — couldn't pick gin/echo. The bridge is small;
  the user-side workaround would be exactly the same bridge.
- **Auto-detect framework from imports and synthesize the bridge in
  the user's call site instead of in `goduct_routes.go`.** Out of
  scope: goduct never edits user files (ADR 0009's spirit).
- **Make echo bridge return an error if the user's handler panicked.**
  Requires `recover()` inside the bridge, which changes panic
  semantics the user might rely on. The honest "no signal available"
  answer is `return nil`.

## Cross-references

- [0009](0009-generated-adapter-same-package.md) — generated adapter
  in the handlers' own package; bridge wrappers live there too.
- [0022](0022-generator-conventions.md) §1 — `Generate` signature
  unchanged; `GenerateFramework` covers the multi-framework case.
- [0030](0030-framework-adapter-selection.md) §2 — framework table
  gains no new fields; `handlerRef`'s framework parameter is the
  one structural addition.
- [0031](0031-raw-handlerfunc-mode.md) §3 — superseded for the
  gin/echo limitation only. §1, §2, §4, §5 stand.
