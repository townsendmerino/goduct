// Package postman generates postman_collection.json: a Postman
// collection format v2.1.0 document. See ADR 0035 for design pins —
// {{baseUrl}} variable, tag-folder grouping, source-order requests
// within a tag, alphabetical folders, deterministic info._postman_id
// derived from SHA-1(packageName), JSDocFull descriptions, and
// per-request synthesized JSON body examples.
package postman

import (
	"bytes"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/townsendmerino/goduct/internal/gen"
	"github.com/townsendmerino/goduct/internal/ir"
)

const schemaURL = "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"
const defaultBaseURL = "http://localhost:8080"

// Generate writes postman_collection.json for api to w (ADR 0035).
func Generate(api *ir.API, w io.Writer) error {
	name := gen.PackageName(api)
	if name == "" {
		name = "goduct"
	}
	coll := collection{
		Info: collectionInfo{
			PostmanID: deterministicID(name),
			Name:      name,
			Schema:    schemaURL,
		},
		Items: buildItems(api),
		Variables: []variable{
			{Key: "baseUrl", Value: defaultBaseURL, Type: "string"},
		},
	}

	var compact bytes.Buffer
	enc := json.NewEncoder(&compact)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(coll); err != nil {
		return fmt.Errorf("postman: encode: %w", err)
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, compact.Bytes(), "", "  "); err != nil {
		return fmt.Errorf("postman: indent: %w", err)
	}
	_, err := w.Write(pretty.Bytes())
	return err
}

// ----------------------------------------------------------------
// Collection types
// ----------------------------------------------------------------

type collection struct {
	Info      collectionInfo `json:"info"`
	Items     []any          `json:"item"`
	Variables []variable     `json:"variable,omitempty"`
}

type collectionInfo struct {
	PostmanID string `json:"_postman_id"`
	Name      string `json:"name"`
	Schema    string `json:"schema"`
}

// folder groups requests under a tag.
type folder struct {
	Name  string `json:"name"`
	Items []any  `json:"item"`
}

// request is one Postman request entry.
type request struct {
	Name    string        `json:"name"`
	Request requestDetail `json:"request"`
}

type requestDetail struct {
	Method      string    `json:"method"`
	Header      []header  `json:"header"`
	Body        *body     `json:"body,omitempty"`
	URL         url       `json:"url"`
	Description string    `json:"description,omitempty"`
}

type header struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Type  string `json:"type,omitempty"`
}

// body is Postman's body envelope. Mode is "raw" for JSON bodies
// and "formdata" for ADR 0042 multipart uploads. Raw + Options are
// only emitted in raw mode; FormData is only emitted in formdata
// mode. omitempty + pointer-typed FormData keep the JSON-body shape
// byte-identical when uploads aren't present.
type body struct {
	Mode     string         `json:"mode"`
	Raw      string         `json:"raw,omitempty"`
	Options  *bodyOptions   `json:"options,omitempty"`
	FormData []formdataItem `json:"formdata,omitempty"`
}

type bodyOptions struct {
	Raw bodyOptionsRaw `json:"raw"`
}

type bodyOptionsRaw struct {
	Language string `json:"language"`
}

// formdataItem is one row of a Postman formdata body. Type is
// "file" for multipart file parts and "text" for form text parts.
type formdataItem struct {
	Key  string `json:"key"`
	Type string `json:"type"`
}

type url struct {
	Raw      string         `json:"raw"`
	Host     []string       `json:"host"`
	Path     []string       `json:"path"`
	Query    []queryParam   `json:"query,omitempty"`
	Variable []urlVariable  `json:"variable,omitempty"`
}

type queryParam struct {
	Key         string `json:"key"`
	Value       string `json:"value"`
	Disabled    bool   `json:"disabled,omitempty"`
	Description string `json:"description,omitempty"`
}

type urlVariable struct {
	Key         string `json:"key"`
	Value       string `json:"value"`
	Description string `json:"description,omitempty"`
}

type variable struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Type  string `json:"type,omitempty"`
}

// ----------------------------------------------------------------
// Items: tag folders + requests
// ----------------------------------------------------------------

