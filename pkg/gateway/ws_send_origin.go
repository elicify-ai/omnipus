// ws_send_origin.go: the server binds each send to its chat (#1428 step 2,
// option C; spec SPEC-C-server-binds-send-to-chat r9, sections 4.3.1-4.3.4).
//
// Every `message`, `redirect` and `cancel` frame from a current page carries
// `origin` (the chat the input was written in) next to its target
// (`session_id`). The raw pre-check below runs FIRST for those three frame
// types, in both validation modes: before the read loop's schema validation,
// before the generated decode, before the empty-content exit and before every
// side effect. It is a consistency control, not an access check: the
// existing authentication and session-access checks stay authoritative.

package gateway

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"regexp"
	"sync/atomic"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Old-tab reply texts (spec 4.3.4, chief's exact wording; S-15). Plain
// sentences, so an old page's sanitizer shows them unchanged.
const (
	sendOriginOutdated         = "This page is out of date. Copy anything you typed, then reload the page."
	sendOriginOutdatedRedirect = "Not stopped. Reload this page. Your instruction was not sent and must be typed again."
	sendOriginOutdatedStop     = "Not stopped. Reload this page."
	// sendOriginOutdatedIDPrefix starts the synthetic client_message_id of an
	// old-tab notice; it never matches a pending or abandoned id (F-q3).
	sendOriginOutdatedIDPrefix = "outdated-"
)

// Field bounds from the contract (F-oo, C-1, C-3).
const (
	sendTargetMaxChars      = 255
	sendCorrelationMaxChars = 128
	newChatKeyMinChars      = 16
	newChatKeyMaxChars      = 128
)

// Refusal reasons (SendRefusedFrame.reason, C-4).
const (
	sendRefusedWrongChat       = "wrong_chat"
	sendRefusedOriginMalformed = "origin_malformed"
	sendRefusedOriginUnverify  = "origin_unverified"
	sendRefusedFrameMalformed  = "frame_malformed"
)

var newChatKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// _wsOriginLessAccepted counts origin-less message/redirect/cancel frames
// accepted on non-browser connections (spec 4.3.8, S-3: accepted and
// counted). Process-wide, like _wsInboundFrameDropped.
var _wsOriginLessAccepted atomic.Uint64

// WSOriginLessAcceptedCount returns the number of origin-less frames accepted
// on non-browser connections since process start (spec 4.3.8).
func WSOriginLessAcceptedCount() uint64 {
	return _wsOriginLessAccepted.Load()
}

// Test seams for the refusal snapshot's ordering boundary (spec 4.3.4 R1-R6,
// SV-19 e/f/j/k), the same pattern as attachAfterBindHook. Never set outside a
// test.
var (
	// refusalSnapshotBeforeHoldHook runs after the refusal reply (R1) and the
	// bound check (R2), before startHold (R3).
	refusalSnapshotBeforeHoldHook func(sessionID string)
	// refusalSnapshotAfterSampleHook runs after the sample (R4) and before
	// direct (R5).
	refusalSnapshotAfterSampleHook func(sessionID string)
	// refusalSnapshotBuild is the R4 builder; a test may force it to return
	// nil (the marshal-failure path).
	refusalSnapshotBuild = func(h *WSHandler, wc *wsConn, sessionID string) []byte {
		return h.sessionStateBytes(wc, sessionID)
	}
)

// sendRawFields is the lenient pre-check decode: every field the pre-check
// reads is raw, so no field type can make this decode fail. A present field
// (including JSON null) has a non-empty RawMessage.
type sendRawFields struct { // not-wire-format: lenient raw view of generated MessageFrame/RedirectFrame/CancelFrame for the pre-check only.
	SessionID       json.RawMessage `json:"session_id"`
	ClientMessageID json.RawMessage `json:"client_message_id"`
	ClientFrameID   json.RawMessage `json:"client_frame_id"`
	Origin          json.RawMessage `json:"origin"`
}

// sendPreCheck is the pre-check's reading of one frame (spec 4.3.2 field
// rules). Only valid values are kept for echoes.
type sendPreCheck struct {
	frameType     string
	target        string
	targetPresent bool
	targetValid   bool
	corr          string
	corrPresent   bool
	corrValid     bool
	originPresent bool
	origin        *generated.SendOrigin // nil unless present AND valid
}

// boundedRawString reads a raw JSON value that must be a string of 1..max
// characters.
func boundedRawString(raw json.RawMessage, maxChars int) (value string, present, valid bool) {
	if len(raw) == 0 {
		return "", false, false
	}
	if err := json.Unmarshal(raw, &value); err != nil || raw[0] != '"' {
		return "", true, false
	}
	n := utf8.RuneCountInString(value)
	return value, true, n >= 1 && n <= maxChars
}

// parseSendOrigin applies the SendOrigin rules (C-1): an object with only
// kind, session_id and new_chat_key; kind chat carries exactly a session_id of
// 1..255 characters, kind new_chat exactly a new_chat_key of 16..128
// characters of [A-Za-z0-9_-]. JSON null is malformed (null is not absent).
func parseSendOrigin(raw json.RawMessage) (*generated.SendOrigin, bool) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, false
	}
	for name := range fields {
		if name != "kind" && name != "session_id" && name != "new_chat_key" {
			return nil, false
		}
	}
	kind, _, kindOK := boundedRawString(fields["kind"], 16)
	if !kindOK {
		return nil, false
	}
	_, hasSession := fields["session_id"]
	_, hasKey := fields["new_chat_key"]
	switch kind {
	case "chat":
		sid, _, ok := boundedRawString(fields["session_id"], sendTargetMaxChars)
		if !ok || hasKey {
			return nil, false
		}
		return &generated.SendOrigin{Kind: kind, SessionId: &sid}, true
	case "new_chat":
		key, _, ok := boundedRawString(fields["new_chat_key"], newChatKeyMaxChars)
		if !ok || hasSession || !validNewChatKey(key) {
			return nil, false
		}
		return &generated.SendOrigin{Kind: kind, NewChatKey: &key}, true
	default:
		return nil, false
	}
}

