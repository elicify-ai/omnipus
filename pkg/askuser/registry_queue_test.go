// Omnipus — AskUserQuestion pending-question queue tests (founder decision on
// #1329 part 2, relayed by team-lead 2026-10-16). SUPERSEDES the retired
// one-per-routing-session refusal: a second question set for a session that
// already has an active card is enqueued FIFO, never refused or replaced just
// because a card is open.
//
// RED STATUS (qa-lead, 2026-10-16): authored against the founder decision and
// pkg/askuser's documented contracts only — oracle values are NOT derived from
// any queue implementation, which does not exist yet. Backend-lead applies
// this payload unchanged and owns the RED run and its receipt. The recommended
// gate (narrow, serial, per CLAUDE.md "Build, test, and quality gates"):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestPendingQueue$' -count=1 -v -p 1 ./pkg/askuser/
//
// Skill note: per elicify-test-writing in RED, items 2 (see it green), 3
// (mutate and confirm it dies) and the Proof-of-failability checklist are
// DEFERRED TO CHECK — they are not claimed here.
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
	"github.com/stretchr/testify/require"
)

// errSentinelSetMeta is the exact sentinel the MetaStore wrapper injects for
// SetMeta failures (brief: "injects an exact sentinel failure for SetMeta").
var errSentinelSetMeta = errors.New("qa-1329: injected SetMeta failure")

// failingSetMeta wraps a MetaStore (production shape: *session.UnifiedStore)
// and injects SetMeta failures only — GetMeta and AppendTranscriptStrict are
// forwarded untouched. remaining: 0 = healthy, -1 = fail every call,
// n > 0 = fail the next n calls then recover.
type failingSetMeta struct {
	inner     MetaStore
	mu        sync.Mutex
	remaining int
}

func newFailingSetMeta(inner MetaStore) *failingSetMeta {
	return &failingSetMeta{inner: inner}
}

func (f *failingSetMeta) GetMeta(sessionID string) (*session.UnifiedMeta, error) {
	return f.inner.GetMeta(sessionID)
}

func (f *failingSetMeta) SetMeta(sessionID string, patch session.MetaPatch) error {
	f.mu.Lock()
	switch {
	case f.remaining > 0:
		f.remaining--
		f.mu.Unlock()
		return errSentinelSetMeta
	case f.remaining < 0:
		f.mu.Unlock()
		return errSentinelSetMeta
	default:
		f.mu.Unlock()
		return f.inner.SetMeta(sessionID, patch)
	}
}

// AppendTranscriptStrict forwards to the real store so human-answer resume
// records stay on the production path (transcriptRecordStore seam).
func (f *failingSetMeta) AppendTranscriptStrict(sessionID string, entry session.TranscriptEntry) error {
	store, ok := f.inner.(transcriptRecordStore)
	if !ok {
		return errors.New("askuser: failingSetMeta inner store cannot record transcript entries")
	}
	return store.AppendTranscriptStrict(sessionID, entry)
}

func (f *failingSetMeta) arm(n int) {
	f.mu.Lock()
	f.remaining = n
	f.mu.Unlock()
}

func (f *failingSetMeta) disarm() {
	f.mu.Lock()
	f.remaining = 0
	f.mu.Unlock()
}

// fakeClock is a mutex-safe controllable clock for Options.Now: tests
// advance fake time instead of waiting on wall-clock timers.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// gatedSetMeta is the channel-gated write double for the deterministic
// concurrency probe: the FIRST SetMeta caller after armGate parks inside the
// gate (signals `entered`, then blocks on `release`) and its write FAILS
// with the exact sentinel once released — a Submit parked mid-persist.
// Every OTHER SetMeta call while the gate is armed fails fast with the same
// sentinel, deterministically, with no sleeps. GetMeta and
// AppendTranscriptStrict always forward to the real store.
//
// Scope note: the fail-fast branch describes THIS DOUBLE's behavior once a
// caller REACHES SetMeta. The probe never requires a production caller to
// reach it at any particular moment — an implementation that serializes
// state-changing operations behind an operation mutex across durable I/O
// (the authorized coordinated transaction protocol) may legitimately hold
// the concurrent admission on that mutex and deliver it to the gate only
// after the parked write has resolved. That waiting is valid and must not
// be mistaken for, or reported as, a production mutex defect.
type gatedSetMeta struct {
	inner   MetaStore
	mu      sync.Mutex
	entered chan struct{}
	release chan struct{}
	parked  bool
}

func newGatedSetMeta(inner MetaStore) *gatedSetMeta {
	return &gatedSetMeta{inner: inner}
}

func (g *gatedSetMeta) armGate(entered, release chan struct{}) {
	g.mu.Lock()
	g.entered, g.release = entered, release
	g.mu.Unlock()
}

func (g *gatedSetMeta) disarm() {
	g.mu.Lock()
	g.entered, g.release, g.parked = nil, nil, false
	g.mu.Unlock()
}

func (g *gatedSetMeta) SetMeta(sessionID string, patch session.MetaPatch) error {
	g.mu.Lock()
	if g.entered == nil {
		g.mu.Unlock()
		return g.inner.SetMeta(sessionID, patch)
	}
	if !g.parked {
		g.parked = true
		g.mu.Unlock()
		close(g.entered)
		<-g.release
		return errSentinelSetMeta
	}
	g.mu.Unlock()
	return errSentinelSetMeta
}

func (g *gatedSetMeta) GetMeta(sessionID string) (*session.UnifiedMeta, error) {
	return g.inner.GetMeta(sessionID)
}

func (g *gatedSetMeta) AppendTranscriptStrict(sessionID string, entry session.TranscriptEntry) error {
	store, ok := g.inner.(transcriptRecordStore)
	if !ok {
		return errors.New("askuser: gatedSetMeta inner store cannot record transcript entries")
	}
	return store.AppendTranscriptStrict(sessionID, entry)
}

// closeUnifiedAtCleanup registers store.Close in the test's LIFO cleanup
// chain, checking the error. Fixture-lifetime correction (green-queue-pack
// run, 2026-10-16): UnifiedStore runs a background stats flusher; without
// Close the flusher outlives t.TempDir removal and logs repeated
// "stats flush failed ... stats.json.lock: no such file or directory"
// warnings for already-completed subtests. Close (stopStatsFlusher →
// flushAllDirtyStats → backend.Close) is the verified production lifecycle
// (session/unified.go::UnifiedStore.Close) — no production fix needed.
// Register this BEFORE the test's timer/registry Quiesce cleanups so LIFO
// runs Quiesce first (timer callbacks finish before the store closes) and
// the store closes before t.TempDir removes the directory.
func closeUnifiedAtCleanup(t *testing.T, store *session.UnifiedStore) {
	t.Cleanup(func() {
		require.NoError(t, store.Close(), "queue pack store.Close must succeed")
	})
}

// newQueueStore is the queue pack's store fixture: the shared newTestStore
// plus a registered, error-checked Close (see closeUnifiedAtCleanup). The
// generic newTestStore in registry_test.go is deliberately left untouched —
// it is used by older tests outside this pack's scope.
func newQueueStore(t *testing.T) *session.UnifiedStore {
	t.Helper()
	store := newTestStore(t)
	closeUnifiedAtCleanup(t, store)
	return store
}

// queueEnv bundles one registry under test with its real store and doubles.
type queueEnv struct {
	store  *session.UnifiedStore
	sid    string
	reg    *Registry
	resume *fakeResume
	sink   *fakeSink
}

// newQueueEnv builds a registry over a REAL session.UnifiedStore (t.TempDir).
// Options may override MaxPending/DefaultSafeDelay; sink and resume are the
// existing registry_test.go doubles. Every registry is cleaned up with
// Quiesce; subtests that arm default-safe timers use short delays and wait
// for them to fire so cleanup never hangs on an unresolved timer.
func newQueueEnv(t *testing.T, opts Options) *queueEnv {
	t.Helper()
	store := newQueueStore(t)
	sid := newOwnerSession(t, store)
	resume := &fakeResume{}
	sink := &fakeSink{}
	if opts.Sink == nil {
		opts.Sink = sink
	}
	reg := NewRegistry(store, resume, opts)
	t.Cleanup(reg.Quiesce)
	return &queueEnv{store: store, sid: sid, reg: reg, resume: resume, sink: sink}
}

