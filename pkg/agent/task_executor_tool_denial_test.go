// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-058 (tool-denial semantics) — W5/Wave 2's plan-task proof: BDD-08 /
// AC-04.
//
// Traces to: docs/internal/specs/adr-058-tool-denial-semantics-spec.md §6
// BDD-08 (line ~418) and §7's traceability row "FR-058-13 | §10.A1 | BDD-08 |
// pkg/agent::task_executor_tool_denial_test.go | AC-04". Ownership per §9's
// wave table: W5 owns pkg/agent/loop_tool_denial_test.go AND this file.
//
// What Wave 1/Wave 2's OTHER files already prove, and what this file does
// NOT re-prove: pkg/agent/tool_denial_quarantine_gate_test.go (W4) already
// proves the aggregate budget fires at exactly the 10th denial inside
// runTurn's dispatch loop, that calls 11/12 are never dispatched, and that
// the abort reason names tool+reason+budget — at the AgentLoop.ProcessDirect
// level. This file's distinct job is the ONE additional hop those tests do
// not exercise: a REAL task.Store-backed TaskExecutor dispatching a REAL
// task through ExecuteTask -> runTask -> processTaskDirect -> runAgentLoop
// -> abortTurn -> finishTaskRun -> failTask, proving the same abort reason
// survives that whole chain into the PERSISTED Task.Result a human or the UI
// actually reads — not just the in-memory error returned by ProcessDirect.
//
// Verification bar (spec §8.1): no spy that records its argument. The
// approver here is countingDenyApprover (already defined in
// tool_denial_quarantine_gate_test.go, same package) — its call COUNT is the
// fact under test (did the quarantine gate suppress rounds 2-10?), not a
// canned return value standing in for something else. The task, the store,
// the TaskExecutor and the AgentLoop are all real; nothing about the
// dispatch/finish path is mocked.
package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// deniedBashStub is a test-only tool literally named "bash" — BDD-08's own
// fixture tool name — so the persisted Task.Result can be asserted against
// the exact string "bash" rather than a paraphrase or a different stub
// name. Mirrors dangerousStubTool (scenario_runturn_test.go) exactly, except
// for the tool name.
//
// Registering a tool named "bash" on a bare pkg/agent.NewAgentLoop test
// harness does not collide with anything: the REAL sandboxed bash tool is
// wired by AgentLoop.WireBashDeps/WireTier13Deps, called only from
// gateway.go with deps (sandbox egress proxy, policy auditor, exec-tool
// config) this harness never constructs. NewAgentLoop itself registers no
// bash tool at all — see loop.go's WireBashDeps doc comment (called
// exclusively from gateway boot).
type deniedBashStub struct {
	tools.BaseTool
	wasCalled atomic.Bool
}

func (d *deniedBashStub) Name() string { return "bash" }
func (d *deniedBashStub) Description() string {
	return "Test-only stub named bash — must never execute"
}
func (d *deniedBashStub) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (d *deniedBashStub) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (d *deniedBashStub) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	d.wasCalled.Store(true)
	return &tools.ToolResult{ForLLM: "executed — this should never happen", IsError: false}
}

