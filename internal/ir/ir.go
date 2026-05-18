// Package ir defines the intermediate representation that the analyzer
// produces and every generator consumes. It is the single contract that
// holds the project together: change it carefully.
package ir

// API is the top-level result of analyzing a Go package (or set of packages).
type API struct {
	// Routes is every handler the analyzer found, in source order.
	Routes []Route

	// Types is every named type referenced (transitively) by any route's
	// request or response. Keyed by the type's qualified name (e.g.
	// "github.com/townsendmerino/goduct/examples/chi-basic/api.GetUserResponse").
	// Generators decide how to render the unqualified name in their output.
	Types map[string]TypeDef
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

	// ResponseType references the response value's type. nil means the
	// handler returns no body (status 204).
	ResponseType *TypeRef

	// SuccessStatus is the HTTP status returned on a non-error result.
	// Defaults to 200, or 201 for POST, or 204 when ResponseType is nil.
	SuccessStatus int

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
	FieldSourceJSON   FieldSource = iota // wire body (default for non-request types)
	FieldSourcePath                      // URL path
	FieldSourceQuery                     // URL query string
	FieldSourceHeader                    // HTTP header
	FieldSourceNone                      // untagged; not on the wire
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
