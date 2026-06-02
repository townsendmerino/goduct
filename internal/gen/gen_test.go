package gen

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/townsendmerino/goduct/internal/analyzer"
	"github.com/townsendmerino/goduct/internal/ir"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0) // internal/gen/gen_test.go
	r, err := filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func chiBasic(t *testing.T) *ir.API {
	t.Helper()
	api, err := analyzer.Analyze([]string{"./examples/chi-basic/api"},
		analyzer.LoadOptions{Dir: repoRoot(t)})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	return api
}

func TestSourcePath_ChiBasic(t *testing.T) {
	api := chiBasic(t)
	// Derive the expected import path from a route's named response type
	// (do not hardcode the module path).
	var want string
	for _, r := range api.Routes {
		if r.ResponseType != nil && r.ResponseType.Kind == ir.KindNamed {
			want = r.ResponseType.Named[:strings.LastIndex(r.ResponseType.Named, ".")]
			break
		}
	}
	if want == "" {
		t.Fatal("no named response type found to derive expected path")
	}
	if got := SourcePath(api); got != want {
		t.Errorf("SourcePath = %q, want %q", got, want)
	}
}

func TestSourcePath_Empty(t *testing.T) {
	if got := SourcePath(&ir.API{}); got != "" {
		t.Errorf("SourcePath(empty) = %q, want \"\"", got)
	}
}

func TestSourcePath_TwoPackages(t *testing.T) {
	api := &ir.API{Routes: []ir.Route{
		{ResponseType: &ir.TypeRef{Kind: ir.KindNamed, Named: "pkg/b.X"}},
		{ResponseType: &ir.TypeRef{Kind: ir.KindNamed, Named: "pkg/a.Y"}},
	}}
	if got := SourcePath(api); got != "pkg/a, pkg/b" {
		t.Errorf("SourcePath = %q, want %q", got, "pkg/a, pkg/b")
	}
}

func TestTopoSortTypes_ChiBasic(t *testing.T) {
	order := TopoSortTypes(chiBasic(t))
	idx := map[string]int{}
	for i, td := range order {
		idx[td.Name] = i
	}
	if !(idx["UserStatus"] < idx["User"]) {
		t.Errorf("UserStatus (%d) must precede User (%d)", idx["UserStatus"], idx["User"])
	}
	if !(idx["User"] < idx["ListUsersResponse"]) {
		t.Errorf("User (%d) must precede ListUsersResponse (%d)", idx["User"], idx["ListUsersResponse"])
	}
}

func TestTopoSortTypes_MutualCycle(t *testing.T) {
	api := &ir.API{Types: map[string]ir.TypeDef{
		"m.A": {QualifiedName: "m.A", Name: "A", Kind: ir.TypeStruct, Pos: "f.go:1:6",
			Fields: []ir.Field{{GoName: "B", JSONName: "b", Source: ir.FieldSourceJSON,
				Type: ir.TypeRef{Kind: ir.KindNamed, Named: "m.B"}}}},
		"m.B": {QualifiedName: "m.B", Name: "B", Kind: ir.TypeStruct, Pos: "f.go:2:6",
			Fields: []ir.Field{{GoName: "A", JSONName: "a", Source: ir.FieldSourceJSON,
				Type: ir.TypeRef{Kind: ir.KindNamed, Named: "m.A"}}}},
	}}
	order := TopoSortTypes(api)
	if len(order) != 2 {
		t.Fatalf("got %d types, want 2", len(order))
	}
	// Deterministic: SCC members in source order (A at line 1, B at line 2).
	if order[0].Name != "A" || order[1].Name != "B" {
		t.Errorf("order = %s,%s; want A,B (source order within SCC)", order[0].Name, order[1].Name)
	}
}

func TestWireFields_UpdateUserRequest(t *testing.T) {
	api := chiBasic(t)
	var td ir.TypeDef
	for k, v := range api.Types {
		if strings.HasSuffix(k, ".UpdateUserRequest") {
			td = v
		}
	}
	if td.Name != "UpdateUserRequest" {
		t.Fatal("UpdateUserRequest not found in api.Types")
	}
	var names []string
	for _, f := range WireFields(td) {
		names = append(names, f.GoName)
	}
	got := strings.Join(names, ",")
	if got != "Name,Status" { // ID is path-sourced → excluded
		t.Errorf("WireFields GoNames = %q, want %q", got, "Name,Status")
	}
}

func TestJSDoc_vs_JSDocFull(t *testing.T) {
	// Single sentence: both variants identical; identifier+copula stripped.
	const t1 = "User is the canonical user shape returned by the API."
	if got := JSDoc("User", t1); got != "The canonical user shape returned by the API." {
		t.Errorf("JSDoc = %q", got)
	}
	if got := JSDocFull("User", t1); got != "The canonical user shape returned by the API." {
		t.Errorf("JSDocFull = %q", got)
	}
	// Multi-sentence: JSDoc truncates (Synopsis), JSDocFull preserves.
	const t2 = "UpdateUser updates fields on an existing user. Omitted fields are not changed."
	if got := JSDoc("UpdateUser", t2); got != "Updates fields on an existing user." {
		t.Errorf("JSDoc (truncated) = %q", got)
	}
	if got := JSDocFull("UpdateUser", t2); got != "Updates fields on an existing user. Omitted fields are not changed." {
		t.Errorf("JSDocFull (full) = %q", got)
	}
	// Handler-name identifier (not a copula word after it) — "returns" kept.
	if got := JSDocFull("GetUser", "GetUser returns a single user by ID."); got != "Returns a single user by ID." {
		t.Errorf("JSDocFull GetUser = %q", got)
	}
	// Empty → "".
	if got := JSDocFull("X", "   "); got != "" {
		t.Errorf("JSDocFull empty = %q", got)
	}
}

func TestPackageName(t *testing.T) {
	if got := PackageName(chiBasic(t)); got != "api" {
		t.Errorf("PackageName(chi-basic) = %q, want %q", got, "api")
	}
	if got := PackageName(&ir.API{}); got != "" {
		t.Errorf("PackageName(empty) = %q, want \"\"", got)
	}
}

func TestMethodName(t *testing.T) {
	cases := []struct {
		handler, tag, want string
	}{
		{"GetUser", "users", "get"},
		{"ListUsers", "users", "list"},
		{"CreateUser", "users", "create"},
		{"UpdateUser", "users", "update"},
		{"DeleteUser", "users", "delete"},
		{"BulkCreateUser", "users", "bulkCreate"}, // camelCase preserved
		{"Healthcheck", "system", "healthcheck"},  // no suffix match → first-rune lower
		{"GetWidget", "inventory", "getWidget"},   // tag has no 's'; no match
	}
	for _, c := range cases {
		if got := MethodName(c.handler, c.tag); got != c.want {
			t.Errorf("MethodName(%q,%q) = %q, want %q", c.handler, c.tag, got, c.want)
		}
	}
}
