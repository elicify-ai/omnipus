// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Defect C3 (UAT 2026-07-31, MAJOR->BLOCKER): delegate(action="status") goes
// blind after any WebSocket reconnect.
//
// Root cause (traced against real store-backed state, not a mock):
//   - pkg/gateway/websocket.go:615 mints chatID := "webchat:" + uuid.New().String()
//     fresh on EVERY WebSocket connection — a page refresh, a network blip, or
//     any client that opens one connection per message all rotate it.
//   - pkg/gateway/websocket.go:1707 sets Channel: "webchat" as a fixed literal
//     for every webchat conversation, so it provides NO per-conversation
//     isolation for this channel at all — chatID was the ONLY thing doing that
//     job, and it is exactly what breaks on reconnect.
//   - The durable identity survives reconnect: websocket.go's `sessionID`
//     (msg.SessionID) is resent unchanged by the client on every reconnect
//     (websocket.go:1608-1612 only fills h.sessionIDs[chatID] the first time a
//     NEW chatID sees an already-known session_id — it never mints a new one).
//     DelegateTaskState.SessionID (delegate.go's executeAsync) already
//     captures this exact durable id via ToolTranscriptSessionID(ctx) at
//     dispatch time.
//   - executeStatus's scoping filter (pre-fix) compared ONLY
//     callerChannel/callerChatID against task.OriginChannel/OriginChatID —
//     never consulting the durable SessionID it already had in hand — so any
//     reconnect (new chatID) made a prior turn's own dispatch permanently
//     unfindable via action:"status", while peek/list_jobs (which key off the
//     durable session id) kept reporting it alive.
//
// This file proves the reconnect case is fixed (a caller's own child is found
// again across the chatID rotation) AND proves cross-conversation isolation
// still holds (a different durable session can never see it) — the two
// halves the fix must not trade off against each other.
package tools

import (
	"context"
	"strings"
	"testing"
)

