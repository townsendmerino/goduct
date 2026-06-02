# User-friction reduction plan

**Snapshot date:** 2026-06-02 (post-generics; pre-v0.3.0 tag)

A living view of what's between goduct and a frictionless typed-API
workflow for the user. Updated after each significant release. Status
truths here go stale fast — for current shipped state, check the README
roadmap line and `git log v0.2.0..HEAD`.

## Shipped — feature complete

| Friction at v0.1.0 | Status | Lever |
|---|---|---|
| Chi-only — gin/echo/`net/http` users can't adopt | ✅ **v0.2.0 tagged** | `--framework chi\|gin\|echo\|mux` ([ADR 0030](decisions/0030-framework-adapter-selection.md)) |
| Must use idiomatic `func(ctx, T) (*U, error)`; existing `http.HandlerFunc` codebases locked out | ✅ **v0.2.0 tagged** (chi/mux) | Raw `http.HandlerFunc` + `goduct:request`/`response` annotations ([ADR 0031](decisions/0031-raw-handlerfunc-mode.md)) |
| No hot-reload during dev | ✅ **v0.2.0 tagged** | `--watch` mode ([ADR 0029](decisions/0029-watch-mode-design.md)) |
| React shops re-implement fetch wrappers | ✅ **v0.2.0 tagged** | `--hooks` (React Query factory + auto tag-invalidation) ([ADR 0028](decisions/0028-react-query-hooks-design.md)) |
| `oneof` validator silently dropped | ✅ **v0.2.0 tagged** | `oneof` → `z.enum([...])` for string fields ([ADR 0006](decisions/0006-validation-tag-translation.md) v0.2) |
| `decimal.Decimal`, `civil.Date`, etc. loud-fail | ✅ **v0.2.0 tagged** | `--adapter <qname>=<wire>` ([ADR 0032](decisions/0032-custom-type-adapters.md)) |
| **Generics unsupported** (no `Page[T]`, `Result[T, E]`) | ✅ **on `main`** (post-v0.2.0, pre-v0.3.0) | Full generic struct support, multi-param, end-to-end through all 4 TS generators ([ADR 0033](decisions/0033-generics.md)) |

## Friction still present

| Friction now | Plan | Honest size |
|---|---|---|
| `@latest` points at v0.2.0 — generics works on `main` only | **Cut v0.3.0 (or `-rc1`)** — narrative call. Either tag v0.3.0 with just generics now, or wait for OpenAPI to also land. | One commit (tag) |
| No OpenAPI export — can't hand the API to non-Go/TS consumers (mobile teams, Postman users, internal docs) | **v0.3** — `--openapi` generator emitting OpenAPI 3.1 from the existing IR | Discrete generator; ~1 session like adapters/hooks. Spec-text-rendering, no analyzer surgery |
| No Swagger UI page out of the box | **v0.3** — small static-HTML generator pointing at the OpenAPI doc | Trivial follow-up to OpenAPI; ~couple hours |
| No Postman collection export | **v0.3** — collection JSON generator | Trivial; sibling of OpenAPI rendering |
| Type-param constraints other than `any` loud-fail (`[T Stringer]`, `[T int \| int64]`) | **v0.4** | Constraint-aware TS rendering is real work; uncommon for HTTP types |
| Generic enums / aliases loud-fail (`type Status[T any] string`, `type Opt[T any] = *T`) | **v0.4** | Rare in practice; lift if usage surfaces |
| gin/echo raw `HandlerFunc` routes loud-fail | **v0.4** | Need context-converting wrappers per framework |
| Non-standard `goduct:status` codes loud-fail | **v0.4 polish** | Full `net/http` status constant map |
| Non-`any` adapters: `Page[string]`-style builtin TypeArgs work via `schemasExpr`'s small zod builtin map; but if `--adapter foo.Bar=number` is the TypeArg, current rendering inlines `z.number()` rather than the adapter's qname-keyed schema. Edge case, no real user. | **v0.4** (only if a real user hits it) | Thread `adapters` through `schemasExpr` |
| No SSE/streaming, WebSocket, file-upload helpers | **v0.4** | Real new runtime surface; not just codegen |
| Swift/Kotlin/Python clients | **Maybe** | Additive generator implementations; purely IR-consumer pattern |

## Spec-trust coverage gaps queued (don't gate UX, but real)

Three features ship with synthetic-test coverage and queue a chi-basic golden-integration TODO. Each would, when done, touch every existing golden:

- **Raw `http.HandlerFunc` mode** — analyzer + goadapter chi/mux paths tested via synthetic packages; chi-basic itself stays idiomatic.
- **Custom type adapters** — `math/big.Int`-on-synthetic-fixture covers analyzer + wire-table rendering; chi-basic uses no adapter-eligible types.
- **Generics** — `Page[T]` / `Result[T, E]`-synthetic covers analyzer + all four TS generators; chi-basic's `ListUsersResponse` could become `Page[User]` for full-pipeline integration.

These aren't friction for *users* — they're friction for a *future contributor* who'd want a single chi-basic golden run to prove every v0.2/v0.3 path. Doable in one focused session per feature (touches many goldens but mechanical once the analyzer + generators are byte-stable, which they are).

## Internal polish queued

- **`goduct.toml` project config** for `--adapter` (and likely `--framework`) — the CLI-flag form is enough for now; a file is the obvious follow-up when projects accumulate enough adapters that the Makefile gets unwieldy.
- **Status-code map expansion** beyond `200/201/204`.
- **Named-alias-of-named TS dedup** (Post-v0.1 known limitation; no concrete trigger yet).

## Bottom line

**v0.3-in-progress on `main` is the most user-seam-narrowing release the project has done.** A typical Go API author can now:

- Pick any of 4 routers via `--framework`.
- Use idiomatic or raw `http.HandlerFunc` handlers.
- Generate types, zod schemas, a typed fetch client, React Query hooks, and the Go adapter from one command.
- Hot-reload with `--watch`.
- Declare `decimal.Decimal=string` and `civil.Date=string` via `--adapter` for any rich types they import.
- Use `Page[T]`, `Result[T, E]`, and other generic structures freely in request/response signatures.

The two biggest remaining levers are **non-Go/TS consumers** (OpenAPI / Swagger / Postman — natural v0.3 cluster) and **streaming/realtime** (SSE / WebSocket / file upload — v0.4). After v0.3, generics tightens, gin/echo raw HandlerFunc, and the chi-basic golden refactors are the polish path to v0.4.

**Recommended next move:** cut `v0.3.0` now so `@latest` users get generics — it's a substantial feature that's been on `main` for one session and is fully tested. The OpenAPI cluster can land as `v0.3.1` or a separate `v0.4.0`. Holding v0.3.0 until OpenAPI lands is defensible (the roadmap promised both) but stretches the tag-vs-main gap further.

---

*See [`decisions/TODO.md`](decisions/TODO.md) for the granular post-v0.1 backlog (concrete triggers + remediations per item). See the README Roadmap for the user-facing version timeline.*
