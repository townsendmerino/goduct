# 0043. v0.6.1 closure pass: chi-basic SSE demo + upload polish (v0.6.1)

**Status:** Accepted
**Date:** 2026-06-02

## Context

Four items deferred from v0.5 / v0.6 have their triggers activated
by a closure-pass request:

- **chi-basic SSE demo** (deferred per
  [ADR 0041 §7](docs/decisions/0041-sse-streaming.md)). Without it,
  the SSE feature ships behind unit tests only; the demo example
  doesn't surface the new wire shape to a reader of the repo.

- **Multi-file uploads** (`[]*multipart.FileHeader`), deferred per
  [ADR 0042 §7](docs/decisions/0042-file-uploads.md). v0.6 ships
  single-file only; the slice form is a mechanical extension.

- **Configurable upload size limits via `goduct.json`**, deferred
  per the same ADR. v0.6 trusts `ParseMultipartForm`'s 32 MiB
  default; some users will want bigger.

- **`maxbytes=N` per-field byte-limit validator**, deferred per the
  same ADR. Today an oversized file just trips the
  ParseMultipartForm ceiling with a generic error; a per-field
  limit gives a useful field-named rejection.

Two items from the same TODO.md bucket are **intentionally
excluded** from this pass — their original triggers haven't fired
and they're bigger than they look:

- **Named SSE events** (`event: foo\ndata: {...}\n\n`). Needs a
  discriminated-union representation in the IR that goduct doesn't
  model, OR a partial-answer convention (every event gets
  `event: <TypeName>` automatically) that would need its own
  design ADR.
- **Last-Event-ID / auto-reconnect.** Needs stateful resumption
  the runtime can't supply generically (only the user's event
  source knows how to "resume from ID X"), plus a new IR/runtime
  contract for "events carry IDs." Out of scope until a real
  user reports needing it.

Both stay in [TODO.md](../../TODO.md) with their original triggers
preserved.

## Decision

### 1. chi-basic SSE demo

Add a streaming route + per-event type to `examples/chi-basic/api/`:

```go
type UserEvent struct {
    UserID string    `json:"userId"`
    Action string    `json:"action"`
    At     time.Time `json:"at"`
}

// goduct:route GET /users/:id/events
// goduct:tag   users
func WatchUserEvents(ctx context.Context, req WatchUserEventsRequest) (<-chan UserEvent, error) {
    out := make(chan UserEvent)
    go func() {
        defer close(out)
        <-ctx.Done()
    }()
    return out, nil
}
```

The handler body is a no-op that exits on `ctx.Done()` — the
golden tests care about the generated wire shape, not runtime
behavior. The demo cascades into types.ts, schemas.ts, client.ts
(streamSSE scaffold helper now actually emitted), openapi.json
(text/event-stream content type), and the four framework adapter
goldens (per-framework streaming wrapper). Postman and hooks skip
the route per ADR 0041.

### 2. Multi-file uploads (`[]*multipart.FileHeader`)

A multipart field may be either `*multipart.FileHeader` (single,
v0.6 shape) or `[]*multipart.FileHeader` (multi, this ADR). The
analyzer's `isMultipartFileHeader` accepts both; the IR TypeRef
distinguishes them via `Kind`:

- Single: `{Kind: KindBuiltin, Builtin: "multipart.FileHeader"}`
- Multi: `{Kind: KindSlice, Element: &{KindBuiltin, Builtin: "multipart.FileHeader"}}`

Generator behavior diverges naturally per Kind:

- **goadapter** for multi-file: `req.X = r.MultipartForm.File["name"]`
  (assign the slice directly; the request struct field type is
  `[]*multipart.FileHeader`). Required-check is `len(files) == 0`.
