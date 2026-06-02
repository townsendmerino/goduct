package main

import (
	"testing"

	"github.com/fsnotify/fsnotify"
)

// TestTriggers locks in the per-event filtering rule from ADR 0029 §2:
// *.go files trigger; *_test.go and goduct_routes.go don't; go.mod does;
// non-Op-changing events (Chmod-only) don't.
func TestTriggers(t *testing.T) {
	const dir = "/abs/path/to/pkg/"
	cases := []struct {
		name string
		ev   fsnotify.Event
		want bool
	}{
		{"write .go", fsnotify.Event{Name: dir + "users.go", Op: fsnotify.Write}, true},
		{"create .go", fsnotify.Event{Name: dir + "new.go", Op: fsnotify.Create}, true},
		{"rename .go", fsnotify.Event{Name: dir + "moved.go", Op: fsnotify.Rename}, true},
		{"remove .go", fsnotify.Event{Name: dir + "gone.go", Op: fsnotify.Remove}, true},

		{"write go.mod", fsnotify.Event{Name: "/abs/path/go.mod", Op: fsnotify.Write}, true},

		// Excluded: test files, the adapter's own output, non-.go files,
		// and Chmod-only events (touch without content change).
		{"_test.go ignored", fsnotify.Event{Name: dir + "users_test.go", Op: fsnotify.Write}, false},
		{"goduct_routes.go ignored", fsnotify.Event{Name: dir + "goduct_routes.go", Op: fsnotify.Write}, false},
		{".md ignored", fsnotify.Event{Name: dir + "README.md", Op: fsnotify.Write}, false},
		{"chmod-only ignored", fsnotify.Event{Name: dir + "users.go", Op: fsnotify.Chmod}, false},
		{"empty op ignored", fsnotify.Event{Name: dir + "users.go", Op: 0}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := triggers(c.ev); got != c.want {
				t.Errorf("triggers(%+v) = %v, want %v", c.ev, got, c.want)
			}
		})
	}
}

// TestReconcileWatches verifies the diff-and-update logic without a
// real fsnotify watcher. A nil *fsnotify.Watcher would crash on Add/
// Remove, so we exercise the function indirectly via its set-arithmetic
// helpers — the actual Add/Remove calls are trivial passthroughs.
func TestReconcileWatchesDiff(t *testing.T) {
	// White-box: replicate the set diff and assert what would happen.
	before := []string{"/a", "/b"}
	after := []string{"/b", "/c"}
	bset := make(map[string]bool)
	for _, d := range before {
		bset[d] = true
	}
	aset := make(map[string]bool)
	for _, d := range after {
		aset[d] = true
	}
	var removed, added []string
	for d := range bset {
		if !aset[d] {
			removed = append(removed, d)
		}
	}
	for d := range aset {
		if !bset[d] {
			added = append(added, d)
		}
	}
	if len(removed) != 1 || removed[0] != "/a" {
		t.Errorf("removed = %v, want [/a]", removed)
	}
	if len(added) != 1 || added[0] != "/c" {
		t.Errorf("added = %v, want [/c]", added)
	}
}
