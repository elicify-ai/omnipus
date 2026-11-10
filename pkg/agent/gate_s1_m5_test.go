package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// S1 final-gate M5: a completion notice owed to the task's starter that cannot be
// delivered must leave a record ON THE TASK (a line in its own transcript) and be
// counted, not only logged. A recipient that is hidden or removed is a by-design
// skip and leaves no loss record.
func TestDeliverTaskCompletionUpward_LostNoticeIsRecordedOnTheTask(t *testing.T) {
	al, lifecycle, _, _ := newDeliverTestLoop(t)
	te := al.taskExecutor
	const ws = "ws-1"
	parentMain, err := session.MainSessionID(ws, testDefaultAgentID)
	require.NoError(t, err)

	childID, recipients := u6SeedChildAndRecipients(t, al, lifecycle, ws, parentMain)
	// The child's own transcript must exist to receive the loss line.
	parentMeta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "")
	require.NoError(t, err)
	_, err = al.GetSessionStore().CreateSessionWithID(childID, parentMeta.ID, session.SessionTypeTask, "", testDefaultAgentID)
	require.NoError(t, err)

	tk := &task.Task{
		ID: "u6-notice-task", AgentID: testDefaultAgentID, WorkspaceID: ws,
		Title: "noticed", Status: task.StatusDone, SessionID: childID, Result: "r",
	}
	run := &activeRun{runID: "run-m5", recipients: recipients}

	transcriptHasLoss := func() bool {
		entries, rerr := al.GetSessionStore().ReadTranscript(childID)
		require.NoError(t, rerr)
		for _, e := range entries {
			if e.Role == "system" && strings.Contains(e.Content, "completion notice") && strings.Contains(e.Content, "could not be delivered") {
				return true
			}
		}
		return false
	}

	// By-design skip: the recipient's main is hidden (not eligible) -> no loss.
	te.SetMainSessionResolver(&u6FakeMainResolver{elig: map[[2]string]string{}})
	before := taskNoticeLosses.Load()
	te.deliverTaskCompletionUpward(context.Background(), tk, run)
	require.Equal(t, before, taskNoticeLosses.Load(), "a hidden recipient is skipped by design, not a loss")
	require.False(t, transcriptHasLoss())

	// Failure: eligibility cannot be determined -> the notice is owed and lost.
	te.SetMainSessionResolver(&u6FakeMainResolver{errs: map[[2]string]error{{ws, testDefaultAgentID}: errors.New("membership unavailable")}})
	te.deliverTaskCompletionUpward(context.Background(), tk, run)
	require.Equal(t, before+1, taskNoticeLosses.Load(), "the lost notice must be counted")
	require.True(t, transcriptHasLoss(), "the lost notice must be recorded in the task's own transcript")
}
