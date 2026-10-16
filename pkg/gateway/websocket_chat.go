// websocket_chat.go: Handle an inbound chat message frame (model-name stamping, transcript attachments).

package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/validation"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/google/uuid"
)

// parseSetupKickoffMetadata decides whether a MessageFrame's
// metadata.workspace_setup_kickoff (contracts/asyncapi.yaml) signals a
// client workspace-setup kickoff, based on KEY PRESENCE rather than a loose
// type assertion. Every client kickoff is refused at the frame seam
// (websocket.go::handleMessageFrame): the server is the only producer of the
// setup offer (omnipus-system-agent FR-047, K9; workspace_setup_offer.go).
//
// A naive `metadata["workspace_setup_kickoff"].(bool)` type assertion
// silently reads ANY non-bool value — the string "true", a JSON number, an
// object — as ok=false, which would demote a malformed marker into an
// ordinary chat message instead of refusing it.
//
// Returns (setupKickoff=false, malformed=false) when the key is absent or is
// the JSON boolean false — an ordinary message, handled normally (the
// contract's "absent/false for ordinary messages"). Returns
// (setupKickoff=true, malformed=false) when the value is the JSON boolean
// true. Returns (setupKickoff=false, malformed=true) for any non-boolean
// value, signaling the caller to reject the frame outright.
func parseSetupKickoffMetadata(metadata map[string]any) (setupKickoff bool, malformed bool) {
	raw, present := metadata["workspace_setup_kickoff"]
	if !present {
		return false, false
	}
	b, isBool := raw.(bool)
	if !isBool {
		return false, true
	}
	return b, false
}

// wsHandlerHandleChatMessage carries the shared state of handleChatMessage across its stages.
type wsHandlerHandleChatMessage struct {
	h               *WSHandler
	ctx             context.Context
	chatID          string
	frameSessionID  string
	content         string
	agentID         string
	mediaRefs       []string
	modelName       string
	workspaceID     string
	clientMessageID string
	// autoApprove is MessageFrame.auto_approve (ADR-092, contracts/asyncapi.yaml):
	// the per-chat Auto-approve choice for the chat THIS message mints, carried
	// on the minting message itself so it is recorded before the turn is
	// dispatched to the agent loop — see recordSessionAndTranscript's mint
	// branch. nil means "no choice sent" (frame omitted it, or sent explicit
	// null — both collapse to the same Go zero value for an optional pointer
	// field, and both mean the same thing here: leave the chat's per-chat
	// modifier unset). Meaningless — and never read — once frameSessionID is
	// non-empty: an existing session's mode changes exclusively via
	// session_mode_update.
	autoApprove *bool
	// recipient, when non-nil, makes this message a request to that peer pair
	// (MessageFrame.recipient, session-core C-ADDRESS) rather than a turn for
	// the owning chat's agent.
	recipient *addressing.Pair
	// recipientAskApproved: the receiver's Ask was approved at the preflight.
	recipientAskApproved bool
	wc                   *wsConn
	targetAgentID        string
	sessionID            string
	store                *session.UnifiedStore
	acceptedMedia        []string
	msg                  bus.InboundMessage
	transcriptPersisted  bool
	// transcriptEntryID is the id of the user entry persistUserMessage wrote
	// successfully; empty when nothing was written.
	transcriptEntryID string
	admitted          bool
	// firstMessage is an ordinary session-less send using save-before-ack.
	// ID-bearing oversized sends are rejected before minting; legacy no-ID
	// oversized input keeps ADR-066's separate agent-side refusal path.
	firstMessage bool
	// firstRequestDigest captures original requested fields before intake mutates
	// them, so a retry compares the request, not newly resolved server defaults.
	firstRequestDigest firstClientMessageDigest
}

// pendingMessageStatus is one persisted user message still waiting for its
// turn to open a streamer (the received→working tick). #823 (BE-DESIGN.md
// §1.2, review finding 12): it no longer holds the sending *wsConn — the
// "working" tick is published through the session hub to every bound tab,
// so nothing here keeps a dead connection alive in memory.
type pendingMessageStatus struct {
	clientMessageID string
}

// handleChatMessage mints a new session when frame.SessionID is empty, records
// every user message to the transcript, and publishes the message to the bus.
//
// modelName, when non-empty and non-whitespace, is forwarded to the agent loop
// as msg.Metadata["model_name"] so the per-turn switch (Phase 1, FR-010) routes
// THIS message to the chosen model instead of the agent's default. Whitespace-only
// or empty values are treated as absent — the agent falls back to its configured model.
func (h *WSHandler) handleChatMessage(
	ctx context.Context,
	chatID string,
	frameSessionID string,
	content string,
	agentID string,
	mediaRefs []string,
	modelName string,
	workspaceID string,
	wc *wsConn,
) {
	h.handleChatMessageWithClientID(ctx, chatID, frameSessionID, content, agentID, mediaRefs, modelName, workspaceID, "", nil, wc)
}

// handleChatMessageWithClientID is the acknowledgement-aware message intake.
// The compatibility wrapper above keeps older callers and clients unchanged;
// a client_message_id opts a newer client into received/working/failed frames.
// autoApprove is MessageFrame.auto_approve (ADR-092) — see
// wsHandlerHandleChatMessage.autoApprove's doc comment; nil for every caller
// that does not carry the field (the compatibility wrapper above, and every
// non-message-frame path).
func (h *WSHandler) handleChatMessageWithClientID(
	ctx context.Context,
	chatID string,
	frameSessionID string,
	content string,
	agentID string,
	mediaRefs []string,
	modelName string,
	workspaceID string,
	clientMessageID string,
	autoApprove *bool,
	wc *wsConn,
) {
	h.handleChatMessageToRecipient(ctx, chatID, frameSessionID, content, agentID, mediaRefs, modelName, workspaceID, clientMessageID, autoApprove, nil, wc)
}

