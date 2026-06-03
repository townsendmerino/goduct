package goadapter

import (
	"bytes"
	"go/format"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/townsendmerino/goduct/internal/analyzer"
	"github.com/townsendmerino/goduct/internal/ir"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	r, err := filepath.Abs(filepath.Join(filepath.Dir(file), "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func lineDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	n := len(w)
	if len(g) > n {
		n = len(g)
	}
	var b strings.Builder
	for i := 0; i < n; i++ {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl == gl {
			b.WriteString("  " + wl + "\n")
		} else {
			b.WriteString("- " + wl + "\n+ " + gl + "\n")
		}
	}
	return b.String()
}

func TestGenerate_Golden(t *testing.T) {
	// One sub-test per framework. ADR 0030 §3: each framework has its
	// own golden under testdata/expected/<name>/goduct_routes.go.
	for _, fw := range []string{"chi", "gin", "echo", "mux"} {
		t.Run(fw, func(t *testing.T) {
			root := repoRoot(t)
			api, err := analyzer.Analyze([]string{"./examples/chi-basic/api"},
				analyzer.LoadOptions{Dir: root})
			if err != nil {
				t.Fatalf("Analyze: %v", err)
			}
			var buf bytes.Buffer
			if err := GenerateFramework(api, &buf, fw); err != nil {
				t.Fatalf("GenerateFramework(%s): %v", fw, err)
			}
			want, err := os.ReadFile(filepath.Join(root,
				"examples/chi-basic/testdata/expected/"+fw+"/goduct_routes.go"))
			if err != nil {
				t.Fatalf("read golden: %v", err)
			}
			if !bytes.Equal(buf.Bytes(), want) {
				t.Errorf("generated goduct_routes.go != %s golden:\n%s",
					fw, lineDiff(string(want), buf.String()))
			}
		})
	}
}

// TestGenerateFramework_RawMode covers raw-route emission per
// framework: chi/mux register the user's HandlerFunc directly
// (ADR 0031 §3); gin/echo register a generated context-bridge
// `handle<Name>` that adapts the framework context to (w, r) and
// calls the user's handler (ADR 0037). Built from a synthetic IR so
// chi-basic goldens stay focused on idiomatic mode.
func TestGenerateFramework_RawMode(t *testing.T) {
	rawAPI := &ir.API{
		SourceDirs: map[string]string{"pkg": "/tmp/pkg"},
		Routes: []ir.Route{{
			HandlerName:   "RawPing",
			Method:        "GET",
			Path:          "/ping",
			Tag:           "ping",
			Mode:          ir.ModeRaw,
			Pos:           "/tmp/pkg/raw.go:1:1",
			RequestType:   &ir.TypeRef{Kind: ir.KindNamed, Named: "pkg.PingRequest"},
			ResponseType:  &ir.TypeRef{Kind: ir.KindNamed, Named: "pkg.Pong"},
			SuccessStatus: 200,
		}},
		Types: map[string]ir.TypeDef{
			"pkg.PingRequest": {QualifiedName: "pkg.PingRequest", Name: "PingRequest", Kind: ir.TypeStruct},
			"pkg.Pong":        {QualifiedName: "pkg.Pong", Name: "Pong", Kind: ir.TypeStruct},
		},
	}
	t.Run("chi raw routes register directly, no wrapper", func(t *testing.T) {
		var buf bytes.Buffer
		if err := GenerateFramework(rawAPI, &buf, "chi"); err != nil {
			t.Fatalf("GenerateFramework chi: %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, `r.Get("/ping", RawPing)`) {
			t.Errorf("chi raw output missing direct register %q:\n%s", `r.Get("/ping", RawPing)`, out)
		}
		if strings.Contains(out, "func handleRawPing(") {
			t.Errorf("chi raw output unexpectedly contains a wrapper:\n%s", out)
		}
	})
	t.Run("mux raw routes register directly, no wrapper", func(t *testing.T) {
		var buf bytes.Buffer
		if err := GenerateFramework(rawAPI, &buf, "mux"); err != nil {
			t.Fatalf("GenerateFramework mux: %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, `r.HandleFunc("GET /ping", RawPing)`) {
			t.Errorf("mux raw output missing direct register:\n%s", out)
		}
		if strings.Contains(out, "func handleRawPing(") {
			t.Errorf("mux raw output unexpectedly contains a wrapper:\n%s", out)
		}
	})
	t.Run("gin raw routes use a context-bridge wrapper (ADR 0037)", func(t *testing.T) {
		var buf bytes.Buffer
		if err := GenerateFramework(rawAPI, &buf, "gin"); err != nil {
			t.Fatalf("GenerateFramework gin: %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, `r.GET("/ping", handleRawPing)`) {
			t.Errorf("gin raw output missing bridge register %q:\n%s", `r.GET("/ping", handleRawPing)`, out)
		}
		if !strings.Contains(out, "func handleRawPing(c *gin.Context) {") {
			t.Errorf("gin raw output missing bridge declaration:\n%s", out)
		}
		if !strings.Contains(out, "RawPing(c.Writer, c.Request)") {
			t.Errorf("gin bridge body should call the user's handler with (c.Writer, c.Request):\n%s", out)
		}
	})
	t.Run("echo raw routes use a context-bridge wrapper (ADR 0037)", func(t *testing.T) {
		var buf bytes.Buffer
		if err := GenerateFramework(rawAPI, &buf, "echo"); err != nil {
			t.Fatalf("GenerateFramework echo: %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, `r.GET("/ping", handleRawPing)`) {
			t.Errorf("echo raw output missing bridge register %q; got:\n%s", `r.GET("/ping", handleRawPing)`, out)
		}
		if !strings.Contains(out, "func handleRawPing(c echo.Context) error {") {
			t.Errorf("echo raw output missing bridge declaration:\n%s", out)
		}
		if !strings.Contains(out, "RawPing(c.Response().Writer, c.Request())") {
			t.Errorf("echo bridge body should call the user's handler with (c.Response().Writer, c.Request()):\n%s", out)
		}
		if !strings.Contains(out, "return nil") {
			t.Errorf("echo bridge must return nil (no late-error signal from http.HandlerFunc):\n%s", out)
		}
	})
}

// TestGenerateFramework_UnknownErrors: bad framework name is a clear
// error, not a panic — the CLI maps it to exit 2 (ADR 0030 §1).
func TestGenerateFramework_UnknownErrors(t *testing.T) {
	api, err := analyzer.Analyze([]string{"./examples/chi-basic/api"},
		analyzer.LoadOptions{Dir: repoRoot(t)})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	var buf bytes.Buffer
	err = GenerateFramework(api, &buf, "fastapi") // not a Go framework, definitely not ours
	if err == nil {
		t.Fatal("expected error for unknown framework, got nil")
	}
	if !strings.Contains(err.Error(), "unknown framework") {
		t.Errorf("error = %q, want substring 'unknown framework'", err)
	}
}

// TestGoFormatApplied proves the go/format.Source step ran: the output
// is gofmt-canonical, so re-formatting it is a no-op.
func TestGoFormatApplied(t *testing.T) {
	api, err := analyzer.Analyze([]string{"./examples/chi-basic/api"},
		analyzer.LoadOptions{Dir: repoRoot(t)})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	var buf bytes.Buffer
	if err := Generate(api, &buf); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	reformatted, err := format.Source(buf.Bytes())
	if err != nil {
		t.Fatalf("output is not valid Go: %v", err)
	}
	if !bytes.Equal(reformatted, buf.Bytes()) {
		t.Errorf("Generate output is not gofmt-canonical (format.Source step ineffective)")
	}
}

func TestChiPath(t *testing.T) {
	cases := map[string]string{
		"/users":     "/users",
		"/users/:id": "/users/{id}",
		"/a/:x/b/:y": "/a/{x}/b/{y}",
	}
	for in, want := range cases {
		if got := chiPath(in); got != want {
			t.Errorf("chiPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMethodPascal(t *testing.T) {
	for in, want := range map[string]string{
		"GET": "Get", "POST": "Post", "PUT": "Put", "PATCH": "Patch", "DELETE": "Delete",
	} {
		if got := methodPascal(in); got != want {
			t.Errorf("methodPascal(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestQueryAssign(t *testing.T) {
	// Use the chi framework — queryAssign's per-framework variation is
	// only in the writerExpr that appears in error-paths; the rule
	// substrings are framework-independent.
	fw := frameworks["chi"]
	mk := func(b string) ir.Param {
		return ir.Param{GoName: "Limit", WireName: "limit",
			Type: ir.TypeRef{Kind: ir.KindBuiltin, Builtin: b}}
	}
	str := ir.Param{GoName: "Cursor", WireName: "cursor",
		Type: ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "string"}}
	if got := queryAssign(fw, str); got != "\treq.Cursor = q.Get(\"cursor\")\n" {
		t.Errorf("string: %q", got)
	}
	if got := queryAssign(fw, mk("int")); !strings.Contains(got, "strconv.Atoi(v)") ||
		!strings.Contains(got, `"limit must be an integer"`) {
		t.Errorf("int: %q", got)
	}
	if got := queryAssign(fw, mk("bool")); !strings.Contains(got, "strconv.ParseBool(v)") ||
		!strings.Contains(got, `"limit must be a boolean"`) {
		t.Errorf("bool: %q", got)
	}
	if got := queryAssign(fw, mk("float64")); !strings.Contains(got, "strconv.ParseFloat(v, 64)") ||
		!strings.Contains(got, `"limit must be a number"`) {
		t.Errorf("float: %q", got)
	}
}

// TestGenerateFramework_StreamingMode covers ADR 0041: a route with
// StreamType set emits a streaming wrapper that calls the user's
// handler returning a channel, sets SSE headers, and delegates to
// goduct.SSEStream. All four frameworks share the runtime helper so
// the per-framework variation is just the writer/context expressions.
func TestGenerateFramework_StreamingMode(t *testing.T) {
	streamAPI := &ir.API{
		SourceDirs: map[string]string{"pkg": "/tmp/pkg"},
		Routes: []ir.Route{{
			HandlerName:   "WatchOrders",
			Method:        "GET",
			Path:          "/orders/:id/events",
			Tag:           "orders",
			Mode:          ir.ModeIdiomatic,
			Pos:           "/tmp/pkg/stream.go:1:1",
			RequestType:   &ir.TypeRef{Kind: ir.KindNamed, Named: "pkg.WatchOrdersRequest"},
			StreamType:    &ir.TypeRef{Kind: ir.KindNamed, Named: "pkg.OrderEvent"},
			SuccessStatus: 200,
			PathParams: []ir.Param{{
				GoName: "ID", WireName: "id",
				Type: ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "string"},
			}},
		}},
		Types: map[string]ir.TypeDef{
			"pkg.WatchOrdersRequest": {QualifiedName: "pkg.WatchOrdersRequest", Name: "WatchOrdersRequest", Kind: ir.TypeStruct},
			"pkg.OrderEvent":         {QualifiedName: "pkg.OrderEvent", Name: "OrderEvent", Kind: ir.TypeStruct},
		},
	}
	for _, fw := range []string{"chi", "gin", "echo", "mux"} {
		t.Run(fw, func(t *testing.T) {
			var buf bytes.Buffer
			if err := GenerateFramework(streamAPI, &buf, fw); err != nil {
				t.Fatalf("GenerateFramework(%s): %v", fw, err)
			}
			out := buf.String()
			// Header writes are framework-independent (always w / c.Writer / c.Response().Writer).
			if !strings.Contains(out, `.Header().Set("Content-Type", "text/event-stream")`) {
				t.Errorf("%s: missing Content-Type header:\n%s", fw, out)
			}
			if !strings.Contains(out, `.Header().Set("Cache-Control", "no-cache")`) {
				t.Errorf("%s: missing Cache-Control header:\n%s", fw, out)
			}
			if !strings.Contains(out, "goduct.SSEStream(") {
				t.Errorf("%s: missing goduct.SSEStream call:\n%s", fw, out)
			}
			if !strings.Contains(out, "ch, err := WatchOrders(") {
				t.Errorf("%s: missing handler call returning channel:\n%s", fw, out)
			}
			// goformat must succeed (validates output is real Go).
			if _, err := format.Source(buf.Bytes()); err != nil {
				t.Errorf("%s: generated output not valid Go: %v\n%s", fw, err, out)
			}
		})
	}
}