// sinkSnapshot returns a mutex-safe copy of every emitted card.
func sinkSnapshot(f *fakeSink) []*PendingSet {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*PendingSet, len(f.cards))
	copy(out, f.cards)
	return out
}

// sinkCountWhere counts emissions matching pred.
func sinkCountWhere(f *fakeSink, pred func(*PendingSet) bool) int {
	n := 0
	for _, c := range sinkSnapshot(f) {
		if pred(c) {
			n++
		}
	}
	return n
}

// requireNoEmissionFor fails the test if any card with cardID was emitted —
// the exact "queued cards must not emit early" oracle.
func requireNoEmissionFor(t *testing.T, f *fakeSink, cardID string) {
	t.Helper()
	if n := sinkCountWhere(f, func(c *PendingSet) bool { return c.CardID == cardID }); n != 0 {
		t.Fatalf("card %s was emitted %d time(s) while it must stay hidden", cardID, n)
	}
}

// requireTerminalBeforeNext pins the emission ORDER on a resolve: the
// consumed card's terminal record is emitted BEFORE the next head's
// promotion emission (team-lead compatibility note, 2026-10-16).
func requireTerminalBeforeNext(t *testing.T, f *fakeSink, terminalID string, terminalStatus SetStatus, nextID string) {
	t.Helper()
	termIdx, nextIdx := -1, -1
	for i, c := range sinkSnapshot(f) {
		if c.CardID == terminalID && c.Status == terminalStatus && termIdx == -1 {
			termIdx = i
		}
		if c.CardID == nextID && c.Status == StatusPending && nextIdx == -1 {
			nextIdx = i
		}
	}
	if termIdx == -1 {
		t.Fatalf("no terminal emission (%s, %s) found", terminalID, terminalStatus)
	}
	if nextIdx == -1 {
		t.Fatalf("no promotion emission for next head %s found", nextID)
	}
	if termIdx > nextIdx {
		t.Fatalf("terminal emission for %s must precede promotion emission for %s (got terminal@%d > next@%d)",
			terminalID, nextID, termIdx, nextIdx)
	}
}

// answerScope is the one-answer submission matching testSet's default
// "Scope" question.
func answerScope() []SubmittedAnswer {
	return []SubmittedAnswer{{Header: "Scope", Selected: []string{"Backend"}}}
}

// TestPendingQueue is the single narrow entry point: one top-level test whose
// subtests each pin one sentence of the founder #1329 part 2 decision. Each
// subtest is self-contained (own store, own registry) and named for its
// expected outcome. Run with:
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestPendingQueue$' -count=1 -v -p 1 ./pkg/askuser/
func TestPendingQueue(t *testing.T) {
	t.Run("second_set_enqueues_hidden_and_without_early_emission", queueCase_SecondSetEnqueuesHidden)
	t.Run("three_sets_promote_strict_fifo_terminal_before_next", queueCase_ThreeSetsStrictFIFO)
	t.Run("same_content_new_card_id_is_not_deduplicated", queueCase_SameContentNotDeduplicated)
	t.Run("independent_sessions_stay_independently_active", queueCase_IndependentSessions)
	t.Run("human_answer_promotes_head_after_terminal_emission_single_resume", queueCase_HumanAnswerPromotes)
	t.Run("cancel_by_user_promotes_head_after_terminal_emission_cancelled_resume", queueCase_CancelByUserPromotes)
	t.Run("session_stop_promotes_head_without_resume", queueCase_SessionStopPromotesWithoutResume)
	t.Run("default_safe_auto_submit_promotes_head", queueCase_DefaultSafeAutoSubmitPromotes)
	t.Run("queued_default_safe_clock_starts_fresh_at_promotion", queueCase_QueuedClockStartsAtPromotion)
	t.Run("restart_rearm_preserves_seeded_queued_sets_old_reader_compatible", queueCase_RestartSeededEnvelope)
	t.Run("real_enqueue_survives_store_reopen_end_to_end", queueCase_RealEnqueueSurvivesReopen)
	t.Run("admission_before_rearm_preserves_durable_head_and_fifo", queueCase_AdmissionBeforeRearm)
	t.Run("admission_persist_failure_rejects_without_losing_queue_retry_works", queueCase_AdmissionPersistFailure)
	t.Run("answer_persist_failure_rolls_back_consumption_and_retry_works", queueCase_AnswerPersistFailure)
	t.Run("cancel_persist_failure_rolls_back_and_retry_works", queueCase_CancelPersistFailure)
	t.Run("queued_sets_count_toward_global_capacity", queueCase_CapacityCountsQueued)
	t.Run("submit_of_hidden_queued_card_rejected_without_consuming", queueCase_SubmitHiddenRejected)
	t.Run("exact_retry_of_queued_card_id_does_not_append_twice", queueCase_ExactRetryIdempotent)
	t.Run("resume_dispatch_failure_keeps_err_resume_dispatch_and_still_promotes", queueCase_ResumeFailureStillPromotes)
	t.Run("concurrent_enqueue_versus_blocked_submit_preserves_durable_accepted_work", queueCase_ConcurrentEnqueueVersusBlockedSubmit)
	t.Run("default_safe_save_failure_rolls_back_and_retry_works", queueCase_DefaultSafeSaveFailureRollsBackAndRetries)
	t.Run("session_stop_persist_failure_retains_cards_and_queue", queueCase_SessionStopPersistFailureRetainsCards)
	t.Run("failed_auto_save_auto_retries_without_manual_fire", queueCase_FailedAutoSaveAutoRetries)
}

// Founder decision: "A second new question-card set for an already-open
// session must enqueue FIFO, never replace/refuse just because a card is
// open." Only the active card is visible/answerable; queued cards must not
// emit early.
func queueCase_SecondSetEnqueuesHidden(t *testing.T) {
	env := newQueueEnv(t, Options{})
	first := testSet(env.sid)
	require.NoError(t, env.reg.CreatePending(first))
	second := testSet(env.sid)
	require.NoError(t, env.reg.CreatePending(second),
		"a second set for an open card must enqueue, not refuse (founder #1329 part 2)")

	// Queued card: invisible everywhere, never emitted.
	_, visible := env.reg.Get(second.CardID)
	require.False(t, visible, "a queued card must not be retrievable via Get before promotion")
	active, ok := env.reg.PendingForSession(env.sid)
	require.True(t, ok, "the active card must remain")
	require.Equal(t, first.CardID, active.CardID, "the active card must not be replaced")
	all := env.reg.PendingAll()
	require.Len(t, all, 1, "PendingAll must expose only the active card")
	require.Equal(t, first.CardID, all[0].CardID)
	require.Equal(t, 1, env.sink.count(), "exactly the park emission must have been emitted")
	requireNoEmissionFor(t, env.sink, second.CardID)

	// Existing json.Unmarshal(...,&PendingSet{}) readers keep seeing the
	// current ACTIVE card (additive storage candidate contract).
	meta, err := env.store.GetMeta(env.sid)
	require.NoError(t, err)
	require.NotEmpty(t, meta.PendingAskJSON)
	var oldReader PendingSet
	require.NoError(t, json.Unmarshal([]byte(meta.PendingAskJSON), &oldReader))
	require.Equal(t, first.CardID, oldReader.CardID,
		"legacy PendingSet readers must keep seeing the active card")
	require.Equal(t, StatusPending, oldReader.Status)
}