// TestDelegateStatus_SurvivesReconnect_GetByID is the direct reproduction of
// UAT's paired-controls test: same-connection two-turn finds the task;
// reconnect (new ephemeral chatID, SAME durable session_id) must ALSO find
// it. Before the fix this failed with "No subagent found with task ID: ...",
// identical to a genuine not-found, even though the task is very much alive.
func TestDelegateStatus_SurvivesReconnect_GetByID(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	const durableSessionID = "sess-durable-AAAA"
	tool.mu.Lock()
	tool.tasks["delegate-1"] = &DelegateTaskState{
		ID:            "delegate-1",
		Task:          "mine",
		Status:        "running",
		OriginChannel: "webchat",
		OriginChatID:  "webchat:conn-A", // the connection that dispatched it
		SessionID:     durableSessionID, // the durable identity that survives reconnect
	}
	tool.mu.Unlock()

	// First turn: same connection that dispatched — must find it (sanity,
	// mirrors the pre-existing passing case).
	sameConnCtx := WithTranscriptSessionID(
		WithToolContext(context.Background(), "webchat", "webchat:conn-A"),
		durableSessionID,
	)
	result := tool.Execute(sameConnCtx, map[string]any{"action": "status", "task_id": "delegate-1"})
	if result.IsError {
		t.Fatalf("same-connection status lookup should succeed, got error: %s", result.ForLLM)
	}

	// Reconnect: brand-new WebSocket connection mints a brand-new chatID
	// (pkg/gateway/websocket.go:615), but the client resent the SAME durable
	// session_id, so ToolTranscriptSessionID(ctx) is unchanged.
	reconnectedCtx := WithTranscriptSessionID(
		WithToolContext(context.Background(), "webchat", "webchat:conn-B-AFTER-RECONNECT"),
		durableSessionID,
	)
	result = tool.Execute(reconnectedCtx, map[string]any{"action": "status", "task_id": "delegate-1"})
	if result.IsError {
		t.Fatalf("status lookup must survive a reconnect (durable session_id unchanged, only "+
			"the ephemeral chatID rotated) — got error: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "delegate-1") {
		t.Errorf("expected the task's own status block after reconnect, got:\n%s", result.ForLLM)
	}
}

// TestDelegateStatus_SurvivesReconnect_ListAll is the list-all-tasks sibling
// of the GetByID case above — same durable-session identity, same reconnect
// scenario, but through the no-task_id/no-session_id listing path.
func TestDelegateStatus_SurvivesReconnect_ListAll(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	const durableSessionID = "sess-durable-BBBB"
	tool.mu.Lock()
	tool.tasks["delegate-2"] = &DelegateTaskState{
		ID:            "delegate-2",
		Task:          "mine too",
		Status:        "completed",
		OriginChannel: "webchat",
		OriginChatID:  "webchat:conn-X",
		SessionID:     durableSessionID,
	}
	// A different conversation's task, dispatched over a different durable
	// session — must never show up in the reconnected caller's list.
	tool.tasks["delegate-3"] = &DelegateTaskState{
		ID:            "delegate-3",
		Task:          "someone else's",
		Status:        "completed",
		OriginChannel: "webchat",
		OriginChatID:  "webchat:conn-Y",
		SessionID:     "sess-durable-OTHER",
	}
	tool.mu.Unlock()

	reconnectedCtx := WithTranscriptSessionID(
		WithToolContext(context.Background(), "webchat", "webchat:conn-X-AFTER-RECONNECT"),
		durableSessionID,
	)
	result := tool.Execute(reconnectedCtx, map[string]any{"action": "status"})
	if result.IsError {
		t.Fatalf("list-all status must survive a reconnect, got error: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "delegate-2") {
		t.Errorf("expected own task visible after reconnect, got:\n%s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "delegate-3") {
		t.Errorf("must NOT see a different durable session's task, got:\n%s", result.ForLLM)
	}
}

// TestDelegateStatus_DifferentDurableSession_StillIsolated is the
// cross-conversation isolation proof the fix must preserve: two DIFFERENT
// durable sessions must never see each other's tasks via action:"status",
// even though this scenario's ephemeral OriginChannel happens to match
// (webchat is a shared literal for every webchat connection — see the file
// doc comment) and even if a chatID were to collide by coincidence, the
// durable session id must be the authoritative isolation boundary once both
// sides have one.
func TestDelegateStatus_DifferentDurableSession_StillIsolated(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	tool.mu.Lock()
	tool.tasks["delegate-7"] = &DelegateTaskState{
		ID:            "delegate-7",
		Task:          "conversation A's task",
		Status:        "completed",
		Result:        "conversation A's private result",
		OriginChannel: "webchat",
		OriginChatID:  "webchat:conn-A",
		SessionID:     "sess-durable-CONV-A",
	}
	tool.mu.Unlock()

	// A caller on a genuinely different durable session (a different browser
	// tab / different conversation entirely) must get "not found" — never the
	// task, never its private result.
	otherConvCtx := WithTranscriptSessionID(
		WithToolContext(context.Background(), "webchat", "webchat:conn-Z"),
		"sess-durable-CONV-B",
	)
	result := tool.Execute(otherConvCtx, map[string]any{"action": "status", "task_id": "delegate-7"})
	if !result.IsError {
		t.Fatalf("expected 'not found' for a different durable session, got success: %s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "conversation A's private result") {
		t.Error("must never leak another conversation's task data across durable sessions")
	}
	if !strings.Contains(result.ForLLM, "No subagent found") {
		t.Errorf("expected a 'not found' style message, got: %s", result.ForLLM)
	}
}

// TestDelegateStatus_NoDurableSessionOnEitherSide_FallsBackToChannelChatID
// pins the fallback behavior for callers where no durable session id is in
// play at all (e.g. a direct programmatic Execute call, or a task minted
// before any transcript session was bound) — the pre-existing
// channel+chatID scoping from TestDelegateStatus_ChannelFiltering_GetByID
// must still govern in that case, unchanged.
func TestDelegateStatus_NoDurableSessionOnEitherSide_FallsBackToChannelChatID(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	tool.mu.Lock()
	tool.tasks["delegate-99"] = &DelegateTaskState{
		ID:            "delegate-99",
		Task:          "secret",
		Status:        "completed",
		Result:        "private data",
		OriginChannel: "slack",
		OriginChatID:  "room-Z",
		// SessionID intentionally empty — no durable identity captured at
		// dispatch time.
	}
	tool.mu.Unlock()

	ctx := WithToolContext(context.Background(), "slack", "room-OTHER")
	result := tool.Execute(ctx, map[string]any{"action": "status", "task_id": "delegate-99"})
	if !result.IsError {
		t.Errorf("expected error (cross-chat lookup blocked via legacy channel/chatID scoping), got: %s", result.ForLLM)
	}
}

// TestDelegateStatus_CallerHasDurableSession_TaskDoesNot verifies the
// asymmetric case: the CALLER's context carries a durable session id but the
// stored task does not (e.g. registered by an older build, or a direct
// Execute call with no transcript session bound) — the fix must fall back to
// the legacy channel/chatID comparison rather than rejecting outright just
// because task.SessionID is empty.
func TestDelegateStatus_CallerHasDurableSession_TaskDoesNot(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	tool.mu.Lock()
	tool.tasks["delegate-42"] = &DelegateTaskState{
		ID:            "delegate-42",
		Task:          "legacy task, no session id",
		Status:        "running",
		OriginChannel: "webchat",
		OriginChatID:  "webchat:conn-LEGACY",
		// SessionID intentionally empty.
	}
	tool.mu.Unlock()

	// Same channel+chatID as the task, but the CALLER now carries a durable
	// session id the task itself never recorded.
	ctx := WithTranscriptSessionID(
		WithToolContext(context.Background(), "webchat", "webchat:conn-LEGACY"),
		"sess-durable-CALLER-ONLY",
	)
	result := tool.Execute(ctx, map[string]any{"action": "status", "task_id": "delegate-42"})
	if result.IsError {
		t.Fatalf("expected the legacy channel/chatID match to still succeed when the task has no "+
			"durable session id of its own, got error: %s", result.ForLLM)
	}
}
