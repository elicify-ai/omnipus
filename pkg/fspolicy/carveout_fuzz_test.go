//go:build goolm && stdjson

package fspolicy

import (
	"path/filepath"
	"sync"
	"testing"
)

// FuzzIsCarveOut asserts the invariants IsCarveOut must uphold no matter how
// malformed its input is: it must never panic, its verdict must be
// idempotent (calling it twice with the same input yields the same answer),
// and — item #4 of the ADR-046 P1 review, added alongside the BLOCK #2 fix —
// it must never regress to a constant answer. A prior version of this fuzz
// target would have passed even if IsCarveOut always returned false (the
// exact BLOCK #2 bug shape: the own-tree exception firing unconditionally),
// since neither "no panic" nor "idempotent" catches a constant function.
// Seeds cover the fixed carve-out roots themselves plus the edge inputs
// called out in the spec dataset (NUL byte, unicode, ".." traversal, and a
// Windows/UNC-style path).
func FuzzIsCarveOut(f *testing.F) {
	home := filepath.FromSlash("/omnh")
	workDir := filepath.Join(home, "workspaces", "W", "work")
	policy := FSPolicy{
		WorkDir:   workDir,
		Scope:     FSScopeConfined,
		CarveOuts: buildCarveOuts(home),
	}

	// canaryPolicy's WorkDir is deliberately unrelated to any carve-out root
	// (no ancestor/descendant relationship to $OMNIPUS_HOME at all), so the
	// own-tree exception can never legitimately apply to it — every known
	// carve-out root (and a path under it) MUST classify true. Checked on
	// every corpus entry below (including a plain `go test` run with no
	// `-fuzz` flag, which still executes the full seed corpus), this catches
	// IsCarveOut regressing towards constant-false immediately, on the very
	// first iteration, rather than depending on the fuzzer to stumble onto
	// the right input.
	canaryPolicy := FSPolicy{
		WorkDir:   filepath.FromSlash("/unrelated/elsewhere"),
		Scope:     FSScopeUnrestricted,
		CarveOuts: buildCarveOuts(home),
	}
	knownCarveOutPaths := []string{
		filepath.Join(home, "master.key"),
		filepath.Join(home, "credentials.json"),
		filepath.Join(home, "agents"),
		filepath.Join(home, "agents", "x"),
		filepath.Join(home, "workspaces"),
		filepath.Join(home, "workspaces", "x"),
	}

	seeds := []string{
		filepath.Join(home, "master.key"),
		filepath.Join(home, "credentials.json"),
		filepath.Join(home, "agents"),
		filepath.Join(home, "agents", "other", "SOUL.md"),
		filepath.Join(home, "agents", "self", "SOUL.md"),
		filepath.Join(home, "workspaces"),
		filepath.Join(home, "workspaces", "other", "work", "x"),
		workDir,
		filepath.Join(workDir, "a.txt"),
		"",
		"/",
		"relative/path",
		"../../etc/passwd",
		"/omnh/agents/../credentials.json",
		"/omnh/agents/self/SOUL.md\x00",
		"工作/データ/файл.txt", //nolint:gosmopolitan // intentional non-ASCII fuzz seed
		`\\server\share\x`,
		string([]byte{0x00, 0x01, 0xff}),
	}
	for _, s := range seeds {
		f.Add(s)
	}

	// sawTrue/sawFalse track whether IsCarveOut(path, policy) — the SEED
	// policy, not the canary — has produced BOTH outcomes across the whole
	// corpus. The seed corpus deliberately mixes carve-out hits (e.g.
	// home/master.key) and misses (e.g. "relative/path"), so a constant
	// function of either polarity fails this check once the corpus finishes.
	var mu sync.Mutex
	var sawTrue, sawFalse bool
	f.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		if !sawTrue {
			f.Errorf(
				"FuzzIsCarveOut corpus never produced a true verdict — IsCarveOut may have regressed to constant-false",
			)
		}
		if !sawFalse {
			f.Errorf(
				"FuzzIsCarveOut corpus never produced a false verdict — IsCarveOut may have regressed to constant-true",
			)
		}
	})

	f.Fuzz(func(t *testing.T, path string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("IsCarveOut panicked on input %q: %v", path, r)
			}
		}()

		for _, known := range knownCarveOutPaths {
			if !IsCarveOut(known, canaryPolicy) {
				t.Fatalf(
					"IsCarveOut(%q) = false under a policy whose WorkDir (%q) is unrelated to any carve-out root — IsCarveOut has regressed to (or towards) constant-false",
					known,
					canaryPolicy.WorkDir,
				)
			}
		}

		first := IsCarveOut(path, policy)
		second := IsCarveOut(path, policy)
		if first != second {
			t.Fatalf("IsCarveOut(%q) not idempotent: first=%v second=%v", path, first, second)
		}

		mu.Lock()
		if first {
			sawTrue = true
		} else {
			sawFalse = true
		}
		mu.Unlock()
	})
}
