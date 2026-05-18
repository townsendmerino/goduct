# chi-basic example

This is the canonical end-to-end example for goduct. It serves three purposes:

1. **Documentation** — a worked example of every supported feature in v0.1.
2. **Golden test fixture** — `internal/analyzer` and `internal/generators/*` test against this directory. Output must match `testdata/expected/` byte-for-byte (after gofmt/prettier).
3. **Design anchor** — when in doubt about how something should behave, the answer is "whatever makes this example work."

## Layout

```
chi-basic/
├── api/                        Input: hand-written Go handlers
│   └── users.go                One file, six routes, exercises every feature
└── testdata/
    └── expected/               Output: what goduct must produce
        ├── client/             TS frontend artifacts
        │   ├── types.ts        --types
        │   ├── schemas.ts      --zod
        │   └── client.ts       --client (fetch-based)
        └── go/
            └── goduct_routes.go  --go-adapter (chi router wiring)
```

`api/` is a normal package in the root module — the analyzer loads it
directly. The expected output lives under `testdata/` on purpose: the Go
tool ignores any `testdata` directory, so `go build ./...` never tries to
compile `goduct_routes.go` (it imports chi and references handlers from
`api/`, so it is not buildable where it sits — it is a golden text
snapshot, not a package). This keeps the root module and CI green without a
separate module for the example. See `docs/decisions/0013`.

## What this example covers

| Feature                                | Demonstrated by                              |
| -------------------------------------- | -------------------------------------------- |
| GET with path param                    | `GetUser` (`/users/:id`)                     |
| GET with query params                  | `ListUsers` (`limit`, `cursor`)              |
| POST with JSON body and validation     | `CreateUser` (`email`, `min=1`)              |
| PATCH with path param and body         | `UpdateUser`                                 |
| DELETE with no response body           | `DeleteUser` (204)                           |
| Custom success status (`goduct:status`)| `CreateUser` (201), `DeleteUser` (204)       |
| Enum (`type T string` + consts)        | `UserStatus`                                 |
| Nested optional struct                 | `User.Profile`                               |
| Pointer field → optional               | `UpdateUserRequest.Name`, `.Status`          |
| `omitempty` → optional                 | `Profile.AvatarUrl`, `ListUsersResponse.NextCursor` |
| Slice field                            | `Profile.Tags`, `ListUsersResponse.Users`    |
| Tag grouping (`goduct:tag`)            | all routes → `api.users.*`                   |
| Godoc → JSDoc on the client            | every handler                                |

## What this example deliberately doesn't cover

These belong to later milestones — keeping this example tight makes the golden
diff useful. Each gets its own example directory when implemented.

- Raw `http.HandlerFunc` mode (v0.2)
- gin / echo / std mux (v0.2)
- Generics (v0.2)
- React Query hooks (v0.2)
- OpenAPI export (v0.3)
- Streaming / SSE (v0.4)
