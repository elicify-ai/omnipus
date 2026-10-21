// Omnipus — AskUserQuestion registry tests (spec Tests 2,4,5,6,7,13 backend
// halves): park-state persistence over the REAL session store (no live
// gateway), submission validation + first-valid-wins, cancel semantics, and
// restart restoration.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package askuser

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- fakes ---

type recordedResume struct {
	Set  *PendingSet
	Text string
}

type fakeResume struct {
	mu    sync.Mutex
	calls []recordedResume
	err   error
}

func (f *fakeResume) DispatchResume(set *PendingSet, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, recordedResume{Set: set, Text: text})
	return f.err
}

func (f *fakeResume) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeResume) last(t *testing.T) recordedResume {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		t.Fatal("no resume was dispatched")
	}
	return f.calls[len(f.calls)-1]
}

type fakeSink struct {
	mu    sync.Mutex
	cards []*PendingSet
}

func (f *fakeSink) EmitCard(set *PendingSet) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cards = append(f.cards, set)
}

func (f *fakeSink) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.cards)
}

// --- helpers ---

func newTestStore(t *testing.T) *session.UnifiedStore {
	t.Helper()
	store, err := session.NewUnifiedStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	return store
}

func newOwnerSession(t *testing.T, store *session.UnifiedStore) string {
	t.Helper()
	meta, err := store.NewSession(session.SessionTypeChat, "web", "mia")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return meta.ID
}

func testSet(sessionID string, qs ...Question) *PendingSet {
	if len(qs) == 0 {
		qs = []Question{{
			Header:   "Scope",
			Question: "Which scope?",
			Options:  []Option{{Label: "Backend"}, {Label: "Full stack"}},
		}}
	}
	return &PendingSet{
		CardID:              NewCardID(),
		RoutingSessionKey:   "session:" + sessionID,
		TranscriptSessionID: sessionID,
		AgentID:             "mia",
		Channel:             "web",
		ChatID:              "chat-1",
		Questions:           qs,
	}
}

func strp(s string) *string { return &s }

func captureAskUserSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return buf
}

func findAskUserRegistryLog(t *testing.T, buf *bytes.Buffer, message, cardID string) map[string]any {
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
		if record["msg"] == message && record["card_id"] == cardID {
			return record
		}
	}
	t.Fatalf("missing registry log message %q for card_id=%s (captured_record_count=%d)",
		message, cardID, recordCount)
	return nil
}

func TestLifecycleLogging_SubmitDeliveredCountsOnly(t *testing.T) {
	logBuf := captureAskUserSlog(t)
	store := newTestStore(t)
	sid := newOwnerSession(t, store)
	resume := &fakeResume{}
	reg := NewRegistry(store, resume, Options{})

	const secretSelected = "CONFIDENTIAL-SELECTED-DO-NOT-LOG"
	set := testSet(sid,
		Question{
			Header: "Scope", Question: "Which scope?",
			Options: []Option{{Label: secretSelected}, {Label: "Public option"}},
		},
		Question{
			Header: "Notes", Question: "Any notes?",
			Options: []Option{{Label: "None"}, {Label: "Add note"}},
		},
	)
	set.CardID = "ask_logging_counts"
	require.NoError(t, reg.CreatePending(set))

	secretAnswer := "CONFIDENTIAL-ANSWER-DO-NOT-LOG"
	require.NoError(t, reg.Submit(set.CardID, sid, "", []SubmittedAnswer{
		{Header: "Scope", Selected: []string{secretSelected}},
		{Header: "Notes", FreeText: &secretAnswer},
	}))

	created := findAskUserRegistryLog(t, logBuf, "askuser: card created", set.CardID)
	assert.Equal(t, "INFO", created["level"])
	assert.Equal(t, float64(2), created["question_count"])

	submitted := findAskUserRegistryLog(t, logBuf, "askuser: answer submitted", set.CardID)
	assert.Equal(t, "INFO", submitted["level"])
	assert.Equal(t, float64(2), submitted["answer_count"])
	assert.Equal(t, float64(1), submitted["selected_option_count"])
	assert.Equal(t, float64(1), submitted["free_text_count"])

	if strings.Contains(logBuf.String(), secretAnswer) || strings.Contains(logBuf.String(), secretSelected) {
		t.Fatal("AskUserQuestion lifecycle logs exposed answer content")
	}
}

