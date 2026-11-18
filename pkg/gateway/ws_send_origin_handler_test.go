package gateway

// ws_send_origin_handler_test.go — RED (#1428 step 2 option C, qa-lead): the
// handler-level scenarios SV-1..SV-5, SV-13, SV-17, SV-17b, SV-18 and the
// refusal-snapshot family SV-19 (a)-(h), (j), (k) + control + the cleanup
// postcondition, on the REAL WS wire with the shared sendOriginSrv harness.
// SV-19's helpers live in ws_send_origin_snapshot_test.go; SV-19i is in pkg/agent.
//
// SPEC SOURCES (expected values derive from these, never from the
// implementation): §6.1 SV-1..SV-5, SV-13, SV-17, SV-17b, SV-18, SV-19;
// §4.3.4 (old-tab reply table and the exact OUTDATED texts); §4.3.2 P1a/P1b;
// §4.3.1. Contract commit 32802e2a6: SendRefusedFrame, CommandReceivedFrame,
// ErrorFrame.
//
// Original RED: pre-check, receipts and ordering seams were absent. Fix round 4
// wires the delivered R3/R4 hooks, nil builder and real closing-queue failure;
// no BLOCKED leg is skipped or deleted. The accepted-cancel control remains a
// guard. Saved failing runs establish the fixture repairs; implementation
// mutation and product GREEN remain the independent CHECK instance's duties.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// The exact §4.3.4 texts (chief's wording; full-string equality).
const (
	soOutdated        = "This page is out of date. Copy anything you typed, then reload the page."
	soOutdatedRedir   = "Not stopped. Reload this page. Your instruction was not sent and must be typed again."
	soOutdatedStop    = "Not stopped. Reload this page."
	soOldTabIDPrefix  = "outdated-"
	soRequesterWindow = 4 * time.Second
)

// soErrors decodes the window's error frames (the 4.3.4 reply carrier).
func soErrors(t *testing.T, frames []soFrame) []generated.ErrorFrame {
	t.Helper()
	var out []generated.ErrorFrame
	for _, f := range frames {
		if f.Type == "error" {
			var ef generated.ErrorFrame
			require.NoError(t, json.Unmarshal(f.Raw, &ef), "error frame must decode")
			out = append(out, ef)
		}
	}
	return out
}

// soLastSessionState returns the window's last session_state raw bytes (the
// refusal snapshot is the one that follows the refusal).
func soLastSessionState(t *testing.T, frames []soFrame) map[string]any {
	t.Helper()
	for i := len(frames) - 1; i >= 0; i-- {
		if frames[i].Type == "session_state" {
			var st map[string]any
			require.NoError(t, json.Unmarshal(frames[i].Raw, &st), "session_state must decode")
			return st
		}
	}
	return nil
}

// soHolding reads the connection's holding flag under qmu — the SV-19
// cleanup postcondition oracle (a queuedFrames()==0 read is NOT accepted).
func soHolding(t *testing.T, s *sendOriginSrv, conn *websocket.Conn) bool {
	t.Helper()
	found := soServerConn(t, s, conn)
	found.qmu.Lock()
	defer found.qmu.Unlock()
	return found.holding
}

// soLiveFrameDelivered injects one uniquely identifiable live user-message
// frame at the real hub input (SV-19 cleanup), then reads it on the socket.
// No second turn/binding/map entry can stand in for this connection's queue.
func soLiveFrameDelivered(t *testing.T, s *sendOriginSrv, conn *websocket.Conn, chatID, phase string) {
	t.Helper()
	wc := soRequireBound(t, s, conn, chatID)
	require.False(t, soHolding(t, s, conn), "%s: holding must be false under qmu after refusal handling", phase)
	marker := "live frame for " + phase
	raw, err := json.Marshal(generated.UserMessageFrame{Type: "user_message", SessionId: chatID, Content: marker, Id: "so-live-" + phase})
	require.NoError(t, err)
	s.handler.hubs.getOrCreate(chatID).publish(raw)
	frames := soWindow(t, conn, soRequesterWindow)
	var observed []generated.UserMessageFrame
	for _, frame := range frames {
		if frame.Type == "user_message" {
			var echo generated.UserMessageFrame
			require.NoError(t, json.Unmarshal(frame.Raw, &echo))
			if echo.Id == "so-live-"+phase {
				observed = append(observed, echo)
			}
		}
	}
	require.Len(t, observed, 1, "%s: the exact live hub frame must be delivered, never left held (window: %v)", phase, soTypes(frames))
	require.Equal(t, marker, observed[0].Content)
	require.Equal(t, chatID, observed[0].SessionId)
	require.True(t, s.handler.connBoundToSession(wc, chatID), "%s: binding must remain unchanged", phase)
	require.False(t, soHolding(t, s, conn), "%s: cleanup must not leave holding set", phase)
}

