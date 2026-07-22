// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package gitevidence

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
)

// newTestRepo opens a fresh evidence repo rooted at a fresh temp dir. The
// clock is fixed and monotonically advanced by the caller as needed so
// commit timestamps are deterministic in assertions.
func newTestRepo(t *testing.T, opts ...Option) (*Repo, string) {
	t.Helper()
	dir := t.TempDir()
	r, err := Open(dir, opts...)
	if err != nil {
		t.Fatalf("Open(%s) = %v, want nil error", dir, err)
	}
	return r, dir
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	abs := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func deleteFile(t *testing.T, root, rel string) {
	t.Helper()
	if err := os.Remove(filepath.Join(root, rel)); err != nil {
		t.Fatalf("delete %s: %v", rel, err)
	}
}

// fixedClock returns a monotonically-advancing test clock so successive
// commits never collide on identical timestamps.
func fixedClock() func() time.Time {
	t := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	return func() time.Time {
		t = t.Add(time.Minute)
		return t
	}
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// hashOf parses a commit-hash string produced by CommitResult.Hash back
// into a plumbing.Hash for direct repo.CommitObject lookups in assertions.
func hashOf(t *testing.T, s string) plumbing.Hash {
	t.Helper()
	if s == "" {
		t.Fatalf("hashOf: empty hash string")
	}
	return plumbing.NewHash(s)
}
