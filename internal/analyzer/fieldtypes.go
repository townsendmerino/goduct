package analyzer

// fieldtypes.go builds ir.TypeRef values from go/types types. There are
// deliberately TWO builders, not one — do not "unify" them:
//   - paramTypeRef is route-discovery-historical and intentionally
//     restrictive: path/query/header params must be a primitive (or
//     []primitive for query/header). A path param can never be a struct,
//     slice-of-map, etc. Keeping this exact subset is what makes the frozen
//     route-discovery tests pass byte-identically.
//   - fieldTypeRef is the general type-traversal builder: any
//     wire-representable shape; named types emit KindNamed WITHOUT recursing
//     (the traversal layer owns recursion). One is a deliberate subset of
//     the other's domain, not dead code.

import (
	"fmt"
	"go/types"

	"github.com/townsendmerino/goduct/internal/ir"
)

// typeErr is a categorized type error (ADR 0018). structfields.go renders
// it into ADR 0019 Format B — it owns the pkg/Fset/field context.
type typeErr struct{ cat, desc, hint string }

func (e *typeErr) Error() string { return e.cat + ": " + e.desc }

func basicName(b *types.Basic) (string, bool) {
	switch b.Kind() {
	case types.Bool:
		return "bool", true
	case types.String:
		return "string", true
	case types.Int, types.Int8, types.Int16, types.Int32, types.Int64,
		types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uint64,
		types.Float32, types.Float64:
		return b.Name(), true
	}
	return "", false
}

// currentAdapters is the user-declared --adapter map for the current
// Analyze call, scoped to the single-threaded analyzer pass. Set by
// Analyze and reset (deferred) on return; read by recognizeBuiltin.
// Package-scoped because threading adapters through every fieldTypeRef
// caller would balloon the diff for a value that's effectively read-only
// for the duration of an Analyze invocation. ADR 0032.
var currentAdapters map[string]string

// recognizeBuiltin recognizes either an ADR 0017 special-type or a
// user-declared adapter (ADR 0032). Built-in takes precedence (ADR 0032
// §2). Returns the ir.Builtin string the analyzer should emit.
func recognizeBuiltin(t types.Type) (string, bool) {
	if name, ok := isSpecialBuiltin(t); ok {
		return name, true
	}
	if n, ok := t.(*types.Named); ok && currentAdapters != nil {
		o := n.Obj()
		if o.Pkg() != nil {
			qname := o.Pkg().Path() + "." + o.Name()
			if _, ok := currentAdapters[qname]; ok {
				return qname, true
			}
		}
	}
	return "", false
}

// isSpecialBuiltin recognizes ADR 0017 types by qualified name (for named
// types) or the literal []byte slice. Returns the ir.Builtin string.
func isSpecialBuiltin(t types.Type) (string, bool) {
	if n, ok := t.(*types.Named); ok {
		o := n.Obj()
		if o.Pkg() == nil {
			return "", false
		}
		switch o.Pkg().Path() + "." + o.Name() {
		case "time.Time":
			return "time.Time", true
		case "time.Duration":
			return "time.Duration", true
		case "encoding/json.RawMessage":
			return "json.RawMessage", true
		case "github.com/google/uuid.UUID":
			return "uuid.UUID", true
		}
		return "", false
	}
	if s, ok := t.(*types.Slice); ok {
		if b, ok := s.Elem().Underlying().(*types.Basic); ok && b.Kind() == types.Uint8 {
			return "[]byte", true
		}
	}
	return "", false
}

// hasJSONMarshaler reports whether t (or *t) has MarshalJSON() ([]byte, error)
// — ADR 0018 C3. ADR 0017 special types are checked BEFORE this and never
// reach it.
func hasJSONMarshaler(t types.Type) bool {
	ms := types.NewMethodSet(types.NewPointer(t))
	for i := 0; i < ms.Len(); i++ {
		fn := ms.At(i).Obj()
		if fn.Name() != "MarshalJSON" {
			continue
		}
		sig, ok := fn.Type().(*types.Signature)
		if !ok || sig.Params().Len() != 0 || sig.Results().Len() != 2 {
			continue
		}
		if sl, ok := sig.Results().At(0).Type().(*types.Slice); ok {
			if b, ok := sl.Elem().Underlying().(*types.Basic); ok && b.Kind() == types.Uint8 {
				return true
			}
		}
	}
	return false
}

// paramTypeRef: the restrictive route-discovery builder. Pointers are
// unwrapped (isPtr reported). path → primitive only; query/header → also
// []primitive. ok=false means "not representable as a param" (the caller
// crafts the user-facing message, since it knows the source/field).
func paramTypeRef(t types.Type, allowSlice bool) (ref ir.TypeRef, isPtr, ok bool) {
	if p, ok := t.(*types.Pointer); ok {
		isPtr, t = true, p.Elem()
	}
	if b, ok := t.Underlying().(*types.Basic); ok {
		if n, ok := basicName(b); ok {
			return ir.TypeRef{Kind: ir.KindBuiltin, Builtin: n}, isPtr, true
		}
	}
	if s, ok := t.Underlying().(*types.Slice); ok && allowSlice {
		if b, ok := s.Elem().Underlying().(*types.Basic); ok {
			if n, ok := basicName(b); ok {
				el := ir.TypeRef{Kind: ir.KindBuiltin, Builtin: n}
				return ir.TypeRef{Kind: ir.KindSlice, Element: &el}, isPtr, true
			}
		}
	}
	return ir.TypeRef{}, isPtr, false
}