// TestSendOriginHandler_SV1_MismatchRefused_NoSideEffects is SV-1: browser
// connection, unbound; message{B, origin{chat,A}, m1} → send_refused with the
// echoes and zero side effects. Control: origin B is accepted.
func TestSendOriginHandler_SV1_MismatchRefused_NoSideEffects(t *testing.T) {
	s := newSendOriginSrv(t, &restMockProvider{})
	mintConn := s.dialBrowserConn(t)
	drainAdmissionOneShot(t, mintConn)
	b := s.mintChat(t, mintConn, soKeyB, "SV-1 mint")
	// SV-1 explicitly starts unbound (MU-13 must catch an early adopt).
	conn := s.dialBrowserConn(t)
	drainAdmissionOneShot(t, conn)
	wc := soServerConn(t, s, conn)
	s.handler.mu.Lock()
	initialBinding := wc.boundHub
	s.handler.mu.Unlock()
	require.Nil(t, initialBinding, "SV-1 SETUP: requester has no hub binding")
	before := s.transcriptLen(t, b)

	soSend(t, conn, soMessage(&b, soChatOrigin(soKeyA[len(soKeyA)-4:]), "so-sv1-m1", "borrowed chat origin"))
	frames := soWindow(t, conn, soRequesterWindow)
	refused := soRefusedFrame(t, frames, "SV-1")
	require.Equal(t, "wrong_chat", refused.Reason)
	require.Equal(t, "message", refused.RefusedFrame)
	require.NotNil(t, refused.SessionId)
	require.Equal(t, b, *refused.SessionId)
	require.NotNil(t, refused.ClientMessageId)
	require.Equal(t, "so-sv1-m1", *refused.ClientMessageId)
	require.Equal(t, 0, s.transcriptLen(t, b)-before, "SV-1: B's transcript unchanged")
	require.NotContains(t, soTypes(frames), "message_status", "SV-1: no message_status for the refusal")
	require.NotContains(t, soTypes(frames), "user_message", "SV-1: no echo for the refusal")
	s.handler.mu.Lock()
	afterBinding := wc.boundHub
	s.handler.mu.Unlock()
	require.Nil(t, afterBinding, "SV-1/MU-13: refused input must not adopt the target's hub")

	// Control (performs the action): origin B on the same connection.
	soSend(t, conn, soMessage(&b, soChatOrigin(b), "so-sv1-ctrl", "matching origin control"))
	frames = soWindow(t, conn, soRequesterWindow, "message_status")
	require.Contains(t, soTypes(frames), "message_status", "SV-1 control: origin B is accepted")
}

// TestSendOriginHandler_SV2_RedirectRefused_ThenControlledReceipt is SV-2 and
// SV-17's ordering oracle in one connection: mismatched redirect refused
// (send_refused{redirect, r1}), then the matching redirect accepted
// (command_received{redirect, r1, B}); a redirect WITHOUT client_frame_id
// gets no receipt.
func TestSendOriginHandler_SV2_RedirectRefused_ThenControlledReceipt(t *testing.T) {
	s := newSendOriginSrv(t, &restMockProvider{})
	conn := s.dialBrowserConn(t)
	drainAdmissionOneShot(t, conn)
	b := s.mintChat(t, conn, soKeyB, "SV-2 mint")
	a := s.mintChat(t, conn, soKeyA, "SV-2 other")

	// Mismatch: origin A against target B.
	soSend(t, conn, generated.RedirectFrame{Type: "redirect", SessionId: b, Instruction: "sv2 redirected instruction",
		Origin: soChatOrigin(a), ClientFrameId: strPtr("so-r1")})
	frames := soWindow(t, conn, soRequesterWindow)
	refused := soRefusedFrame(t, frames, "SV-2 mismatched redirect")
	require.Equal(t, "redirect", refused.RefusedFrame)
	require.NotNil(t, refused.ClientFrameId)
	require.Equal(t, "so-r1", *refused.ClientFrameId)
	require.NotContains(t, soTypes(frames), "command_received", "SV-2: refused redirect gets no receipt")

	// Matching: origin B — accepted, receipt first (SV-17 send order).
	soSend(t, conn, generated.RedirectFrame{Type: "redirect", SessionId: b, Instruction: "sv2 matching instruction",
		Origin: soChatOrigin(b), ClientFrameId: strPtr("so-r2")})
	frames = soWindow(t, conn, soRequesterWindow)
	idxReceipt := indexOf(soTypes(frames), "command_received")
	require.Greater(t, idxReceipt, -1, "SV-2 control: the accepted redirect must get command_received (window: %v)", soTypes(frames))
	var receipt generated.CommandReceivedFrame
	require.NoError(t, json.Unmarshal(frames[idxReceipt].Raw, &receipt))
	require.Equal(t, "redirect", receipt.Command)
	require.Equal(t, "so-r2", receipt.ClientFrameId)
	require.Equal(t, b, receipt.SessionId)

	// SV-17's no-id leg: origin-less redirect (also old-tab shape) gets NO receipt.
	soSend(t, conn, generated.RedirectFrame{Type: "redirect", SessionId: b, Instruction: "no id"})
	frames = soWindow(t, conn, soRequesterWindow)
	for _, f := range frames {
		require.NotEqual(t, "command_received", f.Type,
			"SV-17: a redirect without client_frame_id never gets a receipt (got %s)", f.Raw)
	}
}

