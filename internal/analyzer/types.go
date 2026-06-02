package analyzer

// types.go is the type-traversal layer: starting from a []ir.Route it walks
// every type transitively reachable from each route's request/response and
// produces the ir.API.Types map. Cycles are broken per ADR 0018 D4; ADR 0017
// special builtins never become TypeDefs (they are intercepted at the field
// level by fieldTypeRef and emitted as KindBuiltin). All map keys and
// TypeRef.Named values are the FULL import-path qualified name
// ("github.com/.../api.User"), matching the frozen IR contract.

import (
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"reflect"
	"sort"
	"strings"

	"github.com/townsendmerino/goduct/internal/ir"
	"golang.org/x/tools/go/packages"
)

// DiscoverTypes walks the type graph reachable from routes and returns a
// map suitable for ir.API.Types. Errors are joined (ADR 0019 Format B).
func DiscoverTypes(pkg *packages.Package, routes []ir.Route) (map[string]ir.TypeDef, error) {
	result := map[string]ir.TypeDef{}
	visiting := map[string]bool{}
	var errs []error
	enums := collectEnumConsts(pkg)

	requestTypeNames := map[string]bool{}
	seen := map[string]bool{}
	var seeds []string
	add := func(q string) {
		if q != "" && !seen[q] {
			seen[q] = true
			seeds = append(seeds, q)
		}
	}
	for _, r := range routes {
		// The request type is the handler's 2nd param. ir.Route has no
		// field for it (BodyType is nil for GET/DELETE), so re-resolve via
		// the handler's signature — the authoritative source (Q1).
		obj := pkg.Types.Scope().Lookup(r.HandlerName)
		fn, ok := obj.(*types.Func)
		if !ok {
			panic("DiscoverTypes: handler " + r.HandlerName + " not a package-scope func (route discovery should guarantee this)")
		}
		reqT := fn.Signature().Params().At(1).Type()
		if p, ok := reqT.(*types.Pointer); ok {
			reqT = p.Elem()
		}
		n, ok := types.Unalias(reqT).(*types.Named)
		if !ok {
			panic("DiscoverTypes: request type for " + r.HandlerName + " is not a named struct (route discovery should reject this)")
		}
		q := qual(n)
		requestTypeNames[q] = true
		// Seed the request type itself, plus the named-type leaves of
		// each route TypeRef (ADR 0033: walking via collectNamedDeps
		// reaches TypeArgs leaves like the User inside *Page[User]).
		add(q)
		if r.RequestType != nil {
			var d []string
			collectNamedDeps(*r.RequestType, &d)
			for _, x := range d {
				add(x)
			}
		}
		if r.ResponseType != nil {
			var d []string
			collectNamedDeps(*r.ResponseType, &d)
			for _, x := range d {
				add(x)
			}
		}
		if r.BodyType != nil {
			var d []string
			collectNamedDeps(*r.BodyType, &d)
			for _, x := range d {
				add(x)
			}
		}
	}

	for _, s := range seeds {
		visitType(pkg, s, requestTypeNames, enums, result, visiting, &errs)
	}
	if len(errs) > 0 {
		return result, errors.Join(errs...)
	}
	return result, nil
}

