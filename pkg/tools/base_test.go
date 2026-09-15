package tools

import (
	"context"
	"testing"
)

// TestToolContext_RootChatSessionID_UnsetIsEmpty proves the unset default:
// a context that never carried WithToolRootChatSessionID reads back "", the
// documented "no root chat to check the gate against" signal ADR-085 FR-020
// relies on for a non-chat turn (cron/heartbeat/task) to skip the panel-scope
// half of the control gate's two-key evaluation rather than failing closed.
func TestToolContext_RootChatSessionID_UnsetIsEmpty(t *testing.T) {
	if got := ToolRootChatSessionID(context.Background()); got != "" {
		t.Fatalf("expected empty root chat session id on an unstamped context, got %q", got)
	}
}

// TestToolContext_RootChatSessionID_RoundTrip proves the FR-021 carrier
// itself: WithToolRootChatSessionID stamps a value that ToolRootChatSessionID
// reads back unchanged, mirroring the existing
// WithTranscriptSessionID/ToolTranscriptSessionID pair this key is modelled
// on.
func TestToolContext_RootChatSessionID_RoundTrip(t *testing.T) {
	ctx := WithToolRootChatSessionID(context.Background(), "session_root_chat_ABC123")

	got := ToolRootChatSessionID(ctx)
	if got != "session_root_chat_ABC123" {
		t.Fatalf("expected root chat session id %q, got %q", "session_root_chat_ABC123", got)
	}
}

// TestToolContext_RootChatSessionID_EmptyStringRoundTrips confirms
// WithToolRootChatSessionID does not special-case "" (unlike
// WithTurnWorkspaceDir's deliberate no-op-on-empty convention) — stamping ""
// explicitly still reads back "", identical to never stamping at all. FR-021
// draws no distinction between these two cases, so the helper must not
// invent one.
func TestToolContext_RootChatSessionID_EmptyStringRoundTrips(t *testing.T) {
	ctx := WithToolRootChatSessionID(context.Background(), "")
	if got := ToolRootChatSessionID(ctx); got != "" {
		t.Fatalf("expected empty string to round-trip as empty, got %q", got)
	}
}

// TestToolContext_RootChatSessionID_DistinctFromTranscriptSessionID proves
// the two context keys this file now carries — the pre-existing transcript
// session id and the new FR-021 root-chat session id — are genuinely
// independent values, not the same key under two names. ADR-085 FR-021
// deliberately adds a SIBLING key rather than repurposing
// ctxKeyTranscriptSessionID because the two mean different things: the
// transcript session id is where THIS turn's own history is written, while
// the root-chat session id is the CHAT the control gate must check even for
// a delegated child driving its own tab set (FR-023) — the two can differ
// inside a single call.
func TestToolContext_RootChatSessionID_DistinctFromTranscriptSessionID(t *testing.T) {
	ctx := context.Background()
	ctx = WithTranscriptSessionID(ctx, "transcript-session-1")
	ctx = WithToolRootChatSessionID(ctx, "root-chat-session-2")

	if got := ToolTranscriptSessionID(ctx); got != "transcript-session-1" {
		t.Fatalf("expected transcript session id %q to survive the second stamp, got %q", "transcript-session-1", got)
	}
	if got := ToolRootChatSessionID(ctx); got != "root-chat-session-2" {
		t.Fatalf("expected root chat session id %q, got %q", "root-chat-session-2", got)
	}
}

// TestToolContext_RootChatSessionID_NotLeakedByUnrelatedStamp proves a
// context stamped with OTHER tool-context values (channel/chatID) — but
// never with WithToolRootChatSessionID — still reads back "" for the root
// chat id. Each context key is independently keyed and only reachable
// through its own accessor pair; stamping one must never make another
// helper start returning a non-empty value it was never given.
func TestToolContext_RootChatSessionID_NotLeakedByUnrelatedStamp(t *testing.T) {
	ctx := WithToolContext(context.Background(), "webchat", "chat-1")
	ctx = WithAgentID(ctx, "jim")

	if got := ToolRootChatSessionID(ctx); got != "" {
		t.Fatalf("expected root chat session id to remain empty when only unrelated context keys were stamped, got %q", got)
	}
}
