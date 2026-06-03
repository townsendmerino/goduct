# TODO

Deferred work with concrete triggers. Items here are spec-trust gaps
(implemented or designed but not exercised by a golden) or named
deferrals from accepted ADRs. Each entry says *what*, *where the
deferral was made*, and *the trigger that should activate the work*.

For the narrative phasing (which features in which release) see the
**Roadmap** section of [README.md](README.md). This file is the
finer-grained, ADR-anchored punch list.

---

## v0.5.1 — closure pass on the v0.5 wire-shape bundle

Seven items deferred during the v0.5 work: five WebSocket-side per
[ADR 0044 §9](docs/decisions/0044-websocket-bridge.md), two
SSE-side per [ADR 0041 §7](docs/decisions/0041-sse-streaming.md)
(and reaffirmed in [ADR 0043 §1](docs/decisions/0043-v06-closure-pass.md)).

WebSocket polish:

- **Subprotocols** (Sec-WebSocket-Protocol). v0.5 always uses the
  default subprotocol. Adding a `goduct:wssubprotocol` directive
  is a small follow-up. **Trigger:** user reports needing a
  named subprotocol (mqtt, graphql-ws, etc.).

- **Ping/pong timeout customization.** coder/websocket has
  sensible defaults; surfacing knobs through goduct.json is a
  future ADR. **Trigger:** user reports keepalive-related
  disconnections.

- **Binary frames.** v0.5 messages are all JSON text frames via
  wsjson. Binary support is a different IR shape (message type
  is `[]byte`, not a named struct). **Trigger:** user reports a
  protobuf-over-WS or audio-streaming use case.

- **AsyncAPI export.** Proper protocol-aware spec emission via
  AsyncAPI 3.0. Adds another sibling generator like swaggerui.
  **Trigger:** user reports needing WS docs in the same place as
  HTTP docs.

- **TS-side reconnection / backoff / buffering.** Browser
  WebSocket is fire-and-forget; the current `WSConnection` class
  doesn't auto-retry. **Trigger:** user reports needing
  transparent reconnect on flaky networks.

SSE polish:

- **Named SSE events** (`event: foo\ndata: {...}\n\n`). Currently
  goduct emits only nameless `data:` blocks. Needs a discriminated-
  union representation in the IR that goduct doesn't currently
  model — OR a partial-answer convention (every event gets
  `event: <TypeName>` automatically) that would need its own
  design ADR. **Trigger:** user reports that a downstream SSE
  consumer requires named events, OR goduct grows discriminated-
  union support in the IR.

- **Last-Event-ID / auto-reconnect** on the TS client. The current
  `streamSSE` helper exits cleanly on body close; it does not
  retry. Needs a new IR/runtime contract for "events carry IDs"
  plus stateful resumption that only the user's event source
  knows how to do. **Trigger:** user reports a real production
  usage where intermittent disconnects need transparent recovery.

---

## Cross-cutting deferrals still open


- **Per-framework adapter golden compilation in CI** — the
  goadapter goldens for chi/gin/echo/mux are byte-tested but not
  *compiled* (would require pulling in gin/echo/mux as dev deps).
  A typo in the gin emission would pass byte tests and break only
  at user build time. Deferred since v0.2 per
  [ADR 0030 §4](docs/decisions/0030-framework-adapter-selection.md).
  **Trigger:** any compile-failure bug report against a framework
  adapter, OR a v0.x.y where adding gin/echo/mux as a nested test
  module (under `testdata/`) becomes acceptable.

- **Per-handler security scope arguments** (OAuth2-style
  `read:users write:users`). v0.4.1 ships per-handler scheme
  override but always emits empty scopes. Deferred per
  [ADR 0039 §2](docs/decisions/0039-openapi-polish-trio.md) and
  [ADR 0040 §1](docs/decisions/0040-v04-closure-pass.md).
  **Trigger:** user reports needing OAuth2 with scoped operations.

- **Plural / named request examples** —
  `requestBody.examples` (multiple named examples) deferred per
  [ADR 0040 consequences](docs/decisions/0040-v04-closure-pass.md).
  Today `goduct:requestexample` ships only one example per
  handler. **Trigger:** user asks for multiple examples per
  request body.

- **Multi-package input.** Today `goduct gen <pkg>` analyzes one
  package; cross-package request/response types loud-fail. The IR
  (`api.SourceDirs` is a map) is forward-compatible. Per
  [ADR 0014](docs/decisions/0014-handler-signature-strictness.md)
  and [ADR 0027](docs/decisions/0027-enrich-ir-for-go-side-codegen.md).
  **Trigger:** any project with handlers spanning multiple
  packages — but this implies real surgery in routes.go and
  goadapter's package-name resolution.

- **Type alias rendering polish.** A struct reachable only via
  `type A B` alias emits as a duplicate interface rather than a TS
  alias. Listed in README "Known polish". **Trigger:** any user
  report that the duplicate-interface output causes confusion.

---

## Maybe / opportunistic

These are speculative — not on a release path, not in
ADRs as deferrals, just possibilities flagged in the README's
"Maybe" bucket or surfaced in conversation.

- **Swift / Kotlin / Python client generators.** Each is one new
  `Generate(*ir.API, io.Writer) error` function consuming the
  existing IR. No analyzer changes needed. **Trigger:** any
  contributor wants to add one, OR goduct's user base widens past
  Go+TS shops.

- **`goduct doctor` diagnostic command.** Print the resolved
  config, the analyzed routes, the framework target, and any
  unhealthy state in one machine-readable + human-readable dump.
  Useful for "why isn't my route showing up?" debugging.
  **Trigger:** any time the support-question pattern matches this
  shape twice.

- **Streaming hooks for React Query.** Once `@tanstack/react-query`
  (or a community plugin) settles on a subscription/iterator hook
  pattern, the hooks generator can stop silently skipping
  streaming routes. **Trigger:** RQ v6 ships a stable iterator
  hook, OR a clear community-pattern winner emerges.