// handleChatMessageToRecipient is the intake behind
// handleChatMessageWithClientID. recipient is MessageFrame.recipient: non-nil
// makes the message a request to that peer pair (session-core C-ADDRESS)
// instead of a turn for the owning chat's agent; nil is the ordinary path.
func (h *WSHandler) handleChatMessageToRecipient(
	ctx context.Context,
	chatID string,
	frameSessionID string,
	content string,
	agentID string,
	mediaRefs []string,
	modelName string,
	workspaceID string,
	clientMessageID string,
	autoApprove *bool,
	recipient *addressing.Pair,
	wc *wsConn,
) {
	// Schema validation is optional, but the client-id bound is not. Reject
	// before resolution, persistence, or the deferred status/cache cleanup.
	if utf8.RuneCountInString(clientMessageID) > maxClientMessageIDChars {
		const tooLong = "client_message_id must be at most 128 characters"
		if recipient != nil {
			// An @-recipient send names its chat; the invalid id cannot be echoed
			// or ticked, so only the chat is tagged.
			h.sendRecipientRefusal(wc, frameSessionID, "", tooLong)
			return
		}
		sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: tooLong,
		})
		return
	}
	recipientAskApproved := false
	if recipient != nil {
		// A refused @recipient admits nothing — not even the user message — so
		// the whole admission rule runs before any persistence.
		msg, approved := h.refuseRecipient(ctx, frameSessionID, *recipient)
		recipientAskApproved = approved
		if msg != "" {
			h.sendRecipientRefusal(wc, frameSessionID, clientMessageID, msg)
			return
		}
	}
	hcm := &wsHandlerHandleChatMessage{h: h, ctx: ctx, chatID: chatID, frameSessionID: frameSessionID, content: content, agentID: agentID, mediaRefs: mediaRefs, modelName: modelName, workspaceID: workspaceID, clientMessageID: clientMessageID, autoApprove: autoApprove, recipient: recipient, recipientAskApproved: recipientAskApproved, wc: wc}
	hcm.firstMessage = frameSessionID == "" &&
		(clientMessageID != "" || agent.UserMessageChars(content) <= h.agentLoop.UserMessageBound())
	defer func() {
		if !hcm.admitted && !hcm.firstMessage {
			hcm.h.forgetAcceptedMessage(hcm.sessionID, hcm.clientMessageID)
			hcm.sendMessageStatus("failed")
		}
	}()

	if hcm.recipient != nil {
		// The request goes to the recipient's main; the owner's own turn is not
		// started. The user's message is saved in the owning chat INSIDE the
		// admission, after the final receiver authorization (U8 r4 F12).
		hcm.admitRecipientRequest()
		return
	}

	if hcm.prepareMessage() {
		return
	}
	if hcm.transcriptPersisted {
		hcm.sendMessageStatus("received")
	}

	hcm.buildInboundMessage()
	hcm.queueWorkingStatus()
	pubCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := hcm.h.msgBus.PublishInbound(pubCtx, hcm.msg); err != nil {
		hcm.removeQueuedWorkingStatus()
		logsafeWarn("ws: failed to publish message", "error", err)
		if hcm.firstMessage && hcm.transcriptPersisted {
			// Delivery is confirmed. Keep both retry indexes and never relabel
			// this durable first user entry as failed or admit it again on Retry.
			hcm.sendFirstMessageError("answer_not_started")
			return
		}
		sidCopy := hcm.sessionID
		sendConnGenFrame(hcm.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:      string(generated.WsFrameTypeError),
			Message:   fmt.Sprintf("failed to deliver message: %v", err),
			SessionId: &sidCopy,
		})
		return
	}
	hcm.admitted = true
	hcm.markWorkingIfTurnAlreadyActive()
}

// prepareMessage validates and durably records intake. True means rejected
// or answered as a retry. Its first-send mutex protects the cache miss through
// append/cache population, and is released before the caller's bus admission.
func (hcm *wsHandlerHandleChatMessage) prepareMessage() bool {
	if hcm.frameSessionID == "" && hcm.clientMessageID != "" {
		hcm.firstRequestDigest = hcm.digestFirstMessageRequest()
		// One intake mutex, never a principal-keyed claim/lock store. h.mu is
		// free during disk I/O; a slow admission cannot block other new chats.
		hcm.h.firstMessageMu.Lock()
		defer hcm.h.firstMessageMu.Unlock()
		// Saved retries retain the original agent/session if defaults changed.
		if hcm.answerRetriedMessage() {
			hcm.admitted = true
			return true
		}
		// A saved retry stays recoverable even if the configured bound changed.
		// Reject only a new first send, before any session, append, or admission.
		if agent.UserMessageChars(hcm.content) > hcm.h.agentLoop.UserMessageBound() {
			hcm.sendFirstMessageError("not_saved")
			return true
		}
	}

	if hcm.resolveTargetAgent() || hcm.validateFrameIDs() || hcm.resolveSessionStore() {
		return true
	}
	// Existing-session retries retain #823's echo/current-status behavior.
	if hcm.frameSessionID != "" && hcm.answerRetriedMessage() {
		hcm.admitted = true
		return true
	}
	hcm.collectAcceptedMedia()
	return hcm.recordSessionAndTranscript()
}

func (hcm *wsHandlerHandleChatMessage) queueWorkingStatus() {
	if hcm.clientMessageID == "" || hcm.sessionID == "" {
		return
	}
	hcm.h.mu.Lock()
	if hcm.h.pendingMessageStatuses == nil {
		hcm.h.pendingMessageStatuses = make(map[string][]pendingMessageStatus)
	}
	hcm.h.pendingMessageStatuses[hcm.sessionID] = append(
		hcm.h.pendingMessageStatuses[hcm.sessionID],
		pendingMessageStatus{clientMessageID: hcm.clientMessageID},
	)
	hcm.h.mu.Unlock()
}

func (hcm *wsHandlerHandleChatMessage) removeQueuedWorkingStatus() bool {
	hcm.h.mu.Lock()
	defer hcm.h.mu.Unlock()
	return hcm.removeQueuedWorkingStatusLocked()
}

// removeQueuedWorkingStatusLocked is removeQueuedWorkingStatus with h.mu
// already held.
func (hcm *wsHandlerHandleChatMessage) removeQueuedWorkingStatusLocked() bool {
	queue := hcm.h.pendingMessageStatuses[hcm.sessionID]
	for index, pending := range queue {
		if pending.clientMessageID != hcm.clientMessageID {
			continue
		}
		queue = append(queue[:index], queue[index+1:]...)
		if len(queue) == 0 {
			delete(hcm.h.pendingMessageStatuses, hcm.sessionID)
		} else {
			hcm.h.pendingMessageStatuses[hcm.sessionID] = queue
		}
		return true
	}
	return false
}

