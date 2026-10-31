package skills

// RED tests — landing/skills (T9): persistence errors after publication —
// §7.6's outcome classes via returned-error fault injection (T-V11, D-8).
// Scenarios S-11.6, S-11.7, S-11.8, S-11.9, S-11.13, S-11.14, S-11.15,
// S-11.16, S-11.17 (the session halves of the "seen"-record rules are
// asserted at the tool level; the restart/detection halves at the boot and
// loader levels — split noted in the traceability table).
// Traces to: skills-spec-r13.md §7.6, §7.1 steps 5–7, §5.10 partial variants,
// §16 US-11, §17.1 row 33, §17.2 D-8.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestClass2_SingleFilePublished_ManifestWriteFails (S-11.6).
// BDD: Given a returned error injected into the manifest write after a
// single-file write published, When the write runs, Then SKILL_PARTIALLY_SAVED
// names the previous version id; the live file has the new content;
// persistence partial. (The menu-generation bump and the session "seen"
// clearing are asserted at the gateway/tool level.)
// Traces to: skills-spec-r13.md §7.6 class 2, S-11.6.
func TestClass2_SingleFilePublished_ManifestWriteFails(t *testing.T) {
	root := t.TempDir()
	out0, vPrev := commitWrite(t, root, "app", "SKILL.md",
		validSkillMD("app", "v0", "v0 body"), "")
	appDir := filepath.Join(root, "app")

	FaultManifestWrite = func(string) error { return errors.New("disk full") }
	defer func() { FaultManifestWrite = nil }()

	out, err := ApplyChange(root, ChangeRequest{
		Kind: KindEdit, Slug: "app", Path: "notes.md",
		Content: []byte("new notes\n"),
		Expect:  Expectation{Revision: out0.Revision},
		Actor:   ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "write_file"},
	})
	require.Error(t, err, "the class-2 outcome is an error carrying the partial text")
	assert.True(t, errors.Is(err, ErrSkillPartiallySaved))
	assert.Equal(t, PersistencePartial, out.PersistenceStatus)
	assert.Contains(t, out.Message,
		"the change to 'app' is live, but its version record could not be written",
		"byte-exact §5.10 replacement variant prefix")
	assert.Contains(t, out.Message,
		"The previous state is version "+vPrev+"; restore it to undo.",
		"the gate-passing predecessor is offered for restore")
	assert.Contains(t, out.Message, "Read the skill again before the next change.",
		"byte-exact closing sentence")

	body, _ := os.ReadFile(filepath.Join(appDir, "notes.md"))
	assert.Equal(t, "new notes\n", string(body), "the live file has the new content")
}

// TestClass2_InstallReplace_BlobWriteFails_PredecessorRestorable (S-11.7).
// BDD: Given a returned error injected into the blob write after a folder
// replace published, Then the same class-2 outcome, and the previous state is
// restorable from history.
// Traces to: skills-spec-r13.md §7.6 class 2, S-11.7.
func TestClass2_InstallReplace_BlobWriteFails_PredecessorRestorable(t *testing.T) {
	root := t.TempDir()
	writeSkillFixture(t, root, "app")
	rev := readLiveRevision(t, filepath.Join(root, "app"))

	staged := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(staged, "SKILL.md"),
		[]byte(validSkillMD("app", "replaced", "replaced body")), 0o644))

	FaultBlobWrite = func(string) error { return errors.New("blob store full") }
	defer func() { FaultBlobWrite = nil }()

	out, err := ApplyChange(root, ChangeRequest{
		Kind: KindInstallReplace, Slug: "app", StagedDir: staged,
		Expect: Expectation{Revision: rev},
		Actor:  ChangeActor{Kind: "person", Tool: "rest-install"},
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrSkillPartiallySaved))
	assert.Equal(t, PersistencePartial, out.PersistenceStatus)

	// The previous state is restorable from history: the replaced folder is
	// live and a predecessor manifest exists for the key.
	assert.FileExists(t, filepath.Join(root, "app", "SKILL.md"))
	assert.Contains(t, out.Message, "The previous state is version ",
		"class 2 names the previous version id")
}