// buildItems groups routes by tag (alphabetical folders, source-order
// requests within a tag). Routes without a tag (empty string) live at
// the collection root.
func buildItems(api *ir.API) []any {
	byTag := map[string][]ir.Route{}
	for _, r := range api.Routes {
		// ADR 0041: Postman v2.1 doesn't model SSE well (the request
		// would hang indefinitely with no useful UI affordance), so
		// streaming routes are omitted from the collection. The
		// openapi.json + swagger-ui still describe them.
		// ADR 0044: WebSocket routes need Postman's distinct ws-item
		// shape; emission deferred. Skip rather than emit a misleading
		// HTTP GET that won't work.
		if r.StreamType != nil || r.WebSocket != nil {
			continue
		}
		byTag[r.Tag] = append(byTag[r.Tag], r)
	}
	tagKeys := make([]string, 0, len(byTag))
	for k := range byTag {
		tagKeys = append(tagKeys, k)
	}
	sort.Strings(tagKeys)

	var out []any
	for _, tag := range tagKeys {
		reqs := byTag[tag]
		items := make([]any, len(reqs))
		for i, r := range reqs {
			items[i] = buildRequest(api, r)
		}
		if tag == "" {
			// Untagged routes go at the collection root rather than in
			// an empty-named folder.
			out = append(out, items...)
			continue
		}
		out = append(out, folder{Name: tag, Items: items})
	}
	return out
}

// buildRequest assembles one Postman request from an ir.Route.
func buildRequest(api *ir.API, r ir.Route) request {
	rd := requestDetail{
		Method: r.Method,
		Header: buildHeaders(r),
		URL:    buildURL(r),
	}
	if d := gen.JSDocFull(r.HandlerName, r.Doc); d != "" {
		rd.Description = d
	}
	if r.BodyType != nil {
		// ADR 0042: upload routes use Postman's formdata body mode
		// (one row per multipart/form field); everything else uses
		// the JSON raw mode.
		if r.Upload {
			rd.Body = buildUploadBody(api, *r.BodyType)
		} else {
			rd.Body = buildBody(api, *r.BodyType)
		}
	}
	return request{
		Name:    r.HandlerName,
		Request: rd,
	}
}

func buildHeaders(r ir.Route) []header {
	out := make([]header, 0, len(r.HeaderParams))
	for _, h := range r.HeaderParams {
		out = append(out, header{Key: h.WireName, Value: "", Type: "text"})
	}
	return out
}

// buildURL assembles the Postman url object: raw template, host
// segment (`{{baseUrl}}`), path segments split on /, optional query
// (disabled per Param.Optional), and url.variable entries for each
// path param (defaults to empty value).
func buildURL(r ir.Route) url {
	pathSegs := strings.Split(strings.TrimPrefix(r.Path, "/"), "/")
	if len(pathSegs) == 1 && pathSegs[0] == "" {
		pathSegs = nil
	}

	u := url{
		Raw:  "{{baseUrl}}" + r.Path,
		Host: []string{"{{baseUrl}}"},
		Path: pathSegs,
	}
	if len(r.QueryParams) > 0 {
		u.Query = make([]queryParam, len(r.QueryParams))
		for i, q := range r.QueryParams {
			u.Query[i] = queryParam{
				Key:      q.WireName,
				Value:    "",
				Disabled: q.Optional,
			}
		}
	}
	if len(r.PathParams) > 0 {
		u.Variable = make([]urlVariable, len(r.PathParams))
		for i, p := range r.PathParams {
			u.Variable[i] = urlVariable{Key: p.WireName, Value: ""}
		}
	}
	return u
}

// buildBody synthesizes a JSON example body for the body type and
// wraps it in Postman's raw-body envelope with `language: json`.
// Body examples are indented (2-space) so they render cleanly in
// Postman's request-body editor.
func buildBody(api *ir.API, ref ir.TypeRef) *body {
	example := newExampleBuilder(api).exampleFor(ref)
	exampleBytes, err := json.Marshal(example)
	if err != nil {
		// Shouldn't fail for the shapes we emit; if it does it's
		// a generator-internal bug.
		exampleBytes = []byte("{}")
	}
	var indented bytes.Buffer
	if err := json.Indent(&indented, exampleBytes, "", "  "); err != nil {
		indented.Reset()
		indented.Write(exampleBytes)
	}
	return &body{
		Mode: "raw",
		Raw:  indented.String(),
		Options: &bodyOptions{
			Raw: bodyOptionsRaw{Language: "json"},
		},
	}
}