func (hcm *wsHandlerHandleChatMessage) markWorkingIfTurnAlreadyActive() {
	// A message steered into a turn that is already running gets no
	// GetStreamer call of its own for its "working" tick, so mark it here.
	if hcm.h.agentLoop == nil {
		return
	}
	if _, _, _, active := hcm.h.agentLoop.ActiveForegroundTurnInfo(hcm.sessionID); !active {
		return
	}
	data, err := json.Marshal(generated.MessageStatusFrame{
		Type:            string(generated.WsFrameTypeMessageStatus),
		SessionId:       hcm.sessionID,
		ClientMessageId: hcm.clientMessageID,
		State:           "working",
	})
	if err != nil {
		logsafeError("ws: marshal message_status failed", "session_id", hcm.sessionID, "error", err)
		return
	}
	// Take the pending entry AND publish the tick under h.mu — the same lock
	// GetStreamer takes the entry under — so the tick is always numbered
	// before the turn's first token: either GetStreamer already sent it, or
	// GetStreamer waits for this publish. Removing here and publishing after
	// the unlock let a token overtake the tick (#823 fixture recorder found it).
	h := hcm.h
	h.mu.Lock()
	defer h.mu.Unlock()
	if !hcm.removeQueuedWorkingStatusLocked() {
		return
	}
	h.markAcceptedMessageWorkingLocked(hcm.sessionID, hcm.clientMessageID)
	if h.hubs != nil && hcm.sessionID != "" {
		h.hubs.getOrCreate(hcm.sessionID).publishBytes(data)
	}
	if hcm.wc != nil && (hcm.wc.boundHub == nil || hcm.wc.boundHub.id != hcm.sessionID) {
		sendRawFrameBytes(hcm.wc, string(generated.WsFrameTypeMessageStatus), data)
	}
}

func (h *WSHandler) takePendingMessageStatus(sessionID string) (pendingMessageStatus, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	queue := h.pendingMessageStatuses[sessionID]
	if len(queue) == 0 {
		return pendingMessageStatus{}, false
	}
	pending := queue[0]
	if len(queue) == 1 {
		delete(h.pendingMessageStatuses, sessionID)
	} else {
		h.pendingMessageStatuses[sessionID] = queue[1:]
	}
	return pending, true
}

// takeAllPendingMessageStatuses drains and returns every remaining queued
// entry for sessionID, in FIFO order, clearing the map entry. Used at turn
// end (see wsStreamerFinalize.sendDone) so a queued status entry can never
// outlive the turn it was queued for.
func (h *WSHandler) takeAllPendingMessageStatuses(sessionID string) []pendingMessageStatus {
	h.mu.Lock()
	defer h.mu.Unlock()
	queue := h.pendingMessageStatuses[sessionID]
	if len(queue) == 0 {
		return nil
	}
	delete(h.pendingMessageStatuses, sessionID)
	return queue
}

// flushPendingMessageStatusesAsWorking is called at turn end (a `done` frame
// is about to be sent). Review finding 12: "treat a done as implying
// working" plus "clear or expire entries at turn end" — a queued status
// entry that never got consumed by a mid-turn GetStreamer call (e.g. a
// round that never opened a streamer, or a non-streaming reply) must not
// survive past the turn it belongs to: left in place, it would either keep
// a dead connection reference alive indefinitely, or get popped by a LATER,
// UNRELATED turn on the same session and mislabel the wrong message as
// "working". Every remaining entry for sessionID is drained and sent
// "working" (fanned out to all bound connections, not just the original
// sender — see sendPendingMessageWorking) before the turn's done frame goes
// out, so the tick always reaches a terminal, non-stuck state.
func (h *WSHandler) flushPendingMessageStatusesAsWorking(sessionID string) {
	for _, pending := range h.takeAllPendingMessageStatuses(sessionID) {
		sendPendingMessageWorking(h, sessionID, pending)
	}
}

// sendPendingMessageWorking publishes the "working" status for pending
// through sessionID's hub (#823 BE-DESIGN.md §1.2): every tab bound to the
// session receives it with a sequence number — the original sender, a
// reconnected tab under a new chat id, and a second tab alike (review
// finding 12). h is nil only for a bare test fixture, which has no hub.
func sendPendingMessageWorking(h *WSHandler, sessionID string, pending pendingMessageStatus) {
	if h == nil {
		return
	}
	h.markAcceptedMessageWorking(sessionID, pending.clientMessageID)
	h.hubPublishFrame(sessionID, string(generated.WsFrameTypeMessageStatus), generated.MessageStatusFrame{
		Type:            string(generated.WsFrameTypeMessageStatus),
		SessionId:       sessionID,
		ClientMessageId: pending.clientMessageID,
		State:           "working",
	}, nil)
}

// sendMessageStatus publishes a received/working/failed tick for this
// message through the session hub, so every tab bound to the session sees
// it (#823 BE-DESIGN.md §1.2). The sending connection additionally gets an
// unsequenced copy when it is not bound to the session (e.g. a "failed"
// tick for a message rejected before any bind) — it is the one tab whose
// pending bubble is waiting on this exact tick.
func (hcm *wsHandlerHandleChatMessage) sendMessageStatus(state string) {
	if hcm.clientMessageID == "" || hcm.sessionID == "" {
		return
	}
	hcm.h.hubPublishFrame(hcm.sessionID, string(generated.WsFrameTypeMessageStatus), generated.MessageStatusFrame{
		Type:            string(generated.WsFrameTypeMessageStatus),
		SessionId:       hcm.sessionID,
		ClientMessageId: hcm.clientMessageID,
		State:           state,
	}, hcm.wc)
}

// publishUserMessage echoes a just-persisted user message to every tab
// bound to the session as a numbered user_message frame (#823 founder
// decision Q1, BE-DESIGN.md §4.7). The sender's SPA moves its pending
// bubble (matched by client_message_id) into history at this frame's
// position; every other tab inserts it there. Published strictly AFTER the
// transcript append succeeded, so a snapshot read taken after a later
// attach can never miss a message whose frame it already consumed.
func (hcm *wsHandlerHandleChatMessage) publishUserMessage(entry session.TranscriptEntry) {
	frame := generated.UserMessageFrame{
		Type:      string(generated.WsFrameTypeUserMessage),
		SessionId: hcm.sessionID,
		Id:        entry.ID,
		Content:   entry.Content,
		Timestamp: entry.Timestamp.UTC().Format(time.RFC3339Nano),
	}
	if entry.AgentID != "" {
		aid := entry.AgentID
		frame.AgentId = &aid
	}
	if entry.ClientMessageID != "" {
		cid := entry.ClientMessageID
		frame.ClientMessageId = &cid
	}
	for _, a := range entry.Attachments {
		frame.Attachments = append(frame.Attachments, struct {
			MimeType string `json:"mime_type"`
			Path     string `json:"path"`
			Size     int64  `json:"size"`
			Type     string `json:"type"`
		}{MimeType: a.MIMEType, Path: a.Path, Size: a.Size, Type: a.Type})
	}
	data, err := json.Marshal(frame)
	if err != nil {
		logsafeError("ws: marshal user_message failed", "session_id", hcm.sessionID, "error", err)
		return
	}
	principal, _ := hcm.wc.messageRetryPrincipal()
	hcm.h.rememberAcceptedMessage(hcm.sessionID, entry.ClientMessageID, data, principal)
	hcm.h.hubPublishAndDeliverAlsoTo(hcm.sessionID, string(generated.WsFrameTypeUserMessage), data, hcm.wc)
}

