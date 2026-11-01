package skills

// RED tests — landing/skills (T9): restore and outside changes (§9.4,
// §7.1 step 1's single detection rule). Scenarios S-5.1, S-5.4, S-5.9,
// E-3 (repair path). Version paging (S-5.10–S-5.12, E-13) lives with the
// get_skill tests (row 35).
// Traces to: skills-spec-r15.md §9.4, §7.1 step 1, §16 US-5, §17.1 rows
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
// BDD: Given a skill with versions v1..v3 — v1 = {SKILL.md(v1), helpers.md},
// v3 dropped the helper through the pipeline — restoring v1 puts every file
// of v1 back byte-exact (SKILL.md AND the helper) and records a restore
// version; restoring v3 (the newest) undoes it and the helper is gone again.
// Traces to: skills-spec-r15.md §5.4 restore row, S-5.1.
func TestRestore_PutsEveryFileBack_AndRoundTrips(t *testing.T) {
	root := t.TempDir()
	appDir := filepath.Join(root, "app")

	outCreate, _ := createSkillViaPipeline(t, root, "app", "v1", "v1 body")
	_, v1 := commitWrite(t, root, "app", "helpers.md", "v1 helper\n", outCreate.Revision)
	out2, _ := commitWrite(t, root, "app", "SKILL.md",
		validSkillMD("app", "v3", "v3 body"), readLiveRevision(t, appDir))
	out3, err := ApplyChange(root, ChangeRequest{
		Kind: KindDeleteFile, Slug: "app", Path: "helpers.md",
		Expect: Expectation{Revision: out2.Revision},
		Actor:  ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "edit_skill"},
	})
	require.NoError(t, err)
	v3Match := versionIDFromMessage.FindStringSubmatch(out3.Message)
	require.Len(t, v3Match, 2)
	v3 := v3Match[1]
	_, statErr := os.Stat(filepath.Join(appDir, "helpers.md"))
	require.True(t, os.IsNotExist(statErr), "fixture: v3 has no helper")

	// restore v1: every file of v1 back byte-exact (SKILL.md AND the
	// helper), a new restore version recorded.
	out, err := ApplyChange(root, ChangeRequest{
		Kind: KindRestore, Slug: "app", Version: v1,
		Expect: Expectation{Revision: readLiveRevision(t, appDir)},
		Actor:  ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "edit_skill"},
	})
	require.NoError(t, err, "RED: restore unimplemented")
	assert.Equal(t, PersistenceComplete, out.PersistenceStatus)
	assert.Equal(t, validSkillMD("app", "v1", "v1 body"), readFile(t, filepath.Join(appDir, "SKILL.md")),
		"SKILL.md is v1's exact full bytes (frontmatter included), not just the body")
	assert.Equal(t, "v1 helper\n", readFile(t, filepath.Join(appDir, "helpers.md")),
		"every file of v1 is back, including the one v3 dropped")

	// restore of v3 undoes it — and the helper is gone again.
	_, err = ApplyChange(root, ChangeRequest{
		Kind: KindRestore, Slug: "app", Version: v3,
		Expect: Expectation{Revision: readLiveRevision(t, appDir)},
		Actor:  ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "edit_skill"},
	})
	require.NoError(t, err)
	assert.Equal(t, validSkillMD("app", "v3", "v3 body"), readFile(t, filepath.Join(appDir, "SKILL.md")))
	_, statErr = os.Stat(filepath.Join(appDir, "helpers.md"))
	assert.True(t, os.IsNotExist(statErr), "restore v3 restores v3's exact state: the helper stays gone")
}

// TestRestore_DeletedSkill_ThenRecreateByOtherSession_Refused (S-5.4).
// BDD: Given the skill was deleted and then recreated by another session,
// restoring with expected absence is refused as SKILL_EXISTS (the key is
// present). A stale expected revision would instead mean SKILL_CHANGED;
// these two §7.3 branches are not interchangeable.
// Traces to: skills-spec-r15.md §7.3, §5.4 restore row (deleted: expected
// absence), S-5.4.
func TestRestore_DeletedSkill_ThenRecreateByOtherSession_Refused(t *testing.T) {
	root := t.TempDir()
	out1, v1 := createSkillViaPipeline(t, root, "foo", "v1", "v1 body")
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
	assert.Equal(t, PersistenceComplete, out2.PersistenceStatus, "the recreate commits")

	before := readFile(t, filepath.Join(root, "foo", "SKILL.md"))
	beforeHistory := manifestIDs(t, SkillHistoryRoot(root), "foo")
	g2 := generationOf(t, filepath.Join(root, "foo"))
	// The first session's restore of the deleted version is refused: the key
	// is present and the expected absence no longer holds.
	_, err = ApplyChange(root, ChangeRequest{
		Kind: KindRestore, Slug: "foo", Version: v1,
		Expect: Expectation{Absent: true},
		Actor:  ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "edit_skill"},
	})
	require.ErrorIs(t, err, ErrSkillExists, "§7.3: expected absence plus a present key is only SKILL_EXISTS")
	assert.False(t, errors.Is(err, ErrSkillChanged), "the distinct stale-revision code is not accepted")
	assert.Contains(t, err.Error(), "skill 'foo' already exists; read it first", "exact §5.10 remedy")
	assert.Equal(t, before, readFile(t, filepath.Join(root, "foo", "SKILL.md")), "refusal cannot overwrite the other session's recreation")
	assert.Equal(t, beforeHistory, manifestIDs(t, SkillHistoryRoot(root), "foo"), "refusal adds no restore version")
	assert.Equal(t, g2, generationOf(t, filepath.Join(root, "foo")), "refusal does not replace the current generation")
}

