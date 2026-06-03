# 0038. `goduct.json` project config file (v0.4)

**Status:** Accepted
**Date:** 2026-06-02

## Context

Through v0.3 every `goduct gen` invocation is a self-contained
command line. As the flag surface grew (eight generators, `--out`,
`--dir`, `--tags`, `--tests`, `--watch`, `--framework`, `--adapter`),
two friction points emerged:

1. **Repetition.** A project's invocation rarely changes between
   runs. Users wrap goduct in a Makefile, a shell script, or a
   `go:generate` directive — all of which are ad-hoc places for what
   is really project config.
2. **No place for first-class OpenAPI metadata.** The OpenAPI
   generator currently hardcodes `Title = gen.PackageName(api)` and
   `Version = "0.0.0"` (ADR 0034). Users want to set `title`,
   `version`, `description`, and `servers` without a sidecar tool;
   passing six new flags to the CLI is the wrong UX.

A project-level config file resolves both. Open questions: **what
format**, **how is it loaded**, **how does it compose with CLI
flags**, **what schema does it expose**.

## Decision

### 1. Format: `goduct.json`, stdlib `encoding/json`

The config file is `goduct.json`. JSON is chosen over TOML because
goduct's value proposition includes a small, audit-able dependency
graph (v0.3 ships with only `fsnotify` and `x/tools`); the stdlib
has no TOML parser. Adding `BurntSushi/toml` for one file would
double the project's direct dep count for cosmetic gain. Hand-rolling
a TOML subset adds parser bug surface goduct would own forever.

JSON is also the format goduct already speaks — its golden outputs
include three JSON files (openapi.json, postman_collection.json,
swagger-ui.html embeds JSON). Users editing `goduct.json` are not
encountering a new syntax in this tool.