- **tstypes**: `(File | Blob)[]` for multi vs `File | Blob` for single.
- **zod**: `z.array(z.any())` for multi vs `z.any()` for single
  (file objects don't cleanly validate client-side either way).
- **openapi**: `{type: "array", items: {type: "string", format: "binary"}}`
  for multi vs `{type: "string", format: "binary"}` for single.
- **postman**: one formdata row per field; multi-file fields get
  `type: "file"` same as single (Postman's UI prompts the user to
  pick multiple files for each).
- **tsclient FormData**: multi loops `body.x.forEach(f => fd.append("name", f))`;
  single is the current `fd.append("name", body.x)`.

### 3. `goduct.json` `upload.maxBytes`

A new optional `upload` block in `goduct.json`:

```json
{
  "upload": {
    "maxBytes": 67108864
  }
}
```

`maxBytes` is the size passed to `ParseMultipartForm` (the
in-memory ceiling before spooling to disk). Default stays at
32 MiB (`32 << 20` = 33554432). Per
[ADR 0038](docs/decisions/0038-project-config-file.md) precedence,
nothing on the CLI surfaces this — it's config-only because
upload limits are a project-wide concern, not per-invocation.

`ir.Meta` gains `UploadMaxBytes int64` (additive per ADR 0027).
The goadapter wrapper uses `<api.Meta.UploadMaxBytes>` when
non-zero, else falls back to `32 << 20`.

### 4. `validate:"maxbytes=N"` validator

Per-field byte limit on multipart file fields. The existing
`parseValidate` already turns this into
`ir.ValidationRule{Name: "maxbytes", Arg: "N"}` — no parser
change. Enforcement lives in the goadapter wrapper:

```go
if files := r.MultipartForm.File["file"]; len(files) > 0 {
    req.File = files[0]
} else {
    goduct.WriteError(w, goduct.BadRequest("file is required"))
    return
}
if req.File.Size > 1048576 {
    goduct.WriteError(w, goduct.BadRequest("file exceeds 1048576 byte limit"))
    return
}
```

For multi-file fields, the check is per-element in a loop.

The validator does NOT surface in OpenAPI / zod for v0.6.1 — both
would be partial (OpenAPI's `maxLength` is character-count not
byte-count; zod can't see the file size client-side without
inspecting File.size, which is fine but adds noise). Server-side
enforcement is the contract; client-side is the user's job.

### 5. Coverage

- chi-basic: new `WatchUserEvents` route + `UserEvent` type
  (exercises SSE); `UploadAvatarRequest` extended with a
  `Thumbnails []*multipart.FileHeader` field (exercises multi-file)
  and a `maxbytes=1048576` on `File` (exercises the new
  validator). All 11 chi-basic goldens regenerate.
- New analyzer tests for the multi-file type acceptance.
- New goadapter test for multi-file emission (synthetic IR;
  ensures the four-framework shape holds).
- New cliconfig test for the `upload.maxBytes` parse.
- README "What's supported" mentions multi-file and the size
  knob; TODO.md drops the four closed items and keeps the two
  designed-out ones with their triggers intact.

## Consequences

**Easy / unblocked:**

- Newcomers reading chi-basic now see both new v0.5 (SSE) and
  v0.6 (uploads) wire shapes in one place.
- Multi-file uploads work end-to-end with no per-file Go
  bookkeeping at the user's call site.
- Projects with bigger-than-32-MiB upload requirements stop having
  to fall back to raw mode just for size.
- Per-field size-limit errors name the offending field instead of
  surfacing as a generic "invalid multipart form".

**Hard / giving up:**

- chi-basic's golden diff is the third cascade in three releases.
  Reviewer cost matches v0.4.1 / v0.5 / v0.6's pattern; same
  mitigation (the source change is bounded and ADR-authorized).
- `goduct.json upload.maxBytes` is baked into the generated
  adapter at codegen time, not read at runtime. Changing it
  requires regenerating. Acceptable for v0.6.1; a runtime config
  surface is a future ADR if needed.
- The two excluded SSE items remain spec-trust deferrals. README
  documents them via the v0.5 section; users wanting them open
  an issue and we revisit.

## Alternatives considered

- **Ship all six items in this pass.** Rejected per the
  trigger-discipline: named events + Last-Event-ID need design
  work that justifies their own ADR.
- **Bake `maxBytes` into the runtime helper (read at startup
  from env / config).** Rejected: goduct's runtime stays a
  thin shipped library; pulling in config-loading logic widens
  it. Codegen-time injection is simpler.
- **Surface `maxbytes` in OpenAPI / zod.** Deferred: partial
  representations are worse than no representation; server-side
  enforcement covers the security concern.

## Cross-references

- [0027](0027-enrich-ir-for-go-side-codegen.md) — `Meta.UploadMaxBytes`
  is additive.
- [0038](0038-project-config-file.md) — the `upload` block joins
  the existing `openapi` + `security` blocks in goduct.json.
- [0041](0041-sse-streaming.md) §7 — the chi-basic SSE demo
  closes this ADR's spec-trust note. Named events + Last-Event-ID
  remain deferred there.
- [0042](0042-file-uploads.md) §7 — multi-file + configurable
  size limit + maxbytes validator are the three named deferrals
  closing in this ADR.
