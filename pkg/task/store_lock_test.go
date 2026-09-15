// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !windows

package task

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/fileutil/fileutiltest"
)

// Every file this package writes — task files, evidence records, run day
// files — is written with fileutil.WriteFileAtomic (temp file + rename) under a
// cross-process fileutil.WithFlock lock. That lock used to be taken on the data
// file itself. WithFlock opens its path with O_CREATE, so a file's first write
// created it EMPTY and left it there until the rename replaced it: a reader
// outside this store's striped lock read zero bytes, and a crash in that
// window left the empty file for good. The rename also moved the file to a new
// inode, so a writer that opened the path before it and one that opened it
// after held two different locks and ran together.
//
// The first-write tests park the write inside its lock; the "takes the lock"
// tests hold the sidecar lock and require the write to wait for it, which is
// what proves every writer of one task file shares one lock.

func TestTaskStore_FirstWriteNeverExposesAnIncompleteFile(t *testing.T) {
	s := newStore(t)
	tk := mkTask("lock test", "ws")
	tk.ID = "task-lock-first-write"
	target := filepath.Join(s.Dir(), tk.ID+".json")
	parked, release := fileutiltest.HoldFirstWrite(t, &writeFileAtomicFn, tk.ID+".json")

	done := make(chan error, 1)
	go func() { done <- s.Create(tk) }()
	require.Equal(t, target, fileutiltest.WaitParked(t, parked))
	fileutiltest.RequireAbsentOrCompleteJSON(t, target)

	release()
	fileutiltest.WaitDone(t, done)
	fileutiltest.RequireCompleteJSON(t, target)
	require.FileExists(t, fileutil.SidecarLockPath(target), "the task write must take its lock on the sidecar")

	tasks, err := s.List(Filter{})
	require.NoError(t, err)
	require.Len(t, tasks, 1, "the sidecar lock file must never be listed as a task")
}

func TestTaskStore_DropOrphanEdgesTakesTheTaskFileLock(t *testing.T) {
	s := newStore(t)
	blocker := mkTask("blocker", "ws")
	require.NoError(t, s.Create(blocker))
	dependent := mkTask("dependent", "ws")
	dependent.BlockedBy = []string{blocker.ID}
	require.NoError(t, s.Create(dependent))
	require.NoError(t, removeFileRaw(s, blocker.ID), "arrange an orphaned blocked_by edge")

	target := filepath.Join(s.Dir(), dependent.ID+".json")
	fileutiltest.RequireWaitsForSidecarLock(t, target, func() error {
		removed, err := s.DropOrphanEdges()
		if err != nil {
			return err
		}
		if removed != 1 {
			return fmt.Errorf("DropOrphanEdges removed %d edges, want 1", removed)
		}
		return nil
	})

	got, err := s.Get(dependent.ID)
	require.NoError(t, err)
	require.Empty(t, got.BlockedBy)
}

func TestTaskMigrationRewrite_TakesTheTaskFileLock(t *testing.T) {
	s := newStore(t)
	tk := mkTask("before migration", "ws")
	require.NoError(t, s.Create(tk))
	target := filepath.Join(s.Dir(), tk.ID+".json")

	data, err := os.ReadFile(target)
	require.NoError(t, err)
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &raw))
	raw["title"] = json.RawMessage(`"after migration"`)

	fileutiltest.RequireWaitsForSidecarLock(t, target, func() error {
		return rewriteRawTaskFile(target, raw)
	})

	got, err := s.Get(tk.ID)
	require.NoError(t, err)
	require.Equal(t, "after migration", got.Title)
}

func TestTaskStore_DeleteRemovesTheSidecarLockFile(t *testing.T) {
	s := newStore(t)
	tk := mkTask("delete me", "ws")
	require.NoError(t, s.Create(tk))
	target := filepath.Join(s.Dir(), tk.ID+".json")
	lock := fileutil.SidecarLockPath(target)
	require.FileExists(t, lock, "precondition: the task's write created its sidecar lock file")

	_, err := s.Delete(tk.ID)
	require.NoError(t, err)
	require.NoFileExists(t, target)
	require.NoFileExists(t, lock, "a deleted task must not leave its lock file behind")

	_, err = s.Delete(tk.ID)
	require.ErrorIs(t, err, ErrNotFound)
	require.NoFileExists(t, lock, "deleting a task that does not exist must not create a lock file")
}

func identityRedact(s string) string { return s }

