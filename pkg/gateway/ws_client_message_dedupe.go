// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ws_client_message_dedupe.go: idempotent message intake by
// client_message_id (#823 review item 6).
//
// A client re-sends a message when it never saw the server's acknowledgement
// (the connection dropped between send and "received"). If the first copy did
// reach the server, handling the retry as new would persist a second user
// message and start a second turn. The gateway remembers, per session, the
// client message ids it has accepted, and answers a retry by re-sending that
// message's echo and current status to the sender only, unsequenced — no new
// entry, no new turn, nothing re-published to the session. Session-less first
// sends also retain their authenticated principal + client id in memory (#1090).
// Their retries receive a recovered session_started and a received status, then
// attach for replay. A restart loses this cache, including the save-before-ack
// crash window; there is deliberately no on-disk first-send ledger. First-send
// entries are bounded per principal and globally, oldest accepted first. A retry
// of an evicted first id can mint a second chat, just like a retry after restart.
package gateway

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// clientMessagePrincipal distinguishes human accounts, the machine CLI,
// successfully authenticated legacy tokens, and this handler's dev bypass.
// Separate fields prevent a username from aliasing another principal kind.
type clientMessagePrincipal struct {
	kind     string
	identity string
}

type firstClientMessageKey struct {
	principal       clientMessagePrincipal
	clientMessageID string
}

const (
	maxClientMessageIDChars                 = 128
	acceptedFirstClientMessagesPerPrincipal = 256
	acceptedFirstClientMessagesTotal        = 10_000
)

type firstClientMessageDigest [sha256.Size]byte

// acceptedFirstClientMessage is recorded only after a successful durable
// append. It is an extension of the in-memory dedupe, never a prepared claim.
type acceptedFirstClientMessage struct {
	sessionID       string
	agentID         string
	store           *session.UnifiedStore
	acceptanceOrder uint64
	requestDigest   firstClientMessageDigest
}

func (wc *wsConn) messageRetryPrincipal() (clientMessagePrincipal, bool) {
	if wc == nil {
		return clientMessagePrincipal{}, false
	}
	if wc.userID != "" {
		kind := "account"
		if wc.isCLIToken {
			kind = "cli"
		}
		return clientMessagePrincipal{kind: kind, identity: wc.userID}, true
	}
	return wc.retryPrincipal, wc.retryPrincipal.kind != ""
}

// digestFirstMessageRequest runs before any routing/default resolution or media
// normalization. Length prefixes and a media count preserve field boundaries
// and reference order; absent/false/true Auto choices have distinct bytes. Only
// the SHA-256 result survives intake, never the raw request text in this cache.
func (hcm *wsHandlerHandleChatMessage) digestFirstMessageRequest() firstClientMessageDigest {
	digest := sha256.New()
	writeString := func(value string) {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		// hash.Hash.Write is documented never to return an error.
		_, _ = digest.Write(length[:])
		_, _ = digest.Write([]byte(value))
	}
	for _, value := range []string{hcm.content, hcm.agentID, hcm.workspaceID, hcm.modelName} {
		writeString(value)
	}
	var mediaCount [8]byte
	binary.BigEndian.PutUint64(mediaCount[:], uint64(len(hcm.mediaRefs)))
	_, _ = digest.Write(mediaCount[:])
	for _, ref := range hcm.mediaRefs {
		writeString(ref)
	}
	autoChoice := byte(0)
	if hcm.autoApprove != nil {
		autoChoice = 1
		if *hcm.autoApprove {
			autoChoice = 2
		}
	}
	_, _ = digest.Write([]byte{autoChoice})
	var result firstClientMessageDigest
	copy(result[:], digest.Sum(nil))
	return result
}

func (hcm *wsHandlerHandleChatMessage) rememberAcceptedFirstMessage() {
	principal, authenticated := hcm.wc.messageRetryPrincipal()
	if !authenticated || hcm.clientMessageID == "" {
		return
	}
	key := firstClientMessageKey{principal: principal, clientMessageID: hcm.clientMessageID}
	hcm.h.mu.Lock()
	defer hcm.h.mu.Unlock()
	h := hcm.h
	if h.acceptedFirstClientMsgs == nil {
		h.acceptedFirstClientMsgs = make(map[firstClientMessageKey]acceptedFirstClientMessage)
	}
	order := h.acceptedFirstClientMsgs[key].acceptanceOrder
	if _, exists := h.acceptedFirstClientMsgs[key]; !exists {
		h.evictAcceptedFirstMessageLocked(principal)
		h.firstMessageAcceptanceOrder++
		order = h.firstMessageAcceptanceOrder
	}
	h.acceptedFirstClientMsgs[key] = acceptedFirstClientMessage{
		sessionID:       hcm.sessionID,
		agentID:         hcm.targetAgentID,
		store:           hcm.store,
		acceptanceOrder: order,
		requestDigest:   hcm.firstRequestDigest,
	}
}

