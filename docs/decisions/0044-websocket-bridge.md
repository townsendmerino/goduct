# 0044. WebSocket bridge — typed full-duplex on coder/websocket (v0.7)

**Status:** Accepted
**Date:** 2026-06-02

## Context

v0.5 added one-way streaming (SSE). v0.6 added file uploads. The
remaining "wire shape goduct doesn't model" item from
[TODO.md](../../TODO.md) is **full-duplex** — endpoints where
both server and client push typed messages over a persistent
connection. WebSocket is the universal protocol for that.

Two upstream choices needed locking in before any code lands:

- **Library.** Goduct's runtime package currently ships with zero
  non-stdlib dependencies. WebSocket adds one. The shortlist was
  `gorilla/websocket` (mature, widest adoption, but archived by
  original maintainers in 2023), `coder/websocket` (modern,
  context-aware, originally `nhooyr.io/websocket`, actively
  maintained by Coder), and stdlib `x/net/websocket` (officially
  deprecated).
- **Handler signature.** Three plausible shapes: a channel pair
  (symmetric to SSE), a conn-helper (user owns the loop), or a
  reducer (request/response per message). Each fits a different
  use-case shape.

Both questions are now decided. The library is **coder/websocket**
(modern API, active maintenance, zero own-deps, browser-compatible
Streams API on the client). The handler shape is **conn-helper**
(most flexible — supports request/response, broadcast, fan-in
patterns; the user owns the loop). The other shapes are recorded
under "Alternatives considered."

## Decision

### 1. Runtime dependency: `github.com/coder/websocket`

`runtime/go.mod` (which is `goduct/go.mod` — the runtime ships in
the same module) gains a require directive. This is the first
non-stdlib runtime dep goduct ships; users importing
`goduct/runtime` now get coder/websocket transitively. The
tradeoff is explicit: goduct gives up its "stdlib-only runtime"
property in exchange for not hand-rolling a frame parser. The
coder/websocket API is small enough that swapping to another
library later is a runtime-internal change (the goduct.WSConn
public surface stays the same).

The wsjson subpackage (`github.com/coder/websocket/wsjson`) is
used for the encode/decode of typed messages.

### 2. Handler signature

A function is a goduct **WebSocket handler** iff:

- It carries `goduct:route` AND
- Its signature is **exactly**
  `func(context.Context, T, *goduct.WSConn[S, C]) error` where:
  - T is a same-package named struct (the request type; path/
    query/header tags work as on any other route);
  - S is a same-package named struct (server → client message
    type, what the handler's `conn.Send` accepts);
  - C is a same-package named struct (client → server message
    type, what `conn.Recv` returns).

No directive is required. The third parameter being
`*goduct.WSConn[S, C]` is the signal. Other arities or pointer
shapes loud-fail with a hint that names the expected signature.

Method is always GET on the wire (WebSocket upgrade); `goduct:status`
is ignored. `goduct:errorresponse`, `goduct:security`,
`goduct:requestexample`, and the upload directives don't apply
(an upgrade isn't a request/response cycle in the usual sense).

### 3. IR

```go
// ir.go (additive, ADR 0027):
type Route struct {
    ...existing...
    // WebSocket is non-nil iff this is a WS route (ADR 0044).
    // Both Send and Recv are KindNamed pointing at same-package
    // named structs. ResponseType / StreamType / BodyType all
    // stay nil for WS routes; generators that don't yet handle
    // WS (openapi, postman, hooks) see them as no-body routes
    // and skip emission.
    WebSocket *WebSocketTypes
}

type WebSocketTypes struct {
    Send *TypeRef // server → client (conn.Send takes this)
    Recv *TypeRef // client → server (conn.Recv returns this)
}
```

Generators check `Route.WebSocket` first; non-nil means WS, take
the WS path.

### 4. Runtime helper: `goduct.WSConn[S, C]`

```go
// runtime/ws.go (new):
type WSConn[S any, C any] struct {
    conn *websocket.Conn
}

// Send writes msg as a JSON text frame. Returns when the frame
// has been written or ctx is canceled.
func (c *WSConn[S, C]) Send(ctx context.Context, msg S) error {
    return wsjson.Write(ctx, c.conn, msg)
}

// Recv reads the next JSON text frame, decoding it into C.
// Returns when a complete message has been received, ctx is
// canceled, or the connection closes.
func (c *WSConn[S, C]) Recv(ctx context.Context) (C, error) {
    var msg C
    err := wsjson.Read(ctx, c.conn, &msg)
    return msg, err
}

// Close sends a close frame with the given status code and
// reason, then waits for the peer to acknowledge.
func (c *WSConn[S, C]) Close(code websocket.StatusCode, reason string) error {
    return c.conn.Close(code, reason)
}
```

The generated adapter constructs the WSConn after the upgrade and
hands it to the user's handler. Closing on handler return is the
adapter's job (defer); the user may also Close explicitly.

### 5. goadapter: framework wrappers

The wrapper for a WS route does the path/query/header param
assignment (no body — WebSocket upgrades are GET with no body),
calls `websocket.Accept`, constructs the typed WSConn, and calls
the user's handler:

```go
// chi (mux/gin/echo follow the same shape with their writer/
// request expressions per ADR 0030's framework table):
func handleChat(w http.ResponseWriter, r *http.Request) {
    var req ChatRequest
    req.Room = chi.URLParam(r, "room")
    c, err := websocket.Accept(w, r, nil)
    if err != nil {
        goduct.WriteError(w, goduct.BadRequest("websocket accept failed"))
        return
    }
    defer c.CloseNow()
    if err := Chat(r.Context(), req, goduct.NewWSConn[ChatEvent, Message](c)); err != nil {
        return
    }
}
```

`goduct.NewWSConn[S, C](*websocket.Conn) *WSConn[S, C]` is a
constructor in the runtime. The generated wrapper passes the
typed `[Send, Recv]` arguments derived from the user's signature.

Echo's wrapper signature (`func(c echo.Context) error`) appends
`return nil` at the end; the rest of the shape is unchanged.

### 6. tsclient: typed `WSConnection<S, C>`

Streaming methods change return type from `Promise<T>` to
`WSConnection<S, C>`:

```typescript
chat: (params: { room: string }): WSConnection<ChatEvent, Message> =>
  connectWS<ChatEvent, Message>(opts, {
    path: `/chat/${encodeURIComponent(params.room)}`,
    query: undefined,
  }),
```

`connectWS<S, C>` is a scaffold helper appended to client.ts
only when at least one WS route exists (same pattern as v0.5's
streamSSE). It returns a `WSConnection<S, C>`:

```typescript
class WSConnection<S, C> {
    private ws: WebSocket;
    constructor(url: string) { this.ws = new WebSocket(url); }
    send(msg: C): void {
        this.ws.send(JSON.stringify(msg));
    }
    async *messages(): AsyncIterable<S> {
        const queue: S[] = [];
        let resolve: ((v: IteratorResult<S>) => void) | null = null;
        // ... event listener wiring; full impl in client.ts scaffold ...
    }
    close(code?: number, reason?: string): void {
        this.ws.close(code, reason);
    }
}
```

Note the type-parameter direction flip: on the Go side `Send`
takes the server type (S) because the server is sending; on the
TS side `send` takes the client type (C) because the client is
sending. Both surfaces type-correctly without the user thinking
about direction — the `WSConnection<S, C>` parameter order is
"what arrives" (S, server messages) then "what I send" (C, client
messages).

URL construction handles the scheme upgrade automatically: an
`http://` baseUrl becomes `ws://`, `https://` becomes `wss://`.

### 7. Other generators

- **openapi.** OpenAPI 3.1 doesn't natively model WebSockets;
  AsyncAPI does, but emitting an AsyncAPI sibling is out of scope
  for v0.7. WS routes are **omitted** from openapi.json. Same
  treatment as Postman gives SSE.
- **postman.** Postman v2.1 supports WebSocket requests with a
  specific item shape goduct doesn't yet model. Skip for v0.7;
  WS routes are omitted from `postman_collection.json`.
- **hooks.** React Query has no first-class WS pattern; skip.
- **zod / tstypes.** Server and client message types are regular
  named structs and emit normally as types + zod schemas. The
  WS wiring (Send/Recv) is on the connection helper, not on the
  types themselves.

### 8. Coverage

- chi-basic gains a typed WS route exercising the conn-helper
  shape end-to-end. `EchoMessage` (client → server) and
  `EchoEvent` (server → client) types; the handler echoes each
  received message back as an event. Cascade: types/schemas/
  client (with connectWS scaffold) + the four framework adapter
  goldens. openapi/postman/hooks skip.
- New analyzer tests for the WS signature detection + loud-fails
  on mangled shapes (third param not WSConn, wrong type-arity,
  cross-package message types).
- New goadapter test (synthetic IR): all four framework wrappers
  emit the websocket.Accept + WSConn construction + handler
  call. Format-Source validates the generated Go.
- New tsclient test for the WSConnection scaffold + the
  type-parameter direction.

### 9. Deferred to v0.7.1 or later

- **Subprotocols** (Sec-WebSocket-Protocol). v0.7 always uses
  the default subprotocol. Adding a `goduct:wssubprotocol`
  directive is a small follow-up.
- **Ping/pong timeout customization.** coder/websocket has
  sensible defaults; surfacing knobs through goduct.json is a
  future ADR if anyone reports needing it.
- **Binary frames.** All v0.7 messages are JSON text frames via
  wsjson. Binary support is a different IR shape (the message
  type would be `[]byte` instead of a named struct).
- **AsyncAPI export.** Proper protocol-aware spec emission via
  AsyncAPI 3.0. Adds another sibling generator like swaggerui;
  defer until users ask.
- **React Query / equivalent hooks.** No clear community pattern
  yet (same blocker as SSE hooks per ADR 0041).

## Consequences

**Easy / unblocked:**

- Real-time use cases (chat, live dashboards, collaborative
  editing, agent telemetry) become a first-class wire shape
  alongside JSON request/response, SSE, and uploads.
- The typed Send/Recv on both ends eliminates the
  `JSON.parse(e.data) as MyExpectedShape` ceremony every
  hand-rolled WebSocket client repeats.
- Generated code stays adapter-thin: the wrapper is upgrade +
  conn + handler call, mostly delegation to coder/websocket.

**Hard / giving up:**

- First non-stdlib runtime dep. Users importing goduct/runtime
  now get coder/websocket transitively. Mitigation: a small,
  well-maintained library; swap-out is internal if it ever
  matters.
- WS routes don't surface in OpenAPI / Postman / hooks. Doc'd
  in the README's "What's supported" section; same posture as
  v0.5's SSE deferral.
- Reconnection / backoff / message buffering are the user's
  responsibility on the TS side. The browser WebSocket API is
  fire-and-forget; goduct's WSConnection doesn't auto-retry.
  Trigger for v0.7.1: real user feedback.
- Subprotocol negotiation isn't exposed in v0.7; if a user needs
  it before v0.7.1 lands, they fall back to writing the handler
  with raw `*websocket.Conn` (the runtime helper doesn't block
  access to the underlying conn, even if the typed surface
  doesn't expose it).

## Alternatives considered

- **gorilla/websocket dep.** Larger ecosystem, more tutorials,
  but the archive status of the original maintainers + the
  bolted-on ctx integration tipped the call to coder/websocket.
- **Pluggable interface (let the user provide either lib).**
  Adds API surface (a `WSConn` interface in runtime + a default
  impl) for marginal flexibility. Most users don't care which
  WS library is underneath as long as the typed surface works.
  Defer until someone has a reason.
- **Channel-pair signature** `(<-chan S, chan<- C, error)`.
  Symmetric to SSE's `(<-chan E, error)` but awkward for
  request/response patterns (the handler can't easily peek at
  one message and choose what to send back). Rejected for
  flexibility.
- **Reducer signature** `(ctx, T, ClientMsg) (ServerMsg, error)`.
  Simplest for echo-bot-style endpoints, can't model broadcast
  or server-pushed events. Rejected as too narrow.
- **Stdlib `x/net/websocket`.** Officially deprecated; not a
  viable choice.

## Cross-references

- [0009](0009-generated-adapter-same-package.md) — generated
  adapter in the handlers' own package; WS wrappers live there
  too.
- [0014](0014-handler-signature-strictness.md) — adds a fourth
  acceptable handler signature shape alongside the existing
  three. Same strictness: T, S, C must be same-package named
  structs.
- [0022](0022-generator-conventions.md) §1 — `Generate`
  signature unchanged; WS detected per-route via
  `Route.WebSocket`.
- [0027](0027-enrich-ir-for-go-side-codegen.md) —
  `Route.WebSocket` is additive.
- [0030](0030-framework-adapter-selection.md) — framework table
  already carries the writer / context / request expressions
  used by the WS wrapper; no new fields needed.
- [0041](0041-sse-streaming.md) — same pattern of
  "signature-detected wire shape, all generators branch on a
  Route.* flag, scaffold helper conditionally appended to
  client.ts." WS is the full-duplex sibling to SSE's one-way.
- [0042](0042-file-uploads.md) — same posture on "non-Go
  generators skip when they can't model the wire shape" (postman
  for uploads in v0.6, postman + openapi + hooks for WS here).
- [0043](0043-v06-closure-pass.md) — `goduct.json` upload block
  precedent for future runtime config knobs (ping interval,
  subprotocol, etc.) when those land.
