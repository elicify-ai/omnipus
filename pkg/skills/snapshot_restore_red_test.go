package skills

// RED tests — landing/skills (T9): the snapshot domain and restoration
// eligibility (§9.5, R3-MAJ-003), the delete_file resulting-instruction
// validity (§7.2, R6-MAJ-002), bounded capture at mutation time (§7.2
// capture-time enforcement, R7-MAJ-001), and the exec-bit revision fixture
// (R8-MIN-002). Load-side halves of S-5.20/S-5.22/S-5.23 live in the pkg/tools
// loading-path tests.
// Scenarios: S-5.16, S-5.17, S-5.18, S-5.19, S-5.20 (mutation legs), D-9.
// Traces to: skills-spec-r15.md §9.5, §7.2, §17.1 rows 39/40/41, §17.2 D-9.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// blobNames lists the finalized blob file names for a skill's history.
func blobNames(t *testing.T, historyRoot, slug string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(historyRoot, slug, "blobs"))
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// TestRestore_ReproducesNestedDotContent (S-5.16).
// BDD: Given an outside edit adds foo/.notes/x.md and an allowed edit commits
// V2 capturing it (V1 predates .notes), restore V1 removes .notes (folder
// matches V1 byte-exact) and restore V2 brings it back byte-exact; both pass
// validation.
// Traces to: skills-spec-r15.md §6.1 item 7 + §7.2 restore row (SD-16),
// §17.1 row 39, S-5.16.
func TestRestore_ReproducesNestedDotContent(t *testing.T) {
	root := t.TempDir()
	_, v1 := createSkillViaPipeline(t, root, "foo", "v1", "v1 body")

	appDir := filepath.Join(root, "foo")
	require.NoError(t, os.MkdirAll(filepath.Join(appDir, ".notes"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(appDir, ".notes", "x.md"), []byte("secret note\n"), 0o644))

	out2, v2 := commitWrite(t, root, "foo", "SKILL.md",
		validSkillMD("foo", "v2 with dot content", "v2 body"),
		readLiveRevision(t, appDir))

	// restore V1: the captured .notes is removed — the folder matches V1.
	_, err := ApplyChange(root, ChangeRequest{
		Kind: KindRestore, Slug: "foo", Version: v1,
		Expect: Expectation{Revision: out2.Revision},
		Actor:  ChangeActor{Kind: "agent", AgentID: "a", Tool: "edit_skill"},
	})
	require.NoError(t, err, "restoring V1 passes validation")
	_, statErr := os.Stat(filepath.Join(appDir, ".notes"))
	assert.True(t, errors.Is(statErr, os.ErrNotExist), "restoring V1 removes the dot-content (folder matches V1 byte-exact)")

	// restore V2: the nested dot-file comes back byte-exact.
	_, err = ApplyChange(root, ChangeRequest{
		Kind: KindRestore, Slug: "foo", Version: v2,
		Expect: Expectation{Revision: readLiveRevision(t, appDir)},
		Actor:  ChangeActor{Kind: "agent", AgentID: "a", Tool: "edit_skill"},
	})
	require.NoError(t, err, "restoring V2 passes validation (recorded dot-content is not an authoring path)")
	assert.Equal(t, "secret note\n", readFile(t, filepath.Join(appDir, ".notes", "x.md")),
		"restoring V2 brings the captured nested dot-file back byte-exact")
}

// TestSnapshot_SymlinkCanary (S-5.17, §9.5 canary + open-boundary probe).
// BDD: V2's in-skill symlink restores; V3's outside-target link stores only
// the target text — the canary's bytes appear nowhere in blobs/ and the probe
// records zero open attempts on the canary during capture; restore V3 is
// refused WHOLLY; restore V1 returns the pre-link state.
// Traces to: skills-spec-r15.md §9.5 capture contract + canary oracle,
// §17.1 row 41, S-5.17.
func TestSnapshot_SymlinkCanary(t *testing.T) {
	root := t.TempDir()
	historyRoot := SkillHistoryRoot(root)
	createSkillViaPipeline(t, root, "foo", "v1", "v1 body")

	// External edit 1: in-skill symlink inner.md → SKILL.md; capture V2.
	appDir := filepath.Join(root, "foo")
	require.NoError(t, os.Symlink("SKILL.md", filepath.Join(appDir, "inner.md")))
	_, v2 := commitWrite(t, root, "foo", "SKILL.md",
		validSkillMD("foo", "v2", "v2 body"), readLiveRevision(t, appDir))

	// External edit 2: outside-target symlink link.md → canary; capture V3.
	canary := filepath.Join(t.TempDir(), "canary.txt")
	require.NoError(t, os.WriteFile(canary, []byte("CANARY-BYTES-42"), 0o644))
	require.NoError(t, os.Symlink(canary, filepath.Join(appDir, "link.md")))

	expected := readLiveRevision(t, appDir) // outside the measured capture, so revision lookup cannot satisfy its control
	var canaryOpens, includedCaptureOpens int
	prevProbe := CaptureOpenProbe
	CaptureOpenProbe = func(op, path string) {
		if path == canary {
			canaryOpens++
		}
		if op == "capture" && path == filepath.Join(appDir, "SKILL.md") {
			includedCaptureOpens++
		}
	}
	defer func() { CaptureOpenProbe = prevProbe }()

	out3, v3 := commitWrite(t, root, "foo", "SKILL.md",
		validSkillMD("foo", "v3", "v3 body"), expected)
	require.Greater(t, includedCaptureOpens, 0, "positive control: this same production capture reports an included regular-file open; a disconnected probe must fail")

	// The canary's bytes appear nowhere in the blob store.
	blobsDir := filepath.Join(historyRoot, "foo", "blobs")
	require.DirExists(t, blobsDir, "the real committed capture, not the test, must create its blob store")
	for _, b := range blobNames(t, historyRoot, "foo") {
		raw, err := os.ReadFile(filepath.Join(blobsDir, b))
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "CANARY-BYTES-42",
			"capture never stored the outside target's bytes")
	}
	assert.Equal(t, 0, canaryOpens,
		"open-boundary probe: zero open attempts on the canary during capture (non-reading, not only non-storage)")

	// restore V3 is refused wholly.
	_, err := ApplyChange(root, ChangeRequest{
		Kind: KindRestore, Slug: "foo", Version: v3,
		Expect: Expectation{Revision: out3.Revision},
		Actor:  ChangeActor{Kind: "agent", AgentID: "a", Tool: "edit_skill"},
	})
	require.Error(t, err, "an escaping symlink makes the whole version ineligible")
	assert.Contains(t, err.Error(), "symlink target outside the skill", "§7.2 eligibility reason")
	assert.NotContains(t, readFile(t, filepath.Join(appDir, "SKILL.md")), "v2 body",
		"nothing written — the refused restore restores nothing (inner.md not restored either)")

	// restore V2 materializes the validated in-skill symlink.
	_, err = ApplyChange(root, ChangeRequest{
		Kind: KindRestore, Slug: "foo", Version: v2,
		Expect: Expectation{Revision: readLiveRevision(t, appDir)},
		Actor:  ChangeActor{Kind: "agent", AgentID: "a", Tool: "edit_skill"},
	})
	require.NoError(t, err)
	info, err := os.Lstat(filepath.Join(appDir, "inner.md"))
	require.NoError(t, err)
	assert.Equal(t, os.ModeSymlink, info.Mode()&os.ModeSymlink, "inner.md materialized as a symlink")
	target, err := os.Readlink(filepath.Join(appDir, "inner.md"))
	require.NoError(t, err)
	assert.Equal(t, "SKILL.md", target, "the validated in-skill target text")
}

