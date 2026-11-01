// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// judge_tool_output_1484_test.go — issue #1484 (a): a scheduled routine was
// marked failed although it succeeded, because the Judge was shown only
// "[tool_call] read_file -> success params=…" and never the tool's output.
// The brief had been written and read back; the Judge could not see the
// read-back. The Judge's transcript window must carry the tool result the
// working agent itself saw (bounded, redacted), and a result that was
// projected away must still read as "projected away", never as nothing.

package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

const briefReadbackCanary = "MORNING-BRIEF-READBACK-1484: 3 meetings, 2 deadlines, weather 31C"

// TestJudgeToolOutput1484_ScheduledTaskJudgeSeesToolResults reproduces the
// routine: a task-scope adjudication (what a scheduled task's run gets) whose
// working session wrote a file and read it back. The read-back text must
// reach the prose Judge's prompt.
func TestJudgeToolOutput1484_ScheduledTaskJudgeSeesToolResults(t *testing.T) {
	resetVerifierBlockedCheckSeams(t)
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)

	store := al.GetSessionStore()
	if store == nil {
		t.Fatal("the loop must carry a session store")
	}
	meta, err := store.NewSession(session.SessionTypeChat, "web", "native-agent")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	entries := []session.TranscriptEntry{
		{Role: "user", Content: "Routine: morning-brief", AgentID: "native-agent"},
		{
			Role: "assistant", Type: session.EntryTypeToolCall, AgentID: "native-agent",
			ToolCalls: []session.ToolCall{
				{
					ID: "tc-write", Tool: "write_file", Status: "success",
					Parameters: map[string]any{"path": "briefs/2026-10-22.md"},
					Result:     map[string]any{"text": "wrote 412 bytes to briefs/2026-10-22.md"},
				},
				{
					ID: "tc-read", Tool: "read_file", Status: "success",
					Parameters: map[string]any{"path": "briefs/2026-10-22.md"},
					Result:     map[string]any{"text": briefReadbackCanary},
				},
			},
		},
		{Role: "assistant", Content: "The brief is written.", AgentID: "native-agent"},
	}
	for _, e := range entries {
		if appendErr := store.AppendTranscript(meta.ID, e); appendErr != nil {
			t.Fatalf("AppendTranscript: %v", appendErr)
		}
	}
	tk := &task.Task{
		ID: "t-1484-brief", AgentID: "native-agent", WorkspaceID: "test-ws", Title: "Routine: morning-brief",
		SessionID: meta.ID,
	}
	if err := GetTaskStore(al).Create(tk); err != nil {
		t.Fatalf("task Create: %v", err)
	}

	rec := &recordingJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{
			Content: `{"met": true, "criteria": [{"id":"p1","met":true,"reason":"read back","evidence_quote":"MORNING-BRIEF-READBACK-1484"}]}`,
		}, nil
	}}
	judgeInst.Provider = rec

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          tk.ID,
		AssigneeAgentID: "native-agent",
		Criteria:        []task.AcceptanceCriterion{proseCriterion("p1", "the brief file was written and read back")},
		Attempt:         1,
		ClaimText:       "done",
	})
	if result.Unavailable {
		t.Fatalf("unexpected Unavailable: %s", result.Reason)
	}
	msg := rec.capturedLastUserMessage()
	if msg == "" {
		t.Fatal("the prose Judge was never called")
	}
	if !strings.Contains(msg, briefReadbackCanary) {
		t.Errorf("the read_file output did not reach the Judge prompt; the Judge cannot see the read-back.\nprompt:\n%s", msg)
	}
	if !strings.Contains(msg, "wrote 412 bytes") {
		t.Errorf("the write_file output did not reach the Judge prompt")
	}
}

// TestJudgeToolOutput1484_OutputBoundedWithMarker: one call's output is capped
// with an explicit marker, so a truncated output never reads as complete.
func TestJudgeToolOutput1484_OutputBoundedWithMarker(t *testing.T) {
	long := strings.Repeat("b", 10*1024)
	tc := session.ToolCall{Tool: "read_file", Status: "success", Result: map[string]any{"text": long}}
	got := formatTierOneToolCall(tc, nil)
	if !strings.Contains(got, "bbbb") {
		t.Fatalf("output must be rendered, got %q", got)
	}
	if len(got) > 4*1024 {
		t.Errorf("one call's rendered output must stay bounded, got %d bytes", len(got))
	}
	if !strings.Contains(got, tierOneTruncationMarker) {
		t.Errorf("a cut output must carry the truncation marker")
	}
}

// TestJudgeToolOutput1484_ProjectedResultStillMarked: when the window
// projection cut or emptied the result, whatever text the model saw is shown
// AND the explicit projection marker stays, so the Judge never reads it as
// the complete output or as "returned nothing".
func TestJudgeToolOutput1484_ProjectedResultStillMarked(t *testing.T) {
	for _, state := range []string{"capped", "emptied"} {
		tc := session.ToolCall{
			Tool: "read_file", Status: "success", ContentState: state,
			Result: map[string]any{"text": "first part of the file"},
		}
		got := formatTierOneToolCall(tc, nil)
		if !strings.Contains(got, "projected away") {
			t.Errorf("ContentState=%q: the projection marker must stay, got %q", state, got)
		}
		if !strings.Contains(got, "first part of the file") {
			t.Errorf("ContentState=%q: the text the model saw must be shown, got %q", state, got)
		}
	}
}

// TestJudgeToolOutput1484_OutputIsRedacted: the rendered output goes through
// the same redactor as parameters and errors.
func TestJudgeToolOutput1484_OutputIsRedacted(t *testing.T) {
	redact := func(s string) string { return strings.ReplaceAll(s, "live-secret-value", "[REDACTED]") }
	tc := session.ToolCall{Tool: "read_file", Status: "success", Result: map[string]any{"text": "token=live-secret-value"}}
	got := formatTierOneToolCall(tc, redact)
	if strings.Contains(got, "live-secret-value") {
		t.Fatalf("tool output must be redacted before it reaches the Judge, got %q", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Errorf("the redaction marker must stay visible, got %q", got)
	}
}
