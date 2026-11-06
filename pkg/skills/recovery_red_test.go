package skills

// RED tests — landing/skills (T9): interrupted changes (§9.3 recovery).
// Scenarios S-11.1, S-11.2, S-11.3, S-11.5, S-11.10, S-11.12 (T-V12 legacy
// backups), plus the §9.3 r10 staging-scratch rules.
// Traces to: skills-spec-r15.md §9.3, §16 US-11, §17.1 rows 19/34.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeSwap creates skills/.swap/<slug> with content.
func makeSwap(t *testing.T, root, slug, body string) string {
	t.Helper()
	swapDir := filepath.Join(root, ".swap", slug)
	require.NoError(t, os.MkdirAll(swapDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(swapDir, "SKILL.md"),
		[]byte(validSkillMD(slug, "swap leftover", body)), 0o644))
	return swapDir
}

// TestRecovery_SwapLiveAbsent_NonDeleteHead_PutBack (S-11.1).
// BDD: Given .swap/<slug> with live absent and the newest manifest not
// delete_skill, a restart renames it back to live; a second restart changes
// nothing.
// Traces to: skills-spec-r15.md §9.3 .swap table row 1, S-11.1.
func TestRecovery_SwapLiveAbsent_NonDeleteHead_PutBack(t *testing.T) {
	root := t.TempDir()
	historyRoot := t.TempDir()
	swapDir := makeSwap(t, root, "app", "leftover body")
	seedHistory(t, historyRoot, "app", singleManifest(fixtureVersionID(1), "edit")) // newest not delete_skill

	require.NoError(t, RecoverInterrupted(root, historyRoot))

	assert.DirExists(t, filepath.Join(root, "app"), "renamed back to live")
	assert.Equal(t, validSkillMD("app", "swap leftover", "leftover body"), readFile(t, filepath.Join(root, "app", "SKILL.md")))
	_, err := os.Stat(swapDir)
	assert.True(t, errors.Is(err, os.ErrNotExist), "the .swap leftover is consumed by the put-back")

	// Second restart: nothing left to do, nothing breaks.
	require.NoError(t, RecoverInterrupted(root, historyRoot))
	assert.DirExists(t, filepath.Join(root, "app"))
}

// TestRecovery_SwapLivePresent_Removed (S-11.3 / §9.3 row 2).
// BDD: Given .swap/<slug> while live is present (delete completed, .swap
// cleanup crashed), a restart removes it — twice (stays deleted).
// Traces to: skills-spec-r15.md §9.3 .swap table row 2, S-11.3.
func TestRecovery_SwapLivePresent_Removed(t *testing.T) {
	root := t.TempDir()
	historyRoot := t.TempDir()
	writeSkillFixture(t, root, "app") // live present
	makeSwap(t, root, "app", "stale")
	seedHistory(t, historyRoot, "app", singleManifest(fixtureVersionID(2), "delete_skill"))

	require.NoError(t, RecoverInterrupted(root, historyRoot))
	_, err := os.Stat(filepath.Join(root, ".swap", "app"))
	assert.True(t, errors.Is(err, os.ErrNotExist), "the stale .swap is removed")

	require.NoError(t, RecoverInterrupted(root, historyRoot))
	assert.DirExists(t, filepath.Join(root, "app"), "stays deleted: live untouched")
}

// TestRecovery_SwapOfBuiltin_PutBackThenSyncReplaces (S-11.5).
// BDD: Given .swap/plan of a built-in after a crash during sync, a restart
// renames it back and SyncBuiltins then replaces it — one clean plan.
// Traces to: skills-spec-r15.md §9.3, §8.2, S-11.5.
func TestRecovery_SwapOfBuiltin_PutBackThenSyncReplaces(t *testing.T) {
	root := t.TempDir()
	historyRoot := t.TempDir()
	makeSwap(t, root, "plan", "half-synced")

	require.NoError(t, RecoverInterrupted(root, historyRoot))
	assert.DirExists(t, filepath.Join(root, "plan"), "put back to live")

	report, err := SyncBuiltins(root, nil)
	require.NoError(t, err, "RED: SyncBuiltins not implemented")
	_ = report

	shipped := filepath.Join(t.TempDir(), "shipped")
	exportShipped(t, "plan", shipped)
	assert.Equal(t,
		readFile(t, filepath.Join(shipped, "SKILL.md")),
		readFile(t, filepath.Join(root, "plan", "SKILL.md")),
		"one clean shipped plan after recovery + sync")
}

