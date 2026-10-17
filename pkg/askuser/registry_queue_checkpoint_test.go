// Omnipus — RED payload #1343/#1329 round1, file 3 of the backend QA pack.
// Target location when applied: pkg/askuser/registry_queue_checkpoint_test.go
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// RED STATUS (qa-lead, 2026-10-16, revision 2 after backend round-2
// review): authored against the founder #1329 FIFO decision and the
// team-lead F4 Decision A addendum only. Revision-2 corrections — all
// internal oracle/lifetime flaws, none adapts an expectation to production:
//   - Case (d): the proposed ForgetDeletedSession API is asserted BEFORE any
//     timer is armed; cleanup registers stopTimers for every known card id
//     plus Quiesce BEFORE any assertion. Round 1 armed two one-hour timers
//     and cleaned up with a bare Quiesce: Quiesce tracks SCHEDULED timers
//     (each scheduling adds a WaitGroup slot; only firing or stopTimers
//     releases it), so it would have hung up to an hour on both RED (method
//     absent) and GREEN (the intentionally retained other-session timer).
//     The round-1 comment claiming Quiesce ignores scheduled timers was
//     false and is corrected here.
//   - Case (c): B's own timer may legitimately fire before Submit(A)'s
//     return is observed, so the immediate "exactly one resume, and it is
//     A" read was a fabricated dispatch-order contract. The case now waits
//     for the EVENT pair — C active AND exactly two resumes — then asserts
//     the per-card resume identities as an UNORDERED SET (A answered,
//     B answered with all-default answers) and keeps the card-frame
//     emission counts and statuses exact. The 300 ms inverse quiet window is
//     replaced by reg.Quiesce() — a REAL tracked-callback-completion barrier
//     (C has no timer; B's callback has completed once C was promoted) —
//     after which exact counts are asserted. No widened waits.
//   - Case (b): the partial mark is asserted with its EXACT timestamp: the
//     fake clock is advanced one hour before the deadline callback fires and
//     the mark instant must survive the reopen byte-for-byte (time.Equal),
//     not just the header key. regB defensively stops timers on every
//     failure path (if the mark were not restored, the restored head would
//     arm a one-hour timer).
//   - NEW case (e): no phantom recovery — after a CONFIRMED store deletion,
//     the deleted session is absent from ListSessions and an explicit
//     RearmSession(deleted) may report not-found but must install NO state
//     (no head, no queue, no emission).
//
// Cases (a)-(c) anchor the checkpoint semantics; some may already pass on
// the base commit — the pack's RED core is (d) and (e). Backend-lead applies
// this payload unchanged and owns the RED run and its receipt. Recommended
// narrow gate (serial):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestPendingQueueCheckpoints$' -count=1 -v -p 1 ./pkg/askuser/
//
// Skill note: per elicify-test-writing in RED, items 2 (see it green), 3
// (mutate and confirm it dies) and the Proof-of-failability checklist are
// DEFERRED TO CHECK — they are not claimed here.
//
// Fixture note: this file is IN package askuser and reuses the queue pack's
// helpers (newQueueStore, newOwnerSession, testSet, fakeResume, fakeSink,
// sinkSnapshot, sinkCountWhere, requireNoEmissionFor, waitFor, answerScope,
// fakeClock, closeUnifiedAtCleanup from registry_queue_test.go /
// registry_test.go). No existing helper or assertion is modified.

package askuser

