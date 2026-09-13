// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// verifier_budget_test.go — ADR-084 D9 (JUDGE-FR-051, FR-052) and FR-030
// m6's capture composite key. This is the delivery plan's "judge matrix's
// verifier_budget_adr084_test.go" (wave E2, ADR-084/085/086 joint delivery
// plan §3) — one file, this name.
//
// SCOPE NOTE (honest, not silently narrowed): the ORIGINAL judge spec's
// oracle for TestVerifierBudget_CapRefusalDoesNotKillTurn is "the 4th call's
// ToolResult matches the cap-refusal sentinel AND exactly 3 tool calls
// executed AND a verdict was produced" — a full verifier-adjudication
// integration the spec itself assigns to the Judge's real tool-using turn
// (pkg/agent/judge.go's runVerifierAdjudication, wave E9, outside this
// wave's write-set — that dispatch code does not exist yet). What this file
// proves instead, exactly matching this wave's actual Delivers
// (JUDGE-FR-051, FR-052; FR-030's capture mechanism): the VerifierBudget cap
// arithmetic in isolation, AND — via a real al.runTurn — that the
// pkg/agent/loop.go refusal wiring this wave adds produces an ordinary
// tool-result and lets the turn complete normally, never a turn-ending
// error, when the cap is already reached before the tool dispatches.
package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// TestVerifierBudget_ToolCallCapEnforced is JUDGE-FR-051's tool-call half:
// CheckCap must refuse once (and only once) the configured cap has been
// reached by calls already recorded via RecordToolCall.
func TestVerifierBudget_ToolCallCapEnforced(t *testing.T) {
	vb := NewVerifierBudget(2, 0, 0, 0)

	if _, capped := vb.CheckCap(); capped {
		t.Fatalf("CheckCap must not refuse before any call is recorded")
	}
	vb.RecordToolCall(10)
	if _, capped := vb.CheckCap(); capped {
		t.Fatalf("CheckCap must not refuse at 1 of 2 recorded calls")
	}
	vb.RecordToolCall(10)
	refusal, capped := vb.CheckCap()
	require.True(t, capped, "CheckCap must refuse once the tool-call cap (2) is reached")
	assert.Contains(t, refusal, "tool-call cap", "JUDGE-FR-052: the refusal must state which cap was reached")
	assert.Contains(t, refusal, "conclude", "JUDGE-FR-052: the refusal must instruct the Judge to conclude with what it has")
}

// TestVerifierBudget_BytesReadCapEnforced is JUDGE-FR-051's byte half: the
// SAME per-adjudication enforcement, keyed on cumulative tool-result bytes
// rather than call count.
func TestVerifierBudget_BytesReadCapEnforced(t *testing.T) {
	vb := NewVerifierBudget(0, 100, 0, 0)

	vb.RecordToolCall(60)
	if _, capped := vb.CheckCap(); capped {
		t.Fatalf("CheckCap must not refuse at 60 of 100 configured bytes")
	}
	vb.RecordToolCall(50) // cumulative 110 >= 100
	refusal, capped := vb.CheckCap()
	require.True(t, capped, "CheckCap must refuse once cumulative bytes (110) reach the 100-byte cap")
	assert.Contains(t, refusal, "byte cap", "JUDGE-FR-052: the refusal must state which cap was reached")
}

// TestVerifierBudget_NilIsANoOp: every VerifierBudget method must be safe
// and inert on a nil receiver, because verifierBudgetForTurn(turnID) returns
// nil for the overwhelming majority of turns (ordinary chat, never a
// verifier adjudication) and every call site in loop.go/tool_result_admit.go
// relies on that nil-safety rather than an explicit presence check before
// every method call.
func TestVerifierBudget_NilIsANoOp(t *testing.T) {
	var vb *VerifierBudget
	refusal, capped := vb.CheckCap()
	assert.False(t, capped)
	assert.Empty(t, refusal)
	assert.NotPanics(t, func() { vb.RecordToolCall(1_000_000) })
	assert.NotPanics(t, func() { vb.RecordTokens(1, 1) })
	assert.False(t, vb.TokensExceeded())
}

