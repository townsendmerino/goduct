package analyzer

import (
	"strings"
	"testing"
)

func TestAnalyze_ChiBasic(t *testing.T) {
	api, err := Analyze([]string{"./examples/chi-basic/api"}, LoadOptions{Dir: repoRoot(t)})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(api.Routes) != 5 {
		t.Errorf("got %d routes, want 5", len(api.Routes))
	}
	if len(api.Types) != 10 {
		t.Errorf("got %d types, want 10: %v", len(api.Types), keysOf(api.Types))
	}
	// Integration spot-checks only — the route/type contents are already
	// exhaustively tested in routes_test.go / types_test.go.
	var seenGetUser bool
	for _, r := range api.Routes {
		if r.HandlerName == "GetUser" {
			seenGetUser = true
		}
	}
	if !seenGetUser {
		t.Error("expected a GetUser route")
	}
	if u := api.Types[chiBasicPkg+".User"]; len(u.Fields) != 5 {
		t.Errorf("api.User field count = %d, want 5", len(u.Fields))
	}
}

func TestAnalyze_NoMatches(t *testing.T) {
	api, err := Analyze([]string{"./does/not/exist"}, LoadOptions{Dir: repoRoot(t)})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if api != nil {
		t.Errorf("expected nil *ir.API on load failure, got %+v", api)
	}
}

// TestAnalyze_NoShortCircuit proves a routes-phase error in the package
// does not stop type discovery: handler A has a broken annotation
// (routes error), handler B is valid but its response references an
// unrepresentable type (types error). Both must appear in the joined
// output. The fixture is valid Go so Load succeeds.
func TestAnalyze_NoShortCircuit(t *testing.T) {
	dir := t.TempDir()
	src := `package m

import "context"

type ReqA struct{}
type RespA struct{}

// goduct:route FETCH /a
func A(ctx context.Context, r ReqA) (*RespA, error) { return nil, nil }

type ReqB struct{}
type RespB struct {
	C chan int ` + "`json:\"c\"`" + `
}

// goduct:route GET /b
func B(ctx context.Context, r ReqB) (*RespB, error) { return nil, nil }
`
	writeFiles(t, dir, map[string]string{"go.mod": "module m\n\ngo 1.26\n", "f.go": src})
	_, err := Analyze([]string{"."}, LoadOptions{Dir: dir})
	if err == nil {
		t.Fatal("expected joined error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "invalid HTTP method") {
		t.Errorf("missing routes-phase error (invalid HTTP method) in: %s", msg)
	}
	if !strings.Contains(msg, "A1") || !strings.Contains(msg, "channels cannot be serialized") {
		t.Errorf("missing types-phase error (A1 channel) in: %s", msg)
	}
}

func TestAnalyze_EmptyPackage(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"go.mod": "module m\n\ngo 1.26\n",
		"f.go":   "package m\n\ntype Unused struct{ X int }\n",
	})
	api, err := Analyze([]string{"."}, LoadOptions{Dir: dir})
	if err != nil {
		t.Fatalf("expected no error for a handler-less package, got: %v", err)
	}
	if api == nil {
		t.Fatal("expected non-nil *ir.API")
	}
	if len(api.Routes) != 0 || len(api.Types) != 0 {
		t.Errorf("expected empty Routes/Types, got %d/%d", len(api.Routes), len(api.Types))
	}
}
