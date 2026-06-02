# 0035. Swagger UI + Postman collection generators (v0.3)

**Status:** Accepted
**Date:** 2026-06-02

## Context

[ADR 0034](0034-openapi-export.md) shipped OpenAPI 3.1 emission
(`--openapi` → `openapi.json`). Two downstream consumers were
explicitly listed as remaining-for-v0.3-tag:

- **Swagger UI** — a browser page that renders the spec
  interactively. The standard Swagger UI dist is ~MB of JS/CSS; goduct
  is a small Go CLI and shouldn't bundle that.
- **Postman collection** — a JSON file (collection v2.1) describing
  every route as a Postman request. Most teams will instead import
  the OpenAPI doc directly into Postman (which speaks the spec); a
  collection generator is for repos that want the collection
  committed.

Both are sibling generators of OpenAPI: they ride on the same IR, ship
under their own flags, and add nearly nothing to goduct's binary
footprint. One ADR covers both — the decisions are small but real and
naturally co-decided.

## Decision

### Common: each is its own generator + flag

- `--swagger-ui` → `swagger-ui.html` in `--out`.
- `--postman` → `postman_collection.json` in `--out`.
- `--all` includes both. Each is opt-in independently.
- New packages `internal/generators/swaggerui` and
  `internal/generators/postman`, each with the standard
  `Generate(api *ir.API, w io.Writer) error` signature per
  [ADR 0022](0022-generator-conventions.md) §1.

### Swagger UI

