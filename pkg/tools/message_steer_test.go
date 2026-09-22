// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-091 WP-B — boundary 8 (landing order §6, FR-B-009): a steered
// session's message tool may target only its own session's conversation.
// TDD plan test 5, TestMessageTool_SteeredSessionOwnChatOnly.

package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/steer"
)

// fakeSteerAudience is a minimal steer.AudienceResolver double, keyed by
// session id — a "real ownership" fixture per FR-B-009's own requirement
// ("never a nil ownership stub"), paired with a genuine fakeOwnership
// instance in the tests below rather than an unset (nil) t.ownership.
type fakeSteerAudience struct {
	audience map[string]steer.Audience
}

var _ steer.AudienceResolver = (*fakeSteerAudience)(nil)

func (f *fakeSteerAudience) Audience(_ context.Context, sessionID string) (steer.Audience, steer.Class, error) {
	a, ok := f.audience[sessionID]
	if !ok {
		return steer.AudienceUser, steer.ClassOrdinaryRoot, nil
	}
	if a == steer.AudienceSteeringSession {
		return a, steer.ClassSteered, nil
	}
	return a, steer.ClassOrdinaryRoot, nil
}

func TestMessageTool_SteeredSessionOwnChatOnly(t *testing.T) {
	const childSession = "child-1"
	newSteeredTool := func() *MessageTool {
		tool := NewMessageTool()
		// Real channel ownership (FR-B-009: "never a nil ownership stub") —
		// the root's webchat channel is unbound (shared, per ADR-065), and a
		// sibling's own channel is bound to a DIFFERENT agent — both would be
		// ALLOWED by denyUnownedTarget alone; this test proves the steered
		// own-chat-only rule refuses them anyway.
		tool.SetChannelOwnership(fakeOwnership{owner: map[string]string{
			"sibling-channel": "W1/sibling-agent",
		}})
		tool.SetSteerAudienceResolver(&fakeSteerAudience{
			audience: map[string]steer.Audience{childSession: steer.AudienceSteeringSession},
		})
		tool.SetSendCallback(func(string, string, string, SendOrigin) error { return nil })
		return tool
	}

	t.Run("own_conversation_allowed", func(t *testing.T) {
		tool := newSteeredTool()
		ctx := WithToolContext(context.Background(), "webchat", "child-1")
		ctx = WithTranscriptSessionID(ctx, childSession)
		ctx = WithAgentID(ctx, "worker")
		result := tool.Execute(ctx, map[string]any{"content": "status update"})
		if result.IsError {
			t.Fatalf("expected the steered session's own conversation to be allowed, got error: %s", result.ForLLM)
		}
	})

	t.Run("root_webchat_chat_id_refused", func(t *testing.T) {
		tool := newSteeredTool()
		ctx := WithToolContext(context.Background(), "webchat", "child-1")
		ctx = WithTranscriptSessionID(ctx, childSession)
		ctx = WithAgentID(ctx, "worker")
		result := tool.Execute(ctx, map[string]any{
			"content": "sneaking into the root chat", "channel": "webchat", "chat_id": "root-chat-id",
		})
		assertSteeredSessionOwnChatOnlyRefusal(t, result)
	})

	t.Run("sibling_session_refused", func(t *testing.T) {
		tool := newSteeredTool()
		ctx := WithToolContext(context.Background(), "webchat", "child-1")
		ctx = WithTranscriptSessionID(ctx, childSession)
		ctx = WithAgentID(ctx, "sibling-agent")
		result := tool.Execute(ctx, map[string]any{
			"content": "x", "channel": "sibling-channel", "chat_id": "sibling-chat",
		})
		assertSteeredSessionOwnChatOnlyRefusal(t, result)
	})

	t.Run("external_channel_refused", func(t *testing.T) {
		tool := newSteeredTool()
		ctx := WithToolContext(context.Background(), "webchat", "child-1")
		ctx = WithTranscriptSessionID(ctx, childSession)
		ctx = WithAgentID(ctx, "worker")
		result := tool.Execute(ctx, map[string]any{
			"content": "x", "channel": "telegram", "chat_id": "some-telegram-chat",
		})
		assertSteeredSessionOwnChatOnlyRefusal(t, result)
	})

	t.Run("ordinary_root_session_unaffected", func(t *testing.T) {
		tool := NewMessageTool()
		tool.SetSteerAudienceResolver(&fakeSteerAudience{audience: map[string]steer.Audience{}})
		tool.SetSendCallback(func(string, string, string, SendOrigin) error { return nil })
		// Targets the turn's OWN conversation, isolating "does boundary 8
		// wrongly interfere with an ordinary root" from ADR-065's separate,
		// unrelated ownership check (unset here on purpose).
		ctx := WithToolContext(context.Background(), "webchat", "root-chat")
		ctx = WithTranscriptSessionID(ctx, "root-session")
		result := tool.Execute(ctx, map[string]any{"content": "x"})
		if result.IsError {
			t.Fatalf("expected an ordinary root session to be unaffected by the steered own-chat-only rule, got: %s", result.ForLLM)
		}
	})
}

func assertSteeredSessionOwnChatOnlyRefusal(t *testing.T, result *ToolResult) {
	t.Helper()
	if !result.IsError {
		t.Fatal("expected the steered session's off-own-chat target to be refused")
	}
	if !strings.Contains(result.ForLLM, "steered_session_own_chat_only") {
		t.Fatalf("expected the named error steered_session_own_chat_only, got: %s", result.ForLLM)
	}
}