func TestFinishTaskRun_ToolDenialBudgetAbort_TaskLandsFailedNamingToolReasonAgent(t *testing.T) {
	tmpHome := t.TempDir()
	workspaceDir := filepath.Join(tmpHome, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))

	// BDD-08's three DISTINCT fixture values (spec §8.1 "distinct fixture
	// values everywhere" — a test must not be able to pass by conflating two
	// of them). agentID is deliberately "mia" (the spec's own literal
	// example), toolName is "bash" (the spec's own literal example), and
	// denialReason is "timeout" (a PERMANENT reason per the classification
	// table, distinct from both).
	const agentID = "mia"
	const toolName = "bash"
	const denialReason = "timeout"

	// One LLM response containing exactly turnDenialBudget (10) parallel
	// calls to the same permanently-denied tool. No second scripted step:
	// FR-058-13 requires the budget to exhaust and the turn to abort INSIDE
	// this one batch, before a second LLM round-trip is ever attempted — if
	// the implementation under test failed to abort here, ScenarioProvider
	// would return ErrNoMoreResponses instead, which would surface as a
	// DIFFERENT error message and fail this test's exact-string assertion
	// below.
	provider := testutil.NewScenario().
		WithToolCalls(distinctToolCalls(toolName, turnDenialBudget))

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              workspaceDir,
				DefaultModel:      config.DefaultModel{Model: "scripted-model"},
				MaxTokens:         4096,
				MaxToolIterations: 20,
			},
			// A real, NAMED agent config entry for "mia" — required so
			// ExecuteTask's registry.GetAgent(t.AgentID) resolves it. There
			// is no implicit sentinel agent to fall back on anymore: BDD-08
			// requires the FAILING task be assigned to an agent whose own id
			// ("mia") is one of the three fixture values asserted in
			// Task.Result.
			List: []config.AgentConfig{
				{
					ID:    agentID,
					Home:  workspaceDir,
					Model: &config.AgentModelConfig{Primary: "scripted-model"},
				},
			},
		},
		Sandbox: config.OmnipusSandboxConfig{AuditLog: true},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	defer al.Close()

	stub := &deniedBashStub{}
	al.RegisterTool(stub)
	setAskPolicyForAllAgents(t, al, toolName, config.ToolPolicyAsk)

	approver := &countingDenyApprover{reason: denialReason}
	al.SetToolApprover(approver)

	storeDir := t.TempDir()
	store := task.New(filepath.Join(storeDir, "tasks"))

	te := &TaskExecutor{
		agentLoop:    al,
		store:        store,
		running:      make(map[string]*taskSlot),
		dispatchSema: newDispatchSemaphore(10),
	}

	tk := &task.Task{
		Title:       "adr-058-bdd-08-denial-budget",
		Prompt:      "please run bash repeatedly",
		Description: "BDD-08 fixture: a task whose tool is denied every time",
		AgentID:     agentID,
		Action:      task.ActionLLM,
		Priority:    3,
		WorkspaceID: "default",
		Status:      task.StatusNext,
	}
	require.NoError(t, store.Create(tk))

	require.NoError(t, te.ExecuteTask(context.Background(), tk.ID, nil),
		"ExecuteTask must accept the dispatch synchronously — the abort happens "+
			"later, inside the asynchronous runTask goroutine")

	// runTask executes asynchronously (ExecuteTask only claims + launches the
	// goroutine); poll the store for the terminal write finishTaskRun/failTask
	// produces.
	var final *task.Task
	require.Eventually(t, func() bool {
		got, err := store.Get(tk.ID)
		if err != nil || got.Status != task.StatusFailed {
			return false
		}
		final = got
		return true
	}, 5*time.Second, 20*time.Millisecond,
		"task must land StatusFailed within 5s of the aggregate denial budget exhausting")

	require.NotNil(t, final)
	assert.Equal(t, task.StatusFailed, final.Status)

	// --- AC-04's stub-resistance assertion (spec §8.2): the exact,
	// hop-by-hop chain (§3.8) — task_executor.finishTaskRun's "execution
	// error: %v" wrapper around abortTurn's "turn aborted during %s: %s"
	// wrapper around toolDenialAbortReason's own pinned format. This is
	// computed from the SAME production function the loop calls
	// (toolDenialAbortReason), not hand-duplicated, so it tracks the real
	// wording rather than asserting a stale copy of it. An implementation
	// that failed every task with a generic
	// "execution error: something went wrong" — which would still pass a
	// bare "Status == failed" or "Result != ''" check — fails this exact
	// match, because it cannot produce "bash", "timeout" and "mia" together
	// in the pinned shape.
	wantReason := toolDenialAbortReason(toolName, denialReason, agentID, turnDenialBudget)
	wantResult := fmt.Sprintf("execution error: turn aborted during tool_denial_budget: %s", wantReason)
	assert.Equal(t, wantResult, final.Result,
		"Task.Result must be the EXACT hop-by-hop chain from turn abort to persisted task result")

	// Individually, for a clearer failure message if the exact-match above
	// ever regresses for an unrelated formatting reason: the three distinct
	// fixture values must each appear.
	assert.Contains(t, final.Result, toolName, "Result must name the denied tool")
	assert.Contains(t, final.Result, denialReason, "Result must name the denial reason")
	assert.Contains(t, final.Result, agentID, "Result must name the agent")
	assert.Contains(t, final.Result, "tool_denial_budget", "Result must name the abort stage")

	// AC-04's explicit negative requirement: distinguishable from a human
	// Stop. failTask never writes CancelReason, so this also guards against
	// a future refactor that accidentally routes a denial-budget abort
	// through the Stop-cancellation code path instead of its own.
	assert.Empty(t, final.CancelReason, "a denial-budget abort must not be recorded as a user Stop")
	assert.NotContains(t, final.Result, memberCancelReasonMarker,
		"a denial-budget abort must not carry the Stop-cancellation marker")

	// N2 pin (spec §2.4): abortTurn's restoreSession failure branch would
	// return a DIFFERENT error (no "tool_denial_budget" stage in it at all)
	// — the exact-match assertion above already fails loudly if
	// restoreSession had failed, rather than silently mis-attributing the
	// cause.
	assert.Contains(t, wantResult, "tool_denial_budget",
		"sanity: the expected string itself must name the tool_denial_budget stage")

	// Real-mechanism proof, not "any failure landed": exactly ONE real
	// approval round-trip (call 1) — calls 2-10 are quarantine replays, per
	// FR-058-10/11, proven at the loop level by
	// TestRunTurn_ToolDenialBudget_AbortsAtTenNotEleven and re-verified here
	// end to end through the task-dispatch path specifically, since a
	// regression that only broke the ExecuteTask->runTask wiring (e.g. a
	// second, independent approver got constructed for task runs) would not
	// be caught by the pkg/agent/loop-level test alone.
	assert.Equal(t, 1, approver.callCount(),
		"only the FIRST of the ten identical denied calls should ever reach the approver "+
			"through the real task-dispatch path")
	assert.False(t, stub.wasCalled.Load(), "the permanently-denied bash tool must never execute")
}
