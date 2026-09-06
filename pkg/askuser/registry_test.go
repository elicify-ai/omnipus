// Omnipus — AskUserQuestion registry tests (spec Tests 2,4,5,6,7,13 backend
// halves): park-state persistence over the REAL session store (no live
// gateway), submission validation + first-valid-wins, default-safe timers +
// server all-default auto-submit + audit, cancel semantics, and restart
// re-arm.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package askuser

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// --- fakes ---

type recordedResume struct {
	Set  *PendingSet
	Text string
}

type fakeResume struct {
	mu    sync.Mutex
	calls []recordedResume
}

func (f *fakeResume) DispatchResume(set *PendingSet, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, recordedResume{Set: set, Text: text})
	return nil
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

type auditRec struct{ CardID, SessionID, Header, Label string }

type fakeAudit struct {
	mu      sync.Mutex
	entries []auditRec
}

func (f *fakeAudit) RecordAutoDefault(cardID, sessionID, header, label string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries = append(f.entries, auditRec{cardID, sessionID, header, label})
}

func (f *fakeAudit) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.entries)
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

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func strp(s string) *string { return &s }

// --- Test 3/5 backend halves: create, persistence, one-per-session, cap,
// delegated-child rejection ---

func TestCreatePending_PersistsAndEmits(t *testing.T) {
	store := newTestStore(t)
	sid := newOwnerSession(t, store)
	resume := &fakeResume{}
	sink := &fakeSink{}
	reg := NewRegistry(store, resume, Options{Sink: sink})
	t.Cleanup(reg.Quiesce)

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
	t.Cleanup(reg.Quiesce)

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
	t.Cleanup(reg.Quiesce)

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
	t.Cleanup(reg.Quiesce)
	if err := reg.CreatePending(testSet(child.ID)); !errors.Is(err, ErrDelegatedChild) {
		t.Fatalf("want ErrDelegatedChild, got %v", err)
	}
}

