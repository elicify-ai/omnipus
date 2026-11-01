package tools

// message_classification_test.go — T-12, tool classification (channels-spec-r8
// T-4 / T-12; dataset D3 = scenario table S-40; FR-040, FR-041, FR-043;
// S-41's tool-level refusal texts).
//
// The six original preserved/unwired roots below remain byte-for-byte.
// The original four missing-start-seam roots now use the actual SetStartPolicy
// API and SendOrigin.Operation. Their oracles remain T-3/T-4/S-40/S-41/D2;
// no expected text is obtained by invoking the implementation.
// Real MessageTool + StartAllowed + Manager.Availability; only the outbound
// callback and external adapter state are controlled. The separate T-32 row
// drives the constructor-installed production policy and actual bus callback.

import (
	"context"
	"errors"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/require"
)

// t12MainCtx is a main-session turn on telegram:chat-1.
func t12MainCtx() context.Context {
	return WithTranscriptSessionID(
		WithToolContext(WithWorkspaceID(WithAgentID(context.Background(), "mia"), "ws-t12"), "telegram", "chat-1"),
		"main-1")
}

// t12NonMainCtx is a non-main session in the same turn conversation.
func t12NonMainCtx() context.Context {
	return WithTranscriptSessionID(
		WithToolContext(WithAgentID(context.Background(), "mia"), "telegram", "chat-1"),
		"chat-session")
}

// t12ConnectorGuard is the real guard shape for a single telegram connection.
func t12ConnectorGuard(sessionID, channel string) bool {
	return sessionID == "main-1" && channel == "telegram"
}

func TestT12_S40_MainWebchatContentOnly_Unchanged(t *testing.T) {
	// S-40 row 1: a main on webchat sends with content only — unchanged
	// (webchat default; shared and unenforced).
	tool := NewMessageTool()
	sent := false
	tool.SetSendCallback(func(channel, chatID, content string, o SendOrigin) error {
		sent = true
		require.Equal(t, "webchat", channel)
		require.Equal(t, "w", chatID)
		return nil
	})
	ctx := WithTranscriptSessionID(
		WithToolContext(WithAgentID(context.Background(), "mia"), "webchat", "w"), "main-1")
	res := tool.Execute(ctx, map[string]any{"content": "hello"})
	require.False(t, res.IsError, "S-40 row 1: webchat default stays unchanged: %s", res.ForLLM)
	require.True(t, sent, "S-40 row 1: the send happens")
	require.Contains(t, res.ForLLM, "Message sent to webchat:w")
}

func TestT12_S40_MainConnectorRows_ReplyOnlyRefusal(t *testing.T) {
	// S-40 rows 2 and 3: a main on telegram:chat-1 with content only, or with
	// channel=telegram only (the chat id defaults from the turn), is refused
	// with ErrMainConnectorReplyOnly and its amended text — step 2 of T-4,
	// preserved by FR-043's never-wired rule.
	for name, args := range map[string]map[string]any{
		"content only": {"content": "confidential"},
		"channel only": {"content": "confidential", "channel": "telegram"},
	} {
		tool := NewMessageTool()
		sent := false
		tool.SetSendCallback(func(string, string, string, SendOrigin) error { sent = true; return nil })
		tool.SetMainConnectorGuard(t12ConnectorGuard)
		res := tool.Execute(t12MainCtx(), args)
		require.Error(t, res.Err, "S-40 rows 2–3 (%s): the main is refused", name)
		require.True(t, errors.Is(res.Err, ErrMainConnectorReplyOnly),
			"S-40 rows 2–3 (%s): ErrMainConnectorReplyOnly, got %v", name, res.Err)
		require.Contains(t, res.ForLLM, "reply_to=<request id>",
			"S-40 rows 2–3 (%s): the amended text points at reply_to", name)
		require.False(t, sent, "S-40 rows 2–3 (%s): nothing was sent", name)
	}
}

func TestT12_S40_MainWebchatExplicitTarget_UnchangedPath(t *testing.T) {
	// S-40 row 5: a main naming webchat explicitly — not a connector, the
	// unchanged path runs. Ownership is wired (a real OwnerOf double):
	// webchat is registered synthetically and unbound, so it is shared and
	// unenforced (the ADR-065 webchat decision).
	tool := NewMessageTool()
	tool.SetChannelOwnership(fakeOwnership{owner: map[string]string{
		"telegram": "ws-t12/mia", // telegram IS bound here; webchat stays absent
	}})
	sent := false
	tool.SetSendCallback(func(channel, chatID, content string, o SendOrigin) error {
		sent = true
		require.Equal(t, "webchat", channel)
		require.Equal(t, "x", chatID)
		return nil
	})
	ctx := t12MainCtx() // telegram turn; the target is named explicitly
	res := tool.Execute(ctx, map[string]any{"content": "hello", "channel": "webchat", "chat_id": "x"})
	require.False(t, res.IsError, "S-40 row 5: webchat target stays on the unchanged path: %s", res.ForLLM)
	require.True(t, sent)
	require.Contains(t, res.ForLLM, "Message sent to webchat:x")
}

func TestT12_S40_NonMainOwnConversation_UnchangedSend(t *testing.T) {
	// S-40 row 6: a non-main session sending to its own conversation —
	// unchanged own-conversation send, "Message sent to telegram:chat-1".
	tool := NewMessageTool()
	sent := false
	tool.SetSendCallback(func(string, string, string, SendOrigin) error { sent = true; return nil })
	res := tool.Execute(t12NonMainCtx(), map[string]any{"content": "status"})
	require.False(t, res.IsError, "S-40 row 6: own-conversation send unchanged: %s", res.ForLLM)
	require.True(t, sent)
	require.Contains(t, res.ForLLM, "Message sent to telegram:chat-1")
}