import (
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// TestPendingQueueCheckpoints is the single narrow entry point: one
// top-level test whose subtests each pin one sentence of the founder FIFO
// decision or the F4 deletion decision.
func TestPendingQueueCheckpoints(t *testing.T) {
	t.Run("durable_remaining_queue_checkpoint_survives_reopen", checkpointCase_ReopenRemainingFIFO)
	t.Run("partial_default_safe_checkpoint_persists_and_full_answer_promotes", checkpointCase_PartialDefaultSafeCheckpoint)
	t.Run("promoted_card_timer_fires_without_manual_callback", checkpointCase_PromotedCardTimerAutoFires)
	t.Run("forget_deleted_session_clears_head_fifo_timers_memory_only", checkpointCase_ForgetDeletedSession)
	t.Run("no_phantom_recovery_after_deletion", checkpointCase_NoPhantomRecovery)
}

// checkpointDefaultSafeScopeQuestion is the one-question all-default-safe set used by
// the timer cases (same shape as the queue pack's timer cases).
func checkpointDefaultSafeScopeQuestion() Question {
	return Question{
		Header: "Scope", Question: "Which scope?", Recommended: "Backend", DefaultSafe: true,
		Options: []Option{{Label: "Backend"}, {Label: "Full stack"}},
	}
}

// checkpointResumeFor returns the recorded resume dispatched for one card
// id, failing if none was.
func checkpointResumeFor(t *testing.T, resume *fakeResume, cardID string) recordedResume {
	t.Helper()
	resume.mu.Lock()
	defer resume.mu.Unlock()
	for _, rec := range resume.calls {
		if rec.Set.CardID == cardID {
			return rec
		}
	}
	t.Fatalf("no resume was dispatched for card %s (dispatched=%d)", cardID, len(resume.calls))
	return recordedResume{}
}

// checkpointResumeCountFor counts the resumes dispatched for one card id.
func checkpointResumeCountFor(resume *fakeResume, cardID string) int {
	resume.mu.Lock()
	defer resume.mu.Unlock()
	count := 0
	for _, rec := range resume.calls {
		if rec.Set.CardID == cardID {
			count++
		}
	}
	return count
}

// checkpointCase_ReopenRemainingFIFO pins the durable checkpoint: after A is
// answered, a NEW store instance and a NEW registry re-armed over the SAME
// durable directory must restore B active, C hidden, and never resurrect A;
// answering B then C drains the queue in order. The checkpoint is the
// DURABLE remaining queue, not live-only state. (No default-safe question
// here: nothing is ever armed, so cleanup cannot hang.)
func checkpointCase_ReopenRemainingFIFO(t *testing.T) {
	baseDir := t.TempDir()
	storeA, err := session.NewUnifiedStore(baseDir)
	require.NoError(t, err)
	closeUnifiedAtCleanup(t, storeA)
	sid := newOwnerSession(t, storeA)
	regA := NewRegistry(storeA, &fakeResume{}, Options{})
	t.Cleanup(regA.Quiesce)

	first := testSet(sid)
	second := testSet(sid)
	third := testSet(sid)
	require.NoError(t, regA.CreatePending(first))
	require.NoError(t, regA.CreatePending(second))
	require.NoError(t, regA.CreatePending(third))
	require.NoError(t, regA.Submit(first.CardID, sid, "", answerScope()))

	// Reopen: NEW store + NEW registry over the SAME directory (precedent:
	// queueCase_RealEnqueueSurvivesReopen).
	storeB, err := session.NewUnifiedStore(baseDir)
	require.NoError(t, err)
	closeUnifiedAtCleanup(t, storeB)
	resumeB := &fakeResume{}
	sinkB := &fakeSink{}
	regB := NewRegistry(storeB, resumeB, Options{Sink: sinkB})
	t.Cleanup(regB.Quiesce)
	require.NoError(t, regB.RearmSession(sid))

	active, ok := regB.PendingForSession(sid)
	require.True(t, ok, "the durable remaining queue must re-arm with B as head")
	require.Equal(t, second.CardID, active.CardID, "B must be the restored head")
	_, visible := regB.Get(second.CardID)
	require.True(t, visible, "the restored head must be retrievable via Get")
	_, visible = regB.Get(first.CardID)
	require.False(t, visible, "the answered card A must never return after reopen")
	_, visible = regB.Get(third.CardID)
	require.False(t, visible, "C must stay hidden behind the restored head")
	requireNoEmissionFor(t, sinkB, third.CardID)
	emitted := sinkSnapshot(sinkB)
	require.Len(t, emitted, 1, "re-arm must emit exactly the restored head")
	require.Equal(t, second.CardID, emitted[0].CardID)

	require.NoError(t, regB.Submit(second.CardID, sid, "", answerScope()))
	head, ok := regB.PendingForSession(sid)
	require.True(t, ok, "answering B must promote C")
	require.Equal(t, third.CardID, head.CardID)
	require.NoError(t, regB.Submit(third.CardID, sid, "", answerScope()))
	_, ok = regB.PendingForSession(sid)
	require.False(t, ok, "answering C must empty the queue")
	require.Equal(t, 2, resumeB.count(), "exactly one resume per reopened human answer, never for promotion")
}

// checkpointCase_PartialDefaultSafeCheckpoint pins the PARTIAL default-safe
// checkpoint: a mixed default-safe/human-required set auto-resolves ONLY its
// default-safe header (never auto-submitting the set), and that exact
// automatic-resolution mark — header AND timestamp — must survive a
// store+registry reopen, after which a valid FULL human answer promotes the
// waiting set. The fake clock is advanced the configured hour before the
// deadline callback is invoked directly, so the expected mark instant is
// derived, not observed.
func checkpointCase_PartialDefaultSafeCheckpoint(t *testing.T) {
	baseDir := t.TempDir()
	storeA, err := session.NewUnifiedStore(baseDir)
	require.NoError(t, err)
	closeUnifiedAtCleanup(t, storeA)
	sid := newOwnerSession(t, storeA)
	initialFakeTime := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: initialFakeTime}
	regA := NewRegistry(storeA, &fakeResume{}, Options{DefaultSafeDelay: time.Hour, Now: clock.Now})
	mixed := checkpointMixedSet(sid)
	waiting := testSet(sid) // non-default-safe: never arms a timer
	// Cleanup registered BEFORE any assertion and BEFORE the fault/danger
	// window (LIFO: stopTimers + Quiesce run before the store Close the
	// early-registered closeUnifiedAtCleanup performs). stopTimers is
	// defensive on EVERY failure path: whatever happened mid-case, no
	// scheduled one-hour timer can hang the cleanup.
	t.Cleanup(func() {
		regA.stopTimers(mixed.CardID)
		regA.stopTimers(waiting.CardID)
		regA.Quiesce()
	})

	require.NoError(t, regA.CreatePending(mixed))
	require.NoError(t, regA.CreatePending(waiting))

	// Advance the fake clock across the configured hour, then fire the
	// deadline callback directly. The requirement's mark is stamped AT THE
	// RESOLUTION/DEADLINE INSTANT, so the expected instant is the initial
	// fake time PLUS the configured hour — derived from the fixture's own
	// arithmetic, never observed from output.
	//
	// Round-5 fixture correction (measured contradictory-clock failure in
	// green-queue-checkpoints.log; an arithmetic consistency fix, not a
	// product finding): the previous snapshot took clock.Now() BEFORE the
	// advance and then expected that pre-advance reading as the mark —
	// wanting 09:00 while the callback correctly stamped 10:00. The clock is
	// now advanced FIRST and the expected instant derived as
	// initialFakeTime+delay, landing exactly on what the callback stamps.
	deadlineInstant := initialFakeTime.Add(time.Hour)
	clock.Advance(time.Hour)
	require.True(t, clock.Now().Equal(deadlineInstant),
		"fixture: the advance must land exactly on the configured deadline; got %s want %s", clock.Now(), deadlineInstant)
	regA.fireDefaultSafeSet(mixed.CardID)
	partially, ok := regA.PendingForSession(sid)
	require.True(t, ok, "a partial resolution must leave the set pending (never auto-submit a human-required question)")
	require.Equal(t, mixed.CardID, partially.CardID)
	require.Equal(t, StatusPending, partially.Status)
	require.Equal(t, []string{"Scope"}, autoResolvedHeaders(partially),
		"exactly the default-safe header carries the automatic resolution mark")
	stamped, marked := partially.AutoResolved["Scope"]
	require.True(t, marked, "the mark must exist")
	require.True(t, stamped.Equal(deadlineInstant),
		"the mark timestamp must be the registry's clock reading at the deadline: want %s got %s", deadlineInstant, stamped)
	_, visible := regA.Get(waiting.CardID)
	require.False(t, visible, "the waiting set stays hidden behind the partially resolved head")

	// Reopen: the mark is DURABLE — a new store+registry restore A still
	// pending with the exact same mark (header AND instant), B still hidden.
	storeB, err := session.NewUnifiedStore(baseDir)
	require.NoError(t, err)
	closeUnifiedAtCleanup(t, storeB)
	resumeB := &fakeResume{}
	sinkB := &fakeSink{}
	regB := NewRegistry(storeB, resumeB, Options{Sink: sinkB})
	// Defensive on EVERY failure path: if the mark were NOT restored below,
	// the restored head would arm a real one-hour timer here.
	t.Cleanup(func() {
		regB.stopTimers(mixed.CardID)
		regB.stopTimers(waiting.CardID)
		regB.Quiesce()
	})
	require.NoError(t, regB.RearmSession(sid))

	restored, ok := regB.PendingForSession(sid)
	require.True(t, ok, "the partially resolved head must survive the reopen")
	require.Equal(t, mixed.CardID, restored.CardID)
	require.Equal(t, StatusPending, restored.Status)
	require.Equal(t, []string{"Scope"}, autoResolvedHeaders(restored),
		"the automatic-resolution mark is part of the durable checkpoint, not live-only state")
	restoredStamp, restoredMarked := restored.AutoResolved["Scope"]
	require.True(t, restoredMarked, "the restored mark must exist")
	require.True(t, restoredStamp.Equal(deadlineInstant),
		"the mark TIMESTAMP must survive the reopen exactly: want %s got %s", deadlineInstant, restoredStamp)
	_, visible = regB.Get(waiting.CardID)
	require.False(t, visible, "the waiting set must still be hidden after the reopen")

	// A valid FULL human answer (every question answered exactly once)
	// consumes the restored head and promotes the waiting set.
	require.NoError(t, regB.Submit(mixed.CardID, sid, "", []SubmittedAnswer{
		{Header: "Scope", Selected: []string{"Backend"}},
		{Header: "Deploy", Selected: []string{"Staging"}},
	}))
	head, ok := regB.PendingForSession(sid)
	require.True(t, ok, "the full answer must promote the waiting set")
	require.Equal(t, waiting.CardID, head.CardID)
	require.Equal(t, 1, resumeB.count(), "exactly one resume for the full human answer")
}