// resolveTargetAgent resolves the turn's agent (omnipus-system-agent §6.4):
// the frame's agent_id for a new chat, the session's canonical owner for a
// frame naming an existing session without one. There is no default agent:
// a new chat that names no agent is refused with "an agent must be named"
// (FR-122) before anything is minted or dispatched. Worker targets are
// rejected as before.
func (hcm *wsHandlerHandleChatMessage) resolveTargetAgent() bool {
	hcm.targetAgentID = strings.TrimSpace(hcm.agentID)
	if hcm.targetAgentID != "" {
		if err := validateEntityID(hcm.targetAgentID); err != nil {
			logsafeWarn("ws: invalid agent_id in message frame; rejecting", "agent_id", hcm.targetAgentID, "error", err)
			hcm.sendResolveRefusal("invalid agent_id")
			return true
		}
	}
	// CHECK F01: a case spelling of the system agent is the system agent for
	// every guard and write below.
	hcm.targetAgentID = foldSystemAgentID(hcm.targetAgentID)
	hcm.agentID = hcm.targetAgentID
	if hcm.frameSessionID != "" {
		// §6.4: an existing session runs under its canonical owner. An empty
		// or matching agent_id proceeds under that owner; a different one —
		// or an owner that no longer exists — is refused before anything is
		// saved, naming the agents (CHECK F02). An unknown session id
		// resolves no owner here; the session checks below reject it.
		if owner := hcm.existingSessionOwner(); owner != "" {
			// #1330: only the requested system ID folds above; ordinary IDs
			// must match the stored canonical owner exactly before any write.
			if hcm.targetAgentID != "" && hcm.targetAgentID != owner {
				logsafeWarn("ws: rejecting message frame whose agent_id conflicts with the session owner",
					"agent_id", hcm.targetAgentID, "owner", owner, "session_id", hcm.frameSessionID)
				hcm.sendResolveRefusal(sessionOwnerConflictRefusal(owner, hcm.targetAgentID))
				return true
			}
			if _, registered := hcm.h.agentLoop.GetRegistry().GetAgent(owner); !registered {
				logsafeWarn("ws: rejecting message to a session whose owner no longer exists",
					"owner", owner, "session_id", hcm.frameSessionID)
				hcm.sendResolveRefusal(sessionOwnerMissingRefusal(owner))
				return true
			}
			if hcm.targetAgentID == "" {
				hcm.targetAgentID = owner
				return false
			}
			hcm.targetAgentID = owner
		} else if hcm.targetAgentID == "" {
			return false
		}
	}
	if hcm.targetAgentID == "" {
		logsafeWarn("ws: rejecting new-chat frame that names no agent",
			"chat_id", hcm.chatID, "workspace_id", hcm.workspaceID)
		sendConnGenFrame(hcm.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: agentMustBeNamed,
		})
		return true
	}
	// New-chat admission must finish before minting or publishing anything.
	if hcm.frameSessionID == "" {
		if _, registered := hcm.h.agentLoop.GetRegistry().GetAgent(hcm.targetAgentID); !registered {
			logsafeWarn("ws: rejecting new-chat frame for an unknown agent", "agent_id", hcm.targetAgentID)
			hcm.sendResolveRefusal(fmt.Sprintf("the requested agent is not available: %q", hcm.targetAgentID))
			return true
		}
	}
	if isWorkerAgentID(hcm.h.agentLoop.GetConfig(), hcm.targetAgentID) && !hcm.addressesExistingHelperOf(hcm.targetAgentID) {
		// An explicit agent_id that resolves to a worker is illegitimate: a worker
		// is a delegation-only labor tier, never a chat target. Refuse to mint a
		// live chat session for it. Mirror the error-frame pattern used for an
		// unknown/invalid session below.
		logsafeWarn("ws: rejecting chat frame addressed to a worker agent",
			"agent_id", hcm.targetAgentID, "chat_id", hcm.chatID,
			"reason", "worker is invoked via delegation, not as a chat target")
		sendConnGenFrame(hcm.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "this agent is a worker and cannot be a chat target — workers are invoked via delegation",
		})
		return true
	}
	return false
}

// existingSessionOwner returns the stored owner of the frame's existing session,
// or "" when the session cannot be found or read.
func (hcm *wsHandlerHandleChatMessage) existingSessionOwner() string {
	store := hcm.h.agentLoop.ResolveSessionStore(hcm.frameSessionID)
	if store == nil {
		return ""
	}
	meta, err := store.GetMeta(hcm.frameSessionID)
	if err != nil || meta == nil {
		return ""
	}
	return strings.TrimSpace(meta.AgentID)
}

// sendResolveRefusal sends a target-agent refusal as an error frame, tagged
// with the frame's session id when it named one.
func (hcm *wsHandlerHandleChatMessage) sendResolveRefusal(message string) {
	var sidPtr *string
	if hcm.frameSessionID != "" {
		sidCopy := hcm.frameSessionID
		sidPtr = &sidCopy
	}
	sendConnGenFrame(hcm.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
		Type:      string(generated.WsFrameTypeError),
		Message:   message,
		SessionId: sidPtr,
	})
}

// validateFrameIDs validates the client-supplied agent_id/session_id formats up front and drops an unknown workspace binding.
func (hcm *wsHandlerHandleChatMessage) validateFrameIDs() bool {
	// Validate the raw client-supplied agent_id format HERE, before any
	// session is minted or resumed: every frame-level validation completes
	// before the message has any effect.
	if hcm.agentID != "" {
		if err := validateEntityID(hcm.agentID); err != nil {
			logsafeWarn("ws: invalid agent_id in message frame; rejecting", "agent_id", hcm.agentID, "error", err)
			var sidPtr *string
			if hcm.frameSessionID != "" {
				sidCopy := hcm.frameSessionID
				sidPtr = &sidCopy
			}
			sendConnGenFrame(hcm.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
				Type:      string(generated.WsFrameTypeError),
				Message:   "invalid agent_id",
				SessionId: sidPtr,
			})
			return true
		}
	}

	hcm.sessionID = hcm.frameSessionID

	// Validate the client-supplied session_id format up front too — same
	// rationale as the agent_id check above.
	if hcm.sessionID != "" {
		if err := validation.EntityID(hcm.sessionID); err != nil {
			logsafeWarn("ws: invalid session_id in message frame", "session_id", hcm.sessionID, "error", err)
			sidCopy := hcm.sessionID
			sendConnGenFrame(hcm.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
				Type:      string(generated.WsFrameTypeError),
				Message:   "invalid session_id format",
				SessionId: &sidCopy,
			})
			return true
		}
	}

	// M4: validate the client-supplied workspace_id at the binding boundary
	// before it is ever stamped onto session meta. A bad/stale/typo'd id would
	// otherwise persist tasks to a workspace no board renders while the agent
	// reports success. On a miss, drop the binding and fall back to the default
	// (resolveWorkspaceID/ResolveDefaultID picks up the real default) rather
	// than stamping the bogus id.
	if hcm.workspaceID != "" && hcm.h.home != "" && !workspace.Exists(hcm.h.home, hcm.workspaceID) {
		logsafeWarn("ws: dropping unknown workspace_id binding — falling back to default",
			"workspace_id", hcm.workspaceID, "chat_id", hcm.chatID)
		hcm.workspaceID = ""
	}

	return false
}