func TestCreatePending_InvalidQuestionsRejected(t *testing.T) {
	store := newTestStore(t)
	sid := newOwnerSession(t, store)
	reg := NewRegistry(store, &fakeResume{}, Options{})
	t.Cleanup(reg.Quiesce)
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
	t.Cleanup(reg.Quiesce)

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
	if payload.Answers[0].AutoDefault || payload.Answers[1].AutoDefault {
		t.Fatal("manual answers must not be marked auto_default")
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
	t.Cleanup(reg.Quiesce)

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
	t.Cleanup(reg.Quiesce)
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
	t.Cleanup(reg.Quiesce)
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
	t.Cleanup(reg.Quiesce)
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
	t.Cleanup(reg.Quiesce)
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

func TestTimer_AllDefaultSafe_ServerAutoSubmitsWithAudit(t *testing.T) {
	store := newTestStore(t)
	sid := newOwnerSession(t, store)
	resume := &fakeResume{}
	audit := &fakeAudit{}
	reg := NewRegistry(store, resume, Options{DefaultSafeDelay: 20 * time.Millisecond, Audit: audit})
	t.Cleanup(reg.Quiesce)

	set := testSet(sid,
		Question{Header: "Scope", Question: "Which scope?", Recommended: "Backend", DefaultSafe: true,
			Options: []Option{{Label: "Backend"}, {Label: "Full stack"}}},
		Question{Header: "Deploy", Question: "Deploy where?", MultiSelect: true, Recommended: "Staging", DefaultSafe: true,
			Options: []Option{{Label: "Staging"}, {Label: "Prod"}}},
	)
	if err := reg.CreatePending(set); err != nil {
		t.Fatalf("CreatePending: %v", err)
	}
	waitFor(t, "server auto-submit", func() bool { return resume.count() == 1 })
	if audit.count() != 2 {
		t.Fatalf("want 2 audit entries (one per auto-default), got %d", audit.count())
	}
	rec := resume.last(t)
	var payload struct {
		Status  string   `json:"status"`
		Answers []Answer `json:"answers"`
	}
	idx := strings.Index(rec.Text, "{")
	if err := json.Unmarshal([]byte(rec.Text[idx:]), &payload); err != nil {
		t.Fatalf("payload parse: %v", err)
	}
	if payload.Status != "answered" || len(payload.Answers) != 2 {
		t.Fatalf("unexpected payload: %+v", payload)
	}
	for _, a := range payload.Answers {
		if !a.AutoDefault {
			t.Fatalf("server auto-submit answers must be auto_default: %+v", a)
		}
	}
	// multi_select auto-default resolves to a ONE-ELEMENT list (spec §2).
	if len(payload.Answers[1].Selected) != 1 || payload.Answers[1].Selected[0] != "Staging" {
		t.Fatalf("multi_select auto-default must be a one-element list: %+v", payload.Answers[1])
	}
	if _, ok := reg.PendingForSession(sid); ok {
		t.Fatal("server auto-submit must consume the set")
	}
}

func TestTimer_MixedSet_ResolvesPendingSubmitButNeverServerSubmits(t *testing.T) {
	store := newTestStore(t)
	sid := newOwnerSession(t, store)
	resume := &fakeResume{}
	audit := &fakeAudit{}
	reg := NewRegistry(store, resume, Options{DefaultSafeDelay: 20 * time.Millisecond, Audit: audit})
	t.Cleanup(reg.Quiesce)

	set := testSet(sid,
		Question{Header: "Scope", Question: "Which scope?", Recommended: "Backend", DefaultSafe: true,
			Options: []Option{{Label: "Backend"}, {Label: "Full stack"}}},
		Question{Header: "Name", Question: "Name it?",
			Options: []Option{{Label: "A"}, {Label: "B"}}},
	)
	if err := reg.CreatePending(set); err != nil {
		t.Fatalf("CreatePending: %v", err)
	}
	waitFor(t, "auto-default audit entry", func() bool { return audit.count() == 1 })
	// Quiesce waits for the fired timer callback — including its persist —
	// to fully finish. Without it this test raced the callback's SetMeta
	// (a fixed sleep lost to a slow fsync) and flaked on the persistence
	// assertion below.
	reg.Quiesce()
	// The non-default-safe question keeps the card pending indefinitely
	// (US-3 S4 — the closed-tab outcome: it genuinely needs the human).
	time.Sleep(60 * time.Millisecond)
	if resume.count() != 0 {
		t.Fatal("server must NEVER submit while a non-default-safe question is unanswered")
	}
	pending, ok := reg.PendingForSession(sid)
	if !ok {
		t.Fatal("set must remain pending")
	}
	if _, resolved := pending.AutoResolved["Scope"]; !resolved {
		t.Fatal("default-safe question must be marked resolved-pending-submit")
	}
	// The resolved-pending-submit mark is persisted (restart-safe).
	meta, _ := store.GetMeta(sid)
	var persisted PendingSet
	if err := json.Unmarshal([]byte(meta.PendingAskJSON), &persisted); err != nil {
		t.Fatalf("persisted parse: %v", err)
	}
	if _, resolved := persisted.AutoResolved["Scope"]; !resolved {
		t.Fatal("auto-resolution must be persisted")
	}
	// A late client submission (the grace-submit shape: manual + auto-marked
	// answers) still wins and resumes.
	err := reg.Submit(set.CardID, sid, "", []SubmittedAnswer{
		{Header: "Scope", Selected: []string{"Backend"}, AutoDefault: true},
		{Header: "Name", Selected: []string{"A"}},
	})
	if err != nil {
		t.Fatalf("client submit after auto-resolution: %v", err)
	}
	if resume.count() != 1 {
		t.Fatalf("want exactly 1 resume, got %d", resume.count())
	}
}

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

func (c *countingMeta) sets() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.setCalls
}

// One timer per SET, not per question: all default-safe questions share the
// set's single CreatedAt+delay deadline, so their auto-resolutions must land
// as ONE batch — one persist and one card emission — never one round per
// question. (The old per-question timers produced 2 persists + 2 emissions
// for this fixture: this test fails against that behavior.)
func TestTimer_BatchResolvesAllDueHeaders_SinglePersistAndEmit(t *testing.T) {
	store := newTestStore(t)
	sid := newOwnerSession(t, store)
	meta := &countingMeta{inner: store}
	resume := &fakeResume{}
	sink := &fakeSink{}
	audit := &fakeAudit{}
	reg := NewRegistry(meta, resume, Options{DefaultSafeDelay: 20 * time.Millisecond, Sink: sink, Audit: audit})
	t.Cleanup(reg.Quiesce)

	set := testSet(sid,
		Question{Header: "Scope", Question: "Which scope?", Recommended: "Backend", DefaultSafe: true,
			Options: []Option{{Label: "Backend"}, {Label: "Full stack"}}},
		Question{Header: "Deploy", Question: "Deploy where?", Recommended: "Staging", DefaultSafe: true,
			Options: []Option{{Label: "Staging"}, {Label: "Prod"}}},
		Question{Header: "Name", Question: "Name it?",
			Options: []Option{{Label: "A"}, {Label: "B"}}},
	)
	if err := reg.CreatePending(set); err != nil {
		t.Fatalf("CreatePending: %v", err)
	}
	// Both default-safe headers resolve (audit stays per question).
	waitFor(t, "both auto-default audit entries", func() bool { return audit.count() == 2 })
	reg.Quiesce() // let the callback's persist+emit fully finish

	pending, ok := reg.PendingForSession(sid)
	if !ok {
		t.Fatal("mixed set must remain pending (Name needs the human)")
	}
	if len(pending.AutoResolved) != 2 {
		t.Fatalf("want both default-safe headers auto-resolved, got %v", pending.AutoResolved)
	}
	if resume.count() != 0 {
		t.Fatal("mixed set must never server-submit")
	}
	// ONE batch: park persist + park emission, then exactly one more of each
	// for the whole auto-resolution batch.
	if got := meta.sets(); got != 2 {
		t.Fatalf("want 2 persists (park + one batch), got %d", got)
	}
	if got := sink.count(); got != 2 {
		t.Fatalf("want 2 card emissions (park + one batch), got %d", got)
	}
}

func TestTimer_RaceWithClientSubmit_ExactlyOneWins(t *testing.T) {
	store := newTestStore(t)
	sid := newOwnerSession(t, store)
	resume := &fakeResume{}
	reg := NewRegistry(store, resume, Options{DefaultSafeDelay: 10 * time.Millisecond})
	t.Cleanup(reg.Quiesce)

	set := testSet(sid,
		Question{Header: "Scope", Question: "Which scope?", Recommended: "Backend", DefaultSafe: true,
			Options: []Option{{Label: "Backend"}, {Label: "Full stack"}}},
	)
	if err := reg.CreatePending(set); err != nil {
		t.Fatalf("CreatePending: %v", err)
	}
	// Race the client submission against the server auto-submit.
	_ = reg.Submit(set.CardID, sid, "", []SubmittedAnswer{{Header: "Scope", Selected: []string{"Full stack"}}})
	waitFor(t, "exactly one resume", func() bool { return resume.count() >= 1 })
	time.Sleep(50 * time.Millisecond) // let any second (buggy) dispatch land
	if resume.count() != 1 {
		t.Fatalf("first-valid-wins violated: %d resumes dispatched", resume.count())
	}
}

// --- Test 6 backend half: restart persistence + timer re-arm ---

func TestRestart_RearmFromPersistedState(t *testing.T) {
	baseDir := t.TempDir()
	store, err := session.NewUnifiedStore(baseDir)
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	sid := newOwnerSession(t, store)

	// Registry A parks the set with a long delay (timers never fire in-test).
	regA := NewRegistry(store, &fakeResume{}, Options{DefaultSafeDelay: time.Hour})
	set := testSet(sid,
		Question{Header: "Scope", Question: "Which scope?", Recommended: "Backend", DefaultSafe: true,
			Options: []Option{{Label: "Backend"}, {Label: "Full stack"}}},
	)
	if cerr := regA.CreatePending(set); cerr != nil {
		t.Fatalf("CreatePending: %v", cerr)
	}

	// "Restart": a NEW store over the same directory (a genuine disk
	// re-read, not the first store's in-memory cache) and a NEW registry
	// with a short delay. The persisted CreatedAt is already in the past
	// relative to the new delay, so the re-armed timer fires
	// (near-)immediately — continuity from the persisted timestamp, not a
	// fresh 30-minute clock (§0.5).
	storeB, err := session.NewUnifiedStore(baseDir)
	if err != nil {
		t.Fatalf("NewUnifiedStore (restart): %v", err)
	}
	resumeB := &fakeResume{}
	auditB := &fakeAudit{}
	regB := NewRegistry(storeB, resumeB, Options{DefaultSafeDelay: 20 * time.Millisecond, Audit: auditB})
	if err := regB.RearmSession(sid); err != nil {
		t.Fatalf("RearmSession: %v", err)
	}
	got, ok := regB.PendingForSession(sid)
	if !ok || got.CardID != set.CardID {
		t.Fatalf("re-hydrated set mismatch: %v %v", got, ok)
	}
	waitFor(t, "re-armed timer to fire and server-submit", func() bool { return resumeB.count() == 1 })
	if auditB.count() != 1 {
		t.Fatalf("want 1 audit entry after re-armed fire, got %d", auditB.count())
	}
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

func TestRearmSession_NoopWithoutPersistedSet(t *testing.T) {
	store := newTestStore(t)
	sid := newOwnerSession(t, store)
	reg := NewRegistry(store, &fakeResume{}, Options{})
	t.Cleanup(reg.Quiesce)
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