// TestRecovery_LegacyBackup_LivePresent_Deleted (S-11.10, T-V12).
// BDD: Given live foo and a legacy foo.replace-backup (pre-T9 publisher
// leftover, F17), a start deletes the backup with one INFO line; foo and its
// history are unchanged.
// Traces to: skills-spec-r15.md §9.3 legacy table row 2 (r2.1 F-28), S-11.10.
func TestRecovery_LegacyBackup_LivePresent_Deleted(t *testing.T) {
	root := t.TempDir()
	historyRoot := t.TempDir()
	writeSkillFixture(t, root, "foo")
	backup := filepath.Join(root, "foo.replace-backup")
	require.NoError(t, os.MkdirAll(backup, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(backup, "SKILL.md"),
		[]byte(validSkillMD("foo", "old publisher copy", "old")), 0o644))
	seedHistory(t, historyRoot, "foo", singleManifest(fixtureVersionID(1), "edit"))

	require.NoError(t, RecoverInterrupted(root, historyRoot))

	_, err := os.Stat(backup)
	assert.True(t, errors.Is(err, os.ErrNotExist), "the legacy backup is deleted")
	assert.FileExists(t, filepath.Join(root, "foo", "SKILL.md"), "live foo unchanged")
	assert.FileExists(t, filepath.Join(historyRoot, "foo", "versions", fixtureVersionID(1)+".json"),
		"history unchanged — no capture into history (F-28)")
}

// TestRecovery_LegacyBackup_LiveAbsent_PutBack (S-11.12).
// BDD: Given live absent and a legacy foo.replace-backup, a start renames it
// back to foo (the pre-T9 publisher's intent).
// Traces to: skills-spec-r15.md §9.3 legacy table row 1, S-11.12.
func TestRecovery_LegacyBackup_LiveAbsent_PutBack(t *testing.T) {
	root := t.TempDir()
	historyRoot := t.TempDir()
	backup := filepath.Join(root, "foo.replace-backup")
	require.NoError(t, os.MkdirAll(backup, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(backup, "SKILL.md"),
		[]byte(validSkillMD("foo", "rescued", "rescued body")), 0o644))

	require.NoError(t, RecoverInterrupted(root, historyRoot))

	assert.DirExists(t, filepath.Join(root, "foo"), "renamed back to live")
	assert.Equal(t, validSkillMD("foo", "rescued", "rescued body"), readFile(t, filepath.Join(root, "foo", "SKILL.md")))
}

// TestRecovery_CaptureTemporaries_RemovedAtStart (§9.3, r9/r10 rules).
// BDD: Given crash leftovers under system/skill-history/<id>/blobs/.incoming-*
// and skills/.staging/.incoming-*, a start removes both; no committed
// manifest can reference a temporary.
// Traces to: skills-spec-r15.md §9.3 (capture-blob temporaries, r10
// staging-scratch rule), §9.5 rule 8.
func TestRecovery_CaptureTemporaries_RemovedAtStart(t *testing.T) {
	root := t.TempDir()
	historyRoot := t.TempDir()

	blobTemp := filepath.Join(historyRoot, "app", "blobs", ".incoming-attempt9")
	require.NoError(t, os.MkdirAll(blobTemp, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(blobTemp, "half"), []byte("x"), 0o644))

	stagingTemp := filepath.Join(root, ".staging", ".incoming-attempt10")
	require.NoError(t, os.MkdirAll(stagingTemp, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(stagingTemp, "builtin-blob"), []byte("y"), 0o644))

	seedHistory(t, historyRoot, "app", singleManifest(fixtureVersionID(1), "edit"))

	require.NoError(t, RecoverInterrupted(root, historyRoot))

	_, err := os.Stat(blobTemp)
	assert.True(t, errors.Is(err, os.ErrNotExist), "capture-blob temporary removed at start")
	_, err = os.Stat(stagingTemp)
	assert.True(t, errors.Is(err, os.ErrNotExist), "staging-area scratch removed at start")
	assert.FileExists(t, filepath.Join(historyRoot, "app", "blobs", mkFile("SKILL.md", 96).SHA256),
		"the committed manifest's referenced final is untouched")
}

// singleManifest builds a one-entry chain for seedHistory.
func singleManifest(id, kind string) []historyEntry {
	var files []historyFile
	if kind != "delete_skill" {
		files = []historyFile{mkFile("SKILL.md", 96)}
	}
	return []historyEntry{{ID: id, CreatedAt: "20260101T000000Z", Kind: kind, Actor: "agent", Files: files}}
}
