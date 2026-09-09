// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// judge_artifact_criterion_test.go covers fix GX-E-1 (judge.go's
// planArtifactCheck + JudgeCriteria's rung-1.5 dispatch): a prose criterion
// whose Judgment == task.JudgmentArtifact naming a concrete, unambiguous
// in-workspace path is machine-checked BEFORE the LLM ever sees it — reading
// the WORKING TREE directly (via the assignee's own bash tool, never
// gitevidence/git) per the operator's ground-truth correction, so it is
// decidable even from a completely unborn HEAD.

package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/gitevidence"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// artifactCriterion builds a JudgmentArtifact prose criterion for these
// tests — the shape a real goal/task DoD carries after ADR-080 D-TYPES
// inference (kind: prose, judgment: artifact).
func artifactCriterion(id, text string) task.AcceptanceCriterion {
	return task.AcceptanceCriterion{
		ID:       id,
		Kind:     task.KindProse,
		Judgment: task.JudgmentArtifact,
		Text:     text,
		Author:   task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: "native-agent"},
	}
}

// failIfCalledJudgeProvider is a fakeJudgeProvider whose chatFn fails the
// test the instant it is invoked — used to prove a criterion was settled
// deterministically without ever reaching the LLM. Callers should also
// assert callCount() == 0 as defense in depth (chatFn's t.Fatal only fires
// mid-goroutine and cannot itself halt the calling test function).
func failIfCalledJudgeProvider(t *testing.T) *fakeJudgeProvider {
	t.Helper()
	return &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		t.Fatal("the Judge LLM must never be called for a deterministically-settled artifact criterion")
		return &providers.LLMResponse{}, nil
	}}
}

// TestJudgeEvidence_ArtifactCriterion_ExistingFile_SettlesDeterministically_NoLLM
// is fix GX-E-1's core case: a JudgmentArtifact criterion naming a file that
// genuinely exists (non-empty) on disk is machine-checked and PASSES without
// ever calling the Judge LLM. (The companion
// TestJudgeEvidence_ArtifactCriterion_UnbornHEAD_FilePresent_PassesDeterministically
// below is the test that proves the check's PASS/FAIL determination never
// depends on git history — a shared, idempotent side effect of the SAME
// runMachineCheck plumbing kind:check criteria already use,
// workspace.EnsureWorkDir, does lazily materialize a hidden evidence repo
// marker as part of routing ANY check into the right work dir, but the
// check's own exit code comes from `test -f`/`test -s`/`grep` against the
// raw filesystem, never from git.)
func TestJudgeEvidence_ArtifactCriterion_ExistingFile_SettlesDeterministically_NoLLM(t *testing.T) {
	al, workDir, _ := marcusP4TestHome(t, "artifact-ws-exists", "native-agent")
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "output.txt"), []byte("hello world"), 0o644))

	judgeInst, ok := al.GetRegistry().GetAgent(string(coreagent.IDJudge))
	require.True(t, ok)
	fake := failIfCalledJudgeProvider(t)
	judgeInst.Provider = fake

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-artifact-exists",
		AssigneeAgentID: "native-agent",
		WorkspaceID:     "artifact-ws-exists",
		Criteria: []task.AcceptanceCriterion{
			artifactCriterion("c1", "output.txt exists in the workspace as a single self-contained file"),
		},
		Attempt: 1,
	})
	if result.Unavailable {
		t.Fatalf("unexpected Unavailable: %s", result.Reason)
	}
	require.NotNil(t, result.Verdict)
	if !result.Verdict.Met {
		t.Fatalf("verdict.Met = false, want true — per-criterion: %+v", result.Verdict.PerCriterion)
	}
	require.Len(t, result.Verdict.PerCriterion, 1)
	if !result.Verdict.PerCriterion[0].Met {
		t.Errorf("c1 unmet: %s", result.Verdict.PerCriterion[0].Reason)
	}

	if fake.callCount() != 0 {
		t.Errorf("judge LLM called %d times, want 0", fake.callCount())
	}
}