// evictAcceptedFirstMessageLocked makes room for a new accepted id. The scan
// is bounded by the global entry limit; no principal counters outlive entries.
// A principal at its limit loses its own oldest entry, otherwise the global
// limit evicts the oldest entry across principals. Caller holds h.mu.
func (h *WSHandler) evictAcceptedFirstMessageLocked(principal clientMessagePrincipal) {
	var oldest, oldestForPrincipal firstClientMessageKey
	var oldestOrder, oldestPrincipalOrder uint64
	principalCount := 0
	for key, accepted := range h.acceptedFirstClientMsgs {
		if oldestOrder == 0 || accepted.acceptanceOrder < oldestOrder {
			oldest, oldestOrder = key, accepted.acceptanceOrder
		}
		if key.principal == principal {
			principalCount++
			if oldestPrincipalOrder == 0 || accepted.acceptanceOrder < oldestPrincipalOrder {
				oldestForPrincipal, oldestPrincipalOrder = key, accepted.acceptanceOrder
			}
		}
	}
	if principalCount >= acceptedFirstClientMessagesPerPrincipal {
		delete(h.acceptedFirstClientMsgs, oldestForPrincipal)
	} else if len(h.acceptedFirstClientMsgs) >= acceptedFirstClientMessagesTotal {
		delete(h.acceptedFirstClientMsgs, oldest)
	}
}

func (hcm *wsHandlerHandleChatMessage) answerRetriedFirstMessage() bool {
	principal, authenticated := hcm.wc.messageRetryPrincipal()
	if !authenticated {
		return false
	}
	key := firstClientMessageKey{principal: principal, clientMessageID: hcm.clientMessageID}
	hcm.h.mu.Lock()
	accepted, found := hcm.h.acceptedFirstClientMsgs[key]
	hcm.h.mu.Unlock()
	if !found {
		return false
	}
	if accepted.requestDigest != hcm.firstRequestDigest {
		cid := hcm.clientMessageID
		sendConnGenFrame(hcm.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:            string(generated.WsFrameTypeError),
			Message:         "client_message_id conflict: this ID was already used for a different request",
			ClientMessageId: &cid,
		})
		return true
	}

	// The cache never authorizes disclosure after deletion or an owner change.
	// A failed lookup stays a retry hit: do not mint a second chat for this id.
	meta, err := accepted.store.GetMeta(accepted.sessionID)
	if err != nil || meta == nil || meta.Owner != hcm.wc.userID {
		cid := hcm.clientMessageID
		sendConnGenFrame(hcm.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:            string(generated.WsFrameTypeError),
			Message:         "Could not check this chat",
			ClientMessageId: &cid,
		})
		return true
	}

	recovered := true
	cid := hcm.clientMessageID
	started := generated.SessionStartedFrame{
		Type:            string(generated.WsFrameTypeSessionStarted),
		SessionId:       accepted.sessionID,
		ClientMessageId: &cid,
		Recovered:       &recovered,
	}
	if accepted.agentID != "" {
		aid := accepted.agentID
		started.AgentId = &aid
	}
	// Direct, unsequenced replies only. No binding, echo, append, or admission;
	// the browser must attach without a cursor to learn the actual turn state.
	sendConnGenFrame(hcm.wc, string(generated.WsFrameTypeSessionStarted), started)
	sendConnGenFrame(hcm.wc, string(generated.WsFrameTypeMessageStatus), generated.MessageStatusFrame{
		Type:            string(generated.WsFrameTypeMessageStatus),
		SessionId:       accepted.sessionID,
		ClientMessageId: hcm.clientMessageID,
		State:           "received",
	})
	return true
}

// acceptedClientMessagesPerSession bounds the remembered ids per session.
// A retry only ever concerns the last few messages a client sent.
const acceptedClientMessagesPerSession = 256

// acceptedClientMessage is one accepted message: its unsequenced
// user_message echo and the last status the session was told about.
type acceptedClientMessage struct {
	echo  []byte
	state string // received | working
}

