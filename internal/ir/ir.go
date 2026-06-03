// Package ir defines the intermediate representation that the analyzer
// produces and every generator consumes. It is the single contract that
// holds the project together: change it carefully.
package ir

import "time"

// API is the top-level result of analyzing a Go package (or set of packages).
type API struct {
	// Routes is every handler the analyzer found, in source order.
	Routes []Route

	// Types is every named type referenced (transitively) by any route's
	// request or response. Keyed by the type's qualified name (e.g.
	// "github.com/townsendmerino/goduct/examples/chi-basic/api.GetUserResponse").
	// Generators decide how to render the unqualified name in their output.
	Types map[string]TypeDef

	// SourceDirs maps each analyzed package's import path to its
	// filesystem directory. The Go adapter is written into the handlers'
	// own package directory (ADR 0009); this is how the CLI knows where.
	// For v0.1 single-package input this map has exactly one entry; the
	// shape is forward-compatible with v0.2 multi-package input. Added in
	// v0.2 per ADR 0027 (which removes the cmd/goduct Route.Pos-parsing
	// workaround).
	SourceDirs map[string]string

	// CustomAdapters maps a Go type's fully qualified name
	// (`<import-path>.<TypeName>`) to its JSON wire shape — one of
	// "string", "number", "boolean", "unknown". Populated by the
	// analyzer from LoadOptions.CustomAdapters; consumed by generators
	// to render adapted types per ADR 0032 (v0.2). Built-in special
	// types (ADR 0017) take precedence; an adapter declared on a
	// built-in qname is silently ignored.
	CustomAdapters map[string]string

	// Meta carries project-level metadata loaded from goduct.json
	// (ADR 0038). The analyzer does not populate Meta — the CLI sets
	// it after Analyze returns; generators that care (currently
	// openapi) read fields with empty-as-default semantics. Zero
	// value means "no config supplied; use built-in defaults".
	Meta Meta
}

// Meta is the project-config-derived metadata bag (ADR 0038). One
// flat struct; new fields are additive per ADR 0027. Empty-string /
// nil-slice fields are the sentinel for "no override"; a generator
// reading an empty value falls back to its own default.
type Meta struct {
	OpenAPITitle       string
	OpenAPIVersion     string
	OpenAPIDescription string
	OpenAPIServers     []string

	// Security carries goduct.json's "security.schemes" block
	// verbatim (ADR 0039). Each value is the OpenAPI 3.1
	// SecurityScheme shape — goduct does not validate the inner
	// structure; it emits it as-is under components.securitySchemes.
	Security map[string]any

	// SecurityRequirements is the document-level "security" array
	// (ADR 0039). Each map is one requirement entry, mapping a
	// scheme name to the (typically empty) scopes list.
	SecurityRequirements []map[string][]string

	// UploadMaxBytes is the cap passed to http.Request.ParseMultipartForm
	// in the generated typed-upload wrapper (ADR 0043). Zero means
	// "use the v0.6 default" of 32 << 20 (32 MiB). Sourced from
	// goduct.json's upload.maxBytes block; baked into the generated
	// adapter at codegen time, not read at runtime.
	UploadMaxBytes int64

	// WebSocketPingInterval, when non-zero, causes the generated
	// adapter to spawn a background ping goroutine on every accepted
	// WebSocket connection (ADR 0045 §2). Sourced from goduct.json's
	// websocket.pingInterval block (parsed via time.ParseDuration).
	// Zero / absent → no ping goroutine (the v0.6 default).
	WebSocketPingInterval time.Duration
}

// ErrorResponse is one `goduct:errorresponse <status> <Type>`
// declaration on a handler (ADR 0039). Type is always KindNamed —
// the analyzer resolves the name against the handler's package
// scope, same as goduct:request / goduct:response.
type ErrorResponse struct {
	Status int
	Type   *TypeRef
}

// SecurityRequirement is one OpenAPI operation-level security entry
// (ADR 0040). Empty Schemes (length 0) represents the "none" form
// — an explicit unauthenticated operation overriding the document
// default.
type SecurityRequirement struct {
	Schemes []string
}

