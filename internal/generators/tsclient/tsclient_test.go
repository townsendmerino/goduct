package tsclient

import (
	"bytes"
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
	root := repoRoot(t)
	api, err := analyzer.Analyze([]string{"./examples/chi-basic/api"},
		analyzer.LoadOptions{Dir: root})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	var buf bytes.Buffer
	if err := Generate(api, &buf); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	want, err := os.ReadFile(filepath.Join(root,
		"examples/chi-basic/testdata/expected/client/client.ts"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("generated client.ts != golden:\n%s", lineDiff(string(want), buf.String()))
	}
}

func TestPathExpr(t *testing.T) {
	cases := map[string]string{
		"/users":     "`/users`",
		"/users/:id": "`/users/${encodeURIComponent(params.id)}`",
		"/a/:x/b/:y": "`/a/${encodeURIComponent(params.x)}/b/${encodeURIComponent(params.y)}`",
	}
	for in, want := range cases {
		if got := pathExpr(in); got != want {
			t.Errorf("pathExpr(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSignature(t *testing.T) {
	str := ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "string"}
	num := ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "int"}
	pathOnly := ir.Route{PathParams: []ir.Param{{WireName: "id", Type: str}}}
	if got := signature(pathOnly, nil); got != "params: { id: string }" {
		t.Errorf("path-only sig = %q", got)
	}
	queryOnly := ir.Route{QueryParams: []ir.Param{
		{WireName: "limit", Type: num, Optional: true},
		{WireName: "cursor", Type: str, Optional: true},
	}}
	if got := signature(queryOnly, nil); got != "params: { limit?: number; cursor?: string }" {
		t.Errorf("query-only sig = %q", got)
	}
	bodyOnly := ir.Route{BodyType: &ir.TypeRef{Kind: ir.KindNamed, Named: "x/api.CreateUserRequest"}}
	if got := signature(bodyOnly, nil); got != "body: t.CreateUserRequest" {
		t.Errorf("body-only sig = %q", got)
	}
	pathBody := ir.Route{
		PathParams: []ir.Param{{WireName: "id", Type: str}},
		BodyType:   &ir.TypeRef{Kind: ir.KindNamed, Named: "x/api.UpdateUserRequest"},
	}
	if got := signature(pathBody, nil); got != "params: { id: string }, body: t.UpdateUserRequest" {
		t.Errorf("path+body sig = %q", got)
	}
}

// A route with no Doc emits no /** */ line (ADR 0024: emit if non-empty).
func TestRenderMethod_NoDoc(t *testing.T) {
	r := ir.Route{
		HandlerName: "DeleteUser", Tag: "users", Method: "DELETE",
		Path: "/users/:id", Doc: "",
		PathParams: []ir.Param{{WireName: "id", Type: ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "string"}}},
	}
	out := renderMethod(r, nil)
	if strings.Contains(out, "/**") {
		t.Errorf("no-doc route must not emit JSDoc, got:\n%s", out)
	}
	if !strings.HasPrefix(out, "      delete: async (params: { id: string }): Promise<void> => {") {
		t.Errorf("unexpected method head:\n%s", out)
	}
}