// TestClass3_SwapRemoveOrAuditFault_SuccessWithWarning (S-11.8).
// BDD: Given returned errors injected into the .swap removal, and separately
// into the audit write, after the manifest committed, Then the change
// succeeds with the version id and a warning (swap) / silently-degraded audit
// counted by the audit-degraded counter, never an agent-facing failure.
// Traces to: skills-spec-r13.md §7.6 class 3, S-11.8.
func TestClass3_SwapRemoveOrAuditFault_SuccessWithWarning(t *testing.T) {
	t.Run("swap removal fault", func(t *testing.T) {
		root := t.TempDir()
		out0, _ := commitWrite(t, root, "app", "SKILL.md",
			validSkillMD("app", "v0", "v0 body"), "")

		FaultSwapRemove = func(string) error { return errors.New("swap busy") }
		defer func() { FaultSwapRemove = nil }()

		out, err := ApplyChange(root, ChangeRequest{
			Kind: KindEdit, Slug: "app", Path: "notes.md",
			Content: []byte("notes\n"),
			Expect:  Expectation{Revision: out0.Revision},
			Actor:   ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "write_file"},
		})
		require.NoError(t, err, "class 3: the outcome stands")
		assert.Equal(t, PersistenceComplete, out.PersistenceStatus)
		assert.NotEmpty(t, out.Warning, "success carries a warning, never silent")
		assert.Contains(t, out.Warning, "saved as skill version ", "the version id is named")
	})

	t.Run("audit write fault", func(t *testing.T) {
		root := t.TempDir()
		out0, _ := commitWrite(t, root, "app", "SKILL.md",
			validSkillMD("app", "v0", "v0 body"), "")

		FaultAuditWrite = func(string) error { return errors.New("audit sink down") }
		defer func() { FaultAuditWrite = nil }()

		out, err := ApplyChange(root, ChangeRequest{
			Kind: KindEdit, Slug: "app", Path: "notes.md",
			Content: []byte("notes\n"),
			Expect:  Expectation{Revision: out0.Revision},
			Actor:   ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "write_file"},
		})
		// The audit failure is counted by the existing audit-degraded counter,
		// not reported to the agent: the caller still sees success.
		require.NoError(t, err, "an audit write failure must not fail the change for the caller")
		assert.Equal(t, PersistenceComplete, out.PersistenceStatus)
	})
}

// TestClass1_Step5FailsWithPutBack (S-11.9). BDD: Given a returned error in
// step 5 with the previous folder put back, Then class-1 "not saved", the
// live folder unchanged, and no new version except step 1's
// baseline/external_change.
// Traces to: skills-spec-r13.md §7.6 class 1, S-11.9.
func TestClass1_Step5FailsWithPutBack(t *testing.T) {
	root := t.TempDir()
	writeSkillFixture(t, root, "app")
	original := readLiveRevision(t, filepath.Join(root, "app"))

	FaultForwardRename = func(oldpath, newpath string) error {
		if filepath.Base(newpath) == "app.swap-tmp" {
			return errors.New("rename failed")
		}
		return nil
	}
	defer func() { FaultForwardRename = nil }()

	out, err := ApplyChange(root, ChangeRequest{
		Kind: KindInstallReplace, Slug: "app", StagedDir: stagedWithSkillMD(t, "app", "replacement"),
		Expect: Expectation{Revision: original},
		Actor:  ChangeActor{Kind: "person", Tool: "rest-install"},
	})
	require.Error(t, err, "class 1: not saved")
	assert.Equal(t, PersistenceNone, out.PersistenceStatus,
		"nothing was published; the live state returned to the pre-operation state")
	assert.Contains(t, err.Error(), "not saved")

	body, _ := os.ReadFile(filepath.Join(root, "app", "SKILL.md"))
	assert.Contains(t, string(body), "fixture skill app", "live unchanged")
}

