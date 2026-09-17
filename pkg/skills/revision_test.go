package skills

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestReviewedSkillMutationsRejectStaleRevisionWithoutWrites(t *testing.T) {
	w := NewSkillWriter(t.TempDir())
	if _, _, err := w.CreateSkillReviewed("release", validFor("release")); err != nil {
		t.Fatal(err)
	}
	reviewed, err := w.SkillRevision("release")
	if err != nil {
		t.Fatal(err)
	}
	first := validFor("release") + "\nFirst writer.\n"
	if _, _, _, err = w.EditSkillReviewed("release", first, false, reviewed); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(w.Root(), "release", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = w.EditSkillReviewed("release", validFor("release")+"\nStale writer.\n", false, reviewed); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale edit err=%v", err)
	}
	after, err := os.ReadFile(filepath.Join(w.Root(), "release", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("stale edit changed persisted bytes")
	}
}

func TestReviewedCreateRequiresAbsenceAndConcurrentWritersHaveOneWinner(t *testing.T) {
	w1 := NewSkillWriter(t.TempDir())
	w2 := NewSkillWriter(w1.Root())
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, w := range []*SkillWriter{w1, w2} {
		wg.Add(1)
		go func(writer *SkillWriter) {
			defer wg.Done()
			<-start
			_, _, err := writer.CreateSkillReviewed("race", validFor("race"))
			errs <- err
		}(w)
	}
	close(start)
	wg.Wait()
	close(errs)
	wins, conflicts := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, ErrAlreadyExists):
			conflicts++
		default:
			t.Fatalf("unexpected create error: %v", err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("wins=%d conflicts=%d", wins, conflicts)
	}
}

func TestReviewedRemoveRequiresCurrentRevision(t *testing.T) {
	w := NewSkillWriter(t.TempDir())
	_, revision, err := w.CreateSkillReviewed("cleanup", validFor("cleanup"))
	if err != nil {
		t.Fatal(err)
	}
	if err = w.RemoveSkillReviewed("cleanup", "missing-review"); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("wrong revision err=%v", err)
	}
	if _, err = os.Stat(filepath.Join(w.Root(), "cleanup", "SKILL.md")); err != nil {
		t.Fatalf("conflict removed skill: %v", err)
	}
	if err = w.RemoveSkillReviewed("cleanup", revision); err != nil {
		t.Fatal(err)
	}
}

func TestPublishStagedSkillRequiresExplicitCurrentRevisionAndPreservesAssets(t *testing.T) {
	root := t.TempDir()
	w := NewSkillWriter(root)
	_, reviewed, err := w.CreateSkillReviewed("package", validFor("package"))
	if err != nil {
		t.Fatal(err)
	}
	asset := filepath.Join(root, "package", "scripts", "helper.py")
	if err = os.MkdirAll(filepath.Dir(asset), 0o755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(asset, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	reviewed, err = w.SkillRevision("package")
	if err != nil {
		t.Fatal(err)
	}

	makeStage := func(body, helper string) string {
		stage := filepath.Join(root, ".staging", body)
		if err := os.MkdirAll(filepath.Join(stage, "scripts"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(stage, "SKILL.md"), []byte(validFor("package")+"\n"+body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(stage, "scripts", "helper.py"), []byte(helper), 0o644); err != nil {
			t.Fatal(err)
		}
		return stage
	}
	if _, err = PublishStagedSkill(root, "package", makeStage("without-review", "bad"), ""); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("blind replacement err=%v", err)
	}
	if got, _ := os.ReadFile(asset); string(got) != "old" {
		t.Fatalf("blind replacement changed asset: %q", got)
	}
	next, err := PublishStagedSkill(root, "package", makeStage("reviewed", "new"), reviewed)
	if err != nil || next.Revision == reviewed {
		t.Fatalf("next=%q err=%v", next, err)
	}
	if got, _ := os.ReadFile(asset); string(got) != "new" {
		t.Fatalf("replacement lost staged asset: %q", got)
	}
}

func TestPublishStagedSkillReportsSuccessfulPublishWhenBackupCleanupFails(t *testing.T) {
	root := t.TempDir()
	w := NewSkillWriter(root)
	_, reviewed, err := w.CreateSkillReviewed("package", validFor("package"))
	if err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(root, ".staging", "replacement")
	if err = os.MkdirAll(stage, 0o755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(stage, "SKILL.md"), []byte(validFor("package")+"\nreplacement\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	originalRemove := removePublishedBackup
	removePublishedBackup = func(string) error { return errors.New("cleanup refused") }
	t.Cleanup(func() { removePublishedBackup = originalRemove })

	outcome, err := PublishStagedSkill(root, "package", stage, reviewed)
	if err != nil {
		t.Fatalf("published replacement reported as failed: %v", err)
	}
	if outcome.Revision == "" || outcome.Revision == reviewed || outcome.PersistenceStatus != "complete" || outcome.ActivationStatus != "active" {
		t.Fatalf("outcome=%+v", outcome)
	}
	if outcome.Warning == "" || len(outcome.ChangedFields) != 1 || outcome.ChangedFields[0] != "installed" {
		t.Fatalf("missing cleanup warning/state: %+v", outcome)
	}
	got, readErr := os.ReadFile(filepath.Join(root, "package", "SKILL.md"))
	if readErr != nil || !strings.Contains(string(got), "replacement") {
		t.Fatalf("published bytes missing: %q err=%v", got, readErr)
	}
}

func TestPublishStagedSkillReportsFailedRestoreWithoutClaimingPublication(t *testing.T) {
	root := t.TempDir()
	w := NewSkillWriter(root)
	_, reviewed, err := w.CreateSkillReviewed("package", validFor("package"))
	if err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(root, ".staging", "replacement")
	if err = os.MkdirAll(stage, 0o755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(stage, "SKILL.md"), []byte(validFor("package")+"\nreplacement\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	originalRename := renamePublishedSkill
	renames := 0
	renamePublishedSkill = func(oldPath, newPath string) error {
		renames++
		if renames == 1 {
			return os.Rename(oldPath, newPath)
		}
		return errors.New("rename refused")
	}
	t.Cleanup(func() { renamePublishedSkill = originalRename })

	outcome, err := PublishStagedSkill(root, "package", stage, reviewed)
	if err == nil || !strings.Contains(err.Error(), "restore failed") {
		t.Fatalf("err=%v", err)
	}
	if outcome.Revision != "" || outcome.PersistenceStatus != "" || outcome.ActivationStatus != "" {
		t.Fatalf("failed publication claimed saved state: %+v", outcome)
	}
}

func TestPublishStagedSkillRejectsInvalidStageBeforeMovingPublishedSkill(t *testing.T) {
	root := t.TempDir()
	w := NewSkillWriter(root)
	_, reviewed, err := w.CreateSkillReviewed("package", validFor("package"))
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(root, "package", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(root, ".staging", "invalid")
	if err = os.MkdirAll(stage, 0o755); err != nil {
		t.Fatal(err)
	}

	outcome, err := PublishStagedSkill(root, "package", stage, reviewed)
	if err == nil || !strings.Contains(err.Error(), "verify staged skill") {
		t.Fatalf("err=%v", err)
	}
	if outcome.Revision != "" {
		t.Fatalf("invalid stage claimed revision: %+v", outcome)
	}
	after, readErr := os.ReadFile(filepath.Join(root, "package", "SKILL.md"))
	if readErr != nil || string(after) != string(before) {
		t.Fatalf("published skill changed: %q err=%v", after, readErr)
	}
}
