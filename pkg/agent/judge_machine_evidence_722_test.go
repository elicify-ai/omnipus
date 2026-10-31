// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// judge_machine_evidence_722_test.go — issue #722: a machine check that ran
// must reach the prose Judge's prompt even when the adjudication has no task
// (a chat goal) and even when writing the evidence record to disk fails. The
// on-disk evidence store is partitioned by task id; the Judge's evidence is
// not. Before the fix both cases reached the Judge as "(no machine-check
// results on this attempt)" although the check had just passed.

package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const machineEvidenceCanary = "MACHINE-CHECK-OUTPUT-CANARY-722"

func runJudgeWithPassingCheck(t *testing.T, in JudgeCriteriaInput, breakPersistence bool) string {
	t.Helper()
	resetVerifierBlockedCheckSeams(t)
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	workerInst, _ := al.GetRegistry().GetAgent("native-agent")
	allowBashPolicy(workerInst)
	workerInst.Tools.RegisterReplacing(&fakeBashTool{result: &tools.ToolResult{ForLLM: machineEvidenceCanary}})

	if breakPersistence {
		// A regular FILE where the evidence directory must go: every
		// EvidenceStore write fails with a real filesystem error.
		blocker := filepath.Join(config.OmnipusHomeDir(), "tasks_evidence")
		if err := os.MkdirAll(filepath.Dir(blocker), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	rec := &recordingJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{
			Content: `{"met": true, "criteria": [{"id":"p1","met":true,"reason":"check output shown","evidence_quote":"` +
				machineEvidenceCanary + `"}]}`,
		}, nil
	}}
	judgeInst.Provider = rec

	in.AssigneeAgentID = "native-agent"
	in.Attempt = 1
	in.ClaimText = "done"
	in.Criteria = []task.AcceptanceCriterion{
		machineCriterion("c1", "echo ok", 0),
		proseCriterion("p1", "the check output proves the work"),
	}
	result := al.JudgeCriteria(context.Background(), in)
	if result.Unavailable {
		t.Fatalf("unexpected Unavailable: %s", result.Reason)
	}
	msg := rec.capturedLastUserMessage()
	if msg == "" {
		t.Fatal("the prose Judge was never called; the prompt channel was not exercised")
	}
	return msg
}

func assertMachineEvidenceInPrompt(t *testing.T, msg string) {
	t.Helper()
	if strings.Contains(msg, "(no machine-check results on this attempt)") {
		t.Errorf("the Judge was told there were no machine-check results although a check ran")
	}
	if !strings.Contains(msg, machineEvidenceCanary) {
		t.Errorf("the machine check's output did not reach the Judge prompt")
	}
}

func TestJudgeMachineEvidence722_TasklessGoalKeepsEvidence(t *testing.T) {
	msg := runJudgeWithPassingCheck(t, JudgeCriteriaInput{
		Scope:  task.VerdictScopeGoal,
		GoalID: "goal-722",
	}, false)
	assertMachineEvidenceInPrompt(t, msg)
}

func TestJudgeMachineEvidence722_PersistFailureKeepsEvidence(t *testing.T) {
	msg := runJudgeWithPassingCheck(t, JudgeCriteriaInput{
		Scope:  task.VerdictScopeTask,
		TaskID: "t-722-persist-fails",
	}, true)
	assertMachineEvidenceInPrompt(t, msg)
}
