// Omnipus — tests for epoch.go's four guardrails: absence-vs-corruption,
// atomic persistence, in-process concurrency safety, and the exact bump
// arithmetic.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestIndexEpoch_NeverTouchedIsZeroNoError is guardrail 1's first half: a
// collection with no epoch file at all is a fresh baseline, not an error.
func TestIndexEpoch_NeverTouchedIsZeroNoError(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()

	got, err := IndexEpoch(home, root)
	if err != nil {
		t.Fatalf("IndexEpoch on a never-touched collection returned an error: %v", err)
	}
	if got != 0 {
		t.Fatalf("IndexEpoch on a never-touched collection = %d, want 0", got)
	}
}

// TestIndexEpoch_CorruptFileIsLoudNotZero is guardrail 1's second half, and
// the one this whole design exists to get right: an unreadable epoch file
// must not silently compare equal to "never touched". If this test is
// deleted or weakened to accept a nil error, IndexEpoch would report a
// corrupt collection as freshly baselined — exactly the defect class named
// in epoch.go's doc comment (the unset index-phase and the unreadable
// manifest, both of which shipped once already on this branch).
func TestIndexEpoch_CorruptFileIsLoudNotZero(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()

	dir, err := IndexDirFor(home, root)
	if err != nil {
		t.Fatalf("IndexDirFor: %v", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	path := filepath.Join(dir, indexEpochFileName)
	if err := os.WriteFile(path, []byte("{not valid json"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := IndexEpoch(home, root)
	if err == nil {
		t.Fatalf("IndexEpoch on a corrupt epoch file returned no error (got epoch=%d) — "+
			"a corrupt file must never read as a fresh baseline", got)
	}
	// The zero value MUST still be accompanied by the error — a caller that
	// forgets to check err and uses got directly gets 0, which is why the
	// error must never be nil for this case.
	if got != 0 {
		t.Fatalf("IndexEpoch on a corrupt file returned epoch=%d, want 0 (alongside the error)", got)
	}
}

// TestIndexEpoch_NegativeIsLoud guards the same absence-vs-corruption line
// against a different corruption shape: a syntactically valid JSON file
// holding a value the counter can never legitimately hold.
func TestIndexEpoch_NegativeIsLoud(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()

	dir, err := IndexDirFor(home, root)
	if err != nil {
		t.Fatalf("IndexDirFor: %v", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	path := filepath.Join(dir, indexEpochFileName)
	if err := os.WriteFile(path, []byte(`{"epoch":-3}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := IndexEpoch(home, root); err == nil {
		t.Fatal("IndexEpoch on a negative epoch value returned no error")
	}
}

// TestBumpIndexEpoch_IncrementsAndPersists is guardrail 2: the bump is
// exactly +1, and it survives being re-read.
func TestBumpIndexEpoch_IncrementsAndPersists(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()

	first, err := BumpIndexEpoch(home, root)
	if err != nil {
		t.Fatalf("first BumpIndexEpoch: %v", err)
	}
	if first != 1 {
		t.Fatalf("first bump = %d, want 1 (0 -> 1)", first)
	}

	second, err := BumpIndexEpoch(home, root)
	if err != nil {
		t.Fatalf("second BumpIndexEpoch: %v", err)
	}
	if second != 2 {
		t.Fatalf("second bump = %d, want 2", second)
	}

	// Persistence: an independent read sees the same value the bump returned.
	got, err := IndexEpoch(home, root)
	if err != nil {
		t.Fatalf("IndexEpoch after bumps: %v", err)
	}
	if got != 2 {
		t.Fatalf("IndexEpoch after two bumps = %d, want 2", got)
	}
}

// TestBumpIndexEpoch_AtomicNoHalfWrite is guardrail 2's crash-safety half: a
// write that fails must never leave a file that PARSES as a smaller number
// than the true value. This test proves the mechanism (WriteFileAtomic:
// temp file + rename) rather than injecting a real crash — a torn write is
// not reproducible from a test without fault injection into the OS, and
// WriteFileAtomic's own guarantee is exercised directly here: after N
// successful bumps the file must be exactly N, never a value that could
// only arise from a torn write (e.g. a truncated leading digit).
func TestBumpIndexEpoch_AtomicNoHalfWrite(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()

	const n = 25
	var last int64
	for i := 0; i < n; i++ {
		v, err := BumpIndexEpoch(home, root)
		if err != nil {
			t.Fatalf("bump %d: %v", i, err)
		}
		last = v
	}
	if last != n {
		t.Fatalf("after %d sequential bumps, epoch = %d, want %d", n, last, n)
	}

	dir, err := IndexDirFor(home, root)
	if err != nil {
		t.Fatalf("IndexDirFor: %v", err)
	}
	// No leftover temp file from WriteFileAtomic's rename — a stray .tmp*
	// entry would indicate a rename that did not complete cleanly.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != indexEpochFileName && e.Name() != indexEpochFileName+".lock" {
			t.Fatalf("unexpected leftover file in index dir: %s", e.Name())
		}
	}
}

// TestBumpIndexEpoch_ConcurrentGoroutinesNeverLoseABump is guardrail 4's
// in-process half: N goroutines each bumping once must produce exactly N,
// never fewer — a lost update would mean two goroutines read the same
// current value and both wrote current+1.
func TestBumpIndexEpoch_ConcurrentGoroutinesNeverLoseABump(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()

	const n = 50
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := BumpIndexEpoch(home, root); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent bump failed: %v", err)
	}

	got, err := IndexEpoch(home, root)
	if err != nil {
		t.Fatalf("IndexEpoch after concurrent bumps: %v", err)
	}
	if got != n {
		t.Fatalf("epoch after %d concurrent bumps = %d, want %d (a lost update would read lower)", n, got, n)
	}
}
