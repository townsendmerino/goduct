# 0030. Framework-adapter selection mechanism and structure (v0.2)

**Status:** Accepted
**Date:** 2026-06-02

## Context

[ADR 0002](0002-v01-framework-scope.md) pinned chi as the v0.1
framework. v0.2 lifts that: the `--go-adapter` generator must emit
correct wiring for chi, gin, echo, and `net/http` mux.

Each framework differs from chi in concrete, generator-affecting
ways:

| Concern | chi | gin | echo | `net/http` mux |
|---|---|---|---|---|
| Import | `github.com/go-chi/chi/v5` | `github.com/gin-gonic/gin` | `github.com/labstack/echo/v4` | stdlib |
| Router type | `chi.Router` | `*gin.Engine` (or `gin.IRouter`) | `*echo.Echo` | `*http.ServeMux` |
| Path param syntax | `{id}` | `:id` | `:id` | `{id}` (Go 1.22+) |
| Method registration | `r.Get(path, h)` | `r.GET(path, h)` | `e.GET(path, h)` | `r.HandleFunc("METHOD path", h)` |
| Path param extract | `chi.URLParam(r,"id")` | `c.Param("id")` | `c.Param("id")` | `r.PathValue("id")` |
| Wrapper signature | `func(w, r)` | `func(c *gin.Context)` | `func(c echo.Context) error` | `func(w, r)` |
| Request context | `r.Context()` | `c.Request.Context()` | `c.Request().Context()` | `r.Context()` |

Three v0.2 design calls follow: **how is the framework selected**,
**how is the generator structured** to support four targets without
duplicating ~80% of the code, and **where do the per-framework
goldens live** in the example tree.

## Decision

### 1. Selection: `--framework` CLI flag

The framework is chosen by a new `--framework` flag on `goduct gen`:

```
goduct gen ./api --out ./web/src/api --go-adapter --framework gin
```

Valid values: `chi` (default), `gin`, `echo`, `mux`. An unknown value
errors with exit 2 listing the valid set. Defaulting to `chi`
preserves v0.1's invocation form byte-for-byte: existing
`--go-adapter` users see no change.

Selection lives at the CLI layer (a single value per gen run), not in
the IR or per-route. Rationale:

- A user's API is one framework. Mixing chi handlers and gin handlers
  in the same package is not a real workflow.
