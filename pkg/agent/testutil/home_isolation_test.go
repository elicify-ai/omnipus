// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package testutil

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// newIsolatedHomeForTest builds an IsolatedHome over a temp dir WITHOUT touching
// the process environment (IsolateProcessHome does, and would clobber HOME for
// every later test in this package).
func newIsolatedHomeForTest(t *testing.T) *IsolatedHome {
	t.Helper()
	dir := t.TempDir()
	return &IsolatedHome{Dir: dir, OmnipusDir: filepath.Join(dir, ".omnipus")}
}

func TestIsolatedHome_LeaksIsEmptyWhenNothingWasCreated(t *testing.T) {
	h := newIsolatedHomeForTest(t)
	if got := h.Leaks(); len(got) != 0 {
		t.Fatalf("Leaks() = %v on a home nothing wrote to, want none", got)
	}
	// Files outside .omnipus (tooling caches under HOME) are not a leak.
	if err := os.MkdirAll(filepath.Join(h.Dir, "Library", "Caches"), 0o750); err != nil {
		t.Fatal(err)
	}
	if got := h.Leaks(); len(got) != 0 {
		t.Fatalf("Leaks() = %v for files outside .omnipus, want none", got)
	}
}

// The instrument must be able to fail: a write under $HOME/.omnipus (what an
// un-isolated test does) has to be reported, with its relative path.
func TestIsolatedHome_LeaksSeesWhatWasCreatedUnderDotOmnipus(t *testing.T) {
	h := newIsolatedHomeForTest(t)
	if err := os.MkdirAll(filepath.Join(h.OmnipusDir, "system"), 0o750); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"system/audit.jsonl", ".milestones_migrated"} {
		if err := os.WriteFile(filepath.Join(h.OmnipusDir, rel), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{".milestones_migrated", "system", "system/audit.jsonl"}
	if got := h.Leaks(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Leaks() = %v, want %v", got, want)
	}
}