// TestClass2CreateI_TrueFirstCreate_ManifestFails (S-11.13 first leg).
// BDD: Given a true first create published and the manifest write faulted
// (no manifest existed for the key), Then the true-first-creation variant:
// names no previous version, names delete as the undo, live folder present,
// persistence partial.
// Traces to: skills-spec-r13.md §7.6 class 2-create-i, §5.10
// true-first-creation variant, S-11.13.
func TestClass2CreateI_TrueFirstCreate_ManifestFails(t *testing.T) {
	root := t.TempDir()

	FaultManifestWrite = func(string) error { return errors.New("disk full") }
	defer func() { FaultManifestWrite = nil }()

	out, err := ApplyChange(root, ChangeRequest{
		Kind: KindCreate, Slug: "newskill",
		Content: []byte(validSkillMD("newskill", "first", "first body")),
		Expect:  Expectation{Absent: true},
		Actor:   ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "write_file"},
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrSkillPartiallySaved))
	assert.Equal(t, PersistencePartial, out.PersistenceStatus)
	assert.Contains(t, out.Message,
		"No version history exists for this skill; remove the skill with edit_skill action=delete (or REST DELETE) to undo.",
		"byte-exact §5.10 true-first-creation undo (the only variant allowed to say no history exists)")
	assert.NotContains(t, out.Message, "The previous state is version",
		"class 2-create-i names no version")
	assert.FileExists(t, filepath.Join(root, "newskill", "SKILL.md"), "live folder present, content kept")
}

// TestDelete_ManifestFails_PutBackSucceeds_Class1 (S-11.14 main leg).
// BDD: Given the delete's manifest write fails after live → .swap, Then the
// put-back renames the folder to live, the caller gets the ordinary "not
// saved" failure, and there is no leftover, no version, no generation change.
// Traces to: skills-spec-r13.md §7.6 class 1b-del, S-11.14.
func TestDelete_ManifestFails_PutBackSucceeds_Class1(t *testing.T) {
	root := t.TempDir()
	writeSkillFixture(t, root, "app")
	rev := readLiveRevision(t, filepath.Join(root, "app"))

	FaultManifestWrite = func(string) error { return errors.New("disk full") }
	defer func() { FaultManifestWrite = nil }()

	out, err := ApplyChange(root, ChangeRequest{
		Kind: KindDelete, Slug: "app",
		Expect: Expectation{Revision: rev},
		Actor:  ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "edit_skill"},
	})
	require.Error(t, err, "the delete either completes or leaves no trace")
	assert.Equal(t, PersistenceNone, out.PersistenceStatus)
	assert.DirExists(t, filepath.Join(root, "app"), "put-back restored the live folder")
	_, statErr := os.Stat(filepath.Join(root, ".swap", "app"))
	assert.True(t, os.IsNotExist(statErr), "no .swap leftover on the successful put-back")
}

// TestDelete_ManifestFails_PutBackAlsoFails_Class1bDel2 (S-11.14 variant).
// BDD: Given the put-back rename also fails, Then the 1b-del-2 text with
// persistence partial; the restart repairs (asserted at the recovery level —
// S-11.1's rule).
// Traces to: skills-spec-r13.md §7.6 class 1b-del-2, S-11.14 variant.
func TestDelete_ManifestFails_PutBackAlsoFails_Class1bDel2(t *testing.T) {
	root := t.TempDir()
	writeSkillFixture(t, root, "app")
	rev := readLiveRevision(t, filepath.Join(root, "app"))

	FaultManifestWrite = func(string) error { return errors.New("disk full") }
	FaultPutBackRename = func(string, string) error { return errors.New("rename failed") }
	defer func() {
		FaultManifestWrite = nil
		FaultPutBackRename = nil
	}()

	out, err := ApplyChange(root, ChangeRequest{
		Kind: KindDelete, Slug: "app",
		Expect: Expectation{Revision: rev},
		Actor:  ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "edit_skill"},
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrSkillPartiallySaved), "1b-del-2 is a partial outcome")
	assert.Equal(t, PersistencePartial, out.PersistenceStatus)
	assert.Contains(t, err.Error(),
		"the deletion of 'app' did not complete and its folder is temporarily unavailable; it will be restored at the next start. Do not recreate 'app' until then.",
		"byte-exact §7.6 class 1b-del-2 text")
}

