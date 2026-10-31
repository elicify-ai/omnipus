package skills

// RED tests — landing/skills (T9): restore and outside changes (§9.4,
// §7.1 step 1's single detection rule). Scenarios S-5.1, S-5.4, S-5.9,
// E-3 (repair path). Version paging (S-5.10–S-5.12, E-13) lives with the
// get_skill tests (row 35).
// Traces to: skills-spec-r13.md §9.4, §7.1 step 1, §16 US-5, §17.1 rows
// 15/18, E-3.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRestore_PutsEveryFileBack_AndRoundTrips (S-5.1).
// BDD: Given a skill with versions v1..v3, restoring v1 puts every file of v1
// back and records a restore version; restoring v3 (the newest) undoes it.
// Traces to: skills-spec-r13.md §5.4 restore row, S-5.1.
func TestRestore_PutsEveryFileBack_AndRoundTrips(t *testing.T) {
	root := t.TempDir()
	commitWrite(t, root, "app", "SKILL.md", validSkillMD("app", "v1", "v1 body"), "")
	require.NoError(t, os.WriteFile(filepath.Join(root, "app", "helpers.md"), []byte("v1 helper\n"), 0o644))

	out1, v1 := commitWrite(t, root, "app", "SKILL.md",
		validSkillMD("app", "v2", "v2 body"), readLiveRevision(t, filepath.Join(root, "app")))
	require.NoError(t, os.Remove(filepath.Join(root, "app", "helpers.md")), "v2 dropped the helper")

	_, v3 := commitWrite(t, root, "app", "SKILL.md",
		validSkillMD("app", "v3", "v3 body"), out1.Revision)

	// restore v1: every file of v1 back (including the helper), new restore version.
	out, err := ApplyChange(root, ChangeRequest{
		Kind: KindRestore, Slug: "app", Version: v1,
		Expect: Expectation{Revision: readLiveRevision(t, filepath.Join(root, "app"))},
		Actor:  ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "edit_skill"},
	})
	require.NoError(t, err, "RED: restore unimplemented")
	assert.Equal(t, PersistenceComplete, out.PersistenceStatus)
	assert.Equal(t, "v1 body", readFile(t, filepath.Join(root, "app", "SKILL.md")))
	assert.Equal(t, "v1 helper\n", readFile(t, filepath.Join(root, "app", "helpers.md")),
		"every file of v1 is back")

	// restore of v3 undoes it.
	_, err = ApplyChange(root, ChangeRequest{
		Kind: KindRestore, Slug: "app", Version: v3,
		Expect: Expectation{Revision: readLiveRevision(t, filepath.Join(root, "app"))},
		Actor:  ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "edit_skill"},
	})
	require.NoError(t, err)
	assert.Equal(t, "v3 body", readFile(t, filepath.Join(root, "app", "SKILL.md")))
}

// TestRestore_DeletedSkill_ThenRecreateByOtherSession_Refused (S-5.4).
// BDD: Given the skill was deleted and then recreated by another session,
// restoring the deleted version is refused (the key is present —
// SKILL_CHANGED against the recreated state).
// Traces to: skills-spec-r13.md §7.3, §5.4 restore row (deleted: expected
// absence), S-5.4.
func TestRestore_DeletedSkill_ThenRecreateByOtherSession_Refused(t *testing.T) {
	root := t.TempDir()
	out1, v1 := commitWrite(t, root, "foo", "SKILL.md", validSkillMD("foo", "v1", "v1 body"), "")
	_, err := ApplyChange(root, ChangeRequest{
		Kind: KindDelete, Slug: "foo",
		Expect: Expectation{Revision: out1.Revision},
		Actor:  ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "edit_skill"},
	})
	require.NoError(t, err)

	// Another session recreates the name (expected absence holds for it).
	out2, err := ApplyChange(root, ChangeRequest{
		Kind: KindCreate, Slug: "foo",
		Content: []byte(validSkillMD("foo", "recreated by b", "b body")),
		Expect:  Expectation{Absent: true},
		Actor:   ChangeActor{Kind: "agent", AgentID: "agent-b", SessionID: "s2", Tool: "write_file"},
	})
	require.NoError(t, err)

	// The first session's restore of the deleted version is refused: the key
	// is present and the expected absence no longer holds.
	_, err = ApplyChange(root, ChangeRequest{
		Kind: KindRestore, Slug: "foo", Version: v1,
		Expect: Expectation{Absent: true},
		Actor:  ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "edit_skill"},
	})
	require.Error(t, err, "the restore's expected absence no longer holds")
	assert.True(t, errors.Is(err, ErrSkillExists) || errors.Is(err, ErrSkillChanged),
		"refused as SKILL_CHANGED/present, got %v", err)
	_ = out2
}

