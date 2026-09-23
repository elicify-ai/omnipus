// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// UAT 2026-09-14 (B-1 run 4) regression coverage. A goal turn on
// z-ai/glm-5.3 called set_todos 137 times in a row — every call a SUCCESS,
// 64 of them byte-identical in one unbroken run — until a human pressed
// Stop at iteration 141. Two hypotheses for "why nothing bounded it" are
// pinned false here, against the real set_todos tool on the real dispatch
// loop:
//
//   - "set_todos is exempt from the per-turn tool-iteration cap as a
//     bookkeeping tool": it is not — the tool executes exactly
//     MaxToolIterations times and no more.
//   - "the cap is absent or reset on this path": it is not — the turn ends
//     at the cap on its own, with the engine's visible max-iterations
//     notice as the final content, not by provider exhaustion.
//
// What this does NOT claim: that the cap is tight enough. The circuit
// breaker in tool_failure_circuit_breaker.go counts only identical
// FAILURES, so an identical SUCCESS streak is bounded only by the cap
// (200 by default). The live turn was 59 rounds short of it.
package agent

import (
	"context"
	"fmt"
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

// countingPassthroughTool wraps a REAL registered tool, delegating every
// method to it unchanged (embedding the interface promotes Name,
// Description, Parameters, Scope, Category), and counts Execute calls and
// error results. Wrapping rather than stubbing keeps the tool under test the
// production one.
type countingPassthroughTool struct {
	tools.Tool
	calls  atomic.Int32
	errors atomic.Int32
}

func (c *countingPassthroughTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	c.calls.Add(1)
	res := c.Tool.Execute(ctx, args)
	if res == nil || res.IsError {
		c.errors.Add(1)
	}
	return res
}

// uatRun4SetTodosArgs is the argument object run 4 repeated 64 times in a row
// (transcript session_01M2FBEJ5DA37MF63PFRCJJV4N, rows 48-111), with the
// transcript's original `goal` key replaced by `outcome` (the set_todos
// rename): the shape is otherwise unchanged, and this test cares about the
// iteration-cap behavior, not the field name.
const uatRun4SetTodosArgs = `{"outcome":"Improve the renewable energy report goal record and re-issue the registration",` +
	`"todos":[{"status":"in_progress","text":"Register the improved goal record via set_goal (mode: register)"},` +
	`{"status":"pending","text":"Continue goal work: improved criteria"}]}`

func TestRunTurn_IdenticalSuccessfulSetTodosForever_EndsAtIterationCapWithNotice(t *testing.T) {
	tmpHome := t.TempDir()
	workspaceDir := filepath.Join(tmpHome, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))
	// set_todos resolves its board card's workspace from the turn context or,
	// for a context-less ProcessDirect turn like this one, from the workspace
	// flagged is_default under filepath.Dir(AgentHomeBasePath()) == tmpHome
	// (loop.go's registration calls SetHome with exactly that). The live UAT
	// gateway had one; without it every call here errors and the test would
	// exercise the failure breaker instead of run 4's success streak.
	wsDir := filepath.Join(tmpHome, "workspaces")
	require.NoError(t, os.MkdirAll(wsDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, "b1-default.json"),
		[]byte(`{"id":"b1-default","is_default":true}`), 0o600))

	// Deliberately BELOW toolFailureCircuitBreakThreshold (6): this test pins
	// the iteration cap on its own, so no repetition mechanism may be able to
	// end the turn first.
	const maxIter = 4
	// Script far more identical rounds than the cap allows. If the cap did
	// not apply, the turn would run until the script is exhausted and the
	// final content would not be the max-iterations notice.
	const scriptedRounds = 4 * maxIter

	provider := testutil.NewScenario()
	for i := 0; i < scriptedRounds; i++ {
		fc := providers.FunctionCall{Name: "set_todos", Arguments: uatRun4SetTodosArgs}
		provider.WithToolCalls([]providers.ToolCall{{ID: fmt.Sprintf("set_todos-round-%02d", i), Function: &fc}})
	}
	provider.WithText("unreachable: the cap must end the turn before this step")

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              workspaceDir,
				DefaultModel:      config.DefaultModel{Model: "scripted-model"},
				MaxTokens:         4096,
				MaxToolIterations: maxIter,
			},
			List: []config.AgentConfig{{ID: "mia", Home: workspaceDir}},
		},
		Sandbox: config.OmnipusSandboxConfig{AuditLog: true},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	defer al.Close()

	agentInst, ok := al.GetRegistry().GetAgent("mia")
	require.True(t, ok, "the harness agent must be registered")
	realSetTodos, ok := agentInst.Tools.Get("set_todos")
	require.True(t, ok, "the production set_todos tool must be registered on the agent")
	counter := &countingPassthroughTool{Tool: realSetTodos}
	al.RegisterTool(counter)
	setAskPolicyForAllAgents(t, al, "set_todos", config.ToolPolicyAllow)

	finalContent, err := al.ProcessDirect(
		context.Background(),
		"/goal improve it",
		"test-session-runaway-identical-set-todos",
	)
	require.NoError(t, err, "hitting the iteration cap is a turn outcome, not a transport error")

	// Faithfulness guard: run 4's calls all SUCCEEDED. If set_todos errored
	// here, this test would be exercising the failure circuit breaker
	// instead of the success-streak shape the UAT actually hit.
	require.Zero(t, counter.errors.Load(),
		"every set_todos call must succeed, mirroring the UAT; an error here means the harness diverged from run 4")

	assert.Equal(t, int32(maxIter), counter.calls.Load(),
		"set_todos must execute exactly MaxToolIterations times: fewer means something else ended the turn, "+
			"more means the cap does not apply to this tool")
	assert.Equal(t, toolLimitResponse, finalContent,
		"the turn must end on the engine's visible max-iterations notice")
}
