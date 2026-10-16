// Omnipus — RED payload #1343/#1329 round1, file 4 of the backend QA pack.
// Target location when applied: pkg/gateway/ws_ask_user_rejection_test.go
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// RED STATUS (qa-lead, 2026-10-16, revision 2 after backend round-1
// feedback): authored against the user-dispatched ErrorFrame Decision A and
// its user-confirmed invalid-envelope boundary only. Revision 2 changes
// (none adapts an expectation to production behavior):
//   - NewSession returns *session.UnifiedMeta — fixtures use
//     sessionMeta.ID (the round-1 measured compile failure, mirrored from
//     backend's fixture-only correction; a compile failure is NOT RED
//     evidence and is never claimed as one).
//   - INVALID-ENVELOPE BOUNDARY (user-confirmed): an unusable envelope —
//     blank/missing card id, blank/missing session id, whitespace-only ids,
//     or a malformed frame — must get an ORDINARY GENERIC ErrorFrame with
//     NO ask_user_card_id (Decision A cannot echo a blank id: minLength 1)
//     and must change NO state (head/queue/timers untouched). Every VALID
//     card-action error still carries BOTH exact ids. Producers must never
//     emit a contract-invalid empty ask_user_card_id.
//   - The most important pre-change runtime witness is pinned:
//     handleAskUserAnswer called directly (typed frame) with card A +
//     session_id "" + cancel:true reaches CancelByUser → cancelCommon's
//     optional-session check skips the mismatch test on an empty session id
//     and CONSUMES the card today. The blank-session case asserts untouched
//     A+queue and is RED on the consumption itself.
//   - A malformed typed-field frame through the REAL raw dispatch
//     (wsHandlerReadLoop.dispatchFrame — declaration read, real params)
//     currently lands a SILENT drop (dispatch's json.Unmarshal failure
//     continue); it must get a generic rejection instead.
//   - Accepted CANCEL + failed resume mirrors the accepted-ANSWER case:
//     terminal/promotion stays committed, ids are the CONSUMED card's.
//
// Every frame is decoded from RAW JSON into a map — deliberately not into
// generated.ErrorFrame — so the pack never references a Go field the
// generated types may not carry yet, and never parses message prose for
// correlation. Recommended narrow gate (serial):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestAskUserErrorCorrelation$' -count=1 -v -p 1 ./pkg/gateway/
//
// Skill note: per elicify-test-writing in RED, items 2 (see it green), 3
// (mutate and confirm it dies) and the Proof-of-failability checklist are
// DEFERRED TO CHECK — they are not claimed here.

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/askuser"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// errQaAskSave is the exact sentinel the MetaStore wrapper injects for
// pending-ask save failures in this pack.
var errQaAskSave = errors.New("qa-1343: injected ask-user save failure")

// qaAskFaultMeta wraps a REAL UnifiedStore and faults SetMeta ONLY for
// pending-ask writes while armed.
type qaAskFaultMeta struct {
	inner *session.UnifiedStore
	mu    sync.Mutex
	armed bool
}

func (m *qaAskFaultMeta) GetMeta(sessionID string) (*session.UnifiedMeta, error) {
	return m.inner.GetMeta(sessionID)
}

func (m *qaAskFaultMeta) SetMeta(sessionID string, patch session.MetaPatch) error {
	m.mu.Lock()
	armed := m.armed
	m.mu.Unlock()
	if armed && patch.PendingAskJSON != nil {
		return errQaAskSave
	}
	return m.inner.SetMeta(sessionID, patch)
}

func (m *qaAskFaultMeta) AppendTranscriptStrict(sessionID string, entry session.TranscriptEntry) error {
	return m.inner.AppendTranscriptStrict(sessionID, entry)
}

func (m *qaAskFaultMeta) arm() {
	m.mu.Lock()
	m.armed = true
	m.mu.Unlock()
}

func (m *qaAskFaultMeta) disarm() {
	m.mu.Lock()
	m.armed = false
	m.mu.Unlock()
}

