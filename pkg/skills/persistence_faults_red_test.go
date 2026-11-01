package skills

// RED tests — landing/skills (T9): persistence errors after publication —
// §7.6's outcome classes via returned-error fault injection (T-V11, D-8).
// Scenarios S-11.6, S-11.7, S-11.8, S-11.9, S-11.13, S-11.14, S-11.15,
// S-11.16, S-11.17 (the session halves of the "seen"-record rules are
// asserted at the tool level; the restart/detection halves at the boot and
// loader levels — split noted in the traceability table).
// Traces to: skills-spec-r15.md §7.6, §7.1 steps 5–7, §5.10 partial variants,
// §16 US-11, §17.1 row 33, §17.2 D-8.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestClass2_SingleFilePublished_ManifestWriteFails (S-11.6).
// BDD: Given a returned error injected into the manifest write after a
// single-file write published, When the write runs, Then SKILL_PARTIALLY_SAVED
// names the previous version id; the live file has the new content;
// persistence partial. (The menu-generation bump and the session "seen"
// clearing are asserted at the gateway/tool level.)
// Traces to: skills-spec-r15.md §7.6 class 2, S-11.6.
func TestClass2_SingleFilePublished_ManifestWriteFails(t *testing.T) {
	root := t.TempDir()
	out0, vPrev := createSkillViaPipeline(t, root, "app", "v0", "v0 body")
	appDir := filepath.Join(root, "app")

	var fired bool
	FaultManifestWrite = func(string) error {
		if _, statErr := os.Stat(filepath.Join(appDir, "notes.md")); statErr == nil {
			fired = true
			return errors.New("disk full")
		}
		return nil
	}
	defer func() { FaultManifestWrite = nil }()

	out, err := ApplyChange(root, ChangeRequest{
		Kind: KindEdit, Slug: "app", Path: "notes.md",
		Content: []byte("new notes\n"),
		Expect:  Expectation{Revision: out0.Revision},
		Actor:   ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "write_file"},
	})
	require.True(t, fired, "post-publication manifest fault fired")
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
// Traces to: skills-spec-r15.md §7.6 class 2, S-11.7.
func TestClass2_InstallReplace_BlobWriteFails_PredecessorRestorable(t *testing.T) {
	root := t.TempDir()
	// The prerequisite is committed through the pipeline, so a predecessor
	// version genuinely exists to be named and restored (live == newest →
	// the replace's step 1 records nothing extra).
	out0, vPrev := createSkillViaPipeline(t, root, "app", "v0", "v0 body")
	appDir := filepath.Join(root, "app")

	// Fault only after the exact replacement bytes are visible at live.
	// A checkpoint or pre-publication blob write is never the injected failure.
	var fired bool
	FaultBlobWrite = func(string) error {
		b, readErr := os.ReadFile(filepath.Join(appDir, "SKILL.md"))
		if readErr == nil && string(b) == validSkillMD("app", "replaced", "replaced body") {
			fired = true
			return errors.New("blob store full")
		}
		return nil
	}
	defer func() { FaultBlobWrite = nil }()

	staged := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(staged, "SKILL.md"),
		[]byte(validSkillMD("app", "replaced", "replaced body")), 0o644))

	out, err := ApplyChange(root, ChangeRequest{
		Kind: KindInstallReplace, Slug: "app", StagedDir: staged,
		Expect: Expectation{Revision: out0.Revision},
		Actor:  ChangeActor{Kind: "person", Tool: "rest-install"},
	})
	require.True(t, fired, "the blob-write fault hook fired")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrSkillPartiallySaved))
	assert.Equal(t, PersistencePartial, out.PersistenceStatus)

	// Class 2 names the previous version id and offers it for restore.
	assert.Contains(t, out.Message, "The previous state is version "+vPrev+";",
		"class 2 names the previous version id")
	assert.Contains(t, out.Message, "restore it to undo",
		"the predecessor is offered for restore")

	// The replacement is live — and the named predecessor genuinely
	// restores from history (positive control for "restorable").
	body, err := os.ReadFile(filepath.Join(appDir, "SKILL.md"))
	require.NoError(t, err)
	assert.Equal(t, validSkillMD("app", "replaced", "replaced body"), string(body), "the replacement is live")
	assert.Equal(t, []string{vPrev}, manifestIDs(t, SkillHistoryRoot(root), "app"), "no failed replacement version committed")
	FaultBlobWrite = nil // do not poison the positive recovery control
	require.NoError(t, RecoverInterrupted(root, SkillHistoryRoot(root)))
	_, err = ApplyChange(root, ChangeRequest{
		Kind: KindRestore, Slug: "app", Version: vPrev,
		Expect: Expectation{Revision: readLiveRevision(t, appDir)},
		Actor:  ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "edit_skill"},
	})
	require.NoError(t, err, "the named predecessor genuinely restores from history")
	assert.Equal(t, validSkillMD("app", "v0", "v0 body"), readFile(t, filepath.Join(appDir, "SKILL.md")),
		"the restored bytes are the predecessor's exact full state")
}

