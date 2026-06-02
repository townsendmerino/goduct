package hooks

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
		"examples/chi-basic/testdata/expected/client/hooks.ts"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("generated hooks.ts != golden:\n%s", lineDiff(string(want), buf.String()))
	}
}

func TestTSType(t *testing.T) {
	cases := []struct {
		name string
		ref  ir.TypeRef
		want string
	}{
		{"string", ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "string"}, "string"},
		{"bool", ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "bool"}, "boolean"},
		{"int", ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "int"}, "number"},
		{"time.Duration", ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "time.Duration"}, "number"},
		{"json.RawMessage", ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "json.RawMessage"}, "unknown"},
		{"uuid.UUID", ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "uuid.UUID"}, "string"},
		{"named", ir.TypeRef{Kind: ir.KindNamed, Named: "x/api.User"}, "User"},
		{"slice", ir.TypeRef{Kind: ir.KindSlice,
			Element: &ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "string"}}, "string[]"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := tsType(c.ref, nil); got != c.want {
				t.Errorf("tsType = %q, want %q", got, c.want)
			}
		})
	}
}

func TestMutationTVarsAndCall(t *testing.T) {
	mkPath := func(name string) ir.Param {
		return ir.Param{GoName: "ID", WireName: name,
			Type: ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "string"}}
	}
	body := &ir.TypeRef{Kind: ir.KindNamed, Named: "x/api.CreateUserRequest"}

	t.Run("body-only", func(t *testing.T) {
		r := ir.Route{HandlerName: "CreateUser", Tag: "users", Method: "POST", BodyType: body}
		tv, call := mutationTVarsAndCall(r, "create", nil)
		if tv != "t.CreateUserRequest" {
			t.Errorf("TVars = %q", tv)
		}
		if call != "(body) => client.users.create(body)" {
			t.Errorf("call = %q", call)
		}
	})

	t.Run("path-only", func(t *testing.T) {
		r := ir.Route{HandlerName: "DeleteUser", Tag: "users", Method: "DELETE",
			PathParams: []ir.Param{mkPath("id")}}
		tv, call := mutationTVarsAndCall(r, "delete", nil)
		if tv != "{ id: string }" {
			t.Errorf("TVars = %q", tv)
		}
		if call != "(params) => client.users.delete(params)" {
			t.Errorf("call = %q", call)
		}
	})

	t.Run("path+body", func(t *testing.T) {
		r := ir.Route{HandlerName: "UpdateUser", Tag: "users", Method: "PATCH",
			PathParams: []ir.Param{mkPath("id")},
			BodyType:   &ir.TypeRef{Kind: ir.KindNamed, Named: "x/api.UpdateUserRequest"}}
		tv, call := mutationTVarsAndCall(r, "update", nil)
		want := "{ params: { id: string }; body: t.UpdateUserRequest }"
		if tv != want {
			t.Errorf("TVars = %q, want %q", tv, want)
		}
		if call != "(vars) => client.users.update(vars.params, vars.body)" {
			t.Errorf("call = %q", call)
		}
	})

	t.Run("query-on-mutation panics", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Error("expected panic for mutation with query params")
			}
		}()
		r := ir.Route{HandlerName: "Weird", Tag: "x", Method: "POST",
			QueryParams: []ir.Param{{WireName: "q",
				Type: ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "string"}}}}
		mutationTVarsAndCall(r, "weird", nil)
	})
}