// checkpointMixedSet builds the mixed set: one default-safe question + one
// human-required multi-select question.
func checkpointMixedSet(sessionID string) *PendingSet {
	return testSet(sessionID,
		checkpointDefaultSafeScopeQuestion(),
		Question{
			Header: "Deploy", Question: "Deploy where?", MultiSelect: true,
			Options: []Option{{Label: "Staging"}, {Label: "Prod"}},
		})
}

// autoResolvedHeaders returns the sorted header keys of a set's
// automatic-resolution mark (sorted for a deterministic exact-match oracle).
func autoResolvedHeaders(set *PendingSet) []string {
	headers := make([]string, 0, len(set.AutoResolved))
	for header := range set.AutoResolved {
		headers = append(headers, header)
	}
	sort.Strings(headers)
	return headers
}

// checkpointCase_PromotedCardTimerAutoFires pins the promotion-timer bridge:
// a promoted all-default-safe head schedules and expires WITHOUT any manual
// callback invocation, auto-submits itself, and promotes the next queued
// set. The queue precondition (B hidden, nothing armed) is established
// BEFORE any resolve and uses NO timer at all: the head A has no
// default-safe question, so nothing can fire early; the short physical delay
// (60 ms) exists only AFTER promotion.
//
// Dispatch ORDER between A's human-answer resume and B's auto-submit resume
// is deliberately NOT a contract: B's own timer may fire the moment its
// promotion lands (even before A's dispatch is observed), so the case waits
// for the EVENT PAIR — C active AND exactly two resumes — and then asserts
// the per-card resume identities as an UNORDERED SET, with B's shape exact.
// Card-frame emission counts and statuses remain exact. The duplicate-proof
// is reg.Quiesce() — the registry's real tracked-callback-completion barrier
// (C has no timer; B's callback has completed once C was promoted) — after
// which exact counts are asserted. No quiet window, no widened wait.
func checkpointCase_PromotedCardTimerAutoFires(t *testing.T) {
	store := newQueueStore(t)
	sid := newOwnerSession(t, store)
	resume := &fakeResume{}
	sink := &fakeSink{}
	reg := NewRegistry(store, resume, Options{DefaultSafeDelay: 60 * time.Millisecond, Sink: sink})
	t.Cleanup(func() {
		reg.Quiesce()
	})

	head := testSet(sid)                                          // not default-safe: arms nothing
	autoSet := testSet(sid, checkpointDefaultSafeScopeQuestion()) // all-default-safe, queued behind A
	behind := testSet(sid)                                        // waits behind the auto-submitting set; not default-safe
	require.NoError(t, reg.CreatePending(head))
	require.NoError(t, reg.CreatePending(autoSet))
	require.NoError(t, reg.CreatePending(behind))

	// Queue precondition, timer-free by construction: A never arms a timer,
	// so no physical delay is needed here and none is asserted.
	_, visible := reg.Get(autoSet.CardID)
	require.False(t, visible, "the all-default-safe set must be hidden behind the open head before promotion")
	requireNoEmissionFor(t, sink, autoSet.CardID)
	requireNoEmissionFor(t, sink, behind.CardID)
	require.Equal(t, 3, reg.PendingCount(), "all three sets count toward the checkpoint")

	// Resolve A. B's own promoted timer auto-submits and promotes C — no
	// manual fireDefaultSafeSet anywhere in this case — and either resume may
	// be observed first, so the oracle is the EVENT PAIR: C active AND
	// exactly two resumes.
	require.NoError(t, reg.Submit(head.CardID, sid, "", answerScope()))
	waitFor(t, "the promoted set's own timer to auto-submit, dispatch its resume, and promote the next set (C active, exactly two resumes)", func() bool {
		active, ok := reg.PendingForSession(sid)
		return ok && active.CardID == behind.CardID && resume.count() == 2
	})

	// Per-card resume identities as an UNORDERED SET: exactly one A resume
	// (the human answer) and exactly one B resume (the server auto-submit,
	// all-default shape exact).
	require.Equal(t, 1, checkpointResumeCountFor(resume, head.CardID),
		"exactly one resume may exist for the human-answered set A")
	require.Equal(t, 1, checkpointResumeCountFor(resume, autoSet.CardID),
		"exactly one resume may exist for the auto-submitted set B")
	resumeA := checkpointResumeFor(t, resume, head.CardID)
	require.True(t, strings.Contains(resumeA.Text, `"status":"answered"`), "A resume text: %q", resumeA.Text)
	resumeB := checkpointResumeFor(t, resume, autoSet.CardID)
	require.Equal(t, StatusAnswered, resumeB.Set.Status, "B's resume is the server auto-submit")
	require.NotEmpty(t, resumeB.Set.Answers, "the server auto-submit carries the built default answers")
	for _, answer := range resumeB.Set.Answers {
		require.True(t, answer.AutoDefault, "every server auto-submit answer is marked auto_default")
		require.Equal(t, []string{"Backend"}, answer.Selected, "the auto answer is the recommended label")
	}

	// Post-event state: C active and retrievable, B consumed.
	_, visible = reg.Get(behind.CardID)
	require.True(t, visible, "the finally-promoted set C is active and retrievable")
	_, visible = reg.Get(autoSet.CardID)
	require.False(t, visible, "the auto-submitted set is consumed")

	// No duplicate: Quiesce is the registry's OWN tracked-callback barrier —
	// C has no timer, and B's callback has completed (its resume and C's
	// promotion were observed), so this returns only when every scheduled
	// timer's work has settled. After it, the counts must be exact.
	reg.Quiesce()
	require.Equal(t, 2, resume.count(), "after the tracked callback barrier exactly two resumes may exist — a duplicate auto-submit is a defect")
	require.Equal(t, 1, sinkCountWhere(sink, func(c *PendingSet) bool {
		return c.CardID == autoSet.CardID && c.Status == StatusPending
	}), "the promoted set must have been emitted as pending exactly once — never re-emitted")
	require.Equal(t, 1, sinkCountWhere(sink, func(c *PendingSet) bool {
		return c.CardID == autoSet.CardID && c.Status == StatusAnswered
	}), "the auto-submitted set must carry exactly one answered terminal emission")
}

