# goduct

> A typed conduit between your Go API and your TypeScript client.

Write Go HTTP handlers. Get a fully-typed TypeScript client. No protobuf, no OpenAPI YAML, no codegen rituals.

```bash
go run github.com/townsendmerino/goduct/cmd/goduct gen ./api --out ./web/src/api
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
npm install zod
```

(zod is the only runtime dependency, and only if you generate validators.)

---

## The handler convention

goduct supports two handler styles. Pick whichever fits your codebase.

### Idiomatic (recommended)

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

For existing code or finer control, annotate a standard handler:

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

goduct can't verify these annotations match the handler's behavior, so use this mode when you need it, not as the default.

---

## Generators

Each generator is opt-in. Use what you need.

```bash
goduct gen ./api --out ./web/src/api \
  --types         \   # types.ts        (TS interfaces + types)
  --zod           \   # schemas.ts      (zod schemas for runtime validation)
  --client        \   # client.ts       (fetch-based, typed)
  --hooks         \   # hooks.ts        (React Query hooks)
  --go-adapter        # api/goduct_routes.go (chi wiring)
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
React Query hooks for every endpoint:

```typescript
const { data, isLoading } = useGetUser({ id });
const createUser = useCreateUser();
await createUser.mutateAsync({ email, name });
```

### `--go-adapter`
A `Register(chi.Router)` function that wires every handler to the right route, decodes path/query/body into your request struct, runs validation if generated, and serializes the response. Errors flow through `goduct.WriteError` and produce a consistent wire format.

---

## Errors

goduct ships a tiny error package that gives you typed errors on both sides.

**Go:**

```go
import "github.com/townsendmerino/goduct/runtime/goduct"

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

## What's supported (v0.1)

**Frameworks:** chi
**Go types:** primitives, structs, slices, maps with string keys, pointers, `time.Time` (→ ISO 8601 string), `[]byte` (→ base64 string), `*T` (→ optional), enums (`type Status string` + consts → TS string union)
**Validation tags:** `required`, `email`, `url`, `min`, `max`, `len`, `oneof`
**Frontend:** fetch client, zod schemas, React Query hooks

**Not yet supported (planned):** gin, echo, std `net/http` mux; generics; custom `MarshalJSON`; SSE/streaming; WebSockets; OpenAPI export; gRPC bridging.

When goduct sees something it can't represent, it errors loudly with a file:line pointer — no silent skipping.

---

## How it works (short version)

1. `go/packages` loads your code with full type information.
2. The analyzer walks function declarations, looking for `// goduct:route` comments.
3. For each route, it builds an intermediate representation (IR): method, path, params, request/response types, validation rules, status code.
4. Each generator consumes the IR and emits one file. Generators don't talk to each other.
5. Output is gofmt'd / prettier'd before writing.

The IR is the contract. If you want to add a generator (e.g. SolidJS, Swift client), implement one function: `Generate(ir.API, io.Writer) error`.

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

**v0.1** (this release) — chi, idiomatic handlers, types + zod + fetch + hooks + go-adapter, basic validation, typed errors.

**v0.2** — Raw `http.HandlerFunc` mode, gin support, generics, custom type adapters (e.g. `decimal.Decimal` → `string`), `--watch` mode.

**v0.3** — OpenAPI 3.1 export, Swagger UI generator, Postman collection export.

**v0.4** — SSE / streaming responses, file upload helpers, WebSocket bridge (probably).

**Maybe** — Swift client, Kotlin client, Python client. These follow the same pattern: implement a `Generator`, consume the IR.

---

## Status

Pre-1.0. The handler convention and IR shape are stable; the generated output may change cosmetically (formatting, helper names) before v1.0. Pin a version in your build script.

## Contributing

Issues and PRs welcome. The fastest way to help: try goduct on a real project, file an issue when it falls over, paste the Go code that broke it. Edge cases are the product.

## License

MIT.
