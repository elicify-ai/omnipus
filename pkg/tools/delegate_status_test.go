// delegate_status_test.go: tests for poll and report on a delegation — task status, live activity, child inbox messages, and checkpoint peek.

package tools

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// --- moved from delegate.go tests 2026-09-15 ---

// Founder decision 2026-09-14: when only reasoning is arriving, `delegate
// action=status` must say the worker is thinking. "generating tool call" would
// be false — there is no tool call yet — and an orchestrator reading a
// reasoning-only worker as idle is exactly the misreading that got healthy
// workers killed before.
func TestFormatToolCallProgressLine_ReasoningOnlyReadsAsThinking(t *testing.T) {
	line := formatToolCallProgressLine(ToolCallProgressSnapshot{
		ReasoningBytes: 2048,
		Age:            40 * time.Second,
	})

	want := "  progress: thinking — 2048 bytes of reasoning so far this round, last update 40s ago"
	if line != want {
		t.Fatalf("progress line = %q\nwant            %q", line, want)
	}
	if strings.Contains(line, "tool call") {
		t.Fatalf("a reasoning-only snapshot must not mention a tool call: %q", line)
	}
}

func TestFormatToolCallProgressLine_StaleReasoningSaysStalledWhileThinking(t *testing.T) {
	line := formatToolCallProgressLine(ToolCallProgressSnapshot{
		ReasoningBytes: 10,
		Age:            maxToolCallProgressStaleness + time.Second,
	})
	if !strings.Contains(line, "stale — no update recently, may have stalled while thinking") {
		t.Fatalf("stale reasoning snapshot line = %q, want the stale-while-thinking wording", line)
	}
}

// Reasoning earlier in the round must not relabel a tool call that is
// streaming NOW.
func TestFormatToolCallProgressLine_ToolCallWithEarlierReasoningStillSaysGenerating(t *testing.T) {
	line := formatToolCallProgressLine(ToolCallProgressSnapshot{
		Name:           "write_file",
		ArgsBytes:      300,
		TotalArgsBytes: 300,
		ReasoningBytes: 900,
		Age:            3 * time.Second,
	})
	if !strings.Contains(line, `generating tool call "write_file" — 300 bytes`) {
		t.Fatalf("tool-call snapshot line = %q, want it to keep saying generating tool call", line)
	}
	if strings.Contains(line, "thinking") {
		t.Fatalf("a tool-call snapshot must not read as thinking: %q", line)
	}
}

// TestFormatToolCallProgressLine_FreshVsStale is a pure-function unit test
// for the "still generating" vs. "may have stalled" distinction
// formatToolCallProgressLine renders, without needing to wait
// maxToolCallProgressStaleness (5 minutes) in real time. This is the second
// of the two required scenarios: a recently-recorded snapshot and a
// stale one must render text a human or orchestrator can tell apart.
func TestFormatToolCallProgressLine_FreshVsStale(t *testing.T) {
	fresh := formatToolCallProgressLine(ToolCallProgressSnapshot{
		Name: "web_serve", ArgsBytes: 100, TotalArgsBytes: 100, Age: 2 * time.Second,
	})
	if !strings.Contains(fresh, "generating") {
		t.Errorf("expected a fresh snapshot to render as 'generating', got: %s", fresh)
	}
	if strings.Contains(fresh, "stale") {
		t.Errorf("a fresh snapshot must not render as stale, got: %s", fresh)
	}

	stale := formatToolCallProgressLine(ToolCallProgressSnapshot{
		Name: "web_serve", ArgsBytes: 100, TotalArgsBytes: 100, Age: 10 * time.Minute,
	})
	if !strings.Contains(stale, "stale") {
		t.Errorf("expected a snapshot older than maxToolCallProgressStaleness to render as stale, got: %s", stale)
	}

	if fresh == stale {
		t.Fatal("fresh and stale snapshots must render distinguishably — a caller has no way to tell " +
			"'still working' from 'may have stalled' otherwise")
	}
}

func TestDelegateStatus_FromRecordNotStreaming(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 22, 12, 0, 0, 0, time.UTC)
	tool, lifecycle, inbox, _ := newADR053TestTool(t)
	tool.SetClock(func() time.Time { return now })
	ctx := WithTranscriptSessionID(context.Background(), "parent-status")

	persist := func(id string, createdAt time.Time) {
		t.Helper()
		if err := lifecycle.Persist(&session.LifecycleRecord{
			SessionID: id, Generation: 1, State: session.LifecycleRunning,
			OwnerScopeKind: session.OwnerScopeParentSession, OwnerScopeID: "parent-status",
			SteeredBy:   &session.SteeredBy{SteeringSessionID: "parent-status"},
			WorkspaceID: "ws-status", AgentID: "worker", ParentAgentID: "orchestrator",
			CreatedAt: createdAt,
		}); err != nil {
			t.Fatalf("persist lifecycle record %s: %v", id, err)
		}
	}

	persist("child-with-message", now.Add(-2*time.Minute))
	msg := progressMsgForDelegateTest(t, "child-with-message", "status-message")
	progress, err := msg.AsSessionMessageProgress()
	if err != nil {
		t.Fatalf("decode progress message: %v", err)
	}
	progress.CreatedAt = now.Add(-40 * time.Second)
	progress.Text = "last status line"
	if err := msg.FromSessionMessageProgress(progress); err != nil {
		t.Fatalf("encode progress message: %v", err)
	}
	if _, err := inbox.Append("parent-status", msg); err != nil {
		t.Fatalf("append progress message: %v", err)
	}

	got := tool.Execute(ctx, map[string]any{"action": "status", "session_id": "child-with-message"})
	if got.IsError {
		t.Fatalf("status from durable record failed: %s", got.ForLLM)
	}
	if !strings.Contains(got.ForLLM, "running, last status line, 40 s ago") {
		t.Fatalf("status = %q, want lifecycle state, last inbox line, and inbox age", got.ForLLM)
	}
	if err := inbox.Ack("parent-status", []string{"status-message"}); err != nil {
		t.Fatalf("ack progress message: %v", err)
	}
	got = tool.Execute(ctx, map[string]any{"action": "status", "session_id": "child-with-message"})
	if got.IsError || !strings.Contains(got.ForLLM, "running, last status line, 40 s ago") {
		t.Fatalf("status must retain the latest durable line after acknowledgement, got: %+v", got)
	}

	persist("child-without-message", now.Add(-25*time.Second))
	got = tool.Execute(ctx, map[string]any{"action": "status", "session_id": "child-without-message"})
	if got.IsError {
		t.Fatalf("status without inbox entry failed: %s", got.ForLLM)
	}
	if !strings.Contains(got.ForLLM, "running, no message yet, started 25 s ago") {
		t.Fatalf("status = %q, want lifecycle state and record age without a fabricated message time", got.ForLLM)
	}
}
