# goduct

> A typed conduit between your Go API and your TypeScript client.

Write Go HTTP handlers. Get a fully-typed TypeScript client. No protobuf, no OpenAPI YAML, no codegen rituals.

```bash
go run github.com/townsendmerino/goduct/cmd/goduct gen ./api --out ./web/src/api --all
```

That's it. Your frontend now knows your backend.

---

## Why

If you have a Go backend and a TypeScript frontend, you have a problem: the types drift. You change a struct, forget to update the TS type, and ship a runtime bug that should have been a compile error.

The existing answers each cost something:

- **Hand-write TS types** — works until it doesn't. Always behind.
- **OpenAPI + codegen** — accurate, but you maintain a third source of truth (the spec).
- **gRPC / Buf Connect** — excellent, but requires `.proto` files and a build pipeline.
- **tRPC** — solves it beautifully, if your backend is TypeScript.

goduct takes the tRPC approach for Go: your Go code is the source of truth. Write idiomatic handlers, run one command, get a typed client.

---

## What you write

```go
// api/users.go
package api

import "context"

type GetUserRequest struct {
    ID string `path:"id"`
}

type GetUserResponse struct {
    ID    string `json:"id"`
    Email string `json:"email"`
    Name  string `json:"name"`
}

// goduct:route GET /users/:id
func GetUser(ctx context.Context, req GetUserRequest) (*GetUserResponse, error) {
    user, err := db.FindUser(ctx, req.ID)
    if err != nil {
        return nil, goduct.NotFound("user not found")
    }
    return &GetUserResponse{ID: user.ID, Email: user.Email, Name: user.Name}, nil
}

type CreateUserRequest struct {
    Email string `json:"email" validate:"required,email"`
    Name  string `json:"name"  validate:"required,min=1"`
}

// goduct:route POST /users
func CreateUser(ctx context.Context, req CreateUserRequest) (*GetUserResponse, error) {
    // ...
}
```

## What you get

```typescript
import { createClient } from "./api/client";

const api = createClient({ baseUrl: "/api" });

// Fully typed. Autocomplete works. Response is runtime-validated.
const user = await api.users.get({ id: "u_123" });
console.log(user.email); // TS knows this exists

await api.users.create({
  email: "foo@bar.com",
  name: "Frank",
});
```

And on the Go side, your `main.go` becomes:

```go
r := chi.NewRouter()
r.Use(middleware.Logger)
api.Register(r)        // ← generated; wires every handler to the right route
http.ListenAndServe(":8080", r)
```

One source of truth. Wired on both sides automatically.

---

## Install

```bash
go install github.com/townsendmerino/goduct/cmd/goduct@latest
```

In your frontend project:

```bash
npm install zod                          # only if you generate --zod
npm install @tanstack/react-query        # only if you generate --hooks
```

Both are peer dependencies — install the ones for the generators you use.

---

## The handler convention

goduct supports two handler styles. Pick whichever fits your codebase. The **idiomatic** style (recommended) infers everything from the signature. The raw `http.HandlerFunc` style is for existing codebases or finer control; it requires `goduct:request` / `goduct:response` annotations and is supported on chi and `net/http` mux (gin and echo support is deferred — see [ADR 0031](docs/decisions/0031-raw-handlerfunc-mode.md)).

### Idiomatic (the v0.1 style)

A typed function with a fixed signature. goduct infers everything from the types.

```go
// goduct:route GET /users/:id
func GetUser(ctx context.Context, req GetUserRequest) (*GetUserResponse, error)
```

Request struct fields are sourced from tags:

| Tag                    | Source                   |
| ---------------------- | ------------------------ |
| `path:"id"`            | URL path parameter       |
| `query:"limit"`        | Query string parameter   |
| `header:"X-Trace-Id"`  | Request header           |
| `json:"email"`         | JSON body (POST/PUT/PATCH) |

