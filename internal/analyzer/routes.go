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
	if !ok {
		return fail("handler %s: missing signature info", name)
	}

	// ADR 0031: branch on raw vs idiomatic signature.
	// func(http.ResponseWriter, *http.Request) -> raw mode (needs
	// goduct:request/response annotations); anything else falls
	// through to the idiomatic shape validation below.
	if isRawHandlerSig(sig) {
		return discoverRawHandler(pkg, fn, dirs)
	}

	// Idiomatic mode forbids goduct:request/response (the types are in
	// the signature; the annotation would be a second source of truth
	// per ADR 0031 §1).
	if dirs.Request != "" || dirs.Response != "" {
		return fail("handler %s: goduct:request/response directives are not allowed on idiomatic handlers "+
			"(use them only with the raw http.HandlerFunc signature, ADR 0031)", name)
	}

	if sig.Params().Len() != 2 {
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
		Pos: pos,
	}
	// ADR 0027: RequestType always non-nil for a discovered route.
	// ADR 0033: TypeArgs populated if reqNamed is an instantiated generic.
	reqRef, err := namedRefWithArgs(reqNamed)
	if err != nil {
		return fail("handler %s: %v", name, err)
	}
	route.RequestType = reqRef
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
		return ir.Route{}, err // ParseStructField already returns ADR 0019 Format B
	}

	bodyAllowed := route.Method != "GET" && route.Method != "HEAD" && route.Method != "DELETE"
	if !bodyAllowed && hasJSON {
		return fail("%s method does not support a request body, but %s has json-tagged fields",
			route.Method, reqNamed.Obj().Name())
	}
	if bodyAllowed && hasJSON {
		bRef, err := namedRefWithArgs(reqNamed)
		if err != nil {
			return fail("handler %s body type: %v", name, err)
		}
		route.BodyType = bRef
	}
	if respNamed != nil {
		rRef, err := namedRefWithArgs(respNamed)
		if err != nil {
			return fail("handler %s response type: %v", name, err)
		}
		route.ResponseType = rRef
	}

	// ADR 0039: capture goduct:example verbatim and resolve each
	// goduct:errorresponse <status> <Type> against the handler's
	// package. Error-response types must be same-package named
	// structs (same constraint as request/response per ADR 0014).
	route.Example = dirs.Example
	for _, er := range dirs.ErrorResponses {
		erNamed, ok := lookupNamedInPkg(pkg, er.TypeName)
		if !ok {
			return fail("handler %s: goduct:errorresponse %d %s: type %s not found in package %s",
				name, er.Status, er.TypeName, er.TypeName, pkg.Types.Path())
		}
		ref, err := namedRefWithArgs(erNamed)
		if err != nil {
			return fail("handler %s errorresponse %d: %v", name, er.Status, err)
		}
		route.ErrorResponses = append(route.ErrorResponses,
			ir.ErrorResponse{Status: er.Status, Type: ref})
	}

	if err := checkPathParams(route); err != nil {
		return ir.Route{}, fmt.Errorf("goduct: %s: %w", pos, err)
	}
	return route, nil
}

// lookupNamedInPkg resolves an unqualified type name against the
// handler's package scope and returns the *types.Named on success.
// Returns ok=false when the name is not in scope or refers to a
// non-named type. Used by ADR 0039's errorresponse and ADR 0031's
// raw-mode request/response resolution shares this shape via the
// same package lookup pattern.
func lookupNamedInPkg(pkg *packages.Package, name string) (*types.Named, bool) {
	obj := pkg.Types.Scope().Lookup(name)
	if obj == nil {
		return nil, false
	}
	named, ok := obj.Type().(*types.Named)
	return named, ok
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

// extractParams fills route.PathParams/QueryParams/HeaderParams and reports
// whether the request struct has any json (body) fields, using the shared
// ParseStructField so route discovery and type traversal cannot disagree.
func extractParams(pkg *packages.Package, req *types.Named, route *ir.Route) (hasJSON bool, err error) {
	st := req.Underlying().(*types.Struct)
	ctx := StructContext{IsRequestType: true, QualifiedName: pkg.Types.Name() + "." + req.Obj().Name()}
	for i := 0; i < st.NumFields(); i++ {
		pf, e := ParseStructField(pkg, st.Field(i), reflect.StructTag(st.Tag(i)), ctx)
		if e != nil {
			return false, e
		}
		if pf == nil {
			continue // skipped: unexported or json:"-" (ADR 0018 D1/D2)
		}
		switch pf.Field.Source {
		case ir.FieldSourceJSON:
			hasJSON = true
		case ir.FieldSourcePath:
			route.PathParams = append(route.PathParams, toParam(pf))
		case ir.FieldSourceQuery:
			route.QueryParams = append(route.QueryParams, toParam(pf))
		case ir.FieldSourceHeader:
			route.HeaderParams = append(route.HeaderParams, toParam(pf))
		}
	}
	return hasJSON, nil
}

// toParam adapts a ParsedField to the ir.Param shape route discovery uses.
// WireName lives on ParsedField, not ir.Field.
func toParam(pf *ParsedField) ir.Param {
	return ir.Param{
		GoName:     pf.Field.GoName,
		WireName:   pf.WireName,
		Type:       pf.Field.Type,
		Optional:   pf.Field.Optional,
		Validation: pf.Field.Validation,
		Doc:        pf.Field.Doc,
	}
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

// namedRefWithArgs builds a KindNamed TypeRef for n, carrying TypeArgs
// when n is an instantiated generic (ADR 0033). RequestType, BodyType,
// and ResponseType all flow through here so a handler returning
// *Page[User] gets a TypeRef whose TypeArgs == [User]. Errors building
// any single arg via fieldTypeRef propagate; the request/response
// types themselves were validated upstream so an error here would be
// a TypeArgs-level issue (e.g. a func in an arg position) that
// rightly stops route discovery.
func namedRefWithArgs(n *types.Named) (*ir.TypeRef, error) {
	ref := &ir.TypeRef{Kind: ir.KindNamed, Named: qualified(n)}
	ta := n.TypeArgs()
	if ta == nil || ta.Len() == 0 {
		return ref, nil
	}
	args := make([]*ir.TypeRef, ta.Len())
	for i := 0; i < ta.Len(); i++ {
		arg, argPtr, te := fieldTypeRef(ta.At(i))
		if te != nil {
			return nil, fmt.Errorf("%s in type argument %d", te.desc, i+1)
		}
		arg.Optional = argPtr
		args[i] = &arg
	}
	ref.TypeArgs = args
	return ref, nil
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