// decodeSendPreCheck reads the target, the frame type's correlation id and
// the origin from the raw frame (spec 4.3.2 field rules). The other type's id
// field is not read (RR-9).
func decodeSendPreCheck(data []byte, frameType string) sendPreCheck {
	var raw sendRawFields
	// The read loop's peek already decoded this frame as a JSON object, so
	// this lenient decode cannot fail on field types; a failure leaves every
	// field absent, which the rules below treat as such.
	_ = json.Unmarshal(data, &raw)
	pc := sendPreCheck{frameType: frameType}
	pc.target, pc.targetPresent, pc.targetValid = boundedRawString(raw.SessionID, sendTargetMaxChars)
	corrRaw := raw.ClientFrameID
	if frameType == string(generated.WsFrameTypeMessage) {
		corrRaw = raw.ClientMessageID
	}
	pc.corr, pc.corrPresent, pc.corrValid = boundedRawString(corrRaw, sendCorrelationMaxChars)
	if len(raw.Origin) > 0 {
		pc.originPresent = true
		if origin, ok := parseSendOrigin(raw.Origin); ok {
			pc.origin = origin
		}
	}
	return pc
}

// isCommand reports whether the frame is a redirect or a cancel.
func (pc sendPreCheck) isCommand() bool {
	return pc.frameType != string(generated.WsFrameTypeMessage)
}

// fieldsMalformed is the P1b condition: a present target or correlation id
// breaks its bounds, or a command has no target.
func (pc sendPreCheck) fieldsMalformed() bool {
	if pc.targetPresent && !pc.targetValid {
		return true
	}
	if !pc.targetPresent && pc.isCommand() {
		return true
	}
	return pc.corrPresent && !pc.corrValid
}

// sendOriginMetaKey reads the saved new-chat key of a chat. missing reports
// that there is no such chat; err is a real read failure.
type sendOriginMetaKey func(sessionID string) (key string, missing bool, err error)

// checkSendOrigin is spec 4.3.1 for a frame whose origin is present and valid
// and whose fields passed the pre-check. It returns "" to accept, or the
// refusal reason.
func checkSendOrigin(pc sendPreCheck, metaKey sendOriginMetaKey) string {
	origin := pc.origin
	if origin.Kind == "chat" {
		if pc.target == *origin.SessionId {
			return ""
		}
		return sendRefusedWrongChat
	}
	// kind new_chat
	if pc.target == "" {
		// Only a message can be target-less (a first message): the created
		// chat gets the key.
		return ""
	}
	key, missing, err := metaKey(pc.target)
	switch {
	case err != nil:
		return sendRefusedOriginUnverify
	case missing || key == "" || key != *origin.NewChatKey:
		return sendRefusedWrongChat
	default:
		return ""
	}
}

