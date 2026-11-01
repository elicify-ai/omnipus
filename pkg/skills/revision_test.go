package skills

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createAuthoredSkill keeps registry fixtures on the real r15 §7.1 pipeline.
func createAuthoredSkill(t *testing.T, root, slug, content string) PublishOutcome {
	t.Helper()
	out, err := ApplyChange(root, ChangeRequest{Kind: KindCreate, Slug: slug,
		Content: []byte(content), Expect: Expectation{Absent: true},
		Actor: ChangeActor{Kind: "person", Tool: "write_file"}})
	require.NoError(t, err, "fixture creation must commit before the tested mutation")
	require.Equal(t, PersistenceComplete, out.PersistenceStatus)
	require.Equal(t, content, readFile(t, filepath.Join(root, slug, "SKILL.md")))
	require.Equal(t, out.Revision, readLiveRevision(t, filepath.Join(root, slug)))
	return out
}

// §7.3/§15 E-2: preserve the stale-writer safety intent at the one pipeline.
func TestReviewedSkillMutationsRejectStaleRevisionWithoutWrites(t *testing.T) {
	root := filepath.Join(t.TempDir(), "skills")
	created := createAuthoredSkill(t, root, "release", validFor("release"))
	first := validFor("release") + "\nFirst writer.\n"
	committed, _ := commitWrite(t, root, "release", "SKILL.md", first, created.Revision)
	beforeHistory := manifestIDs(t, SkillHistoryRoot(root), "release")
	out, err := ApplyChange(root, ChangeRequest{Kind: KindEdit, Slug: "release", Path: "SKILL.md",
		Content: []byte(validFor("release") + "\nStale writer.\n"), Expect: Expectation{Revision: created.Revision}})
	require.ErrorIs(t, err, ErrSkillChanged)
	assert.Contains(t, err.Error(), "skill 'release' changed since you read it (now "+committed.Revision[:8]+" by agent-a); read it again with get_skill, Skill or read_file")
	assert.Equal(t, PersistenceNone, out.PersistenceStatus)
	assert.Equal(t, first, readFile(t, filepath.Join(root, "release", "SKILL.md")), "stale edit changes no byte")
	assert.Equal(t, beforeHistory, manifestIDs(t, SkillHistoryRoot(root), "release"), "no refused-write version")
}

// §15 E-1: independent concurrent callers share no writer instance.
func TestReviewedCreateRequiresAbsenceAndConcurrentWritersHaveOneWinner(t *testing.T) {
	root := filepath.Join(t.TempDir(), "skills")
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := ApplyChange(root, ChangeRequest{Kind: KindCreate, Slug: "race",
				Content: []byte(validFor("race")), Expect: Expectation{Absent: true}})
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	wins, conflicts := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, ErrSkillExists):
			conflicts++
			assert.Contains(t, err.Error(), "skill 'race' already exists; read it first")
		default:
			t.Fatalf("unexpected create error: %v", err)
		}
	}
	assert.Equal(t, 1, wins, "exactly one expected-absence caller commits")
	assert.Equal(t, 1, conflicts, "the other caller gets only SKILL_EXISTS")
	assert.Equal(t, validFor("race"), readFile(t, filepath.Join(root, "race", "SKILL.md")))
	assert.Len(t, manifestIDs(t, SkillHistoryRoot(root), "race"), 1, "only the winner records a creation")
}

// §7.3/§15 E-12: a bad revision cannot delete; the current one can.
func TestReviewedRemoveRequiresCurrentRevision(t *testing.T) {
	root := filepath.Join(t.TempDir(), "skills")
	created := createAuthoredSkill(t, root, "cleanup", validFor("cleanup"))
	_, err := ApplyChange(root, ChangeRequest{Kind: KindDelete, Slug: "cleanup", Expect: Expectation{Revision: "missing-review"}})
	require.ErrorIs(t, err, ErrSkillChanged)
	assert.Equal(t, validFor("cleanup"), readFile(t, filepath.Join(root, "cleanup", "SKILL.md")), "conflict preserves the exact skill")
	out, err := ApplyChange(root, ChangeRequest{Kind: KindDelete, Slug: "cleanup", Expect: Expectation{Revision: created.Revision}})
	require.NoError(t, err)
	assert.Equal(t, PersistenceComplete, out.PersistenceStatus)
	assert.NoDirExists(t, filepath.Join(root, "cleanup"))
	ids := manifestIDs(t, SkillHistoryRoot(root), "cleanup")
	require.Len(t, ids, 2, "create and deletion both recorded")
	assert.Equal(t, "delete_skill", manifestField(t, SkillHistoryRoot(root), "cleanup", ids[0], "kind"))
}