// FIFO: three sets resolve strictly in enqueue order, and each resolve emits
// the consumed card's terminal record before the next head's promotion.
func queueCase_ThreeSetsStrictFIFO(t *testing.T) {
	env := newQueueEnv(t, Options{})
	first := testSet(env.sid)
	second := testSet(env.sid)
	third := testSet(env.sid)
	require.NoError(t, env.reg.CreatePending(first))
	require.NoError(t, env.reg.CreatePending(second))
	require.NoError(t, env.reg.CreatePending(third))
	require.Equal(t, 1, env.sink.count(), "queueing must not emit queued cards")

	require.NoError(t, env.reg.Submit(first.CardID, env.sid, "", answerScope()))
	head, ok := env.reg.PendingForSession(env.sid)
	require.True(t, ok)
	require.Equal(t, second.CardID, head.CardID, "second enqueued set must be promoted first")
	requireTerminalBeforeNext(t, env.sink, first.CardID, StatusAnswered, second.CardID)

	require.NoError(t, env.reg.Submit(second.CardID, env.sid, "", answerScope()))
	head, ok = env.reg.PendingForSession(env.sid)
	require.True(t, ok)
	require.Equal(t, third.CardID, head.CardID)
	requireTerminalBeforeNext(t, env.sink, second.CardID, StatusAnswered, third.CardID)

	require.NoError(t, env.reg.Submit(third.CardID, env.sid, "", answerScope()))
	_, ok = env.reg.PendingForSession(env.sid)
	require.False(t, ok, "queue must be empty after the last card resolves")
	require.Equal(t, 3, env.resume.count(), "exactly one resume per human answer")
}

// Founder decision: "Same-workspace/same-content sets with different CardID
// values must not be deduplicated."
func queueCase_SameContentNotDeduplicated(t *testing.T) {
	env := newQueueEnv(t, Options{})
	questions := []Question{{
		Header:   "Scope",
		Question: "Which scope?",
		Options:  []Option{{Label: "Backend"}, {Label: "Full stack"}},
	}}
	first := testSet(env.sid, questions...)
	second := testSet(env.sid, questions...) // identical content, fresh CardID
	require.NotEqual(t, first.CardID, second.CardID)
	require.NoError(t, env.reg.CreatePending(first))
	require.NoError(t, env.reg.CreatePending(second),
		"same-content sets with different card ids must not be deduplicated")

	require.NoError(t, env.reg.Submit(first.CardID, env.sid, "", answerScope()))
	head, ok := env.reg.PendingForSession(env.sid)
	require.True(t, ok, "the same-content queued set must survive to promotion")
	require.Equal(t, second.CardID, head.CardID)
}

// Queueing is per routing session: another session's card is admitted ACTIVE
// immediately and is untouched by the first session's queue.
func queueCase_IndependentSessions(t *testing.T) {
	env := newQueueEnv(t, Options{})
	sid2 := newOwnerSession(t, env.store)
	first := testSet(env.sid)
	require.NoError(t, env.reg.CreatePending(first))
	other := testSet(sid2)
	require.NoError(t, env.reg.CreatePending(other),
		"a different session's set must be admitted as its own active card")

	active2, ok := env.reg.PendingForSession(sid2)
	require.True(t, ok, "an independent session's card must be active immediately")
	require.Equal(t, other.CardID, active2.CardID)
	_, visible := env.reg.Get(other.CardID)
	require.True(t, visible, "an independent session's card is not queue-hidden")
	require.Equal(t, 2, env.sink.count(), "both park emissions must have happened")

	// The first session's resolve must not disturb the other session.
	require.NoError(t, env.reg.Submit(first.CardID, env.sid, "", answerScope()))
	_, ok = env.reg.PendingForSession(env.sid)
	require.False(t, ok, "first session has no queued set; its queue is empty")
	stillActive, ok := env.reg.PendingForSession(sid2)
	require.True(t, ok)
	require.Equal(t, other.CardID, stillActive.CardID)
}

// "It is emitted/promoted on answer": resolving by human answer promotes the
// queued head, dispatches exactly one resume (for the answer, not for the
// promotion), and orders terminal-before-promotion emissions.
func queueCase_HumanAnswerPromotes(t *testing.T) {
	env := newQueueEnv(t, Options{})
	first := testSet(env.sid)
	second := testSet(env.sid)
	require.NoError(t, env.reg.CreatePending(first))
	require.NoError(t, env.reg.CreatePending(second))

	require.NoError(t, env.reg.Submit(first.CardID, env.sid, "", answerScope()))
	require.Equal(t, 1, env.resume.count(), "promotion must not dispatch a resume")
	rec := env.resume.last(t)
	require.Equal(t, first.CardID, rec.Set.CardID, "the resume must be the answered card's")
	require.True(t, strings.Contains(rec.Text, `"status":"answered"`), "resume text: %q", rec.Text)

	head, ok := env.reg.PendingForSession(env.sid)
	require.True(t, ok)
	require.Equal(t, second.CardID, head.CardID)
	_, visible := env.reg.Get(second.CardID)
	require.True(t, visible, "the promoted head must be retrievable via Get")
	requireTerminalBeforeNext(t, env.sink, first.CardID, StatusAnswered, second.CardID)
}

// Card cancellation promotes the queued head with a submission-free
// cancelled resume (no answers).
func queueCase_CancelByUserPromotes(t *testing.T) {
	env := newQueueEnv(t, Options{})
	first := testSet(env.sid)
	second := testSet(env.sid)
	require.NoError(t, env.reg.CreatePending(first))
	require.NoError(t, env.reg.CreatePending(second))

	require.NoError(t, env.reg.CancelByUser(first.CardID, env.sid))
	require.Equal(t, 1, env.resume.count())
	rec := env.resume.last(t)
	require.Equal(t, first.CardID, rec.Set.CardID)
	require.True(t, strings.Contains(rec.Text, `"status":"cancelled"`), "resume text: %q", rec.Text)
	require.False(t, strings.Contains(rec.Text, `"answers"`),
		"cancelled resume must carry no answers: %q", rec.Text)

	head, ok := env.reg.PendingForSession(env.sid)
	require.True(t, ok, "card cancellation must promote the queued head")
	require.Equal(t, second.CardID, head.CardID)
	requireTerminalBeforeNext(t, env.sink, first.CardID, StatusCancelled, second.CardID)
}

// "It is emitted/promoted on ... session-stop cancellation" and the payload
// list's "session stop promotion without resume": session Stop cancels the
// ACTIVE card (no resume turn — the user asked everything to stop) and the
// queued head is PROMOTED, not discarded. Legacy readers see the current
// active card after the promotion (additive storage contract).
func queueCase_SessionStopPromotesWithoutResume(t *testing.T) {
	env := newQueueEnv(t, Options{})
	first := testSet(env.sid)
	second := testSet(env.sid)
	require.NoError(t, env.reg.CreatePending(first))
	require.NoError(t, env.reg.CreatePending(second))

	stopOK, stopErr := env.reg.CancelOnSessionStop(env.sid)
	require.NoError(t, stopErr, "stop must cancel the session's active card")
	require.True(t, stopOK, "stop must cancel the session's active card")
	require.Equal(t, 0, env.resume.count(), "session Stop must not dispatch any resume")

	head, ok := env.reg.PendingForSession(env.sid)
	require.True(t, ok, "the queued head must be promoted on session-stop cancellation, not discarded")
	require.Equal(t, second.CardID, head.CardID)
	require.Len(t, env.reg.PendingAll(), 1, "only the promoted head is pending after stop")
	_, visible := env.reg.Get(second.CardID)
	require.True(t, visible, "the promoted head must be retrievable via Get")
	requireTerminalBeforeNext(t, env.sink, first.CardID, StatusCancelled, second.CardID)

	// Legacy readers see the CURRENT ACTIVE card after promotion
	// ("current active (or last terminal) card").
	meta, err := env.store.GetMeta(env.sid)
	require.NoError(t, err)
	var oldReader PendingSet
	require.NoError(t, json.Unmarshal([]byte(meta.PendingAskJSON), &oldReader))
	require.Equal(t, second.CardID, oldReader.CardID)
	require.Equal(t, StatusPending, oldReader.Status)

	// The promoted head is answerable; only its own answer resumes a turn.
	require.NoError(t, env.reg.Submit(second.CardID, env.sid, "", answerScope()))
	require.Equal(t, 1, env.resume.count(), "only the promoted head's own answer may resume")
}

