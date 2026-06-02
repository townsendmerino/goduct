// Package goadapter generates goduct_routes.go: Go router wiring for
// one of the supported frameworks (chi by default; gin, echo, or
// net/http mux via --framework). Unlike the TS generators it walks
// api.Routes and emits Go (a Register(<routerType>) plus a handle<Name>
// wrapper per route, in the handlers' own package per ADR 0009). Per
// ADR 0022 §4 the final step is go/format.Source — a gofmt failure
// here is a generator bug, not a user problem. No doc comments
// (ADR 0024); Register's two-line comment is fixed boilerplate.
//
// Framework parameterization (ADR 0030 §2): the four frameworks differ
// in maybe a dozen identifiers (router type, register-method shape,
// path-param extraction, wrapper signature, ResponseWriter expression,
// return shape for the echo error-returning handler). A small
// `framework` struct holds those knobs; the generator body reads from
// it instead of hardcoding chi. v0.1 callers using Generate(api, w)
// see byte-identical chi output.
package goadapter

import (
	"fmt"
	"go/format"
	"io"
	"strings"

	"github.com/townsendmerino/goduct/internal/gen"
	"github.com/townsendmerino/goduct/internal/ir"
)

// goductRuntimeImport is a constant: goduct's own runtime module path.
// Every consumer imports goduct's runtime here regardless of their own
// module path — it is goduct's identity, not derived from the user.
const goductRuntimeImport = `goduct "github.com/townsendmerino/goduct/runtime"`

const registerDoc = `// Register wires every goduct-annotated handler in this package to r.
// Call this after applying any middleware you want to share across handlers.`

// framework holds the per-target knobs ADR 0030 §2 enumerates. Adding a
// new framework is one new entry in `frameworks` and one new golden
// under examples/chi-basic/testdata/expected/<framework>/.
type framework struct {
	name              string
	importLine        string // empty if the framework is stdlib only (mux)
	registerParamType string // e.g. "chi.Router", "*gin.Engine"
	pathConvert       func(string) string
	registerCall      func(fw *framework, rt ir.Route) string // one Register-body line
	wrapperParams     string // e.g. "w http.ResponseWriter, r *http.Request"
	wrapperRet        string // "" or " error"
	earlyReturn       string // "return" or "return nil"
	finalReturn       string // "" or "\treturn nil\n" (emitted just before closing brace)
	pathParamExpr     func(name string) string
	writerExpr        string // ResponseWriter expression
	bodyExpr          string // request body io.Reader
	ctxExpr           string // context.Context expression
	queryExpr         string // url.Values expression
}

// stdRegisterCall: r.<MethodIdent>("<convertedPath>", handle<Name>) —
// used by chi/gin/echo. mux overrides for its `r.HandleFunc("GET /x", h)`
// shape.
func stdRegisterCall(methodIdent func(string) string) func(*framework, ir.Route) string {
	return func(fw *framework, rt ir.Route) string {
		return "r." + methodIdent(rt.Method) + `("` + fw.pathConvert(rt.Path) +
			`", handle` + rt.HandlerName + ")"
	}
}

// methodUpper is a no-op rename for clarity at framework call sites:
// gin/echo/mux register methods are upper-case ("GET"), not Pascal.
func methodUpper(m string) string { return m }