func TestDispatchResume_LogsStrandedWithoutDispatcher(t *testing.T) {
	logBuf := captureAskUserSlog(t)

	stranded := testSet("session_stranded")
	stranded.CardID = "ask_stranded"
	stranded.Status = StatusAnswered
	stranded.Answers = []Answer{{Header: "Scope", FreeText: strp("STRANDED-ANSWER-DO-NOT-LOG")}}
	strandedReg := NewRegistry(nil, nil, Options{})
	require.Error(t, strandedReg.dispatchResume(stranded))
	strandedLog := findAskUserRegistryLog(t, logBuf, "askuser: resume dispatch completed", stranded.CardID)
	assert.Equal(t, "WARN", strandedLog["level"])
	assert.Equal(t, "stranded", strandedLog["outcome"])
	assert.Equal(t, "no_dispatcher", strandedLog["reason"])

	if strings.Contains(logBuf.String(), "DO-NOT-LOG") {
		t.Fatal("resume outcome logs exposed answer content")
	}
}

func TestSubmit_PersistsHumanFreeTextAsCorrelatedUserTranscript(t *testing.T) {
	store := newTestStore(t)
	sid := newOwnerSession(t, store)
	resume := &fakeResume{}
	reg := NewRegistry(store, resume, Options{})

	set := testSet(sid)
	set.CardID = "ask_human_free_text"
	require.NoError(t, reg.CreatePending(set))
	freeText := "Use the private staging environment."
	require.NoError(t, reg.Submit(set.CardID, sid, "", []SubmittedAnswer{
		{Header: "Scope", FreeText: &freeText},
	}))

	entries, err := store.ReadTranscript(sid)
	require.NoError(t, err)
	require.Len(t, entries, 1, "a human card submission must add one transcript record")
	require.Equal(t, "user", entries[0].Role)
	require.Equal(t, "mia", entries[0].AgentID)
	persisted, recognized, err := ParseResumeMessage(entries[0].Content)
	require.NoError(t, err)
	require.True(t, recognized, "the transcript record must use the canonical resume-message shape")
	require.Equal(t, set.CardID, persisted.CardID)
	require.Equal(t, StatusAnswered, persisted.Status)
	require.Len(t, persisted.Answers, 1)
	require.NotNil(t, persisted.Answers[0].FreeText)
	require.Equal(t, freeText, *persisted.Answers[0].FreeText,
		"free text typed into the card is user-authored conversation content")
}

func TestSubmit_TranscriptPersistenceUnavailableDoesNotResume(t *testing.T) {
	store := newTestStore(t)
	sid := newOwnerSession(t, store)
	metaOnly := &countingMeta{inner: store}
	resume := &fakeResume{}
	reg := NewRegistry(metaOnly, resume, Options{})

	set := testSet(sid)
	require.NoError(t, reg.CreatePending(set))
	err := reg.Submit(set.CardID, sid, "", []SubmittedAnswer{
		{Header: "Scope", Selected: []string{"Backend"}},
	})

	require.ErrorIs(t, err, ErrResumeDispatch)
	require.Zero(t, resume.count(),
		"the agent must not act on a human answer that could not be written to the transcript")
}

// --- Test 3/5 backend halves: create, persistence, one-per-session, cap,
// delegated-child rejection ---