// WebSocketTypes captures the two message-type ends of a WebSocket
// route (ADR 0044). Both are always KindNamed pointing at
// same-package named structs — Send is the server → client message
// type (what conn.Send takes on the Go side); Recv is the client →
// server message type (what conn.Recv returns).
type WebSocketTypes struct {
	Send *TypeRef
	Recv *TypeRef
}

// Route is one HTTP endpoint.
type Route struct {
	// HandlerName is the Go function's identifier (e.g. "GetUser").
	HandlerName string

	// Method is the uppercased HTTP method ("GET", "POST", ...).
	Method string

	// Path is the route pattern exactly as written in the annotation, using
	// colon-prefixed params (e.g. "/users/:id"). Generators translate to
	// their target router's syntax (the chi adapter, for example, emits
	// "/users/{id}").
	Path string

	// PathParams, QueryParams, HeaderParams are extracted from the request
	// struct's `path:`, `query:`, and `header:` tags respectively.
	PathParams   []Param
	QueryParams  []Param
	HeaderParams []Param

	// BodyType references the JSON body type (the remaining `json:`-tagged
	// fields of the request struct), or nil when the route has no body
	// (typically GET/DELETE, or when the request struct has no json fields).
	BodyType *TypeRef

	// RequestType is the handler's second-parameter type (T in
	// `func(ctx, T) ...`). Always non-nil for a discovered route — ADR 0014
	// guarantees idiomatic handlers have a named-struct request parameter.
	// For body routes, RequestType and BodyType point at the same named
	// type; for non-body routes (GET/DELETE/body-less POST) RequestType is
	// populated and BodyType is nil. Added in v0.2 per ADR 0027 (which
	// supersedes the ADR 0026 naming convention).
	RequestType *TypeRef

	// ResponseType references the response value's type. nil means the
	// handler returns no body (status 204) or is a streaming route
	// (StreamType is non-nil instead).
	ResponseType *TypeRef

	// WebSocket is non-nil iff this is a WebSocket route (ADR 0044) —
	// the handler signature is
	// func(ctx, T, *goduct.WSConn[S, C]) error and WebSocket carries
	// the Send/Recv message types (both KindNamed, same-package).
	// ResponseType / BodyType / StreamType all stay nil for WS routes;
	// generators that don't yet handle WS (openapi, postman, hooks)
	// see them as no-body routes and skip emission.
	WebSocket *WebSocketTypes

	// WebSocketSubprotocols is the ordered list of subprotocols the
	// route accepts (Sec-WebSocket-Protocol header). Populated from
	// repeated `goduct:wssubprotocol <name>` directives per ADR 0045.
	// nil/empty for routes that take the default subprotocol. Only
	// meaningful when WebSocket != nil.
	WebSocketSubprotocols []string

	// StreamType is non-nil iff this is an SSE route (ADR 0041) —
	// the handler signature is func(ctx, T) (<-chan E, error) and
	// StreamType points at E (always KindNamed; same-package named
	// struct per the detection rule). ResponseType is nil for
	// streaming routes; generators that don't yet handle streaming
	// see them as "no response body" and skip body emission.
	StreamType *TypeRef

	// Upload is true iff this route accepts multipart/form-data
	// (ADR 0042). Set by the analyzer in two ways: a typed handler
	// whose request struct has multipart:"..." tagged fields, or a
	// raw handler declaring `goduct:upload`. The flag drives every
	// non-Go generator (tsclient builds FormData, openapi emits
	// multipart/form-data content type, postman uses formdata body
	// mode). The Go adapter emits the multipart-parsing wrapper
	// only for typed uploads — raw uploads keep ADR 0031's direct-
	// register behavior.
	Upload bool

	// SuccessStatus is the HTTP status returned on a non-error result.
	// Defaults to 200, or 201 for POST, or 204 when ResponseType is nil.
	SuccessStatus int

	// Example is a raw JSON literal captured from a `goduct:example`
	// directive (ADR 0039). Empty when no example was declared. The
	// OpenAPI generator parses this once when emitting the operation's
	// success-response body; a malformed literal is a loud-fail at
	// generate time.
	Example string

	// ErrorResponses lists per-status alternative response bodies
	// declared via `goduct:errorresponse <status> <Type>` (ADR 0039).
	// Order preserved in source order. nil/empty for handlers that
	// declare none. The OpenAPI generator emits each as an additional
	// responses[<status>] entry.
	ErrorResponses []ErrorResponse

	// RequestExample is a raw JSON literal captured from a
	// `goduct:requestexample` directive (ADR 0040). Empty when
	// absent. Validated only at OpenAPI generate time; rendered as
	// requestBody.content."application/json".example. No-op when the
	// route has no body (GET/DELETE).
	RequestExample string

	// Security carries per-handler security requirements declared
	// via `goduct:security <name>` / `goduct:security none`
	// (ADR 0040). Each entry is one OpenAPI security requirement;
	// multiple entries compose as OR. nil/empty means the operation
	// inherits the document-level requirements from goduct.json
	// (the v0.4 default). The single-entry `none` form is
	// represented by SecurityRequirement{Schemes: nil} — an empty
	// OpenAPI requirement object that flags the operation as
	// explicitly unauthenticated.
	Security []SecurityRequirement

	// Tag groups routes in the generated client (e.g. all routes with
	// tag "users" become api.users.*). Defaults to the first path segment.
	Tag string

	// Doc is the godoc comment on the handler, with `goduct:` directives
	// stripped. Used for client-side JSDoc.
	Doc string

	// Mode records how this route was declared.
	Mode HandlerMode

	// Pos is "file:line" of the handler, for error messages.
	Pos string
}

