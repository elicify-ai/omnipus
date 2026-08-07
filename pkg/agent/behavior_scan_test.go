// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"os"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// mkToolCall builds a session.ToolCall for test fixtures.
func mkToolCall(tool, status string) session.ToolCall {
	return session.ToolCall{ID: session.ToolCallID(tool + "-" + status), Tool: tool, Status: status}
}

// mkEntry builds a session.TranscriptEntry carrying the given tool calls at
// ts, for test fixtures.
func mkEntry(ts time.Time, calls ...session.ToolCall) session.TranscriptEntry {
	return session.TranscriptEntry{
		ID:        "entry",
		Type:      session.EntryTypeToolCall,
		Role:      "assistant",
		Timestamp: ts,
		ToolCalls: calls,
	}
}

// intPtr is defined package-wide in delegation_enforce_test.go; reused here.

var baseTime = time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)

// TestBehaviorScan_CountMet covers DS-7 "web_search >= 5x, 5 calls -> met, no
// verifier" (FR-034).
func TestBehaviorScan_CountMet(t *testing.T) {
	var entries []session.TranscriptEntry
	for i := 0; i < 5; i++ {
		entries = append(entries, mkEntry(baseTime.Add(time.Duration(i)*time.Second), mkToolCall("web_search", "success")))
	}
	criterion := BehaviorCriterion{Tool: "web_search", MinCount: intPtr(5), Scope: BehaviorScopeTaskSession}

	result := ScanBehaviorCriterionEntries(entries, criterion, time.Time{})

	if !result.Met {
		t.Errorf("Met = false, want true; reason=%q", result.Reason)
	}
	if result.Observed != 5 {
		t.Errorf("Observed = %d, want 5", result.Observed)
	}
	if result.UnknownTool {
		t.Error("UnknownTool = true, want false")
	}
	if result.Reason == "" {
		t.Error("Reason is empty, want an explanation with the observed count")
	}
}

// TestBehaviorScan_CountUnmet covers DS-7 "web_search >= 5x, 4 calls -> unmet,
// no verifier (steering: 1 more search)" (FR-034).
func TestBehaviorScan_CountUnmet(t *testing.T) {
	var entries []session.TranscriptEntry
	for i := 0; i < 4; i++ {
		entries = append(entries, mkEntry(baseTime.Add(time.Duration(i)*time.Second), mkToolCall("web_search", "success")))
	}
	criterion := BehaviorCriterion{Tool: "web_search", MinCount: intPtr(5), Scope: BehaviorScopeTaskSession}

	result := ScanBehaviorCriterionEntries(entries, criterion, time.Time{})

	if result.Met {
		t.Error("Met = true, want false")
	}
	if result.Observed != 4 {
		t.Errorf("Observed = %d, want 4", result.Observed)
	}
}

// TestBehaviorScan_SendMessageCalled covers DS-7's "send_message called"
// met/unmet pair (min_count default of 1).
func TestBehaviorScan_SendMessageCalled(t *testing.T) {
	criterion := BehaviorCriterion{Tool: "send_message", MinCount: intPtr(1), Scope: BehaviorScopeTaskSession}

	t.Run("one call met", func(t *testing.T) {
		entries := []session.TranscriptEntry{mkEntry(baseTime, mkToolCall("send_message", "success"))}
		result := ScanBehaviorCriterionEntries(entries, criterion, time.Time{})
		if !result.Met || result.Observed != 1 {
			t.Errorf("got Met=%v Observed=%d, want Met=true Observed=1", result.Met, result.Observed)
		}
	})

	t.Run("zero calls unmet", func(t *testing.T) {
		result := ScanBehaviorCriterionEntries(nil, criterion, time.Time{})
		if result.Met || result.Observed != 0 {
			t.Errorf("got Met=%v Observed=%d, want Met=false Observed=0", result.Met, result.Observed)
		}
	})
}

