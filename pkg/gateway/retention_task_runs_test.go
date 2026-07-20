//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// retention_task_runs_test.go tests the ADR-050 RD9/RD10 retention WIRING
// added in retention_task_runs.go / retention_goroutine.go: that
// pruneAllTaskRuns visits every task known to the store with the configured
// cutoff/staleAfter, and that executeSweepTick invokes it on the same tick
// and retention window as the session-transcript sweep. The underlying
// per-task primitive — (*task.Store).PruneRuns' age-cutoff, floor-of-one,
// and stuck-run reaper behavior — is already fully unit-tested in
// pkg/task/run_store_test.go and is deliberately NOT re-tested here.

package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// newTestTaskStore builds a *task.Store backed by a temp directory and
// returns both the store and the directory (the store's own dir field is
// unexported, so callers that need to fabricate run day files directly —
// bypassing OpenRun/CloseRun's always-"now" day-file targeting, exactly
// like pkg/task/run_store_test.go's own writeRunRecordAt/chtimes helpers —
// must know the root independently).
func newTestTaskStore(t *testing.T) (*task.Store, string) {
	t.Helper()
	dir := t.TempDir()
	return task.New(dir), dir
}

// mustCreateTestTask creates a minimally-valid task and returns its ID.
func mustCreateTestTask(t *testing.T, store *task.Store, title string) string {
	t.Helper()
	tk := &task.Task{Title: title, Action: task.ActionLLM, WorkspaceID: "ws-retention-test"}
	if err := store.Create(tk); err != nil {
		t.Fatalf("Create task %q: %v", title, err)
	}
	return tk.ID
}

// writeClosedRunDayFile fabricates one day file under
// <dir>/<taskID>/runs/<day>.jsonl containing a single already-CLOSED
// (terminal) TaskRun record, in the exact on-disk shape
// (*task.Store).appendRunRecord produces, then backdates the file's mtime
// to mtimeAt. A closed record is used so PruneRuns' stuck-run reaper (which
// only ever touches OPEN runs) never has a reason to rewrite this file
// before the cutoff-based deletion pass runs — isolating each test to the
// single behavior it's asserting.
func writeClosedRunDayFile(t *testing.T, dir, taskID string, day, mtimeAt time.Time) string {
	t.Helper()
	runsDir := filepath.Join(dir, taskID, "runs")
	if err := os.MkdirAll(runsDir, 0o700); err != nil {
		t.Fatalf("mkdir runs dir: %v", err)
	}
	path := filepath.Join(runsDir, day.UTC().Format("2006-01-02")+".jsonl")
	ended := day.UTC().Format(time.RFC3339)
	rec := task.TaskRun{
		RunID:     "01TESTRUNID" + day.Format("20060102"),
		TaskID:    taskID,
		Status:    task.StatusDone,
		Result:    "ok",
		SessionID: "sess-" + taskID,
		Kind:      task.RunKindManual,
		StartedAt: day.UTC().Format(time.RFC3339),
		EndedAt:   &ended,
	}
	line, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal run record: %v", err)
	}
	if err := os.WriteFile(path, append(line, '\n'), 0o600); err != nil {
		t.Fatalf("write run day file: %v", err)
	}
	if err := os.Chtimes(path, mtimeAt, mtimeAt); err != nil {
		t.Fatalf("chtimes run day file: %v", err)
	}
	return path
}

// fileExists reports whether path exists on disk.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// ---- pruneAllTaskRuns: fan-out wiring ---------------------------------------

