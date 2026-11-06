package skills

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDefaultSkills_EmbeddedAndSeeded preserves the exact WU-9/FR-010 shipped
// inventory, first-boot materialization and loader coverage. r15 §8.2 replaces
// the retired seeder with the real startup synchronizer.
func TestDefaultSkills_EmbeddedAndSeeded(t *testing.T) {
	// The embedded default set after WU-9 (omnipus-system-agent FR-010): the
	// Ava/Admin-era skills are removed and the W1–W18 workflow skills ship in
	// their place, alongside shared helpers and the merged elicify-office
	// package (office-preview AD-14/FR-023).
	want := []string{
		"add-connector", "add-provider", "capability-plan", "change-agent",
		"connect-channel", "create-agent", "deep-research", "define-goal",
		"dependency-check", "disable-channel", "doctor-usage", "elicify-office",
		"inbox-triage",
		"install-skill", "interview", "orchestrate", "plan", "provider-config",
		"remove-agent", "remove-connector", "shell-diagnosis", "team-delegation",
		"team-setup", "verify", "welcome-workspace", "write-skill",
	}
	got := DefaultSkillNames()
	sort.Strings(got)
	require.Equal(t, want, got, "the embedded inventory is the independently specified WU-9 set")

	dest := t.TempDir()
	res, err := SyncBuiltins(dest, nil)
	require.NoError(t, err)
	sort.Strings(res.Installed)
	assert.Equal(t, want, res.Installed, "fresh startup installs every shipped package exactly once")
	assert.Empty(t, res.Updated, "an empty root has no previous built-in to update")
	for _, name := range want {
		data, readErr := os.ReadFile(filepath.Join(dest, name, "SKILL.md"))
		require.NoError(t, readErr, "installed %s has instructions", name)
		assert.NoError(t, ValidateSkillMarkdown(name, string(data)), "installed %s validates", name)
		requireSameTree(t, name, filepath.Join(dest, name))
	}

	loader := NewSkillsLoader("", dest, "")
	listed := loader.ListSkills()
	ids := make([]string, 0, len(listed))
	for _, skill := range listed {
		ids = append(ids, skill.ID)
	}
	sort.Strings(ids)
	assert.Equal(t, want, ids, "the loader sees every installed package, not merely the right count")
}

// r15 §8.2 supersedes the old never-overwrite builtin oracle. Startup must
// replace an outside edit, leave a differently named user skill untouched,
// and then leave equal shipped content and identity completely untouched.
func TestSyncBuiltins_ReplacesEditedBuiltinThenIsIdempotent(t *testing.T) {
	dest := t.TempDir()
	planDir := writeSkillFixture(t, dest, "plan") // an unstamped outside edit
	before := readFile(t, filepath.Join(planDir, "SKILL.md"))
	userDir := writeSkillFixture(t, dest, "my-plan")
	userContent := readFile(t, filepath.Join(userDir, "SKILL.md"))

	res, err := SyncBuiltins(dest, nil)
	require.NoError(t, err)
	updated := findOutcome(res.Updated, "plan")
	require.NotNil(t, updated, "a differing built-in is updated, never skipped")
	assert.Equal(t, BuiltinUpdated, updated.Case)
	assert.NotContains(t, res.Installed, "plan", "replacing an existing folder is not a fresh installation")
	requireSameTree(t, "plan", planDir)
	shipped := readFile(t, filepath.Join(planDir, "SKILL.md"))
	assert.NotEqual(t, before, shipped, "the outside edit was actually replaced")
	assert.Equal(t, userContent, readFile(t, filepath.Join(userDir, "SKILL.md")), "unrelated user skill survives")
	assert.NoDirExists(t, filepath.Join(SkillHistoryRoot(dest), "plan"), "built-ins never acquire version history")
	generation := generationOf(t, planDir)

	oldTime := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(filepath.Join(planDir, "SKILL.md"), oldTime, oldTime))
	ageDir(t, planDir, oldTime)
	beforeDirTime := dirMtime(t, planDir)
	beforeFileTime := fileMtime(t, filepath.Join(planDir, "SKILL.md"))
	res2, err := SyncBuiltins(dest, nil)
	require.NoError(t, err)
	assert.Empty(t, res2.Installed, "equal second startup installs nothing")
	assert.Empty(t, res2.Updated, "equal second startup updates nothing")
	assert.Equal(t, shipped, readFile(t, filepath.Join(planDir, "SKILL.md")))
	assert.Equal(t, generation, generationOf(t, planDir), "equal startup preserves the generation")
	assert.True(t, beforeDirTime.Equal(dirMtime(t, planDir)), "equal folder mtime is unchanged")
	assert.True(t, beforeFileTime.Equal(fileMtime(t, filepath.Join(planDir, "SKILL.md"))), "equal instruction mtime is unchanged")
	assert.Equal(t, userContent, readFile(t, filepath.Join(userDir, "SKILL.md")))
}
