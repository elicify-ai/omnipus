// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/media"
)

// webchatChannel implements channels.Channel so the channel Manager can route
// outbound messages back to WebSocket clients. Without this, the Manager's
// dispatch loop logs "Unknown channel for outbound message" and drops responses.
//
// When streaming is active (via WSHandler's GetStreamer → wsStreamer), tokens
// are delivered incrementally and Finalize sends the "done" frame. In that case,
// Send() is a no-op to avoid duplicating the response.
type webchatChannel struct {
	wsHandler *WSHandler

	// streamed tracks chatIDs where wsStreamer.Finalize has already delivered
	// the response. Send() skips these to avoid duplication.
	mu       sync.Mutex
	streamed map[string]bool
}

func newWebchatChannel(wsHandler *WSHandler) *webchatChannel {
	return &webchatChannel{
		wsHandler: wsHandler,
		streamed:  make(map[string]bool),
	}
}

func (c *webchatChannel) markStreamed(chatID string) {
	c.mu.Lock()
	c.streamed[chatID] = true
	c.mu.Unlock()
}

func (c *webchatChannel) Name() string { return "webchat" }

func (c *webchatChannel) Start(_ context.Context) error         { return nil }
func (c *webchatChannel) Stop(_ context.Context) error          { return nil }
func (c *webchatChannel) IsRunning() bool                       { return true }
func (c *webchatChannel) IsAllowed(_ string) bool               { return true }
func (c *webchatChannel) IsAllowedSender(_ bus.SenderInfo) bool { return true }
func (c *webchatChannel) ReasoningChannelID() string            { return "" }

// Send delivers an outbound message to the WebSocket client.
// If the response was already delivered via streaming (wsStreamer), this is a no-op.
func (c *webchatChannel) Send(_ context.Context, msg bus.OutboundMessage) error {
	c.mu.Lock()
	alreadyStreamed := c.streamed[msg.ChatID]
	delete(c.streamed, msg.ChatID) // consume the flag
	c.mu.Unlock()

	if alreadyStreamed {
		slog.Debug("webchat: skipping Send — response already delivered via streaming", "chat_id", msg.ChatID)
		return nil
	}

	// Resolve the originating chat's session_id and find every connection
	// currently bound to that session. Cross-browser session attach (#133)
	// requires that a second tab observing the same session receives the
	// final token/done frames — not just the chat_id that triggered the turn.
	c.wsHandler.mu.Lock()
	sid := msg.SessionID
	if sid == "" {
		sid = c.wsHandler.sessionIDs[msg.ChatID]
	}
	// ADR-082 review CR7: resolveSessionConnsLocked (WSHandler's own session
	// resolver, shared with wsStreamer.Update/Finalize) now also accepts an
	// origin-chatID fallback, unifying what used to be two independent
	// resolvers with the same job — see that function's doc comment.
	conns := c.wsHandler.resolveSessionConnsLocked(msg.ChatID, sid)
	c.wsHandler.mu.Unlock()

	if len(conns) == 0 {
		// ADR-082 D6/FR-012: zero bound connections is no longer a failure.
		// A turn is UI-independent (ADR-082 P1) — the content is already
		// durable in the transcript (written by the streaming path's
		// Finalize, or by the caller before this Send for a non-streamed
		// response) and will replay on the next attach_session, whenever
		// that happens. Returning ErrSendFailed here used to make the
		// Manager's sendWithRetry loop classify a merely-absent viewer as a
		// PERMANENT failure and (for keeper-originated turns, E5) log
		// "Send failed"/trigger a drop notice every 60-90s until the goal
		// cleared — pure noise, since there was never anything to retry.
		slog.Debug("webchat: no bound connection, transcript is durable",
			"chat_id", msg.ChatID, "session_id", sid)
		return nil
	}

	for _, conn := range conns {
		if msg.Content != "" {
			sendConnGenFrame(conn, string(generated.WsFrameTypeToken), generated.TokenFrame{
				Type:      string(generated.WsFrameTypeToken),
				Content:   msg.Content,
				SessionId: sid,
			})
		}
		sendConnGenFrame(conn, string(generated.WsFrameTypeDone), generated.DoneFrame{
			Type:      string(generated.WsFrameTypeDone),
			SessionId: sid,
		})
	}
	return nil
}