// TestSnapshot_RetainedNotRestorable (S-5.18).
// BDD: The delete's step-1 commits V2 = external_change of the broken state
// and V3 = delete_skill; restore V2 refused (no valid SKILL.md); restore V3
// refused (delete_skill selection); refusals leave the live state deleted;
// restore V1 rebuilds the pre-break state; recreate-then-delete completes.
// Traces to: skills-spec-r15.md §7.2 restore row, §9.5, §17.1 row 41, S-5.18.
func TestSnapshot_RetainedNotRestorable(t *testing.T) {
	root := t.TempDir()
	historyRoot := SkillHistoryRoot(root)
	_, v1 := createSkillViaPipeline(t, root, "foo", "v1", "v1 body")

	// External edit removes SKILL.md (helpers left → present-but-broken).
	appDir := filepath.Join(root, "foo")
	require.NoError(t, os.WriteFile(filepath.Join(appDir, "helper.md"), []byte("h\n"), 0o644))
	require.NoError(t, os.Remove(filepath.Join(appDir, "SKILL.md")))

	// The delete's expectation is the broken live state's revision (§7.3:
	// the folder is present, so ContentRevision — not revisionForDir —
	// supplies it).
	brokenRev, err := ContentRevision(appDir)
	require.NoError(t, err, "the broken folder is present")

	// The owner deletes: step 1 captures V2 (broken state), then V3 delete_skill.
	_, err = ApplyChange(root, ChangeRequest{
		Kind: KindDelete, Slug: "foo",
		Expect: Expectation{Revision: brokenRev},
		Actor:  ChangeActor{Kind: "agent", AgentID: "a", Tool: "edit_skill"},
	})
	require.NoError(t, err)

	// The on-disk manifest chain is the observable fixture: the kinds
	// identify V2 (the delete's step-1 external_change of the broken state)
	// and V3 (delete_skill).
	manifests := manifestIDs(t, historyRoot, "foo")
	require.Contains(t, manifests, v1, "V1 retained")
	var v2, v3 string
	for _, id := range manifests {
		switch manifestField(t, historyRoot, "foo", id, "kind") {
		case "external_change":
			v2 = id
		case "delete_skill":
			v3 = id
		}
	}
	require.NotEmpty(t, v2, "step 1 recorded the broken state as a version")
	require.NotEmpty(t, v3, "the delete committed a delete_skill manifest")

	_, statErr := os.Stat(appDir)
	assert.True(t, errors.Is(statErr, os.ErrNotExist), "the delete removed the live folder")

	// restore V2 refused: the version carries no valid instruction file.
	_, err = ApplyChange(root, ChangeRequest{
		Kind: KindRestore, Slug: "foo", Version: v2,
		Expect: Expectation{Absent: true},
		Actor:  ChangeActor{Kind: "agent", AgentID: "a", Tool: "edit_skill"},
	})
	require.Error(t, err, "V2 (the broken state) is retained but not restorable")
	assert.Contains(t, err.Error(), "no valid SKILL.md", "§7.2 eligibility reason")

	// restore V3 refused: delete_skill selection.
	_, err = ApplyChange(root, ChangeRequest{
		Kind: KindRestore, Slug: "foo", Version: v3,
		Expect: Expectation{Absent: true},
		Actor:  ChangeActor{Kind: "agent", AgentID: "a", Tool: "edit_skill"},
	})
	require.Error(t, err, "selecting a delete_skill manifest is refused")
	assert.Contains(t, err.Error(),
		"records the deleted state; use edit_skill action=delete to remove the skill",
		"byte-exact §7.2 delete_skill selection refusal")

	// Both refusals leave the live state deleted.
	_, statErr = os.Stat(appDir)
	assert.True(t, errors.Is(statErr, os.ErrNotExist), "the refusals leave the live state deleted")

	// restore V1 rebuilds the pre-break state.
	_, err = ApplyChange(root, ChangeRequest{
		Kind: KindRestore, Slug: "foo", Version: v1,
		Expect: Expectation{Absent: true},
		Actor:  ChangeActor{Kind: "agent", AgentID: "a", Tool: "edit_skill"},
	})
	require.NoError(t, err, "V1 (the last valid state) restores into absence")
	assert.Equal(t, validSkillMD("foo", "v1", "v1 body"), readFile(t, filepath.Join(appDir, "SKILL.md")),
		"V1's exact bytes are back")

	_, err = ApplyChange(root, ChangeRequest{
		Kind: KindDelete, Slug: "foo", Expect: Expectation{Revision: readLiveRevision(t, appDir)},
		Actor: ChangeActor{Kind: "agent", AgentID: "a", Tool: "edit_skill"},
	})
	require.NoError(t, err)
	// Recreate then delete closes the scenario with content valid again.
	outRec, err := ApplyChange(root, ChangeRequest{
		Kind: KindCreate, Slug: "foo",
		Content: []byte(validSkillMD("foo", "recreated", "recreated body")),
		Expect:  Expectation{Absent: true},
		Actor:   ChangeActor{Kind: "agent", AgentID: "a", Tool: "write_file"},
	})
	require.NoError(t, err, "the recreate's parent is the delete_skill head — chain preserved")
	_, err = ApplyChange(root, ChangeRequest{
		Kind: KindDelete, Slug: "foo",
		Expect: Expectation{Revision: outRec.Revision},
		Actor:  ChangeActor{Kind: "agent", AgentID: "a", Tool: "edit_skill"},
	})
	require.NoError(t, err, "the delete removes the recreated folder cleanly")
}