// resolveSessionStore picks the store that owns the session (the existing session's owner store, else shared, else the agent store) and rejects a first send with no usable store.
func (hcm *wsHandlerHandleChatMessage) resolveSessionStore() bool {
	// Pick the right store for the operation:
	//
	//   * Resuming an existing session: ask ResolveSessionStore to find the
	//     owning store. New chat sessions live in sharedSessionStore, but
	//     sessions created by the task scheduler, by per-agent tools, or by
	//     custom agents (e.g. Hans completing a task) live under
	//     agents/<id>/sessions and are visible only via the per-agent stores.
	//     The previous code unconditionally picked sharedSessionStore here,
	//     so any non-shared session produced "session not found" on the next
	//     user message even though the SPA could read its transcript through
	//     the REST GET /api/v1/sessions/{id} endpoint (which already uses
	//     ResolveSessionStore).
	//
	//   * Minting a new session: keep using the shared store so every fresh
	//     chat lands in the modern shared layout — that part was never broken.

	if hcm.sessionID != "" {
		hcm.store = hcm.h.agentLoop.ResolveSessionStore(hcm.sessionID)
		if hcm.store == nil {
			// Truly unknown session — surface it explicitly so the SPA can
			// render the "session not found" toast/banner rather than silently
			// publishing the message to the bus against a non-existent session.
			logsafeWarn(
				"ws: session not found (no store owns it)",
				"session_id", hcm.sessionID,
			)
			sidCopy := hcm.sessionID
			sendConnGenFrame(hcm.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
				Type:      string(generated.WsFrameTypeError),
				Message:   "session not found",
				SessionId: &sidCopy,
			})
			return true
		}
	} else {
		hcm.store = hcm.h.agentLoop.GetSessionStore()
	}

	if hcm.firstMessage && hcm.store == nil {
		hcm.sendFirstMessageError("not_saved")
		return true
	}

	return false
}

// collectAcceptedMedia filters the client-supplied media refs down to well-formed media:// refs under the inbound caps, counting every drop.
func (hcm *wsHandlerHandleChatMessage) collectAcceptedMedia() {
	// #254: thread client-supplied media refs into the inbound message so the
	// agent loop resolves them into multimodal content blocks. Only accept
	// Hard-cap inbound media to prevent resource exhaustion. Refs beyond
	// maxInboundMediaRefs and refs exceeding maxInboundRefLen are dropped and
	// counted against the inbound-dropped counter.
	//
	// Computed HERE — before the transcript-append block below — so that
	// D2 (library-spec) can persist the accepted, validated set as
	// session.TranscriptEntry.Attachments. It used to be computed AFTER
	// that block, which meant the persisted transcript never had access to
	// the media this very message carried.
	const maxInboundMediaRefs = 16
	const maxInboundRefLen = 256

	for i, ref := range hcm.mediaRefs {
		if i >= maxInboundMediaRefs {
			hcm.wc.inboundDropped.Add(1)
			logsafeWarn("ws: media array exceeds cap — dropping remaining refs",
				"chat_id", hcm.chatID, "session_id", hcm.sessionID,
				"dropped_from_index", i, "total_supplied", len(hcm.mediaRefs))
			break
		}
		if len(ref) > maxInboundRefLen {
			hcm.wc.inboundDropped.Add(1)
			logsafeWarn("ws: dropping oversized ref in message frame",
				"chat_id", hcm.chatID, "session_id", hcm.sessionID,
				"ref_prefix", ref[:32])
			continue
		}
		// Accept only well-formed media:// refs (non-empty ID validated by
		// ParseMediaRef — rejects bare "media://" with empty ID, non-prefixed
		// strings, raw paths, and HTTP URLs that a buggy channel might emit).
		// Non-matching strings are a client error or smuggling attempt — drop
		// and count them via the inboundDropped counter.
		if _, err := media.ParseMediaRef(ref); err == nil {
			hcm.acceptedMedia = append(hcm.acceptedMedia, ref)
		} else {
			hcm.wc.inboundDropped.Add(1)
			truncated := ref
			if len(truncated) > 64 {
				truncated = truncated[:64] + "…"
			}
			logsafeWarn("ws: dropping invalid media:// ref in message frame",
				"chat_id", hcm.chatID, "session_id", hcm.sessionID,
				"ref_prefix", truncated)
		}
	}
}

// applyMintTimeAutoApproveChoice records a per-chat Auto-approve choice
// carried on THIS minting message (MessageFrame.auto_approve, ADR-092
// founder ruling 2026-09-24) into SessionModeStore for the just-minted
// newSessionID. A no-op when hcm.autoApprove is nil (no choice sent).
//
// Called from recordSessionAndTranscript's mint branch, strictly BEFORE
// buildInboundMessage/PublishInbound further down in
// handleChatMessageWithClientID: the choice must already be in the store
// before the turn reaches the agent loop, or the new chat's first
// ask-policy tool call can be decided under the wrong mode — the LLM round
// trip that produces that first tool call runs on a separate goroutine, so
// "soon after" is not good enough. This closes that race outright, unlike
// the SPA's superseded post-session_started session_mode_update send it
// replaces. Shares the exact set-and-audit path
// handleSessionModeUpdateFrame (ws_session_mode.go) uses for a live chat's
// toggle, so the two can never diverge.
func (hcm *wsHandlerHandleChatMessage) applyMintTimeAutoApproveChoice(newSessionID string) {
	if hcm.autoApprove == nil {
		return
	}
	hcm.h.applySessionModeChoice(hcm.ctx, hcm.targetAgentID, newSessionID, hcm.autoApprove, "message")
}