// buildUploadBody renders a Postman formdata body for a typed
// upload route (ADR 0042). One row per multipart/form field; file
// parts get type:"file", text parts get type:"text". The user fills
// in actual values in Postman.
func buildUploadBody(api *ir.API, ref ir.TypeRef) *body {
	if ref.Kind != ir.KindNamed {
		return nil
	}
	td, ok := api.Types[ref.Named]
	if !ok {
		return nil
	}
	uploadFields := gen.UploadFields(td)
	if len(uploadFields) == 0 {
		return nil
	}
	items := make([]formdataItem, 0, len(uploadFields))
	for _, f := range uploadFields {
		t := "text"
		if f.Source == ir.FieldSourceMultipart {
			t = "file"
		}
		items = append(items, formdataItem{Key: f.JSONName, Type: t})
	}
	return &body{Mode: "formdata", FormData: items}
}

// ----------------------------------------------------------------
// Example synthesis: walk a TypeRef and produce a Go value whose
// JSON encoding is a typed placeholder per ADR 0035 §Postman.
// ----------------------------------------------------------------

type exampleBuilder struct {
	api  *ir.API
	subst map[string]ir.TypeRef // per-instantiation type-param substitution
}

func newExampleBuilder(api *ir.API) *exampleBuilder {
	return &exampleBuilder{api: api}
}

func (eb *exampleBuilder) exampleFor(ref ir.TypeRef) any {
	switch ref.Kind {
	case ir.KindBuiltin:
		return builtinExample(eb.api, ref.Builtin)
	case ir.KindSlice:
		return []any{}
	case ir.KindMap:
		return map[string]any{}
	case ir.KindTypeParam:
		if r, ok := eb.subst[ref.TypeParam]; ok {
			return eb.exampleFor(r)
		}
		return nil
	case ir.KindNamed:
		td, ok := eb.api.Types[ref.Named]
		if !ok {
			return nil
		}
		// Generic instantiation: push a fresh substitution map.
		subst := eb.subst
		if len(td.TypeParams) > 0 && len(ref.TypeArgs) == len(td.TypeParams) {
			subst = map[string]ir.TypeRef{}
			for i, p := range td.TypeParams {
				subst[p] = *ref.TypeArgs[i]
			}
		}
		inner := &exampleBuilder{api: eb.api, subst: subst}
		return inner.fromTypeDef(td)
	}
	return nil
}

func (eb *exampleBuilder) fromTypeDef(td ir.TypeDef) any {
	switch td.Kind {
	case ir.TypeStruct:
		var op orderedExample
		for _, f := range td.Fields {
			if f.Source != ir.FieldSourceJSON {
				continue
			}
			op.keys = append(op.keys, f.JSONName)
			op.values = append(op.values, eb.exampleFor(f.Type))
		}
		return op
	case ir.TypeEnum:
		if len(td.EnumValues) == 0 {
			return ""
		}
		if td.Underlying == "int" {
			var n int64
			_, _ = fmt.Sscan(td.EnumValues[0].Value, &n)
			return n
		}
		return td.EnumValues[0].Value
	case ir.TypeAlias:
		if td.AliasTo != nil {
			return eb.exampleFor(*td.AliasTo)
		}
		return nil
	}
	return nil
}

// builtinExample returns a JSON value placeholder for an ir.Builtin.
// Wire shapes match ADR 0017 + ADR 0032 (custom adapter fallthrough).
func builtinExample(api *ir.API, b string) any {
	switch b {
	case "string", "time.Time", "[]byte", "uuid.UUID":
		return ""
	case "bool":
		return false
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64", "time.Duration":
		return 0
	case "json.RawMessage":
		return map[string]any{}
	}
	if wire, ok := api.CustomAdapters[b]; ok {
		switch wire {
		case "string":
			return ""
		case "number":
			return 0
		case "boolean":
			return false
		case "unknown":
			return map[string]any{}
		}
	}
	return nil
}

// orderedExample preserves source declaration order for struct
// field examples (matches OpenAPI's orderedProps trick — regular
// Go maps alphabetize which loses wire-order signal).
type orderedExample struct {
	keys   []string
	values []any
}

func (o orderedExample) MarshalJSON() ([]byte, error) {
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
// Deterministic ID
// ----------------------------------------------------------------

// deterministicID derives a UUID-shaped string from SHA-1(name). Not
// a strict RFC 4122 v5 UUID (no version/variant bit fiddling), but
// Postman just needs a unique-looking 36-char ID and this is what
// gives the chi-basic golden a stable, reproducible value.
func deterministicID(name string) string {
	sum := sha1.Sum([]byte(name))
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}