// TestJudgeEvidence_ArtifactCriterion_MissingFile_FailsDeterministically_NoLLM
// is the mirror: the same criterion, but the file was never written — Met
// must be false, deterministically, with no LLM call.
func TestJudgeEvidence_ArtifactCriterion_MissingFile_FailsDeterministically_NoLLM(t *testing.T) {
	al, _, _ := marcusP4TestHome(t, "artifact-ws-missing", "native-agent")

	judgeInst, ok := al.GetRegistry().GetAgent(string(coreagent.IDJudge))
	require.True(t, ok)
	fake := failIfCalledJudgeProvider(t)
	judgeInst.Provider = fake

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-artifact-missing",
		AssigneeAgentID: "native-agent",
		WorkspaceID:     "artifact-ws-missing",
		Criteria: []task.AcceptanceCriterion{
			artifactCriterion("c1", "output.txt exists in the workspace as a single self-contained file"),
		},
		Attempt: 1,
	})
	if result.Unavailable {
		t.Fatalf("unexpected Unavailable: %s", result.Reason)
	}
	require.NotNil(t, result.Verdict)
	if result.Verdict.Met {
		t.Fatal("verdict.Met = true, want false (output.txt was never written)")
	}
	require.Len(t, result.Verdict.PerCriterion, 1)
	if result.Verdict.PerCriterion[0].Met {
		t.Error("c1 must be unmet — the named file does not exist")
	}
	if fake.callCount() != 0 {
		t.Errorf("judge LLM called %d times, want 0", fake.callCount())
	}
}

// TestJudgeEvidence_ArtifactCriterion_UnbornHEAD_FilePresent_PassesDeterministically
// is the operator's explicit correction case: even with a REAL gitevidence
// repo already initialized at the workspace's work/ dir (so HEAD genuinely
// exists as a git ref concept) but with NOTHING EVER COMMITTED — an unborn
// HEAD — a JudgmentArtifact criterion naming a file that is present on disk
// must still pass deterministically. The working tree is ground truth; a
// commit is never a precondition for the judge to see it.
func TestJudgeEvidence_ArtifactCriterion_UnbornHEAD_FilePresent_PassesDeterministically(t *testing.T) {
	al, workDir, _ := marcusP4TestHome(t, "artifact-ws-unborn", "native-agent")

	// Initialize a REAL evidence repo (a genuine unborn-HEAD git ref) without
	// ever committing anything into it.
	repo, err := gitevidence.Open(workDir, gitevidence.WithRedactor(func(s string) string { return s }))
	require.NoError(t, err)
	head, err := repo.Head()
	require.NoError(t, err)
	require.Equal(t, "", head, "HEAD must be genuinely unborn (no commits) for this test to prove anything")

	require.NoError(t, os.WriteFile(filepath.Join(workDir, "output.txt"), []byte("hello world"), 0o644))

	judgeInst, ok := al.GetRegistry().GetAgent(string(coreagent.IDJudge))
	require.True(t, ok)
	fake := failIfCalledJudgeProvider(t)
	judgeInst.Provider = fake

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-artifact-unborn-head",
		AssigneeAgentID: "native-agent",
		WorkspaceID:     "artifact-ws-unborn",
		Criteria: []task.AcceptanceCriterion{
			artifactCriterion("c1", "output.txt exists in the workspace as a single self-contained file"),
		},
		Attempt: 1,
	})
	if result.Unavailable {
		t.Fatalf("unexpected Unavailable: %s", result.Reason)
	}
	require.NotNil(t, result.Verdict)
	if !result.Verdict.Met {
		t.Fatalf("verdict.Met = false, want true — an unborn HEAD must not block a working-tree-based "+
			"artifact check — per-criterion: %+v", result.Verdict.PerCriterion)
	}
	if fake.callCount() != 0 {
		t.Errorf("judge LLM called %d times, want 0", fake.callCount())
	}
}