// TestClass3_SwapRemoveOrAuditFault_SuccessWithWarning (S-11.8).
// BDD: Given returned errors injected into the .swap removal, and separately
// into the audit write, after the manifest committed, Then the change
// succeeds with the version id and a warning (swap) / silently-degraded audit
// counted by the audit-degraded counter, never an agent-facing failure.
// Traces to: skills-spec-r15.md §7.6 class 3, S-11.8.
func TestClass3_SwapRemoveOrAuditFault_SuccessWithWarning(t *testing.T) {
	t.Run("swap removal fault", func(t *testing.T) {
		root := t.TempDir()
		out0, _ := createSkillViaPipeline(t, root, "app", "v0", "v0 body")

		// A .swap exists to remove only when publication moved a whole
		// folder (install replace) — a single-file write never creates one,
		// so the class-3 swap leg drives KindInstallReplace.
		var fired bool
		FaultSwapRemove = func(string) error {
			fired = true
			return errors.New("swap busy")
		}
		defer func() { FaultSwapRemove = nil }()

		out, err := ApplyChange(root, ChangeRequest{
			Kind: KindInstallReplace, Slug: "app", StagedDir: stagedWithSkillMD(t, "app", "replaced"),
			Expect: Expectation{Revision: out0.Revision},
			Actor:  ChangeActor{Kind: "person", Tool: "rest-install"},
		})
		require.True(t, fired, "the swap-removal hook fired (a folder replace leaves a .swap to remove)")
		require.NoError(t, err, "class 3: the outcome stands")
		assert.Equal(t, PersistenceComplete, out.PersistenceStatus)
		assert.NotEmpty(t, out.Warning, "success carries a warning, never silent")
		assert.Contains(t, out.Message+out.Warning, "saved as skill version ", "the version id is named")
		assert.Equal(t, validSkillMD("app", "replaced", "replaced body"), readFile(t, filepath.Join(root, "app", "SKILL.md")))
		assert.Len(t, manifestIDs(t, SkillHistoryRoot(root), "app"), 2)
		assert.DirExists(t, filepath.Join(root, ".swap", "app"), "failed disposal leaves the acknowledged leftover")
		FaultSwapRemove = nil
		require.NoError(t, RecoverInterrupted(root, SkillHistoryRoot(root)))
		_, statErr := os.Stat(filepath.Join(root, ".swap", "app"))
		assert.True(t, os.IsNotExist(statErr))
	})

	t.Run("audit write fault", func(t *testing.T) {
		root := t.TempDir()
		out0, _ := createSkillViaPipeline(t, root, "app", "v0", "v0 body")

		before := audit.SnapshotSkipped().Total
		var fired int
		FaultAuditWrite = func(event string) error {
			assert.Equal(t, "skill.write", event)
			fired++
			return errors.New("audit sink down")
		}
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
		require.Equal(t, 1, fired, "the actual skill.write audit failure fired once")
		assert.Equal(t, before+1, audit.SnapshotSkipped().Total, "exactly one skipped audit emission")
		assert.Equal(t, "notes\n", readFile(t, filepath.Join(root, "app", "notes.md")))
		assert.Len(t, manifestIDs(t, SkillHistoryRoot(root), "app"), 2, "successful mutation really committed history")
		assert.Empty(t, out.Warning, "audit degradation is not an agent-facing failure")
	})
}

