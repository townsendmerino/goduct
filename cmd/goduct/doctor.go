package main

// doctor.go implements the `goduct doctor` subcommand (ADR 0045 §4):
// resolve goduct.json + run the analyzer + print a structured report
// (human-readable or --json). Read-only — generation is `goduct gen`'s
// job. Exit codes: 0 ok, 1 analyze error (per ADR 0019), 2 usage error.

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/townsendmerino/goduct/internal/analyzer"
	"github.com/townsendmerino/goduct/internal/cliconfig"
	"github.com/townsendmerino/goduct/internal/ir"
)

func runDoctor(args []string) int {
	fs := flag.NewFlagSet("goduct doctor", flag.ContinueOnError)
	var (
		asJSON     = fs.Bool("json", false, "emit a structured JSON report instead of human-readable text")
		configPath = fs.String("config", "", "path to goduct.json (default: ./goduct.json if present)")
		dir        = fs.String("dir", "", "working dir for resolving the pattern (default: cwd)")
	)

	var pattern string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		pattern = args[0]
		args = args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "goduct doctor: unexpected argument %q\n", fs.Arg(0))
		return 2
	}

	cfg, err := cliconfig.Load(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if pattern == "" && cfg != nil && cfg.Pattern != nil {
		pattern = *cfg.Pattern
	}
	if pattern == "" {
		fmt.Fprintln(os.Stderr, "goduct doctor: missing package pattern (give one as the first arg, or set it in goduct.json)")
		return 2
	}
	resolvedDir := *dir
	if resolvedDir == "" && cfg != nil && cfg.Dir != nil {
		resolvedDir = *cfg.Dir
	}

	api, err := analyzer.Analyze([]string{pattern}, analyzer.LoadOptions{Dir: resolvedDir})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	meta, err := metaFromConfig(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "goduct doctor:", err)
		return 1
	}
	api.Meta = meta

	report := buildReport(pattern, cfg, *configPath, api)
	if *asJSON {
		return emitJSON(os.Stdout, report)
	}
	return emitHuman(os.Stdout, report)
}

// doctorReport is the JSON-serializable shape of the doctor output.
// The human renderer reads the same struct so the two views can't
// silently drift.
type doctorReport struct {
	Pattern    string             `json:"pattern"`
	ConfigPath string             `json:"configPath,omitempty"`
	Config     *cliconfig.Config  `json:"config,omitempty"`
	Routes     []doctorRoute      `json:"routes"`
	Types      []string           `json:"types"`
	Adapters   map[string]string  `json:"adapters,omitempty"`
}

type doctorRoute struct {
	Handler   string `json:"handler"`
	Method    string `json:"method"`
	Path      string `json:"path"`
	Tag       string `json:"tag"`
	Mode      string `json:"mode"`
	BodyType  string `json:"bodyType,omitempty"`
	RespType  string `json:"respType,omitempty"`
	Streaming bool   `json:"streaming,omitempty"`
	WebSocket bool   `json:"websocket,omitempty"`
	Upload    bool   `json:"upload,omitempty"`
}

func buildReport(pattern string, cfg *cliconfig.Config, cfgPath string, api *ir.API) doctorReport {
	r := doctorReport{Pattern: pattern, Config: cfg, Adapters: api.CustomAdapters}
	if cfgPath != "" {
		r.ConfigPath = cfgPath
	} else if cfg != nil {
		r.ConfigPath = cliconfig.DefaultFilename
	}
	for _, rt := range api.Routes {
		mode := "idiomatic"
		if rt.Mode == ir.ModeRaw {
			mode = "raw"
		}
		dr := doctorRoute{
			Handler: rt.HandlerName,
			Method:  rt.Method,
			Path:    rt.Path,
			Tag:     rt.Tag,
			Mode:    mode,
		}
		if rt.BodyType != nil {
			dr.BodyType = short(rt.BodyType.Named)
		}
		if rt.ResponseType != nil {
			dr.RespType = short(rt.ResponseType.Named)
		}
		if rt.StreamType != nil {
			dr.Streaming = true
			dr.RespType = short(rt.StreamType.Named)
		}
		if rt.WebSocket != nil {
			dr.WebSocket = true
		}
		if rt.Upload {
			dr.Upload = true
		}
		r.Routes = append(r.Routes, dr)
	}
	for name := range api.Types {
		r.Types = append(r.Types, short(name))
	}
	sort.Strings(r.Types)
	return r
}

func emitJSON(w io.Writer, r doctorReport) int {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		fmt.Fprintln(os.Stderr, "goduct doctor: encode:", err)
		return 1
	}
	return 0
}

func emitHuman(w io.Writer, r doctorReport) int {
	fmt.Fprintf(w, "goduct doctor — analyzed %s\n\n", r.Pattern)

	if r.Config != nil {
		fmt.Fprintf(w, "Config: %s (loaded)\n", r.ConfigPath)
		if r.Config.Out != nil {
			fmt.Fprintf(w, "  out:       %s\n", *r.Config.Out)
		}
		if r.Config.Framework != nil {
			fmt.Fprintf(w, "  framework: %s\n", *r.Config.Framework)
		}
		if r.Config.OpenAPI != nil {
			fmt.Fprintf(w, "  openapi:   title=%q version=%q\n",
				r.Config.OpenAPI.Title, r.Config.OpenAPI.Version)
		}
		if r.Config.Upload != nil {
			fmt.Fprintf(w, "  upload:    maxBytes=%d\n", r.Config.Upload.MaxBytes)
		}
		if r.Config.Websocket != nil {
			fmt.Fprintf(w, "  websocket: pingInterval=%s\n", r.Config.Websocket.PingInterval)
		}
		if r.Config.Security != nil && len(r.Config.Security.Schemes) > 0 {
			schemes := make([]string, 0, len(r.Config.Security.Schemes))
			for k := range r.Config.Security.Schemes {
				schemes = append(schemes, k)
			}
			sort.Strings(schemes)
			fmt.Fprintf(w, "  security:  %s\n", strings.Join(schemes, ", "))
		}
	} else {
		fmt.Fprintln(w, "Config: (none — running with built-in defaults)")
	}

	fmt.Fprintf(w, "\nRoutes: %d\n", len(r.Routes))
	for _, rt := range r.Routes {
		extras := ""
		if rt.Streaming {
			extras = "  SSE → " + rt.RespType
		} else if rt.WebSocket {
			extras = "  WS"
		} else if rt.Upload {
			extras = "  upload"
		}
		fmt.Fprintf(w, "  %-6s %-26s %-8s %s%s\n",
			rt.Method, rt.Path, rt.Tag, rt.Mode, extras)
	}

	fmt.Fprintf(w, "\nTypes: %d\n", len(r.Types))
	for _, t := range r.Types {
		fmt.Fprintf(w, "  %s\n", t)
	}

	if len(r.Adapters) > 0 {
		fmt.Fprintf(w, "\nCustom adapters: %d\n", len(r.Adapters))
		keys := make([]string, 0, len(r.Adapters))
		for k := range r.Adapters {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(w, "  %s → %s\n", k, r.Adapters[k])
		}
	}
	return 0
}

// short is doctor's local version of the unqualified-name helper
// (mirrors short() in the tsclient generator and openapi generator).
// "github.com/x/api.User" → "User".
func short(qualified string) string {
	if i := strings.LastIndex(qualified, "."); i >= 0 {
		return qualified[i+1:]
	}
	return qualified
}