// TestSendOriginHandler_SV3_CancelRefused_ThenControlledReceipt is SV-3: as
// SV-2 for cancel; the refused cancel must produce no cancel_stage, and the
// accepted control gets its receipt before the stage frames.
func TestSendOriginHandler_SV3_CancelRefused_ThenControlledReceipt(t *testing.T) {
	s := newSendOriginSrv(t, &restMockProvider{})
	conn := s.dialBrowserConn(t)
	drainAdmissionOneShot(t, conn)
	b := s.mintChat(t, conn, soKeyB, "SV-3 mint")
	a := s.mintChat(t, conn, soKeyA, "SV-3 other")

	soSend(t, conn, generated.CancelFrame{Type: "cancel", SessionId: b, Origin: soChatOrigin(a), ClientFrameId: strPtr("so-c1")})
	frames := soWindow(t, conn, soRequesterWindow)
	refused := soRefusedFrame(t, frames, "SV-3 mismatched cancel")
	require.Equal(t, "cancel", refused.RefusedFrame)
	require.Equal(t, "wrong_chat", refused.Reason)
	require.NotContains(t, soTypes(frames), "cancel_stage", "SV-3: the refused cancel must not stage")
	require.NotContains(t, soTypes(frames), "command_received", "SV-3: the refused cancel gets no receipt")

	soSend(t, conn, generated.CancelFrame{Type: "cancel", SessionId: b, Origin: soChatOrigin(b), ClientFrameId: strPtr("so-c2")})
	frames = soWindow(t, conn, soRequesterWindow)
	require.Contains(t, soTypes(frames), "command_received", "SV-3 control: accepted cancel gets its receipt")
	require.NotContains(t, soTypes(frames), "send_refused", "SV-3 control: the accepted cancel is not refused")
}