// TestClass1_Step5FailsWithPutBack (S-11.9). BDD: Given a returned error in
// step 5 with the previous folder put back, Then class-1 "not saved", the
// live folder unchanged, and no new version except step 1's
// baseline/external_change.
// Traces to: skills-spec-r15.md §7.6 class 1, S-11.9.
func TestClass1_Step5FailsWithPutBack(t *testing.T) {
	root := t.TempDir()
	writeSkillFixture(t, root, "app")
	original := readLiveRevision(t, filepath.Join(root, "app"))

	// Step 5's forward rename is live → skills/.swap/<slug> (§7.1 step 5);
	// the fault targets exactly that rename for app.
	var fired bool
	FaultForwardRename = func(oldpath, newpath string) error {
		if filepath.Base(filepath.Dir(newpath)) == ".swap" && filepath.Base(newpath) == "app" {
			fired = true
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
	require.True(t, fired, "the forward-rename fault fired on the live→.swap rename")
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
// Traces to: skills-spec-r15.md §7.6 class 2-create-i, §5.10
// true-first-creation variant, S-11.13.
func TestClass2CreateI_TrueFirstCreate_ManifestFails(t *testing.T) {
	root := t.TempDir()

	var fired bool
	FaultManifestWrite = func(string) error {
		if _, err := os.Stat(filepath.Join(root, "newskill", "SKILL.md")); err == nil {
			fired = true
			return errors.New("disk full")
		}
		return nil
	}
	defer func() { FaultManifestWrite = nil }()

	out, err := ApplyChange(root, ChangeRequest{
		Kind: KindCreate, Slug: "newskill",
		Content: []byte(validSkillMD("newskill", "first", "first body")),
		Expect:  Expectation{Absent: true},
		Actor:   ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "write_file"},
	})
	require.True(t, fired, "true-first-create manifest fault occurs only after publication")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrSkillPartiallySaved))
	assert.Equal(t, PersistencePartial, out.PersistenceStatus)
	assert.Contains(t, out.Message,
		"No version history exists for this skill; remove the skill with edit_skill action=delete (or REST DELETE) to undo.",
		"byte-exact §5.10 true-first-creation undo (the only variant allowed to say no history exists)")
	assert.NotContains(t, out.Message, "The previous state is version",
		"class 2-create-i names no version")
	assert.Equal(t, validSkillMD("newskill", "first", "first body"), readFile(t, filepath.Join(root, "newskill", "SKILL.md")), "exact first-create content remains live")
	manifests, globErr := filepath.Glob(filepath.Join(SkillHistoryRoot(root), "newskill", "versions", "*.json"))
	require.NoError(t, globErr)
	assert.Empty(t, manifests, "failed first manifest does not invent committed history")
}

// TestDelete_ManifestFails_PutBackSucceeds_Class1 (S-11.14 main leg).
// BDD: Given the delete's manifest write fails after live → .swap, Then the
// put-back renames the folder to live, the caller gets the ordinary "not
// saved" failure, and there is no leftover, no version, no generation change.
// Traces to: skills-spec-r15.md §7.6 class 1b-del, S-11.14.
func TestDelete_ManifestFails_PutBackSucceeds_Class1(t *testing.T) {
	root := t.TempDir()
	out0, _ := createSkillViaPipeline(t, root, "app", "v0", "v0 body")
	rev := out0.Revision

	var fired bool
	FaultManifestWrite = func(string) error {
		if _, statErr := os.Stat(filepath.Join(root, ".swap", "app")); statErr == nil {
			fired = true
			return errors.New("disk full")
		}
		return nil
	}
	defer func() { FaultManifestWrite = nil }()

	out, err := ApplyChange(root, ChangeRequest{
		Kind: KindDelete, Slug: "app",
		Expect: Expectation{Revision: rev},
		Actor:  ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "edit_skill"},
	})
	require.True(t, fired, "delete manifest fault occurs with live moved into .swap")
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
// Traces to: skills-spec-r15.md §7.6 class 1b-del-2, S-11.14 variant.
func TestDelete_ManifestFails_PutBackAlsoFails_Class1bDel2(t *testing.T) {
	root := t.TempDir()
	out0, _ := createSkillViaPipeline(t, root, "app", "v0", "v0 body")
	rev := out0.Revision

	var manifestFired, putbackFired bool
	FaultManifestWrite = func(string) error {
		if _, statErr := os.Stat(filepath.Join(root, ".swap", "app")); statErr == nil {
			manifestFired = true
			return errors.New("disk full")
		}
		return nil
	}
	FaultPutBackRename = func(old, new string) error { putbackFired = true; return errors.New("rename failed") }
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
	require.True(t, manifestFired && putbackFired, "both post-swap manifest and put-back faults fired")
	assert.True(t, errors.Is(err, ErrSkillPartiallySaved), "1b-del-2 is a partial outcome")
	assert.Equal(t, PersistencePartial, out.PersistenceStatus)
	assert.Contains(t, err.Error(),
		"the deletion of 'app' did not complete and its folder is temporarily unavailable; it will be restored at the next start. Do not recreate 'app' until then.",
		"byte-exact §7.6 class 1b-del-2 text")
	assert.DirExists(t, filepath.Join(root, ".swap", "app"))
	FaultManifestWrite, FaultPutBackRename = nil, nil
	require.NoError(t, RecoverInterrupted(root, SkillHistoryRoot(root)))
	assert.Equal(t, validSkillMD("app", "v0", "v0 body"), readFile(t, filepath.Join(root, "app", "SKILL.md")))
}

