# 0041. SSE streaming responses (v0.5)

**Status:** Accepted
**Date:** 2026-06-02

## Context

Through v0.4.1 every goduct route returns one body (or no body for
204). That covers REST CRUD but not the growing class of endpoints
that push events: order-status changes, log tails, agent
intermediate steps, dashboard tickers. Users currently drop to raw
`http.HandlerFunc` mode (ADR 0031 / 0037) for these, losing typed
generation on both sides.

v0.5 opens streaming. The minimum viable cut is **server-sent
events** (SSE): one-way server→client, JSON-encoded payloads,
HTTP/1.1-friendly, no framework dependencies. File upload and
WebSocket are deferred — they have different shapes (request-side
streaming and full duplex respectively) and merit separate ADRs.

The handler signature choice was made before this ADR (channel
return — see "Alternatives considered"). What remains: how
goduct represents the streaming intent in the IR, how each
generator handles it, and what's deferred to v0.5.1.

## Decision

### 1. Detection

A function is an **SSE handler** iff:

- It carries `goduct:route` AND
- Its signature is **exactly**
  `func(context.Context, T) (<-chan E, error)` where:
  - T is a named struct in the handler's package (same rule as
    idiomatic mode per ADR 0014),
  - E is a named struct in the handler's package (the per-event
    type; not a builtin, not a slice, not a map),
  - the channel is **receive-only** (`<-chan`, not `chan`).

No directive is required. The signature is the signal — the
receive-only-channel-of-named-struct return shape is uncommon
enough that finding it in a `goduct:route` function unambiguously
means "stream this." A `chan E` (bidirectional) return shape is a
loud-fail with a "did you mean `<-chan E`?" hint.

`goduct:status` may set the success status (default 200; 201 / 204
are nonsensical for streams and loud-fail). All other directives
(`goduct:tag`, `goduct:errorresponse`, `goduct:security`,
`goduct:example`) work identically — the example attaches to the
single-event schema, errorresponse declares per-status error
shapes for the *initial* response (before the stream opens).
`goduct:requestexample` works for body-allowed methods.

### 2. IR

```go
// ir.go (additive, ADR 0027):
type Route struct {
    ...existing...
    // StreamType is non-nil iff this is an SSE route (ADR 0041).
    // Points at the per-event type (KindNamed; same-package named
    // struct per the detection rule). ResponseType is nil for
    // streaming routes — there's no single-body response to
    // describe.
    StreamType *TypeRef
}
```

Generators check `Route.StreamType` first; non-nil routes go down
the streaming code path. Existing code paths (which check
`Route.ResponseType`) see streaming routes as "no response body" —
they skip body emission entirely, which is the right default for
generators that don't yet handle streaming (postman, hooks).

### 3. Runtime helper: `goduct.SSEStream`

To keep per-framework generated wrappers tiny, a generic runtime
helper does the header-set-once + write-loop work:

```go
// runtime/sse.go (new):
func SSEStream[E any](ctx context.Context, w http.ResponseWriter, ch <-chan E) {
    fl, ok := w.(http.Flusher)
    if !ok {
        return // ResponseWriter doesn't support flushing; bail
    }
    for {
        select {
        case <-ctx.Done():
            return
        case e, ok := <-ch:
            if !ok {
                return // channel closed by handler
            }
            b, err := json.Marshal(e)
            if err != nil {
                return // malformed event; bail
            }
            fmt.Fprintf(w, "data: %s\n\n", b)
            fl.Flush()
        }
    }
}
```

Generic over E so the marshal stays type-safe. Lives in
`runtime/` because every generated adapter imports it.

### 4. goadapter: framework wrappers

Each framework's wrapper for an SSE route is the standard
param-assignment + handler-call block, followed by header set +
delegation to `goduct.SSEStream`:

```go
// chi (mux is identical except for path-param extraction)
func handleWatchOrders(w http.ResponseWriter, r *http.Request) {
    var req WatchOrdersRequest
    req.ID = chi.URLParam(r, "id")
    ch, err := WatchOrders(r.Context(), req)
    if err != nil {
        goduct.WriteError(w, err)
        return
    }
    w.Header().Set("Content-Type", "text/event-stream")
    w.Header().Set("Cache-Control", "no-cache")
    w.WriteHeader(http.StatusOK)
    goduct.SSEStream(r.Context(), w, ch)
}
```

For gin and echo the only differences are the writer expression
(`c.Writer` / `c.Response().Writer`) and the context expression
(`c.Request.Context()` / `c.Request().Context()`) — both already
captured in ADR 0030's framework table.

### 5. tsclient: AsyncIterable

Streaming methods change return type from `Promise<T>` to
`AsyncIterable<T>`:

```typescript
async function* watchOrders(params: WatchOrdersRequest): AsyncIterable<OrderEvent> {
    const res = await fetch(buildUrl("/orders/events", params), { method: "GET" });
    if (!res.ok) throw await parseError(res);
    const reader = res.body!.getReader();
    const decoder = new TextDecoder();
    let buffer = "";
    while (true) {
        const { value, done } = await reader.read();
        if (done) return;
        buffer += decoder.decode(value, { stream: true });
        let idx: number;
        while ((idx = buffer.indexOf("\n\n")) !== -1) {
            const block = buffer.slice(0, idx);
            buffer = buffer.slice(idx + 2);
            for (const line of block.split("\n")) {
                if (line.startsWith("data: ")) {
                    yield JSON.parse(line.slice(6)) as OrderEvent;
                }
            }
        }
    }
}
```