// qaRejectionFixture wires the REAL registry over a REAL store with the
// PRODUCTION resume dispatcher (a real message bus). No session, registry or
// handler is faked; the fault sits at exactly one seam (MetaStore.SetMeta).
type qaRejectionFixture struct {
	store *session.UnifiedStore
	meta  *qaAskFaultMeta
	reg   *askuser.Registry
	mb    *bus.MessageBus
	sid   string
	cardA string
	cardB string
}

func qaNewRejectionFixture(t *testing.T) *qaRejectionFixture {
	t.Helper()
	store, err := session.NewUnifiedStore(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close(), "rejection pack store.Close must succeed") })
	// NewSession returns the session's *UnifiedMeta (round-1 measured
	// compile failure) — the id is the meta's field.
	createdMeta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	sid := createdMeta.ID
	mb := bus.NewMessageBus()
	t.Cleanup(mb.Close)
	meta := &qaAskFaultMeta{inner: store}
	reg := askuser.NewRegistry(meta, &askUserResumeDispatcher{msgBus: mb}, askuser.Options{})
	t.Cleanup(func() {
		meta.disarm()
		reg.Quiesce()
	})
	f := &qaRejectionFixture{store: store, meta: meta, reg: reg, mb: mb, sid: sid}
	head := qaRejectionCard(sid, "Head question for "+sid+"?")
	queued := qaRejectionCard(sid, "Queued question for "+sid+"?")
	require.NoError(t, reg.CreatePending(head))
	require.NoError(t, reg.CreatePending(queued))
	f.cardA, f.cardB = head.CardID, queued.CardID
	return f
}

// qaRejectionCard builds a two-question set matching the wire answer shape
// the handler decodes (one single-select, one multi-select).
func qaRejectionCard(sessionID, question string) *askuser.PendingSet {
	return &askuser.PendingSet{
		CardID:              askuser.NewCardID(),
		RoutingSessionKey:   "session:" + sessionID,
		TranscriptSessionID: sessionID,
		AgentID:             "mia",
		Channel:             "webchat",
		ChatID:              "chat-1343",
		Owner:               "daniel",
		Questions: []askuser.Question{
			{
				Header:   "Scope",
				Question: question,
				Options:  []askuser.Option{{Label: "Backend"}, {Label: "Full stack"}},
			},
			{
				Header:      "Deploy",
				Question:    "Deploy where?",
				MultiSelect: true,
				Options:     []askuser.Option{{Label: "Staging"}, {Label: "Prod"}},
			},
		},
	}
}

// qaValidAnswerFrame renders a valid ask_user_answer body for the fixture's
// head card (cancel selects the cancel path instead).
func qaValidAnswerFrame(cardID, sessionID string, cancel bool) []byte {
	body := map[string]any{
		"type":       "ask_user_answer",
		"card_id":    cardID,
		"session_id": sessionID,
	}
	if cancel {
		body["cancel"] = true
	} else {
		body["answers"] = []map[string]any{
			{"header": "Scope", "selected": []string{"Backend"}},
			{"header": "Deploy", "selected": []string{"Staging"}},
		}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		panic(err) // fixture-only: a map of literals always marshals
	}
	return raw
}

func qaReadErrorFrame(t *testing.T, wc *wsConn) map[string]any {
	t.Helper()
	select {
	case raw := <-wc.sendCh:
		var frame map[string]any
		require.NoError(t, json.Unmarshal(raw, &frame), "raw=%s", raw)
		return frame
	case <-time.After(5 * time.Second):
		t.Fatal("no error frame reached the submitting connection")
		return nil
	}
}