// "It is emitted/promoted on ... default-safe expiry/auto-submit": an
// all-default-safe active card auto-submits server-side and its queued head
// is promoted; the promoted head (no default-safe questions) stays pending
// for the human.
//
// FALSE-GREEN CORRECTION (round 5, OBSERVED in red-full-queue-pack.log —
// not speculation): the earlier version used a real 40ms timer. Against the
// ORIGINAL pre-queue code at commit 2970d40d21589e57caf2e1297f58e4548ad2633f
// the timer fired BEFORE the second admission, so the "second" set was
// admitted as an ordinary new active card and this case PASSED OLD CODE
// without ever queueing anything (saved RED baseline: raw exit=1, 20 FAIL,
// 2 PASS — this case and independent-sessions were the two). Deterministic
// fix: a one-hour PHYSICAL delay so no real timer can resolve the first
// card before the second admission, the mutex-safe fake clock for all
// registry timestamps, and the fireDefaultSafeSet callback invoked directly
// as a statement — the callback IS the unit under test; the real AfterFunc
// bridge is verified independently by the legacy
// TestTimer_AllDefaultSafe_ServerAutoSubmitsWithAudit, which backend runs
// alongside this pack. The pre-fire block below proves the queue
// precondition: old code dies at the second admission, new code must show
// the queued set hidden and unemitted before anything fires.
func queueCase_DefaultSafeAutoSubmitPromotes(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)}
	store := newQueueStore(t)
	sid := newOwnerSession(t, store)
	resume := &fakeResume{}
	sink := &fakeSink{}
	reg := NewRegistry(store, resume, Options{DefaultSafeDelay: time.Hour, Now: clock.Now, Sink: sink})
	first := testSet(sid, Question{
		Header: "Scope", Question: "Which scope?", Recommended: "Backend", DefaultSafe: true,
		Options: []Option{{Label: "Backend"}, {Label: "Full stack"}},
	})
	second := testSet(sid) // no default-safe questions
	// Timer-safe teardown registered BEFORE any assertion: stop both known
	// timers, then Quiesce. The store Close (newQueueStore) is registered
	// earlier and therefore runs AFTER this in the LIFO chain.
	t.Cleanup(func() {
		reg.stopTimers(first.CardID)
		reg.stopTimers(second.CardID)
		reg.Quiesce()
	})

	// Admit first+second while the default-safe card is open. Old registry
	// code fails HERE, at the second admission.
	require.NoError(t, reg.CreatePending(first))
	require.NoError(t, reg.CreatePending(second),
		"the second set must queue while the default-safe card is open")

	// Queue precondition, BEFORE anything fires.
	active, ok := reg.PendingForSession(sid)
	require.True(t, ok)
	require.Equal(t, first.CardID, active.CardID, "the default-safe card must still be active")
	_, visible := reg.Get(second.CardID)
	require.False(t, visible, "the queued set must not be visible before promotion")
	require.Len(t, reg.PendingAll(), 1, "only the active card is pending")
	require.Equal(t, 1, sink.count(), "exactly the park emission; queueing emits nothing")
	requireNoEmissionFor(t, sink, second.CardID)

	// Advance fake time to the first card's deadline and fire its callback
	// directly (statement — compatible with the pre-change void signature,
	// and with an added error return for the scheduler's retry path).
	clock.Advance(time.Hour)
	reg.fireDefaultSafeSet(first.CardID)

	// The auto-submit resume is FIRST's, exactly once.
	require.Equal(t, 1, resume.count(), "the default-safe auto-submit dispatches exactly one resume")
	require.Equal(t, first.CardID, resume.last(t).Set.CardID,
		"the auto-submit resume must be the ACTIVE card's, not the queued set's")

	// The queued head is promoted exactly once, after the terminal emission.
	head, ok := reg.PendingForSession(sid)
	require.True(t, ok, "auto-submit of the active card must promote the queued head")
	require.Equal(t, second.CardID, head.CardID)
	require.Equal(t, 1, sinkCountWhere(sink, func(c *PendingSet) bool {
		return c.CardID == second.CardID
	}), "the promoted head must be emitted exactly once — at its own promotion, never by the active card's auto-submit")
	requireTerminalBeforeNext(t, sink, first.CardID, StatusAnswered, second.CardID)

	// The promoted head stays answerable by the human (one further resume).
	require.NoError(t, reg.Submit(second.CardID, sid, "", answerScope()))
	require.Equal(t, 2, resume.count())
}

// Founder decision: "Queued default-safe clocks start when they become
// visible (promotion), not while hidden in queue." Oracle derivation: the
// wire card's concrete default_safe_at instant is derived from the active
// set's CreatedAt plus the registry's effective delay (askuser.go
// EffectiveDefaultSafeDelay doc), so a promoted head must carry the
// PROMOTION instant as its CreatedAt — exactly, not approximately.
// Determinism: a mutex-safe fake clock supplies Options.Now; the queued set
// dwells 2x the delay in FAKE time at zero wall cost; the REAL default-safe
// delay is one hour so no real timer can fire mid-test. The fresh timer's
// effect is exercised by invoking the fireDefaultSafeSet callback directly
// as a statement — compatible with the pre-change void signature, and
// equally compatible if the implementation adds an error return for the
// scheduler's retry path. Teardown stops the long timers by known card id so even
// a failed assertion cannot hang Quiesce.
func queueCase_QueuedClockStartsAtPromotion(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)}
	store := newQueueStore(t)
	sid := newOwnerSession(t, store)
	resume := &fakeResume{}
	sink := &fakeSink{}
	reg := NewRegistry(store, resume, Options{DefaultSafeDelay: time.Hour, Now: clock.Now, Sink: sink})
	first := testSet(sid) // human-answered active card, no timers
	second := testSet(sid, Question{
		Header: "Scope", Question: "Which scope?", Recommended: "Backend", DefaultSafe: true,
		Options: []Option{{Label: "Backend"}, {Label: "Full stack"}},
	})
	t.Cleanup(func() {
		reg.stopTimers(first.CardID)
		reg.stopTimers(second.CardID)
		reg.Quiesce()
	})

	require.NoError(t, reg.CreatePending(first))
	require.NoError(t, reg.CreatePending(second))
	tEnqueue := clock.Now()
	clock.Advance(2 * time.Hour) // queue dwell far past one full delay, in fake time
	requireNoEmissionFor(t, sink, second.CardID)

	require.NoError(t, reg.Submit(first.CardID, sid, "", answerScope()))
	head, ok := reg.PendingForSession(sid)
	require.True(t, ok)
	require.Equal(t, second.CardID, head.CardID)
	require.True(t, head.CreatedAt.Equal(clock.Now()),
		"promoted head CreatedAt %v must be exactly the promotion instant %v (fresh clock), not the stale enqueue stamp %v",
		head.CreatedAt, clock.Now(), tEnqueue)

	// Nothing may auto-submit on its own while the real timer is an hour out.
	require.Equal(t, 1, resume.count(), "only the human answer resumes before the fresh deadline")

	// Fire the promoted head's fresh default-safe deadline deterministically.
	reg.fireDefaultSafeSet(second.CardID)
	require.Equal(t, 2, resume.count(), "the promoted head's fresh default-safe deadline must auto-submit it")
	_, ok = reg.PendingForSession(sid)
	require.False(t, ok, "the promoted head must be consumed by its own auto-submit")
}

