package agent

// Regression coverage for UAT 2026-09-13 D-81 (the breaker never escalated
// to a refusal at attempt 6) and D-23 (an oscillating create/delete loop of
// successful calls was never detected).

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestToolCircuitBreaker_RefusesAttemptSix is the D-81 unit oracle at the
// turnState level: five identical failures, and the sixth identical call is
// refused BEFORE dispatch — the streak never reaches six real attempts.
func TestToolCircuitBreaker_RefusesAttemptSix(t *testing.T) {
	ts := &turnState{}
	sig := toolCallSignature("bash", map[string]any{"command": "cat /nonexistent/path/xyz"})
	for attempt := 1; attempt < toolFailureCircuitBreakThreshold; attempt++ {
		_, tripped := ts.toolCircuitBreakerTripped(sig)
		require.False(t, tripped, "attempt %d must still dispatch", attempt)
		ts.recordToolFailure(sig)
	}
	reason, tripped := ts.toolCircuitBreakerTripped(sig)
	require.True(t, tripped, "attempt %d must be refused without dispatch", toolFailureCircuitBreakThreshold)
	assert.Contains(t, reason, `"bash"`)
	assert.Contains(t, reason, "5 times in a row")

	// A different call is unaffected, and the refusal is sticky for the turn.
	_, otherTripped := ts.toolCircuitBreakerTripped(toolCallSignature("bash", map[string]any{"command": "ls"}))
	assert.False(t, otherTripped)
	_, stillTripped := ts.toolCircuitBreakerTripped(sig)
	assert.True(t, stillTripped)
}

func TestDetectOscillation(t *testing.T) {
	cases := []struct {
		name    string
		history []string
		period  int
		cycles  int
	}{
		{"empty", nil, 0, 0},
		{"identical repeats are not an oscillation", []string{"A", "A", "A", "A", "A", "A"}, 0, 0},
		{"one cycle is not yet a repetition", []string{"A", "B"}, 0, 0},
		{"two cycles of period 2", []string{"A", "B", "A", "B"}, 2, 2},
		{"five cycles of period 2 with a prefix", []string{"X", "A", "B", "A", "B", "A", "B", "A", "B", "A", "B"}, 2, 5},
		{"period 3", []string{"A", "B", "C", "A", "B", "C", "A", "B", "C"}, 3, 3},
		{"period 2 pattern broken at the end", []string{"A", "B", "A", "B", "A", "B", "C"}, 0, 0},
		{"half cycle at the end counts the complete cycles only", []string{"A", "B", "A", "B", "A"}, 2, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, c := detectOscillation(tc.history)
			assert.Equal(t, tc.cycles, c, "cycles")
			if tc.cycles > 0 {
				assert.Equal(t, tc.period, p, "period")
			}
		})
	}
}

// TestToolCircuitBreaker_OscillationWarnsThenRefuses drives the D-23 shape
// at the turnState level: create X / delete X alternating, every call
// succeeding. The notice appears at the third full cycle; the call that
// would complete the fifth cycle is refused; a call that breaks the pattern
// is allowed.
func TestToolCircuitBreaker_OscillationWarnsThenRefuses(t *testing.T) {
	ts := &turnState{}
	create := toolCallSignature("create_record_type", map[string]any{"name": "describe placeholder"})
	del := toolCallSignature("delete_record_type", map[string]any{"name": "describe placeholder"})

	var notices int
	for cycle := 1; cycle <= 4; cycle++ {
		for _, sig := range []string{create, del} {
			_, tripped := ts.toolCircuitBreakerTripped(sig)
			require.False(t, tripped, "cycle %d must still dispatch", cycle)
			ts.recordToolSuccess(sig)
			if n := ts.recordToolCallForLoopDetection(sig); n != "" {
				notices++
				assert.Contains(t, n, "repeat the same 2-call cycle")
			}
		}
		if cycle < oscillationWarnCycles {
			assert.Equal(t, 0, notices, "no notice before %d full cycles", oscillationWarnCycles)
		}
	}
	assert.Greater(t, notices, 0, "a notice must have been issued from the third full cycle on")

	// Ninth call (create) is still allowed — it only starts the fifth cycle.
	_, tripped := ts.toolCircuitBreakerTripped(create)
	require.False(t, tripped)
	ts.recordToolSuccess(create)
	_ = ts.recordToolCallForLoopDetection(create)

	// Tenth call (delete) would COMPLETE the fifth cycle: refused.
	reason, tripped := ts.toolCircuitBreakerTripped(del)
	require.True(t, tripped, "the call completing the %dth repetition must be refused", oscillationBreakCycles)
	assert.Contains(t, reason, "5th repetition of the same 2-call cycle")
	assert.Contains(t, reason, `"delete_record_type"`)

	// Repeating the refused call is refused again (nothing was appended).
	_, tripped = ts.toolCircuitBreakerTripped(del)
	assert.True(t, tripped)

	// A call that breaks the pattern is allowed.
	_, tripped = ts.toolCircuitBreakerTripped(toolCallSignature("knowledge_describe", map[string]any{}))
	assert.False(t, tripped)
}

