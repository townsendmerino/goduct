# 0042. File uploads — typed multipart + raw-mode extension (v0.6)

**Status:** Accepted
**Date:** 2026-06-02

## Context

v0.5 added the first streaming primitive (SSE). The other half of
the "endpoints that aren't plain JSON request/response" surface is
**file upload**: a browser POSTs `multipart/form-data` with a mix
of text fields and binary file parts, and the server processes
both. v0.6 adds it.

Two real user populations exist:

1. **Typed-shape users.** A profile-picture upload, a form with
   one file field plus three text fields. They want the same
   end-to-end strictness goduct provides for JSON routes: the
   request struct names every part, validators apply, the TS
   client knows the field set.

2. **Raw-shape users.** A 5 GB log upload, a streaming chunked
   import, a webhook that just forwards bytes. They want to
   handle `multipart.Reader` themselves — no buffering, no
   automatic parsing, no struct binding. They already drop to
   raw mode (ADR 0031) for these; goduct currently contributes
   nothing on the wire-shape side.

Both shapes need the same TS-client + OpenAPI surface (FormData
on the client, `multipart/form-data` content type in the spec).
Both ship in v0.6 to avoid two release cuts for the same family
of work.

## Decision

### 1. Typed shape — new tag families

A handler with the existing idiomatic signature
`func(ctx, T) (*U, error)` becomes a **typed upload** when T has
**at least one field tagged `multipart:"<name>"`** (the file
field). Text fields in the same struct use a sibling tag,
`form:"<name>"`. Path/query/header tags continue to work
unchanged. `json:` tags on the same struct are an error — a
struct can't be both JSON and multipart on the wire.

```go
type UploadAvatarRequest struct {
    UserID  string                `path:"id"        validate:"required"`
    File    *multipart.FileHeader `multipart:"file" validate:"required"`
    Caption string                `form:"caption"   validate:"max=200"`
}

// goduct:route POST /users/:id/avatar
// goduct:tag   users
func UploadAvatar(ctx context.Context, req UploadAvatarRequest) (*User, error) {
    f, err := req.File.Open()
    if err != nil { return nil, goduct.BadRequest("cannot read file") }
    defer f.Close()
    // ... save to storage ...
}
```

Field type rules:

- A `multipart:"..."` field must be `*multipart.FileHeader` (or
  `[]*multipart.FileHeader` for multi-file inputs — v0.6 ships
  single only; slice deferred to v0.6.1).
- A `form:"..."` field must be a primitive that the existing
  query-param parsing already handles (string, int*, uint*,
  float*, bool). Optionality follows the existing
  query/header rule: pointer or no `validate:"required"` → optional.
- `path:"..."` / `query:"..."` / `header:"..."` fields work
  identically to non-upload routes.

### 2. Raw shape — `goduct:upload` directive

A raw handler (`func(w, r)` with `goduct:request`/`goduct:response`
annotations per ADR 0031) adds **`goduct:upload`** to declare the
endpoint accepts `multipart/form-data`:

```go
// goduct:route    POST /bulk/import
// goduct:upload
// goduct:request  BulkImportRequest
// goduct:response BulkImportResponse
func BulkImport(w http.ResponseWriter, r *http.Request) {
    mr, err := r.MultipartReader()
    if err != nil { /* ... */ return }
    for {
        part, err := mr.NextPart()
        if err == io.EOF { break }
        // ... process each part streamingly ...
    }
}
```

`goduct:upload` toggles two things:

- The generated TS client method builds a `FormData` (instead of
  `JSON.stringify`).
- The OpenAPI operation's `requestBody.content` key flips from
  `application/json` to `multipart/form-data`.

The Go side is untouched — the user writes the multipart
handling. The directive is purely a wire-format hint to the
non-Go generators.

### 3. IR additions