// Restart RED (the distinct restart-loses shape): the additive envelope —
// active set at the top level plus an ordered queued_sets list — is seeded
// through the REAL session store, the store is genuinely reopened from disk,
// RearmSession hydrates, and resolving the head must surface the queued next
// head. Legacy readers of the same bytes must still see the active card.
// Pre-implementation this fails AFTER restart (queued_sets ignored), not at
// any admission step — no CreatePending call exists in this subtest.
func queueCase_RestartSeededEnvelope(t *testing.T) {
	baseDir := t.TempDir()
	storeA, err := session.NewUnifiedStore(baseDir)
	require.NoError(t, err)
	// Registered FIRST: LIFO cleanup runs Quiesce before this Close, and
	// closes both stores before t.TempDir removes the directory.
	closeUnifiedAtCleanup(t, storeA)
	sid := newOwnerSession(t, storeA)

	first := testSet(sid)
	first.CreatedAt = time.Now().UTC()
	first.Status = StatusPending
	second := testSet(sid)
	second.CreatedAt = time.Now().UTC()
	second.Status = StatusPending

	// seedEnvelope is the agreed internal additive storage candidate (NOT SPA
	// wire): the existing top-level PendingSet fields preserved, queued_sets
	// added as an ordered []PendingSet.
	type seedEnvelope struct {
		PendingSet
		QueuedSets []*PendingSet `json:"queued_sets,omitempty"`
	}
	seed, merr := json.Marshal(seedEnvelope{PendingSet: *first, QueuedSets: []*PendingSet{second}})
	require.NoError(t, merr)

	// Old-reader compatibility on the seeded bytes themselves.
	var oldReader PendingSet
	require.NoError(t, json.Unmarshal(seed, &oldReader))
	require.Equal(t, first.CardID, oldReader.CardID)
	require.Equal(t, StatusPending, oldReader.Status)

	require.NoError(t, storeA.SetMeta(sid, session.MetaPatch{PendingAskJSON: strp(string(seed))}))

	// Genuine disk re-read (TestRestart_RearmFromPersistedState precedent).
	storeB, err := session.NewUnifiedStore(baseDir)
	require.NoError(t, err)
	closeUnifiedAtCleanup(t, storeB)
	resumeB := &fakeResume{}
	regB := NewRegistry(storeB, resumeB, Options{})
	t.Cleanup(regB.Quiesce)
	require.NoError(t, regB.RearmSession(sid))

	active, ok := regB.PendingForSession(sid)
	require.True(t, ok)
	require.Equal(t, first.CardID, active.CardID)

	// Resolve the head; the queued next head must become visible.
	require.NoError(t, regB.Submit(first.CardID, sid, "", answerScope()))
	head, ok := regB.PendingForSession(sid)
	require.True(t, ok, "the seeded queued set must survive restart and become the next head")
	require.Equal(t, second.CardID, head.CardID)
	require.Equal(t, 1, resumeB.count(), "only the human answer dispatches a resume")
}

// Real enqueue/reopen end-to-end: the queue is produced by the REAL
// CreatePending path (no hand-seeded JSON), survives a genuine store reopen
// plus RearmSession, and promotes in FIFO order. Recovery may re-emit the
// active head — no total-emission-count assertions here; queued heads must
// stay unemitted until promotion.
func queueCase_RealEnqueueSurvivesReopen(t *testing.T) {
	baseDir := t.TempDir()
	storeA, err := session.NewUnifiedStore(baseDir)
	require.NoError(t, err)
	// Registered FIRST: LIFO cleanup runs both Quiesce registrations before
	// this Close, and closes both stores before t.TempDir removal.
	closeUnifiedAtCleanup(t, storeA)
	sid := newOwnerSession(t, storeA)
	regA := NewRegistry(storeA, &fakeResume{}, Options{})
	t.Cleanup(regA.Quiesce) // no default-safe clocks here: nothing armed, cannot hang
	first := testSet(sid)
	require.NoError(t, regA.CreatePending(first))
	second := testSet(sid)
	require.NoError(t, regA.CreatePending(second))

	storeB, err := session.NewUnifiedStore(baseDir)
	require.NoError(t, err)
	closeUnifiedAtCleanup(t, storeB)
	resumeB := &fakeResume{}
	sinkB := &fakeSink{}
	regB := NewRegistry(storeB, resumeB, Options{Sink: sinkB})
	t.Cleanup(regB.Quiesce)
	require.NoError(t, regB.RearmSession(sid))

	active, ok := regB.PendingForSession(sid)
	require.True(t, ok)
	require.Equal(t, first.CardID, active.CardID)
	_, visible := regB.Get(second.CardID)
	require.False(t, visible, "the queued set must stay hidden after reopen")
	requireNoEmissionFor(t, sinkB, second.CardID)

	require.NoError(t, regB.Submit(first.CardID, sid, "", answerScope()))
	head, ok := regB.PendingForSession(sid)
	require.True(t, ok)
	require.Equal(t, second.CardID, head.CardID)
}

// Team-lead backend-analysis note (2026-10-16): production boot rearm is
// asynchronous; the implementation lazily rehydrates under the same
// operation lock on first admission. Admission into a freshly reopened
// registry BEFORE the explicit RearmSession must not overwrite the durable
// accepted head or its FIFO queue.
func queueCase_AdmissionBeforeRearm(t *testing.T) {
	baseDir := t.TempDir()
	storeA, err := session.NewUnifiedStore(baseDir)
	require.NoError(t, err)
	// Registered FIRST: LIFO cleanup runs both Quiesce registrations before
	// this Close, and closes both stores before t.TempDir removal.
	closeUnifiedAtCleanup(t, storeA)
	sid := newOwnerSession(t, storeA)
	regA := NewRegistry(storeA, &fakeResume{}, Options{})
	t.Cleanup(regA.Quiesce) // no default-safe clocks here: nothing armed, cannot hang
	first := testSet(sid)
	require.NoError(t, regA.CreatePending(first))
	second := testSet(sid)
	require.NoError(t, regA.CreatePending(second))

	storeB, err := session.NewUnifiedStore(baseDir)
	require.NoError(t, err)
	closeUnifiedAtCleanup(t, storeB)
	resumeB := &fakeResume{}
	regB := NewRegistry(storeB, resumeB, Options{})
	t.Cleanup(regB.Quiesce)
	third := testSet(sid)
	require.NoError(t, regB.CreatePending(third),
		"admission before explicit rearm must not clobber the durable head/queue")
	require.NoError(t, regB.RearmSession(sid), "explicit rearm after lazy rehydration must stay consistent")

	active, ok := regB.PendingForSession(sid)
	require.True(t, ok)
	require.Equal(t, first.CardID, active.CardID, "the durable accepted head must survive")
	for _, hidden := range []string{second.CardID, third.CardID} {
		_, visible := regB.Get(hidden)
		require.False(t, visible, "queued card %s must stay hidden after rearm", hidden)
	}

	require.NoError(t, regB.Submit(first.CardID, sid, "", answerScope()))
	head, ok := regB.PendingForSession(sid)
	require.True(t, ok)
	require.Equal(t, second.CardID, head.CardID, "FIFO order must be preserved across rearm")
	require.NoError(t, regB.Submit(second.CardID, sid, "", answerScope()))
	head, ok = regB.PendingForSession(sid)
	require.True(t, ok)
	require.Equal(t, third.CardID, head.CardID)
	require.NoError(t, regB.Submit(third.CardID, sid, "", answerScope()))
	_, ok = regB.PendingForSession(sid)
	require.False(t, ok)
	require.Equal(t, 3, resumeB.count())
}

