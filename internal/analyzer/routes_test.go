package analyzer

import (
	"strings"
	"testing"

	"github.com/townsendmerino/goduct/internal/ir"
)

const chiBasicPkg = "github.com/townsendmerino/goduct/examples/chi-basic/api"

// Expected route shape for examples/chi-basic/api, in source order.
//
// NOTE: the upstream prompt's table said "ListUsers: limit required" — but
// the fixture's Limit field is `query:"limit" validate:"min=1,max=100"`
// with NO `required`, and ADR 0014 / the optionality rule make a
// query param optional unless it has validate:"required". So limit is
// Optional==true here. This is asserted faithfully (the prompt table was
// inconsistent with the fixture + rule); flagged in the summary.
var chiBasicWant = []struct {
	name     string
	method   string
	path     string
	tag      string
	status   int
	nPath    int
	nQuery   int
	nHeader  int
	bodyType string // qualified name or "" for nil
	respType string // qualified name or "" for nil
}{
	{"GetUser", "GET", "/users/:id", "users", 200, 1, 0, 0, "", chiBasicPkg + ".User"},
	{"ListUsers", "GET", "/users", "users", 200, 0, 2, 0, "", chiBasicPkg + ".ListUsersResponse"},
	{"CreateUser", "POST", "/users", "users", 201, 0, 0, 0, chiBasicPkg + ".CreateUserRequest", chiBasicPkg + ".User"},
	{"UpdateUser", "PATCH", "/users/:id", "users", 200, 1, 0, 0, chiBasicPkg + ".UpdateUserRequest", chiBasicPkg + ".User"},
	{"DeleteUser", "DELETE", "/users/:id", "users", 204, 1, 0, 0, "", ""},
}

func TestDiscoverRoutes_ChiBasic(t *testing.T) {
	pkgs, err := Load([]string{"./examples/chi-basic/api"}, LoadOptions{Dir: repoRoot(t)})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	routes, err := DiscoverRoutes(pkgs[0])
	if err != nil {
		t.Fatalf("DiscoverRoutes: %v", err)
	}
	if len(routes) != len(chiBasicWant) {
		t.Fatalf("got %d routes, want %d", len(routes), len(chiBasicWant))
	}
	for i, w := range chiBasicWant {
		r := routes[i]
		if r.HandlerName != w.name {
			t.Errorf("route %d: HandlerName = %q, want %q (source order)", i, r.HandlerName, w.name)
		}
		if r.Method != w.method || r.Path != w.path || r.Tag != w.tag || r.SuccessStatus != w.status {
			t.Errorf("%s: got %s %s tag=%q status=%d, want %s %s tag=%q status=%d",
				w.name, r.Method, r.Path, r.Tag, r.SuccessStatus, w.method, w.path, w.tag, w.status)
		}
		if len(r.PathParams) != w.nPath || len(r.QueryParams) != w.nQuery || len(r.HeaderParams) != w.nHeader {
			t.Errorf("%s: param counts path/query/header = %d/%d/%d, want %d/%d/%d",
				w.name, len(r.PathParams), len(r.QueryParams), len(r.HeaderParams),
				w.nPath, w.nQuery, w.nHeader)
		}
		if got := namedOrEmpty(r.BodyType); got != w.bodyType {
			t.Errorf("%s: BodyType = %q, want %q", w.name, got, w.bodyType)
		}
		if got := namedOrEmpty(r.ResponseType); got != w.respType {
			t.Errorf("%s: ResponseType = %q, want %q", w.name, got, w.respType)
		}
		if r.Mode != ir.ModeIdiomatic {
			t.Errorf("%s: Mode = %v, want ModeIdiomatic", w.name, r.Mode)
		}
		if r.Pos == "" {
			t.Errorf("%s: Pos is empty", w.name)
		}
	}

	byName := map[string]ir.Route{}
	for _, r := range routes {
		byName[r.HandlerName] = r
	}

	// GetUser: one required path param "id" (string).
	gp := byName["GetUser"].PathParams[0]
	if gp.GoName != "ID" || gp.WireName != "id" || gp.Optional ||
		gp.Type.Kind != ir.KindBuiltin || gp.Type.Builtin != "string" {
		t.Errorf("GetUser path param = %+v, want {ID,id,!optional,string}", gp)
	}
	if !hasRule(gp.Validation, "required") {
		t.Errorf("GetUser path param validation = %+v, want it to contain `required`", gp.Validation)
	}

	// ListUsers: limit (int, optional, min/max rules) + cursor (string, optional).
	lu := byName["ListUsers"]
	limit, cursor := lu.QueryParams[0], lu.QueryParams[1]
	if limit.GoName != "Limit" || limit.WireName != "limit" ||
		limit.Type.Builtin != "int" || !limit.Optional {
		t.Errorf("ListUsers limit = %+v, want {Limit,limit,int,optional}", limit)
	}
	if !hasRule(limit.Validation, "min") || !hasRule(limit.Validation, "max") {
		t.Errorf("ListUsers limit validation = %+v, want min and max", limit.Validation)
	}
	if cursor.GoName != "Cursor" || cursor.WireName != "cursor" ||
		cursor.Type.Builtin != "string" || !cursor.Optional {
		t.Errorf("ListUsers cursor = %+v, want {Cursor,cursor,string,optional}", cursor)
	}
}