// TestPruneAllTaskRuns_VisitsEveryTaskWithConfiguredCutoff is the core wiring
// proof: two DIFFERENT tasks each get an old (pre-cutoff) day file and a
// recent (post-cutoff, always-retained-by-floor-of-one) day file. A single
// pruneAllTaskRuns call must prune EVERY task's old file — not just the
// first one found — establishing that the fan-out actually iterates the
// whole task list rather than stopping after one entity.
func TestPruneAllTaskRuns_VisitsEveryTaskWithConfiguredCutoff(t *testing.T) {
	store, dir := newTestTaskStore(t)

	taskA := mustCreateTestTask(t, store, "task A")
	taskB := mustCreateTestTask(t, store, "task B")

	now := time.Now().UTC()
	oldDay := now.AddDate(0, 0, -100)
	recentDay := now

	oldPathA := writeClosedRunDayFile(t, dir, taskA, oldDay, oldDay)
	recentPathA := writeClosedRunDayFile(t, dir, taskA, recentDay, recentDay)
	oldPathB := writeClosedRunDayFile(t, dir, taskB, oldDay, oldDay)
	recentPathB := writeClosedRunDayFile(t, dir, taskB, recentDay, recentDay)

	cutoff := now.AddDate(0, 0, -30) // 30-day retention window: the 100-day-old files are well past it.
	staleAfter := 24 * time.Hour

	visited, err := pruneAllTaskRuns(store, cutoff, staleAfter)
	if err != nil {
		t.Fatalf("pruneAllTaskRuns: %v", err)
	}
	if visited != 2 {
		t.Fatalf("visited = %d, want 2 (task A and task B)", visited)
	}

	if fileExists(oldPathA) {
		t.Error("task A's old day file should have been pruned by the cutoff")
	}
	if !fileExists(recentPathA) {
		t.Error("task A's recent day file should be retained (floor-of-one)")
	}
	if fileExists(oldPathB) {
		t.Error("task B's old day file should have been pruned by the cutoff — " +
			"every task must be visited, not just the first")
	}
	if !fileExists(recentPathB) {
		t.Error("task B's recent day file should be retained (floor-of-one)")
	}
}

// TestPruneAllTaskRuns_EmptyStoreIsNoop verifies a store with zero tasks is a
// clean no-op: visited=0, no error.
func TestPruneAllTaskRuns_EmptyStoreIsNoop(t *testing.T) {
	store, _ := newTestTaskStore(t)

	visited, err := pruneAllTaskRuns(store, time.Now().AddDate(0, 0, -30), time.Hour)
	if err != nil {
		t.Fatalf("pruneAllTaskRuns on empty store: %v", err)
	}
	if visited != 0 {
		t.Fatalf("visited = %d, want 0 for an empty store", visited)
	}
}

// TestPruneAllTaskRuns_PrunesUnreadableTaskFileRuns verifies the deliberate
// design choice documented on pruneAllTaskRuns: a task whose task.json is
// corrupt/unreadable must still have its run history pruned, because
// PruneRuns only needs the task ID string — never the parsed Task — so a
// corrupt task file must not be allowed to leak its run-history storage
// forever.
func TestPruneAllTaskRuns_PrunesUnreadableTaskFileRuns(t *testing.T) {
	store, dir := newTestTaskStore(t)

	// A task file that exists on disk but fails to parse.
	const corruptID = "corrupt-task-id"
	if err := os.WriteFile(filepath.Join(dir, corruptID+".json"), []byte("{not valid json"), 0o600); err != nil {
		t.Fatalf("write corrupt task file: %v", err)
	}

	now := time.Now().UTC()
	oldDay := now.AddDate(0, 0, -100)
	recentDay := now
	oldPath := writeClosedRunDayFile(t, dir, corruptID, oldDay, oldDay)
	recentPath := writeClosedRunDayFile(t, dir, corruptID, recentDay, recentDay)

	cutoff := now.AddDate(0, 0, -30)
	visited, err := pruneAllTaskRuns(store, cutoff, time.Hour)
	if err != nil {
		t.Fatalf("pruneAllTaskRuns: %v", err)
	}
	if visited != 1 {
		t.Fatalf("visited = %d, want 1 (the corrupt-task-file task)", visited)
	}
	if fileExists(oldPath) {
		t.Error("corrupt task's old day file should still have been pruned")
	}
	if !fileExists(recentPath) {
		t.Error("corrupt task's recent day file should be retained (floor-of-one)")
	}
}

// ---- taskRunStaleAfter -------------------------------------------------------

// TestRetentionTaskRunStaleAfter_DerivesFromScheduleTimeout verifies the
// staleAfter formula: taskRunStaleAfterMultiplier (12) times
// cfg.Schedules.RunTimeoutSeconds, with the SchedulesConfig default (300s)
// applied when unset/invalid — mirroring ApplyDefaults' own <= 0 fallback
// rule so a zero-value config resolves exactly like a real boot would.
func TestRetentionTaskRunStaleAfter_DerivesFromScheduleTimeout(t *testing.T) {
	defaultStale := time.Duration(config.DefaultSchedulesRunTimeoutSeconds) * time.Second * taskRunStaleAfterMultiplier
	cases := []struct {
		name           string
		runTimeoutSecs int
		want           time.Duration
	}{
		{"explicit timeout", 100, 100 * time.Second * taskRunStaleAfterMultiplier},
		{"zero falls back to schedules default", 0, defaultStale},
		{"negative falls back to schedules default", -5, defaultStale},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Schedules.RunTimeoutSeconds = tc.runTimeoutSecs
			got := taskRunStaleAfter(cfg)
			if got != tc.want {
				t.Errorf("taskRunStaleAfter() = %s, want %s", got, tc.want)
			}
		})
	}
}