// TestGroundingCapture_KeyIsCompositeNotToolCallID is JUDGE-FR-030 m6: the
// capture key MUST be (iteration, tool_call_id), never tool_call_id alone —
// a provider can reuse a call id such as "call_0" on every turn iteration
// (pkg/agent/empty_in_place.go's B-29b notes), and a later iteration's
// result must not silently overwrite the one an earlier verdict grounded
// in. FAILS on a tool-call-id-only key (the bug this test exists to catch):
// under that key shape the second Record below would overwrite the first
// and Get(0, "call_0") would come back with iteration 1's content.
func TestGroundingCapture_KeyIsCompositeNotToolCallID(t *testing.T) {
	vc := NewVerifierCapture()
	vc.Record(0, verifierCaptureEntry{Tool: "read_file", ToolCallID: "call_0", Content: "iteration-0 content", Bytes: 20})
	vc.Record(1, verifierCaptureEntry{Tool: "read_file", ToolCallID: "call_0", Content: "iteration-1 content", Bytes: 20})

	e0, ok0 := vc.Get(0, "call_0")
	require.True(t, ok0, "iteration 0's entry must still be retrievable")
	assert.Equal(t, "iteration-0 content", e0.Content, "iteration 1's Record must not overwrite iteration 0's entry")

	e1, ok1 := vc.Get(1, "call_0")
	require.True(t, ok1)
	assert.Equal(t, "iteration-1 content", e1.Content)
}

// TestVerifierCapture_NilIsANoOp mirrors TestVerifierBudget_NilIsANoOp for
// VerifierCapture, for the same reason: verifierCaptureForTurn(turnID)
// returns nil for every non-verifier turn.
func TestVerifierCapture_NilIsANoOp(t *testing.T) {
	var vc *VerifierCapture
	assert.NotPanics(t, func() {
		vc.Record(0, verifierCaptureEntry{ToolCallID: "x"})
	})
	_, ok := vc.Get(0, "x")
	assert.False(t, ok)
	assert.Empty(t, vc.FlaggedToolCallIDs())
}

// capExhaustedToolProvider is a providers.LLMProvider double whose FIRST
// call always proposes one tool call (any name — it must never actually
// dispatch, since the budget below is pre-exhausted before runTurn starts,
// so the refusal must fire before any tool-name resolution, policy or hook
// runs) and whose every later call returns a plain final answer with no
// tool calls, ending the turn normally.
type capExhaustedToolProvider struct {
	model string
	calls int
}

func (p *capExhaustedToolProvider) Chat(
	_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	p.calls++
	if p.calls == 1 {
		return &providers.LLMResponse{
			ToolCalls: []providers.ToolCall{
				{ID: "call_1", Type: "function", Name: "read_file", Function: &providers.FunctionCall{Name: "read_file", Arguments: `{"path":"x.txt"}`}},
			},
		}, nil
	}
	return &providers.LLMResponse{Content: "concluded after refusal"}, nil
}

func (p *capExhaustedToolProvider) GetDefaultModel() string { return p.model }

// TestVerifierBudget_CapRefusalDoesNotKillTurn is JUDGE-FR-052's central
// claim, exercised through the REAL pkg/agent/loop.go wiring this wave adds
// (not a direct admitToolResult call): with the budget already at its
// tool-call cap before the turn starts, the very first tool call the model
// proposes must be refused — and the turn must still complete normally,
// producing the model's own final content, never a turn-ending error. See
// this file's package doc comment for why the spec's stronger "3 executed,
// 4th refused" oracle is out of this wave's scope.
func TestVerifierBudget_CapRefusalDoesNotKillTurn(t *testing.T) {
	const modelName = "cap-refusal-model"
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              t.TempDir(),
				DefaultModel:      config.DefaultModel{Model: modelName},
				MaxTokens:         4096,
				MaxToolIterations: 4,
			},
			List: []config.AgentConfig{{ID: "verifier-cap-agent", Home: t.TempDir()}},
		},
	}

	provider := &capExhaustedToolProvider{model: modelName}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	t.Cleanup(func() { al.Close() })

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent, "expected a default agent")

	opts := processOptions{
		SessionKey:      "verifier-cap-session",
		Channel:         "cli",
		ChatID:          "direct",
		UserMessage:     "review this",
		DefaultResponse: defaultResponse,
		SendResponse:    false,
	}
	ts := newTurnState(defaultAgent, opts, al.newTurnEventScope(defaultAgent.ID, opts.SessionKey))

	// Pre-exhaust the budget BEFORE the turn runs, so the very first tool
	// call the fake provider proposes is refused — proving the refusal
	// fires ahead of any tool-name resolution, quarantine, hook or policy
	// check (all of which sit later in the same loop body).
	vb := NewVerifierBudget(1, 0, 0, 0)
	vb.RecordToolCall(0)
	RegisterVerifierBudget(ts.turnID, vb)
	t.Cleanup(func() { UnregisterVerifierBudget(ts.turnID) })

	result, err := al.runTurn(context.Background(), ts)
	require.NoError(t, err, "JUDGE-FR-052: the turn MUST NOT be killed mid-flight by a cap refusal")
	assert.Equal(t, "concluded after refusal", result.finalContent,
		"the model's own final content must still be produced after the refused tool call")
	assert.Equal(t, 2, provider.calls, "the provider must be called a second time after the refusal (the turn continued)")
}