// qaAssertCorrelatedError pins Decision A on one emitted error frame: the
// type is the existing error frame, BOTH ids come from the submitted frame
// (ask_user_card_id optional-but-present with minLength 1, session_id
// required), the first-send fields are absent, a message exists, and no
// OTHER connection received anything.
func qaAssertCorrelatedError(t *testing.T, wc, other *wsConn, submittedCardID, submittedSessionID string) map[string]any {
	t.Helper()
	frame := qaReadErrorFrame(t, wc)
	require.Equal(t, "error", frame["type"])
	require.Equal(t, submittedSessionID, frame["session_id"],
		"the error frame must carry the submitted frame's session_id (Decision A: requires session_id)")
	cardID, ok := frame["ask_user_card_id"].(string)
	require.True(t, ok,
		"the error frame must carry ask_user_card_id (optional field, minLength 1) populated from the submitted frame; raw=%v", frame)
	require.Equal(t, submittedCardID, cardID, "ask_user_card_id must be the SUBMITTED card id")
	require.NotEmpty(t, cardID, "ask_user_card_id has minLength 1")
	_, hasClientMessageID := frame["client_message_id"]
	require.False(t, hasClientMessageID, "an ask_user error must carry no client_message_id")
	_, hasFirstMessageError := frame["first_message_error"]
	require.False(t, hasFirstMessageError, "an ask_user error must carry no first_message_error")
	require.NotEmpty(t, frame["message"], "the error frame must keep a human-readable message")
	select {
	case raw := <-other.sendCh:
		t.Fatalf("an unrelated connection received a frame that must go only to the submitting connection: %s", raw)
	default:
	}
	return frame
}

// qaAssertGenericEnvelopeError pins the invalid-envelope boundary on one
// emitted error frame: an ORDINARY generic ErrorFrame — the existing shape —
// with NO ask_user_card_id (the producer must never emit a contract-invalid
// empty id: minLength 1). session_id is deliberately NOT asserted here: an
// unusable envelope has nothing lawful to scope to, and pinning its absence
// would over-constrain the producer. The state oracle lives with the caller.
func qaAssertGenericEnvelopeError(t *testing.T, wc *wsConn) map[string]any {
	t.Helper()
	frame := qaReadErrorFrame(t, wc)
	require.Equal(t, "error", frame["type"])
	_, hasCardID := frame["ask_user_card_id"]
	require.False(t, hasCardID,
		"an unusable envelope (blank/missing id) must get a generic error frame with NO ask_user_card_id — the producer must never emit a contract-invalid empty id; raw=%v", frame)
	require.NotEmpty(t, frame["message"], "the generic rejection must keep a human-readable message")
	return frame
}

// qaAssertQueueUntouched pins the no-state-change oracle of the envelope
// boundary on the fixture: head A pending, B hidden, both counted.
func qaAssertQueueUntouched(t *testing.T, f *qaRejectionFixture) {
	t.Helper()
	active, still := f.reg.PendingForSession(f.sid)
	require.True(t, still, "an unusable envelope must change no state — the head must stay pending")
	require.Equal(t, f.cardA, active.CardID)
	require.Equal(t, askuser.StatusPending, active.Status)
	_, queuedVisible := f.reg.Get(f.cardB)
	require.False(t, queuedVisible, "an unusable envelope must change no state — the queue must stay hidden")
	require.Equal(t, 2, f.reg.PendingCount())
}

// TestAskUserErrorCorrelation is the single narrow entry point; one subtest
// per handleAskUserAnswer error branch named by the dispatch, plus the
// invalid-envelope boundary and the accepted-cancel resume arm.
func TestAskUserErrorCorrelation(t *testing.T) {
	t.Run("no_registry", qaAskCase_NoRegistry)
	t.Run("unknown_card", qaAskCase_UnknownCard)
	t.Run("answer_validation_rejected", qaAskCase_ValidationRejected)
	t.Run("cancel_save_failure_retains_head_and_queue", qaAskCase_CancelSaveFailure)
	t.Run("answer_save_failure_retains_head_and_queue", qaAskCase_AnswerSaveFailure)
	t.Run("resume_failure_after_accepted_answer_reports_consumed_ids_without_rollback", qaAskCase_ResumeFailureAfterAnswer)
	t.Run("resume_failure_after_accepted_cancel_reports_consumed_ids_without_rollback", qaAskCase_ResumeFailureAfterCancel)
	t.Run("blank_session_cancel_cannot_consume_through_typed_handler", qaAskCase_BlankSessionCancel)
	t.Run("blank_card_id_gets_generic_rejection_and_changes_nothing", qaAskCase_BlankCardID)
	t.Run("whitespace_session_id_changes_nothing", qaAskCase_WhitespaceSessionID)
	t.Run("raw_dispatch_missing_session_is_generic_regression_control", qaAskCase_RawMissingSession)
	t.Run("raw_dispatch_malformed_frame_gets_generic_rejection_not_silent_drop", qaAskCase_RawMalformedFrame)
}

