// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// judge_check_workspace_reroot_test.go covers the S2 UAT finding (tester plan
// MARCUS-P4): a `check` Definition-of-Done criterion was dispatched through
// the assignee's bash tool WITHOUT the workspace re-root every ordinary agent
// turn gets (tools.WithTurnWorkspaceDir, loop.go's runTurn), so a relative-path
// check ran against the assignee's FIXED agent-home directory while the task's
// own turn had written its output into workspaces/<id>/work/ — a genuine,
// mechanically-run, non-denied, non-timed-out check reporting a false unmet
// on completed work.
//
// These tests drive JudgeCriteria with the REAL ExecTool (not the fakeBashTool
// double used elsewhere in this package) — in GodMode so no kernel sandbox
// setup is required in the test environment — so the working-directory
// resolution proven here is the actual pkg/tools/shell.go baseDir logic, not a
// mock's stand-in for it.
package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// marcusP4TestHome builds the on-disk fixture for the MARCUS-P4 scenario:
// a workspace (wsID) whose core_team contains assigneeID, with its work/ dir
// already containing the file the task's OWN turn would have written there,
// plus a completely separate, empty agent-home directory for assigneeID (the
// directory a bash check would run in if the workspace re-root is missing).
// Returns the AgentLoop, the workspace's work/ dir, and the assignee's fixed
// (private) home dir, so a test can assert exactly where a check ran.
func marcusP4TestHome(t *testing.T, wsID, assigneeID string) (al *AgentLoop, workDir, agentHome string) {
	t.Helper()
	tmpHome := t.TempDir()
	t.Setenv(config.EnvHome, tmpHome)

	workspacesDir := filepath.Join(tmpHome, "workspaces")
	require.NoError(t, os.MkdirAll(workspacesDir, 0o755))
	wsJSON := `{"id":"` + wsID + `","core_team":["` + assigneeID + `"]}`
	require.NoError(t, os.WriteFile(filepath.Join(workspacesDir, wsID+".json"), []byte(wsJSON), 0o644))

	workDir = filepath.Join(workspacesDir, wsID, "work")
	require.NoError(t, os.MkdirAll(workDir, 0o700))
	// Simulate: the task's own turn already did the work, for real, in the
	// workspace's shared work/ dir — exactly what MARCUS-P4 observed
	// (workspaces/<wsid>/work/marcus-happy-4.txt containing "OK").
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "marcus-happy-4.txt"), []byte("OK"), 0o644))

	agentHome = filepath.Join(tmpHome, "agents", assigneeID)
	require.NoError(t, os.MkdirAll(agentHome, 0o755))

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: t.TempDir(), DefaultModel: config.DefaultModel{Model: "test-model"}},
			List: []config.AgentConfig{
				{ID: assigneeID, Name: "Native Agent", Type: config.AgentTypeWorker, Home: agentHome},
				{ID: string(coreagent.IDJudge), Name: "Judge", Type: config.AgentTypeSystem, Home: t.TempDir()},
			},
		},
	}

	al = mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	t.Cleanup(func() { al.Close() })

	assigneeInst, ok := al.GetRegistry().GetAgent(assigneeID)
	require.True(t, ok, "assignee agent must be registered")
	allowBashPolicy(assigneeInst)

	// Swap in a GodMode-real ExecTool (real command execution, no kernel
	// sandbox setup needed in the test env) so the assertions below exercise
	// the REAL pkg/tools/shell.go baseDir/TurnWorkspaceDir resolution, not a
	// fake double standing in for it.
	execTool, err := tools.NewExecToolWithDeps(agentHome, false, nil, tools.ExecToolDeps{GodMode: true})
	require.NoError(t, err)
	assigneeInst.Tools.RegisterReplacing(execTool)

	return al, workDir, agentHome
}

