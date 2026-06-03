package openapi

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/townsendmerino/goduct/internal/analyzer"
	"github.com/townsendmerino/goduct/internal/ir"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	r, err := filepath.Abs(filepath.Join(filepath.Dir(file), "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestGenerate_ChiBasicGolden(t *testing.T) {
	root := repoRoot(t)
	api, err := analyzer.Analyze([]string{"./examples/chi-basic/api"},
		analyzer.LoadOptions{Dir: root})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	var buf bytes.Buffer
	if err := Generate(api, &buf); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	want, err := os.ReadFile(filepath.Join(root,
		"examples/chi-basic/testdata/expected/openapi.json"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("openapi.json != golden (got %d bytes, want %d bytes)",
			buf.Len(), len(want))
	}
}

// TestGenerate_ValidJSON: the generated output round-trips through
// encoding/json. If a future change emits malformed JSON, this fires
// before the byte-diff test masks it as a "huge diff".
func TestGenerate_ValidJSON(t *testing.T) {
	root := repoRoot(t)
	api, err := analyzer.Analyze([]string{"./examples/chi-basic/api"},
		analyzer.LoadOptions{Dir: root})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	var buf bytes.Buffer
	if err := Generate(api, &buf); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if parsed["openapi"] != "3.1.0" {
		t.Errorf("openapi field = %v, want 3.1.0", parsed["openapi"])
	}
}

func TestPathConvert(t *testing.T) {
	cases := map[string]string{
		"/users":          "/users",
		"/users/:id":      "/users/{id}",
		"/a/:x/b/:y":      "/a/{x}/b/{y}",
		"/no/colons/here": "/no/colons/here",
	}
	for in, want := range cases {
		if got := pathConvert(in); got != want {
			t.Errorf("pathConvert(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestInstantiationName(t *testing.T) {
	cases := []struct {
		name   string
		qname  string
		args   []*ir.TypeRef
		want   string
	}{
		{
			"Page<User>",
			"x/api.Page",
			[]*ir.TypeRef{{Kind: ir.KindNamed, Named: "x/api.User"}},
			"Page_User",
		},
		{
			"Result<User, Err>",
			"x/api.Result",
			[]*ir.TypeRef{
				{Kind: ir.KindNamed, Named: "x/api.User"},
				{Kind: ir.KindNamed, Named: "x/api.Err"},
			},
			"Result_User_Err",
		},
		{
			"Page<Result<User, Err>>",
			"x/api.Page",
			[]*ir.TypeRef{{Kind: ir.KindNamed, Named: "x/api.Result",
				TypeArgs: []*ir.TypeRef{
					{Kind: ir.KindNamed, Named: "x/api.User"},
					{Kind: ir.KindNamed, Named: "x/api.Err"},
				}}},
			"Page_Result_User_Err",
		},
		{
			"Optional<string>",
			"x/api.Optional",
			[]*ir.TypeRef{{Kind: ir.KindBuiltin, Builtin: "string"}},
			"Optional_string",
		},
		{
			"List<time.Time>",
			"x/api.List",
			[]*ir.TypeRef{{Kind: ir.KindBuiltin, Builtin: "time.Time"}},
			"List_Time",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := instantiationName(c.qname, c.args); got != c.want {
				t.Errorf("instantiationName = %q, want %q", got, c.want)
			}
		})
	}
}

func TestBuiltinSchema(t *testing.T) {
	api := &ir.API{}
	cases := []struct {
		in   string
		want string // a substring check on the JSON-encoded schema
	}{
		{"string", `"type":"string"`},
		{"bool", `"type":"boolean"`},
		{"int", `"type":"integer"`},
		{"int64", `"type":"integer"`},
		{"float64", `"type":"number"`},
		{"time.Time", `"format":"date-time"`},
		{"[]byte", `"format":"byte"`},
		{"uuid.UUID", `"format":"uuid"`},
		{"json.RawMessage", `{}`},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			s := builtinSchema(api, c.in)
			b, _ := json.Marshal(s)
			if !strings.Contains(string(b), c.want) {
				t.Errorf("builtinSchema(%q) = %s, want substring %q", c.in, b, c.want)
			}
		})
	}
}

func TestCustomAdapter_FallsThrough(t *testing.T) {
	// ADR 0032: a user-declared adapter qname renders per its wire
	// shape (mirrors AdapterWireTS / AdapterWireZod for JSON Schema).
	api := &ir.API{
		CustomAdapters: map[string]string{
			"github.com/shopspring/decimal.Decimal": "string",
			"foo.Pi":                                "number",
		},
	}
	if got, _ := json.Marshal(builtinSchema(api, "github.com/shopspring/decimal.Decimal")); !strings.Contains(string(got), `"type":"string"`) {
		t.Errorf("decimal adapter not string: %s", got)
	}
	if got, _ := json.Marshal(builtinSchema(api, "foo.Pi")); !strings.Contains(string(got), `"type":"number"`) {
		t.Errorf("Pi adapter not number: %s", got)
	}
}

// TestGenericInstantiation: a synthetic IR with a Page[T] origin +
// a route returning *Page[User] produces a Page_User component
// schema with the substituted field types.
func TestGenericInstantiation(t *testing.T) {
	api := &ir.API{
		Types: map[string]ir.TypeDef{
			"x/api.Page": {
				QualifiedName: "x/api.Page", Name: "Page", Kind: ir.TypeStruct,
				TypeParams: []string{"T"},
				Fields: []ir.Field{
					{GoName: "Items", JSONName: "items", Source: ir.FieldSourceJSON,
						Type: ir.TypeRef{Kind: ir.KindSlice,
							Element: &ir.TypeRef{Kind: ir.KindTypeParam, TypeParam: "T"}}},
					{GoName: "NextCursor", JSONName: "nextCursor", Source: ir.FieldSourceJSON, Optional: true,
						Type: ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "string"}},
				},
			},
			"x/api.User": {
				QualifiedName: "x/api.User", Name: "User", Kind: ir.TypeStruct,
				Fields: []ir.Field{
					{GoName: "ID", JSONName: "id", Source: ir.FieldSourceJSON,
						Type: ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "string"}},
				},
			},
		},
		Routes: []ir.Route{{
			HandlerName: "ListUsers", Method: "GET", Path: "/users", Tag: "users",
			SuccessStatus: 200,
			ResponseType: &ir.TypeRef{Kind: ir.KindNamed, Named: "x/api.Page",
				TypeArgs: []*ir.TypeRef{{Kind: ir.KindNamed, Named: "x/api.User"}}},
		}},
	}
	var buf bytes.Buffer
	if err := Generate(api, &buf); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	out := buf.String()
	// The instantiation is emitted as Page_User; the generic origin
	// Page is NOT emitted as a standalone component.
	if !strings.Contains(out, `"Page_User"`) {
		t.Errorf("expected Page_User component schema:\n%s", out)
	}
	if strings.Contains(out, `"schemas": {`) && strings.Contains(out, `"Page": {`) {
		// Look for `"Page": {` as a component-schema header, not the prefix of "Page_User".
		if idx := strings.Index(out, `"Page": {`); idx >= 0 {
			t.Errorf("Page (generic origin) should not be emitted as standalone component:\n%s", out)
		}
	}
	// The route's response references Page_User by $ref.
	if !strings.Contains(out, `"$ref": "#/components/schemas/Page_User"`) {
		t.Errorf("response $ref should target Page_User:\n%s", out)
	}
	// The substituted Page_User has items: array of User-$ref.
	if !strings.Contains(out, `"$ref": "#/components/schemas/User"`) {
		t.Errorf("Page_User.items should $ref User:\n%s", out)
	}
}

