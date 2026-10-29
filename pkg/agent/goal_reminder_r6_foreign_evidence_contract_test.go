package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGoalReminderR6ForeignTaskEvidenceDoesNotReset is founder R6 under the
// conservative rule: a machine-check record counts only inside one owned open
// run, in a later timestamp second than that run's start. A task id is not
// ownership. EvidenceRecord has no session field. The check command is not
// executed; Record is the production writer. Only the ambiguous-second control
// arranges its stored timestamp explicitly at the reader boundary, rather than
// hoping two filesystem writes fit in one wall-clock second. No run/session
// identity is fabricated. The streak oracles are unchanged.
func TestGoalReminderR6ForeignTaskEvidenceDoesNotReset(t *testing.T) {
	t.Run("OwnedOpenRunResetsAndForeignDoesNot", r6GateForeignEvidenceOracle)
	t.Run("SameSecondIsNotOwnedCredit", r6GateSameSecondEvidence)
	t.Run("MissingRunHistoryIsNotCredit", r6GateEvidenceWithoutRun)
}

func r6GateForeignEvidenceOracle(t *testing.T) {
	h := grNewRecordedBrake(t)
	grTextReminder(t, h, h.reminder(t))
	store := GetTaskStore(h.al)
	require.NotNil(t, store, "SETUP: the keeper must read the real task store")
	tk := mustCreateTask(t, store, &task.Task{
		Title: "R6 foreign evidence", WorkspaceID: testHarnessWorkspaceMembershipID,
		AgentID: testDefaultAgentID, Status: task.StatusNext,
	})
	ownedSession := newTestSteeringSession(t, h.al, testHarnessWorkspaceMembershipID)
	ownedRun := r6GateOpenRun(t, h, tk.ID, h.id, ownedSession, nil)
	r6GateWaitPastSecond(t, ownedRun.StartedAt)
	owned := r6GateRecordEvidence(t, h, tk.ID, "r6-owned-check", 1)
	r6GateRequireOpenWindow(t, store, ownedRun, owned, ownedSession)

	grSetStreak(t, h, 1)
	h.age(t)
	afterOwned := h.reminder(t)
	assert.Equal(t, 0, goalRecordForSession(t, h.id).ZeroOutputPushes, "a valid machine-check record inside this goal's own open run resets the seeded streak")
	grTextReminder(t, h, afterOwned)
	// The owned record was written and credited while this run was open.
	// Close it only after that settle, so the later foreign check can open
	// its own nil-occurrence run. Two open nil-occurrence runs cannot exist.
	require.NoError(t, store.CloseRun(tk.ID, ownedRun.RunID, task.StatusDone, "owned check window closed"))

	// Closing the owned run is independently productive. Assess and consume
	// that real completion before establishing the foreign-only window; a
	// correct evidence filter must not suppress required completion credit.
	grSetStreak(t, h, 1)
	h.age(t)
	fresh := h.reminder(t)
	require.Equal(t, 0, goalRecordForSession(t, h.id).ZeroOutputPushes, "SETUP: the genuine owned completion must reset and be assessed before the foreign-only outcome")
	grTextReminder(t, h, fresh)

	foreignInitiator := newTestSteeringSession(t, h.al, testHarnessWorkspaceMembershipID)
	foreignSession := newTestSteeringSession(t, h.al, testHarnessWorkspaceMembershipID)
	require.NotEqual(t, h.id, foreignInitiator)
	require.NotEqual(t, ownedSession, foreignSession)
	foreignRun := r6GateOpenRun(t, h, tk.ID, foreignInitiator, foreignSession, []string{h.id})
	r6GateWaitPastSecond(t, foreignRun.StartedAt)
	foreign := r6GateRecordEvidence(t, h, tk.ID, "r6-foreign-check", 1)
	require.NotEqual(t, owned.ID, foreign.ID, "SETUP: the later record must be a new check, not the owned record again")
	r6GateRequireOpenWindow(t, store, foreignRun, foreign, foreignSession)
	grSetStreak(t, h, 2)
	h.age(t)
	h.reminder(t)
	assert.Equal(t, 3, goalRecordForSession(t, h.id).ZeroOutputPushes, "a machine-check record for this task written inside only a foreign run is not this goal's progress")
	assert.Equal(t, "active", string(goalRecordForSession(t, h.id).State))
	assert.Equal(t, 0, grHasSystemLine(h.visibleHistory(t), grPauseLine))
}

