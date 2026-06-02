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
	for _, fw := range []string{"chi", "gin"} {
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
