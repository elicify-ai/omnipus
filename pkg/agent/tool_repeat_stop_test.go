// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Regression coverage for the identical SUCCESSFUL repetition stop
// (tool_failure_circuit_breaker.go: toolRepeatWarnThreshold = 4,
// toolRepeatStopThreshold = 8; wired in loop.go's tool-result branch and at
// the end of each tool round). Two live cases on 2026-09-14:
//
//   - UAT B-1 run 4: set_todos called 137 times in a row, every call a success.
//   - Mia polling a stuck task with list_jobs (174 calls) and list_tasks (8).
//
// Expected counts below are the design values written as literals (warn at
// the 4th identical success, stop at the 8th), not read back from the
// constants, so a silent change to either threshold fails these tests.
package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// fixedResultTool is a read-only stub that returns the same successful result
// on every call — a poll of a job whose state never changes.
type fixedResultTool struct {
	tools.BaseTool
	name   string
	result string
	calls  atomic.Int32
}

func (f *fixedResultTool) Name() string        { return f.name }
func (f *fixedResultTool) Description() string { return "read-only stub for repetition-stop coverage" }

// Parameters declares the one filter argument the scripted polls send; the
// registry's argument validation rejects undeclared properties, which would
// turn every poll into a FAILURE and exercise the failure streak instead.
func (f *fixedResultTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"status": map[string]any{"type": "string", "description": "status filter"},
	}}
}
func (f *fixedResultTool) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (f *fixedResultTool) Execute(_ context.Context, _ map[string]any) *tools.ToolResult {
	f.calls.Add(1)
	return tools.NewToolResult(f.result)
}

// allowToolsForAllAgents grants explicit allow for every name at once
// (setAskPolicyForAllAgents replaces the whole map, so calling it per tool
// would leave only the last tool allowed).
func allowToolsForAllAgents(t *testing.T, al *AgentLoop, names ...string) {
	t.Helper()
	policies := make(map[string]config.ToolPolicy, len(names))
	for _, n := range names {
		policies[n] = config.ToolPolicyAllow
	}
	for _, agentID := range al.GetRegistry().ListAgentIDs() {
		if agentInst, ok := al.GetRegistry().GetAgent(agentID); ok {
			agentInst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: policies})
		}
	}
}

// newRepeatStopLoop builds an agent loop for agent "mia" with a generous
// iteration cap (so the cap can never be what ends these turns) and a default
// workspace on disk (set_todos needs one to succeed).
func newRepeatStopLoop(t *testing.T, provider providers.LLMProvider) *AgentLoop {
	t.Helper()
	tmpHome := t.TempDir()
	workspaceDir := filepath.Join(tmpHome, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))
	wsDir := filepath.Join(tmpHome, "workspaces")
	require.NoError(t, os.MkdirAll(wsDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, "repeat-default.json"),
		[]byte(`{"id":"repeat-default","is_default":true}`), 0o600))

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              workspaceDir,
				DefaultModel:      config.DefaultModel{Model: "scripted-model"},
				MaxTokens:         4096,
				MaxToolIterations: 60,
			},
			List: []config.AgentConfig{{ID: "mia", Home: workspaceDir}},
		},
		Sandbox: config.OmnipusSandboxConfig{AuditLog: true},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	t.Cleanup(al.Close)
	return al
}

// scriptRounds appends n single-call rounds of name(args) with unique call IDs.
func scriptRounds(p *testutil.ScenarioProvider, prefix, name, args string, n int) {
	for i := 0; i < n; i++ {
		fc := providers.FunctionCall{Name: name, Arguments: args}
		p.WithToolCalls([]providers.ToolCall{{ID: fmt.Sprintf("%s-%s-%02d", prefix, name, i), Function: &fc}})
	}
}

// toolMessagesContaining counts tool-role messages across every provider
// request whose content contains needle.
func toolMessagesContaining(p *testutil.ScenarioProvider, needle string) int {
	n := 0
	for _, req := range p.AllRequests() {
		for _, m := range req {
			if m.Role == "tool" && strings.Contains(m.Content, needle) {
				n++
			}
		}
	}
	return n
}

