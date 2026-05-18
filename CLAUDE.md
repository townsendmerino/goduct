# CLAUDE.md — Working on this repository

This file is read by Claude Code at the start of every session in this
repository. It exists to keep the project's decision discipline intact
across sessions and to prevent re-deriving rules that have already
been pinned.

## Project orientation

`goduct` reads Go HTTP handlers and generates TypeScript types, zod
validators, a typed fetch client, and a chi router adapter. The pitch
and full feature list are in `README.md`. The current state is pre-1.0;
the v0.1 milestone is in progress.

The repository is hosted at https://github.com/townsendmerino/goduct.
The `main` branch tracks `origin/main`. During pre-1.0 development,
commits land directly on `main` (no feature branches yet); this may
change post-1.0. Push to `origin/main` after each successful milestone
unless instructed otherwise.

## How decisions are made and recorded

**This project is ADR-driven.** Every non-trivial design choice lives
in `docs/decisions/` as a numbered ADR. Before making a non-trivial
design choice — anything that future-you would ask "wait, why did we
do it this way?" about — check the existing ADRs.

If your proposed work would contradict an accepted ADR, STOP and ask.
Do not silently reconcile.

The convention for ADRs is in `docs/decisions/README.md`. The key
rules:

- ADRs are numbered sequentially. Burned numbers get a one-line
  tombstone in the index, not deletion (see ADR 0021 for the pattern).
- The Decision section of an accepted ADR is immutable. If the
  decision changes, write a new ADR that supersedes the old one and
  update the old ADR's Status line to "Superseded by NNNN" (see
  ADRs 0011 → 0013).
- Cross-reference errors (typos, broken links) are editable like
  typos. Substantive content changes are not.
- Empirical findings discovered during implementation go in the
  Consequences section of the relevant ADR, with a note in the
  commit message flagging the addition. Verbatim adherence to
  dictated text is less important than accuracy (see ADR 0023's
  history for the precedent).

## Working pattern that has served this project

The project has shipped seven major milestones (analyzer, four
generators, orchestration, CLI) using one consistent pattern. The
pattern is not optional; it has caught real defects every milestone
it has been applied to.

### 1. Probe before implementing

When a milestone has frozen artifacts (a golden file, the IR, an
existing ADR), construct a small probe before writing implementation
code:

- Load the IR via `analyzer.Analyze` against the relevant example
- Read the golden file byte-by-byte
- For every byte the golden encodes, identify which rule in the
  prompt or ADRs predicts it
- Any byte not predicted is a candidate unwritten rule

The probe is mandatory pre-implementation work, not a discretionary
practice. It does not require permission; it is part of how
milestones are done in this project.

### 2. Surface contradictions as decisions, not silent reconciliations

When the probe finds a contradiction, write a paste-ready block that:

- Names the contradiction precisely (which prompt section, which
  golden byte, which ADR)
- Identifies two or three resolution options
- Recommends one with reasoning
- Waits for the decision

Do not invent a category, a rule, or a reconciliation on your own.
The discipline has caught contradictions between prompts and goldens,
between ADRs and goldens, between prompts and frozen IR, and between
ADRs and prompts. Every one of these has been resolved as a deliberate
decision (often as a new ADR or amendment). Silent reconciliation
would have shipped defects.

### 3. Faithful reporting

Every milestone ends with a structured report:

- Byte-diff result against the relevant golden (should be empty)
- Test status (`go test ./...`, with `-count=1` for non-cached runs)
- Vet status (`go vet ./...`)
- Line counts of all new/modified files
- Any deviations from the prompt, with reasons
- Any new TODOs
- Any "STOP and flag" conditions that triggered, with how each was
  resolved

Deviations are flagged, not buried. If a prompt's expected output
list is wrong, the report says so. If a line-count cap was exceeded
with permission, the report names the permission. If a defensive
catch-all was added that isn't in any ADR, the report flags it as
needing an ADR category before merge.

### 4. Naming what's deferred

Spec-trust items (implemented per spec but not verified by a golden)
go in `TODO.md` with a concrete remediation: either add a coverage
example, or explicitly accept the gap in the README's "What's
supported" section. Spec-trust is not silent acceptance; it's named
deferral with a trigger.