// "Persistence failure must not acknowledge acceptance, consume the active
// card, lose the queue, emit a new head, or dispatch a resume; retries must
// work." Admission whose queue-persist write fails is rejected; the already
// accepted queue survives untouched and a retry succeeds.
func queueCase_AdmissionPersistFailure(t *testing.T) {
	store := newQueueStore(t)
	sid := newOwnerSession(t, store)
	meta := newFailingSetMeta(store)
	resume := &fakeResume{}
	sink := &fakeSink{}
	reg := NewRegistry(meta, resume, Options{Sink: sink})
	t.Cleanup(reg.Quiesce)

	first := testSet(sid)
	require.NoError(t, reg.CreatePending(first))
	second := testSet(sid)
	require.NoError(t, reg.CreatePending(second), "precondition: queue admission succeeds while the store is healthy")

	// Block every SetMeta and attempt a third admission.
	meta.arm(-1)
	third := testSet(sid)
	err := reg.CreatePending(third)
	require.Error(t, err, "a persistence failure must not acknowledge acceptance")
	require.ErrorIs(t, err, errSentinelSetMeta)
	_, ok := reg.Get(third.CardID)
	require.False(t, ok, "the rejected set must not be visible")
	active, ok := reg.PendingForSession(sid)
	require.True(t, ok)
	require.Equal(t, first.CardID, active.CardID, "the active card must be untouched")
	require.Equal(t, 1, sink.count(), "no emission may follow the failed admission")
	requireNoEmissionFor(t, sink, third.CardID)

	// The queue must not have lost the earlier accepted set, and the rejected
	// admission must be retryable: the SAME set is admitted on retry and
	// queues behind the earlier accepted set.
	meta.disarm()
	require.NoError(t, reg.CreatePending(third), "the rejected admission must succeed on retry")
	require.NoError(t, reg.Submit(first.CardID, sid, "", answerScope()))
	head, ok := reg.PendingForSession(sid)
	require.True(t, ok)
	require.Equal(t, second.CardID, head.CardID, "the earlier queued set must be promoted first, preserving FIFO")
	require.NoError(t, reg.Submit(second.CardID, sid, "", answerScope()))
	head, ok = reg.PendingForSession(sid)
	require.True(t, ok)
	require.Equal(t, third.CardID, head.CardID)
	require.NoError(t, reg.Submit(third.CardID, sid, "", answerScope()))
	_, ok = reg.PendingForSession(sid)
	require.False(t, ok)
	require.Equal(t, 3, resume.count(), "exactly one resume per successful submission")
}

// "terminal/promotion write failure on answer": a SetMeta failure while
// persisting the answer's terminal record must NOT acknowledge acceptance,
// must NOT consume the active card, must NOT promote the queued head, and
// must NOT dispatch a resume; the retry then works end to end.
func queueCase_AnswerPersistFailure(t *testing.T) {
	store := newQueueStore(t)
	sid := newOwnerSession(t, store)
	meta := newFailingSetMeta(store)
	resume := &fakeResume{}
	sink := &fakeSink{}
	reg := NewRegistry(meta, resume, Options{Sink: sink})
	t.Cleanup(reg.Quiesce)

	first := testSet(sid)
	require.NoError(t, reg.CreatePending(first))
	second := testSet(sid)
	require.NoError(t, reg.CreatePending(second))

	meta.arm(-1)
	err := reg.Submit(first.CardID, sid, "", answerScope())
	require.ErrorIs(t, err, errSentinelSetMeta,
		"a failed terminal persist must surface the exact injected failure, not be acknowledged")
	active, ok := reg.PendingForSession(sid)
	require.True(t, ok, "the active card must NOT be consumed")
	require.Equal(t, first.CardID, active.CardID)
	require.Equal(t, StatusPending, active.Status, "the active card must roll back to pending")
	_, visible := reg.Get(second.CardID)
	require.False(t, visible, "the queued head must NOT be promoted")
	require.Equal(t, 0, resume.count(), "no resume may follow a failed terminal persist")
	require.Equal(t, 0, sinkCountWhere(sink, func(c *PendingSet) bool {
		return c.CardID == first.CardID && c.Status == StatusAnswered
	}), "no answered terminal record may be emitted after a failed persist")
	require.Equal(t, 0, sinkCountWhere(sink, func(c *PendingSet) bool {
		return c.CardID == second.CardID
	}), "no promotion emission may follow a failed persist")

	meta.disarm()
	require.NoError(t, reg.Submit(first.CardID, sid, "", answerScope()), "the retry must work")
	require.Equal(t, 1, resume.count())
	head, ok := reg.PendingForSession(sid)
	require.True(t, ok)
	require.Equal(t, second.CardID, head.CardID)
}

// The cancel twin: a SetMeta failure while persisting the cancellation must
// not acknowledge, consume, promote, or resume; the retry works.
func queueCase_CancelPersistFailure(t *testing.T) {
	store := newQueueStore(t)
	sid := newOwnerSession(t, store)
	meta := newFailingSetMeta(store)
	resume := &fakeResume{}
	sink := &fakeSink{}
	reg := NewRegistry(meta, resume, Options{Sink: sink})
	t.Cleanup(reg.Quiesce)

	first := testSet(sid)
	require.NoError(t, reg.CreatePending(first))
	second := testSet(sid)
	require.NoError(t, reg.CreatePending(second))

	meta.arm(-1)
	err := reg.CancelByUser(first.CardID, sid)
	require.ErrorIs(t, err, errSentinelSetMeta,
		"a failed cancel persist must surface the exact injected failure, not be acknowledged")
	active, ok := reg.PendingForSession(sid)
	require.True(t, ok, "the active card must NOT be consumed")
	require.Equal(t, first.CardID, active.CardID)
	require.Equal(t, StatusPending, active.Status, "the active card must roll back to pending")
	_, visible := reg.Get(second.CardID)
	require.False(t, visible, "the queued head must NOT be promoted")
	require.Equal(t, 0, resume.count(), "no resume may follow a failed cancel persist")
	require.Equal(t, 0, sinkCountWhere(sink, func(c *PendingSet) bool {
		return c.CardID == first.CardID && c.Status == StatusCancelled
	}), "no cancelled terminal record may be emitted after a failed persist")

	meta.disarm()
	require.NoError(t, reg.CancelByUser(first.CardID, sid), "the retry must work")
	require.Equal(t, 1, resume.count())
	head, ok := reg.PendingForSession(sid)
	require.True(t, ok)
	require.Equal(t, second.CardID, head.CardID)
	require.True(t, strings.Contains(resume.last(t).Text, `"status":"cancelled"`))
}

// "Existing global capacity/error gates remain; count waiting sets toward
// capacity." With MaxPending=2, one active + one queued exhaust the cap; a
// DIFFERENT session's first card is then rejected with ErrSaturated.
func queueCase_CapacityCountsQueued(t *testing.T) {
	env := newQueueEnv(t, Options{MaxPending: 2})
	sid2 := newOwnerSession(t, env.store)
	first := testSet(env.sid)
	require.NoError(t, env.reg.CreatePending(first), "cap boundary: 1 of 2 admitted")
	second := testSet(env.sid)
	require.NoError(t, env.reg.CreatePending(second), "cap boundary: 2 of 2, the queued set counts toward capacity")

	overflow := testSet(sid2)
	err := env.reg.CreatePending(overflow)
	require.ErrorIs(t, err, ErrSaturated,
		"the existing global cap gate must stay and count queued waiting sets")
	_, ok := env.reg.Get(overflow.CardID)
	require.False(t, ok, "a cap-rejected set must not be visible")
}

// "Only the active card ... is answerable": Submit naming a hidden queued
// card id is rejected with the existing unknown-card semantics, consumes
// nothing, and leaves both the active card and the queue intact.
func queueCase_SubmitHiddenRejected(t *testing.T) {
	env := newQueueEnv(t, Options{})
	first := testSet(env.sid)
	second := testSet(env.sid)
	require.NoError(t, env.reg.CreatePending(first))
	require.NoError(t, env.reg.CreatePending(second))

	err := env.reg.Submit(second.CardID, env.sid, "", answerScope())
	require.ErrorIs(t, err, ErrNoPending,
		"a queued (hidden) card id must not be answerable")
	_, ok := env.reg.PendingForSession(env.sid)
	require.True(t, ok, "the active card must be untouched")
	require.Equal(t, 0, env.resume.count())

	// The queue is intact: the normal order still promotes the queued set.
	require.NoError(t, env.reg.Submit(first.CardID, env.sid, "", answerScope()))
	head, ok := env.reg.PendingForSession(env.sid)
	require.True(t, ok)
	require.Equal(t, second.CardID, head.CardID)
}