// TestClass1b_ReplaceSwapAndPutBackFail (S-11.15).
// BDD: Given a replace whose step-5 swap fails and whose put-back also fails,
// Then the loss text with the previous version id, persistence partial; the
// skills generation is bumped so the menu drops the skill until restore
// (asserted at the gateway level); restart renames .swap back.
// Traces to: skills-spec-r13.md §7.6 class 1b, S-11.15.
func TestClass1b_ReplaceSwapAndPutBackFail(t *testing.T) {
	root := t.TempDir()
	writeSkillFixture(t, root, "app")
	rev := readLiveRevision(t, filepath.Join(root, "app"))

	FaultForwardRename = func(oldpath, newpath string) error { return errors.New("rename failed") }
	FaultPutBackRename = func(oldpath, newpath string) error { return errors.New("rename failed") }
	defer func() {
		FaultForwardRename = nil
		FaultPutBackRename = nil
	}()

	out, err := ApplyChange(root, ChangeRequest{
		Kind: KindInstallReplace, Slug: "app", StagedDir: stagedWithSkillMD(t, "app", "replacement"),
		Expect: Expectation{Revision: rev},
		Actor:  ChangeActor{Kind: "person", Tool: "rest-install"},
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrSkillPartiallySaved), "class 1b is partial")
	assert.Equal(t, PersistencePartial, out.PersistenceStatus)
	assert.Contains(t, err.Error(),
		"replacement publication failed and the previous package could not be restored; no live package is available",
		"byte-exact §7.6 class 1b loss text")
	assert.Contains(t, out.Message, "The previous state is version ",
		"the recovery version is named")
}

// TestCreateFails_AfterParentDirCreated_CleanupSucceeds (S-11.16 main leg).
// BDD: Given a returned error injected after WriteFileAtomic created
// skills/<new>/ and before the file published, with the cleanup succeeding,
// Then class 1; the live state returns to absent; the retry is a clean
// create (no SKILL_EXISTS); no version, no generation change.
// Traces to: skills-spec-r13.md §7.6 class 1 + step 5 create cleanup, S-11.16.
func TestCreateFails_AfterParentDirCreated_CleanupSucceeds(t *testing.T) {
	root := t.TempDir()

	FaultLiveWrite = func(string) error { return errors.New("write failed") }
	defer func() { FaultLiveWrite = nil }()

	out, err := ApplyChange(root, ChangeRequest{
		Kind: KindCreate, Slug: "fresh",
		Content: []byte(validSkillMD("fresh", "first", "first body")),
		Expect:  Expectation{Absent: true},
		Actor:   ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "write_file"},
	})
	require.Error(t, err, "class 1: the create was not saved")
	assert.Equal(t, PersistenceNone, out.PersistenceStatus)

	_, statErr := os.Stat(filepath.Join(root, "fresh"))
	assert.True(t, os.IsNotExist(statErr),
		"step 5's empty-directory cleanup removed the folder — live state back to absent")

	// The retry is a clean create, not a SKILL_EXISTS collision.
	FaultLiveWrite = nil
	out2, err := ApplyChange(root, ChangeRequest{
		Kind: KindCreate, Slug: "fresh",
		Content: []byte(validSkillMD("fresh", "first", "first body")),
		Expect:  Expectation{Absent: true},
		Actor:   ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "write_file"},
	})
	require.NoError(t, err, "the retry commits as a clean create")
	assert.Equal(t, PersistenceComplete, out2.PersistenceStatus)
}