// TestJudge_MachineCheck_RunsInTaskWorkspaceWorkDir is the direct MARCUS-P4
// regression: a `check` criterion with a RELATIVE path must succeed against a
// file the task genuinely created in its own workspace's work/ dir. Before
// the fix, runMachineCheck never set tools.WithTurnWorkspaceDir on the bash
// call's ctx, so ExecTool's baseDir fell back to the assignee's fixed
// agent-home dir (empty in this fixture) and `test -f marcus-happy-4.txt`
// genuinely, mechanically failed (exit 1) on work that was actually done.
func TestJudge_MachineCheck_RunsInTaskWorkspaceWorkDir(t *testing.T) {
	const wsID = "marcus-ws"
	const assigneeID = "native-agent"
	al, workDir, agentHome := marcusP4TestHome(t, wsID, assigneeID)

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-marcus-p4",
		AssigneeAgentID: assigneeID,
		WorkspaceID:     wsID,
		Criteria: []task.AcceptanceCriterion{
			machineCriterion("c-file", "test -f marcus-happy-4.txt", 0),
			// The tester's own one-step sanity check, made an automated
			// assertion below via the persisted evidence's Output: `pwd`
			// proves exactly which directory the check executed in.
			machineCriterion("c-pwd", "pwd", 0),
		},
		Attempt: 1,
	})

	if result.Unavailable {
		t.Fatalf("unexpected Unavailable: %s", result.Reason)
	}
	require.NotNil(t, result.Verdict)
	if !result.Verdict.Met {
		t.Fatalf("verdict.Met = false, want true (task's own output genuinely exists) — per-criterion: %+v",
			result.Verdict.PerCriterion)
	}
	for _, cv := range result.Verdict.PerCriterion {
		if cv.CriterionID == "c-file" && !cv.Met {
			t.Errorf("c-file (test -f marcus-happy-4.txt) unmet: %s", cv.Reason)
		}
	}

	// Automated pwd sanity check: the check's resolved cwd must be the
	// workspace's work/ dir, never the assignee's private agent-home dir.
	es := task.NewEvidenceStore(config.OmnipusHomeDir(), nil)
	records, err := es.List("t-marcus-p4")
	require.NoError(t, err)
	var pwdOutput string
	found := false
	for _, rec := range records {
		if rec.CriterionID == "c-pwd" {
			pwdOutput = strings.TrimSpace(rec.Output)
			found = true
		}
	}
	require.True(t, found, "expected persisted evidence for the c-pwd criterion")

	resolvedWorkDir, err := filepath.EvalSymlinks(workDir)
	require.NoError(t, err)
	resolvedPwd, err := filepath.EvalSymlinks(pwdOutput)
	require.NoError(t, err, "pwd output %q must be a real, resolvable directory", pwdOutput)
	if resolvedPwd != resolvedWorkDir {
		t.Errorf("check ran in %q, want the task's own workspace work dir %q (agent-home was %q)",
			resolvedPwd, resolvedWorkDir, agentHome)
	}
}

// TestJudge_MachineCheck_NoWorkspace_FallsBackToAgentHome proves the sane
// fallback for a task/adjudication with NO workspace at all (WorkspaceID ==
// "", e.g. an unbound-chat /goal verification — JudgeCriteriaInput.WorkspaceID's
// own doc comment: "may legitimately be empty there"): the check must still
// run — against the assignee's fixed agent-home dir, exactly as it did before
// this fix — never a hard error just because there is no work-under-review
// workspace to root against.
func TestJudge_MachineCheck_NoWorkspace_FallsBackToAgentHome(t *testing.T) {
	const wsID = "marcus-ws"
	const assigneeID = "native-agent"
	al, _, agentHome := marcusP4TestHome(t, wsID, assigneeID)

	// A file that exists ONLY in the assignee's own fixed agent-home dir,
	// never written into any workspace work/ dir.
	require.NoError(t, os.WriteFile(filepath.Join(agentHome, "goal-scope-file.txt"), []byte("OK"), 0o644))

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeGoal,
		GoalSessionID:   "goal-session-1",
		AssigneeAgentID: assigneeID,
		// WorkspaceID intentionally left "" — the unbound-chat case.
		Criteria: []task.AcceptanceCriterion{
			machineCriterion("c1", "test -f goal-scope-file.txt", 0),
		},
		Attempt: 1,
	})

	if result.Unavailable {
		t.Fatalf("no-workspace adjudication must not be Unavailable/refused: %s", result.Reason)
	}
	require.NotNil(t, result.Verdict)
	if !result.Verdict.Met {
		t.Fatalf("verdict.Met = false, want true (today's-behavior fallback to agent-home dir) — "+
			"per-criterion: %+v", result.Verdict.PerCriterion)
	}
}

