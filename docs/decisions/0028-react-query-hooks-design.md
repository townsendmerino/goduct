# 0028. React Query hooks generator design (v0.2)

**Status:** Accepted
**Date:** 2026-06-02

## Context

[ADR 0008](0008-react-query-deferred-to-v02.md) deferred `--hooks` to
keep v0.1's frontend output framework-agnostic. The CLI shipped
`--hooks` as exit-2-with-pointer-to-v0.2. v0.2 fulfills the deferral:
goduct now generates React Query hooks. This ADR pins the shape.

The chi-basic example exposes 5 routes (2 GETs, 1 POST, 1 PATCH, 1
DELETE) and a clean tsclient surface with tag-grouped methods. The
hooks layer is a thin wrapper over that surface — `useGetUser` calls
`api.users.get`, `useCreateUser` calls `api.users.create`, etc. The
substantive design questions are how the hooks reach the client, how
queries are keyed, and how mutations interact with the query cache.

## Decision

The `--hooks` generator emits a single file `hooks.ts` alongside
`types.ts` / `schemas.ts` / `client.ts`.

### 1. Public API: `createHooks(client)` factory

`hooks.ts` exports one function:

```ts
export function createHooks(client: Client) {
  return { useGetUser, useListUsers, useCreateUser, ... };
}
```

The factory is **symmetric with `createClient`**. The user wires both
once at app boundary; hooks are bound to a specific client at
destructure time. **No React Context**, no `<GoductClientProvider>`
wrapper, no `.tsx`. The user still wraps their app in
`<QueryClientProvider>` themselves — that is React Query's
responsibility, not goduct's.

### 2. Hook naming and kind

- Hook name is `use<HandlerName>` — verbatim from `ir.Route.HandlerName`
  (e.g. `GetUser` → `useGetUser`, `ListUsers` → `useListUsers`).
- **Query (`useQuery`)** when method is `GET`.
- **Mutation (`useMutation`)** for `POST` / `PUT` / `PATCH` / `DELETE`.
- v0.1 supports only those 5 methods (analyzer constraint); other
  methods would loud-fail upstream (ADR 0007), so the generator does
  not branch on them.

### 3. Hook signatures

**Query hooks** take the path/query params object (or no args if the
route has none) plus an optional React Query options bag:

```ts
useGetUser(params: { id: string }, opts?: HookQueryOptions<t.User>)
useListUsers(params: { limit?: number; cursor?: string }, opts?: HookQueryOptions<t.ListUsersResponse>)
```

where `HookQueryOptions<TData> = Omit<UseQueryOptions<TData, GoductError>,
"queryKey" | "queryFn">`. The Omit prevents users from overriding the
queryKey/queryFn (which would defeat the generator). All other React
Query options (enabled, staleTime, select, etc.) pass through.

**Mutation hooks** take only the options bag; the variables shape is
typed via `UseMutationOptions`'s third generic:

```ts
useCreateUser(opts?: HookMutationOptions<t.User, t.CreateUserRequest>)
useUpdateUser(opts?: HookMutationOptions<t.User, { params: { id: string }; body: t.UpdateUserRequest }>)
useDeleteUser(opts?: HookMutationOptions<void, { id: string }>)
```

where `HookMutationOptions<TData, TVars> = Omit<UseMutationOptions<TData,
GoductError, TVars>, "mutationFn">`. Path-only mutations
(DELETE) take the bare params object as TVars; body-only mutations
(POST without path params) take the body type as TVars;
path+body mutations (PATCH /users/:id) take `{ params, body }` as
TVars so `mutate({ params: {id}, body: {...} })` is well-typed.

### 4. Query key shape

`queryKey: [tag, methodName, params]`

`tag` is `ir.Route.Tag` (e.g. `"users"`). `methodName` is the
tag-grouped client method name (`"get"`, `"list"`, ...) — same string
the tsclient emits. `params` is the hook's first argument (or `{}`
for parameter-less queries). Object identity is irrelevant; React
Query hashes the key.

The tag prefix is **load-bearing**: it enables single-call bulk
invalidation per feature (`invalidateQueries({queryKey:["users"]})`),
which is what the mutation auto-invalidation below relies on.

### 5. Mutation auto-invalidation

Every generated mutation calls
`queryClient.invalidateQueries({queryKey: [tag]})` in its `onSuccess`
handler. User-supplied `opts.onSuccess` is invoked **after** the
invalidation with the same arguments, so user code can extend the
behavior without losing the default:

```ts
onSuccess: (data, vars, ctx) => {
  queryClient.invalidateQueries({ queryKey: [tag] });
  opts?.onSuccess?.(data, vars, ctx);
}
```

This trades an extra invalidation in the rare "I don't want to refetch
the list" case for the common-path UX of "create a user, see the list
update automatically." Users override per-call by passing
`opts.onSuccess` and explicitly calling `invalidateQueries` themselves
with the desired shape.

