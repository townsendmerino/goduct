# 0011. Quarantine example golden fixtures in a nested module

**Status:** Superseded by [0013](0013-un-nest-example-testdata-fixtures.md)
**Date:** 2026-05-17

## Context

`examples/chi-basic/expected/go/goduct_routes.go` is a golden snapshot of
what the `--go-adapter` generator must emit. It declares `package api` but
lives in its own directory containing only that one file, and it references
handler/request symbols (`GetUser`, `GetUserRequest`, …) that are defined in
`examples/chi-basic/api/` — a different directory, hence a different package.
It also imports `github.com/go-chi/chi/v5`. As a result `go build ./...` from
the repo root fails: first on the missing chi module, and behind that on a
wall of `undefined:` symbols. The file is text to be diffed by generator
tests, never compiled in place — "make it build" was the wrong framing.
Adding chi to the root module would fix only the first of two blockers and
would pull a dependency the generator and runtime do not themselves need.

## Decision

Give `examples/chi-basic` its own `go.mod`
(`github.com/townsendmerino/goduct/examples/chi-basic`), with a filesystem
`replace github.com/townsendmerino/goduct => ../..` so the example resolves the
parent's `runtime` package locally. The root module's `./...` does not
descend into nested modules, so root build/vet/test stay green. Do **not**
add chi to the root module; it is only needed if and when a test compiles
generated adapter code, and is added then.

## Consequences

- Easy: root `go build/vet/test ./...` is green with no exclusion flags; CI
  needs no special-casing. The chi dependency stays out of the root module.
- Easy: each example owns its dependencies — future gin/echo examples get
  their own module rather than accumulating deps in one place.
- Hard / giving up: `go build ./...` *inside* the example module still fails
  on `expected/go/` (golden snapshot, not a package) — accepted, since CI
  builds the example via `./api/`, not `./...`. Editors/gopls need the nested
  module on their workspace (a `go.work` would help; not added — TBD —
  discuss).
- The documented `examples/chi-basic/expected/` layout is preserved (this was
  the reason this option was chosen over moving fixtures to `testdata/`).

## Alternatives considered

- Move fixtures under `testdata/` — rejected: the go tool would ignore them
  cleanly, but it changes the documented `expected/` layout and the
  chi-basic README's pedagogical structure.
- Leave layout, scope CI to `./internal/... ./runtime/...` — rejected:
  `go build ./...` stays broken for anyone running it locally, a foot-gun.
- Add chi to the root module — rejected: fixes only the import error, not the
  cross-directory `undefined` symbols, and adds an unneeded root dependency.
