// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Two small, mechanical wirings handed to backend-lead alongside C3/M4
// (2026-08-04):
//
//   - M1: delegate(action="inbox_ack") reported a FABRICATED count. A
//     sibling landed pkg/session/message_inbox.go's AckDetailed (commit
//     30166cf8), which reports which requested message_ids genuinely
//     correspond to a message ever appended (Acknowledged) versus which do
//     not (Unknown). This file proves executeInboxAck now reports the
//     TRUTHFUL Acknowledged count and surfaces Unknown ids, instead of
//     unconditionally echoing back len(message_ids).
//
//   - M3: list_jobs(label_contains=...) needs a way to resolve a delegate
//     session id's caller-supplied `label` (delegate(..., label:"...")) —
//     the sibling's pkg/tools/list_jobs_sources.go JobLabelResolver
//     interface is the consuming seam (commit bd37c568), but it is inert
//     until DelegateTool actually implements ResolvableLabels. This file
//     proves the method itself resolves correctly (mirroring
//     ResolvableSessionIDs' exact locking/batch contract) — the loop.go
//     SetLabelResolver wiring that connects it to a real AgentLoop is owned
//     by a different agent (C2) and is out of this file's scope.
package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestDelegateInboxAck_TruthfulCountAndUnknownIDs is the RED/GREEN test for
// M1: before the fix, executeInboxAck reported `len(message_ids)` as the
// acknowledged count regardless of whether any of them corresponded to a
// real message — a caller passing one real id and one wholly fabricated id
// got back "Acknowledged 2 message(s)." Confirmed to fail (message claims 2
// acknowledged) against the pre-fix t.inbox.Ack(...)-only call before this
// fix; now must report exactly 1 and name the unknown id.
func TestDelegateInboxAck_TruthfulCountAndUnknownIDs(t *testing.T) {
	tool, lc, inbox, _ := newADR053TestTool(t)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-ack", Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, SteeredBy: &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"},
		WorkspaceID: "ws-1", AgentID: "worker",
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	// Seed exactly ONE real message under this owner key.
	pctMsg := progressMsgForDelegateTest(t, "child-ack", "pm-real-1")
	if _, err := inbox.Append("parent-1", pctMsg); err != nil {
		t.Fatalf("seed inbox message failed: %v", err)
	}

	// Ack a mix: the one real id, plus a wholly fabricated one.
	result := tool.Execute(ctx, map[string]any{
		"action":     "inbox_ack",
		"session_id": "child-ack",
		"message_ids": []any{
			"pm-real-1",
			"pm-FABRICATED-does-not-exist",
		},
	})
	if result.IsError {
		t.Fatalf("inbox_ack failed: %s", result.ForLLM)
	}

	// Positive lower bound: the truthful count is 1, never 2.
	if !strings.Contains(result.ForLLM, "Acknowledged 1 message(s).") {
		t.Errorf("expected a truthful 'Acknowledged 1 message(s).' — a fabricated id must not "+
			"inflate the count — got: %s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "Acknowledged 2 message(s).") {
		t.Error("must NOT report the fabricated id as acknowledged")
	}
	// The unknown id must be surfaced, not silently absorbed.
	if !strings.Contains(result.ForLLM, "pm-FABRICATED-does-not-exist") {
		t.Errorf("expected the unknown message id to be surfaced in the response, got: %s", result.ForLLM)
	}

	// The REAL id must still actually be acknowledged (drained out).
	drained, _, _, err := inbox.Drain("parent-1", "child-ack", "", 10)
	if err != nil {
		t.Fatalf("Drain after ack failed: %v", err)
	}
	if len(drained) != 0 {
		t.Errorf("expected the real message to be genuinely acknowledged (drained), got %d remaining", len(drained))
	}
}

// TestDelegateInboxAck_AllUnknown_ZeroAcknowledgedNoUnackedSideEffect proves
// the boundary condition: every requested id is fabricated. Acknowledged
// must be exactly 0 (a positive lower bound of "0, not something else" would
// be meaningless here per Binding Rule 4, but this is paired with the
// realistic-mix test above which DOES carry a positive assertion — this test
// exists to confirm the all-unknown case doesn't error out or silently
// report false success).
func TestDelegateInboxAck_AllUnknown_ZeroAcknowledged(t *testing.T) {
	tool, lc, _, _ := newADR053TestTool(t)
	ctx := WithTranscriptSessionID(context.Background(), "parent-2")
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-ack-2", Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, SteeredBy: &session.SteeredBy{SteeringSessionID: "parent-2", RootSessionID: "parent-2"},
		WorkspaceID: "ws-1", AgentID: "worker",
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	result := tool.Execute(ctx, map[string]any{
		"action":      "inbox_ack",
		"session_id":  "child-ack-2",
		"message_ids": []any{"totally-made-up-1", "totally-made-up-2"},
	})
	if result.IsError {
		t.Fatalf("inbox_ack with all-unknown ids should still succeed (report 0 acknowledged), got error: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "Acknowledged 0 message(s).") {
		t.Errorf("expected 'Acknowledged 0 message(s).' when every id is fabricated, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "totally-made-up-1") || !strings.Contains(result.ForLLM, "totally-made-up-2") {
		t.Errorf("expected both unknown ids surfaced, got: %s", result.ForLLM)
	}
}

// TestDelegateTool_ResolvableLabels_AlwaysEmptyPostADR091 replaces the old
// M3 RED/GREEN test, which proved ResolvableLabels resolved a dispatch's
// caller-supplied label from the in-memory task-state index this tool used
// to keep (t.tasks/t.sessionIndex, keyed by task_id). ADR-091's launcher
// migration deleted that index outright — see delegate.go's package doc
// comment and ResolvableLabels' own doc comment — and list_jobs never
// actually wired a label resolver into its subagent-row read path even
// before the deletion (collectSubagentRows takes no labelResolver
// parameter), so this was already unreachable from production. The method
// is kept only to satisfy tools.JobLabelResolver for pkg/agent's existing
// wiring; this test pins its new, honest behavior: every id comes back
// unresolved, with no map to seed.
func TestDelegateTool_ResolvableLabels_AlwaysEmptyPostADR091(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)

	got := tool.ResolvableLabels([]string{"ses-a", "ses-b"})

	if len(got) != 0 {
		t.Errorf("expected no resolvable labels (the backing index was deleted with ADR-091), got %d: %v", len(got), got)
	}
}