// TestGenerate_MetaOverrides covers ADR 0038 §5: when api.Meta is
// populated, the generator surfaces those values in info{} and the
// document-level `servers` array. Pre-config defaults still apply
// for the empty-string fields.
func TestGenerate_MetaOverrides(t *testing.T) {
	root := repoRoot(t)
	api, err := analyzer.Analyze([]string{"./examples/chi-basic/api"},
		analyzer.LoadOptions{Dir: root})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	api.Meta = ir.Meta{
		OpenAPITitle:       "Custom Title",
		OpenAPIVersion:     "1.2.3",
		OpenAPIDescription: "A test description.",
		OpenAPIServers:     []string{"https://api.example.com", "https://staging.example.com"},
	}
	var buf bytes.Buffer
	if err := Generate(api, &buf); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `"title": "Custom Title"`) {
		t.Errorf("missing title override:\n%s", out)
	}
	if !strings.Contains(out, `"version": "1.2.3"`) {
		t.Errorf("missing version override:\n%s", out)
	}
	if !strings.Contains(out, `"description": "A test description."`) {
		t.Errorf("missing description:\n%s", out)
	}
	if !strings.Contains(out, `"url": "https://api.example.com"`) ||
		!strings.Contains(out, `"url": "https://staging.example.com"`) {
		t.Errorf("missing servers[].url entries:\n%s", out)
	}
}

