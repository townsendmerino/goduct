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
	"strconv"
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
	// rawNeedsBridge is true when the framework's router takes a
	// non-(w, r) handler and raw routes must therefore be wrapped in
	// a context-bridge `handle<Name>` (ADR 0037). chi/mux: false
	// (router IS http.HandlerFunc). gin/echo: true.
	rawNeedsBridge bool
}

// stdRegisterCall: r.<MethodIdent>("<convertedPath>", <handlerRef>) —
// used by chi/gin/echo. mux overrides for its `r.HandleFunc("GET /x", h)`
// shape. handlerRef is "<HandlerName>" for raw routes on chi/mux (the
// user's function IS the http.HandlerFunc; ADR 0031) or "handle<Name>"
// for idiomatic routes everywhere AND raw routes on gin/echo (the
// generator emits a wrapper — full dispatch for idiomatic, a
// context-bridge for gin/echo raw per ADR 0037).
func stdRegisterCall(methodIdent func(string) string) func(*framework, ir.Route) string {
	return func(fw *framework, rt ir.Route) string {
		return "r." + methodIdent(rt.Method) + `("` + fw.pathConvert(rt.Path) +
			`", ` + handlerRef(fw, rt) + ")"
	}
}

// handlerRef is the identifier the Register call binds to a route.
// Raw routes on chi/mux register the user's http.HandlerFunc directly
// (their router signature is (w, r) natively); raw routes on gin/echo
// register the generated `handle<Name>` context-bridge wrapper
// (ADR 0037). Idiomatic routes always register the generated
// `handle<Name>` dispatch wrapper.
func handlerRef(fw *framework, rt ir.Route) string {
	if rt.Mode == ir.ModeRaw && !fw.rawNeedsBridge {
		return rt.HandlerName
	}
	return "handle" + rt.HandlerName
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
		writerExpr:     "c.Writer",
		bodyExpr:       "c.Request.Body",
		ctxExpr:        "c.Request.Context()",
		queryExpr:      "c.Request.URL.Query()",
		rawNeedsBridge: true,
	},

	"mux": {
		name:              "mux",
		importLine:        "", // stdlib only; net/http already in the import block
		registerParamType: "*http.ServeMux",
		// mux (Go 1.22+) uses the same brace syntax as chi.
		pathConvert: chiPath,
		// mux differs: r.HandleFunc("METHOD /path", h), not r.<METHOD>(...).
		// Raw routes (ADR 0031) reference the user's function directly.
		registerCall: func(fw *framework, rt ir.Route) string {
			return `r.HandleFunc("` + rt.Method + " " + fw.pathConvert(rt.Path) +
				`", ` + handlerRef(fw, rt) + ")"
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
		writerExpr:     "c.Response().Writer",
		bodyExpr:       "c.Request().Body",
		ctxExpr:        "c.Request().Context()",
		queryExpr:      "c.Request().URL.Query()",
		rawNeedsBridge: true,
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
//
// Raw-mode routes (ADR 0031): on chi/mux the user's function IS the
// http.HandlerFunc and Register references it directly. On gin/echo
// (ADR 0037) the user's `(w, r)` function is wrapped in a generated
// context-bridge `handle<Name>` that adapts c -> (w, r) and calls
// the user's handler.
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
		if rt.Mode == ir.ModeRaw {
			// chi/mux register the user's HandlerFunc directly (ADR 0031 §3).
			// gin/echo need a context-bridge wrapper (ADR 0037).
			if s := rawBridge(fw, rt); s != "" {
				b.WriteString("\n" + s + "\n")
			}
			continue
		}
		b.WriteString("\n" + wrapper(fw, rt, api) + "\n")
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
		// JSON body decode is the only encoding/json consumer; upload
		// routes have a BodyType but use multipart, not json.
		if !rt.Upload && rt.BodyType != nil && rt.BodyType.Kind == ir.KindNamed {
			needJSON = true
		}
		for _, q := range rt.QueryParams {
			if q.Type.Kind != ir.KindBuiltin || q.Type.Builtin != "string" {
				needStrconv = true
			}
		}
		// ADR 0042: form text fields with non-string primitives need
		// strconv too (uploadParseBlock emits strconv.Atoi / .ParseBool
		// / .ParseFloat).
		if rt.Upload && rt.BodyType != nil {
			td := api.Types[rt.BodyType.Named]
			for _, f := range gen.UploadFields(td) {
				if f.Source == ir.FieldSourceForm &&
					(f.Type.Kind != ir.KindBuiltin || f.Type.Builtin != "string") {
					needStrconv = true
				}
			}
		}
	}
	// ADR 0044: WebSocket routes need coder/websocket for the
	// Accept call in the generated wrapper. Tracked separately so
	// the third-party import group stays scoped to actual need.
	needWS := false
	for _, rt := range api.Routes {
		if rt.WebSocket != nil {
			needWS = true
			break
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
	// Third-party imports group: framework (per fw.importLine) +
	// coder/websocket when WS routes exist. gofmt/go/format.Source
	// sorts within this group.
	var thirdParty []string
	if fw.importLine != "" {
		thirdParty = append(thirdParty, fw.importLine)
	}
	if needWS {
		thirdParty = append(thirdParty, `"github.com/coder/websocket"`)
	}
	if len(thirdParty) > 0 {
		b.WriteString("\n")
		for _, s := range thirdParty {
			b.WriteString("\t" + s + "\n")
		}
	}
	b.WriteString("\n\t" + goductRuntimeImport + "\n)")
	return b.String()
}

// rawBridge renders one handle<Name> context-bridge wrapper for a raw
// route on gin/echo (ADR 0037). Returns "" for chi/mux (the user's
// function is registered directly; no wrapper is emitted). The bridge
// adapts the framework context to (w, r) and calls the user's raw
// handler — no body decode, no param assignment, no response writing
// (the raw handler owns the wire). Echo wants `error`; the bridge
// always returns nil (an http.HandlerFunc has no late-error signal).
func rawBridge(fw *framework, rt ir.Route) string {
	if !fw.rawNeedsBridge {
		return ""
	}
	var b strings.Builder
	b.WriteString("func handle" + rt.HandlerName + "(" + fw.wrapperParams + ")")
	if fw.wrapperRet != "" {
		b.WriteString(" " + fw.wrapperRet)
	}
	b.WriteString(" {\n")
	b.WriteString("\t" + rt.HandlerName + "(" + fw.writerExpr + ", " + rawRequestExpr(fw) + ")\n")
	b.WriteString(fw.finalReturn)
	b.WriteString("}")
	return b.String()
}

// rawRequestExpr is the *http.Request expression for fw's bridge.
// fw.bodyExpr gives the request body io.Reader (e.g. `c.Request.Body`);
// strip the trailing `.Body` to get the *http.Request itself, which is
// what the raw handler needs as its second argument.
func rawRequestExpr(fw *framework) string {
	return strings.TrimSuffix(fw.bodyExpr, ".Body")
}

// wrapper renders one handle<Name> function for fw's framework.
// Field-assignment order is load-bearing: the JSON body is decoded
// BEFORE path params are applied, so a client cannot override a path
// param via the body. Do not reorder.
func wrapper(fw *framework, rt ir.Route, api *ir.API) string {
	// ADR 0044: WebSocket routes take their own code path — upgrade
	// the connection via coder/websocket.Accept, construct a typed
	// goduct.WSConn, then call the user's handler.
	if rt.WebSocket != nil {
		return wsWrapper(fw, rt)
	}
	// ADR 0041: streaming routes take a different code path —
	// no body decode (streaming is always GET), no JSON response
	// write, delegate to goduct.SSEStream after setting headers.
	if rt.StreamType != nil {
		return streamWrapper(fw, rt)
	}

	reqType := requestTypeName(rt)
	var b strings.Builder
	b.WriteString("func handle" + rt.HandlerName + "(" + fw.wrapperParams + ")")
	if fw.wrapperRet != "" {
		b.WriteString(" " + fw.wrapperRet)
	}
	b.WriteString(" {\n")
	b.WriteString("\tvar req " + reqType + "\n")

	// ADR 0042: typed-upload routes replace the JSON body decode
	// with multipart parsing + per-field assignment. The JSON branch
	// below is mutually exclusive (the analyzer rejects json+multipart
	// on the same struct).
	if rt.Upload {
		b.WriteString(uploadAssigns(fw, rt, api))
	} else if rt.BodyType != nil && rt.BodyType.Kind == ir.KindNamed {
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

// uploadAssigns emits the ParseMultipartForm + per-field assignment
// block for a typed-upload route (ADR 0042 / 0043). The
// ParseMultipartForm cap is api.Meta.UploadMaxBytes when set
// (ADR 0043 §3), else the v0.6 default of 32 MiB. multipart:"..."
// file fields populate from <req>.MultipartForm.File; form:"..."
// text fields from <req>.MultipartForm.Value with strconv parsing
// for numeric / bool primitives (mirrors queryAssign).
func uploadAssigns(fw *framework, rt ir.Route, api *ir.API) string {
	reqExpr := rawRequestExpr(fw)
	maxBytes := "32 << 20"
	if api.Meta.UploadMaxBytes > 0 {
		maxBytes = strconv.FormatInt(api.Meta.UploadMaxBytes, 10)
	}
	var b strings.Builder
	b.WriteString("\tif err := " + reqExpr + ".ParseMultipartForm(" + maxBytes + "); err != nil {\n")
	b.WriteString("\t\tgoduct.WriteError(" + fw.writerExpr +
		", goduct.BadRequest(\"invalid multipart form\"))\n")
	b.WriteString("\t\t" + fw.earlyReturn + "\n\t}\n")

	// Look up the request type's full field set to find multipart/form
	// fields in source order. The Upload flag guarantees BodyType is
	// the request type itself (ADR 0042 §3).
	td := api.Types[rt.BodyType.Named]
	for _, f := range gen.UploadFields(td) {
		switch f.Source {
		case ir.FieldSourceMultipart:
			b.WriteString(uploadFileAssign(fw, f, reqExpr))
		case ir.FieldSourceForm:
			b.WriteString(uploadFormAssign(fw, f, reqExpr))
		}
	}
	return b.String()
}

// uploadFileAssign emits the assignment for one multipart file
// field — either a single *multipart.FileHeader (ADR 0042) or a
// []*multipart.FileHeader multi-file slice (ADR 0043). Single-file
// assigns files[0]; multi-file assigns the slice. Required fields
// short-circuit to BadRequest when absent. The maxbytes validator
// (ADR 0043 §4) emits a per-file size check after the assign.
func uploadFileAssign(fw *framework, f ir.Field, reqExpr string) string {
	var b strings.Builder
	wire := f.JSONName
	multi := f.Type.Kind == ir.KindSlice
	b.WriteString("\tif files := " + reqExpr + ".MultipartForm.File[\"" + wire + "\"]; len(files) > 0 {\n")
	if multi {
		b.WriteString("\t\treq." + f.GoName + " = files\n")
	} else {
		b.WriteString("\t\treq." + f.GoName + " = files[0]\n")
	}
	b.WriteString("\t}")
	if !f.Optional {
		b.WriteString(" else {\n")
		b.WriteString("\t\tgoduct.WriteError(" + fw.writerExpr +
			", goduct.BadRequest(\"" + wire + " is required\"))\n")
		b.WriteString("\t\t" + fw.earlyReturn + "\n\t}")
	}
	b.WriteString("\n")
	// ADR 0043 §4: maxbytes=N per-file enforcement. For multi-file
	// fields the check loops every uploaded file; for single-file
	// the assigned req.<F> is checked directly.
	for _, rule := range f.Validation {
		if rule.Name != "maxbytes" {
			continue
		}
		n := rule.Arg
		if multi {
			b.WriteString("\tfor _, fh := range req." + f.GoName + " {\n")
			b.WriteString("\t\tif fh.Size > " + n + " {\n")
			b.WriteString("\t\t\tgoduct.WriteError(" + fw.writerExpr +
				", goduct.BadRequest(\"" + wire + " exceeds " + n + " byte limit\"))\n")
			b.WriteString("\t\t\t" + fw.earlyReturn + "\n\t\t}\n\t}\n")
		} else {
			b.WriteString("\tif req." + f.GoName + " != nil && req." + f.GoName + ".Size > " + n + " {\n")
			b.WriteString("\t\tgoduct.WriteError(" + fw.writerExpr +
				", goduct.BadRequest(\"" + wire + " exceeds " + n + " byte limit\"))\n")
			b.WriteString("\t\t" + fw.earlyReturn + "\n\t}\n")
		}
	}
	return b.String()
}

// uploadFormAssign emits the assignment for one form:"..." text
// field. String is the direct case; numeric/bool parse with a
// BadRequest on failure. Same set of supported primitives as
// queryAssign — anything outside it is rejected by ParseStructField
// (UPL2 category).
func uploadFormAssign(fw *framework, f ir.Field, reqExpr string) string {
	if f.Type.Kind != ir.KindBuiltin {
		panic("goadapter: unsupported form param kind for " + f.JSONName)
	}
	wire, goName := f.JSONName, f.GoName
	switch f.Type.Builtin {
	case "string":
		var b strings.Builder
		b.WriteString("\tif vs := " + reqExpr + ".MultipartForm.Value[\"" + wire + "\"]; len(vs) > 0 {\n")
		b.WriteString("\t\treq." + goName + " = vs[0]\n")
		b.WriteString("\t}")
		if !f.Optional {
			b.WriteString(" else {\n")
			b.WriteString("\t\tgoduct.WriteError(" + fw.writerExpr +
				", goduct.BadRequest(\"" + wire + " is required\"))\n")
			b.WriteString("\t\t" + fw.earlyReturn + "\n\t}")
		}
		b.WriteString("\n")
		return b.String()
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64":
		return uploadParseBlock(fw, goName, wire, reqExpr, "strconv.Atoi(vs[0])", "an integer", f.Optional)
	case "bool":
		return uploadParseBlock(fw, goName, wire, reqExpr, "strconv.ParseBool(vs[0])", "a boolean", f.Optional)
	case "float32", "float64":
		return uploadParseBlock(fw, goName, wire, reqExpr, "strconv.ParseFloat(vs[0], 64)", "a number", f.Optional)
	}
	panic("goadapter: unsupported form builtin " + f.Type.Builtin + " for " + wire)
}

// uploadParseBlock emits an int/bool/float strconv parse block for a
// form text field. Pattern mirrors queryParseBlock.
func uploadParseBlock(fw *framework, goName, wire, reqExpr, parse, want string, optional bool) string {
	var b strings.Builder
	b.WriteString("\tif vs := " + reqExpr + ".MultipartForm.Value[\"" + wire + "\"]; len(vs) > 0 {\n")
	b.WriteString("\t\tn, err := " + parse + "\n")
	b.WriteString("\t\tif err != nil {\n")
	b.WriteString("\t\t\tgoduct.WriteError(" + fw.writerExpr +
		", goduct.BadRequest(\"" + wire + " must be " + want + "\"))\n")
	b.WriteString("\t\t\t" + fw.earlyReturn + "\n\t\t}\n")
	b.WriteString("\t\treq." + goName + " = n\n")
	b.WriteString("\t}")
	if !optional {
		b.WriteString(" else {\n")
		b.WriteString("\t\tgoduct.WriteError(" + fw.writerExpr +
			", goduct.BadRequest(\"" + wire + " is required\"))\n")
		b.WriteString("\t\t" + fw.earlyReturn + "\n\t}")
	}
	b.WriteString("\n")
	return b.String()
}

// wsWrapper renders the handle<Name> for a WebSocket route (ADR 0044).
// Shape: var req + path/query/header assignment, then websocket.Accept
// to upgrade the HTTP connection, then NewWSConn[Send, Recv] to wrap
// it, then call the user's handler. Error from Accept loud-fails as a
// BadRequest; the handler's error return is swallowed (the upgrade has
// already happened; we can't write an HTTP response after). CloseNow
// on defer guarantees cleanup if the handler returns without
// explicitly closing.
func wsWrapper(fw *framework, rt ir.Route) string {
	reqType := requestTypeName(rt)
	sendName := shortName(rt.WebSocket.Send.Named)
	recvName := shortName(rt.WebSocket.Recv.Named)
	var b strings.Builder
	b.WriteString("func handle" + rt.HandlerName + "(" + fw.wrapperParams + ")")
	if fw.wrapperRet != "" {
		b.WriteString(" " + fw.wrapperRet)
	}
	b.WriteString(" {\n")
	b.WriteString("\tvar req " + reqType + "\n")
	for _, p := range rt.PathParams {
		b.WriteString("\treq." + p.GoName + " = " + fw.pathParamExpr(p.WireName) + "\n")
	}
	if len(rt.QueryParams) > 0 {
		b.WriteString("\tq := " + fw.queryExpr + "\n")
		for _, p := range rt.QueryParams {
			b.WriteString(queryAssign(fw, p))
		}
	}
	b.WriteString("\tc, err := websocket.Accept(" + fw.writerExpr + ", " + rawRequestExpr(fw) + ", nil)\n")
	b.WriteString("\tif err != nil {\n\t\tgoduct.WriteError(" + fw.writerExpr +
		", goduct.BadRequest(\"websocket accept failed\"))\n\t\t" + fw.earlyReturn + "\n\t}\n")
	b.WriteString("\tdefer c.CloseNow()\n")
	b.WriteString("\t_ = " + rt.HandlerName + "(" + fw.ctxExpr + ", req, goduct.NewWSConn[" +
		sendName + ", " + recvName + "](c))\n")
	b.WriteString(fw.finalReturn)
	b.WriteString("}")
	return b.String()
}

// streamWrapper renders the handle<Name> for an SSE route (ADR 0041).
// Shape: var req + path + query assignment, then call the user's
// handler (which returns a receive-only channel + error), then on
// nil error set the SSE headers + delegate to goduct.SSEStream.
// Always GET in practice — body decode is skipped (streaming routes
// have no BodyType per the analyzer's signature recognition).
func streamWrapper(fw *framework, rt ir.Route) string {
	reqType := requestTypeName(rt)
	var b strings.Builder
	b.WriteString("func handle" + rt.HandlerName + "(" + fw.wrapperParams + ")")
	if fw.wrapperRet != "" {
		b.WriteString(" " + fw.wrapperRet)
	}
	b.WriteString(" {\n")
	b.WriteString("\tvar req " + reqType + "\n")
	for _, p := range rt.PathParams {
		b.WriteString("\treq." + p.GoName + " = " + fw.pathParamExpr(p.WireName) + "\n")
	}
	if len(rt.QueryParams) > 0 {
		b.WriteString("\tq := " + fw.queryExpr + "\n")
		for _, p := range rt.QueryParams {
			b.WriteString(queryAssign(fw, p))
		}
	}
	b.WriteString("\tch, err := " + rt.HandlerName + "(" + fw.ctxExpr + ", req)\n")
	b.WriteString("\tif err != nil {\n\t\tgoduct.WriteError(" + fw.writerExpr +
		", err)\n\t\t" + fw.earlyReturn + "\n\t}\n")
	b.WriteString("\t" + fw.writerExpr + ".Header().Set(\"Content-Type\", \"text/event-stream\")\n")
	b.WriteString("\t" + fw.writerExpr + ".Header().Set(\"Cache-Control\", \"no-cache\")\n")
	b.WriteString("\t" + fw.writerExpr + ".WriteHeader(" + statusConst(rt.SuccessStatus) + ")\n")
	b.WriteString("\tgoduct.SSEStream(" + fw.ctxExpr + ", " + fw.writerExpr + ", ch)\n")
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