// HandlerMode distinguishes the two supported handler styles.
type HandlerMode int

const (
	// ModeIdiomatic: func(ctx, Req) (*Resp, error)
	ModeIdiomatic HandlerMode = iota
	// ModeRaw: func(http.ResponseWriter, *http.Request) with annotations
	ModeRaw
)

// Param is one path/query/header parameter.
type Param struct {
	// GoName is the field name in the Go struct (e.g. "ID").
	GoName string
	// WireName is the name on the wire (from the tag, e.g. "id").
	WireName string
	// Type is the parameter's type. For path params this is always a
	// primitive; for query/header it may also be a slice of primitives.
	Type TypeRef
	// Optional is true if the parameter may be omitted (pointer type
	// or query/header with no `required` validation).
	Optional bool
	// Validation rules parsed from the `validate:` tag.
	Validation []ValidationRule
	// Doc is the field's godoc comment.
	Doc string
}

// TypeRef points at a type. Either Builtin is set OR Named is set.
// For slices and maps, Element / Key+Value carry the inner refs.
type TypeRef struct {
	Kind TypeKind

	// Builtin name when Kind == KindBuiltin (e.g. "string", "int", "bool",
	// "time.Time", "[]byte"). These are the names goduct understands; the
	// analyzer normalizes them.
	Builtin string

	// Named is the qualified name when Kind == KindNamed (looks up in API.Types).
	Named string

	// Element is the inner ref when Kind == KindSlice.
	Element *TypeRef

	// Key, Value are the inner refs when Kind == KindMap.
	Key, Value *TypeRef

	// TypeParam is the param's name when Kind == KindTypeParam — a
	// reference to a generic type parameter inside the generic's own
	// field list (e.g. the T in Page[T any]'s Items []T field). Added
	// in v0.3 per ADR 0033.
	TypeParam string

	// TypeArgs carries the concrete type arguments for a generic
	// instantiation. Non-empty only when Kind == KindNamed and Named
	// refers to a generic type. Position matches the named type's
	// TypeParams order. v0.3 per ADR 0033.
	TypeArgs []*TypeRef

	// UnionTerms is set when Kind == KindUnion (a generic type-param
	// constraint of the `T int | int64` shape). Order matches the
	// source declaration. v0.4 per ADR 0036.
	UnionTerms []*TypeRef

	// Optional means "may be absent / null". Set by pointer-ness or
	// omitempty at the use site.
	Optional bool
}