// recordSessionAndTranscript mints or resumes the session and persists the user transcript entry.
func (hcm *wsHandlerHandleChatMessage) recordSessionAndTranscript() bool {
	if hcm.store != nil {
		if hcm.sessionID == "" {
			if hcm.mintSession() {
				return true
			}
		} else if hcm.adoptExistingSession() {
			return true
		}
		if hcm.persistUserMessage() {
			return true
		}
	}
	return false
}

// mintSession creates and stamps a session for a message with no session_id.
// Ordinary first-message acknowledgement is deferred until its durable append.
// Legacy no-ID oversized refusals keep their acknowledgement path.
// Returns true when the message was rejected.
func (hcm *wsHandlerHandleChatMessage) mintSession() bool {
	// omnipus-system-agent FR-003: a first message to Omnipus is accepted only
	// inside the default workspace.
	home := hcm.h.home
	if home == "" {
		home = config.OmnipusHomeDir()
	}
	// FR-005: Omnipus has one conversation; a second is refused with its
	// main chat's id, before anything is minted.
	if msg, _, err := omnipusSecondConversationRefusal(hcm.h.agentLoop.GetSessionStore(), home, hcm.targetAgentID); err != nil || msg != "" {
		if err != nil {
			msg = serverFailureText("could not check Omnipus's conversation", err)
		}
		sendConnGenFrame(hcm.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: msg,
		})
		return true
	}
	if hcm.refuseOmnipusChat(home, hcm.targetAgentID, hcm.workspaceID, "") {
		return true
	}
	// No session_id in frame: mint a new session so all subsequent frames have one.
	meta, err := hcm.store.NewSession(session.SessionTypeChat, "webchat", hcm.targetAgentID)
	if err != nil {
		logsafeError("ws: could not create session", "error", err)
		if hcm.firstMessage {
			hcm.sendFirstMessageError("not_saved")
			return true
		}
		sendConnGenFrame(hcm.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "could not create session: session storage failed. Check disk space and permissions, then retry. Details are in the server log.",
		})
		return true
	}
	hcm.sessionID = meta.ID
	title := session.TitleFromFirstMessage(hcm.content)
	// Stamp the session owner from the authenticated WebSocket user (SEC-2/#406).
	// wc.userID is set at auth time (FR-073); empty on dev-mode bypass.
	ownerCopy := hcm.wc.userID
	metaPatch := session.MetaPatch{Title: &title, Owner: &ownerCopy}
	// M4: bind the new session to the active workspace so created tasks
	// land on this workspace's board (not the agent's default).
	if hcm.workspaceID != "" {
		wsCopy := hcm.workspaceID
		metaPatch.WorkspaceID = &wsCopy
	}
	if err := hcm.store.SetMeta(meta.ID, metaPatch); err != nil {
		if hcm.firstMessage {
			logsafeWarn("ws: could not stamp first-message session", "session_id", meta.ID, "error", err)
			hcm.sendFirstMessageError("not_saved")
			return true
		}
		logsafeWarn("ws: could not set session title/owner", "session_id", meta.ID, "error", err)
	}
	hcm.applyMintTimeAutoApproveChoice(meta.ID)
	if !hcm.firstMessage {
		hcm.acknowledgeNewSession()
	}
	return false
}

// adoptExistingSession validates a client-supplied session_id, re-stamps the
// active workspace, and binds the connection to the session for live
// delivery. Returns true when the message was rejected.
func (hcm *wsHandlerHandleChatMessage) adoptExistingSession() bool {
	// Final-gate M4: a stale tab must not keep chatting in a main the pair is no
	// longer entitled to.
	if !hcm.h.mainSessionAllowed(hcm.wc, hcm.sessionID) {
		return true
	}
	// The session_id format was already validated up front
	// (validateFrameIDs). Validate that the session actually exists in the
	// store.
	existingMeta, err := hcm.store.GetMeta(hcm.sessionID)
	if err != nil {
		logsafeWarn("ws: session not found", "session_id", hcm.sessionID, "error", err)
		sidCopy := hcm.sessionID
		sendConnGenFrame(hcm.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:      string(generated.WsFrameTypeError),
			Message:   "session not found",
			SessionId: &sidCopy,
		})
		return true
	}
	// M4: track the ACTIVE workspace on every message, not just the first
	// one. The SPA resends the CURRENTLY active workspace_id on every
	// outbound frame (src/store/chat.ts sendMessage, read live from
	// useWorkspacesStore) specifically so a task/delegation this turn
	// creates lands on whatever workspace the operator is looking at
	// right now — including an ongoing chat session that started in one
	// workspace and is continuing in another. A stale "first binding
	// wins" rule broke that: a delegation edge wired on a workspace's
	// Team tab AFTER the session's first bind would never be consulted
	// by resolveEffectiveWorkspaceID (pkg/agent/loop.go), which reads
	// this same session meta fresh every turn — so the UI's "Saved just
	// now" was genuine (see TestWorkspaceDelegation_EdgeWiredViaTeamTabPersistsForLiveSession)
	// but the running session kept enforcing/advertising delegation against its
	// ORIGINAL workspace until the operator started a brand new session.
	// Only skip the rewrite when this message carries no workspace_id at
	// all (workspaceID == "") — an absent value must never blank out an
	// existing binding (e.g. a non-workspace-aware channel message
	// resuming a workspace-bound session), it just leaves it as-is.
	if hcm.workspaceID != "" && existingMeta != nil && existingMeta.WorkspaceID != hcm.workspaceID {
		// FR-003: an Omnipus chat lives in the default workspace only; a frame
		// naming another workspace must not re-bind it there.
		if sessionIsOmnipusChat(existingMeta) {
			home := hcm.h.home
			if home == "" {
				home = config.OmnipusHomeDir()
			}
			if hcm.refuseOmnipusChat(home, string(coreagent.IDOmnipus), hcm.workspaceID, hcm.sessionID) {
				return true
			}
		}
		wsCopy := hcm.workspaceID
		if err := hcm.store.SetMeta(hcm.sessionID, session.MetaPatch{WorkspaceID: &wsCopy}); err != nil {
			logsafeWarn("ws: could not bind workspace to session", "session_id", hcm.sessionID, "error", err)
		}
	}
	// Track for streamer, and (#823) bind this connection to the
	// session's hub for live delivery if it is not bound to any
	// session yet — a client that messages an existing session
	// without attaching first must still see its own turn. A
	// connection already attached elsewhere keeps its binding: the
	// sender still gets its own ticks/echo as unsequenced copies.
	hcm.h.mu.Lock()
	if hcm.h.sessionIDs[hcm.chatID] == "" {
		hcm.h.sessionIDs[hcm.chatID] = hcm.sessionID
	}
	if hcm.wc != nil && hcm.wc.boundHub == nil {
		hcm.h.bindConnToSessionHubLocked(hcm.wc, hcm.sessionID)
	}
	hcm.h.mu.Unlock()
	return false
}