// qaAskConnPair builds the submitting connection plus one unrelated
// connected client (the only-submitting-connection-sees-it proof).
func qaAskConnPair() (*wsConn, *wsConn) {
	submitting := &wsConn{userID: "daniel", sendCh: make(chan []byte, 4)}
	other := &wsConn{userID: "daniel", sendCh: make(chan []byte, 4)}
	return submitting, other
}

func qaAskCase_NoRegistry(t *testing.T) {
	h := &WSHandler{} // no registry on this deployment
	submitting, other := qaAskConnPair()
	h.sessions = map[string]*wsConn{"other": other}

	var frame generated.AskUserAnswerFrame
	require.NoError(t, json.Unmarshal(qaValidAnswerFrame("ask_any", "session_any", false), &frame))
	h.handleAskUserAnswer(submitting, frame)

	qaAssertCorrelatedError(t, submitting, other, "ask_any", "session_any")
}

func qaAskCase_UnknownCard(t *testing.T) {
	f := qaNewRejectionFixture(t)
	h := &WSHandler{askUserReg: f.reg}
	submitting, other := qaAskConnPair()
	h.sessions = map[string]*wsConn{"other": other}

	var frame generated.AskUserAnswerFrame
	require.NoError(t, json.Unmarshal(qaValidAnswerFrame("ask_missing", f.sid, false), &frame))
	h.handleAskUserAnswer(submitting, frame)

	qaAssertCorrelatedError(t, submitting, other, "ask_missing", f.sid)
	require.Equal(t, 2, f.reg.PendingCount(), "an unknown card touches nothing")
}

func qaAskCase_ValidationRejected(t *testing.T) {
	f := qaNewRejectionFixture(t)
	h := &WSHandler{askUserReg: f.reg}
	submitting, other := qaAskConnPair()
	h.sessions = map[string]*wsConn{"other": other}

	raw, err := json.Marshal(map[string]any{
		"type":       "ask_user_answer",
		"card_id":    f.cardA,
		"session_id": f.sid,
		"answers": []map[string]any{
			{"header": "Scope", "selected": []string{"NOT-AN-OPTION"}},
			{"header": "Deploy", "selected": []string{"Staging"}},
		},
	})
	require.NoError(t, err)
	var frame generated.AskUserAnswerFrame
	require.NoError(t, json.Unmarshal(raw, &frame))
	h.handleAskUserAnswer(submitting, frame)

	qaAssertCorrelatedError(t, submitting, other, f.cardA, f.sid)
	// A validation rejection consumes nothing: first-valid-wins needs the
	// set still pending.
	qaAssertQueueUntouched(t, f)
}

func qaAskCase_CancelSaveFailure(t *testing.T) {
	f := qaNewRejectionFixture(t)
	h := &WSHandler{askUserReg: f.reg}
	submitting, other := qaAskConnPair()
	h.sessions = map[string]*wsConn{"other": other}

	f.meta.arm()
	var frame generated.AskUserAnswerFrame
	require.NoError(t, json.Unmarshal(qaValidAnswerFrame(f.cardA, f.sid, true), &frame))
	h.handleAskUserAnswer(submitting, frame)

	qaAssertCorrelatedError(t, submitting, other, f.cardA, f.sid)
	// The failed save retains the head AND the queue.
	qaAssertQueueUntouched(t, f)
}

func qaAskCase_AnswerSaveFailure(t *testing.T) {
	f := qaNewRejectionFixture(t)
	h := &WSHandler{askUserReg: f.reg}
	submitting, other := qaAskConnPair()
	h.sessions = map[string]*wsConn{"other": other}

	f.meta.arm()
	var frame generated.AskUserAnswerFrame
	require.NoError(t, json.Unmarshal(qaValidAnswerFrame(f.cardA, f.sid, false), &frame))
	h.handleAskUserAnswer(submitting, frame)

	qaAssertCorrelatedError(t, submitting, other, f.cardA, f.sid)
	// The failed save retains the head AND the queue: the answer was never
	// accepted.
	qaAssertQueueUntouched(t, f)
}