// TestSendOriginHandler_SV4_MissingOriginBrowserReplies drives the §4.3.4
// old-tab table: every row's reply shape and exact text, on a browser
// connection.
func TestSendOriginHandler_SV4_MissingOriginBrowserReplies(t *testing.T) {
	t.Run("message with valid target and id", func(t *testing.T) {
		s := newSendOriginSrv(t, &restMockProvider{})
		conn := s.dialBrowserConn(t)
		drainAdmissionOneShot(t, conn)
		b := s.mintChat(t, conn, soKeyB, "SV-4 mint")
		soSend(t, conn, soMessage(&b, nil, "so-sv4-m1", "old tab send"))
		frames := soWindow(t, conn, soRequesterWindow)
		errs := soErrors(t, frames)
		require.NotEmpty(t, errs, "SV-4: the old-tab message must get the OUTDATED error (window: %v)", soTypes(frames))
		require.NotNil(t, errs[0].SessionId, "old-tab refusal must name its exact target")
		require.Equal(t, b, *errs[0].SessionId, "SV-4: the error must carry session_id")
		require.NotNil(t, errs[0].ClientMessageId)
		require.Equal(t, "so-sv4-m1", *errs[0].ClientMessageId)
		require.Equal(t, soOutdated, errs[0].Message, "SV-4: the OUTDATED text is exact")
		require.Contains(t, soTypes(frames), "message_status",
			"SV-4: the old-tab message reply includes message_status{failed} for that id")
	})

	t.Run("first message not_saved", func(t *testing.T) {
		s := newSendOriginSrv(t, &restMockProvider{})
		conn := s.dialBrowserConn(t)
		drainAdmissionOneShot(t, conn)
		soSend(t, conn, soMessage(nil, nil, "so-sv4-first", "old tab first send"))
		frames := soWindow(t, conn, soRequesterWindow)
		errs := soErrors(t, frames)
		require.NotEmpty(t, errs, "SV-4: the old-tab first message must get the not_saved reply (window: %v)", soTypes(frames))
		require.Nil(t, errs[0].SessionId, "SV-4: the first-message reply carries no session_id")
		require.NotNil(t, errs[0].ClientMessageId)
		require.Equal(t, "so-sv4-first", *errs[0].ClientMessageId)
		require.Equal(t, soOutdated, errs[0].Message)
		require.NotContains(t, soTypes(frames), "session_started",
			"SV-4: nothing is saved for the refused old-tab first message")
	})

	t.Run("redirect old-tab text and snapshot", func(t *testing.T) {
		s := newSendOriginSrv(t, &restMockProvider{})
		conn := s.dialBrowserConn(t)
		drainAdmissionOneShot(t, conn)
		b := s.mintChat(t, conn, soKeyB, "SV-4 redirect mint")
		soSend(t, conn, generated.RedirectFrame{Type: "redirect", SessionId: b, Instruction: "old tab redirect"})
		frames := soWindow(t, conn, soRequesterWindow)
		errs := soErrors(t, frames)
		require.NotEmpty(t, errs, "SV-4: the old-tab redirect must get its error (window: %v)", soTypes(frames))
		require.NotNil(t, errs[0].SessionId, "old-tab refusal must name its exact target")
		require.Equal(t, b, *errs[0].SessionId)
		require.NotNil(t, errs[0].ClientMessageId)
		require.True(t, strings.HasPrefix(*errs[0].ClientMessageId, soOldTabIDPrefix) && len(*errs[0].ClientMessageId) == len(soOldTabIDPrefix)+12,
			"SV-4: the redirect's synthetic id is \"outdated-\" + 12 chars, got %q", *errs[0].ClientMessageId)
		require.Equal(t, soOutdatedRedir, errs[0].Message, "SV-4: OUTDATED_REDIRECT text is exact")
		st := soLastSessionState(t, frames)
		require.NotNil(t, st, "SV-4: the refusal snapshot (session_state) must follow the redirect refusal")
	})

	t.Run("cancel old-tab text and snapshot", func(t *testing.T) {
		s := newSendOriginSrv(t, &restMockProvider{})
		conn := s.dialBrowserConn(t)
		drainAdmissionOneShot(t, conn)
		b := s.mintChat(t, conn, soKeyB, "SV-4 cancel mint")
		soSend(t, conn, generated.CancelFrame{Type: "cancel", SessionId: b})
		frames := soWindow(t, conn, soRequesterWindow)
		errs := soErrors(t, frames)
		require.NotEmpty(t, errs, "SV-4: the old-tab cancel must get its error (window: %v)", soTypes(frames))
		require.Equal(t, soOutdatedStop, errs[0].Message, "SV-4: OUTDATED_STOP text is exact")
		require.NotNil(t, soLastSessionState(t, frames), "SV-4: the refusal snapshot must follow")
	})

	t.Run("message with no valid id gets synthetic-id error only", func(t *testing.T) {
		s := newSendOriginSrv(t, &restMockProvider{})
		conn := s.dialBrowserConn(t)
		drainAdmissionOneShot(t, conn)
		b := s.mintChat(t, conn, soKeyB, "SV-4 no-id mint")
		raw := []byte(`{"type":"message","agent_id":"mia","session_id":"` + b + `","content":"no id"}`)
		soSend(t, conn, raw)
		frames := soWindow(t, conn, soRequesterWindow)
		errs := soErrors(t, frames)
		require.NotEmpty(t, errs, "SV-4: the no-id old-tab message must still get its notice (window: %v)", soTypes(frames))
		require.NotNil(t, errs[0].ClientMessageId, "SV-4: the synthetic id is present")
		require.Equal(t, soOutdated, errs[0].Message)
		require.NotContains(t, soTypes(frames), "message_status",
			"SV-4: no message_status — there is no id to reconcile")
	})

	t.Run("unroutable rows get silence and a drop", func(t *testing.T) {
		s := newSendOriginSrv(t, &restMockProvider{})
		conn := s.dialBrowserConn(t)
		drainAdmissionOneShot(t, conn)
		_ = s.mintChat(t, conn, soKeyB, "SV-4 unroutable mint")
		// M-8b: present-but-malformed target → NO reply at all.
		soSend(t, conn, []byte(`{"type":"message","agent_id":"mia","session_id":7,"client_message_id":"so-m8b","content":"x"}`))
		// M-9: absent target and no valid id.
		soSend(t, conn, []byte(`{"type":"message","agent_id":"mia","content":"no target no id"}`))
		frames := soWindow(t, conn, 2*time.Second)
		require.Empty(t, frames, "M-8b/M-9: an unroutable old-tab frame gets ZERO replies (got %v)", soTypes(frames))
	})
}

