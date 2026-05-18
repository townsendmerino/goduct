package zod

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
	_, file, _, _ := runtime.Caller(0) // internal/generators/zod/zod_test.go
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
		"examples/chi-basic/testdata/expected/client/schemas.ts"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("generated schemas.ts != golden:\n%s", lineDiff(string(want), buf.String()))
	}
	// ADR 0024: zod emits no doc comments.
	if strings.Contains(buf.String(), "/**") {
		t.Errorf("schemas.ts must contain no JSDoc; found a /** in output")
	}
}

func TestZodExpr(t *testing.T) {
	cases := []struct {
		name string
		ref  ir.TypeRef
		want string
	}{
		{"string", ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "string"}, "z.string()"},
		{"bool", ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "bool"}, "z.boolean()"},
		{"int", ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "int"}, "z.number().int()"},
		{"int64", ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "int64"}, "z.number().int()"},
		{"uint", ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "uint"}, "z.number().int().nonnegative()"},
		{"uint8", ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "uint8"}, "z.number().int().nonnegative()"},
		{"float64", ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "float64"}, "z.number()"},
		{"time.Time", ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "time.Time"}, "z.string().datetime({ offset: true })"},
		{"time.Duration", ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "time.Duration"}, "z.number().int()"},
		{"[]byte", ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "[]byte"}, "z.string()"},
		{"json.RawMessage", ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "json.RawMessage"}, "z.unknown()"},
		{"uuid.UUID", ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "uuid.UUID"}, "z.string().uuid()"},
		{"named", ir.TypeRef{Kind: ir.KindNamed, Named: "x/api.User"}, "User"},
		{"slice", ir.TypeRef{Kind: ir.KindSlice,
			Element: &ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "string"}}, "z.array(z.string())"},
		{"map", ir.TypeRef{Kind: ir.KindMap,
			Key:   &ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "string"},
			Value: &ir.TypeRef{Kind: ir.KindNamed, Named: "x/api.User"}}, "z.record(z.string(), User)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := zodExpr(c.ref); got != c.want {
				t.Errorf("zodExpr = %q, want %q", got, c.want)
			}
		})
	}
}

func TestFieldExpr(t *testing.T) {
	str := ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "string"}
	num := ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "int"}
	enum := ir.TypeRef{Kind: ir.KindNamed, Named: "x/api.UserStatus"}
	cases := []struct {
		name string
		f    ir.Field
		want string
	}{
		{"string email optional", ir.Field{Type: str, Optional: true,
			Validation: []ir.ValidationRule{{Name: "required"}, {Name: "email"}}},
			"z.string().email().optional()"},
		{"string min=1", ir.Field{Type: str,
			Validation: []ir.ValidationRule{{Name: "required"}, {Name: "min", Arg: "1"}}},
			"z.string().min(1)"},
		{"int min+max source order", ir.Field{Type: num,
			Validation: []ir.ValidationRule{{Name: "min", Arg: "1"}, {Name: "max", Arg: "100"}}},
			"z.number().int().min(1).max(100)"},
		{"pointer optional no validators", ir.Field{Type: str, Optional: true},
			"z.string().optional()"},
		{"enum-typed required", ir.Field{Type: enum,
			Validation: []ir.ValidationRule{{Name: "required"}}}, "UserStatus"},
		{"enum-typed optional", ir.Field{Type: enum, Optional: true}, "UserStatus.optional()"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := fieldExpr(c.f); got != c.want {
				t.Errorf("fieldExpr = %q, want %q", got, c.want)
			}
		})
	}
}
