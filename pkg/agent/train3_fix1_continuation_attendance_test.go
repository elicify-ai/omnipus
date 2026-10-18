package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Gate round 1, code-reviewer F01: the ordinary worker's post-turn steering
// drain (AgentLoop.Continue) built its turn with no transcript session, so the
// root-origin classification saw an empty session and the turn ran attended.
// A continuation of a task chain must stay unattended (amendment R1/R5): an
// unsettled Ask call is refused at once and never reaches the approver.
func TestTrain3Fix1_SteeringContinuationOfATaskChainStaysUnattended(t *testing.T) {
	prov := testutil.NewScenario().
		WithToolCall("dangerous_tool", `{}`).
		WithText("could not use that tool")
	al, cleanup := newSteerALWithProvider(t, prov)
	defer cleanup()
	mia, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	require.True(t, ok)
	stub := &dangerousStubTool{}
	mia.Tools.Register(stub)
	mia.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{"dangerous_tool": "ask"}})
	approver := &countingApproveApprover{}
	al.SetToolApprover(approver)

	// A real task-origin session: task meta and a task lifecycle record.
	sessions := al.GetSessionStore()
	lifecycle := al.GetSessionLifecycleStore()
	require.NotNil(t, sessions)
	require.NotNil(t, lifecycle)
	meta, err := sessions.NewSession(session.SessionTypeTask, "webchat", testDefaultAgentID)
	require.NoError(t, err)
	taskID := "task-train3-fix1"
	require.NoError(t, sessions.SetMeta(meta.ID, session.MetaPatch{TaskID: &taskID}))
	require.NoError(t, lifecycle.Persist(&session.LifecycleRecord{
		SessionID: meta.ID, Generation: 1, State: session.LifecycleRunning,
		AgentID: testDefaultAgentID, OwnerScopeKind: session.OwnerScopeHuman,
		Origin: &session.Origin{Kind: session.OriginKindTask, TaskID: taskID},
	}))

	// A person's late instruction lands in the task conversation's steering
	// queue and is drained by the worker's continuation.
	scope := "agent:" + testDefaultAgentID + ":session:" + meta.ID
	_, _, err = al.enqueueSteeringMessageEntry(scope, testDefaultAgentID,
		providers.Message{Role: "user", Content: "please use dangerous_tool"}, "", "")
	require.NoError(t, err)
	_, err = al.Continue(context.Background(), scope, "webchat", "webchat:"+meta.ID, "")
	require.NoError(t, err)

	assert.False(t, stub.wasCalled.Load(), "the Ask tool must not run in a task chain")
	assert.Equal(t, int32(0), approver.calls.Load(),
		"a continuation of a task chain must never raise an approval (amendment R1/R5)")
}
