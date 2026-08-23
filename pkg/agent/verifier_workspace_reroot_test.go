// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// verifier_workspace_reroot_test.go covers the product-blocker fix (live
// fresh-install smoke test, 2026-07): the Judge System Agent's verifier
// turn was refused BEFORE it ever started, for all three JudgeCriteria
// callers (task/plan/goal), because ADR-046 P1's resolveTurnWorkDirOrRefuse
// required every turn to resolve a workspace via literal core_team
// membership, while pkg/gateway/rest_workspaces.go's validateCoreTeamMembers
// (and the default-workspace seeder) DELIBERATELY, PERMANENTLY forbid a
// System Agent from ever being ADDED to any workspace's core_team roster.
//
// Operator decision (2026-07-21, "make the judge a member of every
// workspace, keep it simple"): the fix is a POSITIVE membership rule, not a
// refusal bypass. System Agents (coreagent.IsSystemAgentID; today: the
// Judge) are IMPLICIT members of EVERY workspace — this lives entirely in
// pkg/workspace's isImplicitMember (consulted by
// FindForAgent/FindForAgentPreferring), the single place membership itself
// is decided. resolveTurnWorkDirOrRefuse (pkg/agent/workspace_reroot.go) no
// longer special-cases System Agents before calling FindForAgentPreferring —
// it calls it UNIFORMLY for every agent, and the check now genuinely
// SUCCEEDS for the Judge instead of being skipped. The pre-fix refusal
// surfaced as exactly the log lines a live smoke test observed:
//
//	WARN turn refused: agent is not a member of any workspace {agent_id: judge}
//	WARN verifier: turn failed; pausing (D7 unavailability)
//
// JudgeCriteriaInput.WorkspaceID (threaded from all 3 JudgeCriteria callers)
// is UNCHANGED by this restructure: under universal implicit membership it
// is the "preferring" SELECTOR that picks WHICH of the Judge's (every)
// workspace memberships a given adjudication roots in — the work-under-
// review's own workspace — via the exact same preferredWsID/optWorkspaceID
// parameter FindForAgentPreferring already gives every other multi-
// membership agent. When no selector is threaded, the Judge falls back
// EXACTLY like any normal multi-workspace member does in
// FindForAgentPreferring: sorted-first among its memberships (which, for an
// implicit member, is sorted-first among EVERY existing workspace) — see
// TestRunVerifierAdjudication_JudgeSystemAgent_NoOverride_MultipleWorkspacesExist_ResolvesSortedFirst.
// The ONE remaining agent-home fallback branch fires only when genuinely NO
// workspace exists at all yet (the pre-onboarding edge, before a default
// workspace is seeded) — see
// TestRunVerifierAdjudication_JudgeSystemAgent_NoWorkspaceExistsAtAll_FallsBackToAgentHome.
//
// These tests deliberately do NOT use this package's usual
// mustNewAgentLoop/newGoalLoopTestLoop harnesses: ensureTestWorkspaceMembership
// (test_helpers_test.go) unions EVERY cfg.Agents.List agent id — including a
// Type=system entry like "judge" — into one shared workspace's core_team via
// raw JSON, entirely bypassing validateCoreTeamMembers. That is exactly why
// this bug shipped past this whole package's pre-existing test suite: every
// other verifier test accidentally gives "judge" an EXPLICIT membership no
// production install could ever produce, masking the fact that implicit
// membership was the only thing that could ever make it work. NewAgentLoop
// is called directly here so "judge" gets ZERO explicit workspace membership
// from any test harness — the real production shape — and every test below
// still succeeds purely on implicit membership.
//
// Pre-fix evidence (captured 2026-07-21, prior revision of this fix — the
// mechanism has since been restructured per operator direction, but the
// underlying refusal this regression pins is unchanged): a reduced,
// pre-fix-compatible version of the "no workspace exists" test FAILED with
//
//	WRN workspace_reroot.go:59 > turn refused: agent is not a member of any workspace  agent_id=judge
//	WRN verifier_adjudication.go:621 > verifier: turn failed; pausing (D7 unavailability)  error="agent is not a member of any workspace; turn refused: agent_id=judge"
//	WRN judge.go:597 > judge: unavailable, backing off before retry  backoff_ms=60000 ...
//	--- FAIL: TestRunVerifierAdjudication_JudgeNotWorkspaceMember_NoOverrideStillSucceeds (0.02s)
//
// — an exact match for the live fresh-install smoke test's observed log
// lines.
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
// worker agent, with NO EXPLICIT workspace membership seeded for "judge" by
// any test harness (unlike newGoalLoopTestLoop/mustNewAgentLoop) — the
// Judge's only membership is the implicit, every-workspace kind. When
// seedNativeAgentMembership is true, a real workspace ("ws-under-review")
// is created with native-agent as its sole EXPLICIT core_team member —
// mirroring a real task's assignee, who legitimately CAN be an explicit
// workspace member, in contrast to the Judge, who never needs to be.
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
			Defaults: config.AgentDefaults{
				Home: t.TempDir(), DefaultModel: config.DefaultModel{Model: "test-model"}},
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
// all) and asserts adjudication now SUCCEEDS — resolved purely through
// implicit membership + the threaded WorkspaceID selector, with the Judge
// never appearing in the workspace's core_team at all.
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
			"adjudication must SUCCEED once the Judge System Agent is treated as an implicit member of "+
				"every workspace (operator decision, ADR-052 fix) — got Unavailable=true, reason=%q. This is "+
				"EXACTLY the live fresh-install failure shape: \"turn refused: agent is not a member of any "+
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

