package analyzer

import (
	"go/types"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// repoRoot returns the module root, derived from this test file's location
// (<root>/internal/analyzer/loader_test.go), so tests work regardless of the
// process working directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root, err := filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	return root
}

// writeFiles scaffolds a throwaway module/package tree under dir.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLoad_HappyPath(t *testing.T) {
	pkgs, err := Load([]string{"./examples/chi-basic/api"}, LoadOptions{Dir: repoRoot(t)})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("got %d packages, want 1", len(pkgs))
	}
	p := pkgs[0]
	if p.Name != "api" {
		t.Errorf("Name = %q, want %q", p.Name, "api")
	}
	if len(p.Errors) != 0 {
		t.Errorf("unexpected package errors: %v", p.Errors)
	}
	if p.Types == nil {
		t.Error("Types is nil")
	}
	if p.TypesInfo == nil {
		t.Error("TypesInfo is nil")
	}
	if len(p.Syntax) == 0 {
		t.Error("Syntax has no *ast.File")
	}
	obj := p.Types.Scope().Lookup("User")
	if obj == nil {
		t.Fatal(`Scope().Lookup("User") = nil`)
	}
	if _, ok := obj.(*types.TypeName); !ok {
		t.Errorf("User resolved to %T, want *types.TypeName", obj)
	}
}

func TestLoad_NoMatches(t *testing.T) {
	_, err := Load([]string{"./does/not/exist"}, LoadOptions{Dir: repoRoot(t)})
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "does/not/exist") {
		t.Errorf("error %q does not mention the pattern", err.Error())
	}
}

func TestLoad_BadGoCode(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"go.mod":    "module brokenfixture\n\ngo 1.26\n",
		"broken.go": "package x\n\nfunc() {\n",
	})
	_, err := Load([]string{"./..."}, LoadOptions{Dir: dir})
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "broken.go") {
		t.Errorf("error %q missing the file path", msg)
	}
	if !regexp.MustCompile(`broken\.go:\d+`).MatchString(msg) {
		t.Errorf("error %q missing a file:line position", msg)
	}
}

func TestLoad_MultiplePackages(t *testing.T) {
	// After ADR 0013, examples/chi-basic has exactly one Go package, so the
	// prompt's "./examples/..." multi-package check is impossible there. A
	// self-contained two-package temp module exercises the same capability.
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"go.mod":     "module multi\n\ngo 1.26\n",
		"alpha/a.go": "package alpha\n\nconst A = 1\n",
		"beta/b.go":  "package beta\n\nconst B = 2\n",
	})
	pkgs, err := Load([]string{"./..."}, LoadOptions{Dir: dir})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(pkgs) != 2 {
		t.Fatalf("got %d packages, want 2", len(pkgs))
	}
	names := map[string]bool{}
	for _, p := range pkgs {
		names[p.Name] = true
	}
	if !names["alpha"] || !names["beta"] {
		t.Errorf("loaded package names = %v, want alpha and beta", names)
	}
}

func TestLoad_TestsFlag(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"go.mod":          "module testsflag\n\ngo 1.26\n",
		"foo/foo.go":      "package foo\n\nfunc Add(a, b int) int { return a + b }\n",
		"foo/foo_test.go": "package foo\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Fail()\n\t}\n}\n",
	})
	base, err := Load([]string{"./foo"}, LoadOptions{Dir: dir})
	if err != nil {
		t.Fatalf("Load(Tests:false): %v", err)
	}
	withTests, err := Load([]string{"./foo"}, LoadOptions{Dir: dir, Tests: true})
	if err != nil {
		t.Fatalf("Load(Tests:true): %v", err)
	}
	if len(withTests) <= len(base) {
		t.Errorf("Tests:true loaded %d packages, Tests:false loaded %d; want more with Tests:true",
			len(withTests), len(base))
	}
}