// visitType resolves qname and records its TypeDef. ADR 0018 D4: a type
// already in result is done; a type currently `visiting` is a cycle — we
// return and let the in-progress caller finish recording it (the referring
// field already holds a KindNamed ref to qname).
func visitType(pkg *packages.Package, qname string, reqNames map[string]bool, enums map[string][]ir.EnumValue, result map[string]ir.TypeDef, visiting map[string]bool, errs *[]error) {
	if _, done := result[qname]; done {
		return
	}
	if visiting[qname] {
		return
	}
	visiting[qname] = true
	defer delete(visiting, qname)

	n := resolveNamed(pkg, qname)
	if n == nil {
		pkgPath := qname
		if i := strings.LastIndex(qname, "."); i >= 0 {
			pkgPath = qname[:i]
		}
		*errs = append(*errs, fmt.Errorf(
			"goduct: -: C2: type %s is defined in package %s, not the handler's package; "+
				"cross-package types are deferred to v0.2\n        in %s\n        hint: move the type into the handler's package, or wait",
			qname, pkgPath, qname))
		return
	}
	if name, ok := recognizeBuiltin(n); ok {
		panic("visitType reached ADR 0017 special or ADR 0032 adapted builtin " +
			name + " (" + qname + "); must be field-level only")
	}

	doc, pos := typeDocPos(pkg, n)
	base := ir.TypeDef{QualifiedName: qname, Name: n.Obj().Name(), Doc: doc, Pos: pos}

	// ADR 0033 + ADR 0036: capture type-parameter names AND their
	// constraints for generic types. v0.4 supports `any` (constraint
	// = nil) and type-union constraints (`[T int | int64]`); method-
	// bearing interface constraints, `comparable`, and `~`-tilde
	// approximation terms still loud-fail.
	if tp := n.TypeParams(); tp != nil && tp.Len() > 0 {
		for i := 0; i < tp.Len(); i++ {
			p := tp.At(i)
			constraint, te := extractConstraint(p)
			if te != nil {
				*errs = append(*errs, formatTypeErr(pkg, n, te.cat,
					"type parameter "+p.Obj().Name()+" on "+n.Obj().Name()+": "+te.desc,
					te.hint))
				return
			}
			base.TypeParams = append(base.TypeParams, p.Obj().Name())
			base.TypeParamConstraints = append(base.TypeParamConstraints, constraint)
		}
	}

	switch u := n.Underlying().(type) {
	case *types.Struct:
		base.Kind = ir.TypeStruct
		ctx := StructContext{IsRequestType: reqNames[qname], QualifiedName: pkg.Types.Name() + "." + n.Obj().Name()}
		var deps []string
		for i := 0; i < u.NumFields(); i++ {
			pf, e := ParseStructField(pkg, u.Field(i), reflect.StructTag(u.Tag(i)), ctx)
			if e != nil {
				*errs = append(*errs, e)
				continue
			}
			if pf == nil {
				continue
			}
			base.Fields = append(base.Fields, pf.Field)
			collectNamedDeps(pf.Field.Type, &deps)
		}
		result[qname] = base // record BEFORE recursing (cycle correctness, D4)
		for _, d := range deps {
			visitType(pkg, d, reqNames, enums, result, visiting, errs)
		}

	case *types.Basic:
		if ev, ok := enums[qname]; ok {
			base.Kind, base.EnumValues = ir.TypeEnum, ev
			if u.Info()&types.IsString != 0 {
				base.Underlying = "string"
			} else {
				base.Underlying = "int"
			}
			result[qname] = base
			return
		}
		bn, ok := basicName(u)
		if !ok {
			*errs = append(*errs, formatTypeErr(pkg, n, "B4",
				"named type "+n.Obj().Name()+" has unrepresentable underlying "+u.String(),
				"use a wire-representable underlying type"))
			return
		}
		ref := ir.TypeRef{Kind: ir.KindBuiltin, Builtin: bn}
		base.Kind, base.AliasTo = ir.TypeAlias, &ref
		result[qname] = base

	case *types.Slice, *types.Array, *types.Map:
		// ADR 0018 D5: named slice/map of supported types → TypeAlias with
		// a composite AliasTo (type Tags []string, type Headers map[...]).
		ref, deps, te := compositeAlias(u)
		if te != nil {
			*errs = append(*errs, formatTypeErr(pkg, n, te.cat, te.desc, te.hint))
			return
		}
		base.Kind, base.AliasTo = ir.TypeAlias, &ref
		result[qname] = base
		for _, d := range deps {
			visitType(pkg, d, reqNames, enums, result, visiting, errs)
		}

	case *types.Interface:
		*errs = append(*errs, formatTypeErr(pkg, n, "B4",
			"named type "+n.Obj().Name()+" has unrepresentable underlying interface", "use a concrete struct"))
	case *types.Signature:
		*errs = append(*errs, formatTypeErr(pkg, n, "B4",
			"named type "+n.Obj().Name()+" has unrepresentable underlying func", "remove it"))
	case *types.Chan:
		*errs = append(*errs, formatTypeErr(pkg, n, "B4",
			"named type "+n.Obj().Name()+" has unrepresentable underlying chan", "remove it"))
	case *types.Pointer:
		*errs = append(*errs, formatTypeErr(pkg, n, "B4",
			"named type "+n.Obj().Name()+" has unrepresentable underlying pointer", "use the pointee type"))
	default:
		*errs = append(*errs, formatTypeErr(pkg, n, "INTERNAL1",
			fmt.Sprintf("named type %s has unhandled underlying %T — likely a goduct bug; "+
				"please open an issue with the type declaration", n.Obj().Name(), u),
			"open an issue with the type declaration"))
	}
}

