// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// verifier_workspace_reroot_test.go covers the product-blocker fix (live
// fresh-install smoke test, 2026-07): the Judge System Agent's verifier
// turn was refused BEFORE it ever started, for all three JudgeCriteria
// callers (task/plan/goal), because ADR-046 P1's resolveTurnWorkDirOrRefuse
// (workspace_reroot.go) required every turn to resolve a workspace via
// literal core_team membership, while pkg/gateway/rest_workspaces.go's
// validateCoreTeamMembers (and the default-workspace seeder) DELIBERATELY,
// PERMANENTLY forbid a System Agent from ever being added to any
// workspace's core_team roster. The two rules collide: the Judge could
// never satisfy the membership scan, so runVerifierAdjudication's
// al.processTaskDirect dispatch always failed with
// ErrAgentNotWorkspaceMember, surfacing as exactly the log lines a live
// smoke test observed:
//
//	WARN turn refused: agent is not a member of any workspace {agent_id: judge}
//	WARN verifier: turn failed; pausing (D7 unavailability)
//
// The fix (workspace_reroot.go's System Agent exemption +
// resolveSystemAgentTurnWorkDir, threaded via JudgeCriteriaInput.WorkspaceID
// from all three JudgeCriteria callers) is BOTH exemption (Option i — the
// membership scan is definitionally unsatisfiable for a System Agent, no
// generalization of it can ever help) AND threading (Option ii — the
// verifier's turn roots at the WORK-UNDER-REVIEW's own workspace so its
// FR-012(c) read-only tools can actually reach the artifacts they exist to
// inspect, not just the Judge's own private agent home). See
// workspace_reroot.go's doc comments for the full grounding.
//
// These tests deliberately do NOT use this package's usual
// mustNewAgentLoop/newGoalLoopTestLoop harnesses: ensureTestWorkspaceMembership
// (test_helpers_test.go) unions EVERY cfg.Agents.List agent id — including a
// Type=system entry like "judge" — into one shared workspace's core_team via
// raw JSON, entirely bypassing validateCoreTeamMembers. That is exactly why
// this bug shipped past this whole package's pre-existing test suite: every
// other verifier test accidentally gives "judge" a membership no production
// install could ever produce. NewAgentLoop is called directly here so "judge"
// gets ZERO workspace membership from any test harness — the real production
// shape.
//
// Pre-fix evidence (captured 2026-07-21 via a stash of the 8 production
// files + the 2 pre-existing test files this fix updated for the new
// resolveTurnWorkDirOrRefuse signature, keeping only a reduced,
// pre-fix-compatible copy of TestRunVerifierAdjudication_JudgeNotWorkspaceMember_NoOverrideStillSucceeds
// — the one test here that references no new JudgeCriteriaInput field, so it
// still compiles against the OLD struct): the test FAILED with
//
//	WRN workspace_reroot.go:59 > turn refused: agent is not a member of any workspace  agent_id=judge
//	WRN verifier_adjudication.go:621 > verifier: turn failed; pausing (D7 unavailability)  error="agent is not a member of any workspace; turn refused: agent_id=judge"
//	WRN judge.go:597 > judge: unavailable, backing off before retry  backoff_ms=60000 ...
//	    verifier_workspace_reroot_test.go:96: unexpected Unavailable with no workspace override (must fall back to agent home): agent is not a member of any workspace; turn refused: agent_id=judge
//	--- FAIL: TestRunVerifierAdjudication_JudgeNotWorkspaceMember_NoOverrideStillSucceeds (0.02s)
//
// — an exact match for the live fresh-install smoke test's observed log
// lines. (Without overriding judgeSleepFn, the same pre-fix run instead
// blocks for real on judge.go's D7 backoff schedule — 60s then 120s then
// 300s, retried forever against context.Background() — which is itself
// independent confirmation of "the exact flow that hangs today.")
package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// newProductionShapeJudgeTestLoop builds an AgentLoop the way NewAgentLoop
// itself would from a fresh install: a Judge System Agent + one native
// worker agent, with NO workspace membership seeded for "judge" by any test
// harness (unlike newGoalLoopTestLoop/mustNewAgentLoop). When
// seedNativeAgentMembership is true, a real workspace ("ws-under-review")
// is created with native-agent as its sole core_team member — mirroring a
// real task's assignee, who legitimately CAN be a workspace member, in
// contrast to the Judge, who never can.
func newProductionShapeJudgeTestLoop(
	t *testing.T, seedNativeAgentMembership bool,
) (al *AgentLoop, judgeInst *AgentInstance, home, judgeHome string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv(config.EnvHome, home)
	judgeHome = t.TempDir()
	workerHome := t.TempDir()

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: t.TempDir(), ModelName: "test-model"},
			List: []config.AgentConfig{
				{ID: "native-agent", Name: "Native Agent", Type: config.AgentTypeWorker, Home: workerHome},
				{ID: string(coreagent.IDJudge), Name: "Judge", Type: config.AgentTypeSystem, Home: judgeHome},
			},
		},
	}

	if seedNativeAgentMembership {
		workspacesDir := filepath.Join(home, "workspaces")
		if err := os.MkdirAll(workspacesDir, 0o755); err != nil {
			t.Fatalf("MkdirAll workspaces: %v", err)
		}
		wsJSON := `{"id":"ws-under-review","core_team":["native-agent"]}`
		if err := os.WriteFile(filepath.Join(workspacesDir, "ws-under-review.json"), []byte(wsJSON), 0o644); err != nil {
			t.Fatalf("write workspace record: %v", err)
		}
	}

	// NewAgentLoop DIRECTLY — see the file doc comment for why this, not
	// mustNewAgentLoop, is required to reproduce the real bug shape.
	al, err := NewAgentLoop(cfg, bus.NewMessageBus(), &mockProvider{})
	if err != nil {
		t.Fatalf("NewAgentLoop: %v", err)
	}
	t.Cleanup(func() { al.Close() })

	judgeInst, ok := al.GetRegistry().GetAgent(string(coreagent.IDJudge))
	if !ok {
		t.Fatal("judge agent must be registered")
	}
	return al, judgeInst, home, judgeHome
}