// sessionNewChatKey is the production sendOriginMetaKey: it reads the chat's
// metadata from the shared session store.
func (h *WSHandler) sessionNewChatKey(sessionID string) (string, bool, error) {
	if !isStorableChatID(sessionID) {
		return "", true, nil // not a storable chat id: no such chat
	}
	// The one shared store, not resolveSessionStore: that returns nil for an
	// unknown chat, which is wrong_chat here, not origin_unverified.
	var store *session.UnifiedStore
	if h.agentLoop != nil {
		store = h.agentLoop.GetSessionStore()
	}
	if store == nil {
		return "", false, errors.New("session store unavailable")
	}
	meta, err := store.GetMeta(sessionID)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", true, nil
		}
		return "", false, err
	}
	if meta == nil {
		return "", true, nil
	}
	return meta.NewChatKey, false, nil
}

// runSendOriginPreCheck is the raw pre-check of spec 4.3.2 for message,
// redirect and cancel. It returns true when the frame was answered here and
// must not be processed further (P1a, P1b, P1c, P2b); false lets today's path
// (P3) run.
func (wh *wsHandlerReadLoop) runSendOriginPreCheck(data []byte, frameType string) bool {
	pc := decodeSendPreCheck(data, frameType)
	h, wc := wh.h, wh.wc
	if !pc.originPresent {
		if !wc.browser {
			_wsOriginLessAccepted.Add(1) // P2: legacy, accepted and counted
			return false
		}
		h.replyOldTab(wc, wh.chatID, pc) // P2b
		return true
	}
	switch {
	case pc.origin == nil: // P1a
		h.refuseSend(wc, wh.chatID, pc, sendRefusedOriginMalformed)
		return true
	case pc.fieldsMalformed(): // P1b
		h.refuseSend(wc, wh.chatID, pc, sendRefusedFrameMalformed)
		return true
	}
	if reason := checkSendOrigin(pc, h.sessionNewChatKey); reason != "" { // P1c
		h.refuseSend(wc, wh.chatID, pc, reason)
		if pc.isCommand() {
			h.sendRefusalSnapshot(wc, pc.target)
		}
		return true
	}
	return false // P1d
}

// refuseSend enqueues one send_refused to the sending connection, unsequenced
// (S-4), echoing only valid fields (C-4), and writes the content-free log line
// (spec 4.3.8).
func (h *WSHandler) refuseSend(wc *wsConn, chatID string, pc sendPreCheck, reason string) {
	frame := generated.SendRefusedFrame{
		Type:         string(generated.WsFrameTypeSendRefused),
		Reason:       reason,
		RefusedFrame: pc.frameType,
	}
	if pc.targetValid {
		target := pc.target
		frame.SessionId = &target
	}
	if pc.corrValid {
		corr := pc.corr
		if pc.isCommand() {
			frame.ClientFrameId = &corr
		} else {
			frame.ClientMessageId = &corr
		}
	}
	originKind, targetID := "", ""
	if pc.targetValid {
		targetID = pc.target
	}
	if reason != sendRefusedOriginMalformed && pc.origin != nil {
		frame.Origin = pc.origin
		originKind = pc.origin.Kind
	}
	slog.Info("ws: send refused",
		"event", "send_refused", "reason", reason, "frame_type", pc.frameType,
		"origin_kind", originKind, "chat_id", chatID, "target_id", targetID)
	sendConnGenFrame(wc, string(generated.WsFrameTypeSendRefused), frame)
}

// outdatedClientMessageID is "outdated-" plus 12 random URL-safe characters
// (spec 4.3.4).
func outdatedClientMessageID() string {
	var b [9]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand does not fail on supported platforms; a fixed suffix
		// still never matches a real pending id (F-q3).
		slog.Warn("ws: random suffix for an outdated notice failed", "error", err)
		return sendOriginOutdatedIDPrefix + "000000000000"
	}
	return sendOriginOutdatedIDPrefix + base64.RawURLEncoding.EncodeToString(b[:])
}