// TestBehaviorScan_MaxCountExceeded covers DS-7 "{tool:web_search,
// min_count:3, max_count:5}, log has 6 -> unmet (max exceeded)".
func TestBehaviorScan_MaxCountExceeded(t *testing.T) {
	var entries []session.TranscriptEntry
	for i := 0; i < 6; i++ {
		entries = append(entries, mkEntry(baseTime.Add(time.Duration(i)*time.Second), mkToolCall("web_search", "success")))
	}
	criterion := BehaviorCriterion{
		Tool: "web_search", MinCount: intPtr(3), MaxCount: intPtr(5), Scope: BehaviorScopeTaskSession,
	}

	result := ScanBehaviorCriterionEntries(entries, criterion, time.Time{})

	if result.Met {
		t.Error("Met = true, want false (max_count exceeded)")
	}
	if result.Observed != 6 {
		t.Errorf("Observed = %d, want 6", result.Observed)
	}
}

// TestBehaviorScan_ScopeAttemptVsTaskSession covers DS-7 "{tool:web_search,
// scope:'attempt'}, 5 calls across attempts but 0 in current -> unmet
// (scope=attempt counts only the current attempt)" and the complementary
// task_session scope, which counts everything.
func TestBehaviorScan_ScopeAttemptVsTaskSession(t *testing.T) {
	attemptStart := baseTime.Add(1 * time.Hour)
	// 5 calls from a PRIOR attempt, all before attemptStart.
	var entries []session.TranscriptEntry
	for i := 0; i < 5; i++ {
		entries = append(entries, mkEntry(baseTime.Add(time.Duration(i)*time.Minute), mkToolCall("web_search", "success")))
	}

	t.Run("scope=attempt sees none of the prior attempt", func(t *testing.T) {
		criterion := BehaviorCriterion{Tool: "web_search", MinCount: intPtr(1), Scope: BehaviorScopeAttempt}
		result := ScanBehaviorCriterionEntries(entries, criterion, attemptStart)
		if result.Met {
			t.Error("Met = true, want false — prior-attempt calls must not count in scope=attempt")
		}
		if result.Observed != 0 {
			t.Errorf("Observed = %d, want 0", result.Observed)
		}
	})

	t.Run("scope=task_session sees all 5", func(t *testing.T) {
		criterion := BehaviorCriterion{Tool: "web_search", MinCount: intPtr(5), Scope: BehaviorScopeTaskSession}
		result := ScanBehaviorCriterionEntries(entries, criterion, attemptStart)
		if !result.Met {
			t.Errorf("Met = false, want true; reason=%q", result.Reason)
		}
		if result.Observed != 5 {
			t.Errorf("Observed = %d, want 5", result.Observed)
		}
	})

	t.Run("scope=attempt counts calls at-or-after the cutoff", func(t *testing.T) {
		entriesWithCurrent := append(
			append([]session.TranscriptEntry{}, entries...),
			mkEntry(attemptStart, mkToolCall("web_search", "success")),
			mkEntry(attemptStart.Add(time.Minute), mkToolCall("web_search", "success")),
		)
		criterion := BehaviorCriterion{Tool: "web_search", MinCount: intPtr(2), Scope: BehaviorScopeAttempt}
		result := ScanBehaviorCriterionEntries(entriesWithCurrent, criterion, attemptStart)
		if !result.Met {
			t.Errorf("Met = false, want true; reason=%q", result.Reason)
		}
		if result.Observed != 2 {
			t.Errorf("Observed = %d, want 2 (only the current attempt's calls)", result.Observed)
		}
	})
}