// checkpointCountSetMeta wraps the REAL concrete store and counts the
// SetMeta calls that reach it — the memory-only oracle for the
// deletion-forget case. Everything else forwards untouched.
//
// Round-5 fixture correction (measured compile error, mirrored from
// backend's fixture-only source fix): the inner field and constructor
// parameter are the concrete *session.UnifiedStore — the transcript seam
// (AppendTranscriptStrict) is NOT part of the askuser.MetaStore interface,
// so an interface-typed field could not forward it. Production MetaStore is
// NOT widened for a test; every call site supplies the real concrete store.
// No assertion changed.
type checkpointCountSetMeta struct {
	inner     *session.UnifiedStore
	mu        sync.Mutex
	forwarded int
}

func newCheckpointCountSetMeta(inner *session.UnifiedStore) *checkpointCountSetMeta {
	return &checkpointCountSetMeta{inner: inner}
}

func (c *checkpointCountSetMeta) GetMeta(sessionID string) (*session.UnifiedMeta, error) {
	return c.inner.GetMeta(sessionID)
}

func (c *checkpointCountSetMeta) SetMeta(sessionID string, patch session.MetaPatch) error {
	c.mu.Lock()
	c.forwarded++
	c.mu.Unlock()
	return c.inner.SetMeta(sessionID, patch)
}