// qaAskCase_ResumeFailureAfterAnswer pins the accepted-then-failed arm for
// ANSWERS: the answer IS consumed (terminal, promotion committed — no
// rollback), and the error frame still carries the CONSUMED card A's
// submitted ids — never the promoted card B's. The dispatch fault is the
// production dispatcher's own bus-closed branch (the fixture's REAL message
// bus is closed before the answer is submitted).
func qaAskCase_ResumeFailureAfterAnswer(t *testing.T) {
	f := qaNewRejectionFixture(t)
	h := &WSHandler{askUserReg: f.reg}
	submitting, other := qaAskConnPair()
	h.sessions = map[string]*wsConn{"other": other}

	f.mb.Close() // the dispatch fault: production dispatcher fails AFTER consumption

	var frame generated.AskUserAnswerFrame
	require.NoError(t, json.Unmarshal(qaValidAnswerFrame(f.cardA, f.sid, false), &frame))
	h.handleAskUserAnswer(submitting, frame)

	qaAssertCorrelatedError(t, submitting, other, f.cardA, f.sid)
	qaAssertConsumedANotRolledBack(t, f)
}

// qaAskCase_ResumeFailureAfterCancel mirrors the accepted-ANSWER arm for
// CANCELS (round-2 correction 4): the cancel IS consumed, the promotion is
// committed, and the resume-failure frame carries the CONSUMED card A's
// submitted ids — never the promoted card B's — with no rollback.
func qaAskCase_ResumeFailureAfterCancel(t *testing.T) {
	f := qaNewRejectionFixture(t)
	h := &WSHandler{askUserReg: f.reg}
	submitting, other := qaAskConnPair()
	h.sessions = map[string]*wsConn{"other": other}

	f.mb.Close() // the dispatch fault: production dispatcher fails AFTER consumption

	var frame generated.AskUserAnswerFrame
	require.NoError(t, json.Unmarshal(qaValidAnswerFrame(f.cardA, f.sid, true), &frame))
	h.handleAskUserAnswer(submitting, frame)

	qaAssertCorrelatedError(t, submitting, other, f.cardA, f.sid)
	qaAssertConsumedANotRolledBack(t, f)
}

// qaAssertConsumedANotRolledBack is the shared no-rollback oracle of both
// accepted-then-failed arms: A is terminal and gone, B is the promoted head,
// exactly one pending set remains.
func qaAssertConsumedANotRolledBack(t *testing.T, f *qaRejectionFixture) {
	t.Helper()
	_, consumed := f.reg.Get(f.cardA)
	require.False(t, consumed, "an accepted action must stay consumed — the resume failure never rolls back")
	head, ok := f.reg.PendingForSession(f.sid)
	require.True(t, ok, "the promoted head must remain after the failed resume")
	require.Equal(t, f.cardB, head.CardID)
	require.Equal(t, 1, f.reg.PendingCount())
	// The correlation ids name the CONSUMED card on this arm: consumed and
	// promoted ids differ by construction in the fixture.
	require.NotEqual(t, f.cardB, f.cardA, "fixture sanity: consumed and promoted card ids differ")
}

// qaAskCase_BlankSessionCancel is the round-2 boundary's most important
// pre-change runtime witness: a TYPED frame with a valid card id and a BLANK
// session id, dispatched straight at handleAskUserAnswer (exactly what the
// raw gate exists to prevent — this case proves the typed handler must hold
// the same boundary itself). Today the blank session id reaches
// CancelByUser → cancelCommon, whose optional-session check skips the
// mismatch test on an empty id and CONSUMES the card: state is destroyed by
// an unusable envelope. The oracle: generic rejection (no ask_user_card_id —
// a blank id cannot be echoed), and A + queue COMPLETELY untouched.
func qaAskCase_BlankSessionCancel(t *testing.T) {
	f := qaNewRejectionFixture(t)
	h := &WSHandler{askUserReg: f.reg}
	submitting, other := qaAskConnPair()
	h.sessions = map[string]*wsConn{"other": other}

	var frame generated.AskUserAnswerFrame
	require.NoError(t, json.Unmarshal(qaValidAnswerFrame(f.cardA, "", true), &frame))
	h.handleAskUserAnswer(submitting, frame)

	qaAssertGenericEnvelopeError(t, submitting)
	qaAssertQueueUntouched(t, f)
	select {
	case raw := <-other.sendCh:
		t.Fatalf("an unrelated connection received a frame: %s", raw)
	default:
	}
}