// Founder decision: "Exact retries of the same already-queued CardID may be
// idempotent (do not append twice)." The mandated invariant is NO DUPLICATE
// APPEND, asserted behaviorally: a duplicate would surface as a second
// promotion of the same card. The retry's error return is deliberately
// unpinned — the brief permits either nil or an idempotent-refusal error.
func queueCase_ExactRetryIdempotent(t *testing.T) {
	env := newQueueEnv(t, Options{})
	first := testSet(env.sid)
	require.NoError(t, env.reg.CreatePending(first))
	second := testSet(env.sid)
	require.NoError(t, env.reg.CreatePending(second))

	retry := second.Clone() // identical CardID and content
	_ = env.reg.CreatePending(retry)

	require.NoError(t, env.reg.Submit(first.CardID, env.sid, "", answerScope()))
	head, ok := env.reg.PendingForSession(env.sid)
	require.True(t, ok)
	require.Equal(t, second.CardID, head.CardID)

	// Resolving the promoted head must empty the queue — a duplicate append
	// would promote the same card a second time instead.
	require.NoError(t, env.reg.Submit(second.CardID, env.sid, "", answerScope()))
	_, ok = env.reg.PendingForSession(env.sid)
	require.False(t, ok, "an exact retry must not have appended the card twice")
	require.Empty(t, env.reg.PendingAll())
}

// Founder decision: "Resume dispatch failure after successful durable
// consumption still retains existing ErrResumeDispatch semantics and must
// not block promotion."
func queueCase_ResumeFailureStillPromotes(t *testing.T) {
	env := newQueueEnv(t, Options{})
	env.resume.err = errors.New("simulated dispatcher outage")
	first := testSet(env.sid)
	second := testSet(env.sid)
	require.NoError(t, env.reg.CreatePending(first))
	require.NoError(t, env.reg.CreatePending(second))

	err := env.reg.Submit(first.CardID, env.sid, "", answerScope())
	require.ErrorIs(t, err, ErrResumeDispatch, "existing resume-dispatch failure semantics must be retained")

	// Promotion is NOT blocked by the dispatcher outage.
	head, ok := env.reg.PendingForSession(env.sid)
	require.True(t, ok, "promotion must not be blocked by a resume dispatch failure")
	require.Equal(t, second.CardID, head.CardID)
	require.Equal(t, 1, env.resume.count(), "only the answered card's dispatch attempt")

	// Recovery: the promoted head answers normally.
	env.resume.err = nil
	require.NoError(t, env.reg.Submit(second.CardID, env.sid, "", answerScope()))
	require.Equal(t, 2, env.resume.count())
}

// Team-lead probe — concurrent failure safety under a blocked durable
// write. A Submit is parked mid-persist inside the channel-gated SetMeta;
// while it is parked, an admission is STARTED concurrently in its own
// goroutine. The release is closed WITHOUT waiting for the enqueue to
// return: a valid implementation serializes state-changing operations with
// an operation mutex across durable I/O (never the map mutex), so the
// concurrent admission may legitimately WAIT on that mutex and reach the
// gate only after the blocked write has resolved. The wrapper's fail-fast
// applies once a caller reaches SetMeta; the test demands nothing about
// WHEN that happens and treats the waiting itself as valid — it is never
// reported as a production mutex defect.
//
// After both attempts join, each must have failed with the exact sentinel
// while the fault gate was still armed; neither may be acknowledged; the
// durable accepted work (active card, earlier queued set) must be retained;
// and on retry the earlier accepted set must promote first. The interleaving
// points are the gate's `entered` channel and the enqueue's `started`
// channel, never a sleep; the only timeout is a safety valve converting a
// never-entered gate into a loud diagnostic.
//
// Honest scope: this is a SMOKE CHECK of concurrent failure safety with one
// forced interleaving — not a proof that all lost-update schedules were
// forced. A deterministic successful-write lost-update test remains a CHECK
// gap (payload 4, known gaps).
func queueCase_ConcurrentEnqueueVersusBlockedSubmit(t *testing.T) {
	store := newQueueStore(t)
	sid := newOwnerSession(t, store)
	meta := newGatedSetMeta(store)
	resume := &fakeResume{}
	sink := &fakeSink{}
	reg := NewRegistry(meta, resume, Options{Sink: sink})
	t.Cleanup(func() { meta.disarm(); reg.Quiesce() })

	first := testSet(sid)
	require.NoError(t, reg.CreatePending(first))
	second := testSet(sid)
	require.NoError(t, reg.CreatePending(second), "durable accepted work: the earlier queued set")

	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	// Cleanup registration precedes every assertion: on any failure the
	// parked write is still unblocked, so neither the gate nor an operation
	// waiting on it can hang teardown.
	defer releaseOnce.Do(func() { close(release) })
	meta.armGate(entered, release)

	// The Submit parks inside its terminal persist's SetMeta.
	submitDone := make(chan error, 1)
	go func() { submitDone <- reg.Submit(first.CardID, sid, "", answerScope()) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		releaseOnce.Do(func() { close(release) })
		t.Fatal("the blocked Submit never reached its persist")
	}

	// The concurrent admission is STARTED while that write is parked — in
	// its own goroutine, because a valid implementation may hold it on the
	// operation mutex until the blocked write resolves.
	third := testSet(sid)
	enqueueStarted := make(chan struct{})
	enqueueDone := make(chan error, 1)
	go func() {
		close(enqueueStarted)
		enqueueDone <- reg.CreatePending(third)
	}()
	<-enqueueStarted

	// Release the blocked write WITHOUT waiting for the enqueue to return.
	releaseOnce.Do(func() { close(release) })

	// Join both goroutines BEFORE any teardown or disarm path runs, then
	// assert both exact sentinels while the fault gate is still armed.
	errSubmit := <-submitDone
	errEnqueue := <-enqueueDone
	require.ErrorIs(t, errSubmit, errSentinelSetMeta,
		"the parked Submit's failed persist must surface, not be acknowledged")
	require.ErrorIs(t, errEnqueue, errSentinelSetMeta,
		"the concurrent enqueue must fail with the exact injected sentinel")

	// Neither attempt consumed durable accepted work.
	active, ok := reg.PendingForSession(sid)
	require.True(t, ok)
	require.Equal(t, first.CardID, active.CardID)
	require.Equal(t, StatusPending, active.Status)
	_, visible := reg.Get(second.CardID)
	require.False(t, visible, "the original queued set must be retained")
	_, visible = reg.Get(third.CardID)
	require.False(t, visible, "a failed admission was never accepted; it must not appear")
	require.Equal(t, 0, resume.count(), "failed writes dispatch no resumes")
	require.Equal(t, 0, sinkCountWhere(sink, func(c *PendingSet) bool {
		return c.CardID == first.CardID && c.Status == StatusAnswered
	}), "no answered terminal emission may follow a failed write")
	requireNoEmissionFor(t, sink, third.CardID)

	// Disarm, then retry valid actions: FIFO promotes the EARLIER accepted
	// set only — the never-accepted third is not in the queue.
	meta.disarm()
	require.NoError(t, reg.Submit(first.CardID, sid, "", answerScope()))
	head, ok := reg.PendingForSession(sid)
	require.True(t, ok)
	require.Equal(t, second.CardID, head.CardID)
	require.NoError(t, reg.Submit(second.CardID, sid, "", answerScope()))
	_, ok = reg.PendingForSession(sid)
	require.False(t, ok)
	require.Equal(t, 2, resume.count(),
		"the failed attempts dispatch no resumes; the two successful submissions dispatch exactly one each")
}

