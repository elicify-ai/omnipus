package tools

// message_classification_test.go — T-12, tool classification (channels-spec-r8
// T-4 / T-12; dataset D3 = scenario table S-40; FR-040, FR-041, FR-043;
// S-41's tool-level refusal texts).
//
// Two halves, stated plainly:
//
//  1. PRESERVED rows — the classification rows that must hold unchanged
//     before and after the channels work (S-40 rows 1, 2, 3, 5, 6, 9, 10).
//     These are positive controls: they PASS today, and their job in RED is
//     to pin the behaviour the start work must not disturb. Oracles: the
//     S-40 table plus the existing texts (ErrMainConnectorReplyOnly's
//     amended text; "Message sent to …"; ErrSteeredSessionOwnChatOnly).
//
//  2. START rows — the rows whose expected result is a start (S-40 rows 4,
//     7, 8; S-41's tool-level texts; FR-043's Operation=start publish). The
//     seam they need does not exist yet in WP-0: there is no start-policy
//     injection on MessageTool (only mainConnectorGuard), and SendOrigin has
//     no Operation field (the FR-043 rename is production work). Per the RED
//     rules they are t.Fatal BLOCKED — loud, never skipped — naming exactly
//     what the backend lead must provide. The backend lead implements the
//     seam together with the classification, and these become the wired
//     start-policy fixture tests the spec's regression table calls for.
//
// Fixture: the existing real-component patterns of this package —
// NewMessageTool with a real send callback, the context accessors, the
// fakeSteerAudience double for the steered row. No production code is
// invented here.

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

// --- the START rows: BLOCKED on the missing WP-0 seam -----------------------
//
// What the backend lead must provide (all named in the RED report):
//   1. a start-policy injection on MessageTool (the T-3 check wired from the
//      live policy source + availability — pkg/channels.StartAllowed), and
//   2. the FR-043 rename SendOrigin.OwnershipChecked → Operation with
//      bus.OperationChecked / bus.OperationStart values reaching the send
//      callback and the published bus message (bare chat id on starts).

func TestT12_S40_MainWebchatToListedDiscordTarget_StartAccepted(t *testing.T) {
	// S-40 row 4: a main on webchat naming a listed, running discord target
	// explicitly — a start: T-3 passes, the publish carries Operation=start
	// and the bare chat id. No seam exists in WP-0 to wire the start policy.
	t.Fatalf("BLOCKED: no start-policy seam on MessageTool and no Operation field on SendOrigin — required by S-40 row 4 / T-4 step 5 / FR-043; backend lead provides the wired start-policy fixture named in the RED report")
}

func TestT12_S40_NonMainExplicitDifferentChat_StartCheck(t *testing.T) {
	// S-40 row 7: a non-main session naming a DIFFERENT chat on the same
	// platform — step 5, a start through T-3.
	t.Fatalf("BLOCKED: no start-policy seam on MessageTool and no Operation field on SendOrigin — required by S-40 row 7 / T-4 step 5 / FR-043; backend lead provides the wired start-policy fixture named in the RED report")
}

func TestT12_S40_HeartbeatNoConversation_StartCheck(t *testing.T) {
	// S-40 row 8: a heartbeat (no connector conversation of its own) sending
	// to discord 222 — step 5, a start; every connector send from a
	// heartbeat is step 5 (T-4's explicit note).
	t.Fatalf("BLOCKED: no start-policy seam on MessageTool and no Operation field on SendOrigin — required by S-40 row 8 / T-4 heartbeat note / FR-043; backend lead provides the wired start-policy fixture named in the RED report")
}

func TestT12_S41_ToolLevelStartRefusalTexts(t *testing.T) {
	// S-41: the seven setups return EXACTLY the T-3 text for their code and
	// publish nothing. D2 pins the pure function (pkg/channels
	// start_allowed_test.go); this is the tool-level surface with the live
	// policy source and Manager.Availability wired in.
	t.Fatalf("BLOCKED: no start-policy seam on MessageTool — required by S-41 / FR-041; backend lead provides the wired start-policy fixture named in the RED report")
}