Validation tags use [go-playground/validator](https://github.com/go-playground/validator) syntax and are translated to zod where possible.

### Raw `http.HandlerFunc`

For existing code or finer control, annotate a standard handler with the request/response types ([ADR 0031](docs/decisions/0031-raw-handlerfunc-mode.md)):

```go
// goduct:route    GET /users/:id
// goduct:request  GetUserRequest
// goduct:response GetUserResponse
func GetUser(w http.ResponseWriter, r *http.Request) {
    var req GetUserRequest
    req.ID = chi.URLParam(r, "id")
    user, err := db.FindUser(r.Context(), req.ID)
    if err != nil { goduct.WriteError(w, err); return }
    goduct.WriteJSON(w, http.StatusOK, GetUserResponse{...})
}
```

goduct cannot verify these annotations match the handler's behavior, so this mode is intended for when you need it, not as the default. v0.4 supports raw routes on every framework: chi/mux register the user's `http.HandlerFunc` directly; gin/echo register a generated context-bridge wrapper per [ADR 0037](docs/decisions/0037-gin-echo-raw-handlerfunc.md).

---

## Generators

Each generator is opt-in. Use what you need.

```bash
goduct gen ./api --out ./web/src/api \
  --types      \   # types.ts             (TS interfaces + types)
  --zod        \   # schemas.ts           (zod schemas for runtime validation)
  --client     \   # client.ts            (fetch-based, typed)
  --hooks      \   # hooks.ts             (React Query hooks; peer dep
                   #                       @tanstack/react-query v5)
  --openapi    \   # openapi.json         (OpenAPI 3.1 spec)
  --swagger-ui \   # swagger-ui.html      (static page; CDN-loaded UI)
  --postman    \   # postman_collection.json (Postman v2.1 collection)
  --go-adapter     # api/goduct_routes.go (chi wiring, written beside your source)
```

Or just `--all`.

### `--types`
Plain TypeScript types. No runtime dependencies. Smallest output.

### `--zod`
zod schemas for every type. Lets the client validate responses at runtime — useful when the backend version is ahead of the frontend.

### `--client`
A typed fetch wrapper. Methods are grouped by tag (or by URL prefix if no tag).

```typescript
api.users.get({ id })
api.users.list({ limit: 10 })
api.users.create({ email, name })
api.posts.list()
```

### `--hooks`
React Query hooks for every endpoint. GET routes emit `useQuery` wrappers; everything else emits `useMutation` wrappers with auto tag-invalidation on success (see [ADR 0028](docs/decisions/0028-react-query-hooks-design.md) for the design pins).

The generator emits a `createHooks(client)` factory — symmetric with `createClient`, no React Context, no Provider wrap. Wire once at the app boundary:

```typescript
import { createClient } from "./api/client";
import { createHooks } from "./api/hooks";

const api = createClient({ baseUrl: "/api" });
const { useGetUser, useCreateUser, useListUsers } = createHooks(api);

// in a component:
const { data, isLoading } = useGetUser({ id: "u_123" });

const createUser = useCreateUser();
await createUser.mutateAsync({ email: "foo@bar.com", name: "Frank" });
```

Mutations on a given tag (e.g. `users`) auto-invalidate the `[tag]` query-key prefix on success, so a `useCreateUser` mutation refreshes `useListUsers` without manual wiring. Override via the standard `opts.onSuccess`. Errors are typed as `GoductError`.

Peer dependency: `@tanstack/react-query` v5. The user wraps their app in `<QueryClientProvider>` themselves — that is React Query's surface area, not goduct's.

### `--openapi`
An OpenAPI 3.1 JSON document describing every route and type, suitable for Swagger UI, Postman, Stoplight, Redoc, or any OpenAPI-aware consumer ([ADR 0034](docs/decisions/0034-openapi-export.md)). Framework-independent — the same `openapi.json` falls out regardless of `--framework`. Generics flatten per-instantiation: `*Page[User]` produces a `Page_User` component schema; `*Page[Result[User, Err]]` produces `Page_Result_User_Err`. Errors surface as a synthesized `GoductError` component referenced by every operation's `default` response.

Convert to YAML downstream if needed (`yq -P openapi.json > openapi.yaml`); goduct emits JSON only to keep its dep footprint tiny.

### `--swagger-ui`
A one-file static HTML page that loads Swagger UI v5 from the public CDN and points at the sibling `./openapi.json` ([ADR 0035](docs/decisions/0035-openapi-sibling-generators.md)). Drop it next to `openapi.json` on any static host (S3, GitHub Pages, an `nginx` location, your dev server) and you have interactive API docs at zero infra cost. Zero configuration; users with strong opinions edit the HTML themselves.

### `--postman`
A Postman collection v2.1 ready to import. Routes are grouped into folders by tag; path parameters use Postman's native `:name` syntax; a `{{baseUrl}}` collection variable (default `http://localhost:8080`) is overridable per Postman environment. Body methods get a synthesized JSON example body with type-appropriate placeholders for each wire-visible field (the user fills in real values in Postman). Deterministic `_postman_id` (derived from the package name) means re-generating doesn't churn the file in git.

### `--go-adapter`
A `Register(...)` function that wires every handler to the right route, decodes path/query/body into your request struct, and serializes the response. Errors flow through `goduct.WriteError` and produce a consistent wire format. Defaults to chi; pick a framework with `--framework chi|gin|echo|mux` (chi default, [ADR 0030](docs/decisions/0030-framework-adapter-selection.md)):

```bash
goduct gen ./api --out ./web/src/api --go-adapter --framework gin
```

Generated output imports the chosen framework (or stdlib for mux on Go 1.22+) and uses its native handler shape. Raw `http.HandlerFunc` handlers ([ADR 0031](docs/decisions/0031-raw-handlerfunc-mode.md)) work on every framework: chi/mux register the user's function directly; gin/echo register a generated 3-line context-bridge wrapper ([ADR 0037](docs/decisions/0037-gin-echo-raw-handlerfunc.md)).

## `--watch`

Re-run the requested generators on source-file change:

```bash
goduct gen ./api --out ./web/src/api --all --watch
```

Uses `fsnotify` over the source package's directory; debounces 250 ms; ignores `_test.go` and the adapter's own output to avoid regen loops. The first run aborts on error like normal `goduct gen`; subsequent regens during the watch session print errors but keep watching (so transient compile errors mid-edit don't kill the loop). `Ctrl-C` exits cleanly. See [ADR 0029](docs/decisions/0029-watch-mode-design.md).

---

## Errors

goduct ships a tiny error package that gives you typed errors on both sides.

**Go:**

```go
import goduct "github.com/townsendmerino/goduct/runtime"

return goduct.NotFound("user not found")
return goduct.BadRequest("invalid email")
return goduct.Unauthorized("token expired")
return goduct.Conflict("email already in use")
return goduct.Internal(err) // wraps an unknown error; logged, returns 500
```

**TypeScript:**

```typescript
try {
  await api.users.get({ id });
} catch (e) {
  if (e instanceof GoductError) {
    e.status   // 404
    e.code     // "not_found"
    e.message  // "user not found"
  }
}
```

Wire format is stable:

```json
{ "code": "not_found", "message": "user not found" }
```

---

## What's supported

**Frameworks:** chi (default), gin, echo, `net/http` mux (Go 1.22+) — pick one via `--framework chi|gin|echo|mux` ([ADR 0030](docs/decisions/0030-framework-adapter-selection.md)).

**Go types:** primitives, structs, slices, maps with string keys, pointers (`*T` → optional), enums (`type Status string` + consts → TS string union), and these special types ([ADR 0017](docs/decisions/0017-special-stdlib-types.md)):

| Go type | Wire / TypeScript |
| --- | --- |
| `time.Time` | ISO 8601 string |
| `time.Duration` | number (int64 nanoseconds) |
| `[]byte` | base64 string |
| `json.RawMessage` | `unknown` (JSON passthrough) |
| `github.com/google/uuid.UUID` | string |

Other rich types (`decimal.Decimal`, `big.Int`, `net/url.URL`, `civil.Date`, custom `MarshalJSON`, …) declare their wire shape via the `--adapter` flag ([ADR 0032](docs/decisions/0032-custom-type-adapters.md)). Without an adapter, goduct errors loudly with a `file:line` pointer and a remediation pointer to `--adapter` — no silent skipping.

```bash
goduct gen ./api --out ./web --all \
  --adapter github.com/shopspring/decimal.Decimal=string \
  --adapter math/big.Int=string
```

Wire shapes: `string`, `number`, `boolean`, `unknown`. The user's `MarshalJSON` (or default JSON encoding) is the source of truth on the Go side; goduct just renders the wire-shape on the TS/zod side.

**Validation tags** (translated to zod): `required`, `email`, `url`, `min`, `max`, `len`, `oneof` (on string fields → `z.enum([...])`). See [ADR 0006](docs/decisions/0006-validation-tag-translation.md). Tags zod can't express are not enforced client-side but still run server-side via go-playground/validator.

**Frontend:** TypeScript types, zod schemas, typed fetch client, React Query hooks ([ADR 0028](docs/decisions/0028-react-query-hooks-design.md); peer dep `@tanstack/react-query` v5).

**Known polish:** a struct reachable only via a `type A B` alias emits as a duplicate interface rather than a TS alias; the Go adapter maps the 200/201/204 status codes the v0.1 analyzer produces (an explicit non-standard `goduct:status` loud-fails per [ADR 0007](docs/decisions/0007-loud-failure-on-unsupported-input.md)).

**Generics:** `type Page[T any] struct{...}` and instantiations like `*Page[User]` work end-to-end ([ADR 0033](docs/decisions/0033-generics.md)). Multi-param (`Result[T, E]`) supported. v0.4 ([ADR 0036](docs/decisions/0036-constraint-generics.md)) accepts **type-union constraints** like `[T int | int64]` and renders them as `<T extends number>` (dedup applied) — method-bearing constraints (`Stringer`), `comparable`, and `~`-tilde forms still loud-fail. Generic enums/aliases and generic handler signatures themselves are out of scope.

**Raw HandlerFunc on every framework:** `func(w, r)` handlers with `goduct:request`/`goduct:response` annotations now work on all four frameworks. chi/mux register the user's function directly; gin/echo register a generated 3-line context-bridge wrapper ([ADR 0037](docs/decisions/0037-gin-echo-raw-handlerfunc.md)).

**Project config (`goduct.json`):** drop a `goduct.json` at the project root to stop repeating flags ([ADR 0038](docs/decisions/0038-project-config-file.md)). Schema mirrors the CLI 1:1 plus an `openapi` block (title/version/description/servers) and a `security` block. CLI flags override config; config overrides built-in defaults. Unknown keys loud-fail.

```json
{
  "pattern":    "./api",
  "out":        "./web/src/api",
  "generators": ["types", "zod", "client", "hooks", "go-adapter"],
  "framework":  "chi",
  "openapi":    {"title": "My API", "version": "1.0.0"},
  "security":   {
    "schemes":      {"bearerAuth": {"type": "http", "scheme": "bearer"}},
    "requirements": [{"bearerAuth": []}]
  }
}
```

**OpenAPI polish ([ADR 0039](docs/decisions/0039-openapi-polish-trio.md), [ADR 0040](docs/decisions/0040-v04-closure-pass.md)):** `goduct:example {...}` attaches a real JSON example to a response body; `goduct:requestexample {...}` does the same for request bodies; `goduct:errorresponse <status> <Type>` declares per-status error response shapes; security schemes flow from `goduct.json` into `components.securitySchemes` plus a document-level `security` array; `goduct:security <scheme>` (or `goduct:security none`) on a handler overrides the document default for that operation.

**Server-Sent Events ([ADR 0041](docs/decisions/0041-sse-streaming.md)):** a handler with signature `func(ctx, T) (<-chan E, error)` becomes an SSE endpoint. The generated TS client method returns `AsyncIterable<E>` (iterate with `for await`); the four framework adapters all delegate to a generic `goduct.SSEStream` runtime helper that handles headers, flushing, and ctx-cancel. OpenAPI emits `text/event-stream`. Postman and React Query hooks skip streaming routes (Postman doesn't model SSE; React Query v5 has no first-class iterator hook); users call the AsyncIterable directly.

**File uploads ([ADR 0042](docs/decisions/0042-file-uploads.md)):** two shapes share the same TS-client + OpenAPI surface. The **typed shape** mixes `*multipart.FileHeader` fields tagged `multipart:"..."` with text fields tagged `form:"..."` in the same request struct; the generated adapter calls `ParseMultipartForm` and populates each field. The **raw shape** is an existing raw handler plus `goduct:upload`; the user owns the multipart parsing while goduct still emits the FormData-aware TS client + `multipart/form-data` OpenAPI metadata. `json:` and `multipart:`/`form:` on the same struct loud-fail (a wire format is one or the other). 32 MB is the typed-mode in-memory cap (`ParseMultipartForm` default); larger uploads use raw mode.

```go
type UploadAvatarRequest struct {
    UserID  string                `path:"id"        validate:"required"`
    File    *multipart.FileHeader `multipart:"file" validate:"required"`
    Caption string                `form:"caption"`
}

// goduct:route POST /users/:id/avatar
func UploadAvatar(ctx context.Context, req UploadAvatarRequest) (*User, error) {
    f, _ := req.File.Open()
    defer f.Close()
    // ... save to storage ...
}
```

```typescript
await api.users.uploadAvatar({ id: "u_1" }, { file: selectedFile, caption: "selfie" });
```

```go
// goduct:route GET /orders/:id/events
// goduct:tag   orders
func WatchOrders(ctx context.Context, req WatchOrdersRequest) (<-chan OrderEvent, error) {
    out := make(chan OrderEvent)
    go func() {
        defer close(out)
        for e := range source {
            select {
            case <-ctx.Done(): return
            case out <- e:
            }
        }
    }()
    return out, nil
}
```

```typescript
for await (const event of api.orders.watch({ id: "o_1" })) {
    console.log(event);
}
```

**Not yet supported (planned):** SSE/streaming; WebSockets; file upload helpers. See the [Roadmap](#roadmap).

---

## How it works (short version)

1. `go/packages` loads your code with full type information.
2. The analyzer walks function declarations, looking for `// goduct:route` comments.
3. For each route, it builds an intermediate representation (IR): method, path, params, request/response types, validation rules, status code.
4. Each generator consumes the IR and emits one file. Generators don't talk to each other.
5. Output is gofmt'd / prettier'd before writing.

The IR is the contract. If you want to add a generator (e.g. SolidJS, Swift client), implement one function: `Generate(*ir.API, io.Writer) error`.

---

## Comparison

|                              | goduct          | tRPC           | gRPC / Connect | OpenAPI codegen | tygo / guts |
| ---------------------------- | --------------- | -------------- | -------------- | --------------- | ----------- |
| Backend language             | Go              | TypeScript     | any            | any             | Go          |
| Source of truth              | Go code         | TS code        | .proto         | YAML / JSON     | Go structs  |
| Generates TS types           | ✓               | ✓ (inferred)   | ✓              | ✓               | ✓           |
| Generates TS client          | ✓               | ✓              | ✓              | ✓               | ✗           |
| Generates Go router wiring   | ✓               | n/a            | ✓              | partial         | ✗           |
| Runtime response validation  | ✓ (zod)         | ✓ (zod)        | ✓              | varies          | ✗           |
| Build step needed            | one command     | none           | yes            | yes             | one command |

---

## Roadmap

**v0.1** — chi, idiomatic handlers, types + zod + typed fetch client + go-adapter, basic validation, typed errors.

**v0.2** — React Query hooks (`--hooks`), gin + echo + std `net/http` mux adapters (`--framework`), raw `http.HandlerFunc` mode, the `oneof` validator, `--watch` mode, custom type adapters (`--adapter`, e.g. `decimal.Decimal` → `string`).

**v0.3** — Generics in request/response types, OpenAPI 3.1 export (`--openapi`), Swagger UI page (`--swagger-ui`), Postman collection (`--postman`).

**v0.4** — Type-union generic constraints (`[T int | int64]` → `<T extends number>`), gin/echo raw `http.HandlerFunc` via context-bridge wrappers, `goduct.json` project config, OpenAPI polish trio (`goduct:example`, security schemes, `goduct:errorresponse`).

**v0.4.1** — Closure pass: `goduct:requestexample`, per-handler `goduct:security <scheme>` override, plus chi-basic coverage for the `url`/`len` validators, combined path+query argument object, and `bool`/`float` query-param conversion (the v0.2-era spec-trust caveats).

**v0.5** — Server-Sent Events: handlers with `func(ctx, T) (<-chan E, error)` shape become SSE endpoints; TS client returns `AsyncIterable<E>`; the four framework adapters share a `goduct.SSEStream` runtime helper.

**v0.6** (this release) — File uploads in two shapes: typed multipart (request struct mixes `multipart:"file"` and `form:"caption"` tags) and a raw-mode `goduct:upload` toggle. Stdlib only, 32 MB typed cap; chi-basic ships an UploadAvatar route.

**v0.7** — WebSocket bridge (probably), plus the deferrals in [TODO.md](TODO.md).

**Maybe** — Swift client, Kotlin client, Python client. These follow the same pattern: implement a `Generator`, consume the IR.

---

## Status

Pre-1.0. The handler convention and IR shape are stable; the generated output may change cosmetically (formatting, helper names) before v1.0. Pin a version in your build script.

## Contributing

Issues and PRs welcome. The fastest way to help: try goduct on a real project, file an issue when it falls over, paste the Go code that broke it. Edge cases are the product.

## License

MIT.
