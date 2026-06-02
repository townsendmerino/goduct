package main

// watch.go is the --watch implementation: fsnotify-based directory
// watch over api.SourceDirs with a debounced regen loop. Per ADR 0029.

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/townsendmerino/goduct/internal/ir"
)

// debounceWindow collapses a burst of fsnotify events (an editor's
// atomic-write idiom typically fires 3-6 per save) into a single
// regen. ADR 0029 §3 pins 250 ms — the empirical sweet spot.
const debounceWindow = 250 * time.Millisecond

// watchAndRegen subscribes to every directory in api.SourceDirs and
// re-runs generateOnce on each debounced burst, until SIGINT/SIGTERM.
// Returns nil on clean shutdown. Errors during a watch session are
// printed (with a timestamp) but do not abort — transient compile
// errors are expected during active development (ADR 0029 §4).
func watchAndRegen(api *ir.API, req runRequest) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("fsnotify: %w", err)
	}
	defer w.Close()

	currentDirs := append([]string(nil), dirsOf(api)...)
	for _, d := range currentDirs {
		if err := w.Add(d); err != nil {
			return fmt.Errorf("fsnotify: watch %s: %w", d, err)
		}
	}
	fmt.Fprintln(os.Stderr, "goduct: watching for changes (Ctrl-C to stop)")
	for _, d := range currentDirs {
		fmt.Fprintln(os.Stderr, "  "+d)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		cancel()
	}()

	var debounce *time.Timer
	fire := func() {
		ts := time.Now().Format("15:04:05")
		fmt.Fprintf(os.Stderr, "[%s] regenerating\n", ts)
		newAPI, err := generateOnce(req, true)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[%s] %v\n", ts, err)
			return
		}
		fmt.Fprintf(os.Stderr, "[%s] goduct: wrote outputs\n", ts)
		// SourceDirs may have shifted (new packages picked up, or one
		// went away). Reconcile so we don't watch stale dirs or miss
		// fresh ones.
		newDirs := dirsOf(newAPI)
		reconcileWatches(w, currentDirs, newDirs)
		currentDirs = newDirs
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-w.Events:
			if !ok {
				return nil
			}
			if !triggers(ev) {
				continue
			}
			if debounce != nil {
				debounce.Stop()
			}
			debounce = time.AfterFunc(debounceWindow, fire)
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "goduct: watch: %v\n", err)
			}
		}
	}
}

// dirsOf flattens api.SourceDirs (a map) into a stable slice. v0.1 is
// single-package, so this is one entry; the slice form is what
// fsnotify takes and is also what reconcileWatches compares against.
func dirsOf(api *ir.API) []string {
	out := make([]string, 0, len(api.SourceDirs))
	for _, d := range api.SourceDirs {
		out = append(out, d)
	}
	return out
}

// triggers reports whether an fsnotify event should schedule a regen.
// Per ADR 0029 §2: *.go files trigger; *_test.go and goduct_routes.go
// don't (the latter would create an infinite regen loop on
// --watch --go-adapter); go.mod does; other file types don't.
func triggers(ev fsnotify.Event) bool {
	if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove) == 0 {
		return false
	}
	base := filepath.Base(ev.Name)
	if base == "go.mod" {
		return true
	}
	if !strings.HasSuffix(base, ".go") {
		return false
	}
	if strings.HasSuffix(base, "_test.go") {
		return false
	}
	if base == "goduct_routes.go" {
		return false
	}
	return true
}

// reconcileWatches diffs two dir slices and adds/removes fsnotify
// subscriptions to match the second. Idempotent on no-change; safe to
// call after every regen.
func reconcileWatches(w *fsnotify.Watcher, before, after []string) {
	bset := make(map[string]bool, len(before))
	for _, d := range before {
		bset[d] = true
	}
	aset := make(map[string]bool, len(after))
	for _, d := range after {
		aset[d] = true
	}
	for d := range bset {
		if !aset[d] {
			_ = w.Remove(d)
		}
	}
	for d := range aset {
		if !bset[d] {
			_ = w.Add(d)
		}
	}
}