// acceptedClientMessages is guarded by WSHandler.mu.
type acceptedClientMessages struct {
	byID  map[string]*acceptedClientMessage
	order []string
}

func (h *WSHandler) acceptedLocked(sessionID string) *acceptedClientMessages {
	if h.acceptedClientMsgs == nil {
		h.acceptedClientMsgs = make(map[string]*acceptedClientMessages)
	}
	a := h.acceptedClientMsgs[sessionID]
	if a == nil {
		a = &acceptedClientMessages{byID: make(map[string]*acceptedClientMessage)}
		h.acceptedClientMsgs[sessionID] = a
	}
	return a
}

// rememberAcceptedMessage records a persisted message under its client id.
func (h *WSHandler) rememberAcceptedMessage(sessionID, clientMessageID string, echo []byte) {
	if sessionID == "" || clientMessageID == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	a := h.acceptedLocked(sessionID)
	if _, ok := a.byID[clientMessageID]; !ok {
		a.order = append(a.order, clientMessageID)
	}
	a.byID[clientMessageID] = &acceptedClientMessage{echo: echo, state: "received"}
	for len(a.order) > acceptedClientMessagesPerSession {
		delete(a.byID, a.order[0])
		a.order = a.order[1:]
	}
}

// forgetAcceptedMessage drops a message that ended up not admitted (its
// publish failed and the client was told "failed"), so a retry is handled as
// a fresh send.
func (h *WSHandler) forgetAcceptedMessage(sessionID, clientMessageID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if a := h.acceptedClientMsgs[sessionID]; a != nil {
		delete(a.byID, clientMessageID)
	}
}

// markAcceptedMessageWorkingLocked records that the message's turn started.
// Caller holds h.mu.
func (h *WSHandler) markAcceptedMessageWorkingLocked(sessionID, clientMessageID string) {
	if a := h.acceptedClientMsgs[sessionID]; a != nil {
		if m := a.byID[clientMessageID]; m != nil {
			m.state = "working"
		}
	}
}

func (h *WSHandler) markAcceptedMessageWorking(sessionID, clientMessageID string) {
	h.mu.Lock()
	h.markAcceptedMessageWorkingLocked(sessionID, clientMessageID)
	h.mu.Unlock()
}

// answerRetriedMessage answers an already accepted client id to this sender
// only. Session-less ordinary sends use the authenticated first-send cache;
// existing-session sends retain their original echo/current-status behavior.
// False means the message must be processed normally.
func (hcm *wsHandlerHandleChatMessage) answerRetriedMessage() bool {
	if hcm.clientMessageID == "" {
		return false
	}
	if hcm.frameSessionID == "" && !hcm.setupKickoff {
		return hcm.answerRetriedFirstMessage()
	}
	if hcm.sessionID == "" {
		return false
	}
	h := hcm.h
	h.mu.Lock()
	var echo []byte
	var state string
	if a := h.acceptedClientMsgs[hcm.sessionID]; a != nil {
		if m := a.byID[hcm.clientMessageID]; m != nil {
			echo, state = m.echo, m.state
		}
	}
	h.mu.Unlock()
	if state == "" {
		return false
	}
	logsafeInfo("ws: retried client_message_id answered without a second turn",
		"session_id", hcm.sessionID, "client_message_id", hcm.clientMessageID, "state", state)
	if echo != nil {
		sendRawFrameBytes(hcm.wc, string(generated.WsFrameTypeUserMessage), echo)
	}
	status, err := json.Marshal(generated.MessageStatusFrame{
		Type:            string(generated.WsFrameTypeMessageStatus),
		SessionId:       hcm.sessionID,
		ClientMessageId: hcm.clientMessageID,
		State:           state,
	})
	if err == nil {
		sendRawFrameBytes(hcm.wc, string(generated.WsFrameTypeMessageStatus), status)
	}
	return true
}

// releaseEvictedSessions drops the accepted-id memory of sessions whose hub
// was evicted (final-review N7).
// It runs after the eviction, on its own goroutine, so it re-checks that no
// new hub was created for the session in the meantime.
func (h *WSHandler) releaseEvictedSessions(sessionIDs []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, id := range sessionIDs {
		if h.hubs != nil && h.hubs.lookup(id) != nil {
			continue
		}
		delete(h.acceptedClientMsgs, id)
	}
}
