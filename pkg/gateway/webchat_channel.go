//go:build !cgo

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
	"github.com/elicify-ai/omnipus/pkg/channels"
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
	conns := c.collectSessionConnsLocked(msg.ChatID, sid)
	c.wsHandler.mu.Unlock()

	if len(conns) == 0 {
		// Wrap as channels.ErrSendFailed so the Manager's sendWithRetry loop
		// classifies the failure as PERMANENT and stops immediately. Under
		// concurrent load (e.g. 2000 in-process WS clients all hanging up
		// near simultaneously) the default "unknown error" classification
		// triggered exponential-backoff retries — each blocking the worker
		// for up to maxBackoff seconds, multiplied by 3 attempts per
		// dead chat. There is no recovery path when a chat's only WS
		// connection has closed; retrying just wastes the worker's time
		// and starves live chats sharing the same goroutine.
		return fmt.Errorf("webchat: no active connection for chat %s: %w",
			msg.ChatID, channels.ErrSendFailed)
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

// collectSessionConnsLocked returns every wsConn that should receive a
// session-scoped outbound frame: the originating chatID plus any other
// connections (from different browser tabs) attached to the same session.
// Caller must hold c.wsHandler.mu.
func (c *webchatChannel) collectSessionConnsLocked(originChatID, sessionID string) []*wsConn {
	out := make([]*wsConn, 0, 2)
	seen := make(map[*wsConn]struct{}, 2)
	if conn, ok := c.wsHandler.sessions[originChatID]; ok {
		out = append(out, conn)
		seen[conn] = struct{}{}
	}
	if sessionID == "" {
		return out
	}
	for chatID, sid := range c.wsHandler.sessionIDs {
		if sid != sessionID || chatID == originChatID {
			continue
		}
		conn, ok := c.wsHandler.sessions[chatID]
		if !ok {
			continue
		}
		if _, dup := seen[conn]; dup {
			continue
		}
		out = append(out, conn)
		seen[conn] = struct{}{}
	}
	return out
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
func (c *webchatChannel) SendMedia(_ context.Context, msg bus.OutboundMediaMessage) error {
	if len(msg.Parts) == 0 {
		slog.Warn("webchat: SendMedia called with empty parts — skipping", "chat_id", msg.ChatID)
		return nil
	}

	c.wsHandler.mu.Lock()
	conn, ok := c.wsHandler.sessions[msg.ChatID]
	c.wsHandler.mu.Unlock()

	if !ok {
		// Same permanent-failure classification as Send — see comment above.
		return fmt.Errorf("webchat: no active connection for chat %s: %w",
			msg.ChatID, channels.ErrSendFailed)
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

	slog.Debug("webchat: sending media frame", "chat_id", msg.ChatID, "parts", len(parts))

	// Marshal and route through sendRawFrameBytes to get replay-divert and backpressure logic.
	raw, err := json.Marshal(generated.MediaFrame{
		Type:      string(generated.WsFrameTypeMedia),
		SessionId: msg.SessionID,
		Parts:     parts,
	})
	if err != nil {
		return fmt.Errorf("webchat: marshal media frame: %w", err)
	}
	sendRawFrameBytes(conn, string(generated.WsFrameTypeMedia), raw)
	return nil
}
