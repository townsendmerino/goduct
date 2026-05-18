package analyzer

import (
	"go/types"
	"testing"

	"github.com/townsendmerino/goduct/internal/ir"
)

// ftStruct loads a one-file temp module and returns the named struct's
// *types.Struct. Self-contained (no coupling to other _test files' helpers
// beyond the generic writeFiles/Load scaffolding).
func ftStruct(t *testing.T, body, typeName string) *types.Struct {
	t.Helper()
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"go.mod": "module ft\n\ngo 1.26\n",
		"f.go":   "package ft\n" + body + "\n",
	})
	pkgs, err := Load([]string{"."}, LoadOptions{Dir: dir})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	obj := pkgs[0].Types.Scope().Lookup(typeName)
	if obj == nil {
		t.Fatalf("type %s not found", typeName)
	}
	st, ok := obj.Type().Underlying().(*types.Struct)
	if !ok {
		t.Fatalf("%s is not a struct", typeName)
	}
	return st
}

func ftField(t *testing.T, st *types.Struct, name string) types.Type {
	t.Helper()
	for i := 0; i < st.NumFields(); i++ {
		if st.Field(i).Name() == name {
			return st.Field(i).Type()
		}
	}
	t.Fatalf("field %s not found", name)
	return nil
}

func TestFieldTypeRef(t *testing.T) {
	st := ftStruct(t, `
import (
	"time"
	"encoding/json"
)
type Inner struct{ X int }
type BS []byte
type S struct {
	Str   string
	PStr  *string
	Strs  []string
	M     map[string]int
	When  time.Time
	PWhen *time.Time
	Bytes []byte
	Dur   time.Duration
	Raw   json.RawMessage
	Nest  Inner
	Alias BS
}`, "S")

	type want struct {
		kind    ir.TypeKind
		builtin string
		named   string // suffix match
		ptr     bool
		errCat  string
	}
	cases := map[string]want{
		"Str":   {kind: ir.KindBuiltin, builtin: "string"},
		"PStr":  {kind: ir.KindBuiltin, builtin: "string", ptr: true},
		"Strs":  {kind: ir.KindSlice},
		"M":     {kind: ir.KindMap},
		"When":  {kind: ir.KindBuiltin, builtin: "time.Time"},
		"PWhen": {kind: ir.KindBuiltin, builtin: "time.Time", ptr: true},
		"Bytes": {kind: ir.KindBuiltin, builtin: "[]byte"},
		"Dur":   {kind: ir.KindBuiltin, builtin: "time.Duration"},
		"Raw":   {kind: ir.KindBuiltin, builtin: "json.RawMessage"},
		"Nest":  {kind: ir.KindNamed, named: ".Inner"},
		"Alias": {kind: ir.KindNamed, named: ".BS"}, // named []byte alias is NOT []byte
	}
	for name, w := range cases {
		t.Run(name, func(t *testing.T) {
			ref, isPtr, te := fieldTypeRef(ftField(t, st, name))
			if te != nil {
				t.Fatalf("unexpected error: %v", te)
			}
			if ref.Kind != w.kind {
				t.Errorf("Kind = %v, want %v", ref.Kind, w.kind)
			}
			if w.builtin != "" && ref.Builtin != w.builtin {
				t.Errorf("Builtin = %q, want %q", ref.Builtin, w.builtin)
			}
			if w.named != "" && (len(ref.Named) < len(w.named) || ref.Named[len(ref.Named)-len(w.named):] != w.named) {
				t.Errorf("Named = %q, want suffix %q", ref.Named, w.named)
			}
			if isPtr != w.ptr {
				t.Errorf("isPtr = %v, want %v", isPtr, w.ptr)
			}
		})
	}
}

func TestFieldTypeRef_Errors(t *testing.T) {
	st := ftStruct(t, `
type Marsh struct{}
func (Marsh) MarshalJSON() ([]byte, error) { return nil, nil }
type Box[T any] struct{ V T }
type S struct {
	Iface any
	Fn    func()
	Ch    chan int
	Cplx  complex128
	BadM  map[int]string
	Cust  Marsh
	Gen   Box[int]
}`, "S")
	cases := map[string]string{
		"Iface": "B2",
		"Fn":    "A2",
		"Ch":    "A1",
		"Cplx":  "A3",
		"BadM":  "B1",
		"Cust":  "C3",
		"Gen":   "C1",
	}
	for name, cat := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, te := fieldTypeRef(ftField(t, st, name))
			if te == nil {
				t.Fatalf("expected error category %s, got nil", cat)
			}
			if te.cat != cat {
				t.Errorf("category = %q, want %q (desc: %s)", te.cat, cat, te.desc)
			}
		})
	}
}

func TestParamTypeRef(t *testing.T) {
	st := ftStruct(t, `
type ID string
type S struct {
	Str  string
	PStr *string
	Strs []string
	Bad  struct{ X int }
	Al   ID
}`, "S")
	// path: primitives only, no pointer-as-ok-for-path is caller's concern
	if _, _, ok := paramTypeRef(ftField(t, st, "Str"), false); !ok {
		t.Error("string should be ok for path")
	}
	if _, _, ok := paramTypeRef(ftField(t, st, "Strs"), false); ok {
		t.Error("[]string must NOT be ok for path (allowSlice=false)")
	}
	if r, _, ok := paramTypeRef(ftField(t, st, "Strs"), true); !ok || r.Kind != ir.KindSlice {
		t.Error("[]string should be ok for query (allowSlice=true) as KindSlice")
	}
	if _, isPtr, ok := paramTypeRef(ftField(t, st, "PStr"), false); !ok || !isPtr {
		t.Error("*string should be ok with isPtr=true")
	}
	if _, _, ok := paramTypeRef(ftField(t, st, "Bad"), true); ok {
		t.Error("struct must NOT be a valid param type")
	}
	if r, _, ok := paramTypeRef(ftField(t, st, "Al"), false); !ok || r.Builtin != "string" {
		t.Error("named string alias should resolve to builtin string (underlying)")
	}
}

func TestHasJSONMarshaler(t *testing.T) {
	st := ftStruct(t, `
import "time"
type Marsh struct{}
func (Marsh) MarshalJSON() ([]byte, error) { return nil, nil }
type Plain struct{ X int }
type S struct {
	M Marsh
	P Plain
	T time.Time
}`, "S")
	if !hasJSONMarshaler(ftField(t, st, "M")) {
		t.Error("Marsh has MarshalJSON")
	}
	if hasJSONMarshaler(ftField(t, st, "P")) {
		t.Error("Plain has no MarshalJSON")
	}
	// time.Time DOES implement json.Marshaler — proving why isSpecialBuiltin
	// must be checked BEFORE C3 (ADR 0017/0018 ordering).
	if !hasJSONMarshaler(ftField(t, st, "T")) {
		t.Error("time.Time has MarshalJSON (so the special-first ordering matters)")
	}
	if n, ok := isSpecialBuiltin(ftField(t, st, "T")); !ok || n != "time.Time" {
		t.Error("time.Time must be recognized as a special builtin (short-circuits C3)")
	}
}