// AppendTranscriptStrict forwards to the real store (transcriptRecordStore
// seam) — irrelevant to this case, but the seam must stay honest.
func (c *checkpointCountSetMeta) AppendTranscriptStrict(sessionID string, entry session.TranscriptEntry) error {
	return c.inner.AppendTranscriptStrict(sessionID, entry)
}

func (c *checkpointCountSetMeta) forwardedCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.forwarded
}

// checkpointCase_ForgetDeletedSession pins the REGISTRY half of team-lead F4
// Decision A: after a CONFIRMED store.DeleteSession,
// Registry.ForgetDeletedSession removes the deleted session's COMPLETE
// question state — active head, complete FIFO, and every armed timer —
// memory-only (no store write, no resume, no card emission), and is
// idempotent; every other session's state and timers are untouched.
//
// Lifetime discipline (round-2 correction): the proposed API is asserted
// BEFORE anything is armed, and the cleanup registers stopTimers for every
// known card id plus Quiesce BEFORE any assertion. Quiesce tracks SCHEDULED
// timers — each scheduling adds a WaitGroup slot and only firing or
// stopTimers releases it — so a bare Quiesce over a still-scheduled
// one-hour timer would hang the cleanup; stopTimers releases those slots
// deterministically. The store-side invocation (which delete surface fires
// the observer, after which lock, panic containment) is pinned by the
// pkg/session payload and the per-path payloads; this case needs only the
// registry method to exist.
func checkpointCase_ForgetDeletedSession(t *testing.T) {
	store := newQueueStore(t)
	sid := newOwnerSession(t, store)
	other := newOwnerSession(t, store)
	counting := newCheckpointCountSetMeta(store)
	resume := &fakeResume{}
	sink := &fakeSink{}
	reg := NewRegistry(counting, resume, Options{DefaultSafeDelay: time.Hour, Sink: sink})

	// Instrument/precondition RED FIRST, before any timer exists: the
	// proposed method asserted at RUNTIME so this pack always compiles;
	// absence is a loud block, never a skip.
	type sessionForgetter interface{ ForgetDeletedSession(sessionID string) }
	forget, ok := any(reg).(sessionForgetter)
	if !ok {
		t.Fatal("BLOCKED: Registry.ForgetDeletedSession(string) is not implemented — required by team-lead F4 Decision A: confirmed deletion must remove ALL active+queued question state and timers")
	}

	mine := testSet(sid, checkpointDefaultSafeScopeQuestion())       // active, timer armed
	queued := testSet(sid)                                           // hidden behind the head
	otherSet := testSet(other, checkpointDefaultSafeScopeQuestion()) // another session's active, timer armed
	require.NoError(t, reg.CreatePending(mine))
	require.NoError(t, reg.CreatePending(queued))
	require.NoError(t, reg.CreatePending(otherSet))

	// Cleanup BEFORE any assertion: stop every known timer (releasing the
	// scheduled WaitGroup slots — including the other session's intentionally
	// retained one, which is cleanup-only, never an oracle) and only then
	// Quiesce. LIFO: this runs before the store Close.
	t.Cleanup(func() {
		reg.stopTimers(mine.CardID)
		reg.stopTimers(queued.CardID)
		reg.stopTimers(otherSet.CardID)
		reg.Quiesce()
	})

	// The deletion is CONFIRMED through the real store first.
	require.NoError(t, store.DeleteSession(sid))

	writesBefore := counting.forwardedCount()
	sinkBefore := sink.count()
	resumesBefore := resume.count()
	forget.ForgetDeletedSession(sid)

	require.Equal(t, writesBefore, counting.forwardedCount(),
		"the forget is memory-only: no SetMeta write may follow the confirmed deletion")
	require.Equal(t, resumesBefore, resume.count(), "the forget dispatches no resume")
	require.Equal(t, sinkBefore, sink.count(), "the forget emits no card")
	_, visible := reg.Get(mine.CardID)
	require.False(t, visible, "the deleted session's head must be forgotten")
	_, visible = reg.Get(queued.CardID)
	require.False(t, visible, "the deleted session's queued set must be forgotten with the head — ALL state")
	_, ok = reg.PendingForSession(sid)
	require.False(t, ok, "no pending state may remain reachable for the deleted session")
	require.Equal(t, 1, reg.PendingCount(), "only the other session's set survives")

	// Timers: the deleted session's timer is stopped AND dropped; the other
	// session's timer is untouched. (In-package check of the private map —
	// the gateway/agent packages cannot see it, which is exactly why the
	// timer proof lives here.)
	require.Empty(t, reg.timers[mine.CardID], "the deleted session's timer must be stopped and dropped")
	require.NotEmpty(t, reg.timers[otherSet.CardID], "another session's armed timer must be untouched")

	// Idempotence: a repeated forget of the same session is a safe no-op.
	forget.ForgetDeletedSession(sid)
	require.Equal(t, 1, reg.PendingCount(), "a repeated forget changes nothing")
	_, visible = reg.Get(otherSet.CardID)
	require.True(t, visible, "the other session's card is still active after both forgets")
}

