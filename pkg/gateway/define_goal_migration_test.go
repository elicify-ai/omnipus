// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// define_goal_migration_test.go covers deleteOrphanedDefineDoneDir
// (gateway.go), the fail-open orphan-dir-delete fix (code-review fix-wave
// finding #1, 2026-09-07): the old define-done/ skill directory is deleted
// ONLY when the replacement define-goal/ directory is verifiably present on
// disk — never on the ADR-080 D-SKILL migration marker's say-so alone. A
// deleted define-done/ with an absent define-goal/ (e.g. skills.SeedDefaults
// failed mid-boot) would leave loadDefineGoalSkillContent
// (goal_compile_llm.go) silently returning "" for every subsequent /goal
// compile — the quality bar vanishes with no observable signal.

package gateway

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func mustMkdirSkill(t *testing.T, skillsGlobalDir, name string) string {
	t.Helper()
	dir := filepath.Join(skillsGlobalDir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("setup: mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# "+name), 0o644); err != nil {
		t.Fatalf("setup: write SKILL.md in %s: %v", dir, err)
	}
	return dir
}

func requireDirExists(t *testing.T, dir string) {
	t.Helper()
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("expected %s to exist, stat error: %v", dir, err)
	}
}

func requireDirAbsent(t *testing.T, dir string) {
	t.Helper()
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected %s to be absent, stat error: %v", dir, err)
	}
}