// TestGenerate_MetaPartial_DefaultsRetained: empty fields in Meta
// fall back to built-in defaults (title=package, version="0.0.0").
// info.description and document.servers stay absent when their Meta
// fields are empty so existing consumers see no shape change.
// Parses the output as JSON so route-level "description" fields
// don't confuse the assertion.
func TestGenerate_MetaPartial_DefaultsRetained(t *testing.T) {
	root := repoRoot(t)
	api, err := analyzer.Analyze([]string{"./examples/chi-basic/api"},
		analyzer.LoadOptions{Dir: root})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	api.Meta = ir.Meta{OpenAPIVersion: "9.9.9"} // version only
	var buf bytes.Buffer
	if err := Generate(api, &buf); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	var got struct {
		Info struct {
			Title       string `json:"title"`
			Version     string `json:"version"`
			Description string `json:"description"`
		} `json:"info"`
		Servers []any `json:"servers"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Info.Title != "api" {
		t.Errorf("info.title = %q, want \"api\" (package-name default)", got.Info.Title)
	}
	if got.Info.Version != "9.9.9" {
		t.Errorf("info.version = %q, want \"9.9.9\"", got.Info.Version)
	}
	if got.Info.Description != "" {
		t.Errorf("info.description should be absent when empty, got %q", got.Info.Description)
	}
	if got.Servers != nil {
		t.Errorf("document.servers should be absent when empty, got %v", got.Servers)
	}
}

// TestGenerate_ExampleMalformedJSON covers ADR 0039 §1 loud-fail:
// an example that isn't valid JSON fails at generate time with
// the offending handler named.
func TestGenerate_ExampleMalformedJSON(t *testing.T) {
	api := &ir.API{
		Routes: []ir.Route{{
			HandlerName: "BadExample", Method: "GET", Path: "/x", Tag: "x",
			Mode:          ir.ModeIdiomatic,
			RequestType:   &ir.TypeRef{Kind: ir.KindNamed, Named: "pkg.R"},
			ResponseType:  &ir.TypeRef{Kind: ir.KindNamed, Named: "pkg.R"},
			SuccessStatus: 200,
			Example:       `{not valid json}`,
		}},
		Types: map[string]ir.TypeDef{
			"pkg.R": {QualifiedName: "pkg.R", Name: "R", Kind: ir.TypeStruct,
				Fields: []ir.Field{{GoName: "ID", JSONName: "id", Source: ir.FieldSourceJSON,
					Type: ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "string"}}}},
		},
	}
	var buf bytes.Buffer
	err := Generate(api, &buf)
	if err == nil {
		t.Fatal("expected loud-fail for malformed example, got nil")
	}
	if !strings.Contains(err.Error(), "BadExample") || !strings.Contains(err.Error(), "valid JSON") {
		t.Errorf("error should name the handler and mention valid JSON, got: %v", err)
	}
}

// TestGenerate_PerStatusResponses covers ADR 0039 §3: each
// errorresponse entry surfaces as responses["<status>"] with a
// schema $ref to the named type. The default GoductError response
// stays in place for statuses the route doesn't override.
func TestGenerate_PerStatusResponses(t *testing.T) {
	api := &ir.API{
		Routes: []ir.Route{{
			HandlerName: "CreateThing", Method: "POST", Path: "/things", Tag: "things",
			Mode:          ir.ModeIdiomatic,
			RequestType:   &ir.TypeRef{Kind: ir.KindNamed, Named: "pkg.Req"},
			BodyType:      &ir.TypeRef{Kind: ir.KindNamed, Named: "pkg.Req"},
			ResponseType:  &ir.TypeRef{Kind: ir.KindNamed, Named: "pkg.Thing"},
			SuccessStatus: 201,
			ErrorResponses: []ir.ErrorResponse{
				{Status: 400, Type: &ir.TypeRef{Kind: ir.KindNamed, Named: "pkg.ValidationError"}},
				{Status: 409, Type: &ir.TypeRef{Kind: ir.KindNamed, Named: "pkg.ConflictError"}},
			},
		}},
		Types: map[string]ir.TypeDef{
			"pkg.Req":             mkType("Req"),
			"pkg.Thing":           mkType("Thing"),
			"pkg.ValidationError": mkType("ValidationError"),
			"pkg.ConflictError":   mkType("ConflictError"),
		},
	}
	var buf bytes.Buffer
	if err := Generate(api, &buf); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`"400"`, `"409"`, `"default"`,
		`"$ref": "#/components/schemas/ValidationError"`,
		`"$ref": "#/components/schemas/ConflictError"`,
		`"$ref": "#/components/schemas/GoductError"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

// TestGenerate_SecuritySchemesAndRequirements covers ADR 0039 §2:
// the Meta.Security map flows as-is to components.securitySchemes
// and Meta.SecurityRequirements becomes the top-level `security`.
func TestGenerate_SecuritySchemesAndRequirements(t *testing.T) {
	root := repoRoot(t)
	api, err := analyzer.Analyze([]string{"./examples/chi-basic/api"},
		analyzer.LoadOptions{Dir: root})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	api.Meta = ir.Meta{
		Security: map[string]any{
			"bearerAuth": map[string]any{
				"type": "http", "scheme": "bearer", "bearerFormat": "JWT",
			},
		},
		SecurityRequirements: []map[string][]string{
			{"bearerAuth": {}},
		},
	}
	var buf bytes.Buffer
	if err := Generate(api, &buf); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	var got struct {
		Security   []map[string][]string `json:"security"`
		Components struct {
			SecuritySchemes map[string]any `json:"securitySchemes"`
		} `json:"components"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Security) != 1 || got.Security[0]["bearerAuth"] == nil {
		t.Errorf("document.security missing bearerAuth requirement: %v", got.Security)
	}
	if got.Components.SecuritySchemes["bearerAuth"] == nil {
		t.Errorf("components.securitySchemes missing bearerAuth: %v", got.Components.SecuritySchemes)
	}
}

func mkType(name string) ir.TypeDef {
	return ir.TypeDef{
		QualifiedName: "pkg." + name, Name: name, Kind: ir.TypeStruct,
		Fields: []ir.Field{{GoName: "ID", JSONName: "id", Source: ir.FieldSourceJSON,
			Type: ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "string"}}},
	}
}

// TestGenerate_RequestExample covers ADR 0040 §2: a populated
// Route.RequestExample becomes requestBody.content."application/
// json".example as a parsed JSON value (not a string-quoted blob).
// Coexists with the response-body example from ADR 0039.
func TestGenerate_RequestExample(t *testing.T) {
	api := &ir.API{
		Routes: []ir.Route{{
			HandlerName: "CreateThing", Method: "POST", Path: "/things", Tag: "things",
			Mode:           ir.ModeIdiomatic,
			RequestType:    &ir.TypeRef{Kind: ir.KindNamed, Named: "pkg.Req"},
			BodyType:       &ir.TypeRef{Kind: ir.KindNamed, Named: "pkg.Req"},
			ResponseType:   &ir.TypeRef{Kind: ir.KindNamed, Named: "pkg.Thing"},
			SuccessStatus:  201,
			RequestExample: `{"name":"Widget","qty":3}`,
			Example:        `{"id":"t_1","name":"Widget"}`,
		}},
		Types: map[string]ir.TypeDef{
			"pkg.Req":   mkType("Req"),
			"pkg.Thing": mkType("Thing"),
		},
	}
	var buf bytes.Buffer
	if err := Generate(api, &buf); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	var got struct {
		Paths map[string]struct {
			Post struct {
				RequestBody struct {
					Content map[string]struct {
						Example map[string]any `json:"example"`
					} `json:"content"`
				} `json:"requestBody"`
				Responses map[string]struct {
					Content map[string]struct {
						Example map[string]any `json:"example"`
					} `json:"content"`
				} `json:"responses"`
			} `json:"post"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	post := got.Paths["/things"].Post
	if reqEx := post.RequestBody.Content["application/json"].Example; reqEx["name"] != "Widget" {
		t.Errorf("requestBody.example missing or wrong: %v", reqEx)
	}
	if respEx := post.Responses["201"].Content["application/json"].Example; respEx["id"] != "t_1" {
		t.Errorf("response.example missing or wrong: %v", respEx)
	}
}

// TestGenerate_RequestExampleMalformedJSON covers ADR 0040 §2
// loud-fail symmetry with the response-side example.
func TestGenerate_RequestExampleMalformedJSON(t *testing.T) {
	api := &ir.API{
		Routes: []ir.Route{{
			HandlerName: "BadReq", Method: "POST", Path: "/x", Tag: "x",
			Mode:           ir.ModeIdiomatic,
			RequestType:    &ir.TypeRef{Kind: ir.KindNamed, Named: "pkg.Req"},
			BodyType:       &ir.TypeRef{Kind: ir.KindNamed, Named: "pkg.Req"},
			ResponseType:   &ir.TypeRef{Kind: ir.KindNamed, Named: "pkg.Req"},
			SuccessStatus:  200,
			RequestExample: `{not valid json}`,
		}},
		Types: map[string]ir.TypeDef{"pkg.Req": mkType("Req")},
	}
	var buf bytes.Buffer
	err := Generate(api, &buf)
	if err == nil || !strings.Contains(err.Error(), "BadReq") ||
		!strings.Contains(err.Error(), "valid JSON") {
		t.Errorf("expected loud-fail naming the handler, got: %v", err)
	}
}

// TestGenerate_PerOperationSecurity covers ADR 0040 §1: per-handler
// security overrides emit as operation.security. The `none` form
// is an empty inner map (OpenAPI 3.1's explicit unauthenticated
// override); a named scheme becomes a single-key map with empty
// scopes; multiple entries compose as OR (array of length > 1).
func TestGenerate_PerOperationSecurity(t *testing.T) {
	api := &ir.API{
		Routes: []ir.Route{
			{
				HandlerName: "Healthz", Method: "GET", Path: "/healthz", Tag: "system",
				Mode:          ir.ModeIdiomatic,
				RequestType:   &ir.TypeRef{Kind: ir.KindNamed, Named: "pkg.Req"},
				ResponseType:  &ir.TypeRef{Kind: ir.KindNamed, Named: "pkg.Req"},
				SuccessStatus: 200,
				Security:      []ir.SecurityRequirement{{}}, // `none`
			},
			{
				HandlerName: "GetAccount", Method: "GET", Path: "/accounts/:id", Tag: "accounts",
				Mode:          ir.ModeIdiomatic,
				RequestType:   &ir.TypeRef{Kind: ir.KindNamed, Named: "pkg.Req"},
				ResponseType:  &ir.TypeRef{Kind: ir.KindNamed, Named: "pkg.Req"},
				SuccessStatus: 200,
				Security: []ir.SecurityRequirement{
					{Schemes: []string{"bearerAuth"}},
					{Schemes: []string{"apiKey"}},
				},
			},
			{
				HandlerName: "PlainOp", Method: "GET", Path: "/plain", Tag: "plain",
				Mode:          ir.ModeIdiomatic,
				RequestType:   &ir.TypeRef{Kind: ir.KindNamed, Named: "pkg.Req"},
				ResponseType:  &ir.TypeRef{Kind: ir.KindNamed, Named: "pkg.Req"},
				SuccessStatus: 200,
				// No Security: inherits document default.
			},
		},
		Types: map[string]ir.TypeDef{"pkg.Req": mkType("Req")},
	}
	var buf bytes.Buffer
	if err := Generate(api, &buf); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	var got struct {
		Paths map[string]map[string]struct {
			Security []map[string][]string `json:"security"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	healthz := got.Paths["/healthz"]["get"].Security
	if len(healthz) != 1 || len(healthz[0]) != 0 {
		t.Errorf("/healthz security should be [{}] (the `none` form), got %v", healthz)
	}
	acct := got.Paths["/accounts/{id}"]["get"].Security
	if len(acct) != 2 {
		t.Fatalf("/accounts/{id} security len = %d, want 2", len(acct))
	}
	if _, ok := acct[0]["bearerAuth"]; !ok {
		t.Errorf("/accounts/{id} security[0] should have bearerAuth: %v", acct[0])
	}
	if _, ok := acct[1]["apiKey"]; !ok {
		t.Errorf("/accounts/{id} security[1] should have apiKey: %v", acct[1])
	}
	plain := got.Paths["/plain"]["get"].Security
	if plain != nil {
		t.Errorf("/plain has no per-op security; should inherit (be absent), got %v", plain)
	}
}