// fieldTypeRef builds a non-recursive TypeRef for any wire-representable
// type. Named types emit KindNamed (qualified path) without expansion — the
// traversal layer expands them. Errors are categorized per ADR 0018.
func fieldTypeRef(t types.Type) (ref ir.TypeRef, isPtr bool, err *typeErr) {
	if p, ok := t.(*types.Pointer); ok {
		isPtr, t = true, p.Elem()
	}
	// INVARIANT for any future type-walking code in this package: call
	// types.Unalias(t) (after pointer-unwrap) before switching on Go-type
	// kind. Go 1.22+ alias types (`*types.Alias`, default in 1.24+) cause
	// a naive `t.(type)` switch to miss the real kind — `any`/`interface{}`
	// and `type Foo = Bar` arrive as `*types.Alias`, not `*types.Interface`
	// or the aliased type. Defined types (`type Foo Bar`) are `*types.Named`
	// and unaffected by Unalias. Milestone-14 audit verified this is the
	// only kind-switch in the analyzer; new ones must follow the same rule.
	t = types.Unalias(t)
	if name, ok := recognizeBuiltin(t); ok {
		return ir.TypeRef{Kind: ir.KindBuiltin, Builtin: name}, isPtr, nil
	}
	switch u := t.(type) {
	case *types.TypeParam:
		// ADR 0033: inside a generic type's field list, T resolves to a
		// TypeParam — emit a KindTypeParam ref so generators can render
		// the param name verbatim ("T", "K", "V").
		return ir.TypeRef{Kind: ir.KindTypeParam, TypeParam: u.Obj().Name()}, isPtr, nil
	case *types.Named:
		if hasJSONMarshaler(t) {
			path := ""
			if u.Obj().Pkg() != nil {
				path = u.Obj().Pkg().Path() + "."
			}
			qname := path + u.Obj().Name()
			return ir.TypeRef{}, isPtr, &typeErr{"C3",
				"type " + qname + " has a MarshalJSON method; its wire shape cannot be inferred",
				"declare a custom adapter (e.g. --adapter " + qname + "=string) per ADR 0032, " +
					"or use a string field with manual conversion"}
		}
		path := ""
		if u.Obj().Pkg() != nil {
			path = u.Obj().Pkg().Path() + "."
		}
		ref := ir.TypeRef{Kind: ir.KindNamed, Named: path + u.Obj().Name()}
		// ADR 0033: instantiation — carry the concrete type args.
		// Page[User] gets TypeArgs=[User]; the generic origin Page is
		// emitted exactly once in api.Types regardless of how many
		// distinct instantiations the program contains.
		if ta := u.TypeArgs(); ta != nil && ta.Len() > 0 {
			args := make([]*ir.TypeRef, ta.Len())
			for i := 0; i < ta.Len(); i++ {
				arg, argPtr, e := fieldTypeRef(ta.At(i))
				if e != nil {
					return ir.TypeRef{}, isPtr, e
				}
				arg.Optional = argPtr
				args[i] = &arg
			}
			ref.TypeArgs = args
		}
		return ref, isPtr, nil
	case *types.Basic:
		if n, ok := basicName(u); ok {
			return ir.TypeRef{Kind: ir.KindBuiltin, Builtin: n}, isPtr, nil
		}
		cat, msg := "A3", "complex numbers cannot be serialized"
		switch u.Kind() {
		case types.Uintptr:
			cat, msg = "A5", "uintptr cannot be serialized"
		case types.UnsafePointer:
			cat, msg = "A4", "unsafe.Pointer cannot be serialized"
		}
		return ir.TypeRef{}, isPtr, &typeErr{cat, msg, "use a wire-representable type"}
	case *types.Slice:
		el, elPtr, e := fieldTypeRef(u.Elem())
		if e != nil {
			return ir.TypeRef{}, isPtr, e
		}
		el.Optional = elPtr
		return ir.TypeRef{Kind: ir.KindSlice, Element: &el}, isPtr, nil
	case *types.Array:
		el, elPtr, e := fieldTypeRef(u.Elem())
		if e != nil {
			return ir.TypeRef{}, isPtr, e
		}
		el.Optional = elPtr
		return ir.TypeRef{Kind: ir.KindSlice, Element: &el}, isPtr, nil
	case *types.Map:
		if b, ok := u.Key().Underlying().(*types.Basic); !ok || b.Kind() != types.String {
			return ir.TypeRef{}, isPtr, &typeErr{"B1",
				"map key must be string (or a string-defined type) in v0.1",
				"use a string key, or model it as []KeyValue"}
		}
		k, _, e := fieldTypeRef(u.Key())
		if e != nil {
			return ir.TypeRef{}, isPtr, e
		}
		v, vPtr, e := fieldTypeRef(u.Elem())
		if e != nil {
			return ir.TypeRef{}, isPtr, e
		}
		v.Optional = vPtr
		return ir.TypeRef{Kind: ir.KindMap, Key: &k, Value: &v}, isPtr, nil
	case *types.Interface:
		return ir.TypeRef{}, isPtr, &typeErr{"B2",
			"interface types are not supported in v0.1",
			"for arbitrary JSON use json.RawMessage per ADR 0017; for known shapes use a concrete struct"}
	case *types.Signature:
		return ir.TypeRef{}, isPtr, &typeErr{"A2", "functions cannot be serialized", "remove the field"}
	case *types.Chan:
		return ir.TypeRef{}, isPtr, &typeErr{"A1", "channels cannot be serialized", "remove the field"}
	case *types.Struct:
		return ir.TypeRef{}, isPtr, &typeErr{"B3",
			"anonymous struct fields are not supported in v0.1",
			"extract the struct to a named type"}
	}
	return ir.TypeRef{}, isPtr, &typeErr{"INTERNAL1",
		fmt.Sprintf("unsupported field type %s (kind %T) — this is likely a goduct bug; "+
			"please open an issue with the field declaration", types.TypeString(t, nil), t),
		"open an issue with the field declaration"}
}
