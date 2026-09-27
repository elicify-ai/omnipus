// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// adr092_first_message_auto_approve_test.go proves the founder's ADR-092
// rule B (2026-09-24): in a NEW chat, the per-chat Auto-approve choice takes
// effect at the chat's first activity — EVERY tool call of the first turn,
// including the very first one, already runs under the chosen mode. Before
// this fix, the choice was flushed as a session_mode_update sent only AFTER
// the client received the session_started ack — a round trip that could
// arrive behind the agent loop's own first LLM call, letting the new chat's
// first tool call be decided under whatever the agent/global default
// happened to be instead of the user's explicit choice.
//
// The fix carries the choice ON the MessageFrame that mints the session
// (contracts/asyncapi.yaml's optional, nullable `auto_approve` field) and
// records it in SessionModeStore (websocket_chat.go::recordSessionAndTranscript,
// via the shared WSHandler.applySessionModeChoice in ws_session_mode.go)
// BEFORE the turn is ever published to the bus — so there is no window in
// which the agent loop's consumption of that turn (al.Run's InboundChan
// select, a genuinely separate goroutine here exactly as in production) can
// observe anything other than the caller's explicit choice.
//
// Both tests drive handleChatMessageWithClientID directly (the same
// production entry point websocket.go's dispatchFrame calls for a `message`
// frame) with a REAL AgentLoop.Run consuming the bus, a scripted provider
// that issues one ask-policy tool call before its final text, and a
// recording PolicyApprover — the same instrument
// pkg/agent/auto_approve_gate_test.go uses ("a prompt shown to a human is
// exactly one RequestApproval call").
package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// adr092FirstMsgProbeTool is a stand-in for browser_navigate (a catalog
// AutoRuns tool under pkg/tools/auto_approve.go's classifier — see
// autoRunsSample in pkg/agent/auto_approve_gate_helpers_test.go). Its NAME,
// not its implementation, is what the classifier keys off, so replacing the
// real tool with this stub still exercises the real Auto-approve
// classification path for a tool that DOES auto-run once Auto is active —
// unlike an unclassified made-up name, which would ask unconditionally and
// could never discriminate "Auto active" from "Auto not active".
type adr092FirstMsgProbeTool struct {
	tools.BaseTool
	ran chan struct{}
}

func (p *adr092FirstMsgProbeTool) Name() string        { return "browser_navigate" }
func (p *adr092FirstMsgProbeTool) Description() string { return "ADR-092 first-message test stub" }
func (p *adr092FirstMsgProbeTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (p *adr092FirstMsgProbeTool) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (p *adr092FirstMsgProbeTool) Execute(context.Context, map[string]any) *tools.ToolResult {
	close(p.ran)
	return &tools.ToolResult{ForLLM: "navigated"}
}

// adr092FirstMsgApprover records every RequestApproval call. A prompt shown
// to a human is exactly one call on this seam — the same instrument
// pkg/agent/auto_approve_gate_test.go's autoRecordingApprover uses.
type adr092FirstMsgApprover struct {
	calls   chan agent.PolicyApprovalReq
	approve bool
}

func (a *adr092FirstMsgApprover) RequestApproval(
	_ context.Context, req agent.PolicyApprovalReq,
) (bool, string, bool) {
	a.calls <- req
	if a.approve {
		return true, "", false
	}
	return false, "user", false
}

// adr092FirstMsgTurnProvider scripts exactly one ask-policy tool call
// (browser_navigate) before a final text response — one real LLM round trip
// each way, so the tool call genuinely gates on the approval path rather
// than a scripted no-op.
type adr092FirstMsgTurnProvider struct{}

func (p *adr092FirstMsgTurnProvider) Chat(
	_ context.Context, msgs []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	if n := len(msgs); n > 0 && msgs[n-1].Role == "tool" {
		return &providers.LLMResponse{Content: "done"}, nil
	}
	return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{
		ID: "call-nav", Type: "function", Name: "browser_navigate", Arguments: map[string]any{},
	}}}, nil
}

func (p *adr092FirstMsgTurnProvider) GetDefaultModel() string { return "test-model" }