// TestBehaviorScan_FailedCallsNotCounted covers DS-7 "failed/errored tool
// calls of tool -> not counted (successful calls only)".
func TestBehaviorScan_FailedCallsNotCounted(t *testing.T) {
	entries := []session.TranscriptEntry{
		mkEntry(baseTime, mkToolCall("web_search", "success")),
		mkEntry(baseTime.Add(time.Second), mkToolCall("web_search", "error")),
		mkEntry(baseTime.Add(2*time.Second), mkToolCall("web_search", "denied")),
		mkEntry(baseTime.Add(3*time.Second), mkToolCall("web_search", "pending")),
	}
	criterion := BehaviorCriterion{Tool: "web_search", MinCount: intPtr(3), Scope: BehaviorScopeTaskSession}

	result := ScanBehaviorCriterionEntries(entries, criterion, time.Time{})

	if result.Met {
		t.Error("Met = true, want false — only 1 of 4 calls succeeded")
	}
	if result.Observed != 1 {
		t.Errorf("Observed = %d, want 1 (failed/denied/pending calls must not count)", result.Observed)
	}
	if result.UnknownTool {
		t.Error("UnknownTool = true, want false — the tool WAS recorded, just not as success every time")
	}
}

// TestBehaviorScan_NeverCallX covers DS-7's min_count=0 + max_count=0 "never
// call X" pair — met when the tool is never called, violated when it is.
func TestBehaviorScan_NeverCallX(t *testing.T) {
	criterion := BehaviorCriterion{Tool: "delete_file", MinCount: intPtr(0), MaxCount: intPtr(0), Scope: BehaviorScopeTaskSession}

	t.Run("met when never called", func(t *testing.T) {
		entries := []session.TranscriptEntry{mkEntry(baseTime, mkToolCall("read_file", "success"))}
		result := ScanBehaviorCriterionEntries(entries, criterion, time.Time{})
		if !result.Met {
			t.Errorf("Met = false, want true; reason=%q", result.Reason)
		}
		if result.Observed != 0 {
			t.Errorf("Observed = %d, want 0", result.Observed)
		}
	})

	t.Run("violated when called even once", func(t *testing.T) {
		entries := []session.TranscriptEntry{mkEntry(baseTime, mkToolCall("delete_file", "success"))}
		result := ScanBehaviorCriterionEntries(entries, criterion, time.Time{})
		if result.Met {
			t.Error("Met = true, want false — delete_file was called once, violating max_count=0")
		}
		if result.Observed != 1 {
			t.Errorf("Observed = %d, want 1", result.Observed)
		}
	})
}

// TestBehaviorScan_UnknownToolFailClosed covers DS-7 "tool named in criterion
// never recorded in log schema -> fail-closed unmet + flagged (unknown-kind
// guard)".
func TestBehaviorScan_UnknownToolFailClosed(t *testing.T) {
	entries := []session.TranscriptEntry{
		mkEntry(baseTime, mkToolCall("web_search", "success")),
		mkEntry(baseTime.Add(time.Second), mkToolCall("read_file", "success")),
	}
	criterion := BehaviorCriterion{Tool: "totally_not_a_real_tool", MinCount: intPtr(1), Scope: BehaviorScopeTaskSession}

	result := ScanBehaviorCriterionEntries(entries, criterion, time.Time{})

	if result.Met {
		t.Error("Met = true, want false — the criterion's tool never appears in the log")
	}
	if !result.UnknownTool {
		t.Error("UnknownTool = false, want true — the tool never appears under any status")
	}
	if result.Observed != 0 {
		t.Errorf("Observed = %d, want 0", result.Observed)
	}
}

// TestBehaviorScan_KnownToolZeroSuccessNotFlaggedUnknown ensures the
// unknown-tool guard is specifically about a tool that NEVER appears (any
// status) — a tool that was attempted and only ever failed is a legitimate
// unmet result, not an "unknown tool" flag.
func TestBehaviorScan_KnownToolZeroSuccessNotFlaggedUnknown(t *testing.T) {
	entries := []session.TranscriptEntry{
		mkEntry(baseTime, mkToolCall("flaky_tool", "error")),
	}
	criterion := BehaviorCriterion{Tool: "flaky_tool", MinCount: intPtr(1), Scope: BehaviorScopeTaskSession}

	result := ScanBehaviorCriterionEntries(entries, criterion, time.Time{})

	if result.UnknownTool {
		t.Error("UnknownTool = true, want false — flaky_tool WAS recorded, just never succeeded")
	}
	if result.Met {
		t.Error("Met = true, want false")
	}
}

