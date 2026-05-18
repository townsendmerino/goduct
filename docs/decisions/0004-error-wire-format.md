# 0004. Route all errors through the goduct runtime with a stable wire shape

**Status:** Accepted
**Date:** 2026-05-17

## Context

A typed client needs machine-readable errors, not plain text. `runtime/errors.go`
defines `Error{Status int (json:"-"), Code string, Message string, Details any
(omitempty)}` with constructors (`BadRequest`…`Conflict`, `Internal`).
`WriteError` uses `errors.As` to find an `*Error`, falling back to `Internal`
(which logs the original and returns a generic 500). The generated TS client
(`expected/client/client.ts`) throws `GoductError` with `.status`, `.code`,
`.message`, `.details`. The README states the wire format is stable.

## Decision

Errors flow through the `goduct` runtime package with the wire shape
`{ code, message, details? }`. Handlers return `*goduct.Error`; any other
`error` is converted to `Internal` (logged server-side, generic message on the
wire). The TS client throws `GoductError` exposing `.status` (from the HTTP
status), `.code`, `.message`, and optional `.details`.

## Consequences

- Easy: one error path on both sides; unknown errors degrade safely to 500
  without leaking internals; `details` is optional and additive.
- Hard / giving up: error `code` values are free-form strings — no enum or
  per-endpoint error contract is enforced. `Status` is not on the wire
  (`json:"-"`); the client reconstructs it from the HTTP response status.
  Changing the wire shape is a compatibility event (the README promises
  stability).

## Alternatives considered

- Plain-text error bodies — rejected: not machine-readable for a typed client.
- HTTP status only, no body — rejected: loses `code`/`message`.
- Per-endpoint typed error unions — TBD — discuss.
