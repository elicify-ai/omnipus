// auto_approve_gate_test.go: ADR-092 D9 loop wiring — tests T1 (loop half),
// T3, T4, T5, T11, T12, T14, T15 and the production-wiring reachability test
// (docs/internal/specs/adr-092-auto-for-other-tools-design.md §9).
//
// Expected values come from the spec and the founder file
// (/Users/danielpiatkowski/Desktop/auto-approve-choices.json), never from the
// implementation. The instrument is a recording approver: one prompt shown
// to a human is exactly one RequestApproval call.

package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// goldenAutoAskList is the founder file's 28 catalog "asks" entries (§3),
// copied literally. Under Auto each still prompts.
var goldenAutoAskList = []string{
	"request_mount", "install_skill", "environment_setup", "serve_web",
	"send_email", "reply", "delete_task", "browser_evaluate", "browser_upload_file",
	"set_config", "run_doctor", "configure_provider", "test_provider",
	"enable_channel", "disable_channel", "configure_channel", "test_channel",
	"add_mcp_server", "remove_mcp_server", "create_agent", "update_agent", "delete_agent",
	"update_workspace", "delete_workspace", "delete_task_in_workspace",
	"create_skill", "edit_skill", "remove_skill",
}

// autoRunsSample is T4's sample of unconditional RUNS tools (§9 T4).
var autoRunsSample = []string{"delegate", "browser_navigate", "send_message", "knowledge_edit", "get_config"}

// runStubTurn drives one real turn in which the model calls every named stub
// once, and returns the approver and the stubs.
func runStubTurn(t *testing.T, autoApprove bool, names []string) (*autoRecordingApprover, map[string]*autoStubTool, *AgentLoop) {
	t.Helper()
	provider := testutil.NewScenario().WithToolCalls(stubToolCalls(names)).WithText("done")
	al := newAutoTestLoop(t, provider, autoApprove, nil)
	stubs := installAutoStubs(t, al, "mia", names)
	approver := &autoRecordingApprover{approve: true}
	al.SetToolApprover(approver)
	_, err := al.ProcessDirect(context.Background(), "use the tools", "auto-gate-turn")
	require.NoError(t, err)
	return approver, stubs, al
}

// T3: every ASKS tool on Ask with Auto on (kernel sandbox enforcing) still
// prompts, exactly once, and runs only after that approval.
func TestAutoApprove_T3_AskListToolsStillPrompt(t *testing.T) {
	require.Len(t, goldenAutoAskList, 28, "the founder file lists 28 catalog asks")
	withKernelSandbox(t)
	approver, stubs, _ := runStubTurn(t, true, goldenAutoAskList)
	assert.Len(t, approver.requests(), len(goldenAutoAskList))
	for _, name := range goldenAutoAskList {
		assert.Equal(t, 1, approver.countFor(name), "%s is on the ask-list and must prompt once under Auto", name)
		assert.Equal(t, int32(1), stubs[name].calls.Load(), "%s runs after the human approved it", name)
		assert.False(t, stubs[name].pinned.Load(), "%s was prompted, so no Auto decision is pinned", name)
	}
}

// T4 + T11 + T12: unconditional RUNS tools on Ask run with zero approver
// calls, each writes exactly one tool.auto_approved row with class "runs",
// and none records a grant. A prompted ASKS call in the same turn writes no
// tool.auto_approved row.
func TestAutoApprove_T4_T11_T12_RunsToolsRunWithoutPrompt(t *testing.T) {
	withKernelSandbox(t)
	names := append(append([]string(nil), autoRunsSample...), "delete_task")
	provider := testutil.NewScenario().WithToolCalls(stubToolCalls(names)).WithText("done")
	al := newAutoTestLoop(t, provider, true, nil)
	stubs := installAutoStubs(t, al, "mia", names)
	approver := &autoRecordingApprover{approve: true}
	al.SetToolApprover(approver)
	readAudit := swapAuditLogger(t, al)

	_, err := al.ProcessDirect(context.Background(), "use the tools", "auto-runs-turn")
	require.NoError(t, err)

	for _, name := range autoRunsSample {
		assert.Zero(t, approver.countFor(name), "T4: %s is RUNS and must not prompt under Auto", name)
		assert.Equal(t, int32(1), stubs[name].calls.Load(), "T4: %s must run", name)
		assert.True(t, stubs[name].pinned.Load(), "T4: the Auto decision must be pinned on %s's context", name)
		sessionID, agentID := stubs[name].identity()
		require.NotEmpty(t, sessionID)
		assert.False(t, al.ApprovalGrants().IsAllowed(sessionID, agentID, name, map[string]any{}),
			"T12: an auto-approved %s call must record no grant", name)
	}
	assert.Equal(t, 1, approver.countFor("delete_task"), "the ask-list tool in the same turn still prompts")

	rows := readAudit()
	for _, name := range autoRunsSample {
		got := auditRowsFor(rows, audit.EventToolAutoApproved, name)
		require.Len(t, got, 1, "T11: exactly one tool.auto_approved row for %s", name)
		details, _ := got[0]["details"].(map[string]any)
		assert.Equal(t, "runs", details["class"], "T11: class for %s", name)
		assert.Equal(t, "mia", got[0]["agent_id"])
	}
	assert.Empty(t, auditRowsFor(rows, audit.EventToolAutoApproved, "delete_task"),
		"T11: a prompted call writes no tool.auto_approved row")
}

