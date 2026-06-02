package postman

import (
	"bytes"
	"encoding/json"
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

func TestGenerate_ChiBasicGolden(t *testing.T) {
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
		"examples/chi-basic/testdata/expected/postman_collection.json"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("postman_collection.json != golden (got %d bytes, want %d bytes)",
			buf.Len(), len(want))
	}
}

// TestGenerate_ValidJSON: the output round-trips through encoding/json.
// If a future change emits malformed JSON, this fires before the byte-
// diff turns into a noisy "huge diff" failure.
func TestGenerate_ValidJSON(t *testing.T) {
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
	var parsed map[string]any
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	info, _ := parsed["info"].(map[string]any)
	if info["schema"] != schemaURL {
		t.Errorf("info.schema = %v, want %s", info["schema"], schemaURL)
	}
}

// TestDeterministicID: same input -> same output across runs.
func TestDeterministicID(t *testing.T) {
	a := deterministicID("api")
	b := deterministicID("api")
	if a != b {
		t.Errorf("non-deterministic: %s vs %s", a, b)
	}
	// Shape: 8-4-4-4-12 hex, 36 chars total.
	if len(a) != 36 {
		t.Errorf("ID length = %d, want 36", len(a))
	}
	for i, c := range a {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				t.Errorf("expected '-' at pos %d, got %q", i, c)
			}
			continue
		}
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			t.Errorf("non-hex char %q at pos %d in %s", c, i, a)
		}
	}
	// Different input -> different output.
	if deterministicID("api") == deterministicID("other") {
		t.Error("two distinct names produced the same ID")
	}
}

func TestBuiltinExample(t *testing.T) {
	api := &ir.API{}
	cases := []struct {
		in   string
		want any
	}{
		{"string", ""},
		{"bool", false},
		{"int", 0},
		{"int64", 0},
		{"float64", 0},
		{"time.Time", ""},
		{"uuid.UUID", ""},
		{"[]byte", ""},
		{"json.RawMessage", map[string]any{}},
	}
	for _, c := range cases {
		got := builtinExample(api, c.in)
		gotB, _ := json.Marshal(got)
		wantB, _ := json.Marshal(c.want)
		if !bytes.Equal(gotB, wantB) {
			t.Errorf("builtinExample(%q) = %s, want %s", c.in, gotB, wantB)
		}
	}
}

func TestCustomAdapterExample(t *testing.T) {
	api := &ir.API{
		CustomAdapters: map[string]string{
			"github.com/shopspring/decimal.Decimal": "string",
			"foo.Pi":                                "number",
			"foo.OK":                                "boolean",
			"foo.Misc":                              "unknown",
		},
	}
	if v := builtinExample(api, "github.com/shopspring/decimal.Decimal"); v != "" {
		t.Errorf("decimal adapter -> %v, want \"\"", v)
	}
	if v := builtinExample(api, "foo.Pi"); v != 0 {
		t.Errorf("Pi adapter -> %v, want 0", v)
	}
	if v := builtinExample(api, "foo.OK"); v != false {
		t.Errorf("OK adapter -> %v, want false", v)
	}
}

// TestExampleForGenericInstantiation: a synthetic Page[User] body
// produces an example with the substituted User fields, not "T".
func TestExampleForGenericInstantiation(t *testing.T) {
	api := &ir.API{
		Types: map[string]ir.TypeDef{
			"x/api.Page": {
				Name: "Page", Kind: ir.TypeStruct,
				TypeParams: []string{"T"},
				Fields: []ir.Field{
					{GoName: "Items", JSONName: "items", Source: ir.FieldSourceJSON,
						Type: ir.TypeRef{Kind: ir.KindSlice,
							Element: &ir.TypeRef{Kind: ir.KindTypeParam, TypeParam: "T"}}},
					{GoName: "NextCursor", JSONName: "nextCursor", Source: ir.FieldSourceJSON, Optional: true,
						Type: ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "string"}},
				},
			},
			"x/api.User": {
				Name: "User", Kind: ir.TypeStruct,
				Fields: []ir.Field{
					{GoName: "ID", JSONName: "id", Source: ir.FieldSourceJSON,
						Type: ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "string"}},
				},
			},
		},
	}
	ref := ir.TypeRef{Kind: ir.KindNamed, Named: "x/api.Page",
		TypeArgs: []*ir.TypeRef{{Kind: ir.KindNamed, Named: "x/api.User"}}}
	got := newExampleBuilder(api).exampleFor(ref)
	b, _ := json.Marshal(got)
	// items is a slice — Postman shows `[]` empty array; nextCursor is "".
	// Because items is a slice, we don't walk into User's fields here —
	// the slice exampleFor returns []any{} directly.
	if !strings.Contains(string(b), `"items":[]`) {
		t.Errorf("expected items:[] in Page[User] example: %s", b)
	}
	if !strings.Contains(string(b), `"nextCursor":""`) {
		t.Errorf("expected nextCursor:\"\" in Page[User] example: %s", b)
	}
}

func TestExampleForEnum(t *testing.T) {
	api := &ir.API{
		Types: map[string]ir.TypeDef{
			"x/api.Status": {
				Name: "Status", Kind: ir.TypeEnum, Underlying: "string",
				EnumValues: []ir.EnumValue{{Value: "active"}, {Value: "invited"}},
			},
		},
	}
	got := newExampleBuilder(api).exampleFor(
		ir.TypeRef{Kind: ir.KindNamed, Named: "x/api.Status"})
	if got != "active" {
		t.Errorf("enum example = %v, want \"active\" (first value)", got)
	}
}