func TestRunTurn_IdenticalSuccessRepeat_SetTodos_StopsTurnWithVisibleNotice(t *testing.T) {
	provider := testutil.NewScenario()
	scriptRounds(provider, "r4", "set_todos", uatRun4SetTodosArgs, 20)
	provider.WithText("unreachable: the repetition stop must end the turn first")

	al := newRepeatStopLoop(t, provider)
	agentInst, ok := al.GetRegistry().GetAgent("mia")
	require.True(t, ok)
	realSetTodos, ok := agentInst.Tools.Get("set_todos")
	require.True(t, ok, "the production set_todos tool must be registered")
	counter := &countingPassthroughTool{Tool: realSetTodos}
	al.RegisterTool(counter)
	allowToolsForAllAgents(t, al, "set_todos")

	finalContent, err := al.ProcessDirect(context.Background(), "/goal improve it", "test-session-repeat-set-todos")
	require.NoError(t, err)

	require.Zero(t, counter.errors.Load(), "every set_todos call must succeed, as in the UAT")
	assert.Equal(t, int32(8), counter.calls.Load(),
		"the 8th consecutive identical success must be the last dispatch of the turn")
	assert.Equal(t, 8, provider.CallCount(),
		"no further provider round may be spent after the stop is requested")
	assert.Equal(t,
		"I stopped this turn: I called `set_todos` with the same arguments 8 times in a row without making "+
			"progress. Tell me how you would like to proceed, or ask me to try a different approach.",
		finalContent, "the turn must end on the visible repetition notice")
	assert.Positive(t,
		toolMessagesContaining(provider, `called "set_todos" with these exact arguments 4 times in a row`),
		"the model must have been warned at the 4th identical success, before the stop")
}

func TestRunTurn_IdenticalSuccessRepeat_ReadOnlyPolling_StopsTurn(t *testing.T) {
	const pollArgs = `{"status":"running"}`
	provider := testutil.NewScenario()
	scriptRounds(provider, "a", "list_jobs", pollArgs, 3)
	scriptRounds(provider, "b", "list_tasks", pollArgs, 1)
	scriptRounds(provider, "c", "list_jobs", pollArgs, 12)
	provider.WithText("unreachable: the repetition stop must end the turn first")

	al := newRepeatStopLoop(t, provider)
	jobs := &fixedResultTool{name: "list_jobs", result: `{"jobs":[{"id":"t1","status":"verifying"}]}`}
	tasks := &fixedResultTool{name: "list_tasks", result: `{"tasks":[{"id":"t1","status":"verifying"}]}`}
	al.RegisterTool(jobs)
	al.RegisterTool(tasks)
	allowToolsForAllAgents(t, al, "list_jobs", "list_tasks")

	finalContent, err := al.ProcessDirect(context.Background(), "is my task done yet?", "test-session-repeat-polling")
	require.NoError(t, err)

	// The list_tasks call in between ends the first list_jobs run (3), so the
	// stop comes at the 8th list_jobs AFTER it: 3 + 8 = 11.
	assert.Equal(t, int32(11), jobs.calls.Load(), "list_jobs dispatches before the stop")
	assert.Equal(t, int32(1), tasks.calls.Load(), "list_tasks dispatches")
	assert.Equal(t, 12, provider.CallCount(), "provider rounds: 3 + 1 + 8, none after the stop")
	assert.Contains(t, finalContent, "I called `list_jobs` with the same arguments 8 times in a row",
		"a read-only polling loop must end with the same visible notice")
}

func TestRunTurn_CallsBelowRepeatStop_CompleteNormally(t *testing.T) {
	const pollArgs = `{"status":"running"}`

	t.Run("alternating_polls_never_form_a_run", func(t *testing.T) {
		provider := testutil.NewScenario()
		for i := 0; i < 10; i++ {
			scriptRounds(provider, fmt.Sprintf("j%02d", i), "list_jobs", pollArgs, 1)
			scriptRounds(provider, fmt.Sprintf("t%02d", i), "list_tasks", pollArgs, 1)
		}
		provider.WithText("done")

		al := newRepeatStopLoop(t, provider)
		jobs := &fixedResultTool{name: "list_jobs", result: `{"jobs":[]}`}
		tasks := &fixedResultTool{name: "list_tasks", result: `{"tasks":[]}`}
		al.RegisterTool(jobs)
		al.RegisterTool(tasks)
		allowToolsForAllAgents(t, al, "list_jobs", "list_tasks")

		finalContent, err := al.ProcessDirect(context.Background(), "check both lists", "test-session-repeat-alternating")
		require.NoError(t, err)
		assert.Equal(t, "done", finalContent, "20 calls that never repeat back-to-back must not stop the turn")
		assert.Equal(t, int32(10), jobs.calls.Load())
		assert.Equal(t, int32(10), tasks.calls.Load())
	})

	t.Run("seven_identical_successes_is_below_the_stop", func(t *testing.T) {
		provider := testutil.NewScenario()
		scriptRounds(provider, "s", "list_jobs", pollArgs, 7)
		provider.WithText("done")

		al := newRepeatStopLoop(t, provider)
		jobs := &fixedResultTool{name: "list_jobs", result: `{"jobs":[]}`}
		al.RegisterTool(jobs)
		allowToolsForAllAgents(t, al, "list_jobs")

		finalContent, err := al.ProcessDirect(context.Background(), "poll a few times", "test-session-repeat-seven")
		require.NoError(t, err)
		assert.Equal(t, "done", finalContent, "7 identical successes is one below the stop and must complete")
		assert.Equal(t, int32(7), jobs.calls.Load())
	})
}
