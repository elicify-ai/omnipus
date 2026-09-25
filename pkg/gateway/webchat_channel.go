// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
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

	sid, origin := c.resolveOutbound(msg.ChatID, msg.SessionID)
	if sid == "" {
		// No session to number against: the only possible viewer is the
		// originating connection itself (if it is still open).
		if origin != nil {
			if msg.Content != "" {
				sendConnGenFrame(origin, string(generated.WsFrameTypeToken), generated.TokenFrame{
					Type:    string(generated.WsFrameTypeToken),
					Content: msg.Content,
				})
			}
			sendConnGenFrame(origin, string(generated.WsFrameTypeDone), generated.DoneFrame{
				Type: string(generated.WsFrameTypeDone),
			})
		}
		return nil
	}

	// #823 (BE-DESIGN.md §1.2, H17): a turn with no streamed text still gets
	// a numbered token + done through the session hub, delivered to every
	// tab bound to the session and journaled for a tab that attaches later.
	// ADR-082 D6/FR-012: zero bound connections is not a failure — the
	// content is durable in the transcript and in the journal.
	if msg.Content != "" {
		c.wsHandler.hubPublishFrameMeta(sid, string(generated.WsFrameTypeToken), hubFrameMeta{
			kind: hubKindToken, content: msg.Content,
		}, generated.TokenFrame{
			Type:      string(generated.WsFrameTypeToken),
			Content:   msg.Content,
			SessionId: sid,
		})
	}
	c.wsHandler.hubPublishFrameMeta(sid, string(generated.WsFrameTypeDone), hubFrameMeta{kind: hubKindDone},
		generated.DoneFrame{
			Type:      string(generated.WsFrameTypeDone),
			SessionId: sid,
		})
	return nil
}

// resolveOutbound returns the session an outbound message belongs to (its
// own session id, else the one its chat id is bound to) and the chat's own
// connection, if it is still open.
func (c *webchatChannel) resolveOutbound(chatID, sessionID string) (string, *wsConn) {
	c.wsHandler.mu.Lock()
	defer c.wsHandler.mu.Unlock()
	sid := sessionID
	if sid == "" {
		sid = c.wsHandler.sessionIDs[chatID]
	}
	return sid, c.wsHandler.sessions[chatID]
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
// [ADR-082 review CR7] It reaches every connection bound to the message's
// session (a second tab, or the same tab reconnected under a new chat id) —
// #823: through one numbered publish in the session hub — and, matching
// Send's ADR-082 D6 semantics, returns nil rather than ErrSendFailed when
// nobody is watching: the caller (the agent loop's tool-media delivery)
// treats an error as fatal and would otherwise discard toolResult.Media, so
// the attachment would be lost from the transcript too.
func (c *webchatChannel) SendMedia(_ context.Context, msg bus.OutboundMediaMessage) error {
	if len(msg.Parts) == 0 {
		slog.Warn("webchat: SendMedia called with empty parts — skipping", "chat_id", msg.ChatID)
		return nil
	}

	sid, origin := c.resolveOutbound(msg.ChatID, msg.SessionID)

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

	slog.Debug("webchat: sending media frame", "chat_id", msg.ChatID, "session_id", sid, "parts", len(parts))

	frame := generated.MediaFrame{
		Type:      string(generated.WsFrameTypeMedia),
		SessionId: sid,
		Parts:     parts,
	}
	if sid == "" {
		// No session to number against: only the originating connection.
		if origin != nil {
			sendConnGenFrame(origin, string(generated.WsFrameTypeMedia), frame)
		}
		return nil
	}
	// #823: published once through the session hub — every bound tab gets
	// it; a tab that attaches later gets it from the journal (or, until the
	// turn's done, from the active-turn projection). ADR-082 D6/CR7: no bound
	// connection is not a failure — the media refs stay durable.
	c.wsHandler.hubPublishFrameMeta(sid, string(generated.WsFrameTypeMedia), hubFrameMeta{
		kind: hubKindItem, key: "media:" + parts[0].Url + ":" + strconv.Itoa(len(parts)),
	}, frame)
	return nil
}
