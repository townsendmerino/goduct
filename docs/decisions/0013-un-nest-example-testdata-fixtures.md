# 0013. Un-nest the example; ignore golden fixtures via testdata/

**Status:** Accepted
**Date:** 2026-05-17
**Supersedes:** [0011](0011-golden-fixtures-nested-module.md)

## Context

ADR 0011 quarantined `examples/chi-basic` into its own nested module so the
chi-importing, cross-directory `goduct_routes.go` golden snapshot would not
break root `go build ./...`. Building the package loader (`internal/analyzer`)
then needed to load a real type-checked package as its test fixture, and the
canonical fixture is `examples/chi-basic/api`. A root-module test cannot load
a package from a separate nested module via a relative pattern — verified
empirically: `go list ./examples/chi-basic/api` from root fails with "main
module does not contain package …", and `./examples/...` matches no packages.
A throwaway `go.work` spike fixed only the explicit-path case (not
`./examples/...`), was 0011's own parked "TBD", and adds a workspace file
outside the loader's scope. The actual problem 0011 solved was narrow: one
non-compilable snapshot file breaking `./...`. The Go tool ignores any
directory named `testdata`, which neutralizes that without a separate module
— the alternative 0011 had rejected solely to preserve the documented
`expected/` path.

## Decision

Supersede ADR 0011. Delete `examples/chi-basic/go.mod` so the example rejoins
the root module. Move `examples/chi-basic/expected/` →
`examples/chi-basic/testdata/expected/`. The Go tool skips `testdata/`, so
`goduct_routes.go` (chi import + cross-directory symbols) is never compiled
and root `go build/vet/test ./...` stays green. `examples/chi-basic/api`
becomes a normal root-module package the analyzer and its tests load
directly. No `go.work`; no chi in the root module.

## Consequences

- Easy: `examples/chi-basic/api` is a first-class root package — loader and
  analyzer tests load it with a plain relative pattern, `./...` includes it,
  and editors/gopls work with no workspace file (this also resolves 0011's
  `go.work` TBD by removing the need for one).
- Easy: still no chi dependency in the root module; the golden snapshot stays
  byte-for-byte text under `testdata/expected/`.
- Hard / giving up: the documented layout changed (`expected/` →
  `testdata/expected/`); `examples/chi-basic/README.md` updated with the
  rationale. `testdata/` reads as "ancillary test data," marginally less
  discoverable than a top-level `expected/` showcase — accepted.
- Giving up per-example dependency isolation (a benefit 0011 cited): future
  gin/echo examples follow the same `testdata/` pattern in the root module
  rather than owning a module. Revisit if an example ever needs a dependency
  the root module should not carry — TBD — discuss when that arises.
- The ADR 0012 "compile + vet the generated adapter" test is unaffected in
  intent; it still needs its own buildable package and chi when built, and
  now reads the golden from `testdata/expected/go/`.

## Alternatives considered

- Keep 0011's nested module + add `go.work` — rejected: it was 0011's parked
  TBD, adds a workspace file, still doesn't make `./examples/...` resolve from
  root, and is more moving parts than deleting one `go.mod`.
- Loader resolves the target path's enclosing module — rejected: pushes
  module-resolution logic into a file specified as a <120-line thin wrapper.
- All loader fixtures via `t.TempDir`, keep nesting — rejected: viable, but
  discards the canonical, reviewed chi-basic fixture the prompt and ADRs treat
  as the design anchor.