- The IR stays framework-neutral (its `Path` field already uses
  goduct's `:name` syntax; framework translation happens at gen time).
  This matches [ADR 0002](0002-v01-framework-scope.md)'s consequence
  about the IR.
- A flag is simpler than an annotation (no parser changes, no
  per-route bookkeeping) and simpler than auto-detection (which would
  be brittle: users import many things).

**Alternatives rejected:**

- Per-route directive (`// goduct:framework gin`) — adds annotation
  surface for a non-real workflow.
- Auto-detect from imports — brittle; a user importing chi for
  middleware while using gin for routing breaks naive detection.

### 2. Generator structure: parameterized goadapter

`internal/generators/goadapter` stays one package, parameterized by a
small framework-config struct:

```go
type framework struct {
    Name           string                   // "chi", "gin", "echo", "mux"
    ImportPath     string                   // "github.com/go-chi/chi/v5"
    ImportAlias    string                   // "chi"; "" if path's last seg matches package name
    RouterType     string                   // "chi.Router", "*gin.Engine", ...
    RegisterMethod func(method string) string // "Get" for chi, "GET" for gin, ...
    PathConvert    func(string) string      // goduct ":id" -> framework path
    PathParamExpr  func(name string) string // `chi.URLParam(r, "id")`
    WrapperSig     string                   // "w http.ResponseWriter, r *http.Request"
    Ctx            string                   // "r.Context()" / "c.Request().Context()"
    Writer         string                   // "w" / "c.Writer" / "c.Response().Writer"
    ReturnsError   bool                     // echo: handlers return error
}
```

A `frameworks` map keyed by name (`chi`, `gin`, `echo`, `mux`) holds
one instance per framework. `Generate(api, w)` calls a lookup, and
the rest of the renderer reads fields off the config rather than
hardcoding chi.

**Rejected:**

- **Sibling packages** (`goadapter/chi`, `goadapter/gin`, ...) —
  ~80% code duplication across the four. Violates the spirit of
  [ADR 0022](0022-generator-conventions.md) §8 (shared logic →
  `internal/gen`).
- **Interface-based dispatch** — overkill for 4 implementations
  picked at run-time; the struct-of-functions is concrete enough.

Framework selection is passed to the generator via the existing
`Generate(api *ir.API, w io.Writer)` signature — through a new
optional argument? No: per
[ADR 0022](0022-generator-conventions.md) §1 the signature is fixed.
Instead, goadapter exposes
**`GenerateFramework(api *ir.API, w io.Writer, framework string) error`**
as a sibling entrypoint, with `Generate` calling it with `"chi"`
(the v0.1-compatible default). The CLI invokes `GenerateFramework`
when `--framework` is non-default, `Generate` otherwise. Both
satisfy the `func(*ir.API, io.Writer) error` shape consumed by
[cmd/goduct/main.go](../../cmd/goduct/main.go)'s specs table — the
CLI registers the chosen variant at flag-parse time.

### 3. Example layout: per-framework golden subdirs

Add three new golden files in chi-basic's testdata tree:

```
examples/chi-basic/testdata/expected/
  client/  (unchanged: types.ts, schemas.ts, client.ts, hooks.ts)
  chi/goduct_routes.go     (was: go/goduct_routes.go)
  gin/goduct_routes.go     (new)
  echo/goduct_routes.go    (new)
  mux/goduct_routes.go     (new)
```

The existing `expected/go/goduct_routes.go` is **renamed** to
`expected/chi/goduct_routes.go` — `go/` was meaningful when there
was one Go adapter; with four it is misleading. The rename is a
golden-test path change (one line each in `goadapter_test.go` and
`e2e_test.go`), and the file contents stay byte-identical.

The chi-basic `api/` handlers stay framework-agnostic (idiomatic
mode, ADR 0014) and are shared across all four goldens. No
duplication of handler source.

**Rejected:**

- Renaming the example dir to `examples/basic/` — touches many paths
  with no functional gain; the historical name `chi-basic` is fine.
- Separate example dirs per framework (`examples/{chi,gin,echo,mux}-basic/`)
  — duplicates handler source four ways for zero per-framework
  benefit.

### 4. Compilation of generated adapter goldens

The goldens live under `testdata/` and are not compiled by `go build
./...`. v0.2 ships **byte-tested** goldens: a goduct contributor reads
the generated output and verifies it compiles against the framework's
documented API at PR review. Full per-framework integration tests
(an example main.go that imports the framework and wires the
generated `Register`) are **deferred to a future session** — adding
gin/echo/mux to goduct's own deps just for testdata compilation is
the wrong tradeoff for v0.2 ship.

A new TODO entry tracks this: "Compile per-framework adapter goldens
in CI (gin/echo/mux integration examples)."

## Consequences

**Easy / unblocked:**

- Existing `goduct gen --go-adapter` invocations behave identically:
  default `--framework chi`, byte-identical output (after the
  testdata rename).
- Adding a fifth framework (fiber, fasthttp, mux's next iteration)
  is a new `frameworks` map entry + one new golden — no surgery on
  the generator's structure.
- The chi-basic handlers stay one source of truth; framework
  differences live entirely in the generator's framework config.

**Hard / giving up:**

- Goldens not compiled. A typo in the gin extraction string would
  pass byte tests and break only at user-build time. Mitigated by
  the rename's symmetry (each framework golden is closely modeled
  on the chi one; deviations are obvious in review) and tracked as
  the v0.2.1+ follow-up.
- One generator file grows by the parameterization. We accept this
  because the alternative (4 generators, 80% duplicated) is worse.

## Cross-references

- [0002](0002-v01-framework-scope.md) — superseded for v0.2 scope
  (still records the v0.1 decision; not formally Superseded since
  the v0.1 decision was correct at the time and the v0.2 lift is
  additive). Index Status stays Accepted.
- [0009](0009-generated-adapter-same-package.md) — adapter same
  package; framework-agnostic, applies to all four.
- [0014](0014-handler-signature-strictness.md) — idiomatic handler
  signature is framework-neutral; this ADR's parameterization works
  per-framework on top of the same handler set.
- [0022](0022-generator-conventions.md) — §1 fixed Generate signature
  motivates the `GenerateFramework` sibling entrypoint rather than
  a third argument.