var frameworks = map[string]*framework{
	"chi": {
		name:              "chi",
		importLine:        `"github.com/go-chi/chi/v5"`,
		registerParamType: "chi.Router",
		pathConvert:       chiPath,
		registerCall:      stdRegisterCall(methodPascal),
		wrapperParams:     "w http.ResponseWriter, r *http.Request",
		wrapperRet:        "",
		earlyReturn:       "return",
		finalReturn:       "",
		pathParamExpr: func(n string) string {
			return `chi.URLParam(r, "` + n + `")`
		},
		writerExpr: "w",
		bodyExpr:   "r.Body",
		ctxExpr:    "r.Context()",
		queryExpr:  "r.URL.Query()",
	},

	"gin": {
		name:              "gin",
		importLine:        `"github.com/gin-gonic/gin"`,
		registerParamType: "*gin.Engine",
		// gin keeps goduct's :name path syntax verbatim — no conversion needed.
		pathConvert:   func(p string) string { return p },
		registerCall:  stdRegisterCall(methodUpper),
		wrapperParams: "c *gin.Context",
		wrapperRet:    "",
		earlyReturn:   "return",
		finalReturn:   "",
		pathParamExpr: func(n string) string {
			return `c.Param("` + n + `")`
		},
		writerExpr: "c.Writer",
		bodyExpr:   "c.Request.Body",
		ctxExpr:    "c.Request.Context()",
		queryExpr:  "c.Request.URL.Query()",
	},

	"mux": {
		name:              "mux",
		importLine:        "", // stdlib only; net/http already in the import block
		registerParamType: "*http.ServeMux",
		// mux (Go 1.22+) uses the same brace syntax as chi.
		pathConvert: chiPath,
		// mux differs: r.HandleFunc("METHOD /path", h), not r.<METHOD>(...).
		registerCall: func(fw *framework, rt ir.Route) string {
			return `r.HandleFunc("` + rt.Method + " " + fw.pathConvert(rt.Path) +
				`", handle` + rt.HandlerName + ")"
		},
		wrapperParams: "w http.ResponseWriter, r *http.Request",
		wrapperRet:    "",
		earlyReturn:   "return",
		finalReturn:   "",
		// Go 1.22+ adds *http.Request.PathValue for mux-pattern path params.
		pathParamExpr: func(n string) string {
			return `r.PathValue("` + n + `")`
		},
		writerExpr: "w",
		bodyExpr:   "r.Body",
		ctxExpr:    "r.Context()",
		queryExpr:  "r.URL.Query()",
	},

	"echo": {
		name:              "echo",
		importLine:        `"github.com/labstack/echo/v4"`,
		registerParamType: "*echo.Echo",
		// echo also keeps :name path syntax.
		pathConvert:   func(p string) string { return p },
		registerCall:  stdRegisterCall(methodUpper),
		wrapperParams: "c echo.Context",
		// echo handlers return error; the framework dispatches it through
		// its error handler. goduct still writes its own response via the
		// runtime helpers (so the wire format stays consistent), then
		// returns nil. earlyReturn returns nil; finalReturn appends the
		// trailing `return nil` before the closing brace.
		wrapperRet:  "error",
		earlyReturn: "return nil",
		finalReturn: "\treturn nil\n",
		pathParamExpr: func(n string) string {
			return `c.Param("` + n + `")`
		},
		writerExpr: "c.Response().Writer",
		bodyExpr:   "c.Request().Body",
		ctxExpr:    "c.Request().Context()",
		queryExpr:  "c.Request().URL.Query()",
	},
}

// SupportedFrameworks returns the framework names GenerateFramework
// accepts, in the canonical chi/gin/echo/mux order. Used by the CLI
// for usage help and pre-analysis validation of --framework.
func SupportedFrameworks() []string {
	return []string{"chi", "gin", "echo", "mux"}
}

// FrameworkSupported reports whether name is one of SupportedFrameworks.
// Cheap probe so the CLI can reject bad --framework values with exit 2
// before invoking the analyzer.
func FrameworkSupported(name string) bool {
	_, ok := frameworks[name]
	return ok
}

// Generate writes goduct_routes.go for api to w using the chi framework.
// This preserves the v0.1 generator entrypoint shape (ADR 0022 §1) and
// the v0.1 byte output for the default `goduct gen --go-adapter` form.
// Multi-framework callers use GenerateFramework directly.
func Generate(api *ir.API, w io.Writer) error {
	return GenerateFramework(api, w, "chi")
}

// GenerateFramework writes goduct_routes.go for api+framework to w,
// gofmt-clean (ADR 0022). frameworkName is one of "chi", "gin",
// "echo", "mux"; unknown returns an error so the CLI can map it to
// exit 2.
func GenerateFramework(api *ir.API, w io.Writer, frameworkName string) error {
	fw, ok := frameworks[frameworkName]
	if !ok {
		return fmt.Errorf("goadapter: unknown framework %q (want one of chi/gin/echo/mux)",
			frameworkName)
	}
	var b strings.Builder
	b.WriteString("// Code generated by goduct. DO NOT EDIT.\n\n")
	b.WriteString("package " + gen.PackageName(api) + "\n\n")
	b.WriteString(importBlock(api, fw) + "\n\n")

	b.WriteString(registerDoc + "\n")
	b.WriteString("func Register(r " + fw.registerParamType + ") {\n")
	for _, rt := range api.Routes {
		b.WriteString("\t" + fw.registerCall(fw, rt) + "\n")
	}
	b.WriteString("}\n")

	for _, rt := range api.Routes {
		b.WriteString("\n" + wrapper(fw, rt) + "\n")
	}

	src := b.String()
	out, err := format.Source([]byte(src))
	if err != nil {
		return fmt.Errorf("goadapter: go/format.Source failed (generator bug): %w\n---\n%s", err, src)
	}
	_, werr := w.Write(out)
	return werr
}

