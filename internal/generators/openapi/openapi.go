// Package openapi generates openapi.json: an OpenAPI 3.1 document
// describing the analyzed API. See ADR 0034 for the design pins
// (single-file JSON, generics flattened per-instantiation as
// Page_User-style components, synthesized GoductError component
// referenced by every operation's `default` response, alphabetical
// ordering everywhere except properties / HTTP-method keys which use
// source order / canonical OpenAPI order respectively).
//
// The generator emits a Go struct hierarchy via encoding/json and
// then runs the result through json.Indent for pretty-printing.
// Two types implement json.Marshaler:
//   - pathItem uses Go struct-field declaration order so HTTP
//     methods serialize in OpenAPI's canonical sequence (get / put /
//     post / delete / options / head / patch / trace).
//   - orderedProps preserves source declaration order for struct
//     properties (regular Go map alphabetizes them, which loses the
//     wire-order signal the TS generators all keep).
package openapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/townsendmerino/goduct/internal/gen"
	"github.com/townsendmerino/goduct/internal/ir"
)

// Generate writes openapi.json for api to w (ADR 0034 §1). Reads
// project metadata from api.Meta when present, falling back to the
// pre-config defaults otherwise (ADR 0038 §5). Returns w's first
// error or nil.
func Generate(api *ir.API, w io.Writer) error {
	title := api.Meta.OpenAPITitle
	if title == "" {
		title = gen.PackageName(api)
	}
	version := api.Meta.OpenAPIVersion
	if version == "" {
		version = "0.0.0"
	}
	paths, err := buildPaths(api)
	if err != nil {
		return err
	}
	doc := document{
		OpenAPI: "3.1.0",
		Info: info{
			Title:       title,
			Version:     version,
			Description: api.Meta.OpenAPIDescription,
		},
		Paths:      paths,
		Components: components{Schemas: buildSchemas(api)},
	}
	for _, url := range api.Meta.OpenAPIServers {
		doc.Servers = append(doc.Servers, server{URL: url})
	}
	// ADR 0039: security schemes + document-level requirements.
	if len(api.Meta.Security) > 0 {
		doc.Components.SecuritySchemes = api.Meta.Security
	}
	if len(api.Meta.SecurityRequirements) > 0 {
		doc.Security = api.Meta.SecurityRequirements
	}

	var compact bytes.Buffer
	enc := json.NewEncoder(&compact)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(doc); err != nil {
		return fmt.Errorf("openapi: encode: %w", err)
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, compact.Bytes(), "", "  "); err != nil {
		return fmt.Errorf("openapi: indent: %w", err)
	}
	_, werr := w.Write(pretty.Bytes())
	return werr
}

// ----------------------------------------------------------------
// Document hierarchy (matches the OpenAPI 3.1 shape goduct emits)
// ----------------------------------------------------------------

type document struct {
	OpenAPI    string                  `json:"openapi"`
	Info       info                    `json:"info"`
	Servers    []server                `json:"servers,omitempty"`
	Paths      map[string]*pathItem    `json:"paths"`
	Components components              `json:"components"`
	Security   []map[string][]string   `json:"security,omitempty"`
}