The file lives at the **project root** — the directory the user runs
`goduct gen` from. It is **not** discovered by walking upward; v0.4
keeps discovery dead-simple (`./goduct.json` from cwd, or whatever
`--config <path>` names). Walking upward like `go.mod` is a tempting
ergonomic but introduces ambiguity ("which goduct.json is loaded
when I run from a subdir?") that a fresh feature should not inherit.

### 2. Loading

- `--config <path>` selects an explicit file. Missing file → exit 1.
- No `--config` flag → look for `./goduct.json` relative to cwd. If
  absent → run as today (no config). If present and not parseable
  → exit 1.
- The JSON decoder uses `DisallowUnknownFields`. An unknown key is
  a loud-fail per ADR 0007: typos in config files are exactly the
  kind of silent surprise goduct rejects.
- Decoded values populate a `Config` struct mirroring the CLI flag
  set 1:1, plus the new `openapi` block.

### 3. Precedence: CLI > config > built-in default

For every overridable value, the CLI flag wins. If the flag was set
on the command line, its value is final; if it wasn't, the config's
value is used; if neither, the built-in default applies.

Bool flags need a distinguishing mechanism because the stdlib flag
package doesn't natively expose "was this flag set." The CLI walks
the parsed flagset with `fs.Visit` (which only enumerates flags
actually seen on the command line) and overlays those onto the
config-derived values. Flags not visited keep their config value.

`--all` and the `generators` config list compose as follows: if
`--all` is set on the command line (or `"all": true` in config), every
generator runs. Otherwise the union of CLI flags (`--types`, `--zod`,
etc.) and config's `generators` list runs — empty union is still an
error ("no generator selected"). This mirrors how a user thinks: "I
added `--openapi` for one run on top of my normal config."

The pattern (positional arg) is overridable too: if the CLI omits
the positional and config has `"pattern": "./api"`, the config value
is used. If both are present, the CLI wins. The "missing pattern"
loud-fail only triggers when both sources are empty.

### 4. Schema

```json
{
  "pattern":    "./api",
  "out":        "./web/src/api",
  "dir":        ".",
  "tags":       ["integration"],
  "tests":      false,
  "watch":      false,
  "framework":  "chi",
  "all":        false,
  "generators": ["types", "zod", "client", "hooks", "go-adapter"],
  "adapters": {
    "github.com/shopspring/decimal.Decimal": "string"
  },
  "openapi": {
    "title":       "My API",
    "version":     "1.0.0",
    "description": "Optional human-readable summary",
    "servers":     ["https://api.example.com"]
  }
}
```

- Top-level keys are scalars/arrays mirroring CLI flags (no nested
  tables except `openapi` and `adapters`).
- `generators` lists names from the existing dispatch table
  (`types`, `zod`, `client`, `hooks`, `openapi`, `swagger-ui`,
  `postman`, `go-adapter`). An unknown name is a loud-fail.
- `framework` must be one of the four supported values (ADR 0030);
  the CLI's existing pre-analysis validation applies.
- `adapters` is `map[string]string` matching the `--adapter Q=W`
  flag's effect; CLI's `--adapter` flags merge over (CLI keys win
  on conflict, per the precedence rule).
- `openapi` is the new metadata block (see §5).

### 5. OpenAPI metadata (`openapi.*`)

Currently `openapi.Generate` reads `gen.PackageName(api)` and the
literal string `"0.0.0"`. v0.4 keeps that as the *default* when no
config supplies metadata, and overrides per field when config does.

A new field on `ir.API` carries this metadata through to the generator:

```go
// ir.go (additive, ADR 0027):
type Meta struct {
    OpenAPITitle       string
    OpenAPIVersion     string
    OpenAPIDescription string
    OpenAPIServers     []string
}

type API struct {
    ...existing...
    Meta Meta
}
```

`api.Meta` is zero-valued unless the CLI populates it from
`goduct.json`. The analyzer does not read it; the CLI sets it after
`analyzer.Analyze` returns. This keeps the analyzer's
config-discovery surface zero (LoadOptions is not extended for
this; it stays focused on *what to analyze*, not *how to render*).

OpenAPI generator behavior:

- Title: `api.Meta.OpenAPITitle` if non-empty, else
  `gen.PackageName(api)`.
- Version: `api.Meta.OpenAPIVersion` if non-empty, else `"0.0.0"`.
- Description: emit `info.description` only when non-empty.
- Servers: emit a `servers:` array when non-empty.

Other generators (postman, swagger-ui) read no `Meta` fields in
v0.4; postman's existing per-route folder structure and swagger-ui's
sibling-`openapi.json` reference cover their needs. A future ADR may
extend Meta when those generators need more.

### 6. Coverage

- `internal/cliconfig` package (new, single file): the JSON decoder,
  the `Config` struct, the precedence overlay function.
  Unit-tested via small fixture strings — no on-disk fixture in
  testdata/.
- CLI integration: `cmd/goduct/main.go` calls the loader before
  `flag.Parse`'s effects are applied; the overlay function takes
  `(cfg *Config, fs *flag.FlagSet)` and updates only the unset
  flags. A new e2e sub-test in `e2e_test.go` writes a `goduct.json`
  into the temp dir and invokes `goduct gen ./api` (no `--out`,
  no `--types` etc. — all from config), asserts byte-equality
  against existing goldens.
- OpenAPI metadata: extend `openapi_test.go` with a sub-test that
  populates `api.Meta` and asserts the rendered `info.title`,
  `info.version`, `info.description`, and `servers[]` reflect it.
  Existing chi-basic openapi golden is untouched (api.Meta is
  zero, defaults apply, byte output unchanged).

## Consequences

**Easy / unblocked:**

- A project commits one `goduct.json` and CI runs `goduct gen` with
  no flags.
- OpenAPI title/version/description/servers stop being hardcoded
  placeholders; users get a real OpenAPI document without a
  post-processing step.
- Adding a future config knob is one struct field + one overlay
  line — no flag-table reshuffling.

**Hard / giving up:**

- Two sources of truth for invocation. A misconfigured
  `goduct.json` will surprise a user who expects their CLI flags
  alone to govern; the precedence rule and `DisallowUnknownFields`
  loud-fail mitigate this but can't eliminate it.
- JSON's verbosity for config (quoted keys, no comments) is worse
  UX than TOML. Accepted as the cost of zero-dep.
- No upward discovery means a user running `goduct gen` from a
  subdir gets no config unless they pass `--config ../goduct.json`.
  Documented in the README; not surprising once known.

## Alternatives considered

- **TOML via BurntSushi/toml.** Best file ergonomics, but doubles
  the direct dependency count of the module for one file. Rejected.
- **Hand-rolled TOML subset.** Zero-dep but adds ~150 LOC of parser
  with non-zero bug surface goduct would own forever. Rejected.
- **YAML.** Worse than TOML on every axis for this use case
  (significant whitespace, no stdlib parser, indented multi-line
  values are a foot-gun). Rejected.
- **Walk upward for goduct.json (like go.mod).** Tempting; introduces
  "which file got loaded" ambiguity when a user runs from a subdir.
  Deferred. The user explicitly passing `--config` covers the
  needed cases.
- **Put OpenAPI metadata in the source package** (e.g. a magic
  `var GoductMeta = ...` declaration the analyzer picks up).
  Couples analyzer to render-only concerns. Rejected.

## Cross-references

- [0007](0007-loud-failure-on-unsupported-input.md) —
  `DisallowUnknownFields` is the loud-fail surface for config typos.
- [0022](0022-generator-conventions.md) §1 — `Generate` signature
  unchanged; openapi reads metadata from `api.Meta`, not a new arg.
- [0027](0027-enrich-ir-for-go-side-codegen.md) — `Meta` addition
  is additive per the IR contract.
- [0030](0030-framework-adapter-selection.md) — framework values
  validated identically whether sourced from CLI or config.
- [0032](0032-custom-type-adapters.md) — `adapters` config block
  feeds the same `CustomAdapters` map; CLI's `--adapter` merges
  over with CLI keys winning on collision.
- [0034](0034-openapi-export.md) — title/version defaults preserved
  when config is absent; this ADR adds override paths only.