// importBlock builds the three-group import: stdlib (encoding/json iff a
// body route; net/http always; strconv iff a non-string query param),
// the framework's own import (skipped for stdlib mux), then the goduct
// runtime. go/format.Source sorts within groups.
func importBlock(api *ir.API, fw *framework) string {
	needJSON, needStrconv := false, false
	for _, rt := range api.Routes {
		if rt.BodyType != nil && rt.BodyType.Kind == ir.KindNamed {
			needJSON = true
		}
		for _, q := range rt.QueryParams {
			if q.Type.Kind != ir.KindBuiltin || q.Type.Builtin != "string" {
				needStrconv = true
			}
		}
	}
	var std []string
	if needJSON {
		std = append(std, `"encoding/json"`)
	}
	std = append(std, `"net/http"`)
	if needStrconv {
		std = append(std, `"strconv"`)
	}

	var b strings.Builder
	b.WriteString("import (\n")
	for _, s := range std {
		b.WriteString("\t" + s + "\n")
	}
	if fw.importLine != "" {
		b.WriteString("\n\t" + fw.importLine + "\n")
	}
	b.WriteString("\n\t" + goductRuntimeImport + "\n)")
	return b.String()
}

// wrapper renders one handle<Name> function for fw's framework.
// Field-assignment order is load-bearing: the JSON body is decoded
// BEFORE path params are applied, so a client cannot override a path
// param via the body. Do not reorder.
func wrapper(fw *framework, rt ir.Route) string {
	reqType := requestTypeName(rt)
	var b strings.Builder
	b.WriteString("func handle" + rt.HandlerName + "(" + fw.wrapperParams + ")")
	if fw.wrapperRet != "" {
		b.WriteString(" " + fw.wrapperRet)
	}
	b.WriteString(" {\n")
	b.WriteString("\tvar req " + reqType + "\n")

	if rt.BodyType != nil && rt.BodyType.Kind == ir.KindNamed {
		b.WriteString("\tif err := json.NewDecoder(" + fw.bodyExpr + ").Decode(&req); err != nil {\n")
		b.WriteString("\t\tgoduct.WriteError(" + fw.writerExpr + ", goduct.BadRequest(\"invalid json body\"))\n")
		b.WriteString("\t\t" + fw.earlyReturn + "\n\t}\n")
	}
	for _, p := range rt.PathParams { // after body decode (security)
		b.WriteString("\treq." + p.GoName + " = " + fw.pathParamExpr(p.WireName) + "\n")
	}
	if len(rt.QueryParams) > 0 {
		b.WriteString("\tq := " + fw.queryExpr + "\n")
		for _, p := range rt.QueryParams {
			b.WriteString(queryAssign(fw, p))
		}
	}

	hasResp := rt.ResponseType != nil && rt.ResponseType.Kind == ir.KindNamed
	if hasResp {
		b.WriteString("\tresp, err := " + rt.HandlerName + "(" + fw.ctxExpr + ", req)\n")
	} else {
		b.WriteString("\tif err := " + rt.HandlerName + "(" + fw.ctxExpr + ", req); err != nil {\n")
		b.WriteString("\t\tgoduct.WriteError(" + fw.writerExpr + ", err)\n\t\t" + fw.earlyReturn + "\n\t}\n")
		b.WriteString("\t" + successNoBody(fw, rt.SuccessStatus) + "\n")
		b.WriteString(fw.finalReturn)
		b.WriteString("}")
		return b.String()
	}
	b.WriteString("\tif err != nil {\n\t\tgoduct.WriteError(" + fw.writerExpr +
		", err)\n\t\t" + fw.earlyReturn + "\n\t}\n")
	b.WriteString("\tgoduct.WriteJSON(" + fw.writerExpr + ", " + statusConst(rt.SuccessStatus) + ", resp)\n")
	b.WriteString(fw.finalReturn)
	b.WriteString("}")
	return b.String()
}

