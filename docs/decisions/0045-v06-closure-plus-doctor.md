# 0045. v0.6.1 closure pass — WebSocket polish + goduct doctor (v0.6.1)

**Status:** Accepted
**Date:** 2026-06-02

## Context

Three items deferred during v0.6 per
[ADR 0044 §9](docs/decisions/0044-websocket-bridge.md) have
mechanical implementations that don't justify their own ADRs:
WebSocket subprotocols, ping-interval tuning, and TS-side
auto-reconnection. Their triggers are speculative ("user reports
needing X") but they're all small, additive, and low-risk —
shipping them now keeps the closure-pass cadence going.

The fourth item bundled here is a new CLI subcommand,
**`goduct doctor`**. Surfaced repeatedly in the README's "Maybe /
opportunistic" bucket; the trigger ("support-question pattern
matches twice") hasn't strictly fired, but the command is a thin
wrapper around already-shipped analyzer + config code, and having
it before users hit support-question patterns prevents the
patterns from forming.

The other four v0.6 deferrals (binary frames, AsyncAPI export,
SSE named events, SSE Last-Event-ID reconnect) **stay deferred**
— each needs its own design ADR (IR shape change for binary; new
sibling generator for AsyncAPI; discriminated-union IR for named
events; stateful resumption contract for Last-Event-ID). Same
discipline as ADR 0043 applied to ADR 0044's deferral set.

## Decision

### 1. `goduct:wssubprotocol <name>` directive

A new repeatable handler-level directive declares which
Sec-WebSocket-Protocol subprotocols the route accepts. Order
matters (it's the server's preference list); first server-listed
subprotocol that the client also offers wins per RFC 6455.

```go
// goduct:route          GET /graphql
// goduct:wssubprotocol  graphql-transport-ws
// goduct:wssubprotocol  graphql-ws
func GraphQL(ctx context.Context, req GraphQLReq, conn *goduct.WSConn[ServerMsg, ClientMsg]) error {
    // conn.Subprotocol() reports which one negotiated
}
```

- **IR (additive per ADR 0027):** `Route.WebSocketSubprotocols []string`.
- **goadapter:** the generated `websocket.Accept` call passes the
  list via `&websocket.AcceptOptions{Subprotocols: []string{...}}`.
  Empty/nil list → `nil` (current behavior, default subprotocol).
- **Runtime:** `WSConn` gains a `Subprotocol() string` method that
  forwards to `conn.Subprotocol()` (the negotiated value, or "").
- **tsclient:** `WSConnection`'s constructor accepts an optional
  `protocols: string | string[]` parameter, threaded through
  `new WebSocket(url, protocols)`. The generated method emits the
  declared subprotocol list verbatim:
  ```typescript
  graphql: (params): WSConnection<ServerMsg, ClientMsg> =>
    connectWS<ServerMsg, ClientMsg>(opts, {
      path: "/graphql",
      protocols: ["graphql-transport-ws", "graphql-ws"],
    }),
  ```

### 2. `goduct.json` `websocket.pingInterval`

A new optional `websocket` block in `goduct.json`:

```json
{
  "websocket": {
    "pingInterval": "30s"
  }
}
```

`pingInterval` is parsed via `time.ParseDuration`. Zero / empty /
absent → no ping goroutine (current behavior). Non-zero → the
runtime helper spawns a background ping goroutine for the
connection's lifetime that calls `conn.Ping(ctx)` every interval,
exiting when ctx fires or the connection closes.

- **IR (additive):** `Meta.WebSocketPingInterval time.Duration`.
- **cliconfig:** `Upload` sibling block `Websocket *Websocket`
  with `PingInterval string` (parsed at metaFromConfig time —
  invalid duration → loud-fail at config load).
- **Runtime:** `NewWSConn` gains a variadic options parameter.
  `WithPingInterval(d time.Duration)` is the option; when set,
  `WSConn` spawns the ping goroutine on construction and stops
  it on `Close` / context cancel.
- **goadapter:** when `Meta.WebSocketPingInterval > 0` the
  generated wrapper passes
  `goduct.WithPingInterval(<duration literal>)` to `NewWSConn`.
  Codegen-time baking matches ADR 0043's pattern for
  `upload.maxBytes`.

### 3. TS-side reconnection

A new optional `websocket` field on `ClientOptions` opts in to
auto-reconnection for every WS endpoint in the client:

```typescript
const api = createClient({
  baseUrl: "...",
  websocket: { reconnect: true },
});
```

`reconnect: true` enables: exponential backoff (1s, 2s, 4s, 8s,
16s, capped at 30s); indefinite retries; buffered `.send()` calls
during the disconnect interval, replayed on reconnect; the
`.messages()` AsyncIterable continues across reconnects (no
`return` until `.close()` is called explicitly).

Detailed shape (`reconnect: { maxAttempts, baseDelay, maxDelay }`)
is deferred — true / false / absent is enough for the v0.6.1
trigger.

The `WSConnection` class gains internal state to track the
reconnect intent + buffered sends. The implementation is
contained in `wsScaffold`; no IR / Go-side change.

### 4. `goduct doctor` CLI subcommand

A new subcommand, sibling to `gen`:

```
goduct doctor [<pattern>] [--config <path>] [--dir <dir>]
```

Resolves `goduct.json` per the existing CLI rules
([ADR 0038](docs/decisions/0038-project-config-file.md)),
runs the analyzer against `<pattern>` (or `goduct.json`'s
`pattern` field), and prints a structured report:

```
goduct doctor — analyzed ./examples/chi-basic/api

Config: ./goduct.json (loaded)
  out:       ./web/src/api
  framework: chi
  openapi:   title="My API" version="1.0.0"
  upload:    maxBytes=67108864
  websocket: pingInterval=30s

Routes: 8
  GET    /users/:id           users  idiomatic
  GET    /users               users  idiomatic
  POST   /users               users  idiomatic   →  ValidationError [400]
  PATCH  /users/:id           users  idiomatic
  DELETE /users/:id           users  idiomatic
  POST   /users/:id/avatar    users  idiomatic   upload (single+multi+form)
  GET    /users/:id/events    users  idiomatic   SSE → UserEvent
  GET    /users/:id/echo      users  idiomatic   WS  → EchoEvent ⇄ EchoMessage

Types: 16
  User, Profile, UserStatus, ListUsersResponse,
  CreateUserRequest, UpdateUserRequest, GetUserRequest,
  DeleteUserRequest, ListUsersRequest, ValidationError,
  UploadAvatarRequest, WatchUserEventsRequest, UserEvent,
  EchoRequest, EchoEvent, EchoMessage

Custom adapters: (none)
Security: bearerAuth (http/bearer)
```

- **Output format:** human-readable by default. `--json` toggles
  to a structured JSON dump (one object per section) for tooling.
- **Exit code:** 0 if analyze succeeded; 1 if analyze errored
  (and the analyzer's error surfaces normally per ADR 0019);
  2 if usage error (bad flag, missing pattern with no config).
- **Scope cap:** doctor is read-only. It does not generate
  anything, does not write files, does not check for stale
  output. Generation is `goduct gen`'s job.

### 5. Coverage

- chi-basic: no source changes for the four items themselves —
  subprotocols / ping interval / TS reconnection are opt-ins, and
  doctor is a sibling subcommand. The TS scaffold change for
  reconnection bumps the client.ts golden (WSConnection class
  gains internal state + the optional `websocket.reconnect` arg).
  Other goldens stay byte-identical.
- New analyzer tests for the `goduct:wssubprotocol` directive
  parsing + IR population.
- New goadapter test (synthetic IR): the `websocket.Accept` call
  threads `Subprotocols` when the list is non-empty.
- New cliconfig test for the `websocket` block + invalid
  duration loud-fail.
- New cmd/goduct end-to-end test for `goduct doctor`: invoking
  it against chi-basic produces a non-empty report that names
  the expected routes and the config source.

### 6. Deferred (still)

- **Binary frames** — IR shape change (message type `[]byte`).
- **AsyncAPI export** — new sibling generator.
- **SSE named events** — discriminated-union IR.
- **SSE Last-Event-ID reconnect** — stateful resumption contract.
- **`reconnect: { maxAttempts, baseDelay, maxDelay }`** detailed
  shape — boolean is enough until a user reports needing the
  knobs.
- **Additional `websocket.*` tuning knobs** (read message max
  bytes, write timeout, etc.) — pingInterval covers the most
  common case; the rest can grow per real reports.

## Consequences

**Easy / unblocked:**

- Subprotocol-needing protocols (graphql-ws, mqtt, custom
  versioning conventions) work without dropping to raw mode.
- Long-lived connections survive intermediary timeouts via
  pingInterval — common for behind-load-balancer deployments.
- Flaky-network clients reconnect transparently with a one-line
  config opt-in.
- `goduct doctor` gives new users + CI a one-shot view of "is
  this project actually configured the way I think it is?"
  before any generation happens.

**Hard / giving up:**

- The TS client.ts golden grows. Non-WS APIs see no change
  (the scaffold is conditional) but the WSConnection class now
  carries reconnect state even when reconnect isn't enabled.
  Acceptable per ADR 0044's conditional-scaffold pattern.
- Reconnection with buffered sends has a memory cost on the
  client; an unbounded outage means an unbounded queue. v0.6.1
  doesn't cap it; documented as "the boolean form trusts the
  network to come back."
- `goduct doctor` is opt-in scope creep on the CLI. Mitigation:
  it's a sibling subcommand, not a `gen` flag, so existing
  invocations are untouched.

## Alternatives considered

- **Skip pingInterval; let users send their own pings.**
  Rejected: the most common reason users need pings is
  network-intermediary timeouts (NAT, proxy, load balancer),
  which want a "set it and forget it" knob, not per-handler
  bookkeeping.
- **Make subprotocols a `goduct.json` block instead of a
  directive.** Rejected: subprotocols are per-route (different
  endpoints may speak different protocols); a project-wide
  block doesn't fit.
- **Ship reconnection as default-on.** Rejected: opt-in respects
  the principle of least surprise (a reconnect that masks a real
  server error is a debugging hazard).
- **Make `goduct doctor` part of `goduct gen --dry-run`.**
  Rejected: gen is for generation; doctor is for introspection.
  Conflating them obscures both.

## Cross-references

- [0014](0014-handler-signature-strictness.md) — WebSocket
  signature stays as in ADR 0044; this ADR adds a directive,
  not a new shape.
- [0019](0019-error-message-formats-by-layer.md) — doctor's
  error path uses the existing analyzer error format
  (Format A for handler-anchored errors).
- [0022](0022-generator-conventions.md) §1 — `Generate`
  signature unchanged; doctor doesn't call any generator.
- [0027](0027-enrich-ir-for-go-side-codegen.md) — IR additions
  (`Route.WebSocketSubprotocols`, `Meta.WebSocketPingInterval`)
  are additive.
- [0038](0038-project-config-file.md) — `websocket` block joins
  the existing `openapi` + `security` + `upload` blocks.
- [0043](0043-v06-closure-pass.md) — same pattern of
  ADR-by-ADR closure of mechanical items; designed-out items
  stay deferred with their original triggers.
- [0044](0044-websocket-bridge.md) §9 — closes the
  subprotocols + ping-interval + TS-reconnection deferrals;
  binary frames + AsyncAPI export stay open.
