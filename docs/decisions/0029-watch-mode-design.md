# 0029. `--watch` mode design (v0.2)

**Status:** Accepted
**Date:** 2026-06-02

## Context

The v0.2 roadmap promises `--watch` mode: re-run `goduct gen`
automatically when source files change. Without it, every backend
change requires a manual `goduct gen` invocation — friction during
active development.

The shape is open: file-system watcher choice, debounce behavior, what
exactly to watch, error handling during a watch session, and how the
flag fits into the existing CLI. Each is a substantive call; this ADR
pins them so implementation is mechanical.

## Decision

`--watch` is a flag on `goduct gen` (`goduct gen <pattern> --out <dir>
--types --watch ...`). When set, goduct runs the requested generators
once and then watches the source directories for changes,
regenerating on each debounced burst until interrupted.

### 1. Watcher: fsnotify

[`github.com/fsnotify/fsnotify`](https://github.com/fsnotify/fsnotify)
is the canonical Go file-system notification library and the obvious
choice: it uses OS-native events (inotify / FSEvents / kqueue /
ReadDirectoryChangesW), is widely adopted, and matches what every
other modern Go dev tool uses (e.g. `air`, `delve`'s watch features).
Polling is **rejected** — wastes CPU and has worse latency on every
target platform.

`fsnotify` becomes a direct dependency. It is the *only* runtime
dependency goduct itself takes on; the existing build is stdlib +
`golang.org/x/tools/go/packages`, and `fsnotify` is similarly
load-bearing for the watch surface.

### 2. What to watch: `api.SourceDirs`

ADR 0027 added `ir.API.SourceDirs` (pkg import path → fs dir). The
watcher subscribes to every entry in that map after the first
successful `Analyze`. Directories (not individual files) are watched,
so new files appearing trigger a regen and old files removed do not
silently de-list.

Files filtered **out** of trigger events:

- Anything not matching `*.go` (Markdown, JSON, etc. don't change the
  IR; ignoring them avoids spurious work).
- `*_test.go` — the analyzer reads `_test.go` only when
  `LoadOptions.Tests` is set; standard `goduct gen` doesn't, so
  changes there don't affect output.
- `goduct_routes.go` — goduct's own adapter output. Without this
  filter, `--watch --go-adapter` becomes an infinite regen loop:
  every gen writes the adapter, which fires a fsnotify Write, which
  triggers regen.

`go.mod` is **included** — dependency changes can shift type
resolution and the analyzer caches need to redo work.

### 3. Debounce: 250 ms

fsnotify emits multiple events per editor save (atomic-write idioms
do open → write → rename → chmod, each surfacing as a separate event).
Running the full Analyze + 5 generators per event would be wasteful
and visibly janky.

A 250 ms debounce window starts on the first event and resets on each
subsequent event; regen fires when the window closes quietly. The
common case (editor save) collapses to one regen even if 4-6 events
fire. 250 ms is the empirical sweet spot every other watch tool has
converged on; tighter feels jumpy, looser feels unresponsive.

The debounce window is **not** configurable in v0.2 — one less knob,
sensible default, can be revisited if a real user surfaces a need.

### 4. Error handling: first-run-fatal, then continue

The first regen behaves exactly like a normal `goduct gen` — any
error from `Analyze` or any generator causes exit 1 with the error
on stderr. The user has bigger problems than starting a watch
session.

Once the watch loop is running, every subsequent regen prints
errors but **continues watching**. Transient compile errors are
expected during active development (mid-keystroke, a comma is
missing); aborting the watch session every time the source is briefly
broken is hostile. The user sees the error and fixes it; the next
event triggers a fresh regen.

### 5. Signal handling: SIGINT clean shutdown

`Ctrl-C` cleanly tears down the fsnotify watcher and exits 0.
SIGTERM gets the same treatment. No two-step "press Ctrl-C again to
force quit" — one signal, one exit.

### 6. Output formatting during watch

The first regen prints the existing `goduct: wrote N file(s)` /
per-path lines. After that, each subsequent regen prefixes its output
with a timestamp:

```
[14:32:01] regenerating
[14:32:01] goduct: wrote 5 file(s)
  /path/to/types.ts
  ...
[14:34:12] regenerating
[14:34:12] error: goduct: <file>:<line>:<col>: <msg>
```

Timestamps match the `[HH:MM:SS]` convention common to other watch
tools. They make it easy to correlate a save with a regen in a
scrolling terminal.

### 7. CLI structure: flag on `gen`

`--watch` is a `bool` flag on `goduct gen`, not a separate subcommand
(`goduct watch`). Reasons:

- The watch session reuses the entire gen-flag set
  (`--types/--zod/--client/--hooks/--go-adapter/--all`, `--out`,
  `--dir`, `--tags`, `--tests`).
- A separate subcommand would duplicate the flag declarations and
  give the user a second invocation form to remember.
- The README's roadmap line already calls it `--watch mode` (flag).

## Consequences

**Easy / unblocked:**

- Active backend development loops: edit `api/users.go`, save,
  TypeScript types update instantly in the browser via vite/webpack
  HMR. The friction goduct removes between Go and TS becomes
  *zero-friction*.
- Build-pipeline use (CI generating once and exiting) is unaffected
  — `--watch` is opt-in.

**Hard / giving up:**

- One new direct dependency. Mitigated: `fsnotify` is widely used,
  well-maintained, and goduct only needs its top-level API
  (`NewWatcher`, `Add`, `Events`, `Errors`). Limited blast radius.
- Per-platform behavior nuances (e.g. inotify watch limits on Linux,
  rename semantics on macOS) inherit through to goduct. Users hitting
  the inotify limit get fsnotify's error message; goduct doesn't
  invent its own.

**Out of scope:**

- HMR-style incremental regen (only regenerate the file affected by
  the change). v0.2 ships full-API regen on every burst; goduct's gen
  is fast enough that incremental isn't worth the complexity.
- Watching from `go install`'d goduct's own source for development.
  Users who want that wrap `goduct gen --watch` in their own tool.

## Alternatives considered

- **Polling instead of fsnotify** — rejected. Wastes CPU; worse
  latency on every target platform; no win.
- **Configurable debounce window** — rejected for v0.2. 250 ms is
  the well-known sweet spot; add the knob if a real user needs it.
- **`goduct watch` as a separate subcommand** — rejected. Forces flag
  duplication; second invocation form to remember.
- **Watching individual files instead of directories** — rejected.
  Misses new files; fsnotify's directory watching handles add/remove
  in one subscription.
- **HMR-style incremental regen** — rejected for v0.2 (complexity
  vs. full-regen speed); revisit if/when regen latency becomes a
  real bottleneck.
- **Error during watch = exit** — rejected. Active development
  involves transient broken states; aborting on each is hostile.

## Cross-references

- [0007](0007-loud-failure-on-unsupported-input.md) — loud-failure
  principle; first-run errors still abort, watch-mode errors still
  print loudly.
- [0027](0027-enrich-ir-for-go-side-codegen.md) — `api.SourceDirs`,
  the data structure `--watch` subscribes to.
