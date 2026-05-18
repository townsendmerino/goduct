package analyzer

import (
	"go/types"
	"reflect"
	"strings"
	"testing"

	"github.com/townsendmerino/goduct/internal/ir"
)

// sfParse loads a one-file temp module, finds typeName's struct, and runs
// ParseStructField on the named field with the given context.
func sfParse(t *testing.T, body, typeName, field string, ctx StructContext) (*ParsedField, error) {
	t.Helper()
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"go.mod": "module sf\n\ngo 1.26\n",
		"f.go":   "package sf\n" + body + "\n",
	})
	pkgs, err := Load([]string{"."}, LoadOptions{Dir: dir})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	pkg := pkgs[0]
	st := pkg.Types.Scope().Lookup(typeName).Type().Underlying().(*types.Struct)
	for i := 0; i < st.NumFields(); i++ {
		if st.Field(i).Name() == field {
			return ParseStructField(pkg, st.Field(i), reflect.StructTag(st.Tag(i)), ctx)
		}
	}
	t.Fatalf("field %s not found", field)
	return nil, nil
}

func reqCtx() StructContext  { return StructContext{IsRequestType: true, QualifiedName: "sf.R"} }
func respCtx() StructContext { return StructContext{IsRequestType: false, QualifiedName: "sf.Resp"} }

func TestParseStructField_Classification(t *testing.T) {
	body := `type R struct {
	ID    string  ` + "`path:\"id\"`" + `
	Limit int     ` + "`query:\"limit\"`" + `
	Trace string  ` + "`header:\"X-Trace\"`" + `
	Email string  ` + "`json:\"email\"`" + `
	priv  string
	Skip  string  ` + "`json:\"-\"`" + `
	Both  string  ` + "`json:\"-\" path:\"both\"`" + `
}`
	tests := []struct {
		field    string
		wantNil  bool
		source   ir.FieldSource
		wire     string
		jsonName string
	}{
		{"ID", false, ir.FieldSourcePath, "id", ""},
		{"Limit", false, ir.FieldSourceQuery, "limit", ""},
		{"Trace", false, ir.FieldSourceHeader, "X-Trace", ""},
		{"Email", false, ir.FieldSourceJSON, "", "email"},
		{"priv", true, 0, "", ""},                       // unexported → D1 skip
		{"Skip", true, 0, "", ""},                       // json:"-" only → D2 skip
		{"Both", false, ir.FieldSourcePath, "both", ""}, // E1: json:"-" + path → path
	}
	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			pf, err := sfParse(t, body, "R", tt.field, reqCtx())
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if tt.wantNil {
				if pf != nil {
					t.Fatalf("want nil (skipped), got %+v", pf)
				}
				return
			}
			if pf == nil {
				t.Fatal("got nil, want a field")
			}
			if pf.Field.Source != tt.source {
				t.Errorf("Source = %v, want %v", pf.Field.Source, tt.source)
			}
			if pf.WireName != tt.wire {
				t.Errorf("WireName = %q, want %q", pf.WireName, tt.wire)
			}
			if pf.Field.JSONName != tt.jsonName {
				t.Errorf("JSONName = %q, want %q", pf.Field.JSONName, tt.jsonName)
			}
		})
	}
}

func TestParseStructField_Optionality(t *testing.T) {
	body := `type R struct {
	ID    string  ` + "`path:\"id\"`" + `
	Q1    string  ` + "`query:\"q1\"`" + `
	Q2    string  ` + "`query:\"q2\" validate:\"required\"`" + `
	JPlain string ` + "`json:\"jp\"`" + `
	JPtr  *string ` + "`json:\"jptr\"`" + `
	JOmit string  ` + "`json:\"jo,omitempty\"`" + `
}`
	cases := map[string]bool{ // field → want Optional
		"ID":     false, // path always required
		"Q1":     true,  // query, no required
		"Q2":     false, // query with validate:required
		"JPlain": false, // json, no pointer/omitempty
		"JPtr":   true,  // json pointer (ADR 0020)
		"JOmit":  true,  // json omitempty (ADR 0020)
	}
	for f, want := range cases {
		t.Run(f, func(t *testing.T) {
			pf, err := sfParse(t, body, "R", f, reqCtx())
			if err != nil || pf == nil {
				t.Fatalf("parse %s: pf=%v err=%v", f, pf, err)
			}
			if pf.Field.Optional != want {
				t.Errorf("%s Optional = %v, want %v", f, pf.Field.Optional, want)
			}
		})
	}
}

func TestParseStructField_Errors(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		field   string
		ctx     StructContext
		wantCat string
		wantSub string
	}{
		{
			"conflict E3", `type R struct{ ID string ` + "`path:\"id\" query:\"id\"`" + ` }`,
			"ID", reqCtx(), "E3", "conflicting tags",
		},
		{
			"response path tag E2", `type Resp struct{ ID string ` + "`path:\"id\"`" + ` }`,
			"ID", respCtx(), "E2", "not allowed",
		},
		{
			"embedded B5", `type Base struct{}
type R struct{ Base }`,
			"Base", reqCtx(), "B5", "embedded fields in request structs are not yet supported",
		},
		{
			"pointer path PATH1", `type R struct{ ID *string ` + "`path:\"id\"`" + ` }`,
			"ID", reqCtx(), "PATH1", "cannot be a pointer",
		},
		{
			"unsupported query type PATH2", `type Bad struct{ X int }
type R struct{ B Bad ` + "`query:\"b\"`" + ` }`,
			"B", reqCtx(), "PATH2", "unsupported type",
		},
		{
			"json interface B2", `type R struct{ Any any ` + "`json:\"any\"`" + ` }`,
			"Any", reqCtx(), "B2", "interface types are not supported",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := sfParse(t, tt.body, typeNameOf(tt.body), tt.field, tt.ctx)
			if err == nil {
				t.Fatalf("expected error (cat %s), got nil", tt.wantCat)
			}
			msg := err.Error()
			// ADR 0019 Format B: 3 lines, "goduct: file:line:col: CAT: ...",
			// "in <qualified>.<field> (<gotype>)", "hint: ..."
			if !strings.HasPrefix(msg, "goduct: ") {
				t.Errorf("missing goduct: prefix: %q", msg)
			}
			if !strings.Contains(msg, " "+tt.wantCat+": ") {
				t.Errorf("missing category %q in %q", tt.wantCat, msg)
			}
			if !strings.Contains(msg, tt.wantSub) {
				t.Errorf("missing substring %q in %q", tt.wantSub, msg)
			}
			if !strings.Contains(msg, "\n        in "+tt.ctx.QualifiedName+".") {
				t.Errorf("missing Format B 'in <qualified>.<field>' line: %q", msg)
			}
			if !strings.Contains(msg, "\n        hint: ") {
				t.Errorf("missing Format B hint line: %q", msg)
			}
		})
	}
}

// typeNameOf returns the struct type the error tests probe ("R" unless the
// body only declares "Resp").
func typeNameOf(body string) string {
	if strings.Contains(body, "type Resp struct") {
		return "Resp"
	}
	return "R"
}