type info struct {
	Title       string `json:"title"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
}

type server struct {
	URL string `json:"url"`
}

// pathItem's fields are declared in OpenAPI's canonical HTTP-method
// order so encoding/json emits them in that order naturally — no
// custom MarshalJSON needed.
type pathItem struct {
	Get     *operation `json:"get,omitempty"`
	Put     *operation `json:"put,omitempty"`
	Post    *operation `json:"post,omitempty"`
	Delete  *operation `json:"delete,omitempty"`
	Options *operation `json:"options,omitempty"`
	Head    *operation `json:"head,omitempty"`
	Patch   *operation `json:"patch,omitempty"`
	Trace   *operation `json:"trace,omitempty"`
}

type operation struct {
	Tags        []string                `json:"tags,omitempty"`
	Summary     string                  `json:"summary,omitempty"`
	Description string                  `json:"description,omitempty"`
	OperationID string                  `json:"operationId,omitempty"`
	Parameters  []parameter             `json:"parameters,omitempty"`
	RequestBody *requestBody            `json:"requestBody,omitempty"`
	Responses   map[string]*response    `json:"responses"`
	// Security is omitempty: nil/empty means the operation inherits
	// the document-level requirements per ADR 0040. An empty slice
	// is NOT the same as the `none` form — that is represented by
	// a single requirement with an empty inner map.
	Security    []map[string][]string `json:"security,omitempty"`
}

type parameter struct {
	Name        string         `json:"name"`
	In          string         `json:"in"`
	Required    bool           `json:"required,omitempty"`
	Description string         `json:"description,omitempty"`
	Schema      map[string]any `json:"schema"`
}

type requestBody struct {
	Required bool                  `json:"required,omitempty"`
	Content  map[string]*mediaType `json:"content"`
}

type response struct {
	Description string                `json:"description"`
	Content     map[string]*mediaType `json:"content,omitempty"`
}

type mediaType struct {
	Schema  map[string]any `json:"schema"`
	Example any            `json:"example,omitempty"`
}

type components struct {
	Schemas         map[string]any `json:"schemas"`
	SecuritySchemes map[string]any `json:"securitySchemes,omitempty"`
}

// orderedProps preserves source-declaration order of schema
// properties. Built-in encoding/json sorts map keys alphabetically,
// which loses the JSON-wire field order — orderedProps emits in the
// order keys were appended.
type orderedProps struct {
	keys   []string
	values []map[string]any
}

func (o orderedProps) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		b.Write(kb)
		b.WriteByte(':')
		vb, err := json.Marshal(o.values[i])
		if err != nil {
			return nil, err
		}
		b.Write(vb)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// ----------------------------------------------------------------
// Paths + operations
// ----------------------------------------------------------------

func buildPaths(api *ir.API) (map[string]*pathItem, error) {
	out := map[string]*pathItem{}
	for _, r := range api.Routes {
		p := pathConvert(r.Path)
		item := out[p]
		if item == nil {
			item = &pathItem{}
			out[p] = item
		}
		op, err := buildOperation(api, r)
		if err != nil {
			return nil, err
		}
		setMethod(item, r.Method, op)
	}
	return out, nil
}

// pathConvert converts goduct's :name path params to OpenAPI's {name}
// brace syntax. Same shape chi/mux's adapter uses (ADR 0034 §3).
func pathConvert(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		if name, ok := strings.CutPrefix(s, ":"); ok && name != "" {
			segs[i] = "{" + name + "}"
		}
	}
	return strings.Join(segs, "/")
}

// setMethod assigns op to the pathItem's slot for method m. Unknown
// methods are an analyzer-invariant violation (ADR 0014 restricts to
// the standard set); panic loudly.
func setMethod(p *pathItem, m string, op *operation) {
	switch m {
	case "GET":
		p.Get = op
	case "PUT":
		p.Put = op
	case "POST":
		p.Post = op
	case "DELETE":
		p.Delete = op
	case "OPTIONS":
		p.Options = op
	case "HEAD":
		p.Head = op
	case "PATCH":
		p.Patch = op
	case "TRACE":
		p.Trace = op
	default:
		panic("openapi: unsupported HTTP method " + m)
	}
}

func buildOperation(api *ir.API, r ir.Route) (*operation, error) {
	op := &operation{
		OperationID: r.HandlerName,
		Tags:        []string{r.Tag},
	}
	if s := gen.JSDoc(r.HandlerName, r.Doc); s != "" {
		op.Summary = s
	}
	if d := gen.JSDocFull(r.HandlerName, r.Doc); d != "" && d != op.Summary {
		op.Description = d
	}

	// Parameters: path, then query, then header — each in declaration order.
	for _, p := range r.PathParams {
		op.Parameters = append(op.Parameters, parameter{
			Name: p.WireName, In: "path", Required: true,
			Schema: schemaForRef(api, p.Type),
		})
	}
	for _, q := range r.QueryParams {
		op.Parameters = append(op.Parameters, parameter{
			Name: q.WireName, In: "query", Required: !q.Optional,
			Schema: schemaForRef(api, q.Type),
		})
	}
	for _, h := range r.HeaderParams {
		op.Parameters = append(op.Parameters, parameter{
			Name: h.WireName, In: "header", Required: !h.Optional,
			Schema: schemaForRef(api, h.Type),
		})
	}

	// Request body (when a body route). ADR 0040: optionally attach
	// goduct:requestexample to the body's mediaType.
	if r.BodyType != nil {
		mt := &mediaType{Schema: schemaForRef(api, *r.BodyType)}
		if r.RequestExample != "" {
			var ex any
			if err := json.Unmarshal([]byte(r.RequestExample), &ex); err != nil {
				return nil, fmt.Errorf("openapi: handler %s: goduct:requestexample is not valid JSON: %w",
					r.HandlerName, err)
			}
			mt.Example = ex
		}
		op.RequestBody = &requestBody{
			Required: true,
			Content:  map[string]*mediaType{"application/json": mt},
		}
	}

	// Responses: success status + synthesized `default` -> GoductError.
	op.Responses = map[string]*response{}
	key := fmt.Sprintf("%d", r.SuccessStatus)
	if r.ResponseType != nil {
		mt := &mediaType{Schema: schemaForRef(api, *r.ResponseType)}
		// ADR 0039: attach goduct:example to the success response body.
		// Parse once into `any` so the JSON encoder emits the value
		// rather than a string-quoted blob.
		if r.Example != "" {
			var ex any
			if err := json.Unmarshal([]byte(r.Example), &ex); err != nil {
				return nil, fmt.Errorf("openapi: handler %s: goduct:example is not valid JSON: %w",
					r.HandlerName, err)
			}
			mt.Example = ex
		}
		op.Responses[key] = &response{
			Description: "OK",
			Content:     map[string]*mediaType{"application/json": mt},
		}
	} else {
		desc := "OK"
		if r.SuccessStatus == 204 {
			desc = "No Content"
		}
		op.Responses[key] = &response{Description: desc}
	}
	// ADR 0039: emit per-status error responses declared via
	// goduct:errorresponse. Each takes precedence over the synthesized
	// `default` for that specific status (the default still applies to
	// any status the route doesn't explicitly declare).
	for _, er := range r.ErrorResponses {
		if er.Type == nil {
			continue
		}
		op.Responses[fmt.Sprintf("%d", er.Status)] = &response{
			Description: "Error",
			Content: map[string]*mediaType{
				"application/json": {Schema: schemaForRef(api, *er.Type)},
			},
		}
	}
	op.Responses["default"] = &response{
		Description: "Error",
		Content: map[string]*mediaType{
			"application/json": {Schema: map[string]any{
				"$ref": "#/components/schemas/GoductError",
			}},
		},
	}

	// ADR 0040: per-handler security overrides. Empty Schemes
	// becomes an empty inner map (the OpenAPI 3.1 `none` form);
	// a non-empty Schemes becomes a single-key map with an empty
	// scopes list (scopes deferred to v0.5).
	for _, req := range r.Security {
		entry := map[string][]string{}
		for _, scheme := range req.Schemes {
			entry[scheme] = []string{}
		}
		op.Security = append(op.Security, entry)
	}

	return op, nil
}

// ----------------------------------------------------------------
// Schemas
// ----------------------------------------------------------------

func buildSchemas(api *ir.API) map[string]any {
	out := map[string]any{}

	// Non-generic TypeDefs. Generic origins (TypeParams != nil) are
	// NOT emitted as standalone components — they're meaningless in
	// OpenAPI without args. Per-instantiation schemas below cover the
	// concrete uses (ADR 0034 §6). Empty-wire request types (no
	// JSON-visible fields — all path/query/header) are also skipped,
	// mirroring gen.EmitTS: there's no body schema to describe and no
	// operation references them as `$ref`.
	for _, td := range api.Types {
		if len(td.TypeParams) > 0 {
			continue
		}
		if !gen.EmitTS(td) {
			continue
		}
		out[td.Name] = schemaForTypeDef(api, td)
	}

	// Every distinct generic instantiation reachable from the IR.
	for _, inst := range collectInstantiations(api) {
		name := instantiationName(inst.qname, inst.args)
		if _, ok := out[name]; ok {
			continue
		}
		out[name] = substitutedSchema(api, inst.qname, inst.args)
	}

	// Synthesized GoductError component (ADR 0034 §7).
	out["GoductError"] = map[string]any{
		"type":     "object",
		"required": []string{"code", "message"},
		"properties": orderedProps{
			keys: []string{"code", "message", "details"},
			values: []map[string]any{
				{"type": "string"},
				{"type": "string"},
				{},
			},
		},
	}
	return out
}

// schemaForTypeDef builds the schema for a non-generic TypeDef.
// Generic origins must never reach this function — buildSchemas
// filters them upstream.
func schemaForTypeDef(api *ir.API, td ir.TypeDef) map[string]any {
	switch td.Kind {
	case ir.TypeStruct:
		return structSchema(api, td.Fields, nil)
	case ir.TypeEnum:
		return enumSchema(td)
	case ir.TypeAlias:
		return schemaForRef(api, *td.AliasTo)
	}
	panic("openapi: unhandled TypeDefKind for " + td.Name)
}

// structSchema renders a struct's schema, optionally substituting
// type-params via subst (used when emitting a generic instantiation).
func structSchema(api *ir.API, fields []ir.Field, subst map[string]ir.TypeRef) map[string]any {
	var required []string
	props := orderedProps{}
	for _, f := range fields {
		if f.Source != ir.FieldSourceJSON {
			continue
		}
		ref := f.Type
		if subst != nil {
			ref = substTypeRef(ref, subst)
		}
		schema := schemaForRef(api, ref)
		applyValidators(schema, f, ref)
		props.keys = append(props.keys, f.JSONName)
		props.values = append(props.values, schema)
		if !f.Optional {
			required = append(required, f.JSONName)
		}
	}
	sort.Strings(required)
	out := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}

// enumSchema renders a TypeEnum: string-typed enums get type:string +
// enum:[...]; int-typed enums get type:integer + enum:[...].
func enumSchema(td ir.TypeDef) map[string]any {
	vals := make([]any, len(td.EnumValues))
	if td.Underlying == "int" {
		for i, ev := range td.EnumValues {
			// IR stores enum values as decimal strings; OpenAPI needs
			// numbers. Best-effort: parse as int64; fall back to string.
			var n int64
			if _, err := fmt.Sscan(ev.Value, &n); err == nil {
				vals[i] = n
				continue
			}
			vals[i] = ev.Value
		}
		return map[string]any{"type": "integer", "enum": vals}
	}
	for i, ev := range td.EnumValues {
		vals[i] = ev.Value
	}
	return map[string]any{"type": "string", "enum": vals}
}

// schemaForRef builds the JSON-schema fragment for any TypeRef.
// Named refs become $ref entries pointing into components/schemas;
// slices/maps recurse; builtins map to the wire-shape table; custom
// adapters (ADR 0032) consult api.CustomAdapters.
func schemaForRef(api *ir.API, ref ir.TypeRef) map[string]any {
	switch ref.Kind {
	case ir.KindBuiltin:
		return builtinSchema(api, ref.Builtin)
	case ir.KindNamed:
		name := short(ref.Named)
		if len(ref.TypeArgs) > 0 {
			name = instantiationName(ref.Named, ref.TypeArgs)
		}
		return map[string]any{"$ref": "#/components/schemas/" + name}
	case ir.KindSlice:
		return map[string]any{
			"type":  "array",
			"items": schemaForRef(api, *ref.Element),
		}
	case ir.KindMap:
		return map[string]any{
			"type":                 "object",
			"additionalProperties": schemaForRef(api, *ref.Value),
		}
	case ir.KindTypeParam:
		// Reached only when substitution failed upstream — invariant
		// violation, not a user error (ADR 0022 §5).
		panic("openapi: KindTypeParam (" + ref.TypeParam +
			") reached schemaForRef; substTypeRef should have replaced it")
	}
	panic("openapi: unhandled TypeRef kind")
}

// builtinSchema maps an IR builtin name to its JSON Schema. ADR 0017
// types + the ADR 0032 wire-shape table for user adapters; unknown
// builtin = analyzer-invariant violation (ADR 0022 §5).
func builtinSchema(api *ir.API, b string) map[string]any {
	switch b {
	case "string":
		return map[string]any{"type": "string"}
	case "bool":
		return map[string]any{"type": "boolean"}
	case "int", "int8", "int16", "int32",
		"uint", "uint8", "uint16", "uint32":
		return map[string]any{"type": "integer", "format": "int32"}
	case "int64", "uint64", "time.Duration":
		return map[string]any{"type": "integer", "format": "int64"}
	case "float32":
		return map[string]any{"type": "number", "format": "float"}
	case "float64":
		return map[string]any{"type": "number", "format": "double"}
	case "time.Time":
		return map[string]any{"type": "string", "format": "date-time"}
	case "[]byte":
		return map[string]any{"type": "string", "format": "byte"}
	case "uuid.UUID":
		return map[string]any{"type": "string", "format": "uuid"}
	case "json.RawMessage":
		return map[string]any{} // JSON Schema "any value"
	}
	// ADR 0032: custom adapter fall-through.
	if wire, ok := api.CustomAdapters[b]; ok {
		return wireToSchema(wire)
	}
	panic("openapi: unknown builtin " + b)
}

// wireToSchema maps an ADR 0032 wire shape to its OpenAPI / JSON
// Schema form (parallel to gen.AdapterWireTS / gen.AdapterWireZod
// but in JSON-Schema vocabulary).
func wireToSchema(wire string) map[string]any {
	switch wire {
	case "string":
		return map[string]any{"type": "string"}
	case "number":
		return map[string]any{"type": "number"}
	case "boolean":
		return map[string]any{"type": "boolean"}
	case "unknown":
		return map[string]any{}
	}
	panic("openapi: unknown wire shape " + wire +
		" (CLI should have validated this)")
}

// applyValidators decorates a schema with JSON Schema keywords for
// each ir.ValidationRule. `min`/`max` are type-sensitive: string
// fields get minLength/maxLength; numeric fields get
// minimum/maximum. `oneof` REPLACES the base schema with an enum
// (per ADR 0006 v0.2 §Empirical-finding, mirroring zod).
func applyValidators(schema map[string]any, f ir.Field, ref ir.TypeRef) {
	isString := false
	if ref.Kind == ir.KindBuiltin {
		switch ref.Builtin {
		case "string", "time.Time", "[]byte", "uuid.UUID":
			isString = true
		}
	}
	for _, r := range f.Validation {
		switch r.Name {
		case "email":
			schema["format"] = "email"
		case "url":
			schema["format"] = "uri"
		case "min":
			if isString {
				schema["minLength"] = atoiOrZero(r.Arg)
			} else {
				schema["minimum"] = atoiOrZero(r.Arg)
			}
		case "max":
			if isString {
				schema["maxLength"] = atoiOrZero(r.Arg)
			} else {
				schema["maximum"] = atoiOrZero(r.Arg)
			}
		case "len":
			schema["minLength"] = atoiOrZero(r.Arg)
			schema["maxLength"] = atoiOrZero(r.Arg)
		case "oneof":
			if isString {
				parts := strings.Fields(r.Arg)
				vals := make([]any, len(parts))
				for i, p := range parts {
					vals[i] = p
				}
				// oneof replaces, doesn't chain — clear any inherited
				// keyword (we still want "type":"string" + "enum":[...]).
				for k := range schema {
					if k == "type" {
						continue
					}
					delete(schema, k)
				}
				schema["enum"] = vals
			}
		}
	}
}

func atoiOrZero(s string) int {
	var n int
	_, _ = fmt.Sscan(s, &n)
	return n
}

// ----------------------------------------------------------------
// Generics: collect instantiations + substitute type params
// ----------------------------------------------------------------

type instantiation struct {
	qname string
	args  []*ir.TypeRef
}

// collectInstantiations walks every TypeRef reachable from routes
// and types, collecting distinct generic instantiations. Nested
// instantiations are reached recursively (Page[Result[User, Err]]
// produces Page_Result_User_Err, Result_User_Err, and User / Err as
// names — the args' nested-named refs are non-generic and use
// schemaForRef's regular $ref path).
func collectInstantiations(api *ir.API) []instantiation {
	seen := map[string]bool{}
	var out []instantiation

	var visit func(ir.TypeRef)
	visit = func(ref ir.TypeRef) {
		switch ref.Kind {
		case ir.KindNamed:
			if len(ref.TypeArgs) > 0 {
				key := instantiationName(ref.Named, ref.TypeArgs)
				if !seen[key] {
					seen[key] = true
					out = append(out, instantiation{ref.Named, ref.TypeArgs})
				}
				for _, ta := range ref.TypeArgs {
					if ta != nil {
						visit(*ta)
					}
				}
			}
		case ir.KindSlice:
			if ref.Element != nil {
				visit(*ref.Element)
			}
		case ir.KindMap:
			if ref.Key != nil {
				visit(*ref.Key)
			}
			if ref.Value != nil {
				visit(*ref.Value)
			}
		}
	}

	for _, r := range api.Routes {
		for _, x := range []*ir.TypeRef{r.RequestType, r.BodyType, r.ResponseType} {
			if x != nil {
				visit(*x)
			}
		}
	}
	for _, td := range api.Types {
		for _, f := range td.Fields {
			visit(f.Type)
		}
		if td.AliasTo != nil {
			visit(*td.AliasTo)
		}
	}
	return out
}

// substitutedSchema builds the schema for an instantiation by
// substituting the generic origin's type-params with the concrete
// args. The origin's struct shape is preserved; fields with
// KindTypeParam refs are rewritten in-place per the substitution.
func substitutedSchema(api *ir.API, originQname string, args []*ir.TypeRef) map[string]any {
	origin, ok := api.Types[originQname]
	if !ok {
		panic("openapi: missing generic origin " + originQname)
	}
	if len(args) != len(origin.TypeParams) {
		panic(fmt.Sprintf("openapi: generic %s instantiation arity %d != declared %d",
			originQname, len(args), len(origin.TypeParams)))
	}
	subst := make(map[string]ir.TypeRef, len(args))
	for i, p := range origin.TypeParams {
		subst[p] = *args[i]
	}
	return structSchema(api, origin.Fields, subst)
}

// substTypeRef returns ref with KindTypeParam leaves replaced per
// subst. Recursive: KindSlice/KindMap/KindNamed-with-TypeArgs descend
// into their inner refs and substitute there too.
func substTypeRef(ref ir.TypeRef, subst map[string]ir.TypeRef) ir.TypeRef {
	if ref.Kind == ir.KindTypeParam {
		if r, ok := subst[ref.TypeParam]; ok {
			return r
		}
		return ref
	}
	if ref.Element != nil {
		e := substTypeRef(*ref.Element, subst)
		ref.Element = &e
	}
	if ref.Key != nil {
		k := substTypeRef(*ref.Key, subst)
		ref.Key = &k
	}
	if ref.Value != nil {
		v := substTypeRef(*ref.Value, subst)
		ref.Value = &v
	}
	if len(ref.TypeArgs) > 0 {
		newArgs := make([]*ir.TypeRef, len(ref.TypeArgs))
		for i, ta := range ref.TypeArgs {
			s := substTypeRef(*ta, subst)
			newArgs[i] = &s
		}
		ref.TypeArgs = newArgs
	}
	return ref
}

// instantiationName builds the underscore-joined component name for
// a generic instantiation (ADR 0034 §6). Page[User] -> "Page_User";
// Result[User, Err] -> "Result_User_Err"; Page[Result[User, Err]] ->
// "Page_Result_User_Err".
func instantiationName(originQname string, args []*ir.TypeRef) string {
	var b strings.Builder
	b.WriteString(short(originQname))
	for _, ta := range args {
		b.WriteByte('_')
		b.WriteString(argName(*ta))
	}
	return b.String()
}

// argName renders a single TypeArg's contribution to the
// instantiation name. Nested instantiations recurse; builtins use
// their short form; slices/maps compress to "List"/"Map" prefixed
// names.
func argName(ref ir.TypeRef) string {
	switch ref.Kind {
	case ir.KindNamed:
		n := short(ref.Named)
		if len(ref.TypeArgs) == 0 {
			return n
		}
		var b strings.Builder
		b.WriteString(n)
		for _, ta := range ref.TypeArgs {
			b.WriteByte('_')
			b.WriteString(argName(*ta))
		}
		return b.String()
	case ir.KindBuiltin:
		// "string" -> "string"; "time.Time" -> "Time" (short form).
		if i := strings.LastIndex(ref.Builtin, "."); i >= 0 {
			return ref.Builtin[i+1:]
		}
		return ref.Builtin
	case ir.KindSlice:
		return "List" + argName(*ref.Element)
	case ir.KindMap:
		return "Map" + argName(*ref.Value)
	}
	panic("openapi: unsupported arg kind " + fmt.Sprint(ref.Kind) + " in generic instantiation name")
}

// short returns the unqualified TS-identifier portion of an IR
// qualified name ("github.com/x/api.User" -> "User").
func short(qualified string) string {
	if i := strings.LastIndex(qualified, "."); i >= 0 {
		return qualified[i+1:]
	}
	return qualified
}
