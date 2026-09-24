// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// adr092_preflight_turnid_test.go is the end-to-end proof for D-03 (ADR-092,
// 2026-09-24 UAT tester t5): a bash D7 filesystem pre-flight escalation,
// raised from INSIDE the real bash tool under a real AgentLoop turn, must
// produce a tool_approval_required WS frame whose turn_id is non-empty and
// equal to the turn's own, independently-observed id.
//
// Driven through the real production wiring, not a hand-built ExecToolDeps
// or a stand-in approver:
//   - a real *agent.AgentLoop (agent.NewAgentLoop via mustAgentLoop), whose
//     wireExecToolDepsOn (loop_wire.go) builds the bash tool's ADR-092 deps
//     for real, including ShellPermissionGate as both ShellMode and
//     ApprovalRequester;
//   - the real gateway approver: policyApproverAdapter wired to a real
//     approvalRegistryV2 and a real WSHandler (pkg/gateway/policy_approver.go,
//     approvals.go, ws_tool_approval.go) — the exact chain
//     RequestShellApproval -> CheckGrantOrRequestApproval ->
//     policyApproverAdapter.RequestApproval -> requestApproval ->
//     broadcastToolApprovalRequired the D-03 defect and fix both live in;
//   - a real chat turn started through WSHandler.handleChatMessageWithClientID
//     (the same production entry point websocket.go's dispatchFrame calls
//     for a `message` frame), so the turn ID comes from
//     pkg/agent/loop_run_turn_tools.go's real execCtx-building dispatch —
//     the exact code this D-03 fix touches — not a hand-stamped test ctx.
//
// The turn's own id is read independently of the approval path: from the
// AgentLoop's own event bus (EventKindTurnStart's EventMeta.TurnID), a
// completely separate production code path (loop_events.go's
// newTurnEventScope) from the approval-frame's turn_id (entry.TurnID via
// ws_tool_approval.go). Comparing the two proves the SAME real turn id
// reached both places, rather than merely proving the approval frame is
// internally self-consistent.
package gateway

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

const adr092TurnIDTestAgentID = "mia"

// adr092TurnIDProvider scripts exactly one bash tool call (a D7-triggering
// write outside the agent's work dir) before a final text response — one
// real LLM round trip each way, so the escalation genuinely comes from the
// real bash tool's real enforceShellPermissionMode, not a scripted no-op.
type adr092TurnIDProvider struct {
	command string
}

func (p *adr092TurnIDProvider) Chat(
	_ context.Context, msgs []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	if n := len(msgs); n > 0 && msgs[n-1].Role == "tool" {
		return &providers.LLMResponse{Content: "done"}, nil
	}
	return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{
		ID: "call-bash-d7", Type: "function", Name: "bash",
		Arguments: map[string]any{"command": p.command},
	}}}, nil
}

func (p *adr092TurnIDProvider) GetDefaultModel() string { return "test-model" }