### 6. Error type

The hook's `TError` generic is `GoductError` (imported from
`./client`). Users get typed catch/error-rendering:

```ts
const { error } = useGetUser({ id });
if (error) { error.status; error.code; error.message; }
```

### 7. React Query version target

`@tanstack/react-query` **v5**. v5 is the current major; new projects
default to it. v4 users can upgrade or pin v4 and shim — not a goduct
concern.

### 8. File extension and dependencies

- File extension is `.ts` (no JSX → no `.tsx` needed).
- `@tanstack/react-query` is the only peer dependency users add.
  goduct itself does not depend on react-query (it generates strings).
- Generated `hooks.ts` imports from `./client` (for `Client` type and
  `GoductError`) and `./types` (for response/body types) — the
  existing v0.1 outputs are the API surface.

### 9. Doc comments

`hooks.ts` is user-facing API surface (every hook is something a
component author calls), so doc comments use `gen.JSDocFull` —
preserves multi-sentence handler godoc. Same policy as tsclient
([ADR 0024](0024-doc-comment-emission-policy.md)).

ADR 0024's scope was the v0.1 generators; this ADR extends doc-policy
coverage to the fifth generator. ADR 0024 is not amended.

### 10. Shared helpers go to `internal/gen`

The tag-grouped method-name derivation lives in `internal/generators/tsclient`
as `methodName(handler, tag)`. Per [ADR 0022](0022-generator-conventions.md)
§8, shared logic between generators belongs in `internal/gen`. This
ADR moves `methodName` (and its `pascal` helper) to `internal/gen` and
has both tsclient and hooks call it. The tsclient golden enforces
byte-identity on the move.

## Consequences

**Easy / unblocked:**

- `--hooks` flips from exit-2-with-v0.2-pointer to real codegen.
  `--all` now produces five files instead of four.
- Hooks compose with React Query's full options API (`enabled`,
  `staleTime`, `select`, `retry`, `onSuccess`, ...). The Omit on
  queryKey/queryFn (and mutationFn) is the only restriction.
- Bulk invalidation is one call per feature.
- Adding a future generator that needs the same method-name derivation
  (e.g. a Swift client or a TanStack Router integration) just imports
  `gen.MethodName` — no further duplication.

**Hard / giving up:**

- The default auto-invalidation is *opinionated*. Users with very
  granular cache strategies will sometimes see an extra fetch. The
  override mechanism is one extra `onSuccess` line; we accept this
  cost for the much better default UX.
- Hooks-bound-to-a-client (the factory choice) means consumers cannot
  treat hooks as global, ambient module imports. If a future user
  strongly wants the global form, they can wrap `createHooks` in their
  own re-export. ADR not reopened for this preference.
- Generated `hooks.ts` references `@tanstack/react-query` v5. Users
  on v4 either upgrade or pin to a goduct version that targets v4.
  We accept being v5-current rather than spanning both.

## Alternatives considered

- **React Context + `<GoductClientProvider>` + free-function hooks** —
  rejected. Requires `hooks.tsx` (JSX in Provider), forces a second
  provider wrap on the user, hides which client a hook uses. Idiomatic
  React, but the factory shape is symmetric with `createClient` and
  the simpler answer.
- **Module-singleton via `setClient(api)`** — rejected. Hidden global;
  bad for SSR and test isolation.
- **No automatic mutation invalidation** — rejected for the v0.2
  default. Most users want it; the override is cheap; biasing toward
  "list refreshes after create" is the better default.
- **`[handlerName, args]` query keys** — rejected. Loses bulk
  invalidation; mutation auto-invalidation needs the tag prefix.
- **React Query v4** — rejected as the current major default.
- **Hooks file split per tag** (one file per feature) — rejected;
  premature for v0.2; one `hooks.ts` mirrors `client.ts`.
- **Generate a `<QueryClientProvider>` wrapper for the user** —
  rejected; that is React Query's surface area, not goduct's.

## Cross-references

- [0003](0003-generators-as-pipeline.md) — generator-as-IR-consumer
  contract; hooks is the 5th generator under this.
- [0008](0008-react-query-deferred-to-v02.md) — the original deferral
  this ADR fulfills.
- [0022](0022-generator-conventions.md) — generator conventions; this
  ADR's `--hooks` generator follows §1 (Generate signature), §3
  (determinism), §7 (golden), §8 (shared helpers → `internal/gen`).
- [0023](0023-godoc-to-jsdoc-transformation.md) — godoc → JSDoc
  transform reused via `gen.JSDocFull`.
- [0024](0024-doc-comment-emission-policy.md) — v0.1 generators'
  doc-policy table; hooks adds JSDocFull policy.