// TestOutsideChange_DetectionAndRestore (S-5.9, §9.4 + §7.1 step 1 rule).
// BDD: Given state A committed via Omnipus and the test writing C directly,
// the next mutation's step-1 detection records external_change = C; restore
// returns B's exact files.
// Traces to: skills-spec-r13.md §9.4, §7.1 step 1, S-5.9.
func TestOutsideChange_DetectionAndRestore(t *testing.T) {
	root := t.TempDir()
	// A via Omnipus (v1), then B via Omnipus (v2).
	commitWrite(t, root, "app", "SKILL.md", validSkillMD("app", "A", "A body"), "")
	_, v2 := commitWrite(t, root, "app", "SKILL.md",
		validSkillMD("app", "B", "B body"), readLiveRevision(t, filepath.Join(root, "app")))

	// The test writes C directly (outside writer, §6.3).
	appDir := filepath.Join(root, "app")
	require.NoError(t, os.WriteFile(filepath.Join(appDir, "SKILL.md"),
		[]byte(validSkillMD("app", "C", "C body")), 0o644))

	// The next Omnipus change's step-1 records external_change = C: its undo
	// line names the external_change version as predecessor and the new write
	// commits on top.
	outC, _ := commitWrite(t, root, "app", "SKILL.md",
		validSkillMD("app", "D", "D body"), readLiveRevision(t, appDir))
	assert.Contains(t, outC.Message, "the version before it is ",
		"step 1 committed the previous state as a version (external_change = C)")

	// restore B returns B's exact files.
	_, err := ApplyChange(root, ChangeRequest{
		Kind: KindRestore, Slug: "app", Version: v2,
		Expect: Expectation{Revision: outC.Revision},
		Actor:  ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "edit_skill"},
	})
	require.NoError(t, err)
	assert.Equal(t, "B body", readFile(t, filepath.Join(appDir, "SKILL.md")))
}

// TestE3_BrokenFolder_PresentAndRepairable (E-3). BDD: Given SKILL.md removed
// outside Omnipus with helpers left, the folder is present; a write of a
// valid SKILL.md (existing-folder repair, expected revision) or a restore
// repairs it.
// Traces to: skills-spec-r13.md §7.3 present-and-repairable, E-3.
func TestE3_BrokenFolder_PresentAndRepairable(t *testing.T) {
	root := t.TempDir()
	commitWrite(t, root, "foo", "SKILL.md", validSkillMD("foo", "v1", "v1 body"), "")
	appDir := filepath.Join(root, "foo")
	require.NoError(t, os.WriteFile(filepath.Join(appDir, "helpers.md"), []byte("h\n"), 0o644))
	require.NoError(t, os.Remove(filepath.Join(appDir, "SKILL.md")))

	// Repair by write_file of a valid SKILL.md (existing-folder repair).
	out, err := ApplyChange(root, ChangeRequest{
		Kind: KindEdit, Slug: "foo", Path: "SKILL.md",
		Content: []byte(validSkillMD("foo", "repaired", "repaired body")),
		Expect:  Expectation{Revision: readLiveRevision(t, appDir)},
		Actor:   ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "write_file"},
	})
	require.NoError(t, err, "the present-but-repairable folder accepts the repair write (never SKILL_EXISTS)")
	assert.Equal(t, PersistenceComplete, out.PersistenceStatus)
	assert.Equal(t, "repaired body", readFile(t, filepath.Join(appDir, "SKILL.md")))
}

// readFile is a small helper returning file content as string.
func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(raw)
}