A single static HTML file (~30 lines) that loads Swagger UI from the
public CDN and points at `./openapi.json` (the sibling file the
`--openapi` generator writes — they're expected to ship together).

**Decisions:**

- **CDN, not bundled.** The standard Swagger UI dist is hosted at
  `unpkg.com/swagger-ui-dist@5/`. Goduct emits `<link>` /
  `<script>` tags referencing it. Pinned to major version 5 (the
  current major as of v0.3) — minor/patch updates are pulled
  automatically.

  Bundling the JS/CSS inline would add ~1.5 MB to every consumer's
  output and put goduct in the business of tracking Swagger UI
  releases. CDN-loaded is what every Swagger UI tutorial does and
  what every comparable codegen tool emits.

- **Title** from `gen.PackageName(api)` (matches OpenAPI's `info.title`).
  Falls back to `"goduct"` when empty (no API to document — shouldn't
  ship, but doesn't crash).

- **Same-dir reference to `openapi.json`.** The HTML uses
  `url: "./openapi.json"` — a relative path resolved by the browser.
  Hosting Swagger UI elsewhere (CDN with separate spec URL etc.) is
  the user's wrapper.

- **No options.** Swagger UI takes ~80 configuration options
  (deepLinking, displayRequestDuration, etc.). v0.3 takes the defaults;
  users with strong opinions edit the HTML themselves or build their
  own page from the JSON.

- **No persistAuthorization, no extra plugins.** Bare bones; works
  for the common case.

### Postman collection

A JSON file conforming to Postman collection format v2.1.0. The
spec is at <https://schema.getpostman.com/json/collection/v2.1.0/collection.json>.

**Decisions:**

- **Schema version 2.1.0** (current; 2.0 is legacy).
- **info.name** from `gen.PackageName(api)`; **info.schema** the
  v2.1.0 schema URL.
- **info._postman_id** is a deterministic UUID derived from the
  package name (`SHA-1(pkgname)`-formatted as a UUID-shaped string).
  Deterministic so re-generation produces byte-identical output;
  per-project so collections from different APIs don't collide on
  import.

- **Path-param syntax** `{{baseUrl}}/users/:id` — Postman 2.1's
  native form. Goduct's `:name` syntax passes through verbatim (same
  bonus as gin/echo).

- **`{{baseUrl}}` variable** declared at collection level with
  default `"http://localhost:8080"`. Users override in Postman's
  environment for staging / prod.

- **Tag-based folder grouping.** Routes with the same `Tag` (the v0.1
  `tags-by-first-path-segment` convention) become siblings under a
  Postman folder of that tag. A single ungrouped route ends up at the
  collection root.

- **Per-request body**: for body methods (POST/PUT/PATCH), emit a
  `raw` body with `language: json` and a synthesized JSON example
  derived from the body type's wire-visible fields. Each field gets
  a type-appropriate placeholder (empty string, `0`, `false`,
  empty array/object). Goal: the user clicks the request in
  Postman and the body shows the expected JSON shape; they edit
  the values and send. Per-route OpenAPI `examples` overrides are
  not consulted (v0.4).

  Generic instantiations (Page[User] as a body) are flattened the
  same way OpenAPI flattens — the body schema is the per-
  instantiation substituted form.

- **Request description** from `gen.JSDocFull(handler, doc)` (multi-
  sentence preserved — matches tsclient's policy per
  [ADR 0024](0024-doc-comment-emission-policy.md)).

- **Headers** declared per route from `Route.HeaderParams`. Value is
  the empty string — placeholders for users to fill.

- **Query string** declared from `Route.QueryParams`. Each param has
  `disabled: true` if `Param.Optional` (Postman shows them dimmed and
  doesn't include them by default), `disabled: false` otherwise.

- **Tag-folder order** alphabetical (matches OpenAPI's path order);
  requests within a tag in route source order (matches the client
  generator's method order).

### Determinism

- Swagger UI: single static HTML, no IR-driven variability beyond the
  page `<title>`. Byte-identical across runs trivially.
- Postman: collection JSON. The same encoding/json + json.Indent
  pattern OpenAPI uses; `info._postman_id` is deterministic (SHA-1 of
  package name); folder/item ordering is fixed (alphabetical tags +
  source-order routes).

## Consequences

**Easy / unblocked:**

- A user adding `--all` to their `goduct gen` invocation gets a
  ready-to-host docs page and a collection their team can import
  into Postman — no extra steps.
- The two generators add ~150 lines + ~30 lines of Go each, no new
  deps, no analyzer changes.
- Swagger UI loads from a stable CDN; goduct doesn't track upstream
  releases.

**Hard / giving up:**

- CDN dependency: if `unpkg.com` is down or blocked, the Swagger UI
  page doesn't render. Users behind strict CSPs can replace the
  CDN URLs after generation; an `--swagger-ui-offline` flag that
  bundles the JS/CSS is a v0.4 follow-up if real users surface a
  need.
- Postman example bodies are placeholders, not realistic values.
  Users who want richer examples either edit in Postman or wait for
  `--openapi-examples` (v0.4) which would feed both OpenAPI and
  Postman.
- Goduct never produces a Postman environment file (separate JSON,
  separate concern). Users define their own.

## Alternatives considered

- **Bundle Swagger UI's JS inline** — rejected. ~1.5 MB of bundled
  bytes per consumer; goduct would need to track Swagger UI releases.
- **Redoc instead of Swagger UI** — rejected for v0.3 (Swagger UI is
  the universal default). A `--redoc` flag is plausible if asked.
- **Postman collection v2.0** — rejected; v2.1 is current. v1 is
  deeply legacy.
- **Per-tag separate collection files** — rejected. One file per API
  is the conventional Postman pattern; users can split downstream.
- **Skip example bodies entirely (just empty `{}`)** — rejected.
  Field-keyed placeholders give users a meaningful starting point
  with no extra goduct cost.
- **Emit Postman environment file** — rejected for v0.3. Environments
  are per-deploy-target (dev/staging/prod) and goduct knows none of
  them. The `{{baseUrl}}` variable + default is enough.

## Cross-references

- [0022](0022-generator-conventions.md) §1 — generator signature;
  both packages comply.
- [0034](0034-openapi-export.md) — the parent generator; Swagger UI
  references its output, Postman is a separate spec rendering.
- [0024](0024-doc-comment-emission-policy.md) — JSDocFull for
  Postman request descriptions (matches tsclient's policy).
