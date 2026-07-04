//go:build !cgo

// This test file uses //go:build !cgo so it compiles when CGO is disabled.
// When CGO is enabled, pkg/gateway imports pkg/channels/matrix which requires
// the libolm system library (olm/olm.h). If that library is installed,
// remove this build constraint and run tests normally.

package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/agent"
	"github.com/dapicom-ai/omnipus/pkg/security"
)

// TestApproveTool_PolicyDenyOverridesGrant proves ordering is preserved: a
// policy verdict of "deny" must deny the tool call even when a matching
// "Always Allow" grant already exists for this exact (session, agent, tool).
// The policy resolver runs FIRST and its "deny" is authoritative — a granted
// tool is never re-checked against the grant store once policy says deny.
func TestApproveTool_PolicyDenyOverridesGrant(t *testing.T) {
	conn := makeTestConn()
	grants := security.NewApprovalGrantStore()
	grants.Record("session-1", "agent-a", "exec")

	hook := &wsApprovalHook{
		conn:           conn,
		registry:       newWSApprovalRegistry(),
		timeout:        5 * time.Second,
		approvalGrants: grants,
		policyResolver: func(toolName, agentID string) string { return "deny" },
	}

	req := &agent.ToolApprovalRequest{
		Tool:      "exec",
		SessionID: "session-1",
		Meta:      agent.EventMeta{AgentID: "agent-a"},
	}

	decision, err := hook.ApproveTool(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, decision.IsApproved(), "policy deny must win even when a grant exists")
}

// TestApproveTool_PolicyAllowNeverPrompts proves a policy verdict of "allow"
// short-circuits before the grant-store check or any interactive prompt —
// it never blocks waiting on the browser.
func TestApproveTool_PolicyAllowNeverPrompts(t *testing.T) {
	conn := makeTestConn()
	hook := &wsApprovalHook{
		conn:           conn,
		registry:       newWSApprovalRegistry(),
		timeout:        5 * time.Second,
		approvalGrants: nil, // deliberately nil — allow must not even touch it
		policyResolver: func(toolName, agentID string) string { return "allow" },
	}

	req := &agent.ToolApprovalRequest{
		Tool:      "exec",
		SessionID: "session-1",
		Meta:      agent.EventMeta{AgentID: "agent-a"},
	}

	decision, err := hook.ApproveTool(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, decision.IsApproved(), "policy allow must approve without prompting")

	select {
	case <-conn.sendCh:
		t.Error("policy allow must never send an exec_approval_request frame")
	default:
		// expected: nothing sent
	}
}

// TestApproveTool_AskWithGrantAutoApproves is the direct fix regression test
// at the ws_approval.go layer: policy "ask" + an existing grant for this
// exact (session, agent, tool) must auto-approve as VerdictAlways WITHOUT
// sending an exec_approval_request frame (no re-prompt).
func TestApproveTool_AskWithGrantAutoApproves(t *testing.T) {
	conn := makeTestConn()
	grants := security.NewApprovalGrantStore()
	grants.Record("session-1", "agent-a", "exec")

	hook := &wsApprovalHook{
		conn:           conn,
		registry:       newWSApprovalRegistry(),
		timeout:        5 * time.Second,
		approvalGrants: grants,
		policyResolver: func(toolName, agentID string) string { return "ask" },
	}

	req := &agent.ToolApprovalRequest{
		Tool:      "exec",
		SessionID: "session-1",
		Meta:      agent.EventMeta{AgentID: "agent-a"},
	}

	decision, err := hook.ApproveTool(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, decision.IsApproved(), "an existing grant must auto-approve")
	assert.Equal(t, agent.VerdictAlways, decision.Verdict)

	select {
	case <-conn.sendCh:
		t.Error("an auto-approved (granted) tool must never send an exec_approval_request frame")
	default:
		// expected: nothing sent
	}
}