// T5 (J13): with Auto off, or Auto on but no kernel sandbox enforcing, T4's
// RUNS tools prompt once each.
func TestAutoApprove_T5_InactiveAutoPrompts(t *testing.T) {
	for _, tc := range []struct {
		name        string
		autoApprove bool
		sandbox     bool
	}{
		{"auto off, sandbox enforcing", false, true},
		{"auto on, no kernel sandbox", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.sandbox {
				withKernelSandbox(t)
			} else {
				sandbox.RegisterTurnPolicyBase(nil)
			}
			approver, stubs, _ := runStubTurn(t, tc.autoApprove, autoRunsSample)
			for _, name := range autoRunsSample {
				assert.Equal(t, 1, approver.countFor(name), "%s must prompt when Auto is not active", name)
				assert.False(t, stubs[name].pinned.Load(), "%s must carry no Auto pin", name)
			}
		})
	}
}

// writeFileTurn drives one real turn in which the model makes the scripted
// write_file calls against the agent's real write_file tool, with write_file
// on Ask and a denying recording approver.
func writeFileTurn(t *testing.T, autoApprove bool, mutate func(*config.Config), calls ...providers.ToolCall) (*autoRecordingApprover, *testutil.ScenarioProvider) {
	t.Helper()
	scripted := testutil.NewScenario().WithToolCalls(calls).WithText("done")
	al := newAutoTestLoop(t, scripted, autoApprove, mutate)
	inst, ok := al.GetRegistry().GetAgent("mia")
	require.True(t, ok)
	_, registered := inst.Tools.Get("write_file")
	require.True(t, registered, "the production wiring registers write_file")
	installAutoStubs(t, al, "mia", nil, "write_file")
	approver := &autoRecordingApprover{approve: false}
	al.SetToolApprover(approver)
	_, err := al.ProcessDirect(context.Background(), "write the files", "auto-write-turn")
	require.NoError(t, err)
	return approver, scripted
}

// T1 (loop half): write_file on Ask, Auto on, kernel sandbox enforcing. A
// path in the work folder runs with no approver call; a path outside the
// workspace prompts exactly once.
func TestAutoApprove_T1_WriteFileInsideRunsOutsidePrompts(t *testing.T) {
	withKernelSandbox(t)
	outside := filepath.Join(t.TempDir(), "outside.md")
	approver, provider := writeFileTurn(t, true, nil,
		autoToolCall("wf-inside", "write_file", `{"path":"notes/t1.md","content":"inside"}`),
		autoToolCall("wf-outside", "write_file", `{"path":"`+outside+`","content":"outside"}`),
	)
	reqs := approver.requests()
	require.Len(t, reqs, 1, "only the outside path prompts")
	assert.Equal(t, outside, reqs[0].Args["path"])
	inside := toolResultText(t, provider, "wf-inside")
	assert.Contains(t, inside, "File written: notes/t1.md", "the inside write must run")
	assert.Contains(t, toolResultText(t, provider, "wf-outside"), "permission_denied")
	_, statErr := os.Stat(outside)
	assert.True(t, errors.Is(statErr, os.ErrNotExist), "the denied outside write must not happen")
}

// T14: God Mode is on, so Auto is inactive (no sandbox): an agent-level Ask
// on write_file still prompts for a path inside the work folder.
func TestAutoApprove_T14_GodModeStillPrompts(t *testing.T) {
	withKernelSandbox(t)
	setGodModeAvailable(true)
	t.Cleanup(func() { setGodModeAvailable(false) })
	approver, _ := writeFileTurn(t, true, func(c *config.Config) { c.Sandbox.GodMode = true },
		autoToolCall("wf-god", "write_file", `{"path":"notes/t14.md","content":"x"}`),
	)
	assert.Equal(t, 1, approver.countFor("write_file"), "Auto is inactive under God Mode, so Ask prompts")
}