// qaAskCase_BlankCardID pins the blank-card half of the boundary (a
// regression control alongside the raw gate): generic rejection, no
// ask_user_card_id, no state change.
func qaAskCase_BlankCardID(t *testing.T) {
	f := qaNewRejectionFixture(t)
	h := &WSHandler{askUserReg: f.reg}
	submitting, other := qaAskConnPair()
	h.sessions = map[string]*wsConn{"other": other}

	var frame generated.AskUserAnswerFrame
	require.NoError(t, json.Unmarshal(qaValidAnswerFrame("", f.sid, false), &frame))
	h.handleAskUserAnswer(submitting, frame)

	qaAssertGenericEnvelopeError(t, submitting)
	qaAssertQueueUntouched(t, f)
}

// qaAskCase_WhitespaceSessionID pins the F2 whitespace boundary per the
// founder ruling (CHECK F-2): blank/whitespace ids are UNUSABLE envelopes —
// the rejection must be the GENERIC ErrorFrame with NO ask_user_card_id,
// and no question state may change.
func qaAskCase_WhitespaceSessionID(t *testing.T) {
	f := qaNewRejectionFixture(t)
	h := &WSHandler{askUserReg: f.reg}
	submitting, _ := qaAskConnPair()

	var frame generated.AskUserAnswerFrame
	require.NoError(t, json.Unmarshal(qaValidAnswerFrame(f.cardA, "   ", true), &frame))
	h.handleAskUserAnswer(submitting, frame)

	qaAssertGenericEnvelopeError(t, submitting)
	qaAssertQueueUntouched(t, f)
}

// qaAskCase_RawMissingSession pins the RAW dispatch gate as a regression
// control: a raw frame missing session_id goes through the REAL dispatcher
// (wsHandlerReadLoop.dispatchFrame — declaration read, real params) and must
// produce the generic rejection with no id echo, changing nothing.
func qaAskCase_RawMissingSession(t *testing.T) {
	f := qaNewRejectionFixture(t)
	h := &WSHandler{askUserReg: f.reg}
	submitting, _ := qaAskConnPair()

	wh := &wsHandlerReadLoop{h: h, ctx: context.Background(), wc: submitting, chatID: "chat-1343"}
	raw := []byte(`{"type":"ask_user_answer","card_id":"` + f.cardA + `"}`) // no session_id
	wh.dispatchFrame(raw, wsTypeOnly{Type: "ask_user_answer"})

	qaAssertGenericEnvelopeError(t, submitting)
	qaAssertQueueUntouched(t, f)
}

// qaAskCase_RawMalformedFrame pins the malformed-typed-field boundary through
// the REAL raw dispatch: a frame whose typed field cannot decode must get a
// GENERIC rejection, never a silent drop. Today dispatchFrame's
// json.Unmarshal failure branch continues silently — the bounded read below
// fails, which IS the RED.
func qaAskCase_RawMalformedFrame(t *testing.T) {
	f := qaNewRejectionFixture(t)
	h := &WSHandler{askUserReg: f.reg}
	submitting, _ := qaAskConnPair()

	wh := &wsHandlerReadLoop{h: h, ctx: context.Background(), wc: submitting, chatID: "chat-1343"}
	raw := []byte(`{"type":"ask_user_answer","card_id":"` + f.cardA + `","session_id":"` + f.sid + `","cancel":"not-a-bool"}`)
	wh.dispatchFrame(raw, wsTypeOnly{Type: "ask_user_answer"})

	// The bounded read is the failure DETECTOR; the oracle is the frame's
	// shape: generic (a malformed envelope echoes no id), no state change.
	qaAssertGenericEnvelopeError(t, submitting)
	qaAssertQueueUntouched(t, f)
}