func TestCreatePending_PersistsAndEmits(t *testing.T) {
	store := newTestStore(t)
	sid := newOwnerSession(t, store)
	resume := &fakeResume{}
	sink := &fakeSink{}
	reg := NewRegistry(store, resume, Options{Sink: sink})

	set := testSet(sid)
	if err := reg.CreatePending(set); err != nil {
		t.Fatalf("CreatePending: %v", err)
	}
	if sink.count() != 1 {
		t.Fatalf("expected 1 card emission, got %d", sink.count())
	}
	// Durable persistence into UnifiedMeta (M-R2-1).
	meta, err := store.GetMeta(sid)
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	if meta.PendingAskJSON == "" {
		t.Fatal("PendingAskJSON not persisted")
	}
	var persisted PendingSet
	if err := json.Unmarshal([]byte(meta.PendingAskJSON), &persisted); err != nil {
		t.Fatalf("persisted set does not parse: %v", err)
	}
	if persisted.CardID != set.CardID || persisted.Status != StatusPending {
		t.Fatalf("persisted set mismatch: %+v", persisted)
	}
	if got, ok := reg.PendingForSession(sid); !ok || got.CardID != set.CardID {
		t.Fatalf("PendingForSession mismatch: %v %v", got, ok)
	}
}

func TestCreatePending_OnePerRoutingSession(t *testing.T) {
	store := newTestStore(t)
	sid := newOwnerSession(t, store)
	reg := NewRegistry(store, &fakeResume{}, Options{})

	if err := reg.CreatePending(testSet(sid)); err != nil {
		t.Fatalf("first CreatePending: %v", err)
	}
	err := reg.CreatePending(testSet(sid))
	if !errors.Is(err, ErrAlreadyPending) {
		t.Fatalf("second CreatePending: want ErrAlreadyPending, got %v", err)
	}
}

func TestCreatePending_GlobalCapAcrossSessions(t *testing.T) {
	store := newTestStore(t)
	sid1 := newOwnerSession(t, store)
	sid2 := newOwnerSession(t, store)
	reg := NewRegistry(store, &fakeResume{}, Options{MaxPending: 1})

	if err := reg.CreatePending(testSet(sid1)); err != nil {
		t.Fatalf("first CreatePending: %v", err)
	}
	if err := reg.CreatePending(testSet(sid2)); !errors.Is(err, ErrSaturated) {
		t.Fatalf("want ErrSaturated, got %v", err)
	}
}

func TestCreatePending_DelegatedChildRejected(t *testing.T) {
	store := newTestStore(t)
	parent := newOwnerSession(t, store)
	child, err := store.CreateSessionWithID("session_child_ask_ec9", parent, session.SessionTypeDelegate, "web", "worker")
	if err != nil {
		t.Fatalf("CreateSessionWithID: %v", err)
	}
	// spawnSubTurn stamps the FR-008 parent edge via SetMeta right after
	// CreateSessionWithID (pkg/agent/subturn.go) — mirror that here.
	if err := store.SetMeta(child.ID, session.MetaPatch{ParentSessionID: &parent}); err != nil {
		t.Fatalf("SetMeta(ParentSessionID): %v", err)
	}
	reg := NewRegistry(store, &fakeResume{}, Options{})
	// fix(askuser): relay child questions upward (8932d09ff) made the
	// meta.ParentSessionID branch relay the question via
	// steer.UpwardDeliverer BEFORE returning ErrDelegatedChild — an unwired
	// deliverer now fails closed with its own error rather than reaching
	// ErrDelegatedChild. Production wires SetSteerAudienceResolver's
	// resolver/observer/deliverer together (registry.go's own "belt and
	// suspenders" comment: ParentSessionID and the resolver-derived
	// `steered` bool should always agree), so this test wires the same
	// recordingUpwardDeliverer registry_steer_test.go's steered-session
	// coverage uses, matching that production invariant.
	reg.SetSteerAudienceResolver(nil, nil, &recordingUpwardDeliverer{})
	if err := reg.CreatePending(testSet(child.ID)); !errors.Is(err, ErrDelegatedChild) {
		t.Fatalf("want ErrDelegatedChild, got %v", err)
	}
}

func TestCreatePending_InvalidQuestionsRejected(t *testing.T) {
	store := newTestStore(t)
	sid := newOwnerSession(t, store)
	reg := NewRegistry(store, &fakeResume{}, Options{})
	set := testSet(sid)
	set.Questions[0].Options = set.Questions[0].Options[:1]
	if err := reg.CreatePending(set); err == nil {
		t.Fatal("expected validation error")
	}
}

// --- Test 13: submission validation + first-valid-wins + stale card ---

