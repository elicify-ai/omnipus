// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// judge_declared_check_adr084_test.go covers ADR-084 revision 9's §F
// (D5, D5a, D14) — JUDGE-FR-036/FR-038/FR-039/FR-040, "a declared check
// informs, one-way". Revision 7's D14 rebases this section onto rung 1
// (task.KindCheck, judge.go's runMachineCheck — already opt-in, already
// shipped, UNCHANGED in substance by this wave) after deleting the
// INFERRED artifact check (E3's rung 1.5 removal) that revision 6's §F
// was originally written around. These four requirements are therefore
// regression coverage over EXISTING behaviour, not new production code —
// see this file's own doc comment at judge_evidence_tiers.go's package
// header, and the joint delivery plan's §3 E10 row: "Declared-check
// coverage is not lost: FR-036/038/039/040's four tests ... cover rung 1,
// which was never inferred." judge_test.go's existing machine-check suite
// (TestJudge_MachineCheck_*) already exercises runMachineCheck's exit-code
// classification directly; this file adds the four requirements' OWN
// named oracles, including the one shape judge_test.go's suite never
// combines: a declared check running ALONGSIDE a prose criterion in the
// SAME adjudication (FR-036's "rendered into the Judge's evidence block"
// and FR-039's "the Judge MAY still return unmet").

package agent

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// capturingJudgeProvider is a providers.LLMProvider double that, unlike
// judge_test.go's fakeJudgeProvider, RECORDS the messages it was called
// with — needed here to prove FR-036's "rendered into the Judge's
// evidence block" reaches the actual prompt, not merely the in-memory
// []task.EvidenceRecord the mapping loop carries.
type capturingJudgeProvider struct {
	mu       sync.Mutex
	messages []providers.Message
	response *providers.LLMResponse
}

func (f *capturingJudgeProvider) Chat(
	_ context.Context, msgs []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	f.mu.Lock()
	f.messages = msgs
	f.mu.Unlock()
	return f.response, nil
}

func (f *capturingJudgeProvider) GetDefaultModel() string { return "fake-judge-model" }

func (f *capturingJudgeProvider) capturedContent() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var sb strings.Builder
	for _, m := range f.messages {
		sb.WriteString(m.Content)
		sb.WriteString("\n")
	}
	return sb.String()
}

// TestDeclaredCheck_OutcomeAttachedAsEvidenceRecord (JUDGE-FR-036): a
// declared check (task.KindCheck) running ALONGSIDE a prose criterion in
// the SAME adjudication has its outcome attached as a task.EvidenceRecord
// and rendered into the Judge's user-message evidence block — the prose
// Judge sees the check's own command and output.
func TestDeclaredCheck_OutcomeAttachedAsEvidenceRecord(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	workerInst, _ := al.GetRegistry().GetAgent("native-agent")
	fakeBash := &fakeBashTool{result: &tools.ToolResult{ForLLM: "UNIQUE-CHECK-OUTPUT-MARKER-42"}}
	workerInst.Tools.RegisterReplacing(fakeBash)
	allowBashPolicy(workerInst)

	capture := &capturingJudgeProvider{
		response: &providers.LLMResponse{
			Content: `{"met":true,"criteria":[{"id":"c-prose","met":true,"reason":"looks good","evidence_quote":"q"}]}`,
		},
	}
	judgeInst.Provider = capture

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-fr036",
		AssigneeAgentID: "native-agent",
		Criteria: []task.AcceptanceCriterion{
			machineCriterion("c-check", "echo unique", 0),
			proseCriterion("c-prose", "the feature works"),
		},
		Attempt: 1,
	})
	if result.Unavailable {
		t.Fatalf("unexpected Unavailable: %s", result.Reason)
	}
	if !strings.Contains(capture.capturedContent(), "UNIQUE-CHECK-OUTPUT-MARKER-42") {
		t.Fatal("JUDGE-FR-036: the declared check's own output must be rendered into the Judge's evidence block")
	}
}

