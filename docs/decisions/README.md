# Decision log

Lightweight ADRs (Architecture Decision Records). One markdown file per
decision, numbered sequentially: `0001-name.md`, `0002-name.md`, …

## File structure

Every ADR follows this exact shape:

```markdown
# NNNN. Short imperative title

**Status:** Accepted | Superseded by NNNN | Reversed
**Date:** YYYY-MM-DD

## Context
What forced this decision. What was unclear, what tradeoffs existed.

## Decision
What we're doing. Imperative voice ("Use chi as the only v0.1 framework"),
not aspirational ("We should consider chi").

## Consequences
What this makes easy. What this makes hard. What we're explicitly giving up.

## Alternatives considered
Brief — one line each is fine. What we didn't pick and why.
```

## Rules

- **Never edit an accepted ADR's Decision section.** If a decision changes,
  write a new ADR that supersedes the old one, and change the old one's
  `Status` line to `Superseded by NNNN`. The Decision section is a historical
  record, not a living document.
- **ADRs are written when a decision is made**, not retroactively justified.
  If you're inventing the rationale after the fact, it isn't an ADR.
- **If an ADR is under 5 sentences total, the decision probably isn't
  important enough to record. Skip it.**
- Sections that would be speculation are written `TBD — discuss` rather than
  filled with invented rationale.

## Index

| ADR | Title | Status |
| --- | ----- | ------ |
| [0001](0001-handler-signature-convention.md) | Handler signature convention | Accepted |
| [0002](0002-v01-framework-scope.md) | v0.1 framework scope | Accepted |
| [0003](0003-generators-as-pipeline.md) | Generators as an IR pipeline | Accepted |
| [0004](0004-error-wire-format.md) | Error wire format | Accepted |
| [0005](0005-request-type-split-on-client.md) | Request type split on the client | Accepted |
| [0006](0006-validation-tag-translation.md) | Validation tag translation | Accepted |
| [0007](0007-loud-failure-on-unsupported-input.md) | Loud failure on unsupported input | Accepted |
| [0008](0008-react-query-deferred-to-v02.md) | React Query deferred to v0.2 | Accepted |
| [0009](0009-generated-adapter-same-package.md) | Generated adapter in the same package | Accepted |
| [0010](0010-name-and-pronunciation.md) | Name and pronunciation | Accepted |
| [0011](0011-golden-fixtures-nested-module.md) | Quarantine example golden fixtures in a nested module | Superseded by 0013 |
| [0012](0012-generated-code-passes-modernize.md) | Hold generated Go to the module's vet/modernize bar | Accepted |
| [0013](0013-un-nest-example-testdata-fixtures.md) | Un-nest the example; ignore golden fixtures via testdata/ | Accepted |
| [0014](0014-handler-signature-strictness.md) | Handler signature strictness (idiomatic mode) | Accepted |
| [0015](0015-query-header-optionality-rule.md) | Query/header parameter optionality rule | Accepted |
| [0016](0016-field-source-in-ir.md) | Field source in the IR | Accepted |
| [0017](0017-special-stdlib-types.md) | Special standard-library (and well-known) types | Accepted |
| [0018](0018-type-traversal-failure-boundaries.md) | Type-traversal failure boundaries | Accepted |
| [0019](0019-error-message-formats-by-layer.md) | Error message formats by layer | Accepted |
| [0020](0020-body-field-optionality.md) | Body-field optionality | Accepted |
| 0021 | _(number reserved, not used — originally a standalone "param type restrictions" ADR; rolled into the ADR 0014 amendment, commit `8b65ad5`)_ | Not used |
| [0022](0022-generator-conventions.md) | Generator conventions | Accepted |
| [0023](0023-godoc-to-jsdoc-transformation.md) | godoc → JSDoc transformation | Accepted |
| [0024](0024-doc-comment-emission-policy.md) | Per-generator doc-comment emission policy | Accepted |
| [0025](0025-correct-stale-golden-for-list-users.md) | Correct the stale client.ts golden for ADR 0015 | Accepted |
| [0026](0026-goadapter-request-type-name-convention.md) | goadapter request-type-name convention (v0.1) | Superseded by 0027 |
| [0027](0027-enrich-ir-for-go-side-codegen.md) | Enrich the IR for Go-side code generation (v0.2) | Accepted |
| [0028](0028-react-query-hooks-design.md) | React Query hooks generator design (v0.2) | Accepted |
| [0029](0029-watch-mode-design.md) | `--watch` mode design (v0.2) | Accepted |
| [0030](0030-framework-adapter-selection.md) | Framework-adapter selection mechanism and structure (v0.2) | Accepted |
| [0031](0031-raw-handlerfunc-mode.md) | Raw `http.HandlerFunc` mode (v0.2) | Accepted (§3 gin/echo superseded by 0037) |
| [0032](0032-custom-type-adapters.md) | Custom type adapters (v0.2) | Accepted |
| [0033](0033-generics.md) | Generics in request/response types (v0.3) | Accepted |
| [0034](0034-openapi-export.md) | OpenAPI 3.1 export (v0.3) | Accepted |
| [0035](0035-openapi-sibling-generators.md) | Swagger UI + Postman collection generators (v0.3) | Accepted |
| [0036](0036-constraint-generics.md) | Constraint generics (v0.4) | Accepted |
| [0037](0037-gin-echo-raw-handlerfunc.md) | gin/echo raw `http.HandlerFunc` via context-bridge wrappers (v0.4) | Accepted |
| [0038](0038-project-config-file.md) | `goduct.json` project config file (v0.4) | Accepted |
| [0039](0039-openapi-polish-trio.md) | OpenAPI polish trio: examples, security schemes, per-status responses (v0.4) | Accepted |
