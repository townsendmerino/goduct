# 0003. Make each generator an independent consumer of the shared IR

**Status:** Accepted
**Date:** 2026-05-17

## Context

goduct emits several outputs (TS types, zod schemas, fetch client, React Query
hooks, Go adapter). These could share code by chaining (the client generator
reading the types generator's output) or by each re-analyzing the Go source.
The README is explicit: the analyzer builds an IR, each generator consumes the
IR and emits one file, generators don't talk to each other, and adding a
generator means implementing one function, `Generate(ir.API, io.Writer)
error`. `ir.go`'s package doc states the IR is "the single contract that holds
the project together."

## Decision

Each output is an **independent generator** consuming the shared `ir.API`. No
generator depends on another generator. The IR is the contract; the analyzer
is the only producer of it.

## Consequences

- Easy: generators are independently testable (golden file per generator);
  new targets (SolidJS, Swift, Python) are purely additive; a broken generator
  can't corrupt another's output.
- Hard / giving up: every generator is gated on IR expressiveness — anything a
  generator needs must first exist in the IR, which centralizes design
  pressure on `ir.go`. Cross-generator optimizations are out of scope by
  construction.

## Alternatives considered

- Generators reading `go/packages` directly — rejected: each would
  re-implement analysis; no shared contract.
- Generators chaining off each other's output — rejected: introduces coupling
  the README explicitly forbids ("generators don't talk to each other").
