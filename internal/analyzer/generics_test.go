package analyzer

// generics_test.go covers ADR 0033 generic-type recognition: a
// generic declaration emits one TypeDef with TypeParams set, and a
// field of an instantiated generic emits a KindNamed ref with
// TypeArgs populated.

import (
	"strings"
	"testing"

	"github.com/townsendmerino/goduct/internal/ir"
)

const genericsFixture = `package svc

import "context"

type Page[T any] struct {
	Items      []T    ` + "`json:\"items\"`" + `
	NextCursor string ` + "`json:\"nextCursor,omitempty\"`" + `
}

type Result[T any, E any] struct {
	OK    *T ` + "`json:\"ok,omitempty\"`" + `
	Error *E ` + "`json:\"error,omitempty\"`" + `
}

type User struct {
	ID   string ` + "`json:\"id\"`" + `
	Name string ` + "`json:\"name\"`" + `
}

type ErrInfo struct {
	Code string ` + "`json:\"code\"`" + `
}

type ListUsersReq struct {
	Limit int ` + "`query:\"limit\"`" + `
}

// goduct:route GET /users
func ListUsers(ctx context.Context, req ListUsersReq) (*Page[User], error) {
	return nil, nil
}

type GetResultReq struct {
	ID string ` + "`path:\"id\" validate:\"required\"`" + `
}

// goduct:route GET /results/:id
func GetResult(ctx context.Context, req GetResultReq) (*Result[User, ErrInfo], error) {
	return nil, nil
}
`

func TestDiscoverTypes_Generics_SingleAndMultiParam(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"go.mod": "module svc\n\ngo 1.26\n",
		"f.go":   genericsFixture,
	})
	api, err := Analyze([]string{"."}, LoadOptions{Dir: dir})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	// Generic declarations: TypeDef with TypeParams populated, fields
	// referencing KindTypeParam. One TypeDef per generic origin —
	// no per-instantiation duplicate.
	t.Run("Page declaration has TypeParams=[T]", func(t *testing.T) {
		td, ok := api.Types["svc.Page"]
		if !ok {
			t.Fatalf("svc.Page missing from api.Types; keys = %v", typeKeys(api.Types))
		}
		if got := td.TypeParams; len(got) != 1 || got[0] != "T" {
			t.Errorf("Page.TypeParams = %v, want [T]", got)
		}
		// Items []T -> KindSlice{Element: KindTypeParam{TypeParam: "T"}}
		var items *ir.Field
		for i := range td.Fields {
			if td.Fields[i].GoName == "Items" {
				items = &td.Fields[i]
				break
			}
		}
		if items == nil {
			t.Fatal("Page.Items not found")
		}
		if items.Type.Kind != ir.KindSlice || items.Type.Element == nil {
			t.Fatalf("Items.Type = %+v, want KindSlice with Element", items.Type)
		}
		if items.Type.Element.Kind != ir.KindTypeParam {
			t.Errorf("Items.Type.Element.Kind = %v, want KindTypeParam", items.Type.Element.Kind)
		}
		if items.Type.Element.TypeParam != "T" {
			t.Errorf("Items.Type.Element.TypeParam = %q, want T", items.Type.Element.TypeParam)
		}
	})

	t.Run("Result declaration has TypeParams=[T,E]", func(t *testing.T) {
		td, ok := api.Types["svc.Result"]
		if !ok {
			t.Fatalf("svc.Result missing")
		}
		if got := td.TypeParams; len(got) != 2 || got[0] != "T" || got[1] != "E" {
			t.Errorf("Result.TypeParams = %v, want [T E]", got)
		}
	})

	// Instantiations: ListUsers's response type is *Page[User]; goduct
	// records it on ir.Route.ResponseType. The route's ResponseType
	// should be KindNamed{Named:"svc.Page", TypeArgs:[User]}.
	t.Run("Page[User] instantiation has TypeArgs=[User]", func(t *testing.T) {
		var lu *ir.Route
		for i := range api.Routes {
			if api.Routes[i].HandlerName == "ListUsers" {
				lu = &api.Routes[i]
				break
			}
		}
		if lu == nil {
			t.Fatal("ListUsers route missing")
		}
		if lu.ResponseType == nil {
			t.Fatal("ListUsers.ResponseType nil")
		}
		if lu.ResponseType.Kind != ir.KindNamed || lu.ResponseType.Named != "svc.Page" {
			t.Errorf("ResponseType = %+v, want KindNamed svc.Page", lu.ResponseType)
		}
		if len(lu.ResponseType.TypeArgs) != 1 {
			t.Fatalf("ResponseType.TypeArgs len = %d, want 1", len(lu.ResponseType.TypeArgs))
		}
		arg := lu.ResponseType.TypeArgs[0]
		if arg.Kind != ir.KindNamed || arg.Named != "svc.User" {
			t.Errorf("TypeArgs[0] = %+v, want KindNamed svc.User", arg)
		}
	})

	t.Run("Result[User,ErrInfo] instantiation has TypeArgs=[User,ErrInfo]", func(t *testing.T) {
		var gr *ir.Route
		for i := range api.Routes {
			if api.Routes[i].HandlerName == "GetResult" {
				gr = &api.Routes[i]
				break
			}
		}
		if gr == nil {
			t.Fatal("GetResult route missing")
		}
		if gr.ResponseType == nil || len(gr.ResponseType.TypeArgs) != 2 {
			t.Fatalf("ResponseType TypeArgs len = %d, want 2", len(gr.ResponseType.TypeArgs))
		}
		if gr.ResponseType.TypeArgs[0].Named != "svc.User" {
			t.Errorf("TypeArgs[0] = %q, want svc.User", gr.ResponseType.TypeArgs[0].Named)
		}
		if gr.ResponseType.TypeArgs[1].Named != "svc.ErrInfo" {
			t.Errorf("TypeArgs[1] = %q, want svc.ErrInfo", gr.ResponseType.TypeArgs[1].Named)
		}
	})

	// Both User and ErrInfo are reachable transitively via Page[User]
	// and Result[User, ErrInfo] — collectNamedDeps must walk TypeArgs.
	t.Run("type-args reached via generic instantiations", func(t *testing.T) {
		if _, ok := api.Types["svc.User"]; !ok {
			t.Errorf("svc.User not reached via Page[User] TypeArgs")
		}
		if _, ok := api.Types["svc.ErrInfo"]; !ok {
			t.Errorf("svc.ErrInfo not reached via Result[_, ErrInfo] TypeArgs")
		}
	})
}

func TestDiscoverTypes_Generics_ConstraintLoudFail(t *testing.T) {
	// Constraint other than `any` -> C1 loud-fail per ADR 0033 §1.
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"go.mod": "module svc\n\ngo 1.26\n",
		"f.go": `package svc

import "context"

type Numeric interface {
	int | int64 | float64
}

type Box[T Numeric] struct {
	V T ` + "`json:\"v\"`" + `
}

type R struct {
	ID string ` + "`path:\"id\" validate:\"required\"`" + `
}

// goduct:route GET /box/:id
func GetBox(ctx context.Context, req R) (*Box[int], error) { return nil, nil }
`,
	})
	_, err := Analyze([]string{"."}, LoadOptions{Dir: dir})
	if err == nil {
		t.Fatal("expected C1 error for non-any constraint, got nil")
	}
	if !strings.Contains(err.Error(), "C1") {
		t.Errorf("error should be C1: %v", err)
	}
	if !strings.Contains(err.Error(), "other than `any`") {
		t.Errorf("error should name the constraint limit: %v", err)
	}
}

func typeKeys(m map[string]ir.TypeDef) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