func namedOrEmpty(r *ir.TypeRef) string {
	if r == nil {
		return ""
	}
	return r.Named
}

// discoverTemp scaffolds a temp module, loads pattern via the real loader,
// and runs DiscoverRoutes on the first returned package. loadErr is the
// loader error (compile failures); discErr is the discovery error.
func discoverTemp(t *testing.T, files map[string]string, pattern string) (loadErr, discErr error) {
	t.Helper()
	dir := t.TempDir()
	writeFiles(t, dir, files)
	pkgs, err := Load([]string{pattern}, LoadOptions{Dir: dir})
	if err != nil {
		return err, nil
	}
	_, derr := DiscoverRoutes(pkgs[0])
	return nil, derr
}

func TestDiscoverRoutes_Errors(t *testing.T) {
	const gomod = "module x\n\ngo 1.26\n"

	tests := []struct {
		name    string
		files   map[string]string
		pattern string
		wantSub string
	}{
		{
			name: "method receiver",
			files: map[string]string{"go.mod": gomod, "h.go": "package x\nimport \"context\"\n" +
				"type S struct{}\ntype R struct{}\ntype U struct{}\n" +
				"// goduct:route GET /x\nfunc (S) H(ctx context.Context, r R) (*U, error) { return nil, nil }\n"},
			pattern: ".", wantSub: "methods are not supported",
		},
		{
			name: "unexported handler",
			files: map[string]string{"go.mod": gomod, "h.go": "package x\nimport \"context\"\n" +
				"type R struct{}\ntype U struct{}\n" +
				"// goduct:route GET /x\nfunc h(ctx context.Context, r R) (*U, error) { return nil, nil }\n"},
			pattern: ".", wantSub: "must be exported",
		},
		{
			name: "wrong first param",
			files: map[string]string{"go.mod": gomod, "h.go": "package x\n" +
				"type R struct{}\ntype U struct{}\n" +
				"// goduct:route GET /x\nfunc H(n int, r R) (*U, error) { return nil, nil }\n"},
			pattern: ".", wantSub: "first parameter must be context.Context",
		},
		{
			name: "one return not error",
			files: map[string]string{"go.mod": gomod, "h.go": "package x\nimport \"context\"\n" +
				"type R struct{}\ntype U struct{}\n" +
				"// goduct:route GET /x\nfunc H(ctx context.Context, r R) *U { return nil }\n"},
			pattern: ".", wantSub: "single return value must be error",
		},
		{
			name: "three returns",
			files: map[string]string{"go.mod": gomod, "h.go": "package x\nimport \"context\"\n" +
				"type R struct{}\ntype U struct{}\n" +
				"// goduct:route GET /x\nfunc H(ctx context.Context, r R) (*U, int, error) { return nil, 0, nil }\n"},
			pattern: ".", wantSub: "got 3 values",
		},
		{
			name: "non-pointer first return",
			files: map[string]string{"go.mod": gomod, "h.go": "package x\nimport \"context\"\n" +
				"type R struct{}\ntype U struct{}\n" +
				"// goduct:route GET /x\nfunc H(ctx context.Context, r R) (U, error) { return U{}, nil }\n"},
			pattern: ".", wantSub: "must be a pointer to a named struct",
		},
		{
			name: "cross-package request type",
			files: map[string]string{
				"go.mod":         gomod,
				"other/other.go": "package other\ntype R struct{}\n",
				"app/app.go": "package app\nimport (\n\t\"context\"\n\t\"x/other\"\n)\n" +
					"type U struct{}\n" +
					"// goduct:route GET /x\nfunc H(ctx context.Context, r other.R) (*U, error) { return nil, nil }\n",
			},
			pattern: "./app", wantSub: "cross-package request/response types are not yet supported",
		},
		{
			name: "embedded field in request struct",
			files: map[string]string{"go.mod": gomod, "h.go": "package x\nimport \"context\"\n" +
				"type Base struct{}\ntype R struct{ Base\n ID string `path:\"id\"` }\ntype U struct{}\n" +
				"// goduct:route GET /x/:id\nfunc H(ctx context.Context, r R) (*U, error) { return nil, nil }\n"},
			pattern: ".", wantSub: "embedded fields in request structs are not yet supported",
		},
		{
			name: "conflicting tags",
			files: map[string]string{"go.mod": gomod, "h.go": "package x\nimport \"context\"\n" +
				"type R struct{ ID string `path:\"id\" query:\"id\"` }\ntype U struct{}\n" +
				"// goduct:route GET /x/:id\nfunc H(ctx context.Context, r R) (*U, error) { return nil, nil }\n"},
			pattern: ".", wantSub: "conflicting tags",
		},
		{
			name: "pointer path param",
			files: map[string]string{"go.mod": gomod, "h.go": "package x\nimport \"context\"\n" +
				"type R struct{ ID *string `path:\"id\"` }\ntype U struct{}\n" +
				"// goduct:route GET /x/:id\nfunc H(ctx context.Context, r R) (*U, error) { return nil, nil }\n"},
			pattern: ".", wantSub: "cannot be a pointer",
		},
		{
			name: "unsupported query param type",
			files: map[string]string{"go.mod": gomod, "h.go": "package x\nimport \"context\"\n" +
				"type Bad struct{ N int }\ntype R struct{ Foo Bad `query:\"foo\"` }\ntype U struct{}\n" +
				"// goduct:route GET /x\nfunc H(ctx context.Context, r R) (*U, error) { return nil, nil }\n"},
			pattern: ".", wantSub: "unsupported type",
		},
		{
			name: "GET with json field",
			files: map[string]string{"go.mod": gomod, "h.go": "package x\nimport \"context\"\n" +
				"type R struct{ X string `json:\"x\"` }\ntype U struct{}\n" +
				"// goduct:route GET /x\nfunc H(ctx context.Context, r R) (*U, error) { return nil, nil }\n"},
			pattern: ".", wantSub: "does not support a request body",
		},
		{
			name: "path segment with no field",
			files: map[string]string{"go.mod": gomod, "h.go": "package x\nimport \"context\"\n" +
				"type R struct{}\ntype U struct{}\n" +
				"// goduct:route GET /x/:id\nfunc H(ctx context.Context, r R) (*U, error) { return nil, nil }\n"},
			pattern: ".", wantSub: "no matching path-tagged field",
		},
		{
			name: "path field with no segment",
			files: map[string]string{"go.mod": gomod, "h.go": "package x\nimport \"context\"\n" +
				"type R struct{ ID string `path:\"id\"` }\ntype U struct{}\n" +
				"// goduct:route GET /x\nfunc H(ctx context.Context, r R) (*U, error) { return nil, nil }\n"},
			pattern: ".", wantSub: "no matching segment in route path",
		},
		{
			name: "error-only handler with status 200",
			files: map[string]string{"go.mod": gomod, "h.go": "package x\nimport \"context\"\n" +
				"type R struct{}\n" +
				"// goduct:route GET /x\n// goduct:status 200\nfunc H(ctx context.Context, r R) error { return nil }\n"},
			pattern: ".", wantSub: "only 204 is valid for empty responses",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loadErr, discErr := discoverTemp(t, tt.files, tt.pattern)
			if loadErr != nil {
				t.Fatalf("fixture failed to load (should compile cleanly): %v", loadErr)
			}
			if discErr == nil {
				t.Fatalf("expected discovery error containing %q, got nil", tt.wantSub)
			}
			if !strings.Contains(discErr.Error(), tt.wantSub) {
				t.Errorf("error = %q, want substring %q", discErr.Error(), tt.wantSub)
			}
		})
	}
}