func TestSubmit_ValidatedAndResumes(t *testing.T) {
	store := newTestStore(t)
	sid := newOwnerSession(t, store)
	resume := &fakeResume{}
	reg := NewRegistry(store, resume, Options{})

	set := testSet(sid,
		Question{Header: "Scope", Question: "Which scope?", Options: []Option{{Label: "Backend"}, {Label: "Full stack"}}},
		Question{Header: "Deploy", Question: "Deploy where?", MultiSelect: true, Options: []Option{{Label: "Staging"}, {Label: "Prod"}}},
		Question{Header: "Name", Question: "Name the feature?", Options: []Option{{Label: "A"}, {Label: "B"}}},
	)
	if err := reg.CreatePending(set); err != nil {
		t.Fatalf("CreatePending: %v", err)
	}
	err := reg.Submit(set.CardID, sid, "", []SubmittedAnswer{
		{Header: "Scope", Selected: []string{"Backend"}},
		{Header: "Deploy", Selected: []string{"Staging", "Prod"}},
		{Header: "Name", FreeText: strp("call it sparrow")},
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if resume.count() != 1 {
		t.Fatalf("want 1 resume, got %d", resume.count())
	}
	rec := resume.last(t)
	// §0.2 resume-message format, with question-text echo (o-R2-1).
	wantPrefix := "Answers to your questions (card_id=" + set.CardID + "): "
	if !strings.HasPrefix(rec.Text, wantPrefix) {
		t.Fatalf("resume text %q lacks prefix %q", rec.Text, wantPrefix)
	}
	var payload struct {
		Status  string   `json:"status"`
		Answers []Answer `json:"answers"`
	}
	if uerr := json.Unmarshal([]byte(strings.TrimPrefix(rec.Text, wantPrefix)), &payload); uerr != nil {
		t.Fatalf("resume payload does not parse: %v", uerr)
	}
	if payload.Status != "answered" || len(payload.Answers) != 3 {
		t.Fatalf("unexpected payload: %+v", payload)
	}
	if payload.Answers[0].QuestionText != "Which scope?" {
		t.Fatalf("missing question-text echo: %+v", payload.Answers[0])
	}
	if payload.Answers[2].FreeText == nil || *payload.Answers[2].FreeText != "call it sparrow" {
		t.Fatalf("free text lost: %+v", payload.Answers[2])
	}
	// Terminal record persisted (§0.6 — collapsed card renders from it).
	meta, _ := store.GetMeta(sid)
	var terminal PendingSet
	if uerr := json.Unmarshal([]byte(meta.PendingAskJSON), &terminal); uerr != nil {
		t.Fatalf("terminal record does not parse: %v", uerr)
	}
	if terminal.Status != StatusAnswered || len(terminal.Answers) != 3 {
		t.Fatalf("terminal record wrong: %+v", terminal)
	}
	// First-valid-wins: the set is consumed; a second submission is stale.
	err = reg.Submit(set.CardID, sid, "", []SubmittedAnswer{{Header: "Scope", Selected: []string{"Backend"}}})
	if !errors.Is(err, ErrNoPending) {
		t.Fatalf("late submit: want ErrNoPending, got %v", err)
	}
}

func TestSubmit_ValidationRejectsWithoutConsuming(t *testing.T) {
	store := newTestStore(t)
	sid := newOwnerSession(t, store)
	resume := &fakeResume{}
	reg := NewRegistry(store, resume, Options{})

	set := testSet(sid,
		Question{Header: "Scope", Question: "Which scope?", Options: []Option{{Label: "Backend"}, {Label: "Full stack"}}},
	)
	if err := reg.CreatePending(set); err != nil {
		t.Fatalf("CreatePending: %v", err)
	}

	cases := []struct {
		name    string
		answers []SubmittedAnswer
		wantSub string
	}{
		{"unknown header", []SubmittedAnswer{{Header: "Nope", Selected: []string{"Backend"}}}, "unknown question header"},
		{"missing answer", nil, "answer every question"},
		{"label not an option", []SubmittedAnswer{{Header: "Scope", Selected: []string{"Sideways"}}}, "not an option"},
		{"both free text and selection", []SubmittedAnswer{{Header: "Scope", Selected: []string{"Backend"}, FreeText: strp("x")}}, "mutually exclusive"},
		{"neither", []SubmittedAnswer{{Header: "Scope"}}, "needs a selection or free text"},
		{"multi selection on single-select", []SubmittedAnswer{{Header: "Scope", Selected: []string{"Backend", "Full stack"}}}, "single-select"},
		{"free text over cap", []SubmittedAnswer{{Header: "Scope", FreeText: strp(strings.Repeat("y", MaxFreeTextChars+1))}}, "exceeds 2000"},
		{"empty free text", []SubmittedAnswer{{Header: "Scope", FreeText: strp("")}}, "must not be empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := reg.Submit(set.CardID, sid, "", tc.answers)
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("want error containing %q, got %v", tc.wantSub, err)
			}
		})
	}
	// None of the invalid submissions consumed the set.
	if _, ok := reg.PendingForSession(sid); !ok {
		t.Fatal("invalid submissions must not consume the pending set")
	}
	if resume.count() != 0 {
		t.Fatal("no resume must have been dispatched")
	}
	// A valid submission still lands afterward.
	if err := reg.Submit(set.CardID, sid, "", []SubmittedAnswer{{Header: "Scope", Selected: []string{"Backend"}}}); err != nil {
		t.Fatalf("valid submit after rejections: %v", err)
	}
}