// newFirstMessageAutoApproveHarness wires a real AgentLoop (browser_navigate
// stubbed and set to "ask" for "mia"), starts al.Run on its own goroutine
// exactly like gateway.go's production boot (rc.agentLoop.Run(rc.agentLoopCtx)),
// and returns everything a test needs to send one WS message frame and
// observe whether the turn's tool call reached the approver.
func newFirstMessageAutoApproveHarness(t *testing.T, globalAutoApprove bool) (*WSHandler, *adr092FirstMsgApprover, *adr092FirstMsgProbeTool) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	sandbox.RegisterTurnPolicyBase(&sandbox.TurnPolicyInput{HomePath: t.TempDir()})
	t.Cleanup(func() { sandbox.RegisterTurnPolicyBase(nil) })

	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080, DevModeBypass: true},
		Sandbox: config.OmnipusSandboxConfig{AutoApprove: globalAutoApprove},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{{
				ID: "mia",
				Tools: &config.AgentToolsCfg{Builtin: config.AgentBuiltinToolsCfg{
					Policies: map[string]config.ToolPolicy{"browser_navigate": config.ToolPolicyAsk},
				}},
			}},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &adr092FirstMsgTurnProvider{})

	probe := &adr092FirstMsgProbeTool{ran: make(chan struct{})}
	al.RegisterTool(probe)

	approver := &adr092FirstMsgApprover{calls: make(chan agent.PolicyApprovalReq, 4), approve: true}
	al.SetToolApprover(approver)

	runCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go al.Run(runCtx)

	handler := newWSHandler(msgBus, al, "")
	t.Cleanup(handler.Wait)
	return handler, approver, probe
}

// TestFirstMessageAutoApprove_GlobalOnPerChatFalse_FirstToolCallPrompts is
// the DANGEROUS direction the founder called out: global Auto is ON, but
// THIS chat's very first message explicitly turns it off for itself
// (`auto_approve: false`). The first turn's first (and only) ask-policy
// tool call must reach the approver — it must prompt — with no reliance on
// timing: this test does not race a session_mode_update against the turn,
// because there is no second frame to race any more.
func TestFirstMessageAutoApprove_GlobalOnPerChatFalse_FirstToolCallPrompts(t *testing.T) {
	handler, approver, probe := newFirstMessageAutoApproveHarness(t, true) // global Auto ON
	wc := makeTestConn()

	handler.handleChatMessageWithClientID(
		context.Background(), "adr092-chat-false", "", "go browse", "mia", nil,
		"", "", false, "client-msg-false", boolPtr(false), wc,
	)

	select {
	case req := <-approver.calls:
		assert.Equal(t, "browser_navigate", req.ToolName,
			"the first turn's first (and only) ask-policy tool call must be the one that prompts")
	case <-probe.ran:
		t.Fatal("browser_navigate ran WITHOUT ever reaching the approver — " +
			"the per-chat auto_approve:false choice on the first message did not gate the first tool call")
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the approver to be reached")
	}

	// The tool only runs after this test's approver grants it.
	select {
	case <-probe.ran:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for browser_navigate to run after approval")
	}
}

// TestFirstMessageAutoApprove_GlobalOffPerChatTrue_FirstToolCallAutoApproved
// is the mirror case: global Auto is OFF, but this chat's first message
// explicitly turns it ON for itself (`auto_approve: true`) — the one
// deliberate loosening exception in the ADR-092 contract, because a human is
// present in the chat to accept it. The first turn's first ask-policy tool
// call — one the classifier resolves to "runs" under Auto (browser_navigate,
// a catalog AutoRuns tool) — must be auto-approved with ZERO approver
// calls.
func TestFirstMessageAutoApprove_GlobalOffPerChatTrue_FirstToolCallAutoApproved(t *testing.T) {
	handler, approver, probe := newFirstMessageAutoApproveHarness(t, false) // global Auto OFF
	wc := makeTestConn()

	handler.handleChatMessageWithClientID(
		context.Background(), "adr092-chat-true", "", "go browse", "mia", nil,
		"", "", false, "client-msg-true", boolPtr(true), wc,
	)

	select {
	case <-probe.ran:
		// Auto-approved, as required.
	case req := <-approver.calls:
		t.Fatalf("browser_navigate reached the approver (tool=%s) — "+
			"the per-chat auto_approve:true choice on the first message did not auto-approve the first tool call",
			req.ToolName)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for browser_navigate to run")
	}

	require.Zero(t, len(approver.calls), "no approver call must have been queued behind the win in the select above")
}