func TestEvidenceStore_FirstWriteNeverExposesAnIncompleteFile(t *testing.T) {
	home := t.TempDir()
	es := NewEvidenceStore(home, identityRedact)
	const taskID, criterionID = "task-evidence-lock", "crit-1"
	target := filepath.Join(home, evidenceDirName, taskID, criterionID+"-1.json")
	parked, release := fileutiltest.HoldFirstWrite(t, &writeFileAtomicFn, criterionID+"-1.json")

	done := make(chan error, 1)
	go func() {
		_, err := es.Record(taskID, criterionID, 1, "go test ./...", "ok", 0, false, false)
		done <- err
	}()
	require.Equal(t, target, fileutiltest.WaitParked(t, parked))
	fileutiltest.RequireAbsentOrCompleteJSON(t, target)

	release()
	fileutiltest.WaitDone(t, done)
	fileutiltest.RequireCompleteJSON(t, target)
	require.FileExists(t, fileutil.SidecarLockPath(target), "the evidence write must take its lock on the sidecar")

	records, err := es.List(taskID)
	require.NoError(t, err)
	require.Len(t, records, 1)
}

// An evidence record is a <criterion>-<attempt>.json file. Anything else in the
// task's evidence directory — the sidecar lock files above all — is not a
// record and must not be read as one, whatever it happens to contain.
func TestEvidenceStore_ListReadsOnlyJSONRecordFiles(t *testing.T) {
	home := t.TempDir()
	es := NewEvidenceStore(home, identityRedact)
	const taskID = "task-evidence-list"
	rec, err := es.Record(taskID, "crit-1", 1, "go vet ./...", "ok", 0, false, false)
	require.NoError(t, err)

	// Give a non-record file the body of a valid record, so a lister that reads
	// every file in the directory would visibly count it.
	stray := *rec
	stray.ID = "not-a-record"
	data, err := json.Marshal(stray)
	require.NoError(t, err)
	dir := filepath.Join(home, evidenceDirName, taskID)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "crit-1-1.json.lock"), data, 0o600))

	records, err := es.List(taskID)
	require.NoError(t, err)
	require.Len(t, records, 1, "only <criterion>-<attempt>.json files are evidence records")
	require.Equal(t, rec.ID, records[0].ID)
}

func TestRunStore_FirstDayFileWriteNeverExposesAnEmptyFile(t *testing.T) {
	s := newStore(t)
	const taskID = "task-runs-lock"
	at := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	target := filepath.Join(s.Dir(), taskID, "runs", "2026-03-01.jsonl")
	parked, release := fileutiltest.HoldFirstWrite(t, &writeFileAtomicFn, "2026-03-01.jsonl")

	rec := TaskRun{
		RunID:     "run-lock-1",
		TaskID:    taskID,
		Status:    StatusInProgress,
		SessionID: "s",
		Kind:      RunKindManual,
		StartedAt: at.Format(time.RFC3339),
	}
	done := make(chan error, 1)
	go func() { done <- s.appendRunRecord(taskID, rec, at) }()
	require.Equal(t, target, fileutiltest.WaitParked(t, parked))
	fileutiltest.RequireAbsentOrCompleteJSONL(t, target)

	release()
	fileutiltest.WaitDone(t, done)
	fileutiltest.RequireCompleteJSONL(t, target)
	require.FileExists(t, fileutil.SidecarLockPath(target), "the run write must take its lock on the sidecar")

	folded, err := s.foldRunsLocked(taskID)
	require.NoError(t, err)
	require.Len(t, folded, 1, "the sidecar lock file must never be read as a run day file")
}

func TestPruneRuns_RemovesThePrunedDayFilesLockFile(t *testing.T) {
	s := newStore(t)
	const taskID = "task-prune-lock"
	dir := filepath.Join(s.Dir(), taskID, "runs")
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, d := range []time.Time{old, recent} {
		endedAt := d.Format(time.RFC3339)
		writeRunRecordAt(t, s, taskID, TaskRun{
			RunID:     "run-" + d.Format("20060102"),
			TaskID:    taskID,
			Status:    StatusDone,
			Result:    "x",
			SessionID: "s",
			Kind:      RunKindManual,
			StartedAt: endedAt,
			EndedAt:   &endedAt,
		}, d)
	}
	oldFile := filepath.Join(dir, old.Format("2006-01-02")+".jsonl")
	recentFile := filepath.Join(dir, recent.Format("2006-01-02")+".jsonl")
	chtimes(t, oldFile, old)
	chtimes(t, recentFile, recent)
	require.FileExists(t, fileutil.SidecarLockPath(oldFile), "precondition: the old day file's write created its sidecar")

	require.NoError(t, s.PruneRuns(taskID, time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)))

	require.NoFileExists(t, oldFile)
	require.NoFileExists(t, fileutil.SidecarLockPath(oldFile), "a pruned day file must not leave its lock file behind")
	require.FileExists(t, recentFile)
	require.FileExists(t, fileutil.SidecarLockPath(recentFile), "a retained day file keeps its lock file")
}