// TestCreateFails_CleanupAlsoFails_Class1c_LeftoverFolder (S-11.16 variant).
// BDD: Given the empty-dir cleanup also fails, Then the class-1c text names
// the leftover folder and the repair chain; the leftover folder counts as
// present, so an existing-folder write with an expected revision commits
// (never SKILL_EXISTS).
// Traces to: skills-spec-r13.md §7.6 class 1c, §7.3 present-and-repairable,
// S-11.16 variant.
func TestCreateFails_CleanupAlsoFails_Class1c_LeftoverFolder(t *testing.T) {
	root := t.TempDir()

	FaultLiveWrite = func(string) error { return errors.New("write failed") }
	FaultEmptyDirCleanup = func(string) error { return errors.New("remove failed") }
	defer func() {
		FaultLiveWrite = nil
		FaultEmptyDirCleanup = nil
	}()

	out, err := ApplyChange(root, ChangeRequest{
		Kind: KindCreate, Slug: "fresh",
		Content: []byte(validSkillMD("fresh", "first", "first body")),
		Expect:  Expectation{Absent: true},
		Actor:   ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "write_file"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(),
		"an empty folder 'fresh' remains; it is not a skill (no SKILL.md)",
		"byte-exact §7.6 class-1c leftover text")
	assert.DirExists(t, filepath.Join(root, "fresh"), "the empty leftover folder remains")

	// The leftover counts as present and repairable: an existing-folder write
	// with an expected revision commits — never SKILL_EXISTS.
	FaultLiveWrite = nil
	out2, err := ApplyChange(root, ChangeRequest{
		Kind: KindEdit, Slug: "fresh", Path: "SKILL.md",
		Content: []byte(validSkillMD("fresh", "repaired", "repaired body")),
		Expect:  Expectation{Revision: "any-seen-revision"},
		Actor:   ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "write_file"},
	})
	require.NoError(t, err, "the existing-folder repair commits (no SKILL_EXISTS)")
	assert.Equal(t, PersistenceComplete, out2.PersistenceStatus)
	_ = out
}

// TestRestoreIntoAbsence_ManifestFails_Class2CreateIi (S-11.17).
// BDD: Given a deleted skill (delete_skill head V2) whose restore published
// the folder but whose manifest write failed, Then the absence-with-history
// variant: "the previous live state was absent (the skill had been deleted)",
// the earlier history unchanged, undo is delete — never "no previous version
// exists".
// Traces to: skills-spec-r13.md §7.6 class 2-create-ii, §5.10
// absence-with-history, S-11.17.
func TestRestoreIntoAbsence_ManifestFails_Class2CreateIi(t *testing.T) {
	root := t.TempDir()
	out1, v1 := commitWrite(t, root, "foo", "SKILL.md",
		validSkillMD("foo", "v1", "v1 body"), "")
	_, err := ApplyChange(root, ChangeRequest{
		Kind: KindDelete, Slug: "foo",
		Expect: Expectation{Revision: out1.Revision},
		Actor:  ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "edit_skill"},
	})
	require.NoError(t, err)

	FaultManifestWrite = func(string) error { return errors.New("disk full") }
	defer func() { FaultManifestWrite = nil }()

	out, err := ApplyChange(root, ChangeRequest{
		Kind: KindRestore, Slug: "foo", Version: v1,
		Expect: Expectation{Absent: true},
		Actor:  ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "edit_skill"},
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrSkillPartiallySaved))
	assert.Equal(t, PersistencePartial, out.PersistenceStatus)
	assert.Contains(t, out.Message,
		"The previous live state was absent (the skill had been deleted)",
		"byte-exact §5.10 history note for a delete_skill head")
	assert.Contains(t, out.Message, "the skill's earlier history is unchanged")
	assert.Contains(t, out.Message,
		"To undo, remove the skill with edit_skill action=delete (or REST DELETE).",
		"undo is deletion, never a restore offer for the retained head")
	assert.NotContains(t, out.Message, "No version history exists",
		"never claims no history when a chain exists")
	assert.FileExists(t, filepath.Join(root, "foo", "SKILL.md"), "live present, content kept")
}

// stagedWithSkillMD builds a staged install folder with one SKILL.md.
func stagedWithSkillMD(t *testing.T, slug, body string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"),
		[]byte(validSkillMD(slug, body, body+" body")), 0o644))
	return dir
}