// persistUserMessage records the user's message in the transcript (when the
// session exists and the message is within the user-message bound) and, once
// persisted, echoes it to every tab as a user_message frame. An ordinary
// user append also persists its server-internal provenance in the same
// session write (ADR-20260928 D1.4). ANY failed append — first or not —
// rejects intake: a visible error for the sender, no echo, no turn.
func (hcm *wsHandlerHandleChatMessage) persistUserMessage() bool {
	// ID-bearing first sends pass their size preflight before session creation.
	// ADR-066 D4 / FR-015: legacy no-ID first sends and existing-session
	// messages still rely on processMessage for the over-bound refusal. Skip
	// their early transcript write; the refusal returns through the ordinary
	// outbound path (token + done frames).
	overUserBound := agent.UserMessageChars(hcm.content) > hcm.h.agentLoop.UserMessageBound()
	if hcm.sessionID != "" && !overUserBound {
		entry := session.TranscriptEntry{
			ID:        uuid.New().String(),
			Role:      "user",
			AgentID:   hcm.targetAgentID,
			Content:   hcm.content,
			Timestamp: time.Now().UTC(),
			// D2 (library-spec, 2026-07-29 UAT): persist which files this
			// message attached. Previously this field was never set even
			// though it has existed on TranscriptEntry all along — a later
			// turn (or a DIFFERENT AGENT after a handoff, exactly what
			// happened in the UAT: Mia -> Ray) had no durable record of
			// what was uploaded, only the live in-flight message. Built
			// from acceptedMedia (the validated ref set), not the raw
			// client-supplied mediaRefs.
			Attachments: buildTranscriptAttachments(hcm.h.agentLoop.GetMediaStore(), hcm.acceptedMedia, hcm.workspaceID),
		}
		// #823 BE-DESIGN.md §4.7: the persisted user entry carries the
		// client's own message id, so replay (replay_message.
		// client_message_id) and the live user_message echo can both
		// reconcile the sender's pending bubble — no duplicate, and never a
		// bubble stranded above history.
		entry.ClientMessageID = hcm.clientMessageID
		// ADR-20260928 D1.4: an accepted authenticated user message persists
		// server-internal provenance (server message id, session, exact
		// accepted text, connection principal, per-session append ordinal) in
		// the SAME session write as this append. wc.userID is the
		// WS-authenticated principal (FR-073); empty only where the
		// connection has none (dev-mode bypass / legacy env-token auth). The
		// store stamps every other provenance field from the entry it
		// actually persists.
		appendErr := hcm.store.AppendTranscriptWithProvenance(hcm.sessionID, entry, hcm.wc.userID)
		if appendErr != nil {
			logsafeWarn("ws: could not record user message", "session_id", hcm.sessionID, "error", appendErr)
			if hcm.firstMessage {
				hcm.sendFirstMessageError(firstMessageAppendFailure(appendErr))
				return true
			}
			// Every failed append is visible to the sender and never
			// continues the turn (ADR-20260928 D1.4). The previous
			// warn-and-continue for a non-first append is gone: without the
			// durable write there is nothing to acknowledge, echo, or admit.
			hcm.sendAppendFailureError(appendErr)
			return true
		}
		hcm.acceptPersistedUserMessage(entry)
	}
	return false
}

// acceptPersistedUserMessage runs the success path of a durable append:
// mark the intake persisted, acknowledge an ordinary first send, and echo
// the entry to every tab as a user_message frame.
func (hcm *wsHandlerHandleChatMessage) acceptPersistedUserMessage(entry session.TranscriptEntry) {
	hcm.transcriptPersisted = true
	hcm.transcriptEntryID = entry.ID
	if hcm.firstMessage {
		hcm.rememberAcceptedFirstMessage()
		hcm.acknowledgeNewSession()
	}
	hcm.publishUserMessage(entry)
}

// sendAppendFailureError reports a failed transcript append to the sender for
// a message that is not an ID-bearing first send (ADR-20260928 D1.4: a failed
// append is visible for EVERY message, and never continues the turn). Same
// discipline as sendFirstMessageError: no filesystem details, and an unsaved
// or uncertain append of a NEW chat's first message never names the session,
// so nothing looks acknowledged; a message of an existing chat names it (C2).
// The sender's deferred message_status "failed" tick still fires on top of
// this frame through the ordinary rejection path.
func (hcm *wsHandlerHandleChatMessage) sendAppendFailureError(err error) {
	message := "Could not save message"
	if firstMessageAppendFailure(err) == "delivery_unknown" {
		message = "Delivery not confirmed"
	}
	frame := generated.ErrorFrame{
		Type:    string(generated.WsFrameTypeError),
		Message: message,
	}
	if hcm.clientMessageID != "" {
		cid := hcm.clientMessageID
		frame.ClientMessageId = &cid
		// Contract C2: a refusal about ONE message of an existing chat names
		// that chat (the one the sender addressed) beside the message id, so
		// the SPA files it there and does not end the chat's running turn. A
		// send that names no chat (a new chat) has none to name.
		if hcm.frameSessionID != "" {
			sid := hcm.frameSessionID
			frame.SessionId = &sid
		}
	}
	sendConnGenFrame(hcm.wc, string(generated.WsFrameTypeError), frame)
}

