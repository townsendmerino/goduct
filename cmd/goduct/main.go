// Command goduct is the v0.1 CLI:
//
//	goduct gen <pattern> --out <dir> [--types --zod --client --go-adapter | --all]
//
// Layout (README "Generators"): the TS generators write into --out; the
// Go adapter writes goduct_routes.go *beside the source package* (ADR
// 0009), so its dir comes from a route's Pos, never --out. Stdlib flag
// only; the four generators share the ADR 0022 Generate shape. Exit: 0
// ok; 1 analyze/generate/IO error; 2 usage error.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/townsendmerino/goduct/internal/analyzer"
	"github.com/townsendmerino/goduct/internal/generators/goadapter"
	"github.com/townsendmerino/goduct/internal/generators/hooks"
	"github.com/townsendmerino/goduct/internal/generators/tsclient"
	"github.com/townsendmerino/goduct/internal/generators/tstypes"
	"github.com/townsendmerino/goduct/internal/generators/zod"
	"github.com/townsendmerino/goduct/internal/ir"
)

func main() { os.Exit(run(os.Args[1:])) }

// run is main's testable core: argv without the program name in,
// process exit code out. It never calls os.Exit itself.
func run(argv []string) int {
	if len(argv) == 0 || argv[0] != "gen" {
		usage()
		return 2
	}
	return runGen(argv[1:])
}

// genSpec is one row of the generator dispatch table. fn is the ADR 0022
// Generate entry point; goSrc marks the adapter, whose output goes to
// the source package dir (ADR 0009) rather than --out.
type genSpec struct {
	name  string // CLI flag name, e.g. "types"
	out   string // output filename, e.g. "types.ts"
	fn    func(*ir.API, io.Writer) error
	goSrc bool
}

var specs = []genSpec{
	{"types", "types.ts", tstypes.Generate, false},
	{"zod", "schemas.ts", zod.Generate, false},
	{"client", "client.ts", tsclient.Generate, false},
	{"hooks", "hooks.ts", hooks.Generate, false},
	{"go-adapter", "goduct_routes.go", goadapter.Generate, true},
}

func runGen(args []string) int {
	fs := flag.NewFlagSet("goduct gen", flag.ContinueOnError)
	fs.Usage = usage
	sel := make(map[string]*bool, len(specs))
	for _, s := range specs {
		sel[s.name] = fs.Bool(s.name, false, "generate "+s.out)
	}
	var (
		all       = fs.Bool("all", false, "generate every generator")
		out       = fs.String("out", "", "output directory for the TypeScript generators")
		dir       = fs.String("dir", "", "working directory for resolving the pattern (default: cwd)")
		tags      = fs.String("tags", "", "comma-separated build tags")
		tests     = fs.Bool("tests", false, "include _test.go files when loading")
		watch     = fs.Bool("watch", false, "re-run generators on source-file change (Ctrl-C to stop)")
		framework = fs.String("framework", "chi", "go-adapter framework: chi|gin|echo|mux")
		adapters  = &adapterFlag{}
	)
	fs.Var(adapters, "adapter",
		"custom type adapter (repeatable): <qname>=<string|number|boolean|unknown>")

	// README puts the package pattern first, before any flags; the
	// stdlib flag parser stops at the first non-flag token, so pull the
	// leading positional out by hand before parsing the rest.
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		fmt.Fprintln(os.Stderr, "goduct: missing package pattern (e.g. ./api)")
		usage()
		return 2
	}
	pattern := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return 2 // flag already printed the error + usage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "goduct: unexpected argument %q (pattern must come first)\n", fs.Arg(0))
		return 2
	}

	// --framework is validated pre-analysis so bad values exit 2 fast.
	// The flag is silently ignored when --go-adapter is not selected.
	if !goadapter.FrameworkSupported(*framework) {
		fmt.Fprintf(os.Stderr,
			"goduct: unknown --framework %q (want one of: %s)\n",
			*framework, strings.Join(goadapter.SupportedFrameworks(), ", "))
		return 2
	}

	chosen := pickGenerators(sel, *all)
	if len(chosen) == 0 {
		fmt.Fprintln(os.Stderr,
			"goduct: no generator selected "+
				"(use --types/--zod/--client/--hooks/--go-adapter or --all)")
		usage()
		return 2
	}

	// Inject the framework choice into the go-adapter spec's fn. Other
	// specs are unaffected. Per ADR 0030 §2 the generator's ADR 0022 §1
	// Generate signature is preserved via this closure; the multi-arg
	// variant is GenerateFramework, called via the closure.
	for i, s := range chosen {
		if s.name == "go-adapter" {
			fw := *framework
			chosen[i].fn = func(a *ir.API, w io.Writer) error {
				return goadapter.GenerateFramework(a, w, fw)
			}
		}
	}
	needOut := false
	for _, s := range chosen {
		if !s.goSrc {
			needOut = true
		}
	}
	if needOut && *out == "" {
		fmt.Fprintln(os.Stderr, "goduct: --out is required for the TypeScript generators")
		return 2
	}

	req := runRequest{
		pattern:  pattern,
		out:      *out,
		dir:      *dir,
		tags:     splitTags(*tags),
		tests:    *tests,
		chosen:   chosen,
		needOut:  needOut,
		adapters: adapters.Map(),
	}

	// First run uses the loud-failure contract: any analyze/generate/IO
	// error aborts with exit 1 (ADR 0007). Subsequent --watch runs print
	// errors but keep watching (ADR 0029 §4).
	api, err := generateOnce(req, false)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if !*watch {
		return 0
	}
	if err := watchAndRegen(api, req); err != nil {
		fmt.Fprintf(os.Stderr, "goduct: watch: %v\n", err)
		return 1
	}
	return 0
}

