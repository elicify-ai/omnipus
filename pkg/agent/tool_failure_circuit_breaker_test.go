// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// UAT fix (fix/uat-defects-2026-08-22, Defect 1) regression coverage: proves
// the per-turn consecutive-identical-tool-failure circuit breaker
// (tool_failure_circuit_breaker.go) is actually reachable from runTurn's
// dispatch loop (loop.go), not merely present in source. Reproduces the
// UAT's shape directly — a tool that keeps failing with the SAME error for
// the SAME arguments — without needing a live provider or 55 real LLM round
// trips: one scripted LLM response batches N identical calls to a
// permanently-failing tool, mirroring
// TestRunTurn_ToolDenialBudget_AbortsAtTenNotEleven's structure one file
// over (tool_denial_quarantine_gate_test.go) for the SEC-26/ADR-058 sibling
// mechanism.
package agent

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
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// alwaysFailingStubTool always returns the SAME error, regardless of how
// many times it is called — the shape of both UAT failures (a saturated
// dispatch cap, an unwired store): a persistent, non-transient condition
// that keeps producing an identical result no matter how many times the
// model retries the exact same call.
type alwaysFailingStubTool struct {
	tools.BaseTool
	calls atomic.Int32
}

func (a *alwaysFailingStubTool) Name() string { return "always_failing_tool" }
func (a *alwaysFailingStubTool) Description() string {
	return "Test stub that always fails identically — circuit breaker regression coverage"
}
func (a *alwaysFailingStubTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (a *alwaysFailingStubTool) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (a *alwaysFailingStubTool) Execute(_ context.Context, _ map[string]any) *tools.ToolResult {
	a.calls.Add(1)
	return tools.ErrorResult("persistent failure: condition has not changed since the last attempt")
}

func (a *alwaysFailingStubTool) callCount() int { return int(a.calls.Load()) }

// TestRunTurn_ToolFailureCircuitBreaker_CapsIdenticalRetries drives one LLM
// response containing MORE calls than toolFailureCircuitBreakThreshold to a
// tool that fails identically every time, all with the exact same
// (name, arguments) signature — the UAT's run_task/create_task_in_workspace
// shape. Asserts:
//
//  1. The tool's Execute is actually invoked only
//     toolFailureCircuitBreakThreshold times, never more — proving the
//     breaker trips and stops real dispatch, not just that it logs a
//     warning the model can ignore.
//  2. Every call past the threshold is denied WITHOUT dispatching (a
//     ToolExecSkipped event, never reaching Execute).
//  3. The turn does not abort: it completes normally and reaches a SECOND
//     scripted LLM response, proving the breaker is a per-call denial (like
//     the SEC-26 rate limit it mirrors), not a turn-ending failure.
func TestRunTurn_ToolFailureCircuitBreaker_CapsIdenticalRetries(t *testing.T) {
	tmpHome := t.TempDir()
	workspaceDir := filepath.Join(tmpHome, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))

	const toolName = "always_failing_tool"
	const totalScripted = toolFailureCircuitBreakThreshold + 2 // past the trip point

	provider := testutil.NewScenario().
		WithToolCalls(distinctToolCalls(toolName, totalScripted)).
		WithText("acknowledged — I will stop retrying that call")

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
		Sandbox: config.OmnipusSandboxConfig{
			AuditLog: true,
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	defer al.Close()

	stub := &alwaysFailingStubTool{}
	al.RegisterTool(stub)
	setAskPolicyForAllAgents(t, al, toolName, config.ToolPolicyAllow)

	sub := al.SubscribeEvents(64)
	defer al.UnsubscribeEvents(sub.ID)

	finalContent, err := al.ProcessDirect(
		context.Background(),
		"please run always_failing_tool repeatedly",
		"test-session-tool-failure-circuit-breaker",
	)

	require.NoError(t, err,
		"the turn must complete normally — the circuit breaker denies individual calls, "+
			"it must never abort the turn itself")
	assert.Equal(t, "acknowledged — I will stop retrying that call", finalContent,
		"the turn must reach the SECOND scripted LLM response, proving it continued past the breaker")

	// (1) The core proof: Execute is called EXACTLY
	// toolFailureCircuitBreakThreshold times, never totalScripted times. A
	// do-nothing implementation would report totalScripted here.
	assert.Equal(t, toolFailureCircuitBreakThreshold, stub.callCount(),
		"CRITICAL: the tool executed more than toolFailureCircuitBreakThreshold times for an "+
			"identical, persistently-failing call — the circuit breaker is not capping real dispatch")

	// (2) Every call past the threshold must be denied without dispatch: a
	// ToolExecSkipped event whose reason names the circuit breaker, never a
	// ToolExecStart/Result pair for that call.
	events := collectEventStream(sub.C)
	var breakerDenials int
	for _, evt := range events {
		if evt.Kind != EventKindToolExecSkipped {
			continue
		}
		payload, ok := evt.Payload.(ToolExecSkippedPayload)
		require.True(t, ok, "expected ToolExecSkippedPayload, got %T", evt.Payload)
		if payload.Tool != toolName {
			continue
		}
		assert.Contains(t, payload.Reason, "circuit breaker",
			"skip reason should name the circuit breaker so an operator reading events can tell why")
		breakerDenials++
	}
	assert.Equal(t, totalScripted-toolFailureCircuitBreakThreshold, breakerDenials,
		"expected exactly the calls past the trip point to be denied pre-dispatch")

	if _, ok := findEvent(events, EventKindError); ok {
		t.Error("no EventKindError expected — the turn must complete without aborting")
	}
}
