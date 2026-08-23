package gateway

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// fakePolicyApprover is a test double for agent.PolicyApprover. It records
// every RequestApproval call it receives (for assertions on whether the
// interactive approval path was actually reached, and with what identity) and
// returns a single scripted outcome.
type fakePolicyApprover struct {
	mu       sync.Mutex
	calls    []agent.PolicyApprovalReq
	approved bool
	reason   string
}

func (f *fakePolicyApprover) RequestApproval(
	_ context.Context,
	req agent.PolicyApprovalReq,
) (bool, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, req)
	return f.approved, f.reason
}

func (f *fakePolicyApprover) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// minimalAgentLoopCfg returns the smallest config.Config that
// agent.NewAgentLoop accepts, mirroring newTestWSHandler's setup.
func minimalAgentLoopCfg(t *testing.T, extra ...config.AgentConfig) *config.Config {
	t.Helper()
	return &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080, DevModeBypass: true},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         t.TempDir(),
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			List: extra,
		},
	}
}

// --- Policy resolution is independent of the grant store ---

// TestToolApproval_PolicyDenyOverridesGrant proves that policy resolution
// (AgentLoop.ResolveApprovalToolPolicy — the same primitive runTurn's TOCTOU
// dispatch uses to decide allow/ask/deny) never consults the grant store: a
// "deny" verdict holds even when a matching "Always Allow" grant already
// exists for the exact (session, agent, tool). Combined with runTurn's
// structure (deny short-circuits with `continue` before
// CheckGrantOrRequestApproval — the only grant consultation point — is ever
// reached), this is what makes "policy deny overrides an existing grant" hold
// for the system as a whole.
func TestToolApproval_PolicyDenyOverridesGrant(t *testing.T) {
	// There is no DefaultPolicy field any more (CLAUDE.md hard constraint 6);
	// the explicit "exec": deny entry alone determines the verdict below.
	cfg := minimalAgentLoopCfg(t, config.AgentConfig{
		ID: "agent-a",
		Tools: &config.AgentToolsCfg{
			Builtin: config.AgentBuiltinToolsCfg{
				Policies: map[string]config.ToolPolicy{
					"exec": config.ToolPolicyDeny,
				},
			},
		},
	})
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})

	al.ApprovalGrants().Record("session-1", "agent-a", "exec", nil)
	require.True(t, al.ApprovalGrants().IsAllowed("session-1", "agent-a", "exec", nil),
		"setup: the grant must be recorded before asserting policy ignores it")

	assert.Equal(t, "deny", al.ResolveApprovalToolPolicy("agent-a", "exec"),
		"policy deny must hold regardless of an existing grant — policy resolution never consults the grant store")
}

// TestToolApproval_PolicyAllowNeverPrompts proves that a policy verdict of
// "allow" resolves without any grant on file — it does not depend on, and
// never touches, the grant store. Combined with runTurn's structure (an
// "allow" verdict falls straight through to tool execution and never enters
// the "ask" branch where CheckGrantOrRequestApproval lives), this is what
// makes "policy allow never prompts" hold for the system as a whole.
func TestToolApproval_PolicyAllowNeverPrompts(t *testing.T) {
	// The removed DefaultPolicy=allow fallback (CLAUDE.md hard constraint 6)
	// is replaced with an explicit "exec": allow entry — this is the tool the
	// test resolves policy for below, and coverage must now be explicit.
	cfg := minimalAgentLoopCfg(t, config.AgentConfig{
		ID: "agent-a",
		Tools: &config.AgentToolsCfg{
			Builtin: config.AgentBuiltinToolsCfg{
				Policies: map[string]config.ToolPolicy{
					"exec": config.ToolPolicyAllow,
				},
			},
		},
	})
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})

	require.False(t, al.ApprovalGrants().IsAllowed("session-1", "agent-a", "exec", nil),
		"sanity: no grant exists for this (session, agent, tool)")

	assert.Equal(t, "allow", al.ResolveApprovalToolPolicy("agent-a", "exec"),
		"policy allow must resolve without ever consulting (or requiring) a grant")
}

// --- AgentLoop.CheckGrantOrRequestApproval — the new consultation point ---