func stageReviewedPackage(t *testing.T, root, body, helper string) string {
	t.Helper()
	stage := filepath.Join(root, ".staging", body)
	require.NoError(t, os.MkdirAll(filepath.Join(stage, "scripts"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(stage, "SKILL.md"), []byte(validFor("package")+"\n"+body), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(stage, "scripts", "helper.py"), []byte(helper), 0o644))
	return stage
}

// §§7.2–7.3: replacing a staged package needs an explicit current revision;
// all staged assets, not only SKILL.md, are published together. Authored
// downstream success/fault fixtures explicitly consent under r15 §5.6/§9.2;
// the missing/stale-revision table remains unchanged.
func TestApplyChange_InstallReplaceRequiresCurrentRevisionAndPreservesAssets(t *testing.T) {
	root := filepath.Join(t.TempDir(), "skills")
	created := createAuthoredSkill(t, root, "package", validFor("package"))
	withAsset, _ := commitWrite(t, root, "package", "scripts/helper.py", "old", created.Revision)
	before := manifestIDs(t, SkillHistoryRoot(root), "package")
	for _, tc := range []struct {
		name   string
		expect Expectation
		code   error
	}{
		{"without-review", Expectation{}, ErrInvalidInput},
		{"stale-review", Expectation{Revision: created.Revision}, ErrSkillChanged},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := ApplyChange(root, ChangeRequest{Kind: KindInstallReplace, Slug: "package",
				StagedDir: stageReviewedPackage(t, root, tc.name, "bad"), Expect: tc.expect})
			require.ErrorIs(t, err, tc.code)
			assert.Equal(t, PersistenceNone, out.PersistenceStatus)
			assert.Equal(t, "old", readFile(t, filepath.Join(root, "package", "scripts", "helper.py")))
			assert.Equal(t, validFor("package"), readFile(t, filepath.Join(root, "package", "SKILL.md")))
			assert.Equal(t, before, manifestIDs(t, SkillHistoryRoot(root), "package"))
		})
	}
	stage := stageReviewedPackage(t, root, "reviewed", "new")
	next, err := ApplyChange(root, ChangeRequest{Kind: KindInstallReplace, ReplaceLocalChanges: true, Slug: "package", StagedDir: stage,
		Expect: Expectation{Revision: withAsset.Revision}})
	require.NoError(t, err)
	assert.Equal(t, PersistenceComplete, next.PersistenceStatus)
	assert.NotEqual(t, withAsset.Revision, next.Revision)
	assert.Equal(t, validFor("package")+"\nreviewed", readFile(t, filepath.Join(root, "package", "SKILL.md")))
	assert.Equal(t, "new", readFile(t, filepath.Join(root, "package", "scripts", "helper.py")))
}

// §7.6 class 3: cleanup failure never misreports a committed replacement.
func TestApplyChange_ReportsSuccessfulPublishWhenSwapCleanupFails(t *testing.T) {
	root := filepath.Join(t.TempDir(), "skills")
	created := createAuthoredSkill(t, root, "package", validFor("package"))
	stage := stageReviewedPackage(t, root, "replacement", "helper")
	previous := FaultSwapRemove
	calls := []string{}
	FaultSwapRemove = func(path string) error { calls = append(calls, path); return errors.New("cleanup refused") }
	t.Cleanup(func() { FaultSwapRemove = previous })
	outcome, err := ApplyChange(root, ChangeRequest{Kind: KindInstallReplace, ReplaceLocalChanges: true, Slug: "package", StagedDir: stage,
		Expect: Expectation{Revision: created.Revision}})
	require.NoError(t, err, "committed replacement is successful despite cleanup failure")
	assert.Equal(t, []string{filepath.Join(root, ".swap", "package")}, calls, "fault fired on the actual backup disposal")
	assert.NotEmpty(t, outcome.Revision)
	assert.NotEqual(t, created.Revision, outcome.Revision)
	assert.Equal(t, PersistenceComplete, outcome.PersistenceStatus)
	assert.Equal(t, ActivationActive, outcome.ActivationStatus)
	assert.Equal(t, []string{"installed"}, outcome.ChangedFields)
	assert.Contains(t, outcome.Warning, "cleanup is incomplete: cleanup refused")
	assert.Equal(t, validFor("package")+"\nreplacement", readFile(t, filepath.Join(root, "package", "SKILL.md")))
	assert.Equal(t, "helper", readFile(t, filepath.Join(root, "package", "scripts", "helper.py")))
	assert.Len(t, manifestIDs(t, SkillHistoryRoot(root), "package"), 2)
	FaultSwapRemove = previous
	require.NoError(t, RecoverInterrupted(root, SkillHistoryRoot(root)))
	assert.NoDirExists(t, filepath.Join(root, ".swap", "package"))
	assert.Equal(t, validFor("package")+"\nreplacement", readFile(t, filepath.Join(root, "package", "SKILL.md")), "restart keeps the committed replacement")
}

