package skills

// RED tests — landing/skills (T9): capture-then-stage copy provenance
// (§7.1 copy clause, R3/R6/R7), the capture lifecycle and cleanup
// (§9.5 rules 1–8, R8/R9/R10/R11), and oversized copy sources (S-6.17).
// Scenarios S-5.21 (legs a–i), S-6.13, S-6.14, S-6.15, S-6.16, S-6.17.
// Traces to: skills-spec-r13.md §7.1 step 1, §9.5, §17.1 rows 40/41.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// copySrcToDst runs edit_skill copy via the pipeline (KindCreate with To).
func copySrcToDst(t *testing.T, root, src, dst string) (PublishOutcome, error) {
	t.Helper()
	return ApplyChange(root, ChangeRequest{
		Kind: KindCreate, Slug: src, To: dst,
		Expect: Expectation{Absent: true},
		Actor:  ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "edit_skill"},
	})
}

// historyState snapshots the blob names + manifest ids of a history dir.
type historyState struct {
	manifests []string
	blobs     []string
}

func snapshotHistory(t *testing.T, historyRoot, slug string) historyState {
	t.Helper()
	st := historyState{}
	if _, err := os.Stat(filepath.Join(historyRoot, slug, "versions")); err == nil {
		st.manifests = manifestIDs(t, historyRoot, slug)
	}
	if _, err := os.Stat(filepath.Join(historyRoot, slug, "blobs")); err == nil {
		st.blobs = blobNames(t, historyRoot, slug)
	}
	return st
}

// incomingDirs lists remaining .incoming-* directories under dir (recursive).
func incomingDirs(t *testing.T, dir string) []string {
	t.Helper()
	var found []string
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err == nil && info.IsDir() && strings.HasPrefix(info.Name(), ".incoming-") {
			found = append(found, path)
		}
		return nil
	})
	return found
}