// TestDeleteFile_ResultingInstructionValidity (S-5.19, R6-MAJ-002).
// BDD leg 1: SKILL.md is a symlink to instructions.md; deleting
// instructions.md is refused with the resulting-instruction reason — nothing
// written, revision unchanged, the skill still loadable. BDD leg 2: deleting
// an ordinary helper commits.
// Traces to: skills-spec-r15.md §7.2 delete_file row, §17.1 row 11, S-5.19.
func TestDeleteFile_ResultingInstructionValidity(t *testing.T) {
	t.Run("leg 1 symlinked instruction target deletion refused", func(t *testing.T) {
		root := t.TempDir()
		appDir := filepath.Join(root, "foo")
		require.NoError(t, os.MkdirAll(appDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(appDir, "instructions.md"),
			[]byte(validSkillMD("foo", "via link", "instructions body")), 0o644))
		require.NoError(t, os.Symlink("instructions.md", filepath.Join(appDir, "SKILL.md")))

		before := readLiveRevision(t, appDir)
		_, err := ApplyChange(root, ChangeRequest{
			Kind: KindDeleteFile, Slug: "foo", Path: "instructions.md",
			Expect: Expectation{Revision: before},
			Actor:  ChangeActor{Kind: "agent", AgentID: "a", Tool: "edit_skill"},
		})
		require.Error(t, err, "the path-rules-only check set would have passed this call")
		assert.Contains(t, err.Error(),
			"deleting 'instructions.md' would leave the skill without a valid instruction file",
			"byte-exact §7.2 resulting-instruction refusal")
		assert.FileExists(t, filepath.Join(appDir, "instructions.md"), "nothing removed")
		assert.Equal(t, before, readLiveRevision(t, appDir), "revision unchanged")
	})

	t.Run("leg 2 ordinary helper deletion commits", func(t *testing.T) {
		root := t.TempDir()
		appDir := writeSkillFixture(t, root, "bar")
		require.NoError(t, os.MkdirAll(filepath.Join(appDir, "reference"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(appDir, "reference", "forms.md"), []byte("forms\n"), 0o644))

		out, err := ApplyChange(root, ChangeRequest{
			Kind: KindDeleteFile, Slug: "bar", Path: "reference/forms.md",
			Expect: Expectation{Revision: readLiveRevision(t, appDir)},
			Actor:  ChangeActor{Kind: "agent", AgentID: "a", Tool: "edit_skill"},
		})
		require.NoError(t, err, "the validity check refuses only instruction-breaking deletions")
		assert.Equal(t, PersistenceComplete, out.PersistenceStatus)
		_, statErr := os.Stat(filepath.Join(appDir, "reference", "forms.md"))
		assert.True(t, errors.Is(statErr, os.ErrNotExist))
	})
}

// TestBoundedCapture_MutationRefusedBeforeAnythingChanges (S-5.20 mutation
// legs; the load-degradation legs are the loading-path tests').
// BDD: Given oversized content (per-file +1, folder-total, file-count 1001),
// every mutation — including delete — is refused with the capture-time reason
// before anything changes; the consumed counter stops at the limit + one
// buffer; delete included.
// Traces to: skills-spec-r15.md §7.2 capture-time enforcement, §9.5 bounded
// capture, §17.1 row 41, S-5.20.
func TestBoundedCapture_MutationRefusedBeforeAnythingChanges(t *testing.T) {
	const perFileLimit = 10485760
	const totalLimit = 52428800

	build := func(t *testing.T, oversized func(appDir string)) string {
		t.Helper()
		root := t.TempDir()
		historyRoot := SkillHistoryRoot(root)
		t.Cleanup(func() { _ = historyRoot })
		appDir := writeSkillFixture(t, root, "foo")
		seedHistory(t, historyRoot, "foo", singleManifest(fixtureVersionID(1), "edit"))
		oversized(appDir)
		return root
	}

	t.Run("leg a per-file limit +1", func(t *testing.T) {
		root := build(t, func(appDir string) {
			require.NoError(t, os.WriteFile(filepath.Join(appDir, "data.bin"),
				make([]byte, perFileLimit+1), 0o644))
		})

		dataPath := filepath.Join(root, "foo", "data.bin")
		expected := readLiveRevision(t, filepath.Join(root, "foo"))
		var consumed int64
		prevConsumed := CaptureConsumedProbe
		CaptureConsumedProbe = func(path string, n int64) {
			if path == dataPath && n > consumed {
				consumed = n // running total: retain its maximum, never sum it
			}
		}
		defer func() { CaptureConsumedProbe = prevConsumed }()

		_, err := ApplyChange(root, ChangeRequest{
			Kind: KindEdit, Slug: "foo", Path: "clean.md",
			Content: []byte("clean\n"),
			Expect:  Expectation{Revision: expected},
			Actor:   ChangeActor{Kind: "agent", AgentID: "a", Tool: "write_file"},
		})
		require.Error(t, err, "refused before anything changes")
		assert.True(t, errors.Is(err, ErrSaveCheckFailed))
		assert.Contains(t, err.Error(), "exceeds the per-file limit of 10,485,760 bytes",
			"byte-exact §7.2 per-file capture-time reason")
		assert.NotContains(t, err.Error(), "data.bin\":", "the reason names the offending path")
		assert.Contains(t, err.Error(), "data.bin")
		require.Greater(t, consumed, int64(0), "positive control: ApplyChange's actual data.bin reader must report consumption; a disconnected counter is not bounded-read proof")
		assert.LessOrEqual(t, consumed, int64(perFileLimit+65536),
			"consumed counter stops at the enforced limit plus at most one buffer")
	})

	t.Run("leg b folder total", func(t *testing.T) {
		root := build(t, func(appDir string) {
			for i := 0; i < 6; i++ {
				require.NoError(t, os.WriteFile(filepath.Join(appDir, fmt.Sprintf("bulk%d.bin", i)),
					make([]byte, totalLimit/6+10), 0o644))
			}
		})
		_, err := ApplyChange(root, ChangeRequest{
			Kind: KindEdit, Slug: "foo", Path: "clean.md",
			Content: []byte("clean\n"),
			Expect:  Expectation{Revision: readLiveRevision(t, filepath.Join(root, "foo"))},
			Actor:   ChangeActor{Kind: "agent", AgentID: "a", Tool: "write_file"},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "total size limit of 52,428,800 bytes", "folder-total reason")
	})

	t.Run("leg c file count 1001", func(t *testing.T) {
		root := build(t, func(appDir string) {
			for i := 0; i < 1000; i++ {
				require.NoError(t, os.WriteFile(filepath.Join(appDir, fmt.Sprintf("f%04d.txt", i)), []byte("x"), 0o644))
			}
		})
		_, err := ApplyChange(root, ChangeRequest{
			Kind: KindEdit, Slug: "foo", Path: "clean.md",
			Content: []byte("clean\n"),
			Expect:  Expectation{Revision: readLiveRevision(t, filepath.Join(root, "foo"))},
			Actor:   ChangeActor{Kind: "agent", AgentID: "a", Tool: "write_file"},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "more than 1,000 files", "file-count reason")
	})

	t.Run("delete refused by the same rule", func(t *testing.T) {
		root := build(t, func(appDir string) {
			require.NoError(t, os.WriteFile(filepath.Join(appDir, "data.bin"),
				make([]byte, perFileLimit+1), 0o644))
		})
		_, err := ApplyChange(root, ChangeRequest{
			Kind: KindDelete, Slug: "foo",
			Expect: Expectation{Revision: readLiveRevision(t, filepath.Join(root, "foo"))},
			Actor:  ChangeActor{Kind: "agent", AgentID: "a", Tool: "edit_skill"},
		})
		require.Error(t, err, "deletion never silently drops content Omnipus cannot checkpoint")
		assert.True(t, errors.Is(err, ErrSaveCheckFailed))
		assert.DirExists(t, filepath.Join(root, "foo"), "nothing changed — live folder intact")
	})
}

// TestExecBitRevisionFixture (D-9, R8-MIN-002).
// BDD: Two trees identical in paths, sizes and bytes with one regular file's
// executable bit flipped → different content revisions from the proposed
// contract (the pinned tree hash is NOT an oracle: it hashes entry types,
// never permission bits).
// Traces to: skills-spec-r15.md §7.3 contentRevision, D-9 (r9 fixture).
func TestExecBitRevisionFixture(t *testing.T) {
	buildTree := func(t *testing.T, exec bool) string {
		t.Helper()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"),
			[]byte(validSkillMD("foo", "same", "same body")), 0o644))
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "scripts"), 0o755), "seed the scripts dir")
		require.NoError(t, os.WriteFile(filepath.Join(dir, "scripts", "run.sh"), []byte("#!/bin/sh\n"), 0o644))
		if exec {
			require.NoError(t, os.Chmod(filepath.Join(dir, "scripts", "run.sh"), 0o755))
		}
		return dir
	}

	revNoExec, err := ContentRevision(buildTree(t, false))
	require.NoError(t, err, "RED: ContentRevision not implemented")
	revExec, err := ContentRevision(buildTree(t, true))
	require.NoError(t, err)

	require.NotEmpty(t, revNoExec)
	require.NotEmpty(t, revExec)
	assert.NotEqual(t, revNoExec, revExec,
		"the proposed contentRevision differs on an exec-bit-only change (V-45: the pinned hasher is not an oracle for this)")
}