// TestADR092Preflight_BashD7Escalation_CarriesRealTurnID is D-03's
// functional proof. Before the fix (shell_permission_mode.go's
// requestPreflightApproval hardcoding turnID as ""), this test fails: the
// captured frame's turn_id is empty, which both fails the
// contract-required assert.NotEmpty below AND, independently, fails
// contract validation against
// contracts/components/schemas/ToolApprovalRequiredFrame.yaml's
// minLength:1 on turn_id (asserted directly further down).
func TestADR092Preflight_BashD7Escalation_CarriesRealTurnID(t *testing.T) {
	agentHome := filepath.Join(t.TempDir(), "home")
	require.NoError(t, os.MkdirAll(agentHome, 0o700))
	outsideDir, err := os.MkdirTemp("", "adr092-e2e-turnid-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(outsideDir) })

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080, DevModeBypass: true},
		Sandbox: config.OmnipusSandboxConfig{
			AutoApprove:  true,
			ToolPolicies: map[string]string{"bash": "ask"},
		},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				// Home MUST be set here too (not only on the List entry
				// below): a bare config.Config{} literal that never
				// populates Agents.Defaults.Home resolves NewAgentLoop's
				// shared session/task store at
				// filepath.Dir(cfg.AgentHomeBasePath()) == filepath.Dir("")
				// == "." — writing real session data straight into this
				// package's own source directory (pkg/gateway/sessions/)
				// instead of the isolated temp dir. See .gitignore's
				// pkg/agent/sessions/ entry, which documents the identical
				// trap in pkg/agent's own tests.
				Home:              agentHome,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: adr092TurnIDTestAgentID, Home: agentHome}},
		},
	}
	msgBus := bus.NewMessageBus()
	provider := &adr092TurnIDProvider{command: "touch " + filepath.Join(outsideDir, "probe")}
	al := mustAgentLoop(t, cfg, msgBus, provider)

	// Independent oracle: the turn's own id, read off the event bus — a
	// completely separate production code path from the approval frame.
	sub := al.SubscribeEvents(16)
	t.Cleanup(func() { al.UnsubscribeEvents(sub.ID) })

	reg := newApprovalRegistryV2(64, 300*time.Second)
	reg.terminalRetention = 0
	handler := newWSHandler(msgBus, al, "")
	handler.approvalRegV2 = reg
	al.SetToolApprover(newPolicyApproverAdapter(reg, handler))

	runCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go al.Run(runCtx)
	t.Cleanup(handler.Wait)

	tabs := apprResAttachConns(t, handler, 1)
	callerConn := makeTestConn()

	handler.handleChatMessageWithClientID(
		context.Background(), "adr092-turnid-chat", "", "please run the probe command",
		adr092TurnIDTestAgentID, nil, "", "", false, "client-turnid-1", nil, callerConn,
	)

	// Capture the turn's real id from the event bus BEFORE consuming the
	// approval frame, so a slow/dropped turn_start event fails loudly here
	// rather than the frame-comparison silently matching two empty strings.
	var capturedTurnID string
	deadline := time.After(5 * time.Second)
capture:
	for {
		select {
		case ev := <-sub.C:
			if ev.Kind == agent.EventKindTurnStart && ev.Meta.AgentID == adr092TurnIDTestAgentID {
				capturedTurnID = ev.Meta.TurnID
				break capture
			}
		case <-deadline:
			t.Fatal("timed out waiting for the turn's own turn_start event")
		}
	}
	require.NotEmpty(t, capturedTurnID, "fixture: the loop's own turn id must itself be non-empty")

	frame := apprResReadFrame(t, tabs[0], "tool_approval_required")
	assert.Equal(t, "bash", frame["tool_name"])

	turnIDFromFrame, _ := frame["turn_id"].(string)
	assert.NotEmpty(t, turnIDFromFrame,
		"D-03: the tool_approval_required frame's turn_id must be non-empty — an empty value "+
			"fails contracts/components/schemas/ToolApprovalRequiredFrame.yaml's minLength:1 and "+
			"is silently dropped by the SPA's generated Zod schema before it ever reaches "+
			"useToolApprovalStore.enqueue, leaving the bash call hanging on \"Running...\" "+
			"(reproduces UAT tester t5, session session_01M390ZHHGPP4M3J6MSKJRM003)")
	assert.Equal(t, capturedTurnID, turnIDFromFrame,
		"the approval frame's turn_id must be the SAME id the loop's own event bus recorded "+
			"for this turn, not a placeholder or a different turn's id")

	// Let the turn finish cleanly: approve the escalation.
	approvalID, _ := frame["approval_id"].(string)
	require.NotEmpty(t, approvalID)
	resolved, gone := reg.resolve(approvalID, ApprovalActionApprove, false)
	require.True(t, resolved && !gone, "the pending approval must resolve")

	deadline = time.After(5 * time.Second)
	for {
		select {
		case ev := <-sub.C:
			if ev.Kind == agent.EventKindTurnEnd && ev.Meta.AgentID == adr092TurnIDTestAgentID {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for the turn to end after approval")
		}
	}
}