// queryAssign emits the request-field assignment for one query param.
// string is the zero-value-friendly direct case; numeric/bool parse
// with a BadRequest on failure. Unhandled kinds panic (ADR 0022 §5).
func queryAssign(fw *framework, p ir.Param) string {
	if p.Type.Kind != ir.KindBuiltin {
		panic("goadapter: unsupported query param kind for " + p.WireName)
	}
	g, w := p.GoName, p.WireName
	switch p.Type.Builtin {
	case "string":
		return "\treq." + g + " = q.Get(\"" + w + "\")\n"
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64":
		return queryParseBlock(fw, g, w, "strconv.Atoi(v)", "an integer")
	case "bool":
		return queryParseBlock(fw, g, w, "strconv.ParseBool(v)", "a boolean")
	case "float32", "float64":
		return queryParseBlock(fw, g, w, "strconv.ParseFloat(v, 64)", "a number")
	}
	panic("goadapter: unsupported query builtin " + p.Type.Builtin + " for " + p.WireName)
}

func queryParseBlock(fw *framework, goName, wire, parse, want string) string {
	return "\tif v := q.Get(\"" + wire + "\"); v != \"\" {\n" +
		"\t\tn, err := " + parse + "\n" +
		"\t\tif err != nil {\n" +
		"\t\t\tgoduct.WriteError(" + fw.writerExpr + ", goduct.BadRequest(\"" + wire + " must be " + want + "\"))\n" +
		"\t\t\t" + fw.earlyReturn + "\n\t\t}\n" +
		"\t\treq." + goName + " = n\n\t}\n"
}

// requestTypeName returns the short name of the handler's request type.
// ADR 0027 guarantees ir.Route.RequestType is non-nil and KindNamed for
// every discovered route (DiscoverRoutes populates it from the handler's
// second parameter, which ADR 0014 pins as a named struct). A nil here
// is an analyzer/IR-invariant violation, surfaced as a loud panic per
// ADR 0022 §5 — not a user-facing error.
func requestTypeName(rt ir.Route) string {
	if rt.RequestType == nil || rt.RequestType.Kind != ir.KindNamed {
		panic("goduct: goadapter: ir.Route.RequestType is nil or non-Named for handler " +
			rt.HandlerName + " (ADR 0027 invariant violation)")
	}
	return shortName(rt.RequestType.Named)
}

func successNoBody(fw *framework, status int) string {
	if status == 204 {
		return fw.writerExpr + ".WriteHeader(http.StatusNoContent)"
	}
	return fw.writerExpr + ".WriteHeader(" + statusConst(status) + ")"
}

// statusConst maps a status int to its net/http constant. v0.1's
// analyzer only produces 200/201/204 (ADR 0014 defaults); anything else
// is unmapped — panic loudly (ADR 0022 §5; tracked in TODO.md).
func statusConst(code int) string {
	switch code {
	case 200:
		return "http.StatusOK"
	case 201:
		return "http.StatusCreated"
	case 204:
		return "http.StatusNoContent"
	}
	panic(fmt.Sprintf("goduct: goadapter has no net/http constant mapped for status %d "+
		"(v0.1 supports 200/201/204; see TODO.md)", code))
}

// methodPascal converts HTTP method names ("GET") to chi-style PascalCase
// identifiers ("Get") for `r.Get(...)` etc. Used by chi only — gin/echo
// keep the upper form.
func methodPascal(m string) string {
	if m == "" {
		return m
	}
	return strings.ToUpper(m[:1]) + strings.ToLower(m[1:])
}

// chiPath converts goduct's :name path syntax to chi's {name}.
// mux (Go 1.22+) uses the same brace syntax; gin and echo keep :name.
func chiPath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		if name, ok := strings.CutPrefix(s, ":"); ok && name != "" {
			segs[i] = "{" + name + "}"
		}
	}
	return strings.Join(segs, "/")
}

func shortName(qualified string) string {
	if i := strings.LastIndex(qualified, "."); i >= 0 {
		return qualified[i+1:]
	}
	return qualified
}