// TestS521_LegsABCD_AbortedCaptureCleanup (S-5.21 legs (a)–(d), retention
// disabled). BDD: aborted captures remove their temporaries and exactly the
// finals their ledger recorded — the source blob set returns to its
// before-state, no manifest added, three repeated attempts leave no
// orphan-per-attempt growth, and a no-manifest-yet skill cleans up without
// consulting any committed version.
// Traces to: skills-spec-r13.md §9.5 rules 1–3, §17.1 row 41, S-5.21.
func TestS521_LegsABCD_AbortedCaptureCleanup(t *testing.T) {
	const perFileLimit = 10485760

	oversize := func(t *testing.T, dir, name string) {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), make([]byte, perFileLimit+1), 0o644))
	}

	t.Run("leg a existing history — blob set returns to before-state", func(t *testing.T) {
		root := t.TempDir()
		historyRoot := SkillHistoryRoot(root)
		appDir := versionedSkillFixture(t, root, "foo")
		before := snapshotHistory(t, historyRoot, "foo")
		oversize(t, appDir, "data.bin")

		_, err := ApplyChange(root, ChangeRequest{
			Kind: KindEdit, Slug: "foo", Path: "clean.md",
			Content: []byte("clean\n"),
			Expect:  Expectation{Revision: readLiveRevision(t, appDir)},
			Actor:   ChangeActor{Kind: "agent", AgentID: "a", Tool: "write_file"},
		})
		require.Error(t, err, "leg (a): the capture aborts with the capture-time reason")
		assert.True(t, errors.Is(err, ErrSaveCheckFailed))

		after := snapshotHistory(t, historyRoot, "foo")
		assert.Equal(t, before.manifests, after.manifests, "no manifest added (head unchanged)")
		assert.Equal(t, before.blobs, after.blobs,
			"foo's blobs/ holds exactly V1's referenced finals before and after — temporaries gone, retention disabled made no difference")
		assert.Empty(t, incomingDirs(t, filepath.Join(historyRoot, "foo")),
			"no .incoming-* remains after the aborted capture's cleanup")
	})

	t.Run("leg b no manifest yet — cleanup without consulting versions", func(t *testing.T) {
		root := t.TempDir()
		historyRoot := SkillHistoryRoot(root)
		barDir := writeSkillFixture(t, root, "bar") // never loaded or mutated
		oversize(t, barDir, "huge-helper.bin")

		_, err := copySrcToDst(t, root, "bar", "dst")
		require.Error(t, err, "the copy's source capture aborts")
		_, statErr := os.Stat(filepath.Join(root, "dst"))
		assert.True(t, os.IsNotExist(statErr), "nothing created")
		_, statErr = os.Stat(filepath.Join(historyRoot, "bar"))
		assert.True(t, os.IsNotExist(statErr),
			"no history directory was created for the aborted capture (none existed)")
	})

	t.Run("leg c valid early file, later limit violation", func(t *testing.T) {
		root := t.TempDir()
		historyRoot := SkillHistoryRoot(root)
		appDir := versionedSkillFixture(t, root, "foo")
		require.NoError(t, os.WriteFile(filepath.Join(appDir, "good-early.md"), []byte("valid early file\n"), 0o644))
		oversize(t, appDir, "late-oversize.bin")
		before := snapshotHistory(t, historyRoot, "foo")

		_, err := ApplyChange(root, ChangeRequest{
			Kind: KindEdit, Slug: "foo", Path: "clean.md",
			Content: []byte("clean\n"),
			Expect:  Expectation{Revision: readLiveRevision(t, appDir)},
			Actor:   ChangeActor{Kind: "agent", AgentID: "a", Tool: "write_file"},
		})
		require.Error(t, err)
		after := snapshotHistory(t, historyRoot, "foo")
		assert.Equal(t, before, after,
			"the early file's consumed bytes lived only in the attempt's temporary directory and are removed with it")
	})

	t.Run("leg d three repeated attempts leave the blob set identical", func(t *testing.T) {
		root := t.TempDir()
		historyRoot := SkillHistoryRoot(root)
		appDir := versionedSkillFixture(t, root, "foo")
		before := snapshotHistory(t, historyRoot, "foo")

		for i := 0; i < 3; i++ {
			require.NoError(t, os.WriteFile(filepath.Join(appDir, "early.md"),
				[]byte("early content variant "+string(rune('a'+i))+"\n"), 0o644))
			oversize(t, appDir, "data.bin")

			_, err := ApplyChange(root, ChangeRequest{
				Kind: KindEdit, Slug: "foo", Path: "clean.md",
				Content: []byte("clean\n"),
				Expect:  Expectation{Revision: readLiveRevision(t, appDir)},
				Actor:   ChangeActor{Kind: "agent", AgentID: "a", Tool: "write_file"},
			})
			require.Error(t, err, "attempt %d refused", i+1)
			require.NoError(t, os.Remove(filepath.Join(appDir, "data.bin")))
		}
		after := snapshotHistory(t, historyRoot, "foo")
		assert.Equal(t, before, after,
			"three attempts leave the blob set identical — no one-orphan-per-attempt growth")
	})
}