// checkpointCase_NoPhantomRecovery pins the F4 boot note: a deleted session
// is absent from ListSessions, and an explicit RearmSession(deleted) may
// report not-found but must install NO state — no head, no queue, no
// emission, nothing a fresh boot could resurrect.
func checkpointCase_NoPhantomRecovery(t *testing.T) {
	baseDir := t.TempDir()
	store, err := session.NewUnifiedStore(baseDir)
	require.NoError(t, err)
	closeUnifiedAtCleanup(t, store)
	sid := newOwnerSession(t, store)
	regA := NewRegistry(store, &fakeResume{}, Options{})
	t.Cleanup(regA.Quiesce)

	head := testSet(sid)
	queued := testSet(sid)
	require.NoError(t, regA.CreatePending(head))
	require.NoError(t, regA.CreatePending(queued))

	// The deletion is CONFIRMED through the real store.
	require.NoError(t, store.DeleteSession(sid))

	// Boot precondition: the deleted session is absent from the listing.
	metas, err := store.ListSessions()
	require.NoError(t, err)
	for _, meta := range metas {
		require.NotEqual(t, sid, meta.ID, "a deleted session must be absent from ListSessions")
	}

	// An explicit re-arm of the DELETED session may report not-found, but it
	// must install NOTHING: no head, no queue, no emission. Its error (or
	// nil) is deliberately not asserted — only the installed state is.
	regB := NewRegistry(store, &fakeResume{}, Options{Sink: &fakeSink{}})
	t.Cleanup(regB.Quiesce)
	_ = regB.RearmSession(sid)
	_, ok := regB.PendingForSession(sid)
	require.False(t, ok, "RearmSession of a deleted session must install no reachable head")
	require.Equal(t, 0, regB.PendingCount(), "RearmSession of a deleted session must install no queue")
}