func mediaRefURL(ref string) string {
	if strings.HasPrefix(ref, media.WorkspaceRefPrefix) {
		return "/api/v1/media/workspace/" + strings.TrimPrefix(ref, media.WorkspaceRefPrefix)
	}
	return "/api/v1/media/" + strings.TrimPrefix(ref, "media://")
}

// SendMedia delivers media attachments to the WebSocket client.
// Implements channels.MediaSender so the channel manager can route
// OutboundMediaMessage to the webchat channel.
//
// [ADR-082 review CR7] Previously resolved only msg.ChatID's single
// connection (c.wsHandler.sessions[msg.ChatID]) and returned
// channels.ErrSendFailed whenever that one connection was not live —
// ignoring msg.SessionID entirely and, worse, ignoring EVERY other
// connection bound to the same session (a second browser tab, or the SAME
// tab reconnected under a NEW chatID after ADR-082 D2). On a reconnect the
// attachment was lost both live (no connection found under the STALE
// chatID) AND on replay: the caller (pkg/agent/loop.go's tool-media-delivery
// block) treats a SendMedia error as fatal and REPLACES the tool result with
// a plain error message, discarding toolResult.Media entirely — so nothing
// about the attachment ever reaches the transcript either.
//
// Now resolves via the SAME session-bound connection set Send uses
// (resolveSessionConnsLocked, with msg.ChatID as the origin-chatID
// fallback), delivers to every one of them, and — matching Send's ADR-082 D6
// "zero bound connections is not a failure" semantics exactly — returns nil
// rather than ErrSendFailed when nobody is currently watching: the media
// itself is durable (the caller's toolResult.Media references survive
// untouched when this returns nil), so it will show up correctly on the
// next attach_session replay regardless of whether anyone was live to see it
// arrive.
func (c *webchatChannel) SendMedia(_ context.Context, msg bus.OutboundMediaMessage) error {
	if len(msg.Parts) == 0 {
		slog.Warn("webchat: SendMedia called with empty parts — skipping", "chat_id", msg.ChatID)
		return nil
	}

	c.wsHandler.mu.Lock()
	sid := msg.SessionID
	if sid == "" {
		sid = c.wsHandler.sessionIDs[msg.ChatID]
	}
	conns := c.wsHandler.resolveSessionConnsLocked(msg.ChatID, sid)
	c.wsHandler.mu.Unlock()

	if len(conns) == 0 {
		// ADR-082 D6/FR-012, extended to media by CR7: no bound connection is
		// not a failure — the caller still has msg's media refs and will
		// persist them to the transcript; a reconnect replays them normally.
		slog.Debug("webchat: SendMedia — no bound connection, media stays durable",
			"chat_id", msg.ChatID, "session_id", sid)
		return nil
	}

	parts := make([]generated.MediaPart, 0, len(msg.Parts))
	for _, p := range msg.Parts {
		if !strings.HasPrefix(p.Ref, "media://") {
			slog.Warn("webchat: media part has unexpected ref scheme — skipping",
				"chat_id", msg.ChatID, "ref", p.Ref)
			continue
		}
		part := generated.MediaPart{
			Type:        p.Type,
			Url:         mediaRefURL(p.Ref),
			Filename:    p.Filename,
			ContentType: p.ContentType,
		}
		if p.Caption != "" {
			caption := p.Caption
			part.Caption = &caption
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return fmt.Errorf(
			"webchat: all %d media parts had invalid refs — nothing sent for chat %s",
			len(msg.Parts),
			msg.ChatID,
		)
	}

	slog.Debug("webchat: sending media frame", "chat_id", msg.ChatID, "session_id", sid, "parts", len(parts), "conns", len(conns))

	// Marshal once and route through sendRawFrameBytes (replay-divert +
	// backpressure logic) to every connection bound to this session — a
	// second browser tab (or the SAME tab reconnected under a new chatID)
	// must see the attachment too, matching Send's fan-out above.
	raw, err := json.Marshal(generated.MediaFrame{
		Type:      string(generated.WsFrameTypeMedia),
		SessionId: sid,
		Parts:     parts,
	})
	if err != nil {
		return fmt.Errorf("webchat: marshal media frame: %w", err)
	}
	for _, conn := range conns {
		sendRawFrameBytes(conn, string(generated.WsFrameTypeMedia), raw)
	}
	return nil
}