func TestSubmit_OwnershipAndSessionChecks(t *testing.T) {
	store := newTestStore(t)
	sid := newOwnerSession(t, store)
	reg := NewRegistry(store, &fakeResume{}, Options{})
	set := testSet(sid)
	set.Owner = "alice"
	if err := reg.CreatePending(set); err != nil {
		t.Fatalf("CreatePending: %v", err)
	}
	if err := reg.Submit(set.CardID, sid, "mallory", []SubmittedAnswer{{Header: "Scope", Selected: []string{"Backend"}}}); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("want ErrNotOwner, got %v", err)
	}
	if err := reg.Submit(set.CardID, "session_other", "alice", []SubmittedAnswer{{Header: "Scope", Selected: []string{"Backend"}}}); !errors.Is(err, ErrSessionMismatch) {
		t.Fatalf("want ErrSessionMismatch, got %v", err)
	}
	if err := reg.Submit("ask_stale_card", sid, "alice", []SubmittedAnswer{{Header: "Scope", Selected: []string{"Backend"}}}); !errors.Is(err, ErrNoPending) {
		t.Fatalf("stale card: want ErrNoPending, got %v", err)
	}
	if err := reg.Submit(set.CardID, sid, "alice", []SubmittedAnswer{{Header: "Scope", Selected: []string{"Backend"}}}); err != nil {
		t.Fatalf("owner submit: %v", err)
	}
}

// --- Test 7 backend half: cancels ---

func TestCancelByUser_ResumesCancelledWithoutAnswers(t *testing.T) {
	store := newTestStore(t)
	sid := newOwnerSession(t, store)
	resume := &fakeResume{}
	reg := NewRegistry(store, resume, Options{})
	set := testSet(sid)
	if err := reg.CreatePending(set); err != nil {
		t.Fatalf("CreatePending: %v", err)
	}
	if err := reg.CancelByUser(set.CardID, sid); err != nil {
		t.Fatalf("CancelByUser: %v", err)
	}
	rec := resume.last(t)
	if !strings.Contains(rec.Text, `"status":"cancelled"`) {
		t.Fatalf("resume text %q lacks cancelled status", rec.Text)
	}
	if strings.Contains(rec.Text, `"answers"`) {
		t.Fatalf("cancelled resume must carry no answers: %q", rec.Text)
	}
	if _, ok := reg.PendingForSession(sid); ok {
		t.Fatal("cancel must consume the pending set")
	}
}