```go
// ir.go (additive, ADR 0027):
type Route struct {
    ...existing...
    // Upload marks this route as multipart/form-data on the wire
    // (ADR 0042). True for both typed-upload routes (detected from
    // multipart-tagged request fields) and raw-upload routes
    // (detected from goduct:upload). The flag drives:
    //   - tsclient: FormData construction instead of JSON encode
    //   - openapi:  multipart/form-data content type
    //   - postman:  formdata body mode
    //   - goadapter: typed-mode multipart parsing wrapper (raw
    //     handlers are registered directly per ADR 0031)
    Upload bool
}

// FieldSource (existing enum) gains two values:
const (
    FieldSourceJSON      FieldSource = iota
    FieldSourcePath
    FieldSourceQuery
    FieldSourceHeader
    FieldSourceNone
    FieldSourceMultipart  // new: file part (multipart:"...")
    FieldSourceForm       // new: text part (form:"...")
)
```

`gen.WireFields` continues to mean "JSON wire fields" — multipart
and form fields are NOT JSON-visible (they live on the form, not
in a JSON body). A separate helper, `gen.UploadFields`, returns
the multipart+form subset for the generators that care.

### 4. Generator behavior

**goadapter** for typed uploads emits a wrapper that:

```go
func handleUploadAvatar(w http.ResponseWriter, r *http.Request) {
    if err := r.ParseMultipartForm(32 << 20); err != nil {
        goduct.WriteError(w, goduct.BadRequest("invalid multipart form"))
        return
    }
    var req UploadAvatarRequest
    req.UserID = chi.URLParam(r, "id")
    if files := r.MultipartForm.File["file"]; len(files) > 0 {
        req.File = files[0]
    }
    req.Caption = r.MultipartForm.Value["caption"][0]  // with bounds-check
    // ... call handler, write response ...
}
```

32 MB is `http.Request.ParseMultipartForm`'s standard ergonomic
default — anything larger gets spooled to disk. Users needing
larger or streaming uploads use raw mode. (A configurable limit
via `goduct.json` is a future ADR; not v0.6.)

Raw uploads emit nothing on the Go side — `goduct:upload` is
informational to the TS/OpenAPI generators per ADR 0031.

**tsclient** for both shapes:

```typescript
upload: async (params: { id: string }, body: { file: File | Blob; caption?: string }): Promise<User> => {
    const fd = new FormData();
    fd.append("file", body.file);
    if (body.caption !== undefined) fd.append("caption", body.caption);
    const data = await request(opts, {
        method: "POST",
        path: `/users/${encodeURIComponent(params.id)}/avatar`,
        body: fd,
    });
    return schemas.User.parse(data);
},
```

The existing `request` helper handles `FormData` body
specifically — when `body instanceof FormData`, skip the
`Content-Type: application/json` header and skip the
`JSON.stringify` (browsers set the multipart boundary header
automatically).

**openapi** emits the operation's `requestBody.content` as
`multipart/form-data` with a schema describing the part shape:

```json
"requestBody": {
  "required": true,
  "content": {
    "multipart/form-data": {
      "schema": {
        "type": "object",
        "properties": {
          "file":    {"type": "string", "format": "binary"},
          "caption": {"type": "string"}
        },
        "required": ["file"]
      }
    }
  }
}
```