// TestRunVerifierAdjudication_JudgeSystemAgent_NoWorkspaceExistsAtAll_FallsBackToAgentHome
// proves the ONE retained agent-home fallback branch: when genuinely NO
// workspace exists yet at all (the pre-onboarding edge — implicit
// membership over an empty set is still empty, so even the Judge can't
// resolve one), the Judge's turn must still be allowed to proceed, rooted
// at its own agent home, never refused. As soon as ANY workspace exists,
// this branch never fires again (see the sibling test below).
//
// This is the specific property verified to FAIL on the pre-fix tree (see
// the file doc comment for the captured evidence).
func TestRunVerifierAdjudication_JudgeSystemAgent_NoWorkspaceExistsAtAll_FallsBackToAgentHome(t *testing.T) {
	origSleep := judgeSleepFn
	t.Cleanup(func() { judgeSleepFn = origSleep })
	judgeSleepFn = func(context.Context, time.Duration) error {
		return context.Canceled // give up immediately on D7 backoff — no real sleep
	}

	al, judgeInst, _, judgeHome := newProductionShapeJudgeTestLoop(t, false) // zero workspaces created

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
		// WorkspaceID deliberately left empty — no workspace to prefer.
	})
	if result.Unavailable {
		t.Fatalf("unexpected Unavailable with no workspace at all (must fall back to agent home): %s", result.Reason)
	}
	if result.Verdict == nil || !result.Verdict.Met {
		t.Fatalf("expected a real, met verdict falling back to the Judge's own agent home; got %+v", result)
	}

	capturedCtx := fake.capturedCtx()
	if capturedCtx == nil {
		t.Fatal("the verifier's LLM call must have received a ctx")
	}
	if got := tools.TurnWorkspaceDir(capturedCtx); got != judgeHome {
		t.Errorf("with no workspace existing at all, the verifier turn must root at the Judge's own agent home %q, got %q",
			judgeHome, got)
	}
}

