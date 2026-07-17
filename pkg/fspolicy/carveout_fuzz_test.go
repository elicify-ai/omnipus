//go:build goolm && stdjson

package fspolicy

import (
	"path/filepath"
	"testing"
)

// FuzzIsCarveOut asserts the two invariants IsCarveOut must uphold no matter
// how malformed its input is: it must never panic, and its verdict must be
// idempotent (calling it twice with the same input yields the same answer).
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
		"工作/データ/файл.txt",
		`\\server\share\x`,
		string([]byte{0x00, 0x01, 0xff}),
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, path string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("IsCarveOut panicked on input %q: %v", path, r)
			}
		}()

		first := IsCarveOut(path, policy)
		second := IsCarveOut(path, policy)
		if first != second {
			t.Fatalf("IsCarveOut(%q) not idempotent: first=%v second=%v", path, first, second)
		}
	})
}