// compositeAlias builds the AliasTo TypeRef for a named slice/array/map and
// returns the named-type deps to recurse. Reuses fieldTypeRef so the
// element/value/key rules (special builtins, B1 string-key, nesting) match
// the field path exactly.
func compositeAlias(u types.Type) (ir.TypeRef, []string, *typeErr) {
	var ref ir.TypeRef
	switch c := u.(type) {
	case *types.Slice:
		el, elPtr, te := fieldTypeRef(c.Elem())
		if te != nil {
			return ref, nil, te
		}
		el.Optional = elPtr
		ref = ir.TypeRef{Kind: ir.KindSlice, Element: &el}
	case *types.Array:
		el, elPtr, te := fieldTypeRef(c.Elem())
		if te != nil {
			return ref, nil, te
		}
		el.Optional = elPtr
		ref = ir.TypeRef{Kind: ir.KindSlice, Element: &el}
	case *types.Map:
		if b, ok := c.Key().Underlying().(*types.Basic); !ok || b.Kind() != types.String {
			return ref, nil, &typeErr{"B1", "map key must be string (or a string-defined type) in v0.1", "use a string key"}
		}
		k, _, te := fieldTypeRef(c.Key())
		if te != nil {
			return ref, nil, te
		}
		v, vPtr, te := fieldTypeRef(c.Elem())
		if te != nil {
			return ref, nil, te
		}
		v.Optional = vPtr
		ref = ir.TypeRef{Kind: ir.KindMap, Key: &k, Value: &v}
	}
	var deps []string
	collectNamedDeps(ref, &deps)
	return ref, deps, nil
}

func collectNamedDeps(r ir.TypeRef, out *[]string) {
	switch r.Kind {
	case ir.KindNamed:
		*out = append(*out, r.Named)
		// ADR 0033: a generic instantiation's TypeArgs are themselves
		// named-type references (or builtins); walk them so the args
		// get seeded into the visit queue alongside the generic origin.
		for _, ta := range r.TypeArgs {
			if ta != nil {
				collectNamedDeps(*ta, out)
			}
		}
	case ir.KindSlice:
		if r.Element != nil {
			collectNamedDeps(*r.Element, out)
		}
	case ir.KindMap:
		if r.Key != nil {
			collectNamedDeps(*r.Key, out)
		}
		if r.Value != nil {
			collectNamedDeps(*r.Value, out)
		}
	}
}

// extractConstraint resolves a type-param's constraint into an ir.TypeRef
// suitable for TypeDef.TypeParamConstraints (ADR 0036). Returns:
//   - (nil, nil)  → `any` constraint (empty interface).
//   - (ref, nil)  → a single-type or KindUnion-of-types constraint.
//   - (nil, te)   → loud-fail (method-bearing, comparable, tilde-form).
//
// The walker accepts both Go representations of a single-type
// constraint (`interface{ int }` arrives as a bare embedded basic
// OR a 1-term *types.Union; both are normalized here).
func extractConstraint(p *types.TypeParam) (*ir.TypeRef, *typeErr) {
	// `comparable` and `any` both produce an underlying interface
	// with no methods/embeds; the differentiator is the top-level
	// Constraint type's String() representation. Reject `comparable`
	// up-front before the empty-interface short-circuit treats it
	// as `any`.
	if p.Constraint().String() == "comparable" {
		return nil, &typeErr{"C1",
			"the `comparable` constraint is not supported (Go-typesystem-only; no wire mapping)",
			"use `any` or an explicit type-union constraint like `int | int64`"}
	}
	iface, ok := p.Constraint().Underlying().(*types.Interface)
	if !ok {
		return nil, &typeErr{"C1",
			"constraint underlying is not an interface",
			"use `any` or a type-union constraint like `int | int64`"}
	}
	if iface.NumMethods() > 0 {
		return nil, &typeErr{"C1",
			"method-bearing constraints (Stringer, fmt.Stringer, etc.) are not supported in v0.4 — methods don't survive JSON serialization",
			"use `any` or a type-union constraint like `int | int64`"}
	}
	if iface.NumEmbeddeds() == 0 {
		// empty interface → `any`; nil constraint per the IR contract.
		return nil, nil
	}
	var terms []*ir.TypeRef
	for i := 0; i < iface.NumEmbeddeds(); i++ {
		embed := iface.EmbeddedType(i)
		// Reject the `comparable` predeclared identifier loudly.
		if named, ok := embed.(*types.Named); ok &&
			named.Obj().Name() == "comparable" && named.Obj().Pkg() == nil {
			return nil, &typeErr{"C1",
				"the `comparable` constraint is not supported (Go-typesystem-only; no wire mapping)",
				"use `any` or an explicit type-union"}
		}
		switch e := embed.(type) {
		case *types.Union:
			for j := 0; j < e.Len(); j++ {
				term := e.Term(j)
				if term.Tilde() {
					return nil, &typeErr{"C1",
						"the `~` tilde-form approximation in type-union constraints is not supported in v0.4",
						"remove `~` (or wait for v0.5)"}
				}
				ref, _, te := fieldTypeRef(term.Type())
				if te != nil {
					return nil, te
				}
				terms = append(terms, &ref)
			}
		default:
			ref, _, te := fieldTypeRef(embed)
			if te != nil {
				return nil, te
			}
			terms = append(terms, &ref)
		}
	}
	if len(terms) == 1 {
		return terms[0], nil
	}
	return &ir.TypeRef{Kind: ir.KindUnion, UnionTerms: terms}, nil
}