// TestClass1b_ReplaceSwapAndPutBackFail (S-11.15).
// BDD: Given a replace whose step-5 swap fails and whose put-back also fails,
// Then the loss text with the previous version id, persistence partial; the
// skills generation is bumped so the menu drops the skill until restore
// (asserted at the gateway level); restart renames .swap back.
// Traces to: skills-spec-r15.md §7.6 class 1b, S-11.15.
func TestClass1b_ReplaceSwapAndPutBackFail(t *testing.T) {
	root := t.TempDir()
	out0, _ := createSkillViaPipeline(t, root, "app", "v0", "v0 body")
	rev := out0.Revision

	staged := stagedWithSkillMD(t, "app", "replacement")
	var forwardFired, putbackFired bool
	previousRename := renamePublishedSkill
	renamePublishedSkill = func(oldpath, newpath string) error {
		if newpath == filepath.Join(root, "app") && oldpath != filepath.Join(root, "app") {
			// live→swap must succeed; only staged→live is the failing rename.
			forwardFired = true
			return errors.New("rename failed")
		}
		return previousRename(oldpath, newpath)
	}
	FaultPutBackRename = func(oldpath, newpath string) error { putbackFired = true; return errors.New("rename failed") }
	defer func() {
		renamePublishedSkill = previousRename
		FaultPutBackRename = nil
	}()

	out, err := ApplyChange(root, ChangeRequest{
		Kind: KindInstallReplace, Slug: "app", StagedDir: staged,
		Expect: Expectation{Revision: rev},
		Actor:  ChangeActor{Kind: "person", Tool: "rest-install"},
	})
	require.True(t, forwardFired && putbackFired, "replacement publication AND put-back faults both fired")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrSkillPartiallySaved), "class 1b is partial")
	assert.Equal(t, PersistencePartial, out.PersistenceStatus)
	assert.Contains(t, err.Error(),
		"replacement publication failed and the previous package could not be restored; no live package is available",
		"byte-exact §7.6 class 1b loss text")
	assert.Contains(t, out.Message, "The previous state is version ",
		"the recovery version is named")
	assert.DirExists(t, filepath.Join(root, ".swap", "app"))
	_, statErr := os.Stat(filepath.Join(root, "app"))
	assert.True(t, os.IsNotExist(statErr))
	renamePublishedSkill, FaultPutBackRename = previousRename, nil
	require.NoError(t, RecoverInterrupted(root, SkillHistoryRoot(root)))
	assert.Equal(t, validSkillMD("app", "v0", "v0 body"), readFile(t, filepath.Join(root, "app", "SKILL.md")))
}