## Loud failure is the rule

ADR 0007 is the governing principle. When the analyzer or generators
encounter something they don't fully understand, they error with a
clear file:line:col pointer and a remediation hint. They never
silently skip, default, or guess.

This applies to your own code. If you find yourself adding a
defensive catch-all that "should never trigger," it gets a category
in the relevant ADR (or one of `PATH*` / `INTERNAL*` prefixes per
ADR 0018's amendment) with a clear message. Defensive code is fine;
unnamed defensive code is not.

Generators panic on unknown `ir.TypeRef` builtins or unhandled
`Kind`s per ADR 0022 §5. This is the same pattern, applied to
generator-internal invariants.

## Commit hygiene

Commits land in small, reversible chunks. The patterns:

- One commit per logical change. ADRs commit separately from code
  that consumes them, even if landing in the same milestone.
- Commit messages follow conventional commits (`feat:`, `fix:`,
  `docs:`, `refactor:`, `chore:`) with parenthesized scope where
  meaningful (`feat(analyzer)`, `feat(tsclient)`, `docs:`).
- Commit message bodies cite the relevant ADRs by number when the
  commit implements or amends them.
- Forced pushes to `main` are not permitted now that the repo is on
  GitHub. The first push to GitHub was forced because the remote
  had auto-created files; that's the only acceptable use. From now
  on, commits append.

If you're unsure whether something is one commit or two, two is
usually right. The decision log is a portfolio artifact in addition
to a working record; readability matters.

## Files and directories of note

```
README.md                       Pitch + invocation form. UX-spec
                                for the CLI in addition to a sales
                                document. Pre-v0.1 reconciliation
                                pending.
TODO.md                         Pre-v0.1 reconciliation queue.
                                Each entry has a concrete trigger.
docs/decisions/                 ADRs. The contract.
docs/decisions/README.md        Index + convention.
internal/ir/                    The frozen contract. Do not modify
                                without a superseding ADR.
internal/analyzer/              Loader, route discovery, type
                                traversal, orchestration.
internal/gen/                   Shared helpers used by every
                                generator.
internal/generators/            tstypes, zod, tsclient, goadapter.
runtime/                        The small shipped library
                                (goduct.Error, WriteError, WriteJSON).
                                User code imports this.
examples/chi-basic/             The integration test fixture.
                                api/ is the input; testdata/expected/
                                is the byte-target.
cmd/goduct/                     The CLI binary.
```

## Working with the user

The user (Francis) is the architect for this project; you are the
implementer. The collaboration pattern:

- User writes prompts, which are strong sketches, not final specs.
  Goldens and ADRs are the final spec; prompts can be wrong, and
  often are. Your job includes surfacing where prompts and final
  specs disagree.
- User makes the substantive design calls. You implement, test,
  and report.
- You can — and should — push back when an instruction would
  contradict an ADR or a frozen artifact. The user has reversed
  prompt decisions in your favor when you flagged a contradiction
  (see ADRs 0013, 0025).
- Questions like "should I add a TODO for X" are appropriate when
  the answer isn't obvious from the existing pattern. The pattern
  has been: yes, add TODOs for spec-trust items, but only as their
  own commits unless they're directly entailed by another commit's
  work.

## What "done" means for a milestone

A milestone is done when:

- The relevant golden test passes byte-identically
- `go test ./...` is green with `-count=1`
- `go vet ./...` is clean
- All new ADRs are committed and indexed
- All deviations are reported, not hidden
- Commits are pushed to `origin/main`

Not done:

- "It works on my machine but the test isn't written yet"
- "I added the code but the ADR is still a draft"
- "It works but I noticed something weird and didn't flag it"

If any of these apply, the milestone is in progress, not complete.

## Remaining v0.1 milestones (as of this writing)

1. End-to-end golden test — a `go test` target that runs the CLI
   binary against chi-basic and diffs all four outputs.
2. README/TODO reconciliation pass — clear pre-v0.1 TODOs, update
   README to match ADR-pinned reality, possibly tag v0.1.0.

Both small.
