package analyzer

// loader.go is a thin, well-configured wrapper around
// golang.org/x/tools/go/packages. Its only responsibility is "give me
// usable type-checked packages, or a clear error." It does NOT look for
// goduct annotations, build IR, know about routes/handlers, or filter
// functions — those are later analyzer steps.

import (
	"errors"
	"fmt"
	"strings"

	"golang.org/x/tools/go/packages"
)

// LoadOptions configures Load.
type LoadOptions struct {
	// Tests includes _test.go files in loaded packages. Default: false.
	// (Handlers in _test.go are not supported; this exists for future
	// test-driven analysis use cases.)
	Tests bool

	// BuildTags applied during loading (e.g. "integration"). Default: nil.
	BuildTags []string

	// Dir overrides the working directory for resolving relative paths.
	// Default: process cwd.
	Dir string
}

// loadMode is the fixed capability set the analyzer needs: full syntax plus
// resolved type info for the target packages AND their dependencies. Every
// flag is load-bearing — NeedSyntax+NeedTypesInfo to walk the AST with
// resolved types, NeedDeps+NeedImports so cross-package types are real
// *types.Named (not placeholders), NeedTypes for qualified-name resolution,
// NeedCompiledGoFiles for build-tag scenarios. Do not drop any.
const loadMode = packages.NeedName |
	packages.NeedFiles |
	packages.NeedCompiledGoFiles |
	packages.NeedImports |
	packages.NeedDeps |
	packages.NeedTypes |
	packages.NeedSyntax |
	packages.NeedTypesInfo |
	packages.NeedTypesSizes

// Load loads one or more Go packages from the given patterns (go list
// syntax: "./api", "./...", "github.com/x/y/api"). Packages are returned in
// the order packages.Load yields them (source order). It is an error if
// loading fails, if any loaded package has parse/type errors, or if no
// package matches — the analyzer never proceeds on partial type info.
func Load(patterns []string, opts LoadOptions) ([]*packages.Package, error) {
	cfg := &packages.Config{
		Mode:  loadMode,
		Tests: opts.Tests,
		Dir:   opts.Dir,
	}
	if len(opts.BuildTags) > 0 {
		cfg.BuildFlags = []string{"-tags=" + strings.Join(opts.BuildTags, ",")}
	}

	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, fmt.Errorf("loading packages %v: %w", patterns, err)
	}
	if len(pkgs) == 0 {
		return nil, fmt.Errorf("no packages matched patterns: %s", strings.Join(patterns, " "))
	}

	var errs []error
	for _, pkg := range pkgs {
		for _, e := range pkg.Errors {
			errs = append(errs, errors.New(formatPkgError(e)))
		}
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("packages %s had errors: %w",
			strings.Join(patterns, " "), errors.Join(errs...))
	}
	return pkgs, nil
}

// formatPkgError renders a packages.Error in the ADR 0019 Format A
// template `goduct: <file>:<line>:<col>: <msg>`. For ListError (and
// any other case the go/packages loader produces without a position),
// e.Pos is empty; the conventional `-` placeholder fills the position
// field so the prefix shape stays uniform.
func formatPkgError(e packages.Error) string {
	pos := e.Pos
	if pos == "" {
		pos = "-"
	}
	return fmt.Sprintf("goduct: %s: %s", pos, e.Msg)
}