// TestJudge_MachineCheck_TaskWorkspaceUnreachable_RefusesHonestly proves the
// "must stay a refusal" half of the fix: when the assignee is not a CoreTeam
// member of ANY workspace at all (so the task's own workspace is genuinely
// unreachable for it — ADR-046 P1's "an agent that is not a member of any
// workspace cannot execute at all"), the check must NOT silently mis-score as
// an ordinary criterion-unmet — it must resolve unable_to_verify (Unavailable,
// re-run) with a reason that names the refusal, never a bare "exit code did
// not match" that looks like completed work simply failing the check. This
// does NOT weaken sandbox/filesystem confinement — it only reports the
// refusal honestly instead of letting a confinement gap masquerade as unmet.
//
// Built via NewAgentLoop directly (NOT mustNewAgentLoop/marcusP4TestHome):
// the shared test harness auto-seeds every agent id into SOME workspace
// (ensureTestWorkspaceMembership, test_helpers_test.go) specifically so ~100
// other tests in this package don't need their own workspace fixture — which
// would silently defeat the exact "member of no workspace" case under test
// here (mirrors workspace_team_reroot_test.go's
// TestRunTurn_WorkspacelessAgentRefused, which opts out the same way).
// Note: giving JudgeCriteriaInput.WorkspaceID a value the assignee simply
// ISN'T a member of (while it IS a member of some other workspace) does NOT
// refuse — FindForAgentPreferring treats an unmatched preference as "no
// preference" and gracefully falls back to the agent's actual (sole)
// membership, exactly mirroring how an ordinary turn's ts.opts.WorkspaceID
// preference already behaves. A genuine refusal requires no membership at all.
func TestJudge_MachineCheck_TaskWorkspaceUnreachable_RefusesHonestly(t *testing.T) {
	const assigneeID = "native-agent"
	tmpHome := t.TempDir()
	t.Setenv(config.EnvHome, tmpHome)

	agentHome := filepath.Join(tmpHome, "agents", assigneeID)
	require.NoError(t, os.MkdirAll(agentHome, 0o755))
	// No workspaces/ directory at all — assigneeID is a member of nothing.

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: t.TempDir(), DefaultModel: config.DefaultModel{Model: "test-model"}},
			List: []config.AgentConfig{
				{ID: assigneeID, Name: "Native Agent", Type: config.AgentTypeWorker, Home: agentHome},
			},
		},
	}
	al, err := NewAgentLoop(cfg, bus.NewMessageBus(), &mockProvider{})
	require.NoError(t, err, "NewAgentLoop must succeed")
	t.Cleanup(func() { al.Close() })

	assigneeInst, ok := al.GetRegistry().GetAgent(assigneeID)
	require.True(t, ok, "assignee agent must be registered")
	allowBashPolicy(assigneeInst)
	execTool, err := tools.NewExecToolWithDeps(agentHome, false, nil, tools.ExecToolDeps{GodMode: true})
	require.NoError(t, err)
	assigneeInst.Tools.RegisterReplacing(execTool)

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-unreachable-ws",
		AssigneeAgentID: assigneeID,
		WorkspaceID:     "some-workspace-that-does-not-exist",
		Criteria: []task.AcceptanceCriterion{
			machineCriterion("c1", "echo ok", 0),
		},
		Attempt: 1,
	})

	if !result.Unavailable {
		t.Fatalf("an assignee with no workspace membership at all must refuse (Unavailable/unable_to_verify), "+
			"got verdict met=%v, per-criterion=%+v", result.Verdict.Met, result.Verdict.PerCriterion)
	}
	if !strings.Contains(result.Reason, "unable_to_verify") {
		t.Errorf("reason %q must mention unable_to_verify (a distinguishable refusal), not look like a bare criterion-unmet",
			result.Reason)
	}
}
