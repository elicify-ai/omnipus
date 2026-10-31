package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// W5 is the real persistent history-fault hold/repair contract. The earlier
// owned-job reader is deliberately kept: this is NOT an isolated M15 kill.
func TestGoalReminderMalformedHistoryRetainsOutcomeUntilRepair(t *testing.T) {
	h := grNewRecordedBrake(t)
	msg := h.reminder(t)
	grSetStreak(t, h, 2)
	run := h.system(msg)
	call := grAwaitCall(t, h.root, run)
	h.finish(t, run, call)
	grAwaitOwner(t, call)
	require.NotNil(t, call.owner)
	require.NotNil(t, call.turn)
	require.NotNil(t, call.record)
	pending := r6WarnPendingOutcome(h.goalID)
	require.NotNil(t, pending, "SETUP: an actual completed reminder must await assessment")
	require.Same(t, call.owner, pending.execution)
	require.Equal(t, call.turn.turnID, pending.turnID)
	store := GetTaskStore(h.al)
	tk := mustCreateTask(t, store, &task.Task{
		Title: "history repair provenance", WorkspaceID: testHarnessWorkspaceMembershipID,
		AgentID: testDefaultAgentID, Status: task.StatusNext,
	})
	ownedSession := newTestSteeringSession(t, h.al, testHarnessWorkspaceMembershipID)
	owned := r6GateOpenRun(t, h, tk.ID, h.id, ownedSession, nil)
	r6GateWaitPastSecond(t, owned.StartedAt)
	evidence := r6GateRecordEvidence(t, h, tk.ID, "r2-history-repair-owned", 1)
	r6GateRequireOpenWindow(t, store, owned, evidence, ownedSession)
	h.age(t)
	var faults atomic.Int32
	h.al.SetEventSyncTap(func(event Event) {
		payload, ok := event.Payload.(ErrorPayload)
		if event.Kind == EventKindError && ok && payload.SessionID == h.id && payload.Stage == "goal_record_read" &&
			strings.Contains(payload.Message, "read owned task outcomes") {
			faults.Add(1)
		}
	})
	t.Cleanup(func() { h.al.SetEventSyncTap(nil) })
	repair := r2CorruptRunHistory(t, store, tk.ID)
	r6GateRequireStorageHealth(t, h)
	healthyGoal, err := resolveGoalRecordStore().Update(h.goalID, func(*goal.Goal) error { return nil })
	require.NoError(t, err, "SETUP: actual goal writes must remain healthy under the separate run-history fault")
	require.Equal(t, 2, healthyGoal.ZeroOutputPushes)
	beforeCalls := h.root.count()
	for tick := 0; tick < 3; tick++ {
		h.al.goalQuietWindowSettle(time.Now())
		assert.Equal(t, 2, goalRecordForSession(t, h.id).ZeroOutputPushes, "unknown provenance must not be treated as no progress or credited progress")
		retained := r6WarnPendingOutcome(h.goalID)
		assert.Same(t, pending, retained, "history failure must retain the SAME natural producing outcome")
		if retained != nil {
			assert.Same(t, call.owner, retained.execution)
			assert.Equal(t, call.turn.turnID, retained.turnID)
		}
		grNoPublishedReminder(t, h.al)
		assert.Equal(t, beforeCalls, h.root.count())
		assert.Equal(t, 0, grHasSystemLine(h.visibleHistory(t), grPauseLine))
	}
	assert.Greater(t, faults.Load(), int32(0), "the actual earlier owned-history guard must visibly hold this persistent fault")

	repair()
	runs, err := reminderTaskRuns(store, tk.ID)
	require.NoError(t, err, "SETUP: exact repair restores strict reader health")
	require.Len(t, runs, 1)
	require.Equal(t, owned.RunID, runs[0].RunID)
	require.Nil(t, runs[0].EndedAt, "SETUP: repair must not invent completion credit")
	h.al.goalQuietWindowSettle(time.Now())
	assert.Equal(t, 0, goalRecordForSession(t, h.id).ZeroOutputPushes, "the same retained outcome now observes its real unambiguous owned evidence")
	assert.Nil(t, r6WarnPendingOutcome(h.goalID), "successful repaired assessment consumes the old outcome exactly once")
	work := goalReminderWorkFor(h.goalID)
	work.mu.Lock()
	assessed := work.lastAssessedTurn
	work.mu.Unlock()
	assert.Equal(t, call.turn.turnID, assessed)
	next, published := r6GateTakeReminder(h)
	require.True(t, published, "history repair must allow the real next keeper dispatch")
	require.Equal(t, h.id, next.AsyncTranscriptSessionID)
	for tick := 0; tick < 2; tick++ {
		h.age(t)
		h.al.goalQuietWindowSettle(time.Now())
		grNoPublishedReminder(t, h.al)
		assert.Equal(t, 0, goalRecordForSession(t, h.id).ZeroOutputPushes, "further observations must not reassess the repaired outcome")
		assert.Equal(t, beforeCalls, h.root.count(), "the queued repaired reminder has not executed yet")
	}
	grTextReminder(t, h, next)
	assert.Equal(t, beforeCalls+1, h.root.count(), "one permitted actual reminder executes after repair")
}

func r2CorruptRunHistory(t *testing.T, store *task.Store, taskID string) func() {
	t.Helper()
	dir := filepath.Join(store.Dir(), taskID, "runs")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var paths []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".jsonl") {
			paths = append(paths, filepath.Join(dir, entry.Name()))
		}
	}
	require.Len(t, paths, 1, "SETUP: corrupt only the canonical day file of this one retained run")
	path := paths[0]
	original, err := os.ReadFile(path)
	require.NoError(t, err)
	info, err := os.Stat(path)
	require.NoError(t, err)
	restored := false
	repair := func() {
		if restored {
			return
		}
		require.NoError(t, os.WriteFile(path, original, info.Mode().Perm()))
		exact, readErr := os.ReadFile(path)
		require.NoError(t, readErr)
		require.Equal(t, original, exact)
		restored = true
	}
	t.Cleanup(repair)
	malformed := []byte(`{"run_id":`)
	var rejected task.TaskRun
	var syntax *json.SyntaxError
	require.ErrorAs(t, json.Unmarshal(malformed, &rejected), &syntax, "SETUP: fault must be actual malformed bytes, not an injected error return")
	broken := append(append([]byte(nil), original...), malformed...)
	broken = append(broken, '\n')
	require.NoError(t, os.WriteFile(path, broken, info.Mode().Perm()))
	actual, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, broken, actual, "SETUP: the malformed retained history must really be on disk and readable")
	canonical, err := store.ListRuns(taskID)
	require.NoError(t, err, "SETUP: canonical skip-corrupt behavior remains intact, rather than forging strict-reader failure")
	require.Len(t, canonical, 1)
	return repair
}