// buildInboundMessage assembles the bus.InboundMessage from the client content, agent/model metadata, and the accepted media.
func (hcm *wsHandlerHandleChatMessage) buildInboundMessage() {
	hcm.msg = bus.InboundMessage{
		Channel: "webchat",
		Sender: bus.SenderInfo{
			CanonicalID: "webchat_user",
		},
		// FR-017: carry the WS-authenticated gateway principal (set at auth
		// time, e.g. "cli" or the account's username) so the agent loop can
		// stamp audit.Entry.User for turn attribution. This is the ONLY site that sets
		// GatewayUserID — it is a dedicated carrier, NOT Sender.Username, so that
		// platform channels (which fill Sender.Username with the platform handle)
		// can never have their sender misattributed as a gateway principal.
		// Empty under dev-mode bypass / legacy env-token auth (wc.userID is left
		// unset there) — the audit stamp then stays empty rather than guessing.
		GatewayUserID: hcm.wc.userID,
		ChatID:        hcm.chatID,
		Content:       hcm.content,
		SessionID:     hcm.sessionID,
		// The user entry persistUserMessage wrote for this very message —
		// lets the agent loop's history rebuild recognise it by identity.
		TranscriptEntryID: hcm.transcriptEntryID,
		Media:             hcm.acceptedMedia,
		// UserInitiated (ADR-049 Gap #8/r2, R6): the webchat WS `message`
		// handler is the "Web WS message handler (authenticated gateway
		// user)" origination point — always a genuine live user action.
		UserInitiated: true,
		// OperatorPrompt (ADR-085 BROWSER-FR-029): a fail-closed sibling of
		// UserInitiated, set true ONLY here, pkg/gateway/sse.go and
		// pkg/channels/base.go::HandleMessage — the three sites where the
		// OPERATOR composed this message on THIS session. It is what
		// releases a held browser wheel (pkg/agent/loop.go::processMessage,
		// via AgentLoop.SetBrowserWheelReleaseHook) before the turn begins.
		// Deliberately NOT reused from UserInitiated (which is also true on
		// the question-card resume path, where a release would hand the
		// browser back mid-drive — see ws_ask_user.go, which must NOT set
		// this field).
		OperatorPrompt: true,
	}
	if hcm.targetAgentID != "" {
		// The resolved turn agent: the frame's agent_id (format validated up
		// front) or, for an existing session,
		// its canonical owner (§6.4) — never left to a default.
		hcm.msg.Metadata = map[string]string{"agent_id": hcm.targetAgentID}
	}
	// FR-010: forward per-turn model override to the bus so the agent loop's
	// switch-compress path can route THIS turn to the chosen model. Trim
	// whitespace first; empty / whitespace-only values are dropped so the agent
	// falls back to its configured model. The map is created lazily here so a
	// model_name-only frame still produces a populated Metadata on the wire.
	trimmedModel := strings.TrimSpace(hcm.modelName)
	if trimmedModel != "" {
		if hcm.msg.Metadata == nil {
			hcm.msg.Metadata = map[string]string{}
		}
		hcm.msg.Metadata["model_name"] = trimmedModel
	}
}

// buildTranscriptAttachments resolves each accepted media ref into a
// session.Attachment for persistence on the user's TranscriptEntry (D2,
// library-spec). A ref that fails to resolve (deleted file, integrity
// failure, cross-workspace mismatch) is logged and skipped rather than
// aborting the whole message — an attachment record is best-effort
// metadata, not load-bearing for the turn itself (the raw ref still reaches
// the agent loop via msg.Media regardless of what this function returns).
//
// Path prefers the EXACT workspace-relative path the D-1 dual-write staged
// the file at (agent.LookupUploadWorkPath), falling back to the best-effort
// plain-name formula (agent.FallbackAnnouncedUploadPath) for a workspace ref
// whose write-time record is unavailable (e.g. a gateway restart between
// the upload and this message), and finally to the bare ref for a
// non-workspace ref (legacy media://<uuid>, channel-native attachments),
// which has no workspace-relative path at all.
func buildTranscriptAttachments(store media.MediaStore, refs []string, callerWorkspace string) []session.Attachment {
	if store == nil || len(refs) == 0 {
		return nil
	}
	opts := media.ResolveOpts{}
	if callerWorkspace != "" {
		opts = media.WithCallerWorkspace(callerWorkspace)
	}
	out := make([]session.Attachment, 0, len(refs))
	for _, ref := range refs {
		localPath, meta, err := store.ResolveWithMetaOpts(ref, opts)
		if err != nil {
			logsafeWarn("ws: could not resolve media ref for transcript attachment",
				"ref", ref, "error", err)
			continue
		}
		var size int64
		if info, statErr := os.Stat(localPath); statErr == nil {
			size = info.Size()
		} else {
			logsafeWarn("ws: could not stat media file for transcript attachment size",
				"ref", ref, "error", statErr)
		}
		path := ref
		if media.IsWorkspaceRef(ref) {
			if workPath, ok := agent.LookupUploadWorkPath(ref); ok {
				path = workPath
			} else if fallback := agent.FallbackAnnouncedUploadPath(meta.Filename); fallback != "" {
				path = fallback
			}
		}
		out = append(out, session.Attachment{
			// DetectAttachmentType, NOT DetectFileClass. This value crosses
			// the wire, so it must be the contract's media-category enum
			// (image|audio|video|file). DetectFileClass returns the
			// presentation-noun class (image|document|file) used to phrase
			// the LLM guidance line — "document" is not a legal wire value.
			// Using it here shipped a BLOCKER: the SPA's strict Zod schema
			// rejected the whole messages payload, so chat history refused
			// to load at all after any document upload (live UAT 2026-07-29).
			Type:     agent.DetectAttachmentType(meta.ContentType, meta.Filename),
			Path:     path,
			Size:     size,
			MIMEType: meta.ContentType,
		})
	}
	return out
}

// addressesExistingHelperOf reports whether the frame names an existing
// helper (steered) session run by workerID. A person typing into that
// helper's own pane steers that existing session (Steering commands C1);
// the worker guard keeps refusing a new or unrelated chat with a worker.
func (hcm *wsHandlerHandleChatMessage) addressesExistingHelperOf(workerID string) bool {
	if hcm.frameSessionID == "" {
		return false
	}
	lifecycle := hcm.h.agentLoop.GetSessionLifecycleStore()
	if lifecycle == nil {
		return false
	}
	rec, err := lifecycle.Load(hcm.frameSessionID)
	if err != nil || rec == nil {
		return false
	}
	return rec.SteeredBy != nil && rec.AgentID == workerID
}

// sessionIsOmnipusChat reports whether meta is a chat owned by Omnipus.
func sessionIsOmnipusChat(meta *session.UnifiedMeta) bool {
	return meta != nil && len(meta.AgentIDs) > 0 && meta.AgentIDs[0] == string(coreagent.IDOmnipus)
}

// refuseOmnipusChat applies omnipusChatRefusal at a WebSocket door: it sends
// the client ONE error frame and returns true when the Omnipus chat must not
// proceed, the refusal text for a user-facing no, the fixed server-failure text
// for a storage fault. sessionID names the session the frame concerns ("" when
// none yet).
func (hcm *wsHandlerHandleChatMessage) refuseOmnipusChat(home, agentID, workspaceID, sessionID string) bool {
	msg, fault := omnipusChatRefusal(home, agentID, workspaceID)
	if fault != nil {
		msg = serverFailureText("could not check where Omnipus's chat may live", fault)
	}
	if msg == "" {
		return false
	}
	frame := generated.ErrorFrame{Type: string(generated.WsFrameTypeError), Message: msg}
	if sessionID != "" {
		sid := sessionID
		frame.SessionId = &sid
	}
	sendConnGenFrame(hcm.wc, string(generated.WsFrameTypeError), frame)
	return true
}