// replyOldTab answers an origin-less frame on a browser connection with the
// shapes an old page already handles without ending a turn (spec 4.3.4).
// Unroutable frames get no reply at all.
func (h *WSHandler) replyOldTab(wc *wsConn, chatID string, pc sendPreCheck) {
	if !pc.targetValid {
		if !pc.targetPresent && !pc.isCommand() && pc.corrValid {
			// First message from an old page: the not-saved first-send state.
			corr, kind := pc.corr, "not_saved"
			slog.Info("ws: send refused", "event", "send_refused_outdated", "reason", "origin_missing",
				"frame_type", pc.frameType, "origin_kind", "", "chat_id", chatID, "target_id", "")
			sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
				Type: string(generated.WsFrameTypeError), ClientMessageId: &corr,
				FirstMessageError: &kind, Message: sendOriginOutdated,
			})
			return
		}
		// Present-but-malformed target, or no target and no usable id, or a
		// command without a valid target: any reply could end an unrelated
		// turn on the old page (F-q2), so none is sent.
		_wsInboundFrameDropped.Add(1)
		wc.inboundDropped.Add(1)
		slog.Info("ws: origin-less browser frame dropped", "event", "origin_less_unroutable",
			"frame_type", pc.frameType, "chat_id", chatID)
		return
	}
	target := pc.target
	message := sendOriginOutdated
	switch pc.frameType {
	case string(generated.WsFrameTypeRedirect):
		message = sendOriginOutdatedRedirect
	case string(generated.WsFrameTypeCancel):
		message = sendOriginOutdatedStop
	}
	corr := outdatedClientMessageID()
	realID := !pc.isCommand() && pc.corrValid
	if realID {
		corr = pc.corr
	}
	slog.Info("ws: send refused", "event", "send_refused_outdated", "reason", "origin_missing",
		"frame_type", pc.frameType, "origin_kind", "", "chat_id", chatID, "target_id", target)
	sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
		Type: string(generated.WsFrameTypeError), SessionId: &target,
		ClientMessageId: &corr, Message: message,
	})
	if realID {
		sendConnGenFrame(wc, string(generated.WsFrameTypeMessageStatus), generated.MessageStatusFrame{
			Type: string(generated.WsFrameTypeMessageStatus), SessionId: target,
			ClientMessageId: corr, State: "failed",
		})
	}
	if pc.isCommand() {
		h.sendRefusalSnapshot(wc, target)
	}
}

// sendCommandReceived enqueues command_received (C-10, S-12) for an accepted
// redirect or cancel that carried a valid client_frame_id. Called after the
// generated decode succeeded and immediately before the handler acts, from
// the read loop, so it precedes every effect frame of that command on this
// connection.
func sendCommandReceived(wc *wsConn, command string, clientFrameID *string, sessionID string) {
	if clientFrameID == nil || sessionID == "" {
		return
	}
	if n := utf8.RuneCountInString(*clientFrameID); n < 1 || n > sendCorrelationMaxChars {
		return
	}
	sendConnGenFrame(wc, string(generated.WsFrameTypeCommandReceived), generated.CommandReceivedFrame{
		Type:          string(generated.WsFrameTypeCommandReceived),
		Command:       command,
		ClientFrameId: *clientFrameID,
		SessionId:     sessionID,
	})
}

// sendRefusalSnapshot sends one session_state for target after a refused
// redirect/cancel, inside the ordering boundary R2-R6 of spec 4.3.4, so the
// snapshot can never report a turn whose done was delivered before it (S-14).
// The refusal reply (R1) was already enqueued by the caller. Runs in the read
// loop goroutine (the attach path's goroutine, so no attach overlaps). The
// connection's binding is never changed.
func (h *WSHandler) sendRefusalSnapshot(wc *wsConn, target string) {
	// R2: done reaches bound connections only (F-rr); a snapshot on an
	// unbound connection could never be closed.
	if !h.connBoundToSession(wc, target) {
		return
	}
	if hook := refusalSnapshotBeforeHoldHook; hook != nil {
		hook(target)
	}
	wc.startHold() // R3
	// R6 on every exit: holding is false under qmu afterwards.
	defer wc.releaseHold()
	raw := refusalSnapshotBuild(h, wc, target) // R4: sampled strictly after R3
	if hook := refusalSnapshotAfterSampleHook; hook != nil {
		hook(target)
	}
	if raw == nil {
		slog.Warn("ws: refusal snapshot not built; no snapshot sent", "session_id", target)
		return
	}
	// R5: ahead of everything held, never through enqueue (which would hold
	// it behind a done published during the hold).
	if !wc.direct(raw) {
		slog.Debug("ws: refusal snapshot not queued — connection closing", "session_id", target)
	}
}

// isSendOriginFrameType reports whether frameType is one of the three frames
// the raw pre-check covers (spec 4.3.2).
func isSendOriginFrameType(frameType string) bool {
	switch frameType {
	case string(generated.WsFrameTypeMessage), string(generated.WsFrameTypeRedirect), string(generated.WsFrameTypeCancel):
		return true
	}
	return false
}

// validNewChatKey applies the SendOrigin.new_chat_key bounds (C-1): 16..128
// characters of [A-Za-z0-9_-].
func validNewChatKey(key string) bool {
	n := utf8.RuneCountInString(key)
	return n >= newChatKeyMinChars && n <= newChatKeyMaxChars && newChatKeyPattern.MatchString(key)
}

// isStorableChatID reports whether id can name a stored chat at all.
func isStorableChatID(id string) bool {
	return validateEntityID(id) == nil
}
