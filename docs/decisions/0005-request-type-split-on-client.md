# 0005. Split path/query params from the JSON body in the client signature

**Status:** Accepted
**Date:** 2026-05-17

## Context

A Go request struct mixes sources via tags (`path:`, `query:`, `header:`,
`json:`). On the client, those sources have different transport meaning: path
and query go into the URL, json goes into the body. The IR already separates
`PathParams`, `QueryParams`, `HeaderParams`, and `BodyType`. The chi-basic
golden output makes the client shape concrete: `get`/`delete` take
`(params: { id })`; `list` takes `(params: { limit, cursor? })`; `update`
takes `(params: { id }, body)`; `create` takes `(body)` only. The generated
`UpdateUserRequest` type in `types.ts` contains only `name?`/`status?` — no
`id`.

## Decision

The generated TS client passes path/query params as a separate first argument
from the JSON body, e.g. `api.users.update({ id }, body)`. Path and query
fields do **not** appear in the generated TS request body type.

## Consequences

- Easy: path/query never collide with body fields; GET/DELETE call sites take
  only `params`; the generated body type is exactly the JSON wire payload, so
  it matches the zod schema in `schemas.ts`.
- Hard / giving up: a single combined request argument. One Go request struct
  becomes a `(params, body)` pair on the client; callers must distinguish
  URL fields from body fields — which the signature encodes deliberately.

## Alternatives considered

- Single combined argument object — rejected: conflates URL and body, and the
  body type would carry path params that are not in the JSON.
- Positional path args plus a body object — TBD — discuss.
