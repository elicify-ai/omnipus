// delegate_status_test.go: tests for poll and report on a delegation — task status, live activity, child inbox messages, and checkpoint peek.

package tools

import (
	"strings"
	"testing"
	"time"
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