// TestSendOriginHandler_SV13_RecipientMismatchRefusedBeforeRequest is SV-13:
// a mismatched origin with a recipient is refused before any recipient
// request — the refusal arrives and no recipient side effect happens.
func TestSendOriginHandler_SV13_RecipientMismatchRefusedBeforeRequest(t *testing.T) {
	s := newSendOriginSrv(t, &restMockProvider{})
	conn := s.dialBrowserConn(t)
	drainAdmissionOneShot(t, conn)
	b := s.mintChat(t, conn, soKeyB, "SV-13 mint") // drains its own done before the refusal interval
	before := s.transcriptLen(t, b)
	raw := []byte(`{"type":"message","agent_id":"mia","session_id":"` + b +
		`","client_message_id":"so-sv13","origin":{"kind":"chat","session_id":"so-other-chat"},"content":"x",` +
		`"recipient":{"agent_id":"mia"}}`)
	var sent generated.MessageFrame
	require.NoError(t, json.Unmarshal(raw, &sent), "SV-13 SETUP: exact outbound bytes must be valid JSON before send")
	require.Equal(t, &b, sent.SessionId, "SV-13 SETUP: the outbound target is the minted chat")
	require.Equal(t, soChatOrigin("so-other-chat"), sent.Origin, "SV-13 SETUP: the outbound origin mismatches the target")
	require.NotNil(t, sent.Recipient, "SV-13 SETUP: the outbound frame must exercise the recipient branch")
	require.Equal(t, "mia", sent.Recipient.AgentId)
	t.Logf("SV-13 SETUP: mint done drained; exact outbound JSON validated: %s", raw)
	soSend(t, conn, raw)
	frames := soWindow(t, conn, soRequesterWindow)
	for _, f := range frames {
		t.Logf("SV-13 response: %s", f.Raw)
	}
	// Spec r9 §6.1 SV-13 and §5 S-2: refuse the origin mismatch before
	// recipient handling or any side effect, including message_status.
	require.Equal(t, []string{"send_refused"}, soTypes(frames), "SV-13: only send_refused may precede recipient handling")
	refused := soRefusedFrame(t, frames, "SV-13")
	require.Equal(t, "wrong_chat", refused.Reason)
	require.NotNil(t, refused.ClientMessageId)
	require.Equal(t, "so-sv13", *refused.ClientMessageId)
	require.Equal(t, "message", refused.RefusedFrame)
	require.Equal(t, &b, refused.SessionId)
	require.Equal(t, sent.Origin, refused.Origin)
	require.Nil(t, refused.ClientFrameId)
	require.NotContains(t, soTypes(frames), "user_message", "SV-13: the refused frame never reaches a recipient")
	require.Equal(t, before, s.transcriptLen(t, b), "SV-13: the refused recipient frame must not change the transcript")
}