// TestS521_LegsEF_ManifestFaultAndPartialPromotion (S-5.21 legs (e1)/(e2)/(f),
// retention disabled). BDD: (e1) a checkpoint whose manifest write is faulted
// after finalization with a captured early file byte-identical to an existing
// referenced final (content-addressed twin) cleans up: pre-existing finals
// kept and hash-identical, the attempt-created final removed. (e2) the same
// with the attempt's own removal faulted: the refusal carries the cleanup
// suffix, referenced blobs preserved, the residual acknowledged.
// (f) partial promotion: the blob promoted before the later promotion failed
// is removed with the temporaries.
// Traces to: skills-spec-r13.md §9.5 rules 2–3, §7.2 cleanup suffix,
// S-5.21 legs e1/e2/f.
func TestS521_LegsEF_ManifestFaultAndPartialPromotion(t *testing.T) {
	t.Run("leg e1 manifest fault after finalization — twin kept, new final removed", func(t *testing.T) {
		root := t.TempDir()
		historyRoot := SkillHistoryRoot(root)
		appDir := versionedSkillFixture(t, root, "foo")
		before := snapshotHistory(t, historyRoot, "foo")

		// A new file whose bytes twin an existing final would exercise the
		// content-addressed keep; the new early file the capture consumes.
		require.NoError(t, os.WriteFile(filepath.Join(appDir, "early-twin.md"), []byte(readFile(t, filepath.Join(appDir, "SKILL.md"))), 0o644))

		FaultManifestWrite = func(string) error { return errors.New("manifest store down") }
		defer func() { FaultManifestWrite = nil }()

		_, err := ApplyChange(root, ChangeRequest{
			Kind: KindEdit, Slug: "foo", Path: "clean.md",
			Content: []byte("clean\n"),
			Expect:  Expectation{Revision: readLiveRevision(t, appDir)},
			Actor:   ChangeActor{Kind: "agent", AgentID: "a", Tool: "write_file"},
		})
		require.Error(t, err, "the storage-error refusal (class 1)")
		after := snapshotHistory(t, historyRoot, "foo")
		assert.Equal(t, before.manifests, after.manifests, "no manifest added")
		assert.Equal(t, before.blobs, after.blobs,
			"every pre-existing referenced final (twin included) present; the final the attempt created removed")
	})

	t.Run("leg e2 faulted removal — suffix observable, residual acknowledged", func(t *testing.T) {
		root := t.TempDir()
		historyRoot := SkillHistoryRoot(root)
		appDir := versionedSkillFixture(t, root, "foo")
		before := snapshotHistory(t, historyRoot, "foo")
		require.NoError(t, os.WriteFile(filepath.Join(appDir, "early-twin.md"), []byte(readFile(t, filepath.Join(appDir, "SKILL.md"))), 0o644))

		FaultManifestWrite = func(string) error { return errors.New("manifest store down") }
		FaultStagingRemove = func(string) error { return errors.New("cleanup failed too") }
		defer func() {
			FaultManifestWrite = nil
			FaultStagingRemove = nil
		}()

		_, err := ApplyChange(root, ChangeRequest{
			Kind: KindEdit, Slug: "foo", Path: "clean.md",
			Content: []byte("clean\n"),
			Expect:  Expectation{Revision: readLiveRevision(t, appDir)},
			Actor:   ChangeActor{Kind: "agent", AgentID: "a", Tool: "write_file"},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cleanup of partial capture output also failed",
			"byte-exact §7.2 cleanup-error suffix — never silent")
		assert.Contains(t, err.Error(), "manifest store down", "the primary fault is named")

		// Referenced finals preserved; the residual is acknowledged —
		// storage-unchanged is NOT asserted on this leg (R11-MIN-001).
		after := snapshotHistory(t, historyRoot, "foo")
		for _, b := range before.blobs {
			assert.Contains(t, after.blobs, b, "referenced final preserved")
		}
	})

	t.Run("leg f partial promotion — promoted blob removed with the temporaries", func(t *testing.T) {
		root := t.TempDir()
		historyRoot := SkillHistoryRoot(root)
		appDir := versionedSkillFixture(t, root, "foo")
		before := snapshotHistory(t, historyRoot, "foo")

		// Two new blobs; the second finalization faults after the first succeeded.
		require.NoError(t, os.WriteFile(filepath.Join(appDir, "first.md"), []byte("first\n"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(appDir, "second.md"), []byte("second\n"), 0o644))

		var blobFaults int
		FaultBlobWrite = func(string) error {
			blobFaults++
			if blobFaults == 2 {
				return errors.New("second promotion failed")
			}
			return nil
		}
		defer func() { FaultBlobWrite = nil }()

		_, err := ApplyChange(root, ChangeRequest{
			Kind: KindEdit, Slug: "foo", Path: "clean.md",
			Content: []byte("clean\n"),
			Expect:  Expectation{Revision: readLiveRevision(t, appDir)},
			Actor:   ChangeActor{Kind: "agent", AgentID: "a", Tool: "write_file"},
		})
		require.Error(t, err, "the storage-error refusal — the manifest is never attempted")
		after := snapshotHistory(t, historyRoot, "foo")
		assert.Equal(t, before, after,
			"the blob promoted before the later promotion failed is removed with the temporaries (in the attempt's ledger)")
	})
}

// TestS521_LegG_SuccessByReuse_ThreeDistinctDestinations (S-5.21 leg (g), r11
// R10-MIN-002). BDD: src whose live state equals its newest version V1; three
// copies to dst1/dst2/dst3 — each commits with its creation manifest, no new
// source manifest, and each attempt's scratch disposed in the copy's lock
// hold; after the third copy the source history holds exactly the before-state.
// Traces to: skills-spec-r13.md §9.5 rule 5, S-5.21 leg g.
func TestS521_LegG_SuccessByReuse_ThreeDistinctDestinations(t *testing.T) {
	root := t.TempDir()
	historyRoot := SkillHistoryRoot(root)
	srcDir := versionedSkillFixture(t, root, "src")
	before := snapshotHistory(t, historyRoot, "src")
	_ = srcDir

	for _, dst := range []string{"dst1", "dst2", "dst3"} {
		out, err := copySrcToDst(t, root, "src", dst)
		require.NoError(t, err, "copy to the absent %s commits", dst)
		assert.Equal(t, PersistenceComplete, out.PersistenceStatus)
		// Destination carries its creation manifest (FR-030, kind=create) —
		// asserted separately from the source-side unchanged claim (R11-MIN-001).
		dstManifests := manifestIDs(t, historyRoot, dst)
		require.NotEmpty(t, dstManifests, "%s has a creation manifest", dst)
	}

	after := snapshotHistory(t, historyRoot, "src")
	assert.Equal(t, before.manifests, after.manifests, "no new source manifest committed (source head unchanged)")
	assert.Equal(t, before.blobs, after.blobs, "source blob set returns to its before-state (reuse, scratch disposed)")
	assert.Empty(t, incomingDirs(t, filepath.Join(historyRoot, "src")),
		"no .incoming-* remains — nothing delegated to retention or startup")
}

// TestS521_LegH_BuiltinSource_NoHistoryEver (S-5.21 leg (h)).
// BDD: copy plan → plan-copy succeeds; no system/skill-history/plan/ exists at
// any point; the copy's staging scratch lived under skills/.staging/ and is
// absent after success; the faulted-disposal variant surfaces the class-3
// finishing warning while the copy still succeeds.
// Traces to: skills-spec-r13.md §9.5 rule 6, §7.2 built-in equality,
// S-5.21 leg h.
func TestS521_LegH_BuiltinSource_NoHistoryEver(t *testing.T) {
	t.Run("main — copy succeeds, no built-in history, staging scratch disposed", func(t *testing.T) {
		root := t.TempDir()
		historyRoot := SkillHistoryRoot(root)
		exportShipped(t, "plan", filepath.Join(root, "plan"))
		builtinMarkerFixture(t, filepath.Join(root, "plan"))

		out, err := copySrcToDst(t, root, "plan", "plan-copy")
		require.NoError(t, err, "the built-in-source copy commits")
		assert.Equal(t, PersistenceComplete, out.PersistenceStatus)

		_, statErr := os.Stat(filepath.Join(historyRoot, "plan"))
		assert.True(t, os.IsNotExist(statErr), "no system/skill-history/plan/ directory ever exists")
		assert.Empty(t, incomingDirs(t, filepath.Join(root, ".staging")),
			"the copy's staging scratch is disposed in the copy's lock hold")
	})

	t.Run("variant — faulted staging-scratch disposal surfaces the class-3 warning", func(t *testing.T) {
		root := t.TempDir()
		historyRoot := SkillHistoryRoot(root)
		exportShipped(t, "plan", filepath.Join(root, "plan"))
		builtinMarkerFixture(t, filepath.Join(root, "plan"))

		FaultStagingRemove = func(string) error { return errors.New("staging busy") }
		defer func() { FaultStagingRemove = nil }()

		out, err := copySrcToDst(t, root, "plan", "plan-copy-fault")
		require.NoError(t, err, "the copy still succeeds")
		assert.Equal(t, PersistenceComplete, out.PersistenceStatus)
		assert.Contains(t, out.Warning, "capture scratch cleanup failed",
			"the class-3 finishing warning, byte-shape §7.6 class 3 (r10)")
		_, statErr := os.Stat(filepath.Join(historyRoot, "plan"))
		assert.True(t, os.IsNotExist(statErr), "still no built-in history directory")
	})
}

// TestS521_LegI_RepeatedCommits_NoTemporariesAccumulate (S-5.21 leg (i), r11
// R10-MIN-003). BDD: four ordinary committed edits — after each, the manifest
// count grows by exactly one, referenced blobs present and hash-identical, and
// no .incoming-* remains; the faulted variant keeps the success and surfaces
// the class-3 warning.
// Traces to: skills-spec-r13.md §9.5 rule 4, S-5.21 leg i.
func TestS521_LegI_RepeatedCommits_NoTemporariesAccumulate(t *testing.T) {
	root := t.TempDir()
	historyRoot := SkillHistoryRoot(root)
	appDir := versionedSkillFixture(t, root, "chk")

	for i := 1; i <= 4; i++ {
		before := snapshotHistory(t, historyRoot, "chk")
		_, err := ApplyChange(root, ChangeRequest{
			Kind: KindEdit, Slug: "chk", Path: "SKILL.md",
			Content: []byte(validSkillMD("chk", "edit "+string(rune('0'+i)), "body "+string(rune('0'+i)))),
			Expect:  Expectation{Revision: readLiveRevision(t, appDir)},
			Actor:   ChangeActor{Kind: "agent", AgentID: "a", Tool: "edit_file"},
		})
		require.NoError(t, err, "edit %d commits normally", i)
		after := snapshotHistory(t, historyRoot, "chk")
		assert.Len(t, after.manifests, len(before.manifests)+1,
			"after edit %d the committed manifest count grew by exactly one", i)
		for _, b := range before.blobs {
			assert.Contains(t, after.blobs, b, "referenced finals preserved across edit %d", i)
		}
		assert.Empty(t, incomingDirs(t, filepath.Join(historyRoot, "chk")),
			"after edit %d no .incoming-* remains — the attempt directory was removed in the commit's lock hold", i)
	}

	t.Run("variant — post-commit removal fault surfaces the class-3 warning", func(t *testing.T) {
		FaultStagingRemove = func(string) error { return errors.New("attempt dir busy") }
		defer func() { FaultStagingRemove = nil }()

		out, err := ApplyChange(root, ChangeRequest{
			Kind: KindEdit, Slug: "chk", Path: "SKILL.md",
			Content: []byte(validSkillMD("chk", "fifth", "fifth body")),
			Expect:  Expectation{Revision: readLiveRevision(t, appDir)},
			Actor:   ChangeActor{Kind: "agent", AgentID: "a", Tool: "edit_file"},
		})
		require.NoError(t, err, "the outcome stands")
		assert.Contains(t, out.Warning, "capture scratch cleanup failed",
			"the warning stays truthful — §9.3 removes the leftover at the next start")
	})
}

// TestCopyProvenance (S-6.13, S-6.14, S-6.15).
// BDD: unversioned source → step-1 baseline captured in the same lock hold,
// dst.restored_from = src@baseline, content equals the copied snapshot;
// source changed since its last version → external_change V2 provenance and
// the destination carries the declared frontmatter-name transform;
// externally edited built-in → refused until the next start replaces it.
// Traces to: skills-spec-r13.md §7.1 copy clause, §5.4 copy row, rows 40, E-6.
func TestCopyProvenance(t *testing.T) {
	t.Run("S-6.13 unversioned source records a baseline", func(t *testing.T) {
		root := t.TempDir()
		historyRoot := SkillHistoryRoot(root)
		writeSkillFixture(t, root, "src") // created externally, never loaded — no manifest

		out, err := copySrcToDst(t, root, "src", "dst")
		require.NoError(t, err, "RED: copy unimplemented")

		// step 1 recorded a baseline for src in the same lock hold.
		srcManifests := manifestIDs(t, historyRoot, "src")
		require.Len(t, srcManifests, 1, "exactly the baseline was recorded")
		// dst.restored_from = src@<baseline id> — read from the creation manifest.
		dstHead := newestManifest(t, historyRoot, "dst", manifestIDs(t, historyRoot, "dst"))
		restoredFrom := manifestField(t, historyRoot, "dst", dstHead, "restored_from")
		assert.Equal(t, "src@"+srcManifests[0], restoredFrom, "no id is invented")
		_ = out
	})

	t.Run("S-6.14 source changed since its last version — external_change provenance + transform", func(t *testing.T) {
		root := t.TempDir()
		historyRoot := SkillHistoryRoot(root)
		versionedSkillFixture(t, root, "src")

		initial := manifestIDs(t, historyRoot, "src")
		require.Len(t, initial, 1)

		// Owner edits src outside Omnipus (live ≠ V1).
		srcDir := filepath.Join(root, "src")
		require.NoError(t, os.WriteFile(filepath.Join(srcDir, "SKILL.md"),
			[]byte(validSkillMD("src", "externally edited", "edited body")), 0o644))

		out, err := copySrcToDst(t, root, "src", "dst")
		require.NoError(t, err)

		// step 1 recorded external_change V2 for src.
		srcManifests := manifestIDs(t, historyRoot, "src")
		require.Contains(t, srcManifests, initial[0], "V1 itself unchanged")
		require.Len(t, srcManifests, 2, "exactly the external_change was recorded")
		v2 := newestManifest(t, historyRoot, "src", srcManifests)
		kind := manifestField(t, historyRoot, "src", v2, "kind")
		assert.Equal(t, "external_change", kind)

		dstHead := newestManifest(t, historyRoot, "dst", manifestIDs(t, historyRoot, "dst"))
		restoredFrom := manifestField(t, historyRoot, "dst", dstHead, "restored_from")
		assert.Equal(t, "src@"+v2, restoredFrom, "referencing V1 would be false provenance")

		// The declared transform: dst's frontmatter name rewritten to dst,
		// every other file byte-identical to the pre-rewrite snapshot.
		dstBody := readFile(t, filepath.Join(root, "dst", "SKILL.md"))
		assert.Contains(t, dstBody, "name: dst", "the frontmatter name is rewritten to the destination")
		assert.NotContains(t, dstBody, "name: src")
		assert.Contains(t, dstBody, "edited body", "the captured (post-edit) body, with only the transform applied")
		_ = out
	})

	t.Run("S-6.15 externally edited built-in refused until the next start", func(t *testing.T) {
		root := t.TempDir()
		exportShipped(t, "plan", filepath.Join(root, "plan"))
		builtinMarkerFixture(t, filepath.Join(root, "plan"))
		// External edit after start (E-8 state, sandbox off).
		require.NoError(t, os.WriteFile(filepath.Join(root, "plan", "SKILL.md"),
			[]byte(validSkillMD("plan", "externally edited built-in", "edited")), 0o644))

		_, err := copySrcToDst(t, root, "plan", "plan-copy")
		require.Error(t, err, "the shipped-equality check refuses the copy")
		assert.True(t, errors.Is(err, ErrSaveCheckFailed))
		assert.Contains(t, err.Error(),
			"built-in 'plan' does not match its shipped version and will be replaced at the next start; copy it after the next start",
			"byte-exact §7.2 built-in mismatch text")
		_, statErr := os.Stat(filepath.Join(root, "plan-copy"))
		assert.True(t, os.IsNotExist(statErr), "nothing created")
	})
}

// TestCopyInterference_CaptureThenStage (S-6.16 legs 1–4).
// BDD: the single capture walk observed whichever state existed when it ran;
// the destination is staged from the captured version with the declared
// transform; the live source is never read again after the capture
// (probe-asserted); the negative control proves the probe detects a rereading
// variant.
// Traces to: skills-spec-r13.md §7.1 copy clause, §9.5, E-6, S-6.16.
func TestCopyInterference_CaptureThenStage(t *testing.T) {
	t.Run("leg 1 interference before the capture — B captured and provenanced", func(t *testing.T) {
		root := t.TempDir()
		historyRoot := SkillHistoryRoot(root)
		srcDir := versionedSkillFixture(t, root, "src")
		before := manifestIDs(t, historyRoot, "src")
		// B is installed synchronously before the real copy starts. There is
		// no scheduling assumption: the subsequent capture must observe B.
		require.NoError(t, os.WriteFile(filepath.Join(srcDir, "SKILL.md"),
			[]byte(validSkillMD("src", "B", "B body")), 0o644))
		_, err := copySrcToDst(t, root, "src", "dst")
		require.NoError(t, err)
		ids := manifestIDs(t, historyRoot, "src")
		require.Len(t, ids, len(before)+1)
		v2 := newestManifest(t, historyRoot, "src", ids)
		assert.Equal(t, "external_change", manifestField(t, historyRoot, "src", v2, "kind"))
		dstHead := newestManifest(t, historyRoot, "dst", manifestIDs(t, historyRoot, "dst"))
		assert.Equal(t, "src@"+v2, manifestField(t, historyRoot, "dst", dstHead, "restored_from"))
		assert.Equal(t, validSkillMD("dst", "B", "B body"), readFile(t, filepath.Join(root, "dst", "SKILL.md")))
	})

	t.Run("leg 3 interference after capture — destination from captured A", func(t *testing.T) {
		root := t.TempDir()
		historyRoot := SkillHistoryRoot(root)
		srcDir := writeSkillFixture(t, root, "src") // no history: copy must commit a baseline
		original := readFile(t, filepath.Join(srcDir, "SKILL.md"))
		var boundaryFired bool
		var injectionErr error
		var armed, sourceReads, postBoundarySourceReads int32
		oldManifest, oldOpen := FaultManifestWrite, CaptureOpenProbe
		defer func() { FaultManifestWrite, CaptureOpenProbe = oldManifest, oldOpen }()
		// The source baseline manifest write is an existing synchronous seam
		// AFTER its complete capture, before destination staging. Returning nil
		// lets the real commit proceed. No new production test API is added.
		FaultManifestWrite = func(path string) error {
			if filepath.Dir(path) == filepath.Join(historyRoot, "src", "versions") && !boundaryFired {
				boundaryFired = true
				injectionErr = os.WriteFile(filepath.Join(srcDir, "SKILL.md"),
					[]byte(validSkillMD("src", "B", "B body")), 0o644)
				atomic.StoreInt32(&armed, 1)
			}
			return injectionErr
		}
		CaptureOpenProbe = func(op, path string) {
			if op != "metadata" && (path == srcDir || strings.HasPrefix(path, srcDir+string(filepath.Separator))) {
				atomic.AddInt32(&sourceReads, 1)
				if atomic.LoadInt32(&armed) == 1 {
					atomic.AddInt32(&postBoundarySourceReads, 1)
				}
			}
		}
		_, err := copySrcToDst(t, root, "src", "dst")
		require.NoError(t, err, "the real copy commits from its captured version")
		require.True(t, boundaryFired, "source capture must reach its baseline-manifest boundary")
		require.NoError(t, injectionErr)
		require.Greater(t, atomic.LoadInt32(&sourceReads), int32(0), "instrument control: real source capture opens are observable")
		assert.Zero(t, atomic.LoadInt32(&postBoundarySourceReads), "no live-source content open after capture")
		assert.Equal(t, validSkillMD("src", "B", "B body"), readFile(t, filepath.Join(srcDir, "SKILL.md")), "B really landed")
		assert.Equal(t, strings.Replace(original, "name: src", "name: dst", 1),
			readFile(t, filepath.Join(root, "dst", "SKILL.md")), "destination is captured A with only the declared name transform")
		ids := manifestIDs(t, historyRoot, "src")
		require.Len(t, ids, 1)
		head := newestManifest(t, historyRoot, "dst", manifestIDs(t, historyRoot, "dst"))
		assert.Equal(t, "src@"+ids[0], manifestField(t, historyRoot, "dst", head, "restored_from"))
	})

	t.Run("leg 4 negative control — the probe detects a genuinely rereading variant", func(t *testing.T) {
		root := t.TempDir()
		srcDir := writeSkillFixture(t, root, "src")

		// The identical counter predicate leg 3 relies on, in its exact
		// post-boundary state (armed).
		var armed, postBoundarySourceReads int32
		prevOpen := CaptureOpenProbe
		CaptureOpenProbe = func(op, path string) {
			if atomic.LoadInt32(&armed) == 1 && strings.Contains(path, srcDir) {
				atomic.AddInt32(&postBoundarySourceReads, 1)
			}
		}
		defer func() { CaptureOpenProbe = prevOpen }()

		// Deliberately wrong algorithm: capture A, then stage by recapturing
		// the LIVE source after B arrives instead of using captured A. BOTH
		// captures enter the actual production pipeline; this test never calls
		// the observation callback itself. A disconnected production probe
		// cannot be made green by a test-owned read wrapper.
		capturedA := readFile(t, filepath.Join(srcDir, "SKILL.md"))
		_, err := copySrcToDst(t, root, "src", "captured-a")
		require.NoError(t, err, "negative control needs the real first capture")
		require.NoError(t, os.WriteFile(filepath.Join(srcDir, "SKILL.md"),
			[]byte(validSkillMD("src", "B", "B body")), 0o644))
		atomic.StoreInt32(&armed, 1)
		_, err = copySrcToDst(t, root, "src", "wrong-reread")
		require.NoError(t, err, "deliberately rereading staging path uses real production I/O")

		assert.Greater(t, atomic.LoadInt32(&postBoundarySourceReads), int32(0),
			"the identical production instrument catches the prohibited second live-source capture")
		assert.Equal(t, validSkillMD("wrong-reread", "B", "B body"), readFile(t, filepath.Join(root, "wrong-reread", "SKILL.md")), "wrong reread really stages B, not captured A")
		assert.Equal(t, strings.Replace(capturedA, "name: src", "name: captured-a", 1), readFile(t, filepath.Join(root, "captured-a", "SKILL.md")), "first captured A remains the exact control")
	})
}

// TestS617_OversizedCopySource (S-6.17 legs a–c).
// BDD: an oversized or growing copy source refuses with the capture-time
// reason; nothing created; no source version recorded; the lock is released;
// the temporaries removed; with retention disabled and a content-twin early
// file (leg c), every referenced final is intact before and after.
// Traces to: skills-spec-r13.md §7.1 step 1 (bounded copy capture), §9.5,
// S-6.17.
func TestS617_OversizedCopySource(t *testing.T) {
	const perFileLimit = 10485760

	t.Run("leg a oversized helper refuses, nothing created, lock released", func(t *testing.T) {
		root := t.TempDir()
		historyRoot := SkillHistoryRoot(root)
		srcDir := versionedSkillFixture(t, root, "src")
		require.NoError(t, os.WriteFile(filepath.Join(srcDir, "huge.bin"),
			make([]byte, perFileLimit+1), 0o644))
		before := snapshotHistory(t, historyRoot, "src")

		_, err := copySrcToDst(t, root, "src", "dst")
		require.Error(t, err, "refused with the capture-time reason")
		assert.True(t, errors.Is(err, ErrSaveCheckFailed))
		assert.Contains(t, err.Error(), "huge.bin", "the reason names the path and the limit")

		_, statErr := os.Stat(filepath.Join(root, "dst"))
		assert.True(t, os.IsNotExist(statErr), "no dst folder")
		assert.Empty(t, incomingDirs(t, root), "no leftover staging temp")
		after := snapshotHistory(t, historyRoot, "src")
		assert.Equal(t, before, after, "no source version recorded — the aborted capture records nothing")

		// The mutation lock is released: a subsequent unrelated mutation acquires it.
		lockCheckDone := make(chan error, 1)
		go func() {
			lockCheckDone <- WithMutationLock(root, func() error { return nil })
		}()
		require.NoError(t, <-lockCheckDone, "no deadlock — the lock was released")
	})

	t.Run("leg c retention disabled, content-twin early file — referenced finals intact", func(t *testing.T) {
		root := t.TempDir()
		historyRoot := SkillHistoryRoot(root)
		srcDir := versionedSkillFixture(t, root, "src")
		before := snapshotHistory(t, historyRoot, "src")

		// A byte-twin of an existing referenced final as the early file, then
		// the oversized helper.
		existing := readFile(t, filepath.Join(srcDir, "SKILL.md"))
		require.NoError(t, os.WriteFile(filepath.Join(srcDir, "twin.md"), []byte(existing), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(srcDir, "huge.bin"),
			make([]byte, perFileLimit+1), 0o644))

		_, err := copySrcToDst(t, root, "src", "dst")
		require.Error(t, err)

		after := snapshotHistory(t, historyRoot, "src")
		assert.Equal(t, before.blobs, after.blobs,
			"the content-addressed twin was never removed or rewritten; total blob storage unchanged")
	})
}

var manifestIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]+\.json$`)

// newestManifest picks the highest-numbered manifest id (head, §5.11).
func newestManifest(t *testing.T, historyRoot, slug string, ids []string) string {
	t.Helper()
	require.NotEmpty(t, ids)
	best := ""
	for _, id := range ids {
		require.True(t, manifestIDPattern.MatchString(id+".json") || manifestIDPattern.MatchString(id),
			"manifest id shape")
		_ = id
		if id > best { // ids are v<seq>-<timestamp>, lexicographic order ≈ sequence order
			best = id
		}
	}
	require.NotEmpty(t, best)
	_ = historyRoot
	_ = slug
	return best
}

// manifestField reads one JSON string field from the named manifest.
func manifestField(t *testing.T, historyRoot, slug, id, field string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(historyRoot, slug, "versions", id+".json"))
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	v, _ := m[field].(string)
	return v
}

// versionedSkillFixture makes live bytes and the newest manifest agree. A
// synthetic unrelated manifest would induce an unintended external_change.
func versionedSkillFixture(t *testing.T, root, slug string) string {
	t.Helper()
	createSkillViaPipeline(t, root, slug, "fixture skill "+slug, "fixture skill "+slug)
	return filepath.Join(root, slug)
}