// TestCheckGrantOrRequestApproval_AskWithGrantAutoApproves is the direct fix
// regression test: an existing grant for the exact (session, agent, tool)
// must auto-approve WITHOUT ever reaching the interactive approver (no
// re-prompt).
func TestCheckGrantOrRequestApproval_AskWithGrantAutoApproves(t *testing.T) {
	al := mustAgentLoop(t, minimalAgentLoopCfg(t), bus.NewMessageBus(), &restMockProvider{})

	fake := &fakePolicyApprover{approved: false, reason: "must not be called"}
	al.SetToolApprover(fake)

	al.ApprovalGrants().Record("session-1", "agent-a", "exec", nil)

	approved, reason := al.CheckGrantOrRequestApproval(
		context.Background(), "session-1", "agent-a", "exec", "call-1", "turn-1", nil,
	)
	assert.True(t, approved, "an existing grant must auto-approve")
	assert.Empty(t, reason)
	assert.Equal(t, 0, fake.callCount(),
		"an auto-approved (granted) tool must never reach the interactive approver")
}

// TestCheckGrantOrRequestApproval_AskWithoutGrantPrompts proves the other
// half: with NO existing grant, the interactive approver MUST be consulted —
// the fix must not widen approval beyond what was explicitly granted. It also
// verifies the request forwarded to the approver carries the correct
// (session, agent, tool, tool_call_id, turn_id, args) identity.
func TestCheckGrantOrRequestApproval_AskWithoutGrantPrompts(t *testing.T) {
	al := mustAgentLoop(t, minimalAgentLoopCfg(t), bus.NewMessageBus(), &restMockProvider{})

	fake := &fakePolicyApprover{approved: true, reason: ""}
	al.SetToolApprover(fake)

	approved, reason := al.CheckGrantOrRequestApproval(
		context.Background(), "session-1", "agent-a", "exec", "call-1", "turn-1",
		map[string]any{"command": "ls"},
	)
	assert.True(t, approved, "the approver's explicit allow must be honored")
	assert.Empty(t, reason)

	require.Equal(t, 1, fake.callCount(), "no grant on file — the interactive approver must be consulted")
	got := fake.calls[0]
	assert.Equal(t, "exec", got.ToolName)
	assert.Equal(t, "agent-a", got.AgentID)
	assert.Equal(t, "session-1", got.SessionID)
	assert.Equal(t, "call-1", got.ToolCallID)
	assert.Equal(t, "turn-1", got.TurnID)
	assert.Equal(t, "ls", got.Args["command"])
}

// TestCheckGrantOrRequestApproval_GrantScopedByAgentAndSession is an
// end-to-end proof that recording a grant for one (session, agent) does not
// leak to a different agent or a different session — the core scoping fix
// (SEC consent boundary).
func TestCheckGrantOrRequestApproval_GrantScopedByAgentAndSession(t *testing.T) {
	al := mustAgentLoop(t, minimalAgentLoopCfg(t), bus.NewMessageBus(), &restMockProvider{})

	fake := &fakePolicyApprover{approved: false, reason: "denied for test"}
	al.SetToolApprover(fake)

	al.ApprovalGrants().Record("session-1", "agent-a", "exec", nil)

	// Same session, same agent, same tool -> auto-approved, no prompt.
	approved, _ := al.CheckGrantOrRequestApproval(
		context.Background(), "session-1", "agent-a", "exec", "c1", "t1", nil,
	)
	assert.True(t, approved)
	assert.Equal(t, 0, fake.callCount())

	// Different agent, same session/tool -> must still prompt.
	approved, reason := al.CheckGrantOrRequestApproval(
		context.Background(), "session-1", "agent-b", "exec", "c2", "t1", nil,
	)
	assert.False(t, approved, "a different agent must not reuse agent-a's grant")
	assert.Equal(t, "denied for test", reason)
	assert.Equal(t, 1, fake.callCount(), "a different agent must trigger a fresh approval request")

	// Different session, same agent/tool -> must still prompt.
	approved, _ = al.CheckGrantOrRequestApproval(
		context.Background(), "session-2", "agent-a", "exec", "c3", "t1", nil,
	)
	assert.False(t, approved, "a different session must not reuse the grant")
	assert.Equal(t, 2, fake.callCount(), "a different session must trigger a fresh approval request")
}