// ---- executeSweepTick wiring: task-run pass rides the session-sweep tick ---

// TestRetentionSweep_InvokesTaskRunPruneWithSessionCutoff verifies
// executeSweepTick calls retentionTaskRunSweepFn exactly once per tick, with
// a cutoff derived from the SAME retention-days config the session sweep
// uses (RD9: "reuse the session-retention sweep cadence") and a staleAfter
// derived from cfg.Schedules.RunTimeoutSeconds via taskRunStaleAfter.
func TestRetentionSweep_InvokesTaskRunPruneWithSessionCutoff(t *testing.T) {
	store := newTestStore(t) // *session.UnifiedStore helper from retention_goroutine_test.go, same package.

	origSweep := retentionSweepFn
	retentionSweepFn = func(_ *session.UnifiedStore, _ int) (int, error) { return 0, nil }
	t.Cleanup(func() { retentionSweepFn = origSweep })

	origTaskRunSweep := retentionTaskRunSweepFn
	var mu sync.Mutex
	calls := 0
	var gotCutoff time.Time
	var gotStaleAfter time.Duration
	retentionTaskRunSweepFn = func(cutoff time.Time, staleAfter time.Duration) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		gotCutoff = cutoff
		gotStaleAfter = staleAfter
		return 3, nil
	}
	t.Cleanup(func() { retentionTaskRunSweepFn = origTaskRunSweep })

	const days = 30
	const runTimeoutSecs = 120
	cfgFn := func() *config.Config {
		cfg := &config.Config{}
		cfg.Storage.Retention = config.OmnipusRetentionConfig{SessionDays: days, Disabled: false}
		cfg.Schedules.RunTimeoutSeconds = runTimeoutSecs
		return cfg
	}

	before := time.Now()
	executeSweepTick(store, cfgFn)
	after := time.Now()

	mu.Lock()
	defer mu.Unlock()

	if calls != 1 {
		t.Fatalf("retentionTaskRunSweepFn called %d times, want 1", calls)
	}

	wantStaleAfter := time.Duration(runTimeoutSecs) * time.Second * taskRunStaleAfterMultiplier
	if gotStaleAfter != wantStaleAfter {
		t.Errorf("staleAfter = %s, want %s", gotStaleAfter, wantStaleAfter)
	}

	minCutoff := before.Add(-time.Duration(days) * 24 * time.Hour)
	maxCutoff := after.Add(-time.Duration(days) * 24 * time.Hour)
	if gotCutoff.Before(minCutoff) || gotCutoff.After(maxCutoff) {
		t.Errorf("cutoff = %s, want between %s and %s (now - %d days at call time)",
			gotCutoff, minCutoff, maxCutoff, days)
	}
}

// TestRetentionSweep_SkipsTaskRunPruneWhenUnwired verifies that a nil
// retentionTaskRunSweepFn (the pre-boot / test-default state) is a silent
// no-op — executeSweepTick must not panic when the task-run pass was never
// wired (e.g. the task store failed to initialize).
func TestRetentionSweep_SkipsTaskRunPruneWhenUnwired(t *testing.T) {
	store := newTestStore(t)

	origSweep := retentionSweepFn
	retentionSweepFn = func(_ *session.UnifiedStore, _ int) (int, error) { return 0, nil }
	t.Cleanup(func() { retentionSweepFn = origSweep })

	origTaskRunSweep := retentionTaskRunSweepFn
	retentionTaskRunSweepFn = nil
	t.Cleanup(func() { retentionTaskRunSweepFn = origTaskRunSweep })

	cfgFn := func() *config.Config {
		cfg := &config.Config{}
		cfg.Storage.Retention = config.OmnipusRetentionConfig{SessionDays: 30, Disabled: false}
		return cfg
	}

	// Must not panic.
	executeSweepTick(store, cfgFn)
}
