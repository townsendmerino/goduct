# 0026. goadapter request-type-name convention (v0.1)

**Status:** Superseded by [0027](0027-enrich-ir-for-go-side-codegen.md)
**Date:** 2026-05-18

## Context

The goadapter generator emits Go wrapper functions that each begin with
`var req <RequestType>`. Resolving the request type requires knowing the
handler's second parameter type. For body routes (POST/PUT/PATCH with
JSON fields) this is recoverable from `ir.Route.BodyType.Named`. For
non-body routes (GET, DELETE, or body-less POST/PUT/PATCH),
`ir.Route` does not carry the request type — the IR was designed around
the wire shape, and non-body routes have no wire-body request to model.

This is a real gap: the analyzer's `DiscoverTypes` *does* include the
request type in `api.Types` (it is reachable from the handler's
signature during type traversal), but it is not linked back to the
`Route` in the IR. goadapter, which consumes `*ir.API` alone (no
`*packages.Package`), cannot re-resolve from the package.

## Decision

For v0.1, goadapter uses this resolution rule:

1. If `route.BodyType` is non-nil and `KindNamed`, use the short name
   of `BodyType.Named`.
2. Otherwise, look up `<SourcePath>.<HandlerName>Request` in
   `api.Types`. If present, use its short name.
3. Otherwise, panic per ADR 0022 §5 with a clear message naming the
   missing convention and the planned v0.2 fix.

This pins a v0.1 naming convention: when a route has no JSON body, its
request type MUST be named `<HandlerName>Request`. This convention is
satisfied by every chi-basic handler. Users with non-conforming names
hit the panic with clear remediation.

v0.2 plan: add a `RequestType *TypeRef` field to `ir.Route`, populated
by `DiscoverRoutes` (which already has the handler's signature in hand).
goadapter and any future Go-side generator then read it directly and the
convention falls away. The IR change is additive and
backward-compatible.

## Consequences

- Users with non-body routes whose request type is named differently
  from the convention get a clear panic at generation time, not a silent
  miscompilation.
- The chi-basic example satisfies the convention naturally because Go
  programmers tend to use this naming pattern. v0.1 documentation should
  call out the requirement.
- The IR remains frozen; the fix is deferred to v0.2 via an additive
  field, which is the right cost-benefit for now.

## Alternatives considered

- Add `RequestType` to `ir.Route` in v0.1 — rejected. The IR is frozen
  per [0016](0016-field-source-in-ir.md); reopening it for one
  generator's convenience invites scope creep and triggers
  re-verification of every analyzer-layer test.
- Pass `*packages.Package` to `Generate` — rejected.
  [0022](0022-generator-conventions.md) §1 fixes the generator
  signature; the package isn't a generator concern.
- Make the analyzer panic on non-conforming names — rejected. The naming
  convention is a goadapter constraint, not an analyzer rule; the TS
  generators consume types, not handlers, and don't care.
- Match by field-set — rejected as over-clever and fragile.

## Cross-references

- [0014](0014-handler-signature-strictness.md) (handler signature;
  allows any struct name).
- [0016](0016-field-source-in-ir.md) (IR frozen).
- [0022](0022-generator-conventions.md) §5 (panic on
  internal-invariant / convention violation).
- `TODO.md` — v0.2 `ir.Route.RequestType` field.
