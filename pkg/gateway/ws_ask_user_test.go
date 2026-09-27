// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ws_ask_user_test.go — askuserquestion-tool-spec v3 test 8 (gateway wire
// half): toAskUserCard maps pkg/askuser.PendingSet onto the generated card
// shape (default_safe_at materialization, auto_resolved marks, answer echo
// incl. auto_default origin), and the resume dispatcher's origin heuristic.
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/askuser"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func findAskUserGatewayLog(t *testing.T, buf *bytes.Buffer, message string) map[string]any {
	t.Helper()
	recordCount := 0
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			continue
		}
		recordCount++
		if record["msg"] == message {
			return record
		}
	}
	t.Fatalf("missing gateway log message %q (captured_record_count=%d)", message, recordCount)
	return nil
}

func askSetFixture() *askuser.PendingSet {
	return &askuser.PendingSet{
		CardID:              "ask_1",
		TranscriptSessionID: "session_owner_1",
		AgentID:             "mia",
		Channel:             "webchat",
		ChatID:              "chat-1",
		Owner:               "daniel",
		CreatedAt:           time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC),
		Status:              askuser.StatusPending,
		Questions: []askuser.Question{
			{
				Header:   "Scope",
				Question: "Which emails?",
				Options: []askuser.Option{
					{Label: "Only unanswered", Description: "the backlog"},
					{Label: "All"},
				},
				Recommended: "Only unanswered",
				DefaultSafe: true,
				Context:     "- every question answered",
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

func TestToAskUserCard_PendingMapsFaithfully(t *testing.T) {
	set := askSetFixture()
	set.AutoResolved = map[string]time.Time{"Scope": set.CreatedAt.Add(30 * time.Minute)}

	card := toAskUserCard(set, 30*time.Minute)
	assert.Equal(t, "ask_1", card.CardId)
	assert.Equal(t, "session_owner_1", card.SessionId)
	assert.Equal(t, "mia", card.AgentId)
	assert.Equal(t, "pending", card.Status)
	assert.Equal(t, "2026-09-06T12:00:00Z", card.CreatedAt)

	// default_safe_at = created_at + delay, present because a default-safe
	// question exists.
	require.NotNil(t, card.DefaultSafeAt)
	assert.Equal(t, "2026-09-06T12:30:00Z", *card.DefaultSafeAt)
	assert.Equal(t, []string{"Scope"}, card.AutoResolved)

	require.Len(t, card.Questions, 2)
	q0 := card.Questions[0]
	assert.Equal(t, "Scope", q0.Header)
	require.NotNil(t, q0.Recommended)
	assert.Equal(t, "Only unanswered", *q0.Recommended)
	require.NotNil(t, q0.DefaultSafe)
	require.NotNil(t, q0.Context)
	require.Len(t, q0.Options, 2)
	require.NotNil(t, q0.Options[0].Description)
	assert.Nil(t, q0.Options[1].Description, "empty description stays absent")

	q1 := card.Questions[1]
	require.NotNil(t, q1.MultiSelect)
	assert.Nil(t, q1.Recommended)
	assert.Nil(t, q1.DefaultSafe)
	assert.Nil(t, q1.Context)
}

func TestToAskUserCard_NoDefaultSafeNoDeadline(t *testing.T) {
	set := askSetFixture()
	set.Questions[0].DefaultSafe = false
	card := toAskUserCard(set, 30*time.Minute)
	assert.Nil(t, card.DefaultSafeAt, "no default-safe question -> no deadline on the wire")
}

func TestToAskUserCard_TerminalCarriesAnswers(t *testing.T) {
	set := askSetFixture()
	set.Status = askuser.StatusAnswered
	ft := "something else"
	set.Answers = []askuser.Answer{
		{Header: "Scope", QuestionText: "Which emails?", Selected: []string{"Only unanswered"}, AutoDefault: true},
		{Header: "Deploy", QuestionText: "Deploy where?", FreeText: &ft},
	}
	card := toAskUserCard(set, 30*time.Minute)
	assert.Equal(t, "answered", card.Status)
	assert.Nil(t, card.DefaultSafeAt, "terminal card carries no countdown deadline")
	require.Len(t, card.Answers, 2)
	assert.True(t, card.Answers[0].AutoDefault, "auto-default origin marker crosses the wire")
	assert.Equal(t, []string{"Only unanswered"}, card.Answers[0].Selected)
	require.NotNil(t, card.Answers[1].FreeText)
	assert.Equal(t, "something else", *card.Answers[1].FreeText)
	assert.Equal(t, "Deploy where?", card.Answers[1].Question, "question-text echo (o-R2-1) preserved")
}

// broadcastAskUserCard fans out via the shared broadcastRaw helper: every
// connected client receives the ask_user_question frame through its own
// ordered queue. #823 (founder decision Q5): a client whose send window is
// full no longer loses the frame — it waits in that client's queue — and the
// only "drop" left is a connection that is already closed (it re-hydrates
// from session_state's pending_asks on reconnect), which is still reported
// as a WARN with the aggregate counts.
func TestBroadcastAskUserCard_FanOutAndDropCounter(t *testing.T) {
	logBuf := captureSlogJSON(t)
	wcOK := &wsConn{sendCh: make(chan []byte, 4)}
	wcFull := &wsConn{sendCh: make(chan []byte)} // unbuffered, nobody reading → queued, not dropped
	wcClosed := &wsConn{sendCh: make(chan []byte, 4), doneCh: make(chan struct{})}
	wcClosed.close()
	h := &WSHandler{sessions: map[string]*wsConn{"ok": wcOK, "full": wcFull, "closed": wcClosed}}

	h.broadcastAskUserCard(toAskUserCard(askSetFixture(), 30*time.Minute))

	select {
	case raw := <-wcOK.sendCh:
		var frame map[string]any
		require.NoError(t, json.Unmarshal(raw, &frame))
		assert.Equal(t, "ask_user_question", frame["type"])
		card, ok := frame["card"].(map[string]any)
		require.True(t, ok, "frame must carry the card object")
		assert.Equal(t, "ask_1", card["card_id"])
	default:
		t.Fatal("connected client with buffer room never received the frame")
	}
	queued, _ := wcFull.queuedFrames()
	assert.Equal(t, 1, queued, "a full-window client keeps the frame queued — never dropped")
	assert.Empty(t, wcClosed.sendCh, "a closed client gets nothing")

	record := findAskUserGatewayLog(t, logBuf, "ws: ask_user_question broadcast")
	assert.Equal(t, "WARN", record["level"], "a fan-out that missed a connection is genuine production-visible trouble")
	assert.Equal(t, "ask_1", record["card_id"])
	assert.Equal(t, float64(3), record["fanout_count"])
	assert.Equal(t, float64(2), record["enqueued_count"])
	assert.Equal(t, float64(1), record["drop_count"])
}

func TestBroadcastAskUserCard_NoDropsLogsInfo(t *testing.T) {
	logBuf := captureSlogJSON(t)
	wcA := &wsConn{sendCh: make(chan []byte, 1)}
	wcB := &wsConn{sendCh: make(chan []byte, 1)}
	h := &WSHandler{sessions: map[string]*wsConn{"a": wcA, "b": wcB}}

	h.broadcastAskUserCard(toAskUserCard(askSetFixture(), 30*time.Minute))

	record := findAskUserGatewayLog(t, logBuf, "ws: ask_user_question broadcast")
	assert.Equal(t, "INFO", record["level"])
	assert.Equal(t, float64(2), record["fanout_count"])
	assert.Equal(t, float64(2), record["enqueued_count"])
	assert.Equal(t, float64(0), record["drop_count"])
}

// The resume origin heuristic: a human submission/cancel is user-initiated;
// the server's all-default auto-submit is not.
func TestAskUserResumeDispatcher_OriginHeuristic(t *testing.T) {
	d := &askUserResumeDispatcher{} // no bus — only the heuristic matters here
	_ = d

	humanSet := askSetFixture()
	humanSet.Status = askuser.StatusAnswered
	humanSet.Answers = []askuser.Answer{
		{Header: "Scope", Selected: []string{"Only unanswered"}, AutoDefault: true},
		{Header: "Deploy", Selected: []string{"Staging"}}, // one human answer
	}
	autoSet := askSetFixture()
	autoSet.Status = askuser.StatusAnswered
	autoSet.Answers = []askuser.Answer{
		{Header: "Scope", Selected: []string{"Only unanswered"}, AutoDefault: true},
	}
	cancelledSet := askSetFixture()
	cancelledSet.Status = askuser.StatusCancelled

	assert.True(t, resumeIsUserInitiated(humanSet))
	assert.False(t, resumeIsUserInitiated(autoSet))
	assert.True(t, resumeIsUserInitiated(cancelledSet))
}

// TestAskUserResumeDispatcher_PublishesToBus is the SQUAD-R reproduction at
// the publish-path seam: it wires the PRODUCTION askUserResumeDispatcher to
// a REAL bus.MessageBus and verifies that a human-submitted answer lands on
// the bus with the exact §0.2 correlated user-role message shape, the
// channel/chatID/sessionID that the turn machinery needs to route the
// resume, and the origin signal (UserInitiated) the turn options expect.
// The brief's primary suspect is the live-resolve branch ending here at
// Submit → dispatchResume; this test pinpoints whether the publish itself
// drops or fails on the current release/v0.1.1 head.
func TestAskUserResumeDispatcher_PublishesToBus(t *testing.T) {
	logBuf := captureSlogJSON(t)
	mb := bus.NewMessageBus()
	t.Cleanup(mb.Close)
	disp := &askUserResumeDispatcher{msgBus: mb}

	// Park a set, then submit a human answer — the answer carries one
	// non-auto answer so resumeIsUserInitiated returns true.
	set := askSetFixture()
	set.Owner = "daniel"
	set.Status = askuser.StatusAnswered
	const secretDelivered = "DELIVERED-ANSWER-DO-NOT-LOG"
	set.Answers = []askuser.Answer{
		{Header: "Scope", QuestionText: "Which emails?", Selected: []string{secretDelivered}},
		{Header: "Deploy", QuestionText: "Deploy where?", Selected: []string{"Staging", "Prod"}},
	}

	wantText, err := askuser.ResumeMessage(set)
	require.NoError(t, err)

	err = disp.DispatchResume(set, wantText)
	require.NoError(t, err, "DispatchResume must not error with a wired bus")

	select {
	case got := <-mb.InboundChan():
		assert.Equal(t, "webchat", got.Channel, "resume must carry the SPA channel so resolveMessageRoute keys on webchat")
		assert.Equal(t, "chat-1", got.ChatID, "resume must carry the original chatID so resolveSessionConns reaches it")
		assert.Equal(t, "session_owner_1", got.SessionID, "resume must carry the transcript session id so per-session worker scope matches")
		assert.Equal(t, "daniel", got.GatewayUserID, "resume must carry the owner so audit User stamps")
		assert.True(t, got.UserInitiated, "human-submitted answer is a user-initiated turn by the field's own definition")
		assert.Equal(t, "webchat_user", got.Sender.CanonicalID)
		require.NotNil(t, got.Metadata)
		assert.Equal(t, "mia", got.Metadata["agent_id"], "resume must carry the parking agent so route resolution picks the same agent")
		assert.Equal(t, wantText, got.Content, "resume content must be the §0.2 correlated user-role message verbatim")
	default:
		t.Fatal("resume message never landed on the bus — the live-resolve branch drops the answer before the loop can pick it up")
	}

	record := findAskUserGatewayLog(t, logBuf, "ws: ask_user resume dispatch completed")
	assert.Equal(t, "INFO", record["level"])
	assert.Equal(t, "ask_1", record["card_id"])
	assert.Equal(t, "delivered", record["outcome"])
	assert.Equal(t, "message_bus", record["delivery_stage"])
	assert.Equal(t, float64(2), record["answer_count"])
	if strings.Contains(logBuf.String(), secretDelivered) {
		t.Fatal("delivered resume log exposed answer content")
	}
}

func TestAskUserResumeDispatcher_LogsStrandedWithoutBus(t *testing.T) {
	logBuf := captureSlogJSON(t)
	set := askSetFixture()
	set.Status = askuser.StatusAnswered
	set.Answers = []askuser.Answer{{Header: "Scope", FreeText: strpGateway("STRANDED-ANSWER-DO-NOT-LOG")}}
	resumeText, err := askuser.ResumeMessage(set)
	require.NoError(t, err)

	err = (&askUserResumeDispatcher{}).DispatchResume(set, resumeText)
	require.Error(t, err)
	record := findAskUserGatewayLog(t, logBuf, "ws: ask_user resume dispatch completed")
	assert.Equal(t, "WARN", record["level"])
	assert.Equal(t, "stranded", record["outcome"])
	assert.Equal(t, "no_message_bus", record["reason"])
	if strings.Contains(logBuf.String(), "DO-NOT-LOG") {
		t.Fatal("stranded resume log exposed answer content")
	}
}

func TestAskUserResumeDispatcher_LogsTimeout(t *testing.T) {
	logBuf := captureSlogJSON(t)
	mb := bus.NewMessageBus()
	t.Cleanup(mb.Close)
	fillCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for i := 0; i < 64; i++ {
		require.NoError(t, mb.PublishInbound(fillCtx, bus.InboundMessage{}))
	}

	set := askSetFixture()
	set.CardID = "ask_timeout"
	set.Status = askuser.StatusAnswered
	set.Answers = []askuser.Answer{{Header: "Scope", FreeText: strpGateway("TIMEOUT-ANSWER-DO-NOT-LOG")}}
	resumeText, err := askuser.ResumeMessage(set)
	require.NoError(t, err)
	err = (&askUserResumeDispatcher{msgBus: mb}).DispatchResume(set, resumeText)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	record := findAskUserGatewayLog(t, logBuf, "ws: ask_user resume dispatch completed")
	assert.Equal(t, "WARN", record["level"])
	assert.Equal(t, "timed_out", record["outcome"])
	if strings.Contains(logBuf.String(), "DO-NOT-LOG") {
		t.Fatal("timed-out resume log exposed answer content")
	}
}

func TestAskUserResumeDispatcher_LogsClosedBusAsStranded(t *testing.T) {
	logBuf := captureSlogJSON(t)
	mb := bus.NewMessageBus()
	mb.Close()

	set := askSetFixture()
	set.Status = askuser.StatusAnswered
	set.Answers = []askuser.Answer{{Header: "Scope", Selected: []string{"Only unanswered"}}}
	resumeText, err := askuser.ResumeMessage(set)
	require.NoError(t, err)

	err = (&askUserResumeDispatcher{msgBus: mb}).DispatchResume(set, resumeText)
	require.ErrorIs(t, err, bus.ErrBusClosed)
	record := findAskUserGatewayLog(t, logBuf, "ws: ask_user resume dispatch completed")
	assert.Equal(t, "WARN", record["level"])
	assert.Equal(t, "stranded", record["outcome"])
	assert.Equal(t, "bus_closed", record["reason"])
}

func TestHandleAskUserAnswer_RejectionLogDoesNotExposeSubmittedContent(t *testing.T) {
	logBuf := captureSlogJSON(t)
	reg := askuser.NewRegistry(nil, nil, askuser.Options{})
	require.NoError(t, reg.CreatePending(askSetFixture()))
	t.Cleanup(reg.Quiesce)

	const secret = "SECRET-INVALID-OPTION-DO-NOT-LOG"
	var frame generated.AskUserAnswerFrame
	require.NoError(t, json.Unmarshal([]byte(`{
		"type":"ask_user_answer",
		"card_id":"ask_1",
		"session_id":"session_owner_1",
		"answers":[
			{"header":"Scope","selected":["SECRET-INVALID-OPTION-DO-NOT-LOG"]},
			{"header":"Deploy","selected":["Staging"]}
		]
	}`), &frame))

	h := &WSHandler{askUserReg: reg}
	h.handleAskUserAnswer(&wsConn{userID: "daniel", sendCh: make(chan []byte, 1)}, frame)

	record := findAskUserGatewayLog(t, logBuf, "ws: ask_user_answer rejected")
	assert.Equal(t, "INFO", record["level"])
	assert.Equal(t, "invalid_answer", record["reason"])
	if strings.Contains(logBuf.String(), secret) {
		t.Fatal("rejected-answer log exposed submitted answer content")
	}
}

func TestHandleAskUserAnswer_AcceptedAnswerWithResumeFailureIsNotRejected(t *testing.T) {
	logBuf := captureSlogJSON(t)
	mb := bus.NewMessageBus()
	mb.Close()
	const secretAccepted = "ACCEPTED-ANSWER-DO-NOT-LOG"
	set := askSetFixture()
	set.Questions[0].Options[0].Label = secretAccepted
	set.Questions[0].Recommended = secretAccepted
	reg := askuser.NewRegistry(nil, &askUserResumeDispatcher{msgBus: mb}, askuser.Options{})
	require.NoError(t, reg.CreatePending(set))
	t.Cleanup(reg.Quiesce)

	var frame generated.AskUserAnswerFrame
	require.NoError(t, json.Unmarshal([]byte(`{
		"type":"ask_user_answer",
		"card_id":"ask_1",
		"session_id":"session_owner_1",
		"answers":[
			{"header":"Scope","selected":["ACCEPTED-ANSWER-DO-NOT-LOG"]},
			{"header":"Deploy","selected":["Staging"]}
		]
	}`), &frame))

	h := &WSHandler{askUserReg: reg}
	h.handleAskUserAnswer(&wsConn{userID: "daniel", sendCh: make(chan []byte, 1)}, frame)

	record := findAskUserGatewayLog(t, logBuf, "ws: ask_user_answer accepted; resume failed")
	assert.Equal(t, "WARN", record["level"])
	assert.Equal(t, "stranded", record["outcome"])
	assert.Equal(t, "bus_closed", record["reason"])
	if strings.Contains(logBuf.String(), `"msg":"ws: ask_user_answer rejected"`) {
		t.Fatal("accepted answer was incorrectly logged as rejected after resume failure")
	}
	if strings.Contains(logBuf.String(), secretAccepted) {
		t.Fatal("accepted-answer resume-failure log exposed answer content")
	}
}

func strpGateway(value string) *string { return &value }
