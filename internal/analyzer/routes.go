package analyzer

// routes.go: walk loaded packages, find goduct-annotated handlers,
// validate their signatures (ADR 0014), and produce []ir.Route with
// request/response types referenced BY QUALIFIED NAME ONLY. It does NOT
// recurse into type fields, populate ir.API.Types, resolve cross-package
// types, or build ir.API — those are later milestones.

import (
	"errors"
	"fmt"
	"go/ast"
	"go/types"
	"reflect"
	"strings"

	"github.com/townsendmerino/goduct/internal/ir"
	"golang.org/x/tools/go/packages"
)

// errNotHandler marks a func that has goduct: lines but no goduct:route —
// it is not a handler, so discovery skips it (not an error).
var errNotHandler = errors.New("not a goduct handler")

// DiscoverRoutes walks pkg and returns one ir.Route per annotated handler,
// in source order. Errors are collected via errors.Join; one handler's
// failure does not stop discovery of the others.
func DiscoverRoutes(pkg *packages.Package) ([]ir.Route, error) {
	var routes []ir.Route
	var errs []error
	for _, file := range pkg.Syntax {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Doc == nil || !hasGoductLine(fn.Doc.Text()) {
				continue
			}
			r, err := discoverHandler(pkg, fn)
			switch {
			case errors.Is(err, errNotHandler):
				continue
			case err != nil:
				errs = append(errs, err)
			default:
				routes = append(routes, r)
			}
		}
	}
	if len(errs) > 0 {
		return routes, errors.Join(errs...)
	}
	return routes, nil
}

func hasGoductLine(doc string) bool {
	for _, ln := range strings.Split(doc, "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), directivePrefix) {
			return true
		}
	}
	return false
}

func discoverHandler(pkg *packages.Package, fn *ast.FuncDecl) (ir.Route, error) {
	pos := pkg.Fset.Position(fn.Pos()).String()
	name := fn.Name.Name
	fail := func(format string, a ...any) (ir.Route, error) {
		return ir.Route{}, fmt.Errorf("goduct: %s: "+format, append([]any{pos}, a...)...)
	}

	dirs, err := ParseDirectives(fn.Doc.Text())
	if err != nil {
		return ir.Route{}, fmt.Errorf("goduct: %s: handler %s: %w", pos, name, err)
	}
	if dirs.Route == nil {
		return ir.Route{}, errNotHandler
	}
	if fn.Recv != nil {
		return fail("methods are not supported as handlers (handler %s has receiver)", name)
	}
	if !ast.IsExported(name) {
		return fail("handler %s must be exported", name)
	}

	sig, ok := pkg.TypesInfo.Defs[fn.Name].Type().(*types.Signature)
	if !ok || sig.Params().Len() != 2 {
		return fail("handler %s must be func(context.Context, T) (*U, error) or func(context.Context, T) error", name)
	}
	if !isContextContext(sig.Params().At(0).Type()) {
		return fail("handler %s: first parameter must be context.Context", name)
	}
	reqNamed, ok := namedStruct(sig.Params().At(1).Type())
	if !ok {
		return fail("handler %s: request parameter must be a named struct type", name)
	}
	if reqNamed.Obj().Pkg() != pkg.Types {
		return fail("request type %s is defined in package %s, not in handler's package %s "+
			"(cross-package request/response types are not yet supported; ADR 0014)",
			reqNamed.Obj().Name(), reqNamed.Obj().Pkg().Path(), pkg.Types.Path())
	}

	var respNamed *types.Named
	switch res := sig.Results(); res.Len() {
	case 1:
		if !isError(res.At(0).Type()) {
			return fail("handler %s: single return value must be error", name)
		}
	case 2:
		ptr, ok := res.At(0).Type().(*types.Pointer)
		if !ok {
			return fail("handler %s: first return value must be a pointer to a named struct (*U)", name)
		}
		rn, ok := namedStruct(ptr.Elem())
		if !ok {
			return fail("handler %s: first return value must be a pointer to a named struct (*U)", name)
		}
		if !isError(res.At(1).Type()) {
			return fail("handler %s: second return value must be error", name)
		}
		if rn.Obj().Pkg() != pkg.Types {
			return fail("response type %s is defined in package %s, not in handler's package %s "+
				"(cross-package request/response types are not yet supported; ADR 0014)",
				rn.Obj().Name(), rn.Obj().Pkg().Path(), pkg.Types.Path())
		}
		respNamed = rn
	default:
		return fail("handler %s: must return (*U, error) or error, got %d values", name, res.Len())
	}

	route := ir.Route{
		HandlerName: name,
		Method:      dirs.Route.Method,
		Path:        dirs.Route.Path,
		Mode:        ir.ModeIdiomatic,
		Doc:         dirs.Doc,
		Pos:         pos,
	}
	if dirs.Tag != "" {
		route.Tag = dirs.Tag
	} else {
		route.Tag = inferTag(route.Path)
	}

	status, err := resolveStatus(dirs, route.Method, respNamed != nil, name)
	if err != nil {
		return ir.Route{}, fmt.Errorf("goduct: %s: %w", pos, err)
	}
	route.SuccessStatus = status

	hasJSON, err := extractParams(pkg, reqNamed, &route)
	if err != nil {
		return ir.Route{}, fmt.Errorf("goduct: %s: %w", pos, err)
	}

	bodyAllowed := route.Method != "GET" && route.Method != "HEAD" && route.Method != "DELETE"
	if !bodyAllowed && hasJSON {
		return fail("%s method does not support a request body, but %s has json-tagged fields",
			route.Method, reqNamed.Obj().Name())
	}
	if bodyAllowed && hasJSON {
		route.BodyType = &ir.TypeRef{Kind: ir.KindNamed, Named: qualified(reqNamed)}
	}
	if respNamed != nil {
		route.ResponseType = &ir.TypeRef{Kind: ir.KindNamed, Named: qualified(respNamed)}
	}

	if err := checkPathParams(route); err != nil {
		return ir.Route{}, fmt.Errorf("goduct: %s: %w", pos, err)
	}
	return route, nil
}

