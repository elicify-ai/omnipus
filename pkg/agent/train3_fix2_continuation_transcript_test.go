package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// train3Fix2ContinuationSession creates a real session of the given origin
// with the Ask-policy dangerous_tool registered, queues a person's late
// instruction on its session-scoped steering key and returns that key.
func train3Fix2ContinuationSession(
	t *testing.T, al *AgentLoop, sessionType session.UnifiedSessionType, origin session.OriginKind, taskID string,
) (sessionID, scope string, stub *dangerousStubTool, approver *countingApproveApprover) {
	t.Helper()
	mia, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	require.True(t, ok)
	stub = &dangerousStubTool{}
	mia.Tools.Register(stub)
	mia.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{"dangerous_tool": "ask"}})
	approver = &countingApproveApprover{}
	al.SetToolApprover(approver)

	sessions := al.GetSessionStore()
	lifecycle := al.GetSessionLifecycleStore()
	require.NotNil(t, sessions)
	require.NotNil(t, lifecycle)
	meta, err := sessions.NewSession(sessionType, "webchat", testDefaultAgentID)
	require.NoError(t, err)
	rec := &session.LifecycleRecord{
		SessionID: meta.ID, Generation: 1, State: session.LifecycleRunning,
		AgentID: testDefaultAgentID, OwnerScopeKind: session.OwnerScopeHuman,
		Origin: &session.Origin{Kind: origin},
	}
	if taskID != "" {
		require.NoError(t, sessions.SetMeta(meta.ID, session.MetaPatch{TaskID: &taskID}))
		rec.Origin.TaskID = taskID
	}
	require.NoError(t, lifecycle.Persist(rec))

	scope = "agent:" + testDefaultAgentID + ":session:" + meta.ID
	_, _, err = al.enqueueSteeringMessageEntry(scope, testDefaultAgentID,
		providers.Message{Role: "user", Content: "please use dangerous_tool"}, "", "")
	require.NoError(t, err)
	return meta.ID, scope, stub, approver
}

// train3Fix2ToolRows returns the transcript tool rows of one tool.
func train3Fix2ToolRows(t *testing.T, al *AgentLoop, sessionID, tool string) []session.ToolCall {
	t.Helper()
	entries, err := al.GetSessionStore().ReadTranscript(sessionID)
	require.NoError(t, err)
	var calls []session.ToolCall
	for _, entry := range entries {
		for _, c := range entry.ToolCalls {
			if c.Tool == tool {
				calls = append(calls, c)
			}
		}
	}
	return calls
}

// Gate round 2, code-reviewer F01 residual + security S1 remainder: the
// ordinary worker's post-turn steering drain (AgentLoop.Continue) of a task
// conversation refuses an unsettled Ask call (round 1 fixed that), but the
// continuation must also be bound to its concrete session: the refusal row is
// visible in that conversation's transcript (amendment §1.2 R4) and the
// tool.policy.ask.denied audit row names the acting session, never the
// "agent:<agent>:session:<id>" routing key.
func TestTrain3Fix2_TaskContinuationRefusalIsVisibleAndAuditedAgainstItsSession(t *testing.T) {
	prov := testutil.NewScenario().
		WithToolCall("dangerous_tool", `{}`).
		WithText("could not use that tool")
	al, cleanup := newSteerALWithProvider(t, prov)
	defer cleanup()
	readAudit := swapAuditLogger(t, al)
	sessionID, scope, stub, approver := train3Fix2ContinuationSession(
		t, al, session.SessionTypeTask, session.OriginKindTask, "task-train3-fix2")

	_, err := al.Continue(context.Background(), scope, "webchat", "webchat:"+sessionID, "")
	require.NoError(t, err)

	assert.False(t, stub.wasCalled.Load(), "the Ask tool must not run in a task chain")
	assert.Equal(t, int32(0), approver.calls.Load(), "a task continuation never raises an approval")

	denied := auditRowsFor(readAudit(), audit.EventToolPolicyAskDenied, "dangerous_tool")
	require.Len(t, denied, 1, "one tool.policy.ask.denied row for the refused call")
	fields, _ := denied[0]["fields"].(map[string]any)
	assert.Equal(t, sessionID, fields["session_id"],
		"the audit row names the acting session, not the routing key %q", scope)
	assert.Equal(t, string(audit.AskDenyReasonTaskRun), fields["reason"])

	calls := train3Fix2ToolRows(t, al, sessionID, "dangerous_tool")
	require.Len(t, calls, 1, "exactly one visible refusal row in the task conversation's own transcript")
	assert.Equal(t, "denied", calls[0].Status)
	assert.Equal(t, map[string]any{
		"error": true, "text": "Not run: needs approval, and task and plan runs never ask.",
		"reason": autoDenyTaskPlanReason, "permanent": true,
	}, calls[0].Result)
}

// Control: binding the continuation to its session must not make an ordinary
// web conversation's continuation unattended — its Ask call still reaches the
// approver and, once approved, runs and is recorded once in that transcript.
func TestTrain3Fix2_WebContinuationStillAsksAndIsRecorded(t *testing.T) {
	prov := testutil.NewScenario().
		WithToolCall("dangerous_tool", `{}`).
		WithText("done with the tool")
	al, cleanup := newSteerALWithProvider(t, prov)
	defer cleanup()
	sessionID, scope, stub, approver := train3Fix2ContinuationSession(
		t, al, session.SessionTypeChat, session.OriginKindChat, "")

	_, err := al.Continue(context.Background(), scope, "webchat", "webchat:"+sessionID, "")
	require.NoError(t, err)

	assert.Equal(t, int32(1), approver.calls.Load(), "an attended continuation asks the person")
	assert.True(t, stub.wasCalled.Load(), "the approved call runs")
	calls := train3Fix2ToolRows(t, al, sessionID, "dangerous_tool")
	require.Len(t, calls, 1, "the continuation's tool call is recorded once in its own transcript")
	assert.Equal(t, "success", calls[0].Status)

	entries, err := al.GetSessionStore().ReadTranscript(sessionID)
	require.NoError(t, err)
	replies := 0
	for _, entry := range entries {
		if entry.Role == "assistant" && entry.Content == "done with the tool" {
			replies++
		}
	}
	assert.Equal(t, 1, replies, "the continuation's reply is recorded exactly once, never duplicated")
}