// TestCancelOnSessionStopForCardTarget pins the round-4 F1 target-identity
// contract at the registry layer (round-6 corrected): the BOUND
// CancelOnSessionStopForCard(key, expectedCardID) cancels ONLY if the exact
// captured ID is still pending. A captured id that no longer matches the
// live head (including an expected-EMPTY capture with a new head present)
// and a NONEMPTY captured id whose head vanished (or an unknown key) are
// typed no-pending/head-changed errors WITHOUT mutation — the no-op is
// reported, never silent. Only an EMPTY captured id with no head (known or
// unknown key) is the normal false,nil no-op. The legacy UNBOUND
// CancelOnSessionStop keeps its own no-card semantics on an unknown key —
// a separate control, deliberately unchanged. The Stop-side capture order
// (capture before durable work, carried into RequestCancel) is pinned
// end-to-end by the pkg/agent stale-capture case; this layer pins the exact
// semantics the Stop relies on.
//
// RED shape: the proposed method is asserted at RUNTIME (comma-ok interface
// assertion, never a compile error). The exact sentinel identity of the
// typed error is the implementer's to name — this pack asserts the
// OBSERVABLE contract (false + non-nil error + zero mutation) and CHECK
// verifies a typed sentinel exists rather than a bare fmt error.
func TestCancelOnSessionStopForCardTarget(t *testing.T) {
	store := newQueueStore(t)
	sid := newOwnerSession(t, store)
	resume := &fakeResume{}
	sink := &fakeSink{}
	reg := NewRegistry(store, resume, Options{Sink: sink})
	t.Cleanup(reg.Quiesce) // non-default-safe questions only: nothing is ever armed

	type cardCancelFor interface {
		CancelOnSessionStopForCard(key, expectedCardID string) (bool, error)
	}
	forCard, ok := any(reg).(cardCancelFor)
	if !ok {
		t.Fatal("BLOCKED: Registry.CancelOnSessionStopForCard(key, expectedCardID string) (bool, error) is not implemented — required by the round-4 F1 target-identity decision (a fresh Stop must cancel exactly the head it captured)")
	}

	head := testSet(sid)
	queued := testSet(sid)
	require.NoError(t, reg.CreatePending(head))
	require.NoError(t, reg.CreatePending(queued))

	// Exact capture: cancels the captured head, promotes the queued set.
	cancelled, err := forCard.CancelOnSessionStopForCard(sid, head.CardID)
	require.NoError(t, err, "an exact captured-head cancel succeeds")
	require.True(t, cancelled, "an exact captured-head cancel reports true")
	active, still := reg.PendingForSession(sid)
	require.True(t, still, "the queued set is promoted")
	require.Equal(t, queued.CardID, active.CardID)
	require.Equal(t, 0, checkpointResumeCountFor(resume, head.CardID)+checkpointResumeCountFor(resume, queued.CardID),
		"a session-stop cancel dispatches no resume")
	require.Equal(t, 1, sinkCountWhere(sink, func(c *PendingSet) bool {
		return c.CardID == head.CardID && c.Status == StatusCancelled
	}), "exactly one cancelled record for the captured head")

	// Stale capture: the consumed head's id no longer matches the live head —
	// refuse WITHOUT mutation, with an error.
	cancelled, err = forCard.CancelOnSessionStopForCard(sid, head.CardID)
	require.False(t, cancelled, "a stale captured id must not report success")
	require.Error(t, err, "a stale captured id must surface the head-changed error")
	headAfter, headOK := reg.PendingForSession(sid)
	require.True(t, headOK, "a refused stale cancel must not mutate the live head")
	require.Equal(t, queued.CardID, headAfter.CardID)
	require.Equal(t, StatusPending, headAfter.Status)
	require.Equal(t, 0, sinkCountWhere(sink, func(c *PendingSet) bool {
		return c.CardID == queued.CardID && c.Status == StatusCancelled
	}), "the live head must carry no cancelled record after a refused stale cancel")

	// Expected-EMPTY capture with a live head present: refuse without
	// mutation.
	cancelled, err = forCard.CancelOnSessionStopForCard(sid, "")
	require.False(t, cancelled, "an expected-empty capture with a live head must not report success")
	require.Error(t, err, "an expected-empty capture with a live head must surface the mismatch error")
	_, headOK = reg.PendingForSession(sid)
	require.True(t, headOK, "an expected-empty refusal must not mutate the live head")

	// Exact capture of the NEW head: the fresh explicit stop cancels it.
	cancelled, err = forCard.CancelOnSessionStopForCard(sid, queued.CardID)
	require.NoError(t, err, "a fresh exact capture of the new head succeeds")
	require.True(t, cancelled, "a fresh exact capture of the new head reports true")
	require.Equal(t, 0, reg.PendingCount(), "the queue is empty after the fresh exact cancel")

	// Normal no-op (round-6 boundary, bound method): EMPTY expected id with
	// NO head — false, nil — over a known empty key AND over an entirely
	// unknown key. Only this shape is the normal no-op.
	cancelled, err = forCard.CancelOnSessionStopForCard(sid, "")
	require.NoError(t, err, "empty expectation with no head is the normal no-op")
	require.False(t, cancelled, "empty expectation with no head reports false")
	cancelled, err = forCard.CancelOnSessionStopForCard("session:never-existed", "")
	require.NoError(t, err, "empty expectation over an unknown key is the normal no-op")
	require.False(t, cancelled, "empty expectation over an unknown key reports false")

	// BOUND, NONEMPTY expected id with NO head / unknown key (round-6 user
	// rule): the captured target can no longer be found — the no-op is
	// REPORTED as a typed mismatch/no-pending error, never silent. Importing
	// the legacy unbound method's silent no-card semantics here would hide a
	// vanished previously-captured target; no tombstone is invented to tell
	// never-existed from deleted. (The sentinel's NAME — runtime evidence:
	// typed ErrStopTargetChanged — is the implementer's; CHECK verifies a
	// typed sentinel exists rather than a bare formatted error.) No
	// mutation, no emission, no resume.
	cancelled, err = forCard.CancelOnSessionStopForCard(sid, "ask_missing")
	require.False(t, cancelled, "a nonempty expected id with no head reports false")
	require.Error(t, err,
		"a nonempty expected id with no head must surface the typed mismatch/no-pending error, never a silent no-op")
	cancelled, err = forCard.CancelOnSessionStopForCard("session:never-existed", "ask_missing")
	require.False(t, cancelled, "a nonempty expected id over an unknown key reports false")
	require.Error(t, err,
		"a nonempty expected id over an unknown key must surface the typed mismatch/no-pending error, never the legacy silent false")

	// The refused calls changed nothing: no pending state appeared, no new
	// cancelled emission beyond the two exact captures, and nothing resumed.
	require.Equal(t, 0, reg.PendingCount())
	require.Equal(t, 2, sinkCountWhere(sink, func(c *PendingSet) bool {
		return c.Status == StatusCancelled
	}), "exactly the two exactly-captured cards carry cancelled records")
	require.Equal(t, 0, resume.count(), "no refused call may dispatch a resume")

	// LEGACY control (semantics deliberately unchanged): the UNBOUND
	// CancelOnSessionStop keeps its no-card no-op on an unknown key —
	// false, nil. The bound method's stricter identity contract does not
	// change the legacy direct-consumer surface.
	cancelled, err = reg.CancelOnSessionStop("session:never-existed")
	require.NoError(t, err, "the legacy unbound method keeps its no-card semantics on an unknown key")
	require.False(t, cancelled, "the legacy unbound method reports false for an unknown key")
}