// TestSendOriginHandler_SV17b_OldTabShapesNeverGetReceipt is SV-17b.
func TestSendOriginHandler_SV17b_OldTabShapesNeverGetReceipt(t *testing.T) {
	s := newSendOriginSrv(t, &restMockProvider{})
	conn := s.dialBrowserConn(t)
	drainAdmissionOneShot(t, conn)
	b := s.mintChat(t, conn, soKeyB, "SV-17b mint")

	soSend(t, conn, generated.RedirectFrame{Type: "redirect", SessionId: b, Instruction: "old tab no id"})
	frames := soWindow(t, conn, soRequesterWindow)
	require.NotEmpty(t, soErrors(t, frames), "SV-17b: the old-tab redirect still gets its 4.3.4 reply")
	for _, f := range frames {
		require.NotEqual(t, "command_received", f.Type, "SV-17b: zero receipts for old-tab shapes (got %s)", f.Raw)
	}
	soSend(t, conn, generated.CancelFrame{Type: "cancel", SessionId: b})
	frames = soWindow(t, conn, soRequesterWindow)
	require.NotEmpty(t, soErrors(t, frames), "SV-17b: the old-tab cancel still gets its 4.3.4 reply")
	require.NotNil(t, soLastSessionState(t, frames), "SV-17b: the refusal snapshot follows")
	for _, f := range frames {
		require.NotEqual(t, "command_received", f.Type, "SV-17b: zero receipts for old-tab shapes (got %s)", f.Raw)
	}
}

// TestSendOriginHandler_SV18_ReceiptObligationTable is SV-18: every accepted
// command shape (redirect; cancel session scope; cancel tree scope) carrying
// a valid client_frame_id gets EXACTLY ONE command_received naming it,
// before any effect frame.
func TestSendOriginHandler_SV18_ReceiptObligationTable(t *testing.T) {
	tree := "tree"
	cases := []struct {
		name string
		send func(sessionID string) any
	}{
		{"redirect", func(sid string) any {
			return generated.RedirectFrame{Type: "redirect", SessionId: sid, Instruction: "sv18", Origin: soChatOrigin(sid), ClientFrameId: strPtr("sv18-r")}
		}},
		{"cancel session scope", func(sid string) any {
			return generated.CancelFrame{Type: "cancel", SessionId: sid, Origin: soChatOrigin(sid), ClientFrameId: strPtr("sv18-cs")}
		}},
		{"cancel tree scope", func(sid string) any {
			return generated.CancelFrame{Type: "cancel", SessionId: sid, Scope: &tree, Origin: soChatOrigin(sid), ClientFrameId: strPtr("sv18-ct")}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newSendOriginSrv(t, &restMockProvider{})
			conn := s.dialBrowserConn(t)
			drainAdmissionOneShot(t, conn)
			b := s.mintChat(t, conn, soKeyB, "SV-18 mint "+tc.name)
			soSend(t, conn, tc.send(b))
			frames := soWindow(t, conn, soRequesterWindow)
			receipts := 0
			receiptIdx := -1
			effectIdx := -1
			for i, f := range frames {
				switch f.Type {
				case "command_received":
					receipts++
					receiptIdx = i
				case "cancel_stage", "session_started", "done":
					if effectIdx < 0 {
						effectIdx = i
					}
				}
			}
			require.Equal(t, 1, receipts,
				"SV-18: exactly one command_received for the accepted %s (window: %v)", tc.name, soTypes(frames))
			var receipt generated.CommandReceivedFrame
			require.NoError(t, json.Unmarshal(frames[receiptIdx].Raw, &receipt))
			require.Equal(t, b, receipt.SessionId, "SV-18: the receipt names the target")
			if effectIdx >= 0 {
				require.Less(t, receiptIdx, effectIdx,
					"SV-18: the receipt precedes any effect frame (window: %v)", soTypes(frames))
			}
		})
	}
}

// TestSendOriginHandler_SV19_RefusalSnapshot drives SV-19 (a)-(d), (g), (h)
// + control + cleanup; (e)/(f)/(j)/(k) are the seam legs the GREEN hook must
// unlock.
func TestSendOriginHandler_SV19_RefusalSnapshot(t *testing.T) {
	t.Run("a old-tab cancel snapshot keeps active_turn", soSV19A)
	t.Run("b old-tab redirect text", soSV19B)
	t.Run("c new-tab wrong_chat cancel gets receipt then snapshot", soSV19C)
	t.Run("d finished turn snapshot without active_turn", soSV19D)
	t.Run("g unbound connection gets refusal only", soSV19G)
	t.Run("h P1a and P1b get zero snapshots", soSV19H)
	t.Run("control accepted cancel gets no snapshot from this rule", soSV19Control)
	t.Run("e decisive ordering hook after R4", soSV19E)
	t.Run("f decisive ordering hook before R3", soSV19F)
	t.Run("j builder forced nil", soSV19J)
	t.Run("k direct failing on closing connection", soSV19K)
}

func indexOf(haystack []string, needle string) int {
	for i, s := range haystack {
		if s == needle {
			return i
		}
	}
	return -1
}
