// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_compile_llm_fixwave_test.go covers code-review fix-wave finding #7:
// loadDefineGoalSkillContent must WARN (once per call) on a real read
// error — anything other than the seeded file simply not existing yet —
// rather than silently returning "". A silent swallow here is exactly what
// makes finding #1's own failure mode (define-goal/ unreadable after a
// partial SeedDefaults) invisible: every /goal compile would quietly lose
// its quality-bar rewrite with nothing in the logs to explain why.

package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// TestLoadDefineGoalSkillContent_NotExist_StaysSilent is the benign,
// EXPECTED case (a pre-rollout install, or one where SeedDefaults simply
// hasn't run yet): no define-goal/SKILL.md at all produces "" and NO log
// output — the "keep the benign case quiet" half of the fix.
func TestLoadDefineGoalSkillContent_NotExist_StaysSilent(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	readLog := captureLogFile(t, logger.WARN)

	got := loadDefineGoalSkillContent()
	if got != "" {
		t.Fatalf("want empty content when the skill was never seeded, got %q", got)
	}
	if log := readLog(); strings.Contains(log, "define-goal") {
		t.Fatalf("a missing (not-yet-seeded) skill file must log nothing, got:\n%s", log)
	}
}

// TestLoadDefineGoalSkillContent_RealFile_ReturnsContentSilently is the
// happy path: a real seeded SKILL.md returns its (trimmed) content, with no
// WARN — the fix must not make the working case noisy.
func TestLoadDefineGoalSkillContent_RealFile_ReturnsContentSilently(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	skillDir := filepath.Join(home, "skills", "define-goal")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("setup: mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("  the quality bar  \n"), 0o644); err != nil {
		t.Fatalf("setup: write SKILL.md: %v", err)
	}
	readLog := captureLogFile(t, logger.WARN)

	got := loadDefineGoalSkillContent()
	if got != "the quality bar" {
		t.Fatalf("want trimmed file content, got %q", got)
	}
	if log := readLog(); strings.Contains(log, "define-goal") {
		t.Fatalf("a successful read must log nothing, got:\n%s", log)
	}
}

// TestLoadDefineGoalSkillContent_UnreadableFile_WARNsOnce is finding #7's
// core assertion: a genuine read error that is NOT os.IsNotExist (here, the
// SKILL.md path is itself a DIRECTORY — a deterministic, non-root-sensitive
// way to force os.ReadFile to fail with something other than not-exist)
// must produce exactly one WARN log line naming the path, and the function
// must still return "" (degrade gracefully) rather than propagate the
// error — the compile proceeds without the quality bar, but now
// observably.
func TestLoadDefineGoalSkillContent_UnreadableFile_WARNsOnce(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	// Make the SKILL.md PATH a directory instead of a file — os.ReadFile
	// fails with "is a directory", which is NOT os.IsNotExist, and needs no
	// chmod/root-sensitive permission trick to reproduce deterministically.
	skillPath := filepath.Join(home, "skills", "define-goal", "SKILL.md")
	if err := os.MkdirAll(skillPath, 0o755); err != nil {
		t.Fatalf("setup: mkdir (as the SKILL.md path itself): %v", err)
	}
	readLog := captureLogFile(t, logger.WARN)

	got := loadDefineGoalSkillContent()
	if got != "" {
		t.Fatalf("want empty content on a genuine read error (graceful degrade), got %q", got)
	}
	log := readLog()
	if !strings.Contains(log, "define-goal") {
		t.Fatalf("a genuine read error must be WARN-logged (finding #7), got:\n%s", log)
	}
	if !strings.Contains(log, skillPath) {
		t.Fatalf("the WARN must name the unreadable path, got:\n%s", log)
	}
	if got := strings.Count(log, "could not read the seeded define-goal skill"); got != 1 {
		t.Fatalf("want exactly one WARN for this single call, got %d in:\n%s", got, log)
	}
}
