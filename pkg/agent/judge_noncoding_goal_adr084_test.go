// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// judge_noncoding_goal_adr084_test.go covers ADR-084 revision 9's §Q
// FR-111 — "Tier 3 is the default. For a criterion with no declared check
// and no declared behaviour payload — the overwhelming majority, and
// every criterion of a goal about a document, an email, a booking or a
// piece of research — the path is: tier-1 facts assembled into the
// evidence block, then the Judge, with nothing in between." This is the
// non-coding-goal end-to-end proof the spec's own §Q calls out as a
// recorded finding: every worked example in earlier revisions of the
// judge spec was a software artifact, so this test deliberately picks a
// send_email criterion — no shell, no exit code, no check, no behaviour
// payload — and proves TWO things at once: (a) zero shell executions
// occur anywhere in the adjudication (there is no rung to invent one from,
// D14 rule 2/FR-109/FR-110 — an artifact-shaped criterion is an ordinary
// prose criterion, full stop), and (b) the Judge's evidence block
// actually carries the send_email call's own parameters (FR-105's tier-1
// rendering, wired end to end through buildJudgeUserContent's "Session
// transcript window" section).

package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestNonCodingGoal_EmailCriterion_DecidedAtTierOne is the holdout H9 /
// FR-111 oracle: a `send_email` criterion, no check anywhere, zero shell
// executions in the adjudication, and the Judge's evidence block
// containing the call's `to` parameter.
func TestNonCodingGoal_EmailCriterion_DecidedAtTierOne(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	workerInst, _ := al.GetRegistry().GetAgent("native-agent")

	// A bash tool double that FAILS the test outright if it is ever
	// invoked — this criterion has no task.KindCheck payload, so nothing
	// in a correct D14 implementation may reach it.
	fakeBash := &fakeBashTool{result: &tools.ToolResult{ForLLM: "SHOULD NEVER RUN"}}
	workerInst.Tools.RegisterReplacing(fakeBash)
	allowBashPolicy(workerInst)

	// Seed the WORKING session (the one the verifier's window feed reads,
	// via the task's own SessionID) with the send_email call that is this
	// goal's only real evidence — tier 1, a by-product of work already
	// done, never re-run.
	workerStore := al.GetAgentStore("native-agent")
	if workerStore == nil {
		t.Fatal("no session store for native-agent")
	}
	meta, err := workerStore.NewSession("chat", "test-model", "test-provider")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	ts := GetTaskStore(al)
	tk := &task.Task{
		ID: "t-fr111", Title: "email the supplier", AgentID: "native-agent",
		WorkspaceID: "ws-fr111",
		SessionID:   meta.ID,
		Criteria:    []task.AcceptanceCriterion{proseCriterion("c1", "the supplier was emailed about the delay")},
	}
	if err := ts.Create(tk); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	entry := session.TranscriptEntry{
		ID: "e1", Type: session.EntryTypeToolCall, Role: "assistant", Timestamp: baseTime,
		ToolCalls: []session.ToolCall{{
			ID: "call-1", Tool: "send_email", Status: "success",
			Parameters: map[string]any{"to": "supplier@example.com", "subject": "Delay notice"},
		}},
	}
	if err := workerStore.AppendTranscript(meta.ID, entry); err != nil {
		t.Fatalf("AppendTranscript: %v", err)
	}

	capture := &capturingJudgeProvider{
		response: &providers.LLMResponse{
			Content: `{"met":true,"criteria":[{"id":"c1","met":true,"reason":"send_email was called with the supplier's address","evidence_quote":"to: supplier@example.com","evidence_source":"transcript"}]}`,
		},
	}
	judgeInst.Provider = capture

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-fr111",
		AssigneeAgentID: "native-agent",
		Criteria:        tk.Criteria,
		Attempt:         1,
		ClaimText:       "I emailed the supplier about the delay.",
	})
	if result.Unavailable {
		t.Fatalf("unexpected Unavailable: %s", result.Reason)
	}
	if !result.Verdict.Met {
		t.Fatalf("expected met, got per-criterion: %+v", result.Verdict.PerCriterion)
	}
	if fakeBash.callCount() != 0 {
		t.Fatalf("JUDGE-FR-104/FR-109/FR-110: zero shell executions expected for a non-coding "+
			"criterion with no declared check — got %d", fakeBash.callCount())
	}
	content := capture.capturedContent()
	if !strings.Contains(content, "send_email") || !strings.Contains(content, "supplier@example.com") {
		t.Fatalf("JUDGE-FR-105/FR-111: the Judge's evidence block must carry the send_email call's "+
			"own parameters; got content that does not:\n%s", content)
	}
}