// TestCreateFails_AfterParentDirCreated_CleanupSucceeds (S-11.16 main leg).
// BDD: Given a returned error injected after WriteFileAtomic created
// skills/<new>/ and before the file published, with the cleanup succeeding,
// Then class 1; the live state returns to absent; the retry is a clean
// create (no SKILL_EXISTS); no version, no generation change.
// Traces to: skills-spec-r15.md §7.6 class 1 + step 5 create cleanup, S-11.16.
func TestCreateFails_AfterParentDirCreated_CleanupSucceeds(t *testing.T) {
	root := t.TempDir()

	var fired bool
	FaultLiveWrite = func(string) error {
		if st, err := os.Stat(filepath.Join(root, "fresh")); err == nil && st.IsDir() {
			fired = true
			return errors.New("write failed")
		}
		return nil
	}
	defer func() { FaultLiveWrite = nil }()

	out, err := ApplyChange(root, ChangeRequest{
		Kind: KindCreate, Slug: "fresh",
		Content: []byte(validSkillMD("fresh", "first", "first body")),
		Expect:  Expectation{Absent: true},
		Actor:   ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "write_file"},
	})
	require.True(t, fired, "live-write failure happened after the parent directory was really created")
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
// Traces to: skills-spec-r15.md §7.6 class 1c, §7.3 present-and-repairable,
// S-11.16 variant.
func TestCreateFails_CleanupAlsoFails_Class1c_LeftoverFolder(t *testing.T) {
	root := t.TempDir()

	var writeFired, cleanupFired bool
	FaultLiveWrite = func(string) error {
		if st, err := os.Stat(filepath.Join(root, "fresh")); err == nil && st.IsDir() {
			writeFired = true
			return errors.New("write failed")
		}
		return nil
	}
	FaultEmptyDirCleanup = func(string) error { cleanupFired = true; return errors.New("remove failed") }
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
	require.True(t, writeFired && cleanupFired, "both the post-parent write failure and cleanup failure were exercised")
	assert.Contains(t, err.Error(),
		"an empty folder 'fresh' remains; it is not a skill (no SKILL.md)",
		"byte-exact §7.6 class-1c leftover text")
	assert.DirExists(t, filepath.Join(root, "fresh"), "the empty leftover folder remains")

	// The leftover counts as present and repairable: an existing-folder write
	// with an expected revision commits — never SKILL_EXISTS. The
	// expectation is the leftover's actual content revision (§7.3: the
	// folder is present; ContentRevision tolerates its missing SKILL.md).
	leftoverRev, revErr := ContentRevision(filepath.Join(root, "fresh"))
	require.NoError(t, revErr, "the leftover folder is present (its revision is computable)")
	FaultLiveWrite = nil
	out2, err := ApplyChange(root, ChangeRequest{
		Kind: KindEdit, Slug: "fresh", Path: "SKILL.md",
		Content: []byte(validSkillMD("fresh", "repaired", "repaired body")),
		Expect:  Expectation{Revision: leftoverRev},
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
// Traces to: skills-spec-r15.md §7.6 class 2-create-ii, §5.10
// absence-with-history, S-11.17.
func TestRestoreIntoAbsence_ManifestFails_Class2CreateIi(t *testing.T) {
	root := t.TempDir()
	out1, v1 := createSkillViaPipeline(t, root, "foo", "v1", "v1 body")
	_, err := ApplyChange(root, ChangeRequest{
		Kind: KindDelete, Slug: "foo",
		Expect: Expectation{Revision: out1.Revision},
		Actor:  ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "edit_skill"},
	})
	require.NoError(t, err)

	before := manifestIDs(t, SkillHistoryRoot(root), "foo")
	var fired bool
	FaultManifestWrite = func(string) error {
		if _, err := os.Stat(filepath.Join(root, "foo", "SKILL.md")); err == nil {
			fired = true
			return errors.New("disk full")
		}
		return nil
	}
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
	require.True(t, fired, "restore manifest failure occurs after its exact live folder reappears")
	assert.Equal(t, validSkillMD("foo", "v1", "v1 body"), readFile(t, filepath.Join(root, "foo", "SKILL.md")), "restored full content remains live")
	assert.Equal(t, before, manifestIDs(t, SkillHistoryRoot(root), "foo"), "failed restore adds no manifest and preserves the earlier chain")
}

// stagedWithSkillMD builds a staged install folder with one SKILL.md.
func stagedWithSkillMD(t *testing.T, slug, body string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"),
		[]byte(validSkillMD(slug, body, body+" body")), 0o644))
	return dir
}
