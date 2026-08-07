// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// Build tags: goolm,stdjson (CGO_ENABLED=0).
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestGitEvidence' -p 1 ./pkg/workspace/

package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGitEvidence_EnsureWorkDir_CreatesDirAndAutoInitsGitRepo(t *testing.T) {
	home := t.TempDir()

	dir, err := EnsureWorkDir(home, "ws1")
	if err != nil {
		t.Fatalf("EnsureWorkDir: %v", err)
	}
	if dir != WorkDir(home, "ws1") {
		t.Fatalf("EnsureWorkDir dir = %q, want %q", dir, WorkDir(home, "ws1"))
	}
	if _, statErr := os.Stat(dir); statErr != nil {
		t.Fatalf("work dir not created: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".git")); statErr != nil {
		t.Fatalf("git evidence repo not auto-initialized: %v", statErr)
	}
}

func TestGitEvidence_EnsureWorkDir_IsIdempotent(t *testing.T) {
	home := t.TempDir()

	if _, err := EnsureWorkDir(home, "ws1"); err != nil {
		t.Fatalf("first EnsureWorkDir: %v", err)
	}
	dir, err := EnsureWorkDir(home, "ws1")
	if err != nil {
		t.Fatalf("second EnsureWorkDir (idempotent reopen): %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".git")); statErr != nil {
		t.Fatalf("git evidence repo missing on reopen: %v", statErr)
	}
}

func TestGitEvidence_EnsureWorkDir_RejectsUnsafeID(t *testing.T) {
	home := t.TempDir()
	if _, err := EnsureWorkDir(home, "../escape"); err == nil {
		t.Fatalf("EnsureWorkDir with a traversal id = nil error, want ErrInvalidWorkspaceID")
	}
}

func TestGitEvidence_EnsureWorkDir_DegradesGracefullyOnNestedUserRepo(t *testing.T) {
	home := t.TempDir()
	// Simulate the operator's own git repo enclosing OMNIPUS_HOME.
	if err := os.MkdirAll(filepath.Join(home, ".git"), 0o700); err != nil {
		t.Fatalf("simulate ancestor .git: %v", err)
	}

	dir, err := EnsureWorkDir(home, "ws1")
	if err != nil {
		t.Fatalf("EnsureWorkDir must degrade gracefully (nil error) on a nested repo, got: %v", err)
	}
	if _, statErr := os.Stat(dir); statErr != nil {
		t.Fatalf("work dir must still be created even when the git layer degrades: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".git")); !os.IsNotExist(statErr) {
		t.Fatalf("EnsureWorkDir must NOT initialize a .git under a nested-repo work dir")
	}
}