// delegateUnderAutoChat spawns a real delegated sub-turn to "worker" from a
// parent chat whose per-chat Auto modifier is on (global Auto is off). The
// worker calls knowledge_edit — an unconditional RUNS tool — on Ask.
func delegateUnderAutoChat(t *testing.T, workerAutoDisabled bool) (*autoRecordingApprover, *autoStubTool) {
	t.Helper()
	withKernelSandbox(t)
	al, home := schedTestLoop(t)
	parent := registerAgent(t, al, home, "mia", testutil.NewScenario().WithText("parent idle"), true)
	worker := registerAgent(t, al, home, "worker",
		testutil.NewScenario().WithToolCall("knowledge_edit", `{}`).WithText("worker done"), false)
	al.cfg.Agents.List = append(al.cfg.Agents.List,
		config.AgentConfig{ID: "worker", AutoApproveDisabled: workerAutoDisabled})
	stubs := installAutoStubs(t, al, worker.ID, []string{"knowledge_edit"})
	approver := &autoRecordingApprover{approve: false}
	al.SetToolApprover(approver)

	parentSessionID, store := stiMintParentSession(t, al)
	al.SessionModes().Set(parentSessionID, true)
	parentTS := &turnState{
		ctx:                 context.Background(),
		turnID:              "parent-auto-delegate",
		agentID:             parent.ID,
		childTurnIDs:        []string{},
		pendingResults:      make(chan *tools.ToolResult, 4),
		concurrencySem:      make(chan struct{}, testMaxConcurrentSubTurns),
		session:             &ephemeralSessionStore{},
		agent:               parent,
		transcriptSessionID: parentSessionID,
		routingSessionID:    session.RoutingSessionID(parentSessionID),
		transcriptStore:     store,
	}
	res, err := spawnSubTurn(withSpawnToolCallID(context.Background(), "spawn-auto"), al, parentTS, SubTurnConfig{
		Model:         "test-model",
		SystemPrompt:  "edit the knowledge base",
		TargetAgentID: "worker",
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	return approver, stubs["knowledge_edit"]
}

// T15: a delegate whose own auto_approve_disabled is set still prompts for a
// RUNS tool under a parent chat with Auto on; the same delegate without the
// switch runs it unprompted (the control that proves the instrument sees the
// difference).
func TestAutoApprove_T15_DelegateOwnOffSwitchWins(t *testing.T) {
	t.Run("delegate switched off prompts", func(t *testing.T) {
		approver, stub := delegateUnderAutoChat(t, true)
		assert.Equal(t, 1, approver.countFor("knowledge_edit"), "the delegate's own off-switch wins")
		assert.Zero(t, stub.calls.Load(), "the prompt was denied, so the tool did not run")
	})
	t.Run("delegate without the switch inherits Auto", func(t *testing.T) {
		approver, stub := delegateUnderAutoChat(t, false)
		assert.Zero(t, approver.countFor("knowledge_edit"))
		assert.Equal(t, int32(1), stub.calls.Load())
		assert.True(t, stub.pinned.Load())
	})
	t.Run("a modifier on the delegate's own session does not beat its switch", func(t *testing.T) {
		withKernelSandbox(t)
		al := newGateTestLoop(t, "ask", false, config.AgentConfig{ID: "worker", AutoApproveDisabled: true})
		al.SessionModes().Set("child-session", true)
		assert.False(t, al.autoApproveActive("worker", "child-session", true), "delegated: own switch wins")
		assert.True(t, al.autoApproveActive("worker", "child-session", false),
			"a directly attached chat may still loosen past the agent switch (sessionmode.go)")
	})
}

// Reachability through the production wiring: policies come from config (no
// hand-stored policy, no stubs), the agent's tools are the ones NewAgentLoop
// registers. With Auto on and a kernel sandbox, an in-workspace write_file on
// Ask runs with zero approver calls and delete_task prompts once.
func TestAutoApprove_Reachability_ProductionWiring(t *testing.T) {
	withKernelSandbox(t)
	provider := testutil.NewScenario().WithToolCalls([]providers.ToolCall{
		autoToolCall("reach-write", "write_file", `{"path":"notes/reach.md","content":"reachable"}`),
		autoToolCall("reach-delete", "delete_task", `{"task_id":"no-such-task"}`),
	}).WithText("done")
	al := newAutoTestLoop(t, provider, true, func(c *config.Config) {
		c.Sandbox.ToolPolicies = map[string]string{"write_file": "ask", "delete_task": "ask"}
	})
	inst, ok := al.GetRegistry().GetAgent("mia")
	require.True(t, ok)
	for _, name := range []string{"write_file", "delete_task"} {
		_, registered := inst.Tools.Get(name)
		require.True(t, registered, "%s must be registered by the production wiring", name)
		require.Equal(t, "ask", al.ResolveApprovalToolPolicy("mia", name), "%s resolves to ask from config", name)
	}
	approver := &autoRecordingApprover{approve: false}
	al.SetToolApprover(approver)

	_, err := al.ProcessDirect(context.Background(), "write and delete", "auto-reach-turn")
	require.NoError(t, err)

	reqs := approver.requests()
	require.Len(t, reqs, 1, "exactly one prompt in the turn")
	assert.Equal(t, "delete_task", reqs[0].ToolName, "delete_task is on the ask-list")
	written := toolResultText(t, provider, "reach-write")
	assert.Contains(t, written, "File written: notes/reach.md", "write_file must run unprompted")
	assert.Contains(t, toolResultText(t, provider, "reach-delete"), "permission_denied")
}
