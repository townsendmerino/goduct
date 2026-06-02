package analyzer

// routes_raw.go is the raw-mode (ir.ModeRaw) branch of route discovery
// per ADR 0031. Functions whose signature is exactly
// `func(http.ResponseWriter, *http.Request)` are raw handlers when
// they carry a goduct:route plus goduct:request/response annotations;
// the user manages body decoding, path-param extraction, and response
// writing themselves. goduct still consumes the request/response types
// for the TS generators and registers the function as the route's
// http.HandlerFunc verbatim.

import (
	"fmt"
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/packages"

	"github.com/townsendmerino/goduct/internal/ir"
)

// isRawHandlerSig reports whether sig matches `func(http.ResponseWriter,
// *http.Request)` exactly: two params (http.ResponseWriter interface,
// pointer-to-http.Request), no return values.
func isRawHandlerSig(sig *types.Signature) bool {
	if sig.Params().Len() != 2 || sig.Results().Len() != 0 {
		return false
	}
	if !isNamedFromPkg(sig.Params().At(0).Type(), "net/http", "ResponseWriter") {
		return false
	}
	return isPointerToNamedFromPkg(sig.Params().At(1).Type(), "net/http", "Request")
}

// isNamedFromPkg reports whether t is a *types.Named (or alias thereof)
// declared in pkgPath with name typeName.
func isNamedFromPkg(t types.Type, pkgPath, typeName string) bool {
	t = types.Unalias(t)
	n, ok := t.(*types.Named)
	if !ok {
		return false
	}
	obj := n.Obj()
	return obj.Pkg() != nil && obj.Pkg().Path() == pkgPath && obj.Name() == typeName
}

// isPointerToNamedFromPkg reports whether t is `*<pkgPath>.<typeName>`.
func isPointerToNamedFromPkg(t types.Type, pkgPath, typeName string) bool {
	p, ok := t.(*types.Pointer)
	if !ok {
		return false
	}
	return isNamedFromPkg(p.Elem(), pkgPath, typeName)
}

// discoverRawHandler builds the ir.Route for a raw-mode handler.
// Caller has already validated: fn is annotated with goduct:route,
// fn has no receiver, fn is exported, and fn's signature matches
// isRawHandlerSig. We resolve goduct:request / goduct:response in
// the handler's package scope, share the parameter/status logic with
// the idiomatic path, and mark Mode = ModeRaw.
func discoverRawHandler(pkg *packages.Package, fn *ast.FuncDecl, dirs Directives) (ir.Route, error) {
	pos := pkg.Fset.Position(fn.Pos()).String()
	name := fn.Name.Name
	fail := func(format string, a ...any) (ir.Route, error) {
		return ir.Route{}, fmt.Errorf("goduct: %s: "+format, append([]any{pos}, a...)...)
	}

	if dirs.Request == "" {
		return fail("handler %s: raw http.HandlerFunc mode requires `goduct:request <Type>` (ADR 0031)", name)
	}

	reqNamed, err := lookupNamedStruct(pkg, dirs.Request, "goduct:request", name)
	if err != nil {
		return ir.Route{}, fmt.Errorf("goduct: %s: %w", pos, err)
	}

	var respNamed *types.Named
	if dirs.Response != "" {
		rn, err := lookupNamedStruct(pkg, dirs.Response, "goduct:response", name)
		if err != nil {
			return ir.Route{}, fmt.Errorf("goduct: %s: %w", pos, err)
		}
		respNamed = rn
	}

	route := ir.Route{
		HandlerName: name,
		Method:      dirs.Route.Method,
		Path:        dirs.Route.Path,
		Mode:        ir.ModeRaw,
		Doc:         dirs.Doc,
		Pos:         pos,
		RequestType: &ir.TypeRef{Kind: ir.KindNamed, Named: qualified(reqNamed)},
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

	// Path/query/header param extraction is shared with idiomatic mode:
	// raw handlers still declare path:/query:/header:/json: tags on the
	// request struct so the TS client+zod know the wire shape.
	hasJSON, err := extractParams(pkg, reqNamed, &route)
	if err != nil {
		return ir.Route{}, err
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

// lookupNamedStruct resolves a directive-named type in pkg's scope and
// verifies it's a named struct in pkg.Types itself (same-package only,
// matching the v0.2 idiomatic constraint from ADR 0014).
func lookupNamedStruct(pkg *packages.Package, typeName, directive, handler string) (*types.Named, error) {
	obj := pkg.Types.Scope().Lookup(typeName)
	if obj == nil {
		return nil, fmt.Errorf("handler %s: %s type %s not found in package %s",
			handler, directive, typeName, pkg.Types.Path())
	}
	n, ok := namedStruct(obj.Type())
	if !ok {
		return nil, fmt.Errorf("handler %s: %s %s is not a named struct", handler, directive, typeName)
	}
	if n.Obj().Pkg() != pkg.Types {
		return nil, fmt.Errorf("handler %s: %s type %s is defined in package %s, not %s "+
			"(cross-package request/response types are not yet supported; ADR 0014/0031)",
			handler, directive, typeName, n.Obj().Pkg().Path(), pkg.Types.Path())
	}
	return n, nil
}