func r6GateSameSecondEvidence(t *testing.T) {
	h, tk, run := r6GateEvidenceHarness(t)
	written := r6GateRecordEvidence(t, h, tk.ID, "r6-same-second", 1)
	// This is a persisted-reader boundary case, not a claim that the real
	// Record call ran within a particular wall-clock second. Preserve its
	// genuine ID/output and arrange only the deliberately ambiguous timestamp.
	written = r6GateArrangeEvidenceSecond(t, h, written, run.StartedAt)
	start, at := r6GateRunSecond(t, run.StartedAt), r6GateRunSecond(t, written.RecordedAt)
	require.True(t, at.Equal(start), "SETUP: the record must fall in the run's start second")
	grSetStreak(t, h, 2)
	h.age(t)
	h.reminder(t)
	assert.Equal(t, 3, goalRecordForSession(t, h.id).ZeroOutputPushes, "a record in the same second as the run start is not an unambiguous owned check")
}

func r6GateArrangeEvidenceSecond(t *testing.T, h *grHarness, written *task.EvidenceRecord, second string) *task.EvidenceRecord {
	t.Helper()
	path := filepath.Join(config.OmnipusHomeDir(), "tasks_evidence", written.TaskID,
		fmt.Sprintf("%s-%d.json", written.CriterionID, written.Attempt))
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var stored task.EvidenceRecord
	require.NoError(t, json.Unmarshal(raw, &stored))
	require.Equal(t, *written, stored, "SETUP: arrange the actual Record output, not a new evidence identity")
	stored.RecordedAt = second
	arranged, err := json.Marshal(stored)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, arranged, 0o600))
	reopened, err := h.al.evidenceStore().List(written.TaskID)
	require.NoError(t, err)
	require.Len(t, reopened, 1, "SETUP: only this timestamp-boundary record is present")
	require.Equal(t, stored, reopened[0], "SETUP: the real reader must see the exact arranged record")
	return &reopened[0]
}

func r6GateEvidenceWithoutRun(t *testing.T) {
	h := grNewRecordedBrake(t)
	grTextReminder(t, h, h.reminder(t))
	store := GetTaskStore(h.al)
	tk := mustCreateTask(t, store, &task.Task{
		Title: "R6 evidence without a run", WorkspaceID: testHarnessWorkspaceMembershipID,
		AgentID: testDefaultAgentID, Status: task.StatusNext,
	})
	sessionID := newTestSteeringSession(t, h.al, testHarnessWorkspaceMembershipID)
	r6GatePersistRunLifecycle(t, h, tk.ID, h.id, sessionID)
	runs, err := store.ListRuns(tk.ID)
	require.NoError(t, err)
	require.Empty(t, runs, "SETUP: this control has a task in scope and no retained run")
	r6GateRecordEvidence(t, h, tk.ID, "r6-no-run", 1)
	grSetStreak(t, h, 2)
	h.age(t)
	h.reminder(t)
	assert.Equal(t, 3, goalRecordForSession(t, h.id).ZeroOutputPushes, "a machine-check record with no retained run is not owned progress")
}

func r6GateEvidenceHarness(t *testing.T) (*grHarness, *task.Task, *task.TaskRun) {
	t.Helper()
	h := grNewRecordedBrake(t)
	grTextReminder(t, h, h.reminder(t))
	store := GetTaskStore(h.al)
	tk := mustCreateTask(t, store, &task.Task{
		Title: "R6 same-second evidence", WorkspaceID: testHarnessWorkspaceMembershipID,
		AgentID: testDefaultAgentID, Status: task.StatusNext,
	})
	sessionID := newTestSteeringSession(t, h.al, testHarnessWorkspaceMembershipID)
	return h, tk, r6GateOpenRun(t, h, tk.ID, h.id, sessionID, nil)
}