// TestDeclaredCheck_NonZeroExit_VetoesMet (JUDGE-FR-038): a declared check
// that ran to completion with a non-zero exit is scored unmet for THAT
// criterion by runMachineCheck's own deterministic path — rung 1 never
// reaches the Judge at all, so there is nothing for the Judge to overrule.
func TestDeclaredCheck_NonZeroExit_VetoesMet(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	workerInst, _ := al.GetRegistry().GetAgent("native-agent")
	fakeBash := &fakeBashTool{result: &tools.ToolResult{
		ForLLM: "boom\n\n[Command exited with code 1]", IsError: true,
	}}
	workerInst.Tools.RegisterReplacing(fakeBash)
	allowBashPolicy(workerInst)

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-fr038",
		AssigneeAgentID: "native-agent",
		Criteria:        []task.AcceptanceCriterion{machineCriterion("c-check", "false", 0)},
		Attempt:         1,
	})
	if result.Unavailable {
		t.Fatalf("unexpected Unavailable: %s", result.Reason)
	}
	if result.Verdict.PerCriterion[0].Met {
		t.Fatal("JUDGE-FR-038: a declared check's non-zero exit must veto met for that criterion")
	}
}

// TestDeclaredCheck_ZeroExit_VetoesNothing_JudgeMayStillReturnUnmet
// (JUDGE-FR-039): a declared check's zero exit vetoes NOTHING — it does
// not force the overall verdict, and a co-present prose criterion the
// Judge itself scores unmet stays unmet. "A single self-contained file"
// case: existence is not sufficiency.
func TestDeclaredCheck_ZeroExit_VetoesNothing_JudgeMayStillReturnUnmet(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	workerInst, _ := al.GetRegistry().GetAgent("native-agent")
	fakeBash := &fakeBashTool{result: &tools.ToolResult{ForLLM: "ok"}}
	workerInst.Tools.RegisterReplacing(fakeBash)
	allowBashPolicy(workerInst)

	fake := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{
			Content: `{"met":false,"criteria":[{"id":"c-prose","met":false,"reason":"file exists but content is wrong"}]}`,
		}, nil
	}}
	judgeInst.Provider = fake

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-fr039",
		AssigneeAgentID: "native-agent",
		Criteria: []task.AcceptanceCriterion{
			machineCriterion("c-check", "test -f file.txt", 0),
			proseCriterion("c-prose", "file.txt contains the right content"),
		},
		Attempt: 1,
	})
	if result.Unavailable {
		t.Fatalf("unexpected Unavailable: %s", result.Reason)
	}
	var checkVerdict, proseVerdict *task.CriterionVerdict
	for i := range result.Verdict.PerCriterion {
		switch result.Verdict.PerCriterion[i].CriterionID {
		case "c-check":
			checkVerdict = &result.Verdict.PerCriterion[i]
		case "c-prose":
			proseVerdict = &result.Verdict.PerCriterion[i]
		}
	}
	if checkVerdict == nil || !checkVerdict.Met {
		t.Fatal("the zero-exit check's OWN criterion must be met")
	}
	if proseVerdict == nil || proseVerdict.Met {
		t.Fatal("JUDGE-FR-039: a zero-exit declared check must not force a co-present prose criterion met")
	}
	if result.Verdict.Met {
		t.Fatal("the overall unit verdict must be false when any criterion is unmet")
	}
}

// TestDeclaredCheck_UndecidedOutcome_VetoesNothing (JUDGE-FR-040): a check
// whose own outcome could not be decided (here: policy-denied) vetoes
// nothing — it is unable_to_verify (re-run), never scored as a failure.
func TestDeclaredCheck_UndecidedOutcome_VetoesNothing(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	workerInst, _ := al.GetRegistry().GetAgent("native-agent")
	fakeBash := &fakeBashTool{result: &tools.ToolResult{ForLLM: "should never run"}}
	workerInst.Tools.RegisterReplacing(fakeBash)
	workerInst.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"bash": config.ToolPolicyDeny},
	})

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-fr040",
		AssigneeAgentID: "native-agent",
		Criteria:        []task.AcceptanceCriterion{machineCriterion("c-check", "true", 0)},
		Attempt:         1,
	})
	if !result.Unavailable {
		t.Fatal("JUDGE-FR-040: an undecided check outcome must be unable_to_verify (Unavailable), never scored unmet")
	}
	if fakeBash.callCount() != 0 {
		t.Fatal("a policy-denied check must never actually dispatch")
	}
}
