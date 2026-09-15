// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// judge_no_inferred_check_adr084_test.go is wave E3's R-18 replacement
// oracle (delivery plan §3, row E3; ADR-084 revision 9 D14 rule 2;
// JUDGE-FR-109). D14 rule 2 retires the artifact-criterion inference
// outright: a verification command is NEVER synthesised from a criterion's
// prose. Before this wave, judge.go's JudgeCriteria classified a
// `Kind: prose` / `Judgment: artifact` criterion naming a single,
// unambiguous, workspace-relative path into its own "rung 1.5": a
// deterministic `test -f ... && test -s ...` shell command
// (planArtifactCheck), dispatched through the assignee's bash tool BEFORE
// the criterion ever reached the Judge LLM — settling it outright on a
// zero exit and never calling the Judge at all.
//
// This test proves the deletion BEHAVIOURALLY, matching CLAUDE.md's
// "assert behaviour, not source text" rule: it builds a real, resolvable
// workspace (marcusP4TestHome, judge_check_workspace_reroot_test.go) whose
// core team contains the assignee, seeds a real file at the exact
// unambiguous path the criterion's text names, and proves that
// JudgeCriteria dispatches ZERO shell executions and reaches the prose
// Judge exactly like any other prose criterion. Before wave E3's deletion,
// this identical fixture (real workspace membership, single unambiguous
// path token, non-empty WorkspaceID) is precisely what made
// planArtifactCheck return ok=true and resolveTurnWorkDirOrRefuse succeed —
// so reverting the deletion turns this test red: the bash tool would be
// called once and the Judge LLM never called at all.
package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func TestJudge_ArtifactCriterion_NoInferredCheck_ReachesProseJudge(t *testing.T) {
	const wsID = "art-ws"
	const assigneeID = "native-agent"
	al, workDir, _ := marcusP4TestHome(t, wsID, assigneeID)

	// A real file at the exact unambiguous path the criterion's text names
	// — pre-wave-E3, planArtifactCheck would have found this single
	// path-shaped token, built "test -f 'report.md' && test -s 'report.md'",
	// and run it to a real exit 0 without ever calling the Judge.
	if err := os.WriteFile(filepath.Join(workDir, "report.md"), []byte("# Report\n\ndone"), 0o644); err != nil {
		t.Fatalf("seed workspace artifact: %v", err)
	}

	assigneeInst, ok := al.GetRegistry().GetAgent(assigneeID)
	if !ok {
		t.Fatal("assignee agent not found")
	}
	// Replace marcusP4TestHome's real GodMode ExecTool with the same fake
	// double judge_test.go's other tests use, so shell dispatch is countable
	// with certainty instead of inferred from exit codes.
	fakeBash := &fakeBashTool{result: &tools.ToolResult{ForLLM: "ok"}}
	assigneeInst.Tools.RegisterReplacing(fakeBash)

	judgeInst, ok := al.GetRegistry().GetAgent(string(coreagent.IDJudge))
	if !ok {
		t.Fatal("judge agent not registered")
	}
	fakeJudge := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{
			Content: `{"met": true, "criteria": [{"id":"c1","met":true,` +
				`"reason":"report.md exists in the workspace and contains the summary"}]}`,
		}, nil
	}}
	judgeInst.Provider = fakeJudge

	criterion := task.AcceptanceCriterion{
		ID:       "c1",
		Kind:     task.KindProse,
		Judgment: task.JudgmentArtifact,
		Text:     "report.md exists in the workspace and contains a summary of the findings",
		Author:   task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: assigneeID},
	}

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-artifact-no-inference",
		AssigneeAgentID: assigneeID,
		WorkspaceID:     wsID,
		Criteria:        []task.AcceptanceCriterion{criterion},
		Attempt:         1,
		ClaimText:       "wrote report.md with the summary",
	})

	if result.Unavailable {
		t.Fatalf("unexpected Unavailable: %s", result.Reason)
	}
	if fakeBash.callCount() != 0 {
		t.Fatalf("bash tool called %d times, want exactly 0 — D14 rule 2 retires the inferred "+
			"artifact check outright (JUDGE-FR-109); a Kind:prose/Judgment:artifact criterion must "+
			"never have a verification command synthesised from its text", fakeBash.callCount())
	}
	if fakeJudge.callCount() != 1 {
		t.Fatalf("judge LLM called %d times, want exactly 1 — the artifact criterion must be "+
			"dispatched as an ordinary prose criterion, reaching the Judge by the same path any "+
			"other prose criterion does, never settled by an inferred check", fakeJudge.callCount())
	}
	if len(result.Verdict.PerCriterion) != 1 || result.Verdict.PerCriterion[0].CriterionID != "c1" {
		t.Fatalf("PerCriterion = %+v, want exactly one verdict for c1 from the Judge",
			result.Verdict.PerCriterion)
	}
	if !result.Verdict.Met {
		t.Fatalf("verdict.Met = false, want true (per-criterion: %+v)", result.Verdict.PerCriterion)
	}
}