// TestRunVerifierAdjudication_JudgeSystemAgent_NoOverride_MultipleWorkspacesExist_ResolvesSortedFirst
// proves the "uniformity is the point" half of the operator's restructure:
// with NO WorkspaceID selector threaded, but at least one workspace
// existing, the Judge must resolve via implicit membership to SOME
// workspace — the exact same sorted-first tie-break
// workspace.FindForAgent's own doc comment documents for any ordinary agent
// that happens to belong to more than one workspace's core_team — NOT the
// agent-home fallback (which is reserved for the "zero workspaces at all"
// case proven above).
//
// Re-scoped (sign-off 14 MINOR-1 / architect F4): this test now uses
// task-scope (TaskID) rather than an UNBOUND goal-scope input. Sorted-first
// remains the generic no-selector behavior for any turn NOT flagged as an
// unbound /goal — task/plan scope always carries a real work-under-review
// record even when WorkspaceID itself happens to be empty, so "nothing to
// prefer among" never applies to them. An unbound /goal specifically (Scope
// == goal, WorkspaceID == "") is the ONE case with genuinely nothing to
// prefer among at all, and now asserts agent-home instead — see
// TestRunVerifierAdjudication_JudgeSystemAgent_UnboundGoal_MultipleWorkspacesExist_RootsAtAgentHome
// below, which reuses this exact same "two real workspaces" fixture to prove
// the two scopes behave differently from the SAME environment.
func TestRunVerifierAdjudication_JudgeSystemAgent_NoOverride_MultipleWorkspacesExist_ResolvesSortedFirst(t *testing.T) {
	origSleep := judgeSleepFn
	t.Cleanup(func() { judgeSleepFn = origSleep })
	judgeSleepFn = func(context.Context, time.Duration) error {
		return context.Canceled
	}

	al, judgeInst, home, judgeHome := newProductionShapeJudgeTestLoop(t, false)

	// Two real workspaces, NEITHER listing "judge" in core_team — implicit
	// membership alone must make both count. "ws-a" sorts before "ws-b".
	workspacesDir := filepath.Join(home, "workspaces")
	if err := os.MkdirAll(workspacesDir, 0o755); err != nil {
		t.Fatalf("MkdirAll workspaces: %v", err)
	}
	for _, id := range []string{"ws-b", "ws-a"} { // deliberately created out of sorted order
		wsJSON := `{"id":"` + id + `","core_team":["native-agent"]}`
		if err := os.WriteFile(filepath.Join(workspacesDir, id+".json"), []byte(wsJSON), 0o644); err != nil {
			t.Fatalf("write workspace record %s: %v", id, err)
		}
	}

	fake := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{
			Content: `{"met": true, "criteria": [{"id":"c1","met":true,"reason":"ok"}]}`,
		}, nil
	}}
	judgeInst.Provider = fake

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-multi-ws-no-selector",
		AssigneeAgentID: "native-agent",
		Criteria:        []task.AcceptanceCriterion{proseCriterion("c1", "ok")},
		Attempt:         1,
		ClaimText:       "done",
		// WorkspaceID deliberately left empty.
	})
	if result.Unavailable {
		t.Fatalf("unexpected Unavailable with workspaces present but no selector threaded: %s", result.Reason)
	}

	capturedCtx := fake.capturedCtx()
	if capturedCtx == nil {
		t.Fatal("the verifier's LLM call must have received a ctx")
	}
	wantWorkDir, wsErr := workspace.SafeWorkDir(home, "ws-a")
	if wsErr != nil {
		t.Fatalf("workspace.SafeWorkDir: %v", wsErr)
	}
	gotWorkDir := tools.TurnWorkspaceDir(capturedCtx)
	if gotWorkDir != wantWorkDir {
		t.Errorf("verifier turn's TurnWorkspaceDir = %q, want the sorted-first workspace %q (implicit membership, no selector)",
			gotWorkDir, wantWorkDir)
	}
	if gotWorkDir == judgeHome {
		t.Error("verifier turn must NOT fall back to agent home when at least one workspace exists")
	}
}

