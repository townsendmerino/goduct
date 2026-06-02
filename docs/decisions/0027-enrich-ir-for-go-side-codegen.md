# 0027. Enrich the IR for Go-side code generation (v0.2)

**Status:** Accepted
**Date:** 2026-06-02

## Context

The v0.1 IR ([0016](0016-field-source-in-ir.md)) was designed
around the **wire shape** — what crosses the JSON boundary. That was
the right scope for v0.1: three of the four generators (tstypes, zod,
tsclient) are wire-shape consumers and have everything they need.

The Go-side generator and the CLI are not wire-shape consumers — they
need **handler-shape** information that the v0.1 IR does not carry,
and two workarounds emerged to bridge the gap:

1. **goadapter / handler request type.** goadapter emits
   `var req <RequestType>` in every wrapper. For body routes
   (POST/PUT/PATCH with JSON fields), this is `ir.Route.BodyType.Named`.
   For non-body routes (GET, DELETE, body-less POST), `BodyType` is
   `nil` and the IR exposes no other handle on the handler's
   second-parameter type. ADR
   [0026](0026-goadapter-request-type-name-convention.md) introduced a
   v0.1 naming convention (`<HandlerName>Request`) so goadapter could
   look the type up in `api.Types`; non-conforming names hit a panic
   with remediation text.
2. **CLI / source package directory.** The Go adapter is written
   *beside the source package* (ADR
   [0009](0009-generated-adapter-same-package.md)), not under `--out`.
   `cmd/goduct/main.go` needs the source directory to know where to
   write `goduct_routes.go`, but `*ir.API` carries no per-package
   filesystem path. The CLI works around this by parsing
   `Route.Pos` (`"file:line:col"`) and `filepath.Dir`-ing the result.

Both workarounds point at one root cause: `ir.API` / `ir.Route` don't
carry enough identity/position information for Go-side codegen. v0.1
shipped the workarounds rather than reopening the IR mid-milestone;
this ADR pays that debt.

## Decision

Two additive, backward-compatible fields are added to the IR.

**1. `ir.Route.RequestType *TypeRef`** — the handler's second-parameter
type (`T` in `func(ctx, T) ...`). Always non-nil for a discovered
route under [ADR 0014](0014-handler-signature-strictness.md). For body
routes, `RequestType` and `BodyType` both point at the same named
type; both fields are populated, neither is preferred. For non-body
routes, `RequestType` is populated and `BodyType` is `nil`. The
analyzer (`DiscoverRoutes`) already has the type in hand during
signature validation; populating the field is a one-line change.

**2. `ir.API.SourceDirs map[string]string`** — each package's import
path mapped to its filesystem directory (e.g.
`"github.com/townsendmerino/goduct/examples/chi-basic/api"` →
`"/abs/path/to/examples/chi-basic/api"`). The analyzer derives each
entry from the loaded `*packages.Package`'s file list. For v0.1's
single-package input the map has exactly one entry; the multi-package
shape is forward-compatible (each package supplies its own entry).

The map is preferred over a single `SourceDir string` because the
forward-compatibility cost is zero (`len(api.SourceDirs) == 1` in v0.2
single-package mode; consumers iterate or pick by known path) and a
single-string field would force a v0.3 IR break the first time
multi-package input lands.

ADR 0026's `<HandlerName>Request` naming convention is **superseded**
by `RequestType` and falls away — any handler may use any request-type
name. Status of ADR 0026 is updated to `Superseded by 0027`. ADR
0026's *Decision section is not edited* (immutability rule); the
supersession chain is the discoverable record.

## Consequences

**Easy / unblocked:**

- goadapter's `requestTypeName(api, rt)` collapses to
  `shortName(rt.RequestType.Named)`. The `api.Types` lookup, the
  long-form panic with remediation hint, and the v0.1 naming
  constraint all disappear.
- `cmd/goduct/main.go`'s `sourceDir(api)` reads `api.SourceDirs`
  directly. The `Route.Pos`-parsing helpers (`posFile`, `allDigits`)
  are deleted.
- Multi-package input becomes representable in the IR. (The
  generators producing multi-package output is still v0.2+; this
  removes the IR as the blocker.)
- The chi-basic golden output is byte-identical — the resolved
  request-type names are the same; only the resolution path changed.

**Hard / giving up:**

- The IR is no longer "frozen post-v0.1". It is additive-only post-
  v0.1: new fields are allowed, existing fields and their semantics
  are not changed without a superseding ADR. This is the same
  contract the analyzer/generator pair has always had with the IR,
  now explicit.
- Consumers reading the IR from disk (none yet — the IR is not
  serialized in v0.1) would need to handle the new fields. Tracked
  for when/if IR persistence is added.

**Out of scope for this ADR:**

- Multi-package generator output (which package gets which generated
  file). The IR change is the prerequisite; the generator-side
  decisions land in their own ADR when the feature is implemented.

## Alternatives considered

- **Keep the [0026](0026-goadapter-request-type-name-convention.md)
  naming convention forever** — rejected. The convention is a
  user-facing surprise (handler names dictate request-type names) for
  a workaround that costs one IR field to remove.
- **Pass `*packages.Package` through to generators** — rejected.
  [ADR 0022](0022-generator-conventions.md) §1 fixes the generator
  signature as `Generate(*ir.API, io.Writer) error`; introducing a
  loader-side channel violates the contract and couples generators to
  go/packages.
- **A single `ir.API.SourceDir string` field** — rejected. Forces a
  v0.3 IR break the moment multi-package input is supported, for no
  v0.2 simplicity benefit.
- **A richer `Package` struct (`{Path, Dir, GoFiles, ...}`) on
  `ir.API`** — rejected for now as scope creep. Add the fields when a
  consumer needs them; the map is the minimum viable change.

## Cross-references

- [0009](0009-generated-adapter-same-package.md) — adapter written
  beside source (motivates `SourceDirs`).
- [0014](0014-handler-signature-strictness.md) — guarantees
  `RequestType` is always present and is a named struct.
- [0016](0016-field-source-in-ir.md) — the original IR shape decision
  this ADR builds on.
- [0022](0022-generator-conventions.md) §1 — fixed generator
  signature (motivates putting the data on `*ir.API` rather than
  side-channeling).
- [0026](0026-goadapter-request-type-name-convention.md) — superseded.