func TestCancelOnSessionStop_NoResumeDispatched(t *testing.T) {
	store := newTestStore(t)
	sid := newOwnerSession(t, store)
	resume := &fakeResume{}
	reg := NewRegistry(store, resume, Options{})
	set := testSet(sid)
	if err := reg.CreatePending(set); err != nil {
		t.Fatalf("CreatePending: %v", err)
	}
	// Matched via the transcript session id (the cancel scope's key).
	if !reg.CancelOnSessionStop(sid) {
		t.Fatal("CancelOnSessionStop found nothing")
	}
	if resume.count() != 0 {
		t.Fatal("session Stop must not dispatch a resume turn")
	}
	if _, ok := reg.PendingForSession(sid); ok {
		t.Fatal("stop-cancel must consume the pending set")
	}
	// Terminal cancelled record persisted for the collapsed-card render.
	meta, _ := store.GetMeta(sid)
	var terminal PendingSet
	if err := json.Unmarshal([]byte(meta.PendingAskJSON), &terminal); err != nil {
		t.Fatalf("terminal record does not parse: %v", err)
	}
	if terminal.Status != StatusCancelled {
		t.Fatalf("want cancelled terminal record, got %q", terminal.Status)
	}
	// Idempotent no-op afterwards.
	if reg.CancelOnSessionStop(sid) {
		t.Fatal("second stop-cancel must be a no-op")
	}
}

func TestCancelOnSessionStop_MatchesRoutingKey(t *testing.T) {
	store := newTestStore(t)
	sid := newOwnerSession(t, store)
	reg := NewRegistry(store, &fakeResume{}, Options{})
	set := testSet(sid)
	if err := reg.CreatePending(set); err != nil {
		t.Fatalf("CreatePending: %v", err)
	}
	if !reg.CancelOnSessionStop(set.RoutingSessionKey) {
		t.Fatal("stop-cancel by routing key found nothing")
	}
}

// --- Test 4: timers, resolved-pending-submit, server all-default submit,
// audit, closed-tab outcomes ---

// countingMeta wraps a MetaStore and counts SetMeta calls, so tests can
// assert how many persists a code path performed.
type countingMeta struct {
	inner    MetaStore
	mu       sync.Mutex
	setCalls int
}

func (c *countingMeta) GetMeta(sessionID string) (*session.UnifiedMeta, error) {
	return c.inner.GetMeta(sessionID)
}

func (c *countingMeta) SetMeta(sessionID string, patch session.MetaPatch) error {
	c.mu.Lock()
	c.setCalls++
	c.mu.Unlock()
	return c.inner.SetMeta(sessionID, patch)
}

// ParseResumeCardID must recognize exactly what ResumeMessage renders (the
// two share resumeMessagePrefix) — the gateway's replay suppression of the
// §0.2 raw-JSON bubble depends on this round-trip.
func TestParseResumeCardID_RoundTripAndRejects(t *testing.T) {
	set := testSet("session_x")
	set.Status = StatusAnswered
	set.Answers = []Answer{{Header: "Scope", QuestionText: "Which scope?", Selected: []string{"Backend"}}}
	msg, err := ResumeMessage(set)
	if err != nil {
		t.Fatalf("ResumeMessage: %v", err)
	}
	id, ok := ParseResumeCardID(msg)
	if !ok || id != set.CardID {
		t.Fatalf("round-trip failed: got (%q, %v), want (%q, true)", id, ok, set.CardID)
	}
	for _, content := range []string{
		"",
		"hello there",
		"Answers to your questions (card_id=", // truncated, no closing paren
		"Answers to your questions (card_id=): {}", // empty card id
	} {
		if _, ok := ParseResumeCardID(content); ok {
			t.Fatalf("must reject %q", content)
		}
	}
}