type countingOKStubTool struct {
	tools.BaseTool
	name  string
	calls atomic.Int32
}

func (c *countingOKStubTool) Name() string { return c.name }
func (c *countingOKStubTool) Description() string {
	return "always succeeds — oscillation regression"
}
func (c *countingOKStubTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (c *countingOKStubTool) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (c *countingOKStubTool) Execute(_ context.Context, _ map[string]any) *tools.ToolResult {
	c.calls.Add(1)
	return &tools.ToolResult{ForLLM: "ok", ForUser: "ok"}
}

// TestRunTurn_OscillationBreaker_RefusesFifthCycle is the D-23 reproduction
// through the real turn loop: ten alternating, always-succeeding calls in one
// LLM response. The pre-dispatch breaker must refuse the call that completes
// the fifth cycle, so the create stub runs 5 times and the delete stub 4.
func TestRunTurn_OscillationBreaker_RefusesFifthCycle(t *testing.T) {
	tmpHome := t.TempDir()
	workspaceDir := filepath.Join(tmpHome, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))

	calls := make([]providers.ToolCall, 0, 10)
	for i := 0; i < 10; i++ {
		name := "osc_create"
		if i%2 == 1 {
			name = "osc_delete"
		}
		fc := providers.FunctionCall{Name: name, Arguments: `{}`}
		calls = append(calls, providers.ToolCall{ID: name + "-" + string(rune('a'+i)), Function: &fc})
	}
	provider := testutil.NewScenario().
		WithToolCalls(calls).
		WithText("stopping the loop")

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              workspaceDir,
				DefaultModel:      config.DefaultModel{Model: "scripted-model"},
				MaxTokens:         4096,
				MaxToolIterations: 30,
			},
			List: []config.AgentConfig{{ID: "mia", Home: workspaceDir}},
		},
		Sandbox: config.OmnipusSandboxConfig{AuditLog: true},
	}
	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	defer al.Close()

	create := &countingOKStubTool{name: "osc_create"}
	del := &countingOKStubTool{name: "osc_delete"}
	al.RegisterTool(create)
	al.RegisterTool(del)
	// One policy map carrying BOTH tools: setAskPolicyForAllAgents replaces
	// the whole map, so calling it twice would drop the first tool's entry.
	for _, agentID := range al.GetRegistry().ListAgentIDs() {
		if inst, ok := al.GetRegistry().GetAgent(agentID); ok {
			inst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{
				"osc_create": config.ToolPolicyAllow,
				"osc_delete": config.ToolPolicyAllow,
			}})
		}
	}

	sub := al.SubscribeEvents(64)
	defer al.UnsubscribeEvents(sub.ID)

	finalContent, err := al.ProcessDirect(context.Background(), "loop please", "test-session-oscillation-breaker")
	require.NoError(t, err)
	assert.Equal(t, "stopping the loop", finalContent)

	assert.Equal(t, int32(5), create.calls.Load(), "create runs five times (it starts the fifth cycle)")
	assert.Equal(t, int32(4), del.calls.Load(), "delete runs four times — the fifth is refused pre-dispatch")

	var skipped int
	for _, evt := range collectEventStream(sub.C) {
		if evt.Kind != EventKindToolExecSkipped {
			continue
		}
		payload, ok := evt.Payload.(ToolExecSkippedPayload)
		require.True(t, ok)
		if payload.Tool == "osc_delete" {
			assert.Contains(t, payload.Reason, "repetition of the same 2-call cycle")
			skipped++
		}
	}
	assert.Equal(t, 1, skipped)
}