// TestBehaviorScan_InvalidCriterion covers fail-closed behaviour for a
// malformed criterion (min_count < 0, max_count < min_count, missing tool,
// invalid scope) — never a panic, never an implicit pass.
func TestBehaviorScan_InvalidCriterion(t *testing.T) {
	cases := []struct {
		name      string
		criterion BehaviorCriterion
	}{
		{"empty tool", BehaviorCriterion{Tool: "", MinCount: intPtr(1), Scope: BehaviorScopeTaskSession}},
		{"negative min_count", BehaviorCriterion{Tool: "x", MinCount: intPtr(-1), Scope: BehaviorScopeTaskSession}},
		{"max below min", BehaviorCriterion{Tool: "x", MinCount: intPtr(5), MaxCount: intPtr(2), Scope: BehaviorScopeTaskSession}},
		{"invalid scope", BehaviorCriterion{Tool: "x", MinCount: intPtr(1), Scope: "bogus"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := ScanBehaviorCriterionEntries(nil, tc.criterion, time.Time{})
			if result.Met {
				t.Error("Met = true, want false (fail-closed)")
			}
			if result.Reason == "" {
				t.Error("Reason is empty, want an explanation of the invalid criterion")
			}
		})
	}
}

// TestBehaviorCriterionValidate directly exercises BehaviorCriterion.Validate
// for the FR-034 payload constraints.
func TestBehaviorCriterionValidate(t *testing.T) {
	valid := BehaviorCriterion{Tool: "web_search", MinCount: intPtr(1), Scope: BehaviorScopeTaskSession}
	if err := validateBehaviorCriterion(valid); err != nil {
		t.Errorf("Validate() = %v, want nil for a well-formed criterion", err)
	}

	neverCallX := BehaviorCriterion{Tool: "x", MinCount: intPtr(0), MaxCount: intPtr(0), Scope: BehaviorScopeAttempt}
	if err := validateBehaviorCriterion(neverCallX); err != nil {
		t.Errorf("Validate() = %v, want nil — min_count=0+max_count=0 is a valid ('never call X') shape", err)
	}
}

// TestBehaviorScan_ViaPartitionStore_SessionID proves the session-id-based
// wrapper (ScanBehaviorCriterion) round-trips through a real
// session.PartitionStore — the "given a session id" contract, not just the
// pure in-memory evaluator.
func TestBehaviorScan_ViaPartitionStore_SessionID(t *testing.T) {
	dir, err := os.MkdirTemp("", "behavior-scan-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	store := session.NewPartitionStore(dir, "agent-1")
	meta, err := store.NewSession("chat", "test-model", "test-provider")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	for i := 0; i < 3; i++ {
		entry := session.TranscriptEntry{
			ID:        "e" + string(rune('0'+i)),
			Type:      session.EntryTypeToolCall,
			Role:      "assistant",
			Timestamp: baseTime.Add(time.Duration(i) * time.Second),
			ToolCalls: []session.ToolCall{mkToolCall("web_search", "success")},
		}
		if appendErr := store.AppendMessage(meta.ID, entry); appendErr != nil {
			t.Fatalf("AppendMessage: %v", appendErr)
		}
	}

	criterion := BehaviorCriterion{Tool: "web_search", MinCount: intPtr(3), Scope: BehaviorScopeTaskSession}
	result, err := ScanBehaviorCriterion(store, meta.ID, criterion, time.Time{})
	if err != nil {
		t.Fatalf("ScanBehaviorCriterion: %v", err)
	}
	if !result.Met {
		t.Errorf("Met = false, want true; reason=%q", result.Reason)
	}
	if result.Observed != 3 {
		t.Errorf("Observed = %d, want 3", result.Observed)
	}

	// A nonexistent session id must surface a real error, not a silently
	// empty/zero result.
	if _, err := ScanBehaviorCriterion(store, "session_does_not_exist", criterion, time.Time{}); err == nil {
		t.Error("ScanBehaviorCriterion with a nonexistent session id: got nil error, want a read error")
	}
}
