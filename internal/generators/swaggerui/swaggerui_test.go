package swaggerui

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

func TestGenerate_ChiBasicGolden(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root, _ := filepath.Abs(filepath.Join(filepath.Dir(file), "..", "..", ".."))
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
		"examples/chi-basic/testdata/expected/swagger-ui.html"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("swagger-ui.html != golden (got %d bytes, want %d bytes)",
			buf.Len(), len(want))
	}
}

func TestGenerate_TitleFromPackageName(t *testing.T) {
	// Package name flows through to <title>. gen.PackageName derives
	// from any route/type qualified name (last `/`-segment of the
	// import path), so seeding a single route with a body type is
	// enough to drive the title path.
	api := &ir.API{
		Routes: []ir.Route{{
			HandlerName: "X", Method: "POST", Path: "/x",
			BodyType: &ir.TypeRef{Kind: ir.KindNamed, Named: "example.com/myservice/api.X"},
		}},
	}
	var buf bytes.Buffer
	if err := Generate(api, &buf); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.Contains(buf.String(), "<title>api</title>") {
		t.Errorf("expected <title>api</title>, got:\n%s", buf.String())
	}
}

func TestGenerate_EmptyAPIFallsBackToGoduct(t *testing.T) {
	// No routes / no source dirs => no package name. We still want
	// a valid HTML page rather than <title></title>.
	api := &ir.API{}
	var buf bytes.Buffer
	if err := Generate(api, &buf); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.Contains(buf.String(), "<title>goduct</title>") {
		t.Errorf("empty API should fall back to <title>goduct</title>, got:\n%s",
			buf.String())
	}
}

func TestGenerate_NoBundledJS(t *testing.T) {
	// ADR 0035 commits to CDN-loaded Swagger UI. If a future change
	// accidentally inlines the dist, the output blows past a couple
	// kilobytes — sentinel guard.
	api := &ir.API{}
	var buf bytes.Buffer
	_ = Generate(api, &buf)
	if buf.Len() > 4096 {
		t.Errorf("swagger-ui.html grew to %d bytes — did the dist get bundled?", buf.Len())
	}
}