// runRequest is the validated, parameterless-from-here-on description
// of one generation: pattern + flags + the chosen specs. Both the
// one-shot path and the --watch loop call generateOnce(req).
type runRequest struct {
	pattern  string
	out      string
	dir      string
	tags     []string
	tests    bool
	chosen   []genSpec
	needOut  bool
	adapters map[string]string // ADR 0032: qname -> wire shape
}

// generateOnce runs analyze + render-to-memory + write for one regen.
// quiet suppresses the trailing "wrote N file(s)" summary — used by
// the --watch loop, which prints its own timestamped progress lines.
// Returns the (*ir.API, error) so the watch loop can update its watched
// directories from api.SourceDirs.
func generateOnce(req runRequest, quiet bool) (*ir.API, error) {
	api, err := analyzer.Analyze([]string{req.pattern}, analyzer.LoadOptions{
		Dir:            req.dir,
		BuildTags:      req.tags,
		Tests:          req.tests,
		CustomAdapters: req.adapters,
	})
	if err != nil {
		return nil, fmt.Errorf("goduct: analyze: %w", err)
	}

	// Render everything to memory first: a generator failure (e.g. an
	// ADR 0022 §5 panic surfaced as an error) must abort before any
	// file is written, so a failed run never leaves partial output.
	type pending struct {
		path string
		data []byte
	}
	var writes []pending
	for _, s := range req.chosen {
		var buf bytes.Buffer
		if err := s.fn(api, &buf); err != nil {
			return api, fmt.Errorf("goduct: %s: %w", s.name, err)
		}
		var dst string
		if s.goSrc {
			d, err := sourceDir(api)
			if err != nil {
				return api, fmt.Errorf("goduct: %s: %w", s.name, err)
			}
			dst = filepath.Join(d, s.out)
		} else {
			dst = filepath.Join(req.out, s.out)
		}
		writes = append(writes, pending{dst, buf.Bytes()})
	}

	if req.needOut {
		if err := os.MkdirAll(req.out, 0o755); err != nil {
			return api, fmt.Errorf("goduct: create --out: %w", err)
		}
	}
	for _, p := range writes {
		if err := os.WriteFile(p.path, p.data, 0o644); err != nil {
			return api, fmt.Errorf("goduct: write %s: %w", p.path, err)
		}
	}
	if !quiet {
		fmt.Printf("goduct: wrote %d file(s)\n", len(writes))
		for _, p := range writes {
			fmt.Println("  " + p.path)
		}
	}
	return api, nil
}

// pickGenerators resolves the selected specs. --all turns on every
// generator in the specs table.
func pickGenerators(sel map[string]*bool, all bool) []genSpec {
	var out []genSpec
	for _, s := range specs {
		if all || *sel[s.name] {
			out = append(out, s)
		}
	}
	return out
}

// sourceDir returns the filesystem directory of the package whose
// handlers the Go adapter wraps. The adapter must compile in the
// handlers' own package (ADR 0009), so its destination dir comes from
// the analyzer's api.SourceDirs map (ADR 0027), not from --out. v0.1
// is single-package, so the map has exactly one entry; this picks any
// — they're all equivalent. (Multi-package adapter output is v0.2+;
// when it lands, this function evolves to pick the right entry per
// route's source package.)
func sourceDir(api *ir.API) (string, error) {
	for _, d := range api.SourceDirs {
		return d, nil
	}
	return "", fmt.Errorf("cannot locate source package directory (api.SourceDirs is empty)")
}

func splitTags(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	for _, t := range strings.Split(s, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func usage() {
	fmt.Fprint(os.Stderr, `goduct - typed TS/Go clients from annotated Go handlers

usage: goduct gen <pattern> --out <dir> [generators]

generators (opt-in; pick any, or --all):
  --types        types.ts          (TS interfaces + type aliases)
  --zod          schemas.ts        (zod runtime validators)
  --client       client.ts         (typed fetch client)
  --hooks        hooks.ts          (React Query hooks; peer dep
                                    @tanstack/react-query v5)
  --go-adapter   goduct_routes.go  (router wiring; written beside the
                                    source package per ADR 0009, NOT
                                    under --out; framework via --framework)
  --all          all of the above

flags:
  --out <dir>    output dir for the TS generators (required unless only
                 --go-adapter is selected)
  --dir <dir>    working dir for resolving <pattern> (default: cwd)
  --tags <list>  comma-separated build tags
  --tests        include _test.go files when loading
  --watch        re-run generators on source-file change; Ctrl-C to stop
                 (first run aborts on error; subsequent runs print and
                 continue per ADR 0029)
  --framework <fw>  target framework for --go-adapter: chi (default),
                    gin, echo, mux (Go 1.22+ net/http). Per ADR 0030.
  --adapter Q=W     custom type adapter (repeatable): map Go qualified
                    type name Q to JSON wire shape W in
                    {string,number,boolean,unknown}. Built-ins (ADR 0017)
                    win over user adapters. Per ADR 0032. Example:
                      --adapter github.com/shopspring/decimal.Decimal=string

exit codes: 0 ok | 1 analyze/generate/IO error | 2 usage error
`)
}