// TestRunVerifierAdjudication_JudgeNotWorkspaceMember_ProductionShapeRegression
// is the mandatory regression test: it reproduces the refusal shape with the
// ADR-046 P1 workspace-membership enforcement genuinely ACTIVE (via
// NewAgentLoop directly, no test-harness membership seeding for "judge" at
// all) and asserts adjudication now SUCCEEDS.
func TestRunVerifierAdjudication_JudgeNotWorkspaceMember_ProductionShapeRegression(t *testing.T) {
	origSleep := judgeSleepFn
	t.Cleanup(func() { judgeSleepFn = origSleep })
	judgeSleepFn = func(context.Context, time.Duration) error {
		return context.Canceled // give up immediately on D7 backoff — no real sleep
	}

	al, judgeInst, _, _ := newProductionShapeJudgeTestLoop(t, true)

	fake := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{
			Content: `{"met": true, "criteria": [{"id":"c1","met":true,"reason":"looks fine"}]}`,
		}, nil
	}}
	judgeInst.Provider = fake

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-prod-shape",
		AssigneeAgentID: "native-agent",
		Criteria:        []task.AcceptanceCriterion{proseCriterion("c1", "looks fine")},
		Attempt:         1,
		ClaimText:       "done",
		WorkspaceID:     "ws-under-review",
	})

	if result.Unavailable {
		t.Fatalf(
			"adjudication must SUCCEED once the Judge System Agent's turn is exempted from the literal "+
				"core_team membership scan (ADR-052 fix) — got Unavailable=true, reason=%q. This is EXACTLY "+
				"the live fresh-install failure shape: \"turn refused: agent is not a member of any "+
				"workspace\" -> \"verifier: turn failed; pausing (D7 unavailability)\"",
			result.Reason,
		)
	}
	if result.Verdict == nil {
		t.Fatal("expected a real verdict, got nil")
	}
	if !result.Verdict.Met {
		t.Errorf("verdict.Met = false, want true (per-criterion: %+v)", result.Verdict.PerCriterion)
	}
}

