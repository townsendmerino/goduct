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
	out := renderMethod(r, &ir.API{})
	if strings.Contains(out, "/**") {
		t.Errorf("no-doc route must not emit JSDoc, got:\n%s", out)
	}
	if !strings.HasPrefix(out, "      delete: async (params: { id: string }): Promise<void> => {") {
		t.Errorf("unexpected method head:\n%s", out)
	}
}

// TestTSTypeRef_Generics covers the t.-alias-prefix renderer used for
// the client's Promise<T> return types under ADR 0033 §5.
func TestTSTypeRef_Generics(t *testing.T) {
	cases := []struct {
		name string
		ref  ir.TypeRef
		want string
	}{
		{"non-generic named", ir.TypeRef{Kind: ir.KindNamed, Named: "x/api.User"}, "t.User"},
		{"Page<User>", ir.TypeRef{Kind: ir.KindNamed, Named: "x/api.Page",
			TypeArgs: []*ir.TypeRef{{Kind: ir.KindNamed, Named: "x/api.User"}}}, "t.Page<t.User>"},
		{"Page<Result<User, Err>>", ir.TypeRef{Kind: ir.KindNamed, Named: "x/api.Page",
			TypeArgs: []*ir.TypeRef{{Kind: ir.KindNamed, Named: "x/api.Result",
				TypeArgs: []*ir.TypeRef{
					{Kind: ir.KindNamed, Named: "x/api.User"},
					{Kind: ir.KindNamed, Named: "x/api.Err"},
				}}}}, "t.Page<t.Result<t.User, t.Err>>"},
		// Non-named falls through to tsType (no t. prefix on builtin/slice).
		{"slice falls through", ir.TypeRef{Kind: ir.KindSlice,
			Element: &ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "string"}}, "string[]"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := tsTypeRef(c.ref); got != c.want {
				t.Errorf("tsTypeRef = %q, want %q", got, c.want)
			}
		})
	}
}

// TestSchemasExpr covers the schemas.-prefix zod-expression renderer
// used at .parse() call sites under ADR 0033 §5. A generic
// instantiation invokes the factory; nested instantiations compose.
func TestSchemasExpr(t *testing.T) {
	cases := []struct {
		name string
		ref  ir.TypeRef
		want string
	}{
		{"non-generic named", ir.TypeRef{Kind: ir.KindNamed, Named: "x/api.User"}, "schemas.User"},
		{"Page(User)", ir.TypeRef{Kind: ir.KindNamed, Named: "x/api.Page",
			TypeArgs: []*ir.TypeRef{{Kind: ir.KindNamed, Named: "x/api.User"}}},
			"schemas.Page(schemas.User)"},
		{"Page(Result(User, Err)) nested", ir.TypeRef{Kind: ir.KindNamed, Named: "x/api.Page",
			TypeArgs: []*ir.TypeRef{{Kind: ir.KindNamed, Named: "x/api.Result",
				TypeArgs: []*ir.TypeRef{
					{Kind: ir.KindNamed, Named: "x/api.User"},
					{Kind: ir.KindNamed, Named: "x/api.Err"},
				}}}},
			"schemas.Page(schemas.Result(schemas.User, schemas.Err))"},
		// Builtin TypeArg lands a zod expression rather than a schemas. ref.
		{"Optional(string) builtin arg", ir.TypeRef{Kind: ir.KindNamed, Named: "x/api.Optional",
			TypeArgs: []*ir.TypeRef{{Kind: ir.KindBuiltin, Builtin: "string"}}},
			"schemas.Optional(z.string())"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := schemasExpr(c.ref); got != c.want {
				t.Errorf("schemasExpr = %q, want %q", got, c.want)
			}
		})
	}
}

// TestGenerate_StreamingMethod covers ADR 0041: a streaming route
// produces an AsyncIterable<E> method that delegates to the
// streamSSE scaffold helper (which is conditionally appended only
// when at least one streaming route exists).
func TestGenerate_StreamingMethod(t *testing.T) {
	api := &ir.API{
		Types: map[string]ir.TypeDef{
			"pkg.WatchReq":   {QualifiedName: "pkg.WatchReq", Name: "WatchReq", Kind: ir.TypeStruct},
			"pkg.OrderEvent": {QualifiedName: "pkg.OrderEvent", Name: "OrderEvent", Kind: ir.TypeStruct,
				Fields: []ir.Field{{GoName: "ID", JSONName: "id", Source: ir.FieldSourceJSON,
					Type: ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "string"}}}},
		},
		Routes: []ir.Route{{
			HandlerName:   "WatchOrders",
			Method:        "GET",
			Path:          "/orders/events",
			Tag:           "orders",
			Mode:          ir.ModeIdiomatic,
			RequestType:   &ir.TypeRef{Kind: ir.KindNamed, Named: "pkg.WatchReq"},
			StreamType:    &ir.TypeRef{Kind: ir.KindNamed, Named: "pkg.OrderEvent"},
			SuccessStatus: 200,
		}},
	}
	var buf bytes.Buffer
	if err := Generate(api, &buf); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "async function* streamSSE<E>") {
		t.Errorf("missing streamSSE helper (should be emitted when streaming routes exist):\n%s", out)
	}
	if !strings.Contains(out, "AsyncIterable<t.OrderEvent>") {
		t.Errorf("missing AsyncIterable<t.OrderEvent> return type:\n%s", out)
	}
	if !strings.Contains(out, "streamSSE<t.OrderEvent>(opts,") {
		t.Errorf("streaming method should delegate to streamSSE helper:\n%s", out)
	}
	// Non-streaming method shape (`async (...) => {`) should NOT appear
	// for the streaming method.
	if strings.Contains(out, "watch: async (") {
		t.Errorf("streaming method should not use the async-arrow shape (it returns AsyncIterable, not Promise):\n%s", out)
	}
}

// TestGenerate_NoStreamingNoHelper: when an API has zero streaming
// routes the streamSSE helper is NOT emitted, keeping the v0.4
// scaffold byte-identical (verified at the chi-basic golden level
// by the existing TestGenerate_Golden).
func TestGenerate_NoStreamingNoHelper(t *testing.T) {
	api := &ir.API{
		Types: map[string]ir.TypeDef{
			"pkg.R": {QualifiedName: "pkg.R", Name: "R", Kind: ir.TypeStruct,
				Fields: []ir.Field{{GoName: "ID", JSONName: "id", Source: ir.FieldSourceJSON,
					Type: ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "string"}}}},
		},
		Routes: []ir.Route{{
			HandlerName: "Get", Method: "GET", Path: "/r", Tag: "r",
			Mode:          ir.ModeIdiomatic,
			RequestType:   &ir.TypeRef{Kind: ir.KindNamed, Named: "pkg.R"},
			ResponseType:  &ir.TypeRef{Kind: ir.KindNamed, Named: "pkg.R"},
			SuccessStatus: 200,
		}},
	}
	var buf bytes.Buffer
	if err := Generate(api, &buf); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if strings.Contains(buf.String(), "streamSSE") {
		t.Errorf("streamSSE helper should NOT be emitted when no streaming routes:\n%s", buf.String())
	}
}