// TestJudgeEvidence_ArtifactCriterion_Undecidable_FallsThroughToProse proves
// an artifact criterion whose text names no confident single path is left to
// the ordinary prose/LLM path — never machine-checked, never silently
// dropped.
func TestJudgeEvidence_ArtifactCriterion_Undecidable_FallsThroughToProse(t *testing.T) {
	al, _, _ := marcusP4TestHome(t, "artifact-ws-undecidable", "native-agent")

	rec := &recordingJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{
			Content: `{"met": true, "criteria": [{"id":"c1","met":true,"reason":"looks fine"}]}`,
		}, nil
	}}
	judgeInst, ok := al.GetRegistry().GetAgent(string(coreagent.IDJudge))
	require.True(t, ok)
	judgeInst.Provider = rec

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-artifact-undecidable",
		AssigneeAgentID: "native-agent",
		WorkspaceID:     "artifact-ws-undecidable",
		Criteria: []task.AcceptanceCriterion{
			// No path-shaped token anywhere in this text — planArtifactCheck
			// must decline to build a plan, and the criterion must still reach
			// the LLM (never silently passed, never silently dropped).
			artifactCriterion("c1", "the deployment behaves correctly end to end"),
		},
		Attempt:   1,
		ClaimText: "done",
	})
	if result.Unavailable {
		t.Fatalf("unexpected Unavailable: %s", result.Reason)
	}
	if rec.calls != 1 {
		t.Errorf("judge LLM called %d times, want exactly 1 (undecidable extraction must fall through to prose)", rec.calls)
	}
	require.NotNil(t, result.Verdict)
	if !result.Verdict.Met {
		t.Errorf("verdict.Met = false, want true (per the stubbed LLM verdict) — per-criterion: %+v",
			result.Verdict.PerCriterion)
	}
}

// TestJudgeEvidence_ArtifactCriterion_BlockedCheck_FallsThroughToProseWithEvidence
// is fix GX-E-1's "fail closed and explain" case: a path IS confidently
// extracted, but the check's own OUTCOME cannot be decided (bash policy
// denied here) — the criterion must fall through to the prose/LLM path
// (never silently withheld, never silently passed) WITH the check's blocked
// outcome attached as evidence in the Judge's prompt.
func TestJudgeEvidence_ArtifactCriterion_BlockedCheck_FallsThroughToProseWithEvidence(t *testing.T) {
	resetVerifierBlockedCheckSeams(t)
	al, workDir, _ := marcusP4TestHome(t, "artifact-ws-blocked", "native-agent")
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "output.txt"), []byte("hello world"), 0o644))

	// Deny bash for the assignee AFTER marcusP4TestHome's own allowBashPolicy
	// call, so the machine-check mechanism itself cannot run (MAJ-13).
	workerInst, ok := al.GetRegistry().GetAgent("native-agent")
	require.True(t, ok)
	workerInst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{"bash": config.ToolPolicyDeny}})

	rec := &recordingJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{
			Content: `{"met": true, "criteria": [{"id":"c1","met":true,"reason":"trusting the claim"}]}`,
		}, nil
	}}
	judgeInst, ok := al.GetRegistry().GetAgent(string(coreagent.IDJudge))
	require.True(t, ok)
	judgeInst.Provider = rec

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-artifact-blocked",
		AssigneeAgentID: "native-agent",
		WorkspaceID:     "artifact-ws-blocked",
		Criteria: []task.AcceptanceCriterion{
			artifactCriterion("c1", "output.txt exists in the workspace as a single self-contained file"),
		},
		Attempt:   1,
		ClaimText: "done",
	})
	if result.Unavailable {
		t.Fatalf("a blocked artifact check must fall through to prose, never withhold the round: %s", result.Reason)
	}
	if rec.calls != 1 {
		t.Errorf("judge LLM called %d times, want exactly 1 (blocked check must still fall through to prose)", rec.calls)
	}
	msg := rec.capturedLastUserMessage()
	if !strings.Contains(msg, "unable_to_verify") {
		t.Errorf("prose Judge prompt must attach the blocked check's own evidence (unable_to_verify); msg was:\n%s", msg)
	}
}
