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

	// turnIdentity carries the turn_id/message_id of the MOST RECENT turn a
	// wsStreamer finalized for a given chatID, recorded by
	// wsStreamerFinalize.publishDone (websocket_streamer.go) immediately
	// before it decides whether to markStreamed. Send's fallback path
	// (below) consumes it so a frame built directly here — bypassing the
	// streamer's own Update/Finalize, which already stamp TurnId/MessageId
	// — still carries the SAME identity a live frame would have, instead of
	// a nil turn_id/message_id. Populated for every non-shadow Finalize,
	// including the cases markStreamed itself skips (a distinct terminal
	// notice after already-persisted narration; a turn that streamed no
	// tokens at all) — exactly the cases whose delivery actually falls
	// through to Send().
	turnIdentity map[string]turnIdentity
}

// turnIdentity is the (turn, durable message) identity pair a wsStreamer
// recorded for its chatID at Finalize — see webchatChannel.turnIdentity's
// doc comment.
type turnIdentity struct {
	turnID    string
	messageID string
}

// stampFrame sets *turnID/*messageID from id, mirroring exactly what a live
// wsStreamer.Update/Finalize call stamps on TokenFrame/DoneFrame
// (websocket_streamer.go) — leaves a pointer nil (the generated frame's
// "absent" wire representation) when the corresponding id is empty.
func (id turnIdentity) stampFrame(turnID, messageID **string) {
	if id.turnID != "" {
		t := id.turnID
		*turnID = &t
	}
	if id.messageID != "" {
		m := id.messageID
		*messageID = &m
	}
}

func newWebchatChannel(wsHandler *WSHandler) *webchatChannel {
	return &webchatChannel{
		wsHandler:    wsHandler,
		streamed:     make(map[string]bool),
		turnIdentity: make(map[string]turnIdentity),
	}
}

func (c *webchatChannel) markStreamed(chatID string) {
	c.mu.Lock()
	c.streamed[chatID] = true
	c.mu.Unlock()
}

// recordTurnIdentity stashes the turn/message identity of the turn a
// wsStreamer just finalized for chatID, for a subsequent Send() fallback
// delivery to consume. A no-op when both ids are empty (nothing useful to
// hand a later fallback frame).
func (c *webchatChannel) recordTurnIdentity(chatID, turnID, messageID string) {
	if turnID == "" && messageID == "" {
		return
	}
	c.mu.Lock()
	c.turnIdentity[chatID] = turnIdentity{turnID: turnID, messageID: messageID}
	c.mu.Unlock()
}

// consumeTurnIdentity returns and clears the most recently recorded turn
// identity for chatID, so a later unrelated turn never inherits a stale id.
func (c *webchatChannel) consumeTurnIdentity(chatID string) turnIdentity {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.turnIdentity[chatID]
	delete(c.turnIdentity, chatID)
	return id
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

	// This delivery bypasses the streamer's own Update/Finalize — the one
	// place that normally stamps TurnId/MessageId on a live frame — so pull
	// whatever identity the turn's streamer recorded for this chatID (see
	// turnIdentity's doc comment) and stamp it here instead. Empty when no
	// streamer ever ran for this chatID (e.g. a non-webchat-turn producer),
	// in which case the frame carries no identity, exactly as before.
	identity := c.consumeTurnIdentity(msg.ChatID)

	sid, origin := c.resolveOutbound(msg.ChatID, msg.SessionID)
	if sid == "" {
		// No session to number against: the only possible viewer is the
		// originating connection itself (if it is still open).
		if origin != nil {
			if msg.Content != "" {
				tokenFrame := generated.TokenFrame{
					Type:    string(generated.WsFrameTypeToken),
					Content: msg.Content,
				}
				identity.stampFrame(&tokenFrame.TurnId, &tokenFrame.MessageId)
				sendConnGenFrame(origin, string(generated.WsFrameTypeToken), tokenFrame)
			}
			doneFrame := generated.DoneFrame{
				Type: string(generated.WsFrameTypeDone),
			}
			identity.stampFrame(&doneFrame.TurnId, &doneFrame.MessageId)
			sendConnGenFrame(origin, string(generated.WsFrameTypeDone), doneFrame)
		}
		return nil
	}

	// #823 (BE-DESIGN.md §1.2, H17): a turn with no streamed text still gets
	// a numbered token + done through the session hub, delivered to every
	// tab bound to the session and journaled for a tab that attaches later.
	// ADR-082 D6/FR-012: zero bound connections is not a failure — the
	// content is durable in the transcript and in the journal.
	if msg.Content != "" {
		tokenFrame := generated.TokenFrame{
			Type:      string(generated.WsFrameTypeToken),
			Content:   msg.Content,
			SessionId: sid,
		}
		identity.stampFrame(&tokenFrame.TurnId, &tokenFrame.MessageId)
		c.wsHandler.hubPublishFrameMeta(sid, string(generated.WsFrameTypeToken), hubFrameMeta{
			kind: hubKindToken, content: msg.Content,
		}, tokenFrame)
	}
	doneFrame := generated.DoneFrame{
		Type:      string(generated.WsFrameTypeDone),
		SessionId: sid,
	}
	identity.stampFrame(&doneFrame.TurnId, &doneFrame.MessageId)
	c.wsHandler.hubPublishFrameMeta(sid, string(generated.WsFrameTypeDone), hubFrameMeta{kind: hubKindDone}, doneFrame)
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