func r6GateOpenRun(t *testing.T, h *grHarness, taskID, initiator, runSession string, recipients []string) *task.TaskRun {
	t.Helper()
	r6GatePersistRunLifecycle(t, h, taskID, initiator, runSession)
	opened, created, err := GetTaskStore(h.al).OpenRun(taskID, nil, task.RunKindManual, runSession, recipients)
	require.NoError(t, err)
	require.True(t, created, "SETUP: the check window needs a new open run")
	require.Nil(t, opened.EndedAt, "SETUP: the run must still be open when the record is written")
	require.Equal(t, runSession, opened.SessionID)
	return opened
}

func r6GatePersistRunLifecycle(t *testing.T, h *grHarness, taskID, initiator, runSession string) {
	t.Helper()
	err := h.al.GetSessionLifecycleStore().Persist(&session.LifecycleRecord{
		SessionID: runSession, Generation: 1, State: session.LifecycleCompleted,
		OwnerScopeKind: session.OwnerScopeHuman, OwnerScopeID: initiator,
		WorkspaceID: testHarnessWorkspaceMembershipID, AgentID: testDefaultAgentID,
		Origin: &session.Origin{Kind: session.OriginKindTask, TaskID: taskID, CallID: "r6-gate-run-" + runSession},
		InitiatedBy: &session.InitiatedBy{
			AgentID: testDefaultAgentID, SessionID: initiator, Depth: 1,
			Authorization: session.Authorization{Mode: session.AuthorizationModeTask, RemainingDepth: 1},
		},
	})
	require.NoError(t, err)
}

func r6GateWaitPastSecond(t *testing.T, startedAt string) {
	t.Helper()
	start := r6GateRunSecond(t, startedAt)
	deadline := time.Now().Add(2 * time.Second)
	for !time.Now().UTC().Truncate(time.Second).After(start) {
		if time.Now().After(deadline) {
			t.Fatal("SETUP: the clock second did not move past the run start")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func r6GateRunSecond(t *testing.T, raw string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339, raw)
	require.NoError(t, err)
	return at.UTC().Truncate(time.Second)
}

func r6GateRequireOpenWindow(t *testing.T, store *task.Store, run *task.TaskRun, written *task.EvidenceRecord, sessionID string) {
	t.Helper()
	runs, err := store.ListRuns(run.TaskID)
	require.NoError(t, err)
	var stored *task.TaskRun
	for i := range runs {
		if runs[i].RunID == run.RunID {
			stored = &runs[i]
		}
	}
	require.NotNil(t, stored, "SETUP: the open run must still be stored")
	require.Nil(t, stored.EndedAt, "SETUP: the run must still be open")
	require.Equal(t, sessionID, stored.SessionID)
	require.True(t, r6GateRunSecond(t, written.RecordedAt).After(r6GateRunSecond(t, stored.StartedAt)), "SETUP: the record's second must be later than the open run's start second")
}

func r6GateRecordEvidence(t *testing.T, h *grHarness, taskID, criterionID string, attempt int) *task.EvidenceRecord {
	t.Helper()
	written, err := h.al.evidenceStore().Record(taskID, criterionID, attempt, "external deterministic check", "Verified external check output", 0, false, false)
	require.NoError(t, err)
	require.Equal(t, taskID, written.TaskID)
	reopened, err := h.al.evidenceStore().List(taskID)
	require.NoError(t, err)
	var found bool
	for _, record := range reopened {
		if record.ID == written.ID {
			found = true
			require.Equal(t, "Verified external check output", record.Output)
			require.False(t, record.PolicyDenied)
			require.False(t, record.TimedOut)
			require.GreaterOrEqual(t, record.ExitCode, 0)
		}
	}
	require.True(t, found, "SETUP: the keeper must be able to read the record that was just written")
	return written
}