// §7.6 class 1b/§9.3: both failed renames are observable and restart restores
// the predecessor. No missing live folder may be claimed as a publication.
func TestApplyChange_ReportsFailedPutBackWithoutClaimingPublication(t *testing.T) {
	root := filepath.Join(t.TempDir(), "skills")
	created := createAuthoredSkill(t, root, "package", validFor("package"))
	before := manifestIDs(t, SkillHistoryRoot(root), "package")
	require.Len(t, before, 1)
	stage := stageReviewedPackage(t, root, "replacement", "helper")
	previousRename, previousPutBack := renamePublishedSkill, FaultPutBackRename
	var publishCalls, putBackCalls int
	publishErr, restoreErr := errors.New("publish rename refused"), errors.New("restore rename refused")
	renamePublishedSkill = func(oldpath, newpath string) error {
		require.Equal(t, filepath.Join(root, "package"), newpath)
		publishCalls++
		return publishErr
	}
	FaultPutBackRename = func(oldpath, newpath string) error {
		assert.Equal(t, filepath.Join(root, ".swap", "package"), oldpath)
		assert.Equal(t, filepath.Join(root, "package"), newpath)
		putBackCalls++
		return restoreErr
	}
	t.Cleanup(func() { renamePublishedSkill, FaultPutBackRename = previousRename, previousPutBack })
	outcome, err := ApplyChange(root, ChangeRequest{Kind: KindInstallReplace, ReplaceLocalChanges: true, Slug: "package", StagedDir: stage,
		Expect: Expectation{Revision: created.Revision}})
	require.ErrorIs(t, err, publishErr)
	require.ErrorIs(t, err, restoreErr)
	require.ErrorIs(t, err, ErrSkillPartiallySaved)
	assert.Equal(t, 1, publishCalls)
	assert.Equal(t, 1, putBackCalls)
	assert.Empty(t, outcome.Revision, "no fabricated live revision when the folder is unavailable")
	assert.Equal(t, PersistencePartial, outcome.PersistenceStatus)
	assert.Equal(t, ActivationNotAttempted, outcome.ActivationStatus)
	assert.Equal(t, []string{"installed"}, outcome.ChangedFields)
	assert.Equal(t, PublicationErrorStageRestorePrevious, outcome.ErrorStage)
	assert.Contains(t, outcome.Message, "replacement publication failed and the previous package could not be restored; no live package is available")
	assert.Contains(t, outcome.Message, "The previous state is version "+before[0]+"; restore it.")
	assert.NoDirExists(t, filepath.Join(root, "package"))
	assert.Equal(t, validFor("package"), readFile(t, filepath.Join(root, ".swap", "package", "SKILL.md")))
	assert.Equal(t, before, manifestIDs(t, SkillHistoryRoot(root), "package"), "failed publication commits no replacement version")
	renamePublishedSkill, FaultPutBackRename = previousRename, previousPutBack
	require.NoError(t, RecoverInterrupted(root, SkillHistoryRoot(root)))
	assert.Equal(t, validFor("package"), readFile(t, filepath.Join(root, "package", "SKILL.md")))
	assert.NoDirExists(t, filepath.Join(root, ".swap", "package"))
}

// §7.2/class 1: invalid instructions are refused before moving the live tree.
func TestApplyChange_RejectsInvalidStageBeforeMovingPublishedSkill(t *testing.T) {
	root := filepath.Join(t.TempDir(), "skills")
	created := createAuthoredSkill(t, root, "package", validFor("package"))
	stage := filepath.Join(root, ".staging", "invalid")
	require.NoError(t, os.MkdirAll(stage, 0o755))
	outcome, err := ApplyChange(root, ChangeRequest{Kind: KindInstallReplace, ReplaceLocalChanges: true, Slug: "package", StagedDir: stage,
		Expect: Expectation{Revision: created.Revision}})
	require.ErrorIs(t, err, ErrSaveCheckFailed)
	assert.Contains(t, err.Error(), "the change to 'package' was not saved:")
	assert.Empty(t, outcome.Revision)
	assert.Equal(t, PersistenceNone, outcome.PersistenceStatus)
	assert.Equal(t, validFor("package"), readFile(t, filepath.Join(root, "package", "SKILL.md")))
	assert.NoDirExists(t, filepath.Join(root, ".swap", "package"), "validation precedes the live-to-swap move")
	assert.Len(t, manifestIDs(t, SkillHistoryRoot(root), "package"), 1)
}
