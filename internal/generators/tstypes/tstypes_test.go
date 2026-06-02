package tstypes

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
	_, file, _, _ := runtime.Caller(0) // internal/generators/tstypes/tstypes_test.go
	r, err := filepath.Abs(filepath.Join(filepath.Dir(file), "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return r
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
		"examples/chi-basic/testdata/expected/client/types.ts"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("generated types.ts != golden:\n%s", lineDiff(string(want), buf.String()))
	}
}

// lineDiff is a tiny stdlib-only line-by-line diff (no new deps).
func lineDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	var b strings.Builder
	n := len(w)
	if len(g) > n {
		n = len(g)
	}
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

func TestTSType(t *testing.T) {
	enum := ir.TypeRef{Kind: ir.KindNamed, Named: "x/api.UserStatus"}
	cases := []struct {
		name string
		ref  ir.TypeRef
		want string
	}{
		{"string", ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "string"}, "string"},
		{"bool", ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "bool"}, "boolean"},
		{"int", ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "int"}, "number"},
		{"int64", ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "int64"}, "number"},
		{"uint8", ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "uint8"}, "number"},
		{"float64", ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "float64"}, "number"},
		{"time.Time", ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "time.Time"}, "string"},
		{"time.Duration", ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "time.Duration"}, "number"},
		{"[]byte", ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "[]byte"}, "string"},
		{"json.RawMessage", ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "json.RawMessage"}, "unknown"},
		{"uuid.UUID", ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "uuid.UUID"}, "string"},
		{"named-struct", ir.TypeRef{Kind: ir.KindNamed, Named: "x/api.User"}, "User"},
		{"named-enum", enum, "UserStatus"},
		{"slice-string", ir.TypeRef{Kind: ir.KindSlice,
			Element: &ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "string"}}, "string[]"},
		{"slice-enum (no parens: name is a single token)", ir.TypeRef{Kind: ir.KindSlice, Element: &enum}, "UserStatus[]"},
		{"map", ir.TypeRef{Kind: ir.KindMap,
			Key:   &ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "string"},
			Value: &ir.TypeRef{Kind: ir.KindNamed, Named: "x/api.User"}}, "Record<string, User>"},
		// ADR 0033: type-param reference + parametric named refs.
		{"type-param T", ir.TypeRef{Kind: ir.KindTypeParam, TypeParam: "T"}, "T"},
		{"slice of T", ir.TypeRef{Kind: ir.KindSlice,
			Element: &ir.TypeRef{Kind: ir.KindTypeParam, TypeParam: "T"}}, "T[]"},
		{"Page<User>", ir.TypeRef{Kind: ir.KindNamed, Named: "x/api.Page",
			TypeArgs: []*ir.TypeRef{{Kind: ir.KindNamed, Named: "x/api.User"}}}, "Page<User>"},
		{"Map<K, V>", ir.TypeRef{Kind: ir.KindNamed, Named: "x/api.Map",
			TypeArgs: []*ir.TypeRef{
				{Kind: ir.KindBuiltin, Builtin: "string"},
				{Kind: ir.KindNamed, Named: "x/api.User"},
			}}, "Map<string, User>"},
		// Nested instantiations compose: Page<Result<User, Err>>
		{"Page<Result<User, Err>>", ir.TypeRef{Kind: ir.KindNamed, Named: "x/api.Page",
			TypeArgs: []*ir.TypeRef{{Kind: ir.KindNamed, Named: "x/api.Result",
				TypeArgs: []*ir.TypeRef{
					{Kind: ir.KindNamed, Named: "x/api.User"},
					{Kind: ir.KindNamed, Named: "x/api.Err"},
				}}}}, "Page<Result<User, Err>>"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := tsType(c.ref, nil); got != c.want {
				t.Errorf("tsType = %q, want %q", got, c.want)
			}
		})
	}
}

// TestGenerate_ConstrainedGeneric drives the end-to-end declaration
// rendering for ADR 0036: a struct generic over a type-union constraint
// must surface in TS as `<T extends number>` (dedup applied).
func TestGenerate_ConstrainedGeneric(t *testing.T) {
	intRef := &ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "int"}
	int64Ref := &ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "int64"}
	stringRef := &ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "string"}
	api := &ir.API{Types: map[string]ir.TypeDef{
		"x/svc.Box": {
			QualifiedName: "x/svc.Box",
			Name:          "Box",
			Kind:          ir.TypeStruct,
			TypeParams:    []string{"T"},
			TypeParamConstraints: []*ir.TypeRef{
				{Kind: ir.KindUnion, UnionTerms: []*ir.TypeRef{intRef, int64Ref}},
			},
			Fields: []ir.Field{{
				GoName: "V", JSONName: "v", Source: ir.FieldSourceJSON,
				Type: ir.TypeRef{Kind: ir.KindTypeParam, TypeParam: "T"},
			}},
			Pos: "f.go:1:6",
		},
		"x/svc.Pair": {
			QualifiedName: "x/svc.Pair",
			Name:          "Pair",
			Kind:          ir.TypeStruct,
			TypeParams:    []string{"K", "V"},
			TypeParamConstraints: []*ir.TypeRef{
				{Kind: ir.KindBuiltin, Builtin: "string"},
				{Kind: ir.KindUnion, UnionTerms: []*ir.TypeRef{intRef, stringRef}},
			},
			Fields: []ir.Field{
				{GoName: "K", JSONName: "k", Source: ir.FieldSourceJSON,
					Type: ir.TypeRef{Kind: ir.KindTypeParam, TypeParam: "K"}},
				{GoName: "V", JSONName: "v", Source: ir.FieldSourceJSON,
					Type: ir.TypeRef{Kind: ir.KindTypeParam, TypeParam: "V"}},
			},
			Pos: "f.go:2:6",
		},
	}}
	var buf bytes.Buffer
	if err := Generate(api, &buf); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "export interface Box<T extends number> {") {
		t.Errorf("missing dedup'd union constraint; output:\n%s", out)
	}
	if !strings.Contains(out, "export interface Pair<K extends string, V extends number | string> {") {
		t.Errorf("missing multi-param mixed constraint rendering; output:\n%s", out)
	}
}
