package analyzer

// analyze.go is the orchestration seam: it sequences Load -> DiscoverRoutes
// -> DiscoverTypes per package and merges the results into one ir.API. It
// adds no analysis of its own; its only value-add is merging and
// non-short-circuiting error accumulation so `goduct gen` can report every
// problem from every package in a single run.

import (
	"errors"
	"path/filepath"

	"github.com/townsendmerino/goduct/internal/ir"
)

// Analyze loads the packages matching patterns, discovers all annotated
// routes, traverses every type those routes reference, and returns a
// fully-populated ir.API. Errors from any phase/package are joined; the
// function does not short-circuit (a routes error in one package does not
// prevent type discovery in another). Returns (nil, err) only when no
// packages could be loaded at all.
func Analyze(patterns []string, opts LoadOptions) (*ir.API, error) {
	pkgs, loadErr := Load(patterns, opts)
	var errs []error
	if loadErr != nil {
		if len(pkgs) == 0 {
			return nil, loadErr // can't continue without packages
		}
		// Presently unreachable: Load returns (nil, err) on every error
		// path. Kept for fidelity if Load ever reports partial success.
		errs = append(errs, loadErr)
	}

	api := &ir.API{
		Types:      map[string]ir.TypeDef{},
		SourceDirs: map[string]string{},
	}
	for _, pkg := range pkgs {
		routes, err := DiscoverRoutes(pkg)
		if err != nil {
			errs = append(errs, err)
		}
		// DiscoverTypes is seeded from this package's own handlers, so it
		// must get this package's routes, never the combined slice.
		types, err := DiscoverTypes(pkg, routes)
		if err != nil {
			errs = append(errs, err)
		}
		api.Routes = append(api.Routes, routes...)
		for k, v := range types {
			if _, dup := api.Types[k]; dup {
				panic("goduct: internal: duplicate qualified type name across packages: " + k)
			}
			api.Types[k] = v
		}
		// ADR 0027: record the source directory for this package so
		// goadapter consumers (the CLI) can write goduct_routes.go beside
		// the source (ADR 0009) without parsing Route.Pos. Every loaded
		// package has at least one Go file; if it doesn't, leave the entry
		// off rather than panicking — empty input is a `goduct gen` no-op.
		if len(pkg.GoFiles) > 0 {
			api.SourceDirs[pkg.PkgPath] = filepath.Dir(pkg.GoFiles[0])
		}
	}
	return api, errors.Join(errs...)
}