func TestT12_S40_SteeredExplicitTarget_SteeredRefusal(t *testing.T) {
	// S-40 row 9: a steered (delegated) session naming another target —
	// the steered own-chat-only refusal, unchanged (T-4 step 1).
	tool := NewMessageTool()
	tool.SetChannelOwnership(fakeOwnership{owner: map[string]string{
		"discord": "ws-t12/other-agent", // would be ALLOWED by ownership alone
	}})
	tool.SetSteerAudienceResolver(&fakeSteerAudience{
		audience: map[string]steer.Audience{"child-1": steer.AudienceSteeringSession},
	})
	tool.SetSendCallback(func(string, string, string, SendOrigin) error { return nil })
	ctx := WithTranscriptSessionID(
		WithToolContext(WithAgentID(context.Background(), "worker"), "webchat", "child-1"), "child-1")
	res := tool.Execute(ctx, map[string]any{
		"content": "sneak", "channel": "discord", "chat_id": "222",
	})
	require.Error(t, res.Err, "S-40 row 9: the steered session is refused")
	require.True(t, errors.Is(res.Err, ErrSteeredSessionOwnChatOnly),
		"S-40 row 9: ErrSteeredSessionOwnChatOnly, got %v", res.Err)
}

func TestT12_S40_MainReplyTo_CapturedReplyUnchanged(t *testing.T) {
	// S-40 row 10: a main replying with reply_to=q1 — the captured reply
	// path, unchanged (addressed form runs before classification).
	tool, _, reply, sent := addrTool()
	tool.SetMainConnectorGuard(t12ConnectorGuard)
	res := tool.Execute(t12MainCtx(), map[string]any{"content": "here you go", "reply_to": "q1"})
	require.False(t, res.IsError, "S-40 row 10: a valid reply_to from a main still works: %s", res.ForLLM)
	require.Equal(t, 1, len(reply.calls), "S-40 row 10: the reply is captured once")
	require.False(t, *sent, "S-40 row 10: the ordinary send does not run")
}

// START rows reconciled to the committed API (same author, original names).
// The independent fixture/oracles are in message_start_policy_spec_test.go.

func TestT12_S40_MainWebchatToListedDiscordTarget_StartAccepted(t *testing.T) {
	// S-40 row4, plus D2 row9: a canonical prefix is removed before publication.
	for _, target := range []string{"222", "discord:222"} {
		t.Run(target, func(t *testing.T) {
			tool, policy, sent := t12WiredStart(t, "discord")
			ctx := t12StartCtx("main-1", "webchat", "w")
			res := tool.Execute(ctx, map[string]any{"content": "hello", "channel": "discord", "chat_id": target})
			t12RequireAccepted(t, tool, res, *sent, "discord", "222", "hello")
			require.Equal(t, []t12StartCall{{"discord", target, "ws-t12", "mia"}}, policy.calls)
		})
	}
}

func TestT12_S40_NonMainExplicitDifferentChat_StartCheck(t *testing.T) {
	for _, target := range []string{"chat-2", "222"} {
		t.Run(target, func(t *testing.T) {
			tool, policy, sent := t12WiredStart(t, "telegram")
			ctx := t12StartCtx("chat-session", "telegram", "chat-1")
			res := tool.Execute(ctx, map[string]any{"content": "status", "channel": "telegram", "chat_id": target})
			require.Equal(t, []t12StartCall{{"telegram", target, "ws-t12", "mia"}}, policy.calls, "S-40 row7: different chat must go through T-3")
			if target == "chat-2" {
				// T-2 requires numeric Telegram ids. The literal S-40 example is
				// therefore a refused start, not a successful ordinary own reply.
				t12RequireRefused(t, tool, res, *sent, `cannot start a message to "chat-2" on "telegram": it is not on this connection's list. Listed: 222. Nothing was sent.`)
			} else {
				t12RequireAccepted(t, tool, res, *sent, "telegram", "222", "status")
			}
		})
	}
}

func TestT12_S40_HeartbeatNoConversation_StartCheck(t *testing.T) {
	tool, policy, sent := t12WiredStart(t, "discord")
	ctx := t12StartCtx("heartbeat-1", "system", "")
	res := tool.Execute(ctx, map[string]any{"content": "heartbeat status", "channel": "discord", "chat_id": "222"})
	t12RequireAccepted(t, tool, res, *sent, "discord", "222", "heartbeat status")
	require.Equal(t, []t12StartCall{{"discord", "222", "ws-t12", "mia"}}, policy.calls, "S-40 row8: no connector conversation means step5")
}

func TestT12_S41_ToolLevelStartRefusalTexts(t *testing.T) {
	for _, tc := range t12RefusalRows() {
		t.Run(tc.name, func(t *testing.T) {
			tool, policy, sent := t12WiredStart(t, tc.platform)
			tc.setup(policy)
			ctx := t12StartCtx("main-1", "webchat", "w")
			res := tool.Execute(ctx, map[string]any{"content": "must not send", "channel": tc.channel, "chat_id": tc.chat})
			t12RequireRefused(t, tool, res, *sent, tc.want)
			require.Equal(t, []t12StartCall{{tc.channel, tc.chat, "ws-t12", "mia"}}, policy.calls, "S-41: the tool reaches the real T-3 policy exactly once")
		})
	}
}