// ParseResumeMessage must decode both the card id AND the JSON payload,
// mirror ParseResumeCardID's recognition exactly (ok=false for anything not
// a resume message), and distinguish "not a resume message" from "a resume
// message with a corrupt payload" (ADR-079 D3's goal-compile consumer keys
// on this to tell a stray/foreign message apart from a genuine parse fault).
func TestParseResumeMessage_DecodesPayloadAndRejects(t *testing.T) {
	set := testSet("session_x")
	set.Status = StatusAnswered
	set.Answers = []Answer{{Header: "Scope", QuestionText: "Which scope?", Selected: []string{"Backend"}}}
	msg, err := ResumeMessage(set)
	if err != nil {
		t.Fatalf("ResumeMessage: %v", err)
	}

	resume, ok, perr := ParseResumeMessage(msg)
	if !ok || perr != nil {
		t.Fatalf("round-trip failed: ok=%v err=%v", ok, perr)
	}
	if resume.CardID != set.CardID {
		t.Fatalf("CardID = %q, want %q", resume.CardID, set.CardID)
	}
	if resume.Status != StatusAnswered {
		t.Fatalf("Status = %q, want answered", resume.Status)
	}
	if len(resume.Answers) != 1 || resume.Answers[0].Header != "Scope" ||
		len(resume.Answers[0].Selected) != 1 || resume.Answers[0].Selected[0] != "Backend" {
		t.Fatalf("answers not decoded: %+v", resume.Answers)
	}

	// Not a resume message at all: ok=false, no error (mirrors ParseResumeCardID).
	for _, content := range []string{"", "hello there", "Answers to your questions (card_id="} {
		if _, gotOK, gotErr := ParseResumeMessage(content); gotOK || gotErr != nil {
			t.Fatalf("ParseResumeMessage(%q) = ok=%v err=%v, want ok=false err=nil", content, gotOK, gotErr)
		}
	}

	// A cancelled set carries no answers.
	cancelSet := testSet("session_y")
	cancelSet.Status = StatusCancelled
	cancelMsg, err := ResumeMessage(cancelSet)
	if err != nil {
		t.Fatalf("ResumeMessage (cancel): %v", err)
	}
	cancelResume, ok, perr := ParseResumeMessage(cancelMsg)
	if !ok || perr != nil {
		t.Fatalf("cancel round-trip failed: ok=%v err=%v", ok, perr)
	}
	if cancelResume.Status != StatusCancelled || len(cancelResume.Answers) != 0 {
		t.Fatalf("cancel resume = %+v, want cancelled with no answers", cancelResume)
	}

	// Recognized as a resume message (right prefix + card id) but the JSON
	// payload itself is corrupt: ok=true, err!=nil — callers must NOT treat
	// this as "not a resume message" (that would silently pass through a
	// message that really was addressed to this card).
	corrupt := "Answers to your questions (card_id=abc123): not valid json"
	_, ok, perr = ParseResumeMessage(corrupt)
	if !ok {
		t.Fatalf("a message with the right prefix/card-id shape must be recognized as a resume message (ok=true), got ok=%v", ok)
	}
	if perr == nil {
		t.Fatal("a corrupt JSON payload must return a non-nil error, not be silently accepted")
	}
}

func TestRearmSession_NoopWithoutPersistedSet(t *testing.T) {
	store := newTestStore(t)
	sid := newOwnerSession(t, store)
	reg := NewRegistry(store, &fakeResume{}, Options{})
	if err := reg.RearmSession(sid); err != nil {
		t.Fatalf("RearmSession on clean session: %v", err)
	}
	if _, ok := reg.PendingForSession(sid); ok {
		t.Fatal("nothing should have been hydrated")
	}
}

func TestRearmSession_TerminalRecordStaysTerminal(t *testing.T) {
	store := newTestStore(t)
	sid := newOwnerSession(t, store)
	resume := &fakeResume{}
	regA := NewRegistry(store, resume, Options{})
	set := testSet(sid)
	if err := regA.CreatePending(set); err != nil {
		t.Fatalf("CreatePending: %v", err)
	}
	if err := regA.Submit(set.CardID, sid, "", []SubmittedAnswer{{Header: "Scope", Selected: []string{"Backend"}}}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	// Restart over the terminal (answered) record: nothing re-hydrates as
	// pending — the record survives only for the collapsed-card render.
	regB := NewRegistry(store, &fakeResume{}, Options{})
	if err := regB.RearmSession(sid); err != nil {
		t.Fatalf("RearmSession: %v", err)
	}
	if _, ok := regB.PendingForSession(sid); ok {
		t.Fatal("a terminal record must not re-hydrate as pending")
	}
}