The user iterates with `for await`:

```typescript
for await (const event of api.orders.watchOrders({ id: "u_1" })) {
    console.log(event);
}
```

Calls into the existing `createClient` shape; the streaming method
is registered alongside the regular ones, just with a different
return type.

### 6. openapi: text/event-stream content type

The OpenAPI 3.1 representation uses `text/event-stream`. The
schema describes the per-event payload (the same TypeRef E from
the channel):

```json
"/orders/events": {
  "get": {
    "responses": {
      "200": {
        "description": "Server-Sent Events stream of OrderEvent",
        "content": {
          "text/event-stream": {
            "schema": { "$ref": "#/components/schemas/OrderEvent" }
          }
        }
      }
    }
  }
}
```

The default `default` -> GoductError response still emits per
existing convention.

### 7. Deferred to v0.5.1 or later

- **chi-basic SSE demo.** Adding a streaming route to chi-basic
  would cascade into a third golden regen in three release cuts.
  v0.5 ships unit-test-only coverage (synthetic IR drives all four
  framework wrappers + tsclient + openapi paths). A v0.5.1
  closure pass adds the chi-basic demo if needed (same pattern as
  ADR 0040 closing ADR 0039's errorresponse spec-trust).
- **React Query hooks.** `@tanstack/react-query` v5 has no
  first-class subscription/iterator hook; representing an
  AsyncIterable as a hook means picking between several
  community-pattern shapes. Out of scope for v0.5; hooks emission
  skips streaming routes (no hook is generated for them).
- **Postman collection.** Postman v2.1 doesn't model SSE well —
  the collection would show a regular GET that hangs. Postman
  skips streaming routes; the openapi.json + swagger-ui carry the
  documentation.
- **Named events** (`event: foo\ndata: {...}\n\n`). v0.5 emits
  only nameless `data:`-only events. Named events would need a
  discriminated-union representation of the event type, which
  goduct's IR doesn't currently model.
- **Last-Event-ID / reconnect.** Out of scope for v0.5. The
  generated TS client doesn't auto-reconnect; users wrap their
  own retry logic. A future ADR can add it without breaking the
  v0.5 contract.
- **File upload, WebSocket.** Separate ADRs.

## Consequences

**Easy / unblocked:**

- A user adds streaming to one handler — no framework switch, no
  raw-mode escape. The TS client method shape change
  (`Promise<T>` → `AsyncIterable<T>`) is exactly the right
  signal to consumers.
- Channel-return semantics propagate ctx naturally: the user
  selects on `<-ctx.Done()` to exit on client disconnect.
- All four framework adapters share `goduct.SSEStream`; adding a
  fifth framework reuses it.

**Hard / giving up:**

- chi-basic doesn't demonstrate the feature until v0.5.1; new
  users discover SSE through the README + this ADR + the unit
  tests.
- Hooks and Postman silently drop streaming routes. A user who
  expects every route to appear in `hooks.ts` or
  `postman_collection.json` will be surprised. Documented in the
  README.
- The OpenAPI emission is the loosest part: `text/event-stream`
  with a schema describing one event is the standard convention
  but consumer support varies. Same with Swagger UI's rendering
  (it shows the schema but no "try it" affordance).
- Channel-return puts goroutine lifecycle in the user's hands —
  closing the channel cleanly on ctx cancel is the user's
  responsibility. The runtime helper exits cleanly on either
  ctx.Done() OR channel close, but a leaked goroutine in the
  handler is a leak in the user's code, not goduct's.

## Alternatives considered

- **Callback signature** `func(ctx, T, emit func(E) error) error`.
  Simpler for sequential emit; more friction for concurrent
  sources. Picked channel-return for Go-idiomaticity.
- **`iter.Seq2` (Go 1.23+)** for the iteration. Modern, composable,
  but mixing with `ctx` cancellation is awkward (the iterator
  doesn't take a context).
- **Extend raw mode with `goduct:stream <Type>`.** Maximum user
  control; minimum goduct value-add. Picked the typed-signature
  route so streaming endpoints aren't second-class.
- **WebSocket as the first streaming primitive.** Strictly more
  capable but: requires a connection upgrade, has different
  per-framework integrations (Gorilla, nhooyr, x/net/websocket,
  framework-native), and would block on choosing a Go WS library
  to depend on. SSE has none of those problems.

## Cross-references

- [0007](0007-loud-failure-on-unsupported-input.md) — mangled
  streaming signatures (bidirectional channel, non-named E, etc.)
  loud-fail with a hint.
- [0009](0009-generated-adapter-same-package.md) — streaming
  wrappers live in the same package as the handlers.
- [0014](0014-handler-signature-strictness.md) — adds a third
  acceptable signature shape alongside `(*U, error)` and `error`.
  Same strictness rules: T and E must be same-package named
  structs.
- [0022](0022-generator-conventions.md) §1 — `Generate` signature
  unchanged; streaming is detected per-route via
  `Route.StreamType`.
- [0027](0027-enrich-ir-for-go-side-codegen.md) — `Route.StreamType`
  is additive.
- [0030](0030-framework-adapter-selection.md) — framework table
  already carries the writer/context expressions used by the
  streaming wrapper; no new fields needed.
- [0031](0031-raw-handlerfunc-mode.md) — raw mode is the v0.4
  escape hatch for endpoints goduct doesn't model. SSE makes one
  class of those endpoints first-class.
- [0040](0040-v04-closure-pass.md) — sets the pattern for naming
  and timing-out deferred coverage gaps (chi-basic SSE demo
  follows the same path).
