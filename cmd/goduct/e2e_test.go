package main

// End-to-end integration test: builds the goduct binary from the
// current source and runs it as a subprocess against
// examples/chi-basic/api, asserting all four outputs are
// byte-identical to testdata/expected/. This is the integration
// acceptance criterion the chi-basic fixture was scaffolded for.
//
// It deliberately does NOT duplicate the per-generator golden unit
// tests (fast, focused, already in place). What it covers that they
// cannot: the compiled binary's `gen` subcommand + flag dispatch, the
// --out vs. beside-source output-path split (ADR 0009), atomic
// all-or-nothing writes, and process exit codes on the loud-failure
// paths (ADR 0007 / Prompt 12). No build tag: the test runs in well
// under the ~3s threshold (see report); it runs on every `go test`.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func e2eRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0) // cmd/goduct/e2e_test.go
	r, err := filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// procExit returns a finished command's exit code. Run() populates
// ProcessState whenever the process actually started; a nil state
// means exec itself failed (a setup problem, not a CLI behavior).
func procExit(t *testing.T, c *exec.Cmd) int {
	t.Helper()
	if c.ProcessState == nil {
		t.Fatalf("subprocess did not start: %v", c)
	}
	return c.ProcessState.ExitCode()
}

// head returns the first n lines of b, for a readable mismatch dump.
func head(b []byte, n int) string {
	lines := strings.SplitN(string(b), "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

func TestEndToEnd_ChiBasic(t *testing.T) {
	root := e2eRepoRoot(t)
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "goduct")

	// 1. Build from the CURRENT source into tmp — never a stale
	//    `go install`'d binary or a $PATH ambiguity. A build failure
	//    here is a setup issue, not a regression: skip, don't fail.
	build := exec.Command("go", "build", "-o", bin, "./cmd/goduct")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Skipf("go build ./cmd/goduct failed (setup, not a regression): %v\n%s", err, out)
	}

	// 2. Happy path: every generator, run from the repo root.
	outDir := filepath.Join(tmp, "client")
	cmd := exec.Command(bin, "gen", "./examples/chi-basic/api", "--out", outDir, "--all")
	cmd.Dir = root
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()

	// The Go adapter lands beside the source package (ADR 0009), NOT
	// under --out — a real file in a real package dir. Register its
	// removal BEFORE any assertion that might t.Fatal: exit 0 means it
	// was written, and t.Cleanup must run even if a later byte-compare
	// fails or the existence check fatals. The Prompt 12 gitignore
	// makes a stray file harmless to commits; this keeps later test
	// runs clean too (belt-and-suspenders).
	adapter := filepath.Join(root, "examples", "chi-basic", "api", "goduct_routes.go")
	t.Cleanup(func() { os.Remove(adapter) })

	// 3. Exit code 0.
	if runErr != nil || procExit(t, cmd) != 0 {
		t.Fatalf("goduct gen --all: exit %d (%v)\nstderr:\n%s",
			procExit(t, cmd), runErr, stderr.String())
	}

	type pair struct{ got, want string }
	exp := func(p string) string {
		return filepath.Join(root, "examples/chi-basic/testdata/expected", p)
	}
	files := []pair{
		{filepath.Join(outDir, "types.ts"), exp("client/types.ts")},
		{filepath.Join(outDir, "schemas.ts"), exp("client/schemas.ts")},
		{filepath.Join(outDir, "client.ts"), exp("client/client.ts")},
		{filepath.Join(outDir, "hooks.ts"), exp("client/hooks.ts")},
		{adapter, exp("chi/goduct_routes.go")},
	}

	// 4. All five exist (the beside-source adapter included).
	for _, f := range files {
		if _, err := os.Stat(f.got); err != nil {
			t.Fatalf("expected output missing: %s: %v", f.got, err)
		}
	}

	// 5. Byte-compare each to its golden.
	for _, f := range files {
		got, err := os.ReadFile(f.got)
		if err != nil {
			t.Fatalf("read generated %s: %v", f.got, err)
		}
		want, err := os.ReadFile(f.want)
		if err != nil {
			t.Fatalf("read golden %s: %v", f.want, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s != golden (got %d bytes, want %d bytes)\n"+
				"--- want (first 20 lines) ---\n%s\n--- got (first 20 lines) ---\n%s",
				filepath.Base(f.got), len(got), len(want), head(want, 20), head(got, 20))
		}
	}

	// Secondary: loud-failure CLI surface the happy path never reaches.
	// (The prior "exit-2 for --hooks" subtest was removed in v0.2 when
	// --hooks flipped from deferred-with-pointer to a real generator;
	// see ADR 0028. The no-generator path below still exercises the
	// usage-error loud-failure contract.)
	t.Run("exit code 2 for no generators selected", func(t *testing.T) {
		var se bytes.Buffer
		c := exec.Command(bin, "gen", "./examples/chi-basic/api")
		c.Dir, c.Stderr = root, &se
		_ = c.Run()
		if code := procExit(t, c); code != 2 {
			t.Fatalf("no-generator exit = %d, want 2\nstderr:\n%s", code, se.String())
		}
		if !strings.Contains(se.String(), "no generator selected") {
			t.Errorf("no-generator stderr missing the usage hint:\n%s", se.String())
		}
	})
}
