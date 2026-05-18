# 0025. Correct the stale client.ts golden for ADR 0015

**Status:** Accepted
**Date:** 2026-05-18

## Context

During Prompt 10 (tsclient generator) pre-implementation review, a
contradiction was found between the chi-basic `client.ts` golden and
[ADR 0015](0015-query-header-optionality-rule.md). The golden's
`ListUsers` method signature renders `limit` as required:

```
list: async (params: { limit: number; cursor?: string })
```

But the IR (per ADR 0015's "validator tags constrain values, not
presence" rule) marks both `limit` and `cursor` as `Optional=true`
(verified against the analyzer: `limit` has `validate:"min=1,max=100"`,
no `required`; `cursor` has no validators; both `Param.Optional==true`).
The golden was hand-written months ago, before ADR 0015 existed, and
encodes a rule the project subsequently rejected: that a `min=1`
validator implies presence.

## Decision

The golden is the stale artifact; ADR 0015 is the rule. Correct
`examples/chi-basic/testdata/expected/client/client.ts` to render
`limit?: number` instead of `limit: number`. No other byte changes; the
method body's query-object construction already handles `undefined`
values correctly via the scaffolding's `request()` function
(`if (v !== undefined) usp.set(k, String(v))`).

Rationale: the "golden is the spec" discipline applies to unwritten
rules the golden correctly encodes. It does NOT apply to cases where the
golden encodes a rule that has since been written and rejected. ADR 0015
is explicit; the golden predates it. The IR is the contract; tsclient
follows the IR; the golden gets corrected.

## Consequences

- The chi-basic golden is no longer truly "frozen since v0.1
  scaffolding." This is the second deliberate correction (the first was
  ADR 0013's testdata move). Both are documented; the discipline is
  "frozen except when a documented ADR amends it."
- Users following the chi-basic example as a learning resource see
  consistent behavior between zod (which schemas a `min(1)` without
  `.optional()`) and the client (which renders the field as optional in
  the signature, validating the value via the schema when present).
- If other goldens reference the `ListUsers` signature (e.g. via type
  imports or comments), check and correct accordingly. As of this ADR,
  only `client.ts` is affected.

## Alternatives considered

- Introduce a "signature optionality" rule independent of ADR 0015 —
  rejected; ADR 0015's rationale (validator tags ≠ presence) applies to
  both wire and signature equally. Two rules would contradict each
  other.
- Leave the golden as-is and special-case tsclient — rejected; encodes
  the rejected rule in code, not just bytes.
- Modify ADR 0015 so `min=1` implies required — rejected; reverses a
  deliberate decision and would propagate inconsistency into zod and the
  Go adapter.

## Cross-references

- [ADR 0015](0015-query-header-optionality-rule.md) (the rule the golden
  contradicts)
- [ADR 0013](0013-un-nest-example-testdata-fixtures.md) (precedent for
  deliberate golden modification)