// TestRunVerifierAdjudication_JudgeNotWorkspaceMember_NoOverrideStillSucceeds
// proves the exemption's OWN fallback branch (resolveSystemAgentTurnWorkDir)
// in isolation: even with NO workspace threaded at all (WorkspaceID unset —
// the goal-scope-on-an-unbound-chat case), the Judge's turn must still be
// allowed to proceed, rooted at its own agent home, never refused.
//
// This is the specific test verified to FAIL on the pre-fix tree (see the
// file doc comment for the captured evidence) — it is the one test in this
// file that references no new JudgeCriteriaInput field, so a reduced copy of
// it still compiled and ran unmodified against the OLD judge.go/
// workspace_reroot.go/verifier_adjudication.go.
func TestRunVerifierAdjudication_JudgeNotWorkspaceMember_NoOverrideStillSucceeds(t *testing.T) {
	origSleep := judgeSleepFn
	t.Cleanup(func() { judgeSleepFn = origSleep })
	judgeSleepFn = func(context.Context, time.Duration) error {
		return context.Canceled // give up immediately on D7 backoff — no real sleep
	}

	al, judgeInst, _, judgeHome := newProductionShapeJudgeTestLoop(t, false)

	fake := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{
			Content: `{"met": true, "criteria": [{"id":"c1","met":true,"reason":"ok"}]}`,
		}, nil
	}}
	judgeInst.Provider = fake

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeGoal,
		GoalSessionID:   "goal-sess-unbound",
		AssigneeAgentID: "native-agent",
		Criteria:        []task.AcceptanceCriterion{proseCriterion("c1", "ok")},
		Attempt:         1,
		ClaimText:       "done",
		// WorkspaceID deliberately left empty — no workspace to thread.
	})
	if result.Unavailable {
		t.Fatalf("unexpected Unavailable with no workspace override (must fall back to agent home): %s", result.Reason)
	}
	if result.Verdict == nil || !result.Verdict.Met {
		t.Fatalf("expected a real, met verdict falling back to the Judge's own agent home; got %+v", result)
	}

	capturedCtx := fake.capturedCtx()
	if capturedCtx == nil {
		t.Fatal("the verifier's LLM call must have received a ctx")
	}
	if got := tools.TurnWorkspaceDir(capturedCtx); got != judgeHome {
		t.Errorf("with no workspace override, the verifier turn must root at the Judge's own agent home %q, got %q",
			judgeHome, got)
	}
}

// TestJudgeCriteria_TaskScope_VerifierTurnRootedAtWorkUnderReviewWorkspace
// asserts the SECOND mandatory requirement: with JudgeCriteriaInput.WorkspaceID
// threaded — exactly as task_executor.go's real call site now does, from
// task.Task.WorkspaceID (task.go:246-247, "every task belongs to a
// workspace") — the verifier's OWN turn is rooted at the WORK-UNDER-REVIEW's
// workspace work/ directory, NOT the Judge's own private agent home. This is
// the functional payoff of threading (Option ii): without it, ADR-052
// FR-012(c)'s read_file/list_directory grant to the verifier would be dead
// weight — those tools would never be able to reach the artifacts under
// review.
func TestJudgeCriteria_TaskScope_VerifierTurnRootedAtWorkUnderReviewWorkspace(t *testing.T) {
	al, judgeInst, home, judgeHome := newProductionShapeJudgeTestLoop(t, true)

	fake := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{
			Content: `{"met": true, "criteria": [{"id":"c1","met":true,"reason":"ok"}]}`,
		}, nil
	}}
	judgeInst.Provider = fake

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-workdir",
		AssigneeAgentID: "native-agent",
		Criteria:        []task.AcceptanceCriterion{proseCriterion("c1", "ok")},
		Attempt:         1,
		ClaimText:       "done",
		WorkspaceID:     "ws-under-review",
	})
	if result.Unavailable {
		t.Fatalf("unexpected Unavailable: %s", result.Reason)
	}

	capturedCtx := fake.capturedCtx()
	if capturedCtx == nil {
		t.Fatal("the verifier's LLM call must have received a ctx")
	}

	wantWorkDir, wsErr := workspace.SafeWorkDir(home, "ws-under-review")
	if wsErr != nil {
		t.Fatalf("workspace.SafeWorkDir: %v", wsErr)
	}
	gotWorkDir := tools.TurnWorkspaceDir(capturedCtx)
	if gotWorkDir != wantWorkDir {
		t.Errorf(
			"verifier turn's TurnWorkspaceDir = %q, want the work-under-review's workspace work/ dir %q",
			gotWorkDir, wantWorkDir,
		)
	}
	if gotWorkDir == judgeHome {
		t.Error(
			"verifier turn must NOT be rooted at the Judge's own private agent home when a target " +
				"workspace is threaded — its read-only tools (FR-012(c)) would never reach the reviewed work",
		)
	}
}