// TestDiscoverRoutes_Raw exercises ADR 0031 raw-mode discovery on
// synthetic packages — chi-basic stays idiomatic.
func TestDiscoverRoutes_Raw(t *testing.T) {
	t.Run("body-route raw handler", func(t *testing.T) {
		dir := t.TempDir()
		writeFiles(t, dir, map[string]string{
			"go.mod": "module raw\n\ngo 1.26\n",
			"raw.go": `package raw

import "net/http"

type CreateThingRequest struct {
	Name string ` + "`json:\"name\" validate:\"required\"`" + `
}
type Thing struct {
	ID   string ` + "`json:\"id\"`" + `
	Name string ` + "`json:\"name\"`" + `
}

// goduct:route    POST /things
// goduct:request  CreateThingRequest
// goduct:response Thing
// goduct:status   201
func CreateThing(w http.ResponseWriter, r *http.Request) {
}
`,
		})
		pkgs, err := Load([]string{"."}, LoadOptions{Dir: dir})
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		routes, err := DiscoverRoutes(pkgs[0])
		if err != nil {
			t.Fatalf("DiscoverRoutes: %v", err)
		}
		if len(routes) != 1 {
			t.Fatalf("got %d routes, want 1", len(routes))
		}
		rt := routes[0]
		if rt.Mode != ir.ModeRaw {
			t.Errorf("Mode = %v, want ModeRaw", rt.Mode)
		}
		if rt.RequestType == nil || rt.RequestType.Named != "raw.CreateThingRequest" {
			t.Errorf("RequestType = %+v, want raw.CreateThingRequest", rt.RequestType)
		}
		if rt.BodyType == nil || rt.BodyType.Named != "raw.CreateThingRequest" {
			t.Errorf("BodyType = %+v, want raw.CreateThingRequest (POST body)", rt.BodyType)
		}
		if rt.ResponseType == nil || rt.ResponseType.Named != "raw.Thing" {
			t.Errorf("ResponseType = %+v, want raw.Thing", rt.ResponseType)
		}
		if rt.SuccessStatus != 201 {
			t.Errorf("Status = %d, want 201", rt.SuccessStatus)
		}
	})

	errCases := []struct {
		name, body, wantSub string
	}{
		{
			"missing goduct:request",
			`package raw
import "net/http"
type Thing struct { ID string ` + "`json:\"id\"`" + ` }
// goduct:route GET /things
func GetThings(w http.ResponseWriter, r *http.Request) {}
`,
			"raw http.HandlerFunc mode requires `goduct:request <Type>`",
		},
		{
			"goduct:request type not found",
			`package raw
import "net/http"
// goduct:route   POST /things
// goduct:request DoesNotExist
func CreateThing(w http.ResponseWriter, r *http.Request) {}
`,
			"goduct:request type DoesNotExist not found",
		},
		{
			"goduct:request on idiomatic handler is rejected",
			`package raw
import "context"
type R struct{}
type U struct{ ID string ` + "`json:\"id\"`" + ` }
// goduct:route   POST /x
// goduct:request R
func Idio(ctx context.Context, r R) (*U, error) { return nil, nil }
`,
			"directives are not allowed on idiomatic handlers",
		},
	}
	for _, tc := range errCases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFiles(t, dir, map[string]string{
				"go.mod": "module raw\n\ngo 1.26\n",
				"raw.go": tc.body,
			})
			pkgs, err := Load([]string{"."}, LoadOptions{Dir: dir})
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			_, err = DiscoverRoutes(pkgs[0])
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error = %q, want substring %q", err.Error(), tc.wantSub)
			}
		})
	}
}