// TestOutsideChange_DetectionAndRestore (S-5.9, §9.4 + §7.1 step 1 rule).
// BDD: Given state A committed via Omnipus and the test writing C directly,
// the next mutation's step-1 detection records external_change = C; restore
// returns B's exact files (full bytes).
// Traces to: skills-spec-r15.md §9.4, §7.1 step 1, S-5.9.
func TestOutsideChange_DetectionAndRestore(t *testing.T) {
	root := t.TempDir()
	// A via Omnipus (v1), then B via Omnipus (v2).
	createSkillViaPipeline(t, root, "app", "A", "A body")
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

	// restore B returns B's exact full bytes.
	_, err := ApplyChange(root, ChangeRequest{
		Kind: KindRestore, Slug: "app", Version: v2,
		Expect: Expectation{Revision: outC.Revision},
		Actor:  ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "edit_skill"},
	})
	require.NoError(t, err)
	assert.Equal(t, validSkillMD("app", "B", "B body"), readFile(t, filepath.Join(appDir, "SKILL.md")),
		"B's exact full bytes (§5.4 restore: every file back unchanged)")
}

// TestE3_BrokenFolder_PresentAndRepairable (E-3). BDD: Given SKILL.md removed
// outside Omnipus with helpers left, the folder is PRESENT (its content
// revision comes from §7.3's ContentRevision, which tolerates a missing
// SKILL.md — not revisionForDir); a write of a valid SKILL.md (existing-folder
// repair, expected revision) repairs it; a restore also repairs it.
// Traces to: skills-spec-r15.md §7.3 present-and-repairable, E-3,
// §7.3 expectation contract.
func TestE3_BrokenFolder_PresentAndRepairable(t *testing.T) {
	root := t.TempDir()
	_, v1 := createSkillViaPipeline(t, root, "foo", "v1", "v1 body")
	appDir := filepath.Join(root, "foo")
	require.NoError(t, os.WriteFile(filepath.Join(appDir, "helpers.md"), []byte("h\n"), 0o644))
	require.NoError(t, os.Remove(filepath.Join(appDir, "SKILL.md")))

	// The folder is present: ContentRevision hashes it without SKILL.md
	// (ErrNotFound only when the FOLDER is absent — §7.3).
	brokenRev, err := ContentRevision(appDir)
	require.NoError(t, err, "a broken folder is present: ContentRevision does not return ErrNotFound")

	// Repair by write_file of a valid SKILL.md (existing-folder repair),
	// expectation taken from the broken state.
	out, err := ApplyChange(root, ChangeRequest{
		Kind: KindEdit, Slug: "foo", Path: "SKILL.md",
		Content: []byte(validSkillMD("foo", "repaired", "repaired body")),
		Expect:  Expectation{Revision: brokenRev},
		Actor:   ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "write_file"},
	})
	require.NoError(t, err, "the present-but-repairable folder accepts the repair write (never SKILL_EXISTS)")
	assert.Equal(t, PersistenceComplete, out.PersistenceStatus)
	assert.Equal(t, validSkillMD("foo", "repaired", "repaired body"), readFile(t, filepath.Join(appDir, "SKILL.md")),
		"the repaired SKILL.md is the written bytes in full")

	// Repair by restore: break again, then restore the create's own version.
	require.NoError(t, os.Remove(filepath.Join(appDir, "SKILL.md")))
	brokenRev2, err := ContentRevision(appDir)
	require.NoError(t, err)
	_, err = ApplyChange(root, ChangeRequest{
		Kind: KindRestore, Slug: "foo", Version: v1,
		Expect: Expectation{Revision: brokenRev2},
		Actor:  ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "edit_skill"},
	})
	require.NoError(t, err, "restore repairs the broken folder too")
	assert.Equal(t, validSkillMD("foo", "v1", "v1 body"), readFile(t, filepath.Join(appDir, "SKILL.md")))
}

// readFile is a small helper returning file content as string.
func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(raw)
}
