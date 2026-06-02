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

// TestAdapterWireTables covers ADR 0032's wire-shape -> target-language
// helpers. Both tables share the same 4-entry domain (string/number/
// boolean/unknown); an unknown wire returns "" so callers can detect
// and panic with the offending value.
func TestAdapterWireTables(t *testing.T) {
	tsCases := map[string]string{
		"string":  "string",
		"number":  "number",
		"boolean": "boolean",
		"unknown": "unknown",
		"bogus":   "", // unknown wire -> empty string sentinel
	}
	for in, want := range tsCases {
		if got := AdapterWireTS(in); got != want {
			t.Errorf("AdapterWireTS(%q) = %q, want %q", in, got, want)
		}
	}
	zodCases := map[string]string{
		"string":  "z.string()",
		"number":  "z.number()",
		"boolean": "z.boolean()",
		"unknown": "z.unknown()",
		"bogus":   "",
	}
	for in, want := range zodCases {
		if got := AdapterWireZod(in); got != want {
			t.Errorf("AdapterWireZod(%q) = %q, want %q", in, got, want)
		}
	}
	// The exported AdapterWires slice is the validation set the CLI
	// uses to reject bad --adapter flags; assert it matches the
	// table domain so the CLI and renderer can't drift.
	want := map[string]bool{"string": true, "number": true, "boolean": true, "unknown": true}
	if len(AdapterWires) != len(want) {
		t.Errorf("AdapterWires len = %d, want %d", len(AdapterWires), len(want))
	}
	for _, w := range AdapterWires {
		if !want[w] {
			t.Errorf("AdapterWires unexpectedly contains %q", w)
		}
	}
}

// TestTypeParamDecl covers ADR 0036's TS-side constraint rendering: nil
// constraint → bare param; single-term → "T extends X"; union → joined
// with " | "; union terms that render to the same TS spelling collapse.
// The renderer is generator-supplied; here we use a trivial stub that
// maps a couple of Go builtins to their TS spellings.
func TestTypeParamDecl(t *testing.T) {
	render := func(r ir.TypeRef) string {
		if r.Kind == ir.KindBuiltin {
			switch r.Builtin {
			case "int", "int64", "float64":
				return "number"
			case "string":
				return "string"
			case "bool":
				return "boolean"
			}
		}
		if r.Kind == ir.KindNamed {
			if i := strings.LastIndex(r.Named, "."); i >= 0 {
				return r.Named[i+1:]
			}
			return r.Named
		}
		return "?"
	}
	cases := []struct {
		name       string
		param      string
		constraint *ir.TypeRef
		want       string
	}{
		{"any constraint (nil)", "T", nil, "T"},
		{"single builtin",
			"T", &ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "int"},
			"T extends number"},
		{"single named",
			"T", &ir.TypeRef{Kind: ir.KindNamed, Named: "x/api.User"},
			"T extends User"},
		{"union int|int64 dedups to number",
			"T", &ir.TypeRef{Kind: ir.KindUnion, UnionTerms: []*ir.TypeRef{
				{Kind: ir.KindBuiltin, Builtin: "int"},
				{Kind: ir.KindBuiltin, Builtin: "int64"},
			}}, "T extends number"},
		{"union int|string keeps both",
			"T", &ir.TypeRef{Kind: ir.KindUnion, UnionTerms: []*ir.TypeRef{
				{Kind: ir.KindBuiltin, Builtin: "int"},
				{Kind: ir.KindBuiltin, Builtin: "string"},
			}}, "T extends number | string"},
		{"union with three terms, two collapse",
			"T", &ir.TypeRef{Kind: ir.KindUnion, UnionTerms: []*ir.TypeRef{
				{Kind: ir.KindBuiltin, Builtin: "int"},
				{Kind: ir.KindBuiltin, Builtin: "int64"},
				{Kind: ir.KindBuiltin, Builtin: "float64"},
			}}, "T extends number"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := TypeParamDecl(c.param, c.constraint, render); got != c.want {
				t.Errorf("TypeParamDecl = %q, want %q", got, c.want)
			}
		})
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
