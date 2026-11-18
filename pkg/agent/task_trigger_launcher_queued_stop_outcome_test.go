package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// #1557's oracle is the existing task-cancel contract, not a post-Stop snapshot:
// failed(stopped_by_user), the named person's result, one status event, and a
// completion time (Task.yaml::completed_at). D8.10/MAJ-003 separately requires
// the original active task-goal identity and session binding to survive.
const u2bQueuedPersonStopResult = "[reason:stopped_by_user] Cancelled by uat-person via Stop."

func u2bQueuedStopHumanRequest(sessionID string) StopRequest {
	return StopRequest{
		SessionID: sessionID,
		By:        steer.Principal{Kind: steer.PrincipalKindHuman, ID: "uat-person"},
		Channel:   "web",
	}
}

func newU2bQueuedStopFixture(t *testing.T) (*u2bLauncherProcess, string, string) {
	t.Helper()
	worker := newU2bAsyncWorker()
	p := newU2bLauncherProcess(t, t.TempDir(), newTriggerFakeClock(), worker)
	taskID, sessionID := p.fireQueuedPersonStop(t, worker)
	return p, taskID, sessionID
}

func requireU2bQueuedPersonStopOutcome(t *testing.T, got *task.Task) {
	t.Helper()
	assert.Equal(t, task.StatusFailed, got.Status, "person_stop_status: #1557 requires the stored task to be failed")
	assert.Equal(t, task.CancelReasonStoppedByUser, got.CancelReason, "person_stop_reason: the stored task must remain restartable as stopped_by_user")
	assert.Equal(t, u2bQueuedPersonStopResult, got.Result, "person_stop_result: preserve task cancel's exact named-person outcome")
	assert.NotEmpty(t, got.CompletedAt, "person_stop_completed_at: a stopped task carries its completion time")
	_, err := time.Parse(time.RFC3339, got.CompletedAt)
	assert.NoError(t, err, "person_stop_completed_at_format: completed_at must be RFC3339, got %q", got.CompletedAt)
}

func requireU2bQueuedPersonStopEvent(t *testing.T, sub EventSubscription, taskID, sessionID string) {
	t.Helper()
	assert.Equal(t, []TaskStatusChangedPayload{{
		TaskID: taskID, Status: string(task.StatusFailed), SessionID: sessionID, AgentID: "native-agent",
	}}, drainTaskStatusEvents(sub), "person_stop_one_event: exactly one failed event for the stopped task, agent and session")
}

func requireU2bQueuedPersonStopGoal(t *testing.T, taskID string, before *goal.Goal) {
	t.Helper()
	after := readTaskGoal(t, taskID)
	assert.Equal(t, generated.GoalStateActive, after.State, "person_stop_goal_active: MAJ-003 forbids adjudicating a goal on Stop")
	assert.Equal(t, before.GoalID, after.GoalID, "person_stop_goal_id: preserve the original paired goal identity")
	assert.Equal(t, before.ActiveSessionID, after.ActiveSessionID, "person_stop_goal_session: preserve the original active session binding")
	assert.Empty(t, after.TerminalReason, "person_stop_goal_reason: Stop must not record a goal adjudication")
}

func requireU2bQueuedSessionStopped(t *testing.T, p *u2bLauncherProcess, sessionID string) {
	t.Helper()
	result, err := p.loop.StopSession(context.Background(), u2bQueuedStopHumanRequest(sessionID))
	require.NoError(t, err, "the selected queued session must accept Stop")
	require.NoError(t, result.RootErr, "the selected queued session must stop without a task error")
	require.Equal(t, []string{sessionID}, result.Report.Reached, "instrument: Stop reached the old queued session")
	record, err := p.loop.GetSessionLifecycleStore().Load(sessionID)
	require.NoError(t, err)
	assert.Equal(t, "stopped", string(record.State), "instrument: the queued session really stopped")
}