// resolveStatus implements the ADR-0014-faithful rule (confirmed with the
// user): an error-only handler is valid only with explicit status:204, or
// no status AND method DELETE (→204). Two-return handlers default per
// ir.go: 201 for POST, else 200.
func resolveStatus(d Directives, method string, hasResponse bool, name string) (int, error) {
	if d.Status != nil {
		s := *d.Status
		if !hasResponse && s != 204 {
			return 0, fmt.Errorf("handler %s returns no response body but status is %d "+
				"(only 204 is valid for empty responses)", name, s)
		}
		return s, nil
	}
	if !hasResponse {
		if method == "DELETE" {
			return 204, nil
		}
		return 0, fmt.Errorf("handler %s returns only error; an error-only handler requires "+
			"goduct:status 204 (or method DELETE); ADR 0014", name)
	}
	if method == "POST" {
		return 201, nil
	}
	return 200, nil
}

func extractParams(pkg *packages.Package, req *types.Named, route *ir.Route) (hasJSON bool, err error) {
	st := req.Underlying().(*types.Struct)
	docs := fieldDocs(pkg, req)
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		if f.Embedded() {
			return false, fmt.Errorf("embedded fields in request structs are not yet supported "+
				"(field %s in %s)", f.Name(), req.Obj().Name())
		}
		tag := reflect.StructTag(st.Tag(i))
		kind, wire, n := soleTag(tag)
		if n > 1 {
			return false, fmt.Errorf("field %s has conflicting tags "+
				"(path/query/header/json are mutually exclusive)", f.Name())
		}
		if n == 0 {
			continue // untagged: ignored bookkeeping field
		}
		if kind == "json" {
			hasJSON = true
			continue
		}
		p, perr := buildParam(kind, wire, f, tag, docs[f.Name()])
		if perr != nil {
			return false, perr
		}
		switch kind {
		case "path":
			route.PathParams = append(route.PathParams, p)
		case "query":
			route.QueryParams = append(route.QueryParams, p)
		case "header":
			route.HeaderParams = append(route.HeaderParams, p)
		}
	}
	return hasJSON, nil
}

func buildParam(kind, wire string, f *types.Var, tag reflect.StructTag, doc string) (ir.Param, error) {
	rules := parseValidate(tag)
	ref, isPtr, terr := typeRef(f.Type(), kind != "path")
	if terr != nil {
		return ir.Param{}, fmt.Errorf("%s param %s has unsupported type %s in v0.1",
			kind, f.Name(), f.Type().String())
	}
	if kind == "path" && isPtr {
		return ir.Param{}, fmt.Errorf("path param %s cannot be a pointer "+
			"(path params are always present)", f.Name())
	}
	p := ir.Param{GoName: f.Name(), WireName: wire, Type: ref, Validation: rules, Doc: doc}
	if kind == "path" {
		p.Optional = false
	} else {
		p.Optional = !hasRule(rules, "required")
	}
	return p, nil
}