For typed uploads the schema is synthesized from the IR's
multipart + form field set; for raw uploads the request type's
JSON-visible fields (the user's request struct) are emitted as
a best-effort schema — without typed multipart tags goduct
doesn't know which fields are files vs text. The README documents
this asymmetry.

**postman** emits the body as Postman's `formdata` mode for both
shapes, with a row per multipart/form field. File fields get
`type: "file"`; text fields get `type: "text"`.

**zod, tstypes, hooks** are unchanged structurally. The request
type's TS interface includes `File | Blob` for multipart fields
and the regular primitives for form fields; zod validates the
text fields normally (file validation is a server-side concern).
React Query hooks for upload routes work identically — the
mutation just passes a FormData-shaped body.

### 5. Validators on multipart fields

- `required` on a multipart file field is enforced at the
  adapter layer: missing file → `goduct.BadRequest("file is
  required")`.
- `max=N` on a multipart file field is NOT enforced by goduct in
  v0.6 — the 32 MB ParseMultipartForm cap is the effective
  ceiling; per-field byte limits are deferred (a future ADR can
  add a `maxbytes` validator or a goduct.json upload-limits
  block).
- `required` and the regular query/header-style validators on
  form (text) fields work identically to query params.

### 6. Coverage

- New analyzer tests: multipart/form tag parsing; the
  `json:` + `multipart:` co-occurrence loud-fail; bad field types
  (a string field tagged multipart, a `*multipart.FileHeader`
  field tagged json).
- New goadapter tests (synthetic IR): typed-mode wrapper for each
  framework; the wrapper calls ParseMultipartForm, populates
  fields, calls the handler.
- New tsclient tests: FormData construction; conditional
  Content-Type header omission when body is FormData.
- New openapi tests: multipart/form-data content type emission
  for both typed and raw modes.
- chi-basic gains a typed upload route (`POST /users/:id/avatar`
  with a `*multipart.FileHeader` + caption text field). One
  golden cascade — bounded, demonstrates the feature.

### 7. Deferred to v0.6.1 or later

- **Multi-file (`[]*multipart.FileHeader`).** Single-file ships in
  v0.6; the slice form is a small follow-up.
- **Configurable upload size limits** via goduct.json. v0.6 trusts
  ParseMultipartForm's 32 MB default; raw mode is the escape hatch
  for larger.
- **Per-field byte-limit validator** (`validate:"maxbytes=..."`).
- **Streaming upload helpers in the runtime** (e.g. a
  `goduct.MultipartParts(r, fn)` callback iterator). For now raw
  handlers use stdlib directly.

## Consequences

**Easy / unblocked:**

- The "I have a profile picture upload" case becomes typed
  end-to-end with one struct tag.
- The "I have a 5 GB log upload" case keeps the raw-mode escape
  hatch and now gets correct TS client + OpenAPI metadata for
  free.
- No new dependencies — stdlib `mime/multipart` is the runtime.

**Hard / giving up:**

- ParseMultipartForm's 32 MB default means typed-mode uploads
  buffer up to 32 MB in memory; bigger uploads need raw mode.
  Documented in the README's Upload section.
- The TS request type uses `File | Blob` for file fields, which
  doesn't exist in Node ESM contexts (Bun/Node 18+ have it
  globally; older runtimes don't). Users in Node-without-File
  environments wrap the type assertion themselves. Documented.
- chi-basic golden cascades again (third cut in three releases).
  Bounded — one new route, one new request struct, no new
  response type.

## Alternatives considered

- **Typed shape only.** Rejected: the raw escape hatch is the
  real fit for streaming/huge uploads; not providing a wire-shape
  hint to TS+OpenAPI for those would leave a usability gap.
- **Raw shape only with a `multipart:"file"` *response*-side hint.**
  Rejected: too far from goduct's source-of-truth-on-the-Go-side
  philosophy.
- **Reuse `json:` tags as multipart text fields.** Rejected: a
  struct that's sometimes JSON and sometimes multipart depending
  on a route is a confusing source of truth.
- **Add a `multipart` go-playground/validator extension.** Out of
  scope. The required-file check is one adapter-side
  conditional; the rest is too speculative for v0.6.

## Cross-references

- [0014](0014-handler-signature-strictness.md) — typed-upload
  routes use the same `func(ctx, T) (*U, error)` signature
  goduct already pins as idiomatic; only the field-tag set
  widens.
- [0015](0015-query-header-optionality-rule.md) — `form:"..."`
  text fields follow the same optionality rule as query params
  (pointer or no `required` → optional).
- [0022](0022-generator-conventions.md) §1 — `Generate`
  signature unchanged; uploads detected per-route via
  `Route.Upload`.
- [0027](0027-enrich-ir-for-go-side-codegen.md) — `Route.Upload`
  + the two new FieldSource values are additive.
- [0031](0031-raw-handlerfunc-mode.md) — `goduct:upload` joins
  `goduct:request` / `goduct:response` as a raw-mode directive;
  the analyzer/raw-mode interaction is unchanged on the Go side.
- [0034](0034-openapi-export.md) — the openapi generator's
  requestBody content-type branch extends to multipart/form-data
  when Route.Upload is true.
- [0041](0041-sse-streaming.md) — same pattern of "new wire
  shape detected from signature/directive, all generators
  branch on a Route.* flag."