// TestRunVerifierAdjudication_JudgeSystemAgent_UnboundGoal_MultipleWorkspacesExist_RootsAtAgentHome
// is the other half of the sign-off 14 MINOR-1 / architect F4 fix: an
// UNBOUND /goal (Scope==goal, WorkspaceID=="") must root at the Judge's own
// agent home EVEN WHEN one or more workspaces exist — unlike the sibling
// test above (task scope, no selector), which correctly resolves to the
// sorted-first workspace from this EXACT SAME fixture. An unbound /goal has
// no work-under-review workspace to prefer among at all, so picking an
// arbitrary sorted-first one would be a wrong-in-kind answer, not merely a
// low-risk one — even though it is harmless in today's single-tenant
// deployments.
func TestRunVerifierAdjudication_JudgeSystemAgent_UnboundGoal_MultipleWorkspacesExist_RootsAtAgentHome(t *testing.T) {
	origSleep := judgeSleepFn
	t.Cleanup(func() { judgeSleepFn = origSleep })
	judgeSleepFn = func(context.Context, time.Duration) error {
		return context.Canceled
	}

	al, judgeInst, home, judgeHome := newProductionShapeJudgeTestLoop(t, false)

	// Same "two real workspaces" fixture as the sibling task-scope test —
	// deliberately so the ONLY variable between the two tests is Scope, not
	// the environment.
	workspacesDir := filepath.Join(home, "workspaces")
	if err := os.MkdirAll(workspacesDir, 0o755); err != nil {
		t.Fatalf("MkdirAll workspaces: %v", err)
	}
	for _, id := range []string{"ws-b", "ws-a"} {
		wsJSON := `{"id":"` + id + `","core_team":["native-agent"]}`
		if err := os.WriteFile(filepath.Join(workspacesDir, id+".json"), []byte(wsJSON), 0o644); err != nil {
			t.Fatalf("write workspace record %s: %v", id, err)
		}
	}

	fake := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{
			Content: `{"met": true, "criteria": [{"id":"c1","met":true,"reason":"ok"}]}`,
		}, nil
	}}
	judgeInst.Provider = fake

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeGoal,
		GoalSessionID:   "goal-sess-unbound-multi",
		AssigneeAgentID: "native-agent",
		Criteria:        []task.AcceptanceCriterion{proseCriterion("c1", "ok")},
		Attempt:         1,
		ClaimText:       "done",
		// WorkspaceID deliberately left empty — an unbound /goal.
	})
	if result.Unavailable {
		t.Fatalf("unexpected Unavailable for an unbound /goal with workspaces present: %s", result.Reason)
	}
	if result.Verdict == nil || !result.Verdict.Met {
		t.Fatalf("expected a real, met verdict rooted at agent home; got %+v", result)
	}

	capturedCtx := fake.capturedCtx()
	if capturedCtx == nil {
		t.Fatal("the verifier's LLM call must have received a ctx")
	}
	gotWorkDir := tools.TurnWorkspaceDir(capturedCtx)
	if gotWorkDir != judgeHome {
		t.Errorf("an unbound /goal's verifier turn TurnWorkspaceDir = %q, want the Judge's own agent home %q "+
			"(no work-under-review workspace exists to prefer among, even though %d workspace(s) exist)",
			gotWorkDir, judgeHome, 2)
	}
	for _, wsID := range []string{"ws-a", "ws-b"} {
		wsDir, wsErr := workspace.SafeWorkDir(home, wsID)
		if wsErr != nil {
			t.Fatalf("workspace.SafeWorkDir(%s): %v", wsID, wsErr)
		}
		if gotWorkDir == wsDir {
			t.Errorf("an unbound /goal must NOT resolve to workspace %q's work dir, got %q", wsID, gotWorkDir)
		}
	}
}

// TestJudgeCriteria_TaskScope_VerifierTurnRootedAtWorkUnderReviewWorkspace
// asserts the SECOND mandatory requirement: with JudgeCriteriaInput.WorkspaceID
// threaded — exactly as task_executor.go's real call site now does, from
// task.Task.WorkspaceID (task.go:246-247, "every task belongs to a
// workspace") — the verifier's OWN turn is rooted at the WORK-UNDER-REVIEW's
// workspace work/ directory, NOT an arbitrary sorted-first one and NOT the
// Judge's own private agent home. This is the functional payoff of the
// WorkspaceID selector under universal implicit membership: without it,
// ADR-052 FR-012(c)'s read_file/list_directory grant to the verifier would
// still work, but against the wrong workspace's artifacts.
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