// Correction 5a (backend review 2026-10-16): the SERVER auto-submit save
// path under a failed persist. The registry's real default-safe timer is one
// hour out so nothing fires on its own; the callback is invoked directly
// (statement call — compatible with the pre-change void signature, and with
// an added error return for the scheduler's retry path) against an armed
// SetMeta fault. The failed
// save must consume nothing, promote nothing, emit nothing, and dispatch no
// resume; the retry after the fault clears completes the auto-submit and
// promotes the queued head.
func queueCase_DefaultSafeSaveFailureRollsBackAndRetries(t *testing.T) {
	store := newQueueStore(t)
	sid := newOwnerSession(t, store)
	meta := newFailingSetMeta(store)
	resume := &fakeResume{}
	sink := &fakeSink{}
	reg := NewRegistry(meta, resume, Options{DefaultSafeDelay: time.Hour, Sink: sink})
	first := testSet(sid, Question{
		Header: "Scope", Question: "Which scope?", Recommended: "Backend", DefaultSafe: true,
		Options: []Option{{Label: "Backend"}, {Label: "Full stack"}},
	})
	second := testSet(sid) // no default-safe questions
	t.Cleanup(func() {
		meta.disarm()
		reg.stopTimers(first.CardID)
		reg.stopTimers(second.CardID)
		reg.Quiesce()
	})

	require.NoError(t, reg.CreatePending(first)) // arms a 1-hour real timer
	require.NoError(t, reg.CreatePending(second))

	meta.arm(-1)
	reg.fireDefaultSafeSet(first.CardID) // statement call, pre-change-void-signature compatible
	active, ok := reg.PendingForSession(sid)
	require.True(t, ok, "the active card must NOT be consumed by a failed auto-submit save")
	require.Equal(t, first.CardID, active.CardID)
	require.Equal(t, StatusPending, active.Status)
	_, visible := reg.Get(second.CardID)
	require.False(t, visible, "the queued head must NOT be promoted")
	require.Equal(t, 0, resume.count(), "no resume may follow a failed auto-submit save")
	require.Equal(t, 0, sinkCountWhere(sink, func(c *PendingSet) bool {
		return c.CardID == first.CardID && c.Status == StatusAnswered
	}), "no answered terminal record may be emitted after a failed save")
	requireNoEmissionFor(t, sink, second.CardID)

	// Retry after the fault clears: the auto-submit completes and the queued
	// head promotes.
	meta.disarm()
	reg.fireDefaultSafeSet(first.CardID)
	require.Equal(t, 1, resume.count(), "the retried auto-submit must dispatch exactly one resume")
	head, ok := reg.PendingForSession(sid)
	require.True(t, ok)
	require.Equal(t, second.CardID, head.CardID)
}

// Correction 5b (backend review 2026-10-16): a session stop whose
// cancellation cannot be persisted must NOT be acknowledged —
// CancelOnSessionStop returns false, the active card and its queue are
// retained untouched, nothing is emitted or resumed. After the fault clears
// the stop succeeds and promotes the queued head, still without a resume.
func queueCase_SessionStopPersistFailureRetainsCards(t *testing.T) {
	store := newQueueStore(t)
	sid := newOwnerSession(t, store)
	meta := newFailingSetMeta(store)
	resume := &fakeResume{}
	sink := &fakeSink{}
	reg := NewRegistry(meta, resume, Options{Sink: sink})
	t.Cleanup(func() { meta.disarm(); reg.Quiesce() })

	first := testSet(sid)
	require.NoError(t, reg.CreatePending(first))
	second := testSet(sid)
	require.NoError(t, reg.CreatePending(second))

	meta.arm(-1)
	stopOK, stopErr := reg.CancelOnSessionStop(sid)
	require.False(t, stopOK,
		"a stop whose cancellation cannot be persisted must not be acknowledged")
	require.ErrorIs(t, stopErr, errSentinelSetMeta,
		"the failed stop must surface the EXACT underlying save error to the caller")
	active, ok := reg.PendingForSession(sid)
	require.True(t, ok, "the active card must be retained")
	require.Equal(t, first.CardID, active.CardID)
	require.Equal(t, StatusPending, active.Status)
	_, visible := reg.Get(second.CardID)
	require.False(t, visible, "the queue must be retained unchanged")
	require.Equal(t, 0, resume.count())
	require.Equal(t, 0, sinkCountWhere(sink, func(c *PendingSet) bool {
		return c.CardID == first.CardID && c.Status == StatusCancelled
	}), "no cancelled terminal emission may follow a failed stop")

	meta.disarm()
	stopOK, stopErr = reg.CancelOnSessionStop(sid)
	require.NoError(t, stopErr, "the stop retry must succeed")
	require.True(t, stopOK, "the stop retry must succeed")
	require.Equal(t, 0, resume.count(), "stop still dispatches no resume")
	head, ok := reg.PendingForSession(sid)
	require.True(t, ok)
	require.Equal(t, second.CardID, head.CardID)
}

// Round-6 control note: a failed AUTOMATIC save must retain active+queue
// AND be retried automatically by the scheduler — never silently dropped.
// Case 21 cannot detect a removed retry schedule: it fires the callback
// manually a second time, so a manual-only implementation would satisfy it.
// This case fires ONCE and then waits for the scheduler's own retry (the
// planned retry runs about one second later; the existing 5s waitFor safety
// window applies and the wall duration is deliberately NOT asserted). This
// is asynchronous-scheduler proof via deterministic polling, not a
// loop/sleep as a logic oracle. RED/mutation value: on the ORIGINAL code
// this case dies at the second admission; deleting the automatic retry
// scheduling dies at the waitFor below.
func queueCase_FailedAutoSaveAutoRetries(t *testing.T) {
	store := newQueueStore(t)
	sid := newOwnerSession(t, store)
	meta := newFailingSetMeta(store)
	resume := &fakeResume{}
	sink := &fakeSink{}
	reg := NewRegistry(meta, resume, Options{DefaultSafeDelay: time.Hour, Sink: sink})
	first := testSet(sid, Question{
		Header: "Scope", Question: "Which scope?", Recommended: "Backend", DefaultSafe: true,
		Options: []Option{{Label: "Backend"}, {Label: "Full stack"}},
	})
	second := testSet(sid) // no default-safe questions
	// Faults disarmed, known timers stopped, Quiesce — registered BEFORE any
	// assertion; the store Close (newQueueStore) is registered earlier and
	// therefore runs AFTER this in the LIFO chain.
	t.Cleanup(func() {
		meta.disarm()
		reg.stopTimers(first.CardID)
		reg.stopTimers(second.CardID)
		reg.Quiesce()
	})

	require.NoError(t, reg.CreatePending(first))
	require.NoError(t, reg.CreatePending(second),
		"the second set must queue while the default-safe card is open")

	// Fault coordination: SetMeta fails for EVERY call until disarm, so the
	// scheduler's automatic retry cannot land (and succeed) while the
	// negative assertions below are still being made — a heavily descheduled
	// run must not observe a successful one-second retry mid-assert. The
	// behavioral oracle is unchanged; only the fault window is deterministic.
	meta.arm(-1)
	reg.fireDefaultSafeSet(first.CardID) // the one and only manual invocation

	// The failed save must retain active+queue and stay silent.
	active, ok := reg.PendingForSession(sid)
	require.True(t, ok, "the failed auto-save must retain the active card")
	require.Equal(t, first.CardID, active.CardID)
	require.Equal(t, StatusPending, active.Status)
	_, visible := reg.Get(second.CardID)
	require.False(t, visible, "the queued head must stay hidden")
	require.Equal(t, 0, resume.count(), "no resume may follow a failed auto-save")
	require.Equal(t, 0, sinkCountWhere(sink, func(c *PendingSet) bool {
		return c.CardID == first.CardID && c.Status == StatusAnswered
	}), "no answered terminal emission may follow a failed auto-save")
	requireNoEmissionFor(t, sink, second.CardID)

	// NOW let writes through: NO second manual fire — the scheduler's
	// automatic retry must complete the auto-submit on the recovered store.
	meta.disarm()
	waitFor(t, "the scheduler's automatic retry of the failed auto-save", func() bool {
		return resume.count() == 1
	})
	require.Equal(t, first.CardID, resume.last(t).Set.CardID,
		"the retried auto-submit's resume must be the ACTIVE card's, not the queued set's")
	head, ok := reg.PendingForSession(sid)
	require.True(t, ok, "the automatic retry must promote the queued head")
	require.Equal(t, second.CardID, head.CardID)
	require.Equal(t, 1, sinkCountWhere(sink, func(c *PendingSet) bool {
		return c.CardID == second.CardID
	}), "the promoted head must be emitted exactly once — at its own promotion")
	requireTerminalBeforeNext(t, sink, first.CardID, StatusAnswered, second.CardID)
}
