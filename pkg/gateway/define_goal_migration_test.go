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
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/coreagent"
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
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be absent, stat error: %v", dir, err)
	}
}

// TestDeleteOrphanedDefineDoneDir_MarkerAndReplacementPresent_DeletesOrphan
// is case (a): marker present + define-goal/ present + define-done/ present
// => define-done/ deleted.
func TestDeleteOrphanedDefineDoneDir_MarkerAndReplacementPresent_DeletesOrphan(t *testing.T) {
	skillsGlobalDir := t.TempDir()
	mustMkdirSkill(t, skillsGlobalDir, "define-goal")
	doneDir := mustMkdirSkill(t, skillsGlobalDir, "define-done")
	markers := []string{coreagent.SkillsMigrationDefineGoalRename}

	deleted, err := deleteOrphanedDefineDoneDir(skillsGlobalDir, markers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !deleted {
		t.Fatal("expected deleted=true when define-goal/ is present and define-done/ exists")
	}
	requireDirAbsent(t, doneDir)
}

// TestDeleteOrphanedDefineDoneDir_ReplacementAbsent_PreservesOrphan is case
// (b): marker present + define-goal/ ABSENT => define-done/ PRESERVED, no
// delete. This is the fail-open scenario the fix closes: a partial/failed
// SeedDefaults must never cost the operator their quality bar.
func TestDeleteOrphanedDefineDoneDir_ReplacementAbsent_PreservesOrphan(t *testing.T) {
	skillsGlobalDir := t.TempDir()
	// Deliberately do NOT create define-goal/ — simulates SeedDefaults
	// failing after the migration marker was already recorded.
	doneDir := mustMkdirSkill(t, skillsGlobalDir, "define-done")
	markers := []string{coreagent.SkillsMigrationDefineGoalRename}

	deleted, err := deleteOrphanedDefineDoneDir(skillsGlobalDir, markers)
	if err == nil {
		t.Fatal("expected a non-nil error when the replacement define-goal/ directory is absent")
	}
	if deleted {
		t.Fatal("expected deleted=false when the replacement define-goal/ directory is absent")
	}
	requireDirExists(t, doneDir)
}

// TestDeleteOrphanedDefineDoneDir_SecondCall_CleanNoOp is case (c): a second
// call after the orphan has already been deleted is a clean no-op — no
// error, deleted=false — idempotent by the directories' own on-disk state.
func TestDeleteOrphanedDefineDoneDir_SecondCall_CleanNoOp(t *testing.T) {
	skillsGlobalDir := t.TempDir()
	mustMkdirSkill(t, skillsGlobalDir, "define-goal")
	mustMkdirSkill(t, skillsGlobalDir, "define-done")
	markers := []string{coreagent.SkillsMigrationDefineGoalRename}

	first, err := deleteOrphanedDefineDoneDir(skillsGlobalDir, markers)
	if err != nil || !first {
		t.Fatalf("setup: first call must delete cleanly, got deleted=%v err=%v", first, err)
	}

	second, err := deleteOrphanedDefineDoneDir(skillsGlobalDir, markers)
	if err != nil {
		t.Fatalf("second call must be a clean no-op, got error: %v", err)
	}
	if second {
		t.Fatal("second call must report deleted=false — define-done/ was already gone")
	}
}

// TestDeleteOrphanedDefineDoneDir_MarkerAbsent_NeverTouchesEitherDir covers
// a pre-ADR-080 install that has not run the rename yet: the marker is
// absent, so neither directory is touched regardless of what's on disk.
func TestDeleteOrphanedDefineDoneDir_MarkerAbsent_NeverTouchesEitherDir(t *testing.T) {
	skillsGlobalDir := t.TempDir()
	doneDir := mustMkdirSkill(t, skillsGlobalDir, "define-done")

	deleted, err := deleteOrphanedDefineDoneDir(skillsGlobalDir, nil)
	if err != nil {
		t.Fatalf("unexpected error with no marker present: %v", err)
	}
	if deleted {
		t.Fatal("expected deleted=false with no migration marker present")
	}
	requireDirExists(t, doneDir)
}