// TestApproveTool_AskWithoutGrantPrompts proves the other half: policy "ask"
// with NO existing grant must still send the interactive approval request
// and block on the browser's decision — the fix must not widen approval
// beyond what was explicitly granted.
func TestApproveTool_AskWithoutGrantPrompts(t *testing.T) {
	conn := makeTestConn()
	reg := newWSApprovalRegistry()
	hook := &wsApprovalHook{
		conn:           conn,
		registry:       reg,
		timeout:        5 * time.Second,
		approvalGrants: security.NewApprovalGrantStore(), // empty — no grant recorded
		policyResolver: func(toolName, agentID string) string { return "ask" },
	}

	req := &agent.ToolApprovalRequest{
		Tool:      "exec",
		SessionID: "session-1",
		Meta:      agent.EventMeta{AgentID: "agent-a"},
	}

	go func() {
		select {
		case frameBytes := <-conn.sendCh:
			var frame replayFrameDecoder
			if err := unmarshalWSServerFrame(frameBytes, &frame); err != nil {
				return
			}
			if frame.Type == "exec_approval_request" {
				reg.resolve(frame.ID, agent.ApprovalDecision{Verdict: agent.VerdictAllow})
			}
		case <-time.After(2 * time.Second):
		}
	}()

	decision, err := hook.ApproveTool(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, decision.IsApproved(), "the browser's explicit allow must still be honored")
}

// TestApproveTool_AlwaysGrantScopedByAgentAndSession is an end-to-end proof,
// at the ws_approval.go layer, that recording a grant for one (session,
// agent) does not leak to a different agent or a different session — the
// core scoping fix (SEC consent boundary).
func TestApproveTool_AlwaysGrantScopedByAgentAndSession(t *testing.T) {
	conn := makeTestConn()
	grants := security.NewApprovalGrantStore()
	hook := &wsApprovalHook{
		conn:           conn,
		registry:       newWSApprovalRegistry(),
		timeout:        5 * time.Second,
		approvalGrants: grants,
		policyResolver: func(toolName, agentID string) string { return "ask" },
	}

	// First call: "always allow" from the browser for (session-1, agent-a, exec).
	go func() {
		select {
		case frameBytes := <-conn.sendCh:
			var frame replayFrameDecoder
			if err := unmarshalWSServerFrame(frameBytes, &frame); err != nil {
				return
			}
			if frame.Type == "exec_approval_request" {
				hook.registry.resolve(frame.ID, agent.ApprovalDecision{Verdict: agent.VerdictAlways})
			}
		case <-time.After(2 * time.Second):
		}
	}()
	firstReq := &agent.ToolApprovalRequest{
		Tool:      "exec",
		SessionID: "session-1",
		Meta:      agent.EventMeta{AgentID: "agent-a"},
	}
	decision, err := hook.ApproveTool(context.Background(), firstReq)
	require.NoError(t, err)
	assert.True(t, decision.IsApproved())

	// Second call: SAME session, SAME agent, SAME tool -> auto-approved, no prompt.
	secondReq := &agent.ToolApprovalRequest{
		Tool:      "exec",
		SessionID: "session-1",
		Meta:      agent.EventMeta{AgentID: "agent-a"},
	}
	decision2, err := hook.ApproveTool(context.Background(), secondReq)
	require.NoError(t, err)
	assert.True(t, decision2.IsApproved())
	assert.Equal(t, agent.VerdictAlways, decision2.Verdict)

	// Third call: DIFFERENT agent, same session/tool -> must still prompt.
	thirdCh := make(chan struct{}, 1)
	go func() {
		select {
		case frameBytes := <-conn.sendCh:
			var frame replayFrameDecoder
			if err := unmarshalWSServerFrame(frameBytes, &frame); err != nil {
				return
			}
			if frame.Type == "exec_approval_request" {
				thirdCh <- struct{}{}
				hook.registry.resolve(frame.ID, agent.ApprovalDecision{Verdict: agent.VerdictDeny, Reason: "denied for test"})
			}
		case <-time.After(2 * time.Second):
		}
	}()
	thirdReq := &agent.ToolApprovalRequest{
		Tool:      "exec",
		SessionID: "session-1",
		Meta:      agent.EventMeta{AgentID: "agent-b"},
	}
	decision3, err := hook.ApproveTool(context.Background(), thirdReq)
	require.NoError(t, err)
	assert.False(t, decision3.IsApproved(), "a different agent must not reuse agent-a's grant")
	select {
	case <-thirdCh:
		// expected: a fresh prompt was sent for the different agent
	default:
		t.Error("a different agent must trigger a fresh exec_approval_request")
	}
}