type TypeKind int

const (
	KindBuiltin TypeKind = iota
	KindNamed            // struct, enum, alias — see TypeDef.Kind
	KindSlice
	KindMap
	// KindTypeParam: a reference to a type parameter inside a generic
	// type's field list (the T in `[]T` inside Page[T any]'s body).
	// Carries the param name in TypeRef.TypeParam. v0.3 per ADR 0033.
	KindTypeParam
	// KindUnion: a type-union, used as a generic type-param constraint
	// in TypeDef.TypeParamConstraints. UnionTerms holds the individual
	// term TypeRefs in source order. v0.4 per ADR 0036.
	KindUnion
)

// TypeDef is a named user type that needs to be rendered in the output.
type TypeDef struct {
	// QualifiedName is the unique key (matches Named in TypeRef).
	QualifiedName string
	// Name is the unqualified identifier ("GetUserResponse").
	Name string
	// Kind: struct, enum, or alias.
	Kind TypeDefKind

	// Fields is populated when Kind == TypeStruct.
	Fields []Field

	// EnumValues is populated when Kind == TypeEnum.
	// The Go pattern detected is: `type Status string` plus typed string
	// constants. The underlying primitive is in Underlying.
	EnumValues []EnumValue
	Underlying string // "string" or "int" for enums

	// AliasTo is populated when Kind == TypeAlias.
	AliasTo *TypeRef

	// TypeParams names the generic type parameters declared on this
	// type (e.g. ["T"] for Page[T], ["K","V"] for Map[K,V]).
	// nil/empty for non-generic types. Order matches the source
	// declaration. v0.3 per ADR 0033.
	TypeParams []string

	// TypeParamConstraints is parallel-indexed to TypeParams: the
	// constraint applying to TypeParams[i] is TypeParamConstraints[i],
	// or nil for an `any` constraint. A single-term constraint is a
	// bare TypeRef (e.g. {Kind: KindBuiltin, Builtin: "int"}); a
	// multi-term union is {Kind: KindUnion, UnionTerms: [...]}. v0.4
	// per ADR 0036.
	TypeParamConstraints []*TypeRef

	Doc string
	Pos string
}

type TypeDefKind int

const (
	TypeStruct TypeDefKind = iota
	TypeEnum
	TypeAlias
)

// Field is one struct field.
type Field struct {
	GoName     string
	JSONName   string // from `json:` tag; "-" means skipped (excluded upstream)
	Type       TypeRef
	Optional   bool             // pointer or `omitempty`
	Validation []ValidationRule // from `validate:` tag
	Doc        string
	// Source is where this field's value comes from on the wire. Set by
	// the analyzer from the field's tag. Zero value is FieldSourceJSON, so
	// non-request types (response/nested) need no special handling.
	Source FieldSource
}

// FieldSource is where a struct field's value comes from. For request
// types it is set per the field's path/query/header/json tag; for all
// other (response/nested) types every field is FieldSourceJSON or
// FieldSourceNone, and a path/query/header tag on such a type is a load
// error.
type FieldSource int

const (
	FieldSourceJSON      FieldSource = iota // wire body (default for non-request types)
	FieldSourcePath                         // URL path
	FieldSourceQuery                        // URL query string
	FieldSourceHeader                       // HTTP header
	FieldSourceNone                         // untagged; not on the wire
	FieldSourceMultipart                    // multipart/form-data file part (ADR 0042)
	FieldSourceForm                         // multipart/form-data text part (ADR 0042)
)

// EnumValue is one constant of an enum-style type.
type EnumValue struct {
	GoName string // "StatusActive"
	Value  string // "active" — for string enums; stringified for int enums
	Doc    string
}

// ValidationRule is one parsed validate tag entry.
// Examples: {Name:"required"}, {Name:"min", Arg:"3"}, {Name:"oneof", Arg:"a b c"}.
type ValidationRule struct {
	Name string
	Arg  string
}