// resolveNamed returns the *types.Named for a full-path qualified name, or
// nil when it is not in the handler's package (caller emits C2). Special
// builtins never reach here (they are KindBuiltin, never seeded/recursed).
func resolveNamed(pkg *packages.Package, qname string) *types.Named {
	i := strings.LastIndex(qname, ".")
	if i < 0 {
		return nil
	}
	pkgPath, name := qname[:i], qname[i+1:]
	if pkgPath != pkg.Types.Path() {
		return nil
	}
	obj := pkg.Types.Scope().Lookup(name)
	if obj == nil {
		return nil
	}
	n, _ := types.Unalias(obj.Type()).(*types.Named)
	return n
}

// collectEnumConsts maps full-qualified-type-name → []EnumValue for enums
// declared in pkg. SAME-PACKAGE ONLY by design: consistent with ADR 0014 /
// ADR 0018 C2 (cross-package deferred), a named string/int type whose
// constants live in another package is treated as a TypeAlias (no
// constants), NOT an enum. Do not expand to imported packages without an
// ADR — if this rule misclassifies a "should-work" enum, stop and write one.
func collectEnumConsts(pkg *packages.Package) map[string][]ir.EnumValue {
	type posVal struct {
		pos token.Pos
		ev  ir.EnumValue
	}
	grouped := map[string][]posVal{}
	scope := pkg.Types.Scope()
	for _, name := range scope.Names() {
		c, ok := scope.Lookup(name).(*types.Const)
		if !ok {
			continue
		}
		nt, ok := types.Unalias(c.Type()).(*types.Named)
		if !ok || nt.Obj().Pkg() != pkg.Types {
			continue
		}
		b, ok := nt.Underlying().(*types.Basic)
		if !ok {
			continue
		}
		info := b.Info()
		if info&types.IsString == 0 && info&types.IsInteger == 0 {
			continue
		}
		var val string
		if info&types.IsString != 0 {
			val = constant.StringVal(c.Val())
		} else {
			val = c.Val().String()
		}
		q := qual(nt)
		grouped[q] = append(grouped[q], posVal{c.Pos(), ir.EnumValue{GoName: c.Name(), Value: val}})
	}
	// Source-declaration order, NOT scope.Names()'s alphabetical order: the
	// generated TS union / zod enum must match the Go source order (the
	// golden expected/ output relies on this).
	out := map[string][]ir.EnumValue{}
	for q, pvs := range grouped {
		sort.Slice(pvs, func(i, j int) bool { return pvs[i].pos < pvs[j].pos })
		for _, pv := range pvs {
			out[q] = append(out[q], pv.ev)
		}
	}
	return out
}

func qual(n *types.Named) string {
	o := n.Obj()
	if o.Pkg() == nil {
		return o.Name()
	}
	return o.Pkg().Path() + "." + o.Name()
}

// formatTypeErr renders a struct/type-level ADR 0019 Format B error:
// "in <pkg.Type>" with no parenthesized Go-type; position at the decl.
func formatTypeErr(pkg *packages.Package, n *types.Named, cat, desc, hint string) error {
	pos := pkg.Fset.Position(n.Obj().Pos())
	return fmt.Errorf("goduct: %s:%d:%d: %s: %s\n        in %s\n        hint: %s",
		pos.Filename, pos.Line, pos.Column, cat, desc,
		pkg.Types.Name()+"."+n.Obj().Name(), hint)
}

// typeDocPos returns a named type's godoc and "file:line:col", best-effort
// from the AST (go/types carries no doc comments).
func typeDocPos(pkg *packages.Package, n *types.Named) (string, string) {
	pos := pkg.Fset.Position(n.Obj().Pos()).String()
	for _, file := range pkg.Syntax {
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || ts.Name.Name != n.Obj().Name() {
					continue
				}
				d := strings.TrimSpace(ts.Doc.Text())
				if d == "" {
					d = strings.TrimSpace(gd.Doc.Text())
				}
				return d, pos
			}
		}
	}
	return "", pos
}