// soleTag returns the single source tag (path/query/header/json) and how
// many of those four are present (for the mutual-exclusion check).
func soleTag(tag reflect.StructTag) (kind, wire string, n int) {
	for _, k := range [...]string{"path", "query", "header", "json"} {
		if v, ok := tag.Lookup(k); ok {
			n++
			kind, wire = k, strings.Split(v, ",")[0]
		}
	}
	return kind, wire, n
}

// typeRef builds an ir.TypeRef for a param field. Pointers are unwrapped
// (reporting isPtr). path allows primitives only; query/header also allow
// []primitive (allowSlice).
func typeRef(t types.Type, allowSlice bool) (ref ir.TypeRef, isPtr bool, err error) {
	if p, ok := t.(*types.Pointer); ok {
		isPtr, t = true, p.Elem()
	}
	if b, ok := t.Underlying().(*types.Basic); ok {
		if n, ok := basicName(b); ok {
			return ir.TypeRef{Kind: ir.KindBuiltin, Builtin: n}, isPtr, nil
		}
	}
	if s, ok := t.Underlying().(*types.Slice); ok && allowSlice {
		if b, ok := s.Elem().Underlying().(*types.Basic); ok {
			if n, ok := basicName(b); ok {
				el := ir.TypeRef{Kind: ir.KindBuiltin, Builtin: n}
				return ir.TypeRef{Kind: ir.KindSlice, Element: &el}, isPtr, nil
			}
		}
	}
	return ir.TypeRef{}, isPtr, fmt.Errorf("unsupported")
}

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

func parseValidate(tag reflect.StructTag) []ir.ValidationRule {
	v, ok := tag.Lookup("validate")
	if !ok || v == "" {
		return nil
	}
	var rules []ir.ValidationRule
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if name, arg, found := strings.Cut(part, "="); found {
			rules = append(rules, ir.ValidationRule{Name: name, Arg: arg})
		} else {
			rules = append(rules, ir.ValidationRule{Name: part})
		}
	}
	return rules
}

func hasRule(rules []ir.ValidationRule, name string) bool {
	for _, r := range rules {
		if r.Name == name {
			return true
		}
	}
	return false
}

// checkPathParams enforces that every {name}/:name segment in the route
// path has a matching path-tagged field, and vice versa.
func checkPathParams(r ir.Route) error {
	inPath := map[string]bool{}
	for _, seg := range strings.Split(r.Path, "/") {
		if name, ok := strings.CutPrefix(seg, ":"); ok && name != "" {
			inPath[name] = true
		} else if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") && len(seg) > 2 {
			inPath[seg[1:len(seg)-1]] = true
		}
	}
	inStruct := map[string]bool{}
	for _, p := range r.PathParams {
		inStruct[p.WireName] = true
		if !inPath[p.WireName] {
			return fmt.Errorf("path param %q has no matching segment in route path %q",
				p.WireName, r.Path)
		}
	}
	for name := range inPath {
		if !inStruct[name] {
			return fmt.Errorf("route path %q has segment :%s with no matching path-tagged field",
				r.Path, name)
		}
	}
	return nil
}

func inferTag(path string) string {
	for _, seg := range strings.Split(path, "/") {
		if seg != "" && !strings.HasPrefix(seg, ":") && !strings.HasPrefix(seg, "{") {
			return strings.ToLower(seg)
		}
	}
	return ""
}

func qualified(n *types.Named) string {
	return n.Obj().Pkg().Path() + "." + n.Obj().Name()
}

func isContextContext(t types.Type) bool {
	n, ok := t.(*types.Named)
	return ok && n.Obj().Pkg() != nil &&
		n.Obj().Pkg().Path() == "context" && n.Obj().Name() == "Context"
}

func isError(t types.Type) bool {
	return types.Identical(t, types.Universe.Lookup("error").Type())
}

func namedStruct(t types.Type) (*types.Named, bool) {
	n, ok := t.(*types.Named)
	if !ok {
		return nil, false
	}
	if _, ok := n.Underlying().(*types.Struct); !ok {
		return nil, false
	}
	return n, true
}

// fieldDocs maps request-struct field name → godoc, best-effort from the
// AST (go/types carries no doc comments).
func fieldDocs(pkg *packages.Package, n *types.Named) map[string]string {
	out := map[string]string{}
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
				stype, ok := ts.Type.(*ast.StructType)
				if !ok {
					continue
				}
				for _, fld := range stype.Fields.List {
					d := strings.TrimSpace(fld.Doc.Text())
					for _, id := range fld.Names {
						out[id.Name] = d
					}
				}
			}
		}
	}
	return out
}
