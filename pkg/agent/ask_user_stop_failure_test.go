// Omnipus — RED payload #1343/#1329 round1, file 1 of the backend QA pack.
// Target location when applied: pkg/agent/ask_user_stop_failure_test.go
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// RED STATUS (qa-lead, 2026-10-16, revision 2 after backend round-1
// feedback): authored against the founder FIFO decision and the
// user-dispatched gate findings only. Oracles are NOT derived from any
// implementation of the required behavior. Revision 2 corrections (backend
// round-1 review, none of which adapts an expectation to production
// behavior):
//   - NewSession returns *session.UnifiedMeta, not a string (measured
//     compile failure in the round-1 gateway payload) — every fixture here
//     mirrors the backend's fixture-only correction
//     (sessionMeta, err := store.NewSession(...); sid := sessionMeta.ID).
//   - askStopFaultMeta now counts EVERY pending-ask SetMeta write and EVERY
//     GetMeta read unconditionally; the memory-only wiring oracle compares
//     before/after snapshots (a counter that only runs while armed can
//     never witness an observer write — the round-1 counter was a
//     false-green instrument).
//   - The live-turn case registers unconditional provider release + bounded
//     turn join as cleanup BEFORE any assertion, so the expected RED
//     t.Fatal can never leak a real turn writer past the fixtures.
//   - NEW selected-stop coverage (round-2/3 corrections): a REAL owner-root
//     lifecycle record (no ParentSessionID, no SteeredBy) driven through the
//     cascade's selected-stop early path (cancel.go::
//     prepareSelectedStopDisposition — the second discard site) pins error
//     surfacing + retention on the first failure; and a separate case pins
//     the round-3 F1 retry root cause — after the durable stop has landed
//     (the durable branch can skip RequestCancel entirely for an
//     already-stopped record, Report.SkippedTerminal), an explicit retry
//     Stop must STILL re-drive the retained card cancellation (A cancels, B
//     promotes exactly once; no double promotion on repeat stops; the
//     superseded/newer-generation skip is deliberately NOT exercised — an
//     obsolete Stop must never consume newer work).
//
// Backend-lead applies this payload unchanged, owns the RED run and its
// receipt, then implements only the requirements. Recommended narrow gate
// (serial, never parallel, per CLAUDE.md build/test rules):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestAskUserStopSaveFailure$|^TestSetAskUserRegistry_WiresSessionDeletedObserver$' -count=1 -v -p 1 ./pkg/agent/
//
// Skill note: per elicify-test-writing in RED, items 2 (see it green), 3
// (mutate and confirm it dies) and the Proof-of-failability checklist are
// DEFERRED TO CHECK — they are not claimed here.
//
// Spec sources for every oracle below:
//   - Gate finding (user dispatch, 2026-10-16): "State save failure retains
//     both card/queue and must reach Stop caller/user, while other Stop
//     effects continue." ⇒ RootErr carries the sentinel, head+queue
//     retained, approvals hook still ran.
//   - Backend API decision (user dispatch): "Registry.CancelOnSessionStop
//     changes bool -> (bool,error) ... returns false,underlyingError on
//     failed save ... AgentLoop.cancelPendingAskForScope returns error;
//     RequestCancel accumulates it into its RETURNED error without
//     aborting approvals/graceful stop/escalation, so StopSession.RootErr
//     carries it." stopTurn (stop_session.go) assigns RequestCancel's
//     returned error to RootErr for the requested session, so the selected-
//     stop early arm is covered by the same contract.
//   - Round-2 correction 5 (user dispatch): a retry after the card-save
//     failure must still re-drive the retained card cancellation even when
//     the durable Stop already landed — never skipped as "already stopped".
//   - F4 Decision A (team-lead): SetAskUserRegistry wires the store's
//     post-success deletion observer to ForgetDeletedSession; the observer
//     is memory-only (no persistence, no store access, no resume).
//
// No fake StopSession, no fake registry, no test-only production setters:
// everything below drives the REAL AgentLoop.StopSession, the REAL
// pkg/askuser.Registry and the loop's REAL UnifiedStore, wrapped ONLY at
// MetaStore.SetMeta/GetMeta for the exact fault and the access counters.

package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/askuser"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// errAskStopSetMeta is the exact sentinel the MetaStore wrapper injects for
// pending-ask (PendingAskJSON) save failures. errors.Is must find it through
// whatever wrapping the implementation applies — that is the whole point of
// the finding: the REAL storage failure, not a paraphrase, reaches the Stop
// caller.
var errAskStopSetMeta = errors.New("qa-1343: injected pending-ask SetMeta failure")

// askStopFaultMeta wraps the loop's REAL UnifiedStore at the askuser.MetaStore
// seam and injects SetMeta failures ONLY for pending-ask writes (a patch that
// carries PendingAskJSON) while the fault is armed.
//
// Round-2 instrument correction: the observability counters below count
// UNCONDITIONALLY (armed or not). The armed-only fault counter proves the
// fault actually fired; the always-on pendingAskWrites / metaReads counters
// are what the memory-only oracle snapshots — a counter that only moved
// while armed could never witness an observer's write or read after a
// deletion, which made the round-1 memory-only assertion vacuous. Counters
// are never reset; every oracle compares a before/after delta.
type askStopFaultMeta struct {
	inner *session.UnifiedStore
	mu    sync.Mutex
	armed bool
	// faultAttempts counts pending-ask SetMeta calls made WHILE ARMED — the
	// injection proof (an empty instrument would make the retention
	// assertions vacuous).
	faultAttempts int
	// allPendingAskWrites counts EVERY SetMeta call carrying a PendingAskJSON
	// patch, armed or not — the memory-only oracle's write side.
	allPendingAskWrites int
	// metaReads counts EVERY GetMeta call — the memory-only oracle's read
	// side (the observer must not access the store at all).
	metaReads int
}

func newAskStopFaultMeta(store *session.UnifiedStore) *askStopFaultMeta {
	return &askStopFaultMeta{inner: store}
}

func (f *askStopFaultMeta) GetMeta(sessionID string) (*session.UnifiedMeta, error) {
	f.mu.Lock()
	f.metaReads++
	f.mu.Unlock()
	return f.inner.GetMeta(sessionID)
}

func (f *askStopFaultMeta) SetMeta(sessionID string, patch session.MetaPatch) error {
	f.mu.Lock()
	pendingAsk := patch.PendingAskJSON != nil
	armed := f.armed
	if pendingAsk {
		f.allPendingAskWrites++
	}
	if pendingAsk && armed {
		f.faultAttempts++
	}
	f.mu.Unlock()
	if pendingAsk && armed {
		return errAskStopSetMeta
	}
	return f.inner.SetMeta(sessionID, patch)
}

// AppendTranscriptStrict forwards to the real store: human answer records
// stay on the production transcriptRecordStore seam.
func (f *askStopFaultMeta) AppendTranscriptStrict(sessionID string, entry session.TranscriptEntry) error {
	return f.inner.AppendTranscriptStrict(sessionID, entry)
}

func (f *askStopFaultMeta) arm() {
	f.mu.Lock()
	f.armed = true
	f.mu.Unlock()
}

func (f *askStopFaultMeta) disarm() {
	f.mu.Lock()
	f.armed = false
	f.mu.Unlock()
}

// injected proves the fault actually fired at least once — without this the
// "retained" assertions below could pass on a registry that never tried to
// write (an empty instrument).
func (f *askStopFaultMeta) injected(t *testing.T) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.faultAttempts == 0 {
		t.Fatal("instrument error: no pending-ask SetMeta attempt was made while armed — the fault never fired, so every retention assertion below is vacuous")
	}
}

// accessSnapshot is the memory-only oracle's before/after reading.
type askStopAccessSnapshot struct {
	pendingAskWrites int
	metaReads        int
}

func (f *askStopFaultMeta) snapshot() askStopAccessSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return askStopAccessSnapshot{pendingAskWrites: f.allPendingAskWrites, metaReads: f.metaReads}
}

// askStopResumeRecorder records every resume dispatch. The oracle is the
// COUNT and the card ids, never the resume text.
type askStopResumeRecorder struct {
	mu    sync.Mutex
	cards []string
}

func (r *askStopResumeRecorder) DispatchResume(set *askuser.PendingSet, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cards = append(r.cards, set.CardID)
	return nil
}

func (r *askStopResumeRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.cards)
}

// askStopHookTally counts the CancelHooks effects a Stop is still required to
// perform AFTER the pending-ask cancel fails — the no-early-return proof.
type askStopHookTally struct {
	mu        sync.Mutex
	approvals int
	kills     int
}

func (h *askStopHookTally) hooksFor() func(string) CancelHooks {
	return func(sessionID string) CancelHooks {
		return CancelHooks{
			CancelPendingApprovals: func(string, string) {
				h.mu.Lock()
				h.approvals++
				h.mu.Unlock()
			},
			KillBackgroundSessions: func(string) (killed, failed int) {
				h.mu.Lock()
				h.kills++
				h.mu.Unlock()
				return 0, 0
			},
		}
	}
}

func (h *askStopHookTally) approvalCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.approvals
}

// newAskStopFixture builds the shared fixture: the REAL judge-test loop
// (newGoalLoopTestLoop — mockProvider, no model calls), the loop's REAL
// UnifiedStore wrapped ONLY at MetaStore.SetMeta/GetMeta, and a REAL
// pkg/askuser.Registry wired through the production SetAskUserRegistry
// entry point. Cleanup is registered so LIFO runs fault-disarm + Quiesce
// BEFORE the loop's own Close (registered earlier inside newGoalLoopTestLoop).
type askStopFixture struct {
	al     *AgentLoop
	store  *session.UnifiedStore
	meta   *askStopFaultMeta
	reg    *askuser.Registry
	resume *askStopResumeRecorder
	// hooks is ONE tally shared by every Stop this fixture drives, so
	// per-Stop effect counts accumulate across the faulted stop and its
	// retry instead of the retry silently resetting them.
	hooks *askStopHookTally
	sid   string
	cardA *askuser.PendingSet
	cardB *askuser.PendingSet
}

func newAskStopFixture(t *testing.T, provider providers.LLMProvider, mutateCfg func(*config.Config)) *askStopFixture {
	t.Helper()
	al, _ := newGoalLoopTestLoop(t, provider, mutateCfg)
	store := al.GetSessionStore()
	if store == nil {
		t.Fatal("fixture: the loop has no session store")
	}
	meta := newAskStopFaultMeta(store)
	resume := &askStopResumeRecorder{}
	reg := askuser.NewRegistry(meta, resume, askuser.Options{})
	al.SetAskUserRegistry(reg) // the production wiring entry point
	t.Cleanup(func() {
		meta.disarm()
		reg.Quiesce() // waits out any in-flight timer persist before loop Close
	})
	// NewSession returns the session's *UnifiedMeta (round-1 measured
	// compile failure) — the id is the meta's field, never the return value.
	sessionMeta, err := store.NewSession(session.SessionTypeChat, "webchat", "native-agent")
	if err != nil {
		t.Fatalf("fixture: NewSession: %v", err)
	}
	sid := sessionMeta.ID
	return &askStopFixture{
		al: al, store: store, meta: meta, reg: reg, resume: resume,
		hooks: &askStopHookTally{},
		sid:   sid,
		cardA: askStopCard(sid, "Which scope first?"),
		cardB: askStopCard(sid, "Which database next?"),
	}
}

// askStopCard builds a non-default-safe one-question set (no default-safe
// timer is ever armed by this pack — cleanup prefers no live timers).
func askStopCard(sessionID, question string) *askuser.PendingSet {
	return &askuser.PendingSet{
		CardID:              askuser.NewCardID(),
		RoutingSessionKey:   "session:" + sessionID,
		TranscriptSessionID: sessionID,
		AgentID:             "native-agent",
		Channel:             "webchat",
		ChatID:              "chat-qa-1343",
		Questions: []askuser.Question{{
			Header:   "Scope",
			Question: question,
			Options:  []askuser.Option{{Label: "Backend"}, {Label: "Full stack"}},
		}},
	}
}

func (f *askStopFixture) admitTwoCards(t *testing.T) {
	t.Helper()
	if err := f.reg.CreatePending(f.cardA); err != nil {
		t.Fatalf("fixture: CreatePending(A): %v", err)
	}
	if err := f.reg.CreatePending(f.cardB); err != nil {
		t.Fatalf("fixture: CreatePending(B): %v", err)
	}
	if got := f.reg.PendingCount(); got != 2 {
		t.Fatalf("fixture: expected 2 pending sets (A active, B queued), got %d", got)
	}
}

func (f *askStopFixture) stopRequest() StopRequest {
	return StopRequest{
		SessionID: f.sid,
		By:        steer.Principal{Kind: steer.PrincipalKindHuman, ID: "qa-1343"},
		Channel:   "web",
		HooksFor:  f.hooks.hooksFor(),
	}
}

func (f *askStopFixture) approvalsAfterStop() int { return f.hooks.approvalCount() }

// assertHeadRetained pins the retention oracle after a failed save: A is
// still the active pending card, B is still hidden, nothing resumed.
func (f *askStopFixture) assertHeadRetained(t *testing.T) {
	t.Helper()
	active, ok := f.reg.PendingForSession(f.sid)
	if !ok {
		t.Fatal("the head card must stay pending after a failed save (founder: state save failure retains both card and queue)")
	}
	if active.CardID != f.cardA.CardID || active.Status != askuser.StatusPending {
		t.Fatalf("head card changed after failed save: id=%s status=%s", active.CardID, active.Status)
	}
	if _, visible := f.reg.Get(f.cardB.CardID); visible {
		t.Fatal("the queued card must stay hidden after a failed save")
	}
	if got := f.reg.PendingCount(); got != 2 {
		t.Fatalf("pending count = %d, want 2 (head retained AND queue retained)", got)
	}
	if n := f.resume.count(); n != 0 {
		t.Fatalf("resume dispatched %d time(s) after a failed save; the card was never accepted", n)
	}
}

// TestAskUserStopSaveFailure is the single narrow entry point. Each subtest
// is self-contained and named for its expected outcome.
func TestAskUserStopSaveFailure(t *testing.T) {
	t.Run("parked_stop_save_failure_surfaces_in_root_err_and_retains_queue", askStopCase_Parked)
	t.Run("live_turn_stop_save_failure_surfaces_without_aborting_graceful_stop", askStopCase_LiveTurn)
	t.Run("selected_stop_save_failure_surfaces_and_retains", askStopCase_SelectedStop)
	t.Run("retry_after_durable_stop_redrives_retained_cards_once", askStopCase_RetryAfterDurableStop)
	t.Run("stale_capture_never_cancels_promoted_card", askStopCase_StaleCapture)
}

// askStopCase_Parked is the exact parked shape: the turn already ended
// parked, the card pends, the user hits Stop, and the cancellation's durable
// write fails. The finding: the failure must reach the Stop caller on
// StopResult.RootErr while every other Stop effect still runs; retry after
// the fault clears cancels the head, promotes the queue, and still resumes
// nothing.
func askStopCase_Parked(t *testing.T) {
	f := newAskStopFixture(t, &mockProvider{}, nil)
	f.admitTwoCards(t)

	f.meta.arm()
	res, err := f.al.StopSession(context.Background(), f.stopRequest())
	if err != nil {
		t.Fatalf("StopSession returned a could-not-start error; a failed pending-ask save is a per-session failure, not a stop refusal: %v", err)
	}
	f.meta.injected(t) // the fault really fired — the assertions below are not vacuous
	if !errors.Is(res.RootErr, errAskStopSetMeta) {
		t.Fatalf("StopSession.RootErr must carry the failed pending-ask save (errors.Is sentinel); got %v", res.RootErr)
	}
	f.assertHeadRetained(t)
	if got := f.approvalsAfterStop(); got != 1 {
		t.Fatalf("pending-approval cancel hook ran %d time(s) during the failed stop, want exactly 1 — other Stop effects must continue (no early return)", got)
	}

	// Retry once the storage fault clears: the same REAL Stop now completes.
	f.meta.disarm()
	res2, err := f.al.StopSession(context.Background(), f.stopRequest())
	if err != nil {
		t.Fatalf("retried StopSession could-not-start: %v", err)
	}
	if res2.RootErr != nil {
		t.Fatalf("retried StopSession.RootErr = %v, want nil", res2.RootErr)
	}
	active, ok := f.reg.PendingForSession(f.sid)
	if !ok || active.CardID != f.cardB.CardID {
		t.Fatalf("after the retried stop the queued card must be promoted head; got ok=%v card=%v", ok, active)
	}
	if _, still := f.reg.Get(f.cardA.CardID); still {
		t.Fatal("the head card must be cancelled by the retried stop")
	}
	if got := f.reg.PendingCount(); got != 1 {
		t.Fatalf("pending count after retried stop = %d, want 1 (promoted head only)", got)
	}
	if n := f.resume.count(); n != 0 {
		t.Fatalf("session-stop cancellation dispatched %d resume(s); a session-stop cancel never resumes (spec US-6 S2)", n)
	}
	if got := f.approvalsAfterStop(); got != 2 {
		t.Fatalf("approval hook ran %d time(s) across both stops, want 2 (once per Stop)", got)
	}
}

// askStopBlockProvider is a providers.LLMProvider double for the live-turn
// case: the single real Chat call parks until the test releases it (or the
// turn's own ctx is cancelled by the graceful stop — which is exactly the
// outcome under test). open() is sync.Once-guarded: every exit path —
// setup timeout included — releases through it, so a double-close is
// structurally impossible.
type askStopBlockProvider struct {
	entered chan context.Context
	release chan struct{}
	once    sync.Once
}

func (p *askStopBlockProvider) Chat(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.entered <- ctx
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.release:
		return &providers.LLMResponse{Content: "qa-1343 live turn finished"}, nil
	}
}

func (*askStopBlockProvider) GetDefaultModel() string { return "qa-1343-block-provider" }
func (p *askStopBlockProvider) open()                 { p.once.Do(func() { close(p.release) }) }

// askStopCase_LiveTurn proves the accumulation does not abort the graceful
// stop of a REAL running turn: the pending-ask save fails, RootErr carries
// the sentinel, and the turn was still asked to stop (its provider ctx is
// cancelled). The turn is real — ProcessScheduled, the scheduled entry the
// engine itself drives — never a fake StopSession.
//
// Round-2 correction 3: the unconditional release+join cleanup is registered
// the moment the turn goroutine exists and BEFORE any assertion, so the
// expected RED t.Fatal can never leave the real turn's writers outliving the
// fixtures into reg.Quiesce / loop Close. The join carries a bounded
// diagnostic (t.Error on timeout), never a widened sleep.
func askStopCase_LiveTurn(t *testing.T) {
	provider := &askStopBlockProvider{
		entered: make(chan context.Context, 1),
		release: make(chan struct{}),
	}
	f := newAskStopFixture(t, provider, func(cfg *config.Config) {
		cfg.Performance.MaxParallelAgents = 2
		cfg.Performance.MaxDelegationDepth = 2
		cfg.Sandbox.Mode = config.SandboxModeOff
	})
	f.admitTwoCards(t)

	turnDone := make(chan struct{})
	go func() {
		defer close(turnDone)
		_, _ = f.al.ProcessScheduled(context.Background(), "native-agent", f.sid, "qa-1343 live turn", "webchat", f.sid)
	}()
	// Unconditional cleanup FIRST: release (sync.Once) on every exit path,
	// then join the real turn with a bounded diagnostic. Registered after the
	// fixture's Quiesce cleanup, so LIFO runs THIS before reg.Quiesce and the
	// loop's own Close — a still-running turn can never outlive them.
	t.Cleanup(func() {
		provider.open()
		select {
		case <-turnDone:
		case <-time.After(10 * time.Second):
			t.Error("the real ProcessScheduled turn never joined after release — its writers would outlive the fixtures")
		}
	})

	var turnCtx context.Context
	select {
	case turnCtx = <-provider.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("SETUP: the real turn never entered the blocking provider")
	}

	f.meta.arm()
	res, err := f.al.StopSession(context.Background(), f.stopRequest())
	if err != nil {
		t.Fatalf("StopSession returned a could-not-start error: %v", err)
	}
	f.meta.injected(t)
	if !errors.Is(res.RootErr, errAskStopSetMeta) {
		t.Fatalf("StopSession.RootErr must carry the failed pending-ask save on a live-turn stop too; got %v", res.RootErr)
	}
	f.assertHeadRetained(t)
	if got := f.approvalsAfterStop(); got != 1 {
		t.Fatalf("approval hook ran %d time(s), want 1 — the graceful stop must continue past the failed card save", got)
	}

	// The graceful stop still proceeded: the real turn's provider context was
	// cancelled. Bounded poll — never an elapsed-time assertion.
	deadline := time.Now().Add(5 * time.Second)
	for turnCtx.Err() == nil {
		if time.Now().After(deadline) {
			t.Fatal("the live turn's provider context was never cancelled — accumulating the pending-ask error must not abort the graceful stop, but the stop itself must still happen")
		}
		time.Sleep(5 * time.Millisecond)
	}

	if n := f.resume.count(); n != 0 {
		t.Fatalf("resume dispatched %d time(s); neither the failed save nor the stop may resume", n)
	}
}

// askStopSink is a CardSink double counting emissions per card id and
// status — the no-double-promotion oracle of the retry cases.
type askStopSink struct {
	mu    sync.Mutex
	cards []*askuser.PendingSet
}

func (s *askStopSink) EmitCard(set *askuser.PendingSet) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cards = append(s.cards, set)
}

func (s *askStopSink) countWhere(cardID string, status askuser.SetStatus) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, c := range s.cards {
		if c.CardID == cardID && c.Status == status {
			count++
		}
	}
	return count
}

// newAskSelectedStopFixture builds the settlement pack's proven lawful
// owner-session shape (root_stop_settlement_test.go, mode
// "idle_after_real_admission"): REAL chat-origin Launch (production-owned
// record, no ParentSessionID, no SteeredBy), REAL dispatch, REAL turn
// through the gated provider, REAL completion — then the session is idle
// with its record not yet stopped. Nothing is manufactured; no delegated-
// child card is staged (the registry correctly rejects those); no Stop is
// faked.
func newAskSelectedStopFixture(t *testing.T) (*AgentLoop, *askStopFaultMeta, *askuser.Registry, *askStopResumeRecorder, *askStopSink, string) {
	t.Helper()
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	gate := newGoalRunGate("root partial work without a completion claim", nil)
	installGoalRunProvider(t, al, gate)
	root := rootLaunchWithGoal(t, al)
	dispatchChild(t, al, root.SessionID, root.Generation, true)
	awaitGoalProvider(t, gate)
	// Real completion: release the provider and join the turn AND its
	// disposal tail — the settlement pack's idle-after-real-admission shape.
	gate.open()
	joinGoalFixtureRuns(t, al)

	// Owner-root sanity (round-3 constraint, kept per dispatcher): the record
	// is the session's own — LifecycleRecord carries the steering edge in
	// SteeredBy (it has NO ParentSessionID field; that field lives on
	// UnifiedMeta), and the store meta must name no parent.
	store := al.GetSessionStore()
	if store == nil {
		t.Fatal("fixture: the steer loop has no session store")
	}
	record, err := al.GetSessionLifecycleStore().Load(root.SessionID)
	if err != nil {
		t.Fatalf("fixture: the launched root has no lifecycle record: %v", err)
	}
	if record.SteeredBy != nil {
		t.Fatalf("SETUP: the fixture root must be an owner root (no SteeredBy edge); got %+v", record)
	}
	ownerMeta, metaErr := store.GetMeta(root.SessionID)
	if metaErr != nil || ownerMeta == nil {
		t.Fatalf("fixture: the launched root has no store meta: %v", metaErr)
	}
	if ownerMeta.ParentSessionID != "" {
		t.Fatalf("SETUP: the fixture root must be an owner root (meta.ParentSessionID must be empty); got %q", ownerMeta.ParentSessionID)
	}
	meta := newAskStopFaultMeta(store)
	resume := &askStopResumeRecorder{}
	sink := &askStopSink{}
	reg := askuser.NewRegistry(meta, resume, askuser.Options{Sink: sink})
	al.SetAskUserRegistry(reg)
	t.Cleanup(func() {
		meta.disarm()
		reg.Quiesce()
	})
	return al, meta, reg, resume, sink, root.SessionID
}

// askStopCase_SelectedStop pins the SECOND discard site — the cascade's
// selected-stop early path (cancel.go::prepareSelectedStopDisposition,
// reached because the target has a REAL lifecycle record with a non-zero
// generation, so stopTurn calls RequestCancel with Generation != 0).
//
// The pending-ask cancel there fails and its error must surface on RootErr
// exactly as on the claim path (stop_session.go::stopTurn assigns
// RequestCancel's returned error to RootErr for the requested session),
// with the head+queue retained and the approval hook still run. Whether the
// durable stop ALSO lands while the callback errors is observed and REPORTED
// (t.Logf), not asserted: if existing retainSelectedStop semantics prevent
// the first Stop from landing while its callback errors, that is a concrete
// fact for the dispatcher, and the retry requirement itself is pinned
// independently by askStopCase_RetryAfterDurableStop below — which is where
// the user's root-cause concern (the durable branch skipping RequestCancel
// for an already-stopped record) is anchored.
func askStopCase_SelectedStop(t *testing.T) {
	al, meta, reg, resume, _, sid := newAskSelectedStopFixture(t)

	cardA := askStopCard(sid, "Selected-stop head question?")
	cardB := askStopCard(sid, "Selected-stop queued question?")
	if err := reg.CreatePending(cardA); err != nil {
		t.Fatalf("fixture: CreatePending(A): %v", err)
	}
	if err := reg.CreatePending(cardB); err != nil {
		t.Fatalf("fixture: CreatePending(B): %v", err)
	}

	req := StopRequest{
		SessionID: sid,
		By:        steer.Principal{Kind: steer.PrincipalKindHuman, ID: "qa-1343"},
		Channel:   "web",
	}
	faultedHooks := &askStopHookTally{}
	req.HooksFor = faultedHooks.hooksFor()

	meta.arm()
	res, err := al.StopSession(context.Background(), req)
	if err != nil {
		t.Fatalf("StopSession returned a could-not-start error: %v", err)
	}
	meta.injected(t)
	if !errors.Is(res.RootErr, errAskStopSetMeta) {
		t.Fatalf("selected-stop early path must surface the failed pending-ask save on RootErr (same accumulation contract as the claim path); got %v", res.RootErr)
	}
	active, ok := reg.PendingForSession(sid)
	if !ok || active.CardID != cardA.CardID || active.Status != askuser.StatusPending {
		t.Fatalf("the failed selected-stop save must retain the head; got ok=%v card=%v", ok, active)
	}
	if _, visible := reg.Get(cardB.CardID); visible {
		t.Fatal("the failed selected-stop save must retain the queue hidden")
	}
	if got := faultedHooks.approvalCount(); got != 1 {
		t.Fatalf("approval hook ran %d time(s) on the faulted selected stop, want 1 — the early path must continue past the failed card save", got)
	}
	// OBSERVED FACT for the dispatcher (not an oracle): did the durable stop
	// land while the callback errored?
	if record, loadErr := al.GetSessionLifecycleStore().Load(sid); loadErr == nil {
		t.Logf("observed: after the faulted selected stop the record state=%q stop_note=%v", record.State, record.StopNote)
	}
	_ = resume
}

// askStopCase_RetryAfterDurableStop pins the round-3 F1 retry root cause:
// StopSession's durable branch can skip RequestCancel entirely when the
// lifecycle record is already stopped (rootReached=false,
// Report.SkippedTerminal) — so merely joining the error into RequestCancel
// fixes the FIRST failure's reporting but would STILL never re-drive the
// retained cards on a subsequent explicit Stop. The requirement: the retry
// is permitted and must work.
//
// Lawful fixture (per dispatcher instruction): a real owner-root lifecycle
// record driven to durable STOPPED state through a real healthy StopSession
// (no cards parked yet), and only THEN a still-pending owner card pair
// admitted on the stopped session — exactly the "already-stopped record
// with a still-pending owner card" shape. No delegated child is involved
// (the registry rejects those cards correctly).
//
// The faulted stop on the stopped record must surface the card-save failure
// to the caller (on RootErr where a RequestCancel ran, else on the returned
// error — the durable-skip branch has no Root/RootErr assignment, and the
// requirement is that the failure REACHES the caller); the retry with the
// fault cleared must then cancel A and promote B exactly once. Repeated
// healthy stops afterwards must change nothing: no double promotion, A's
// cancelled record emitted exactly once, B's promoted record emitted exactly
// once. A superseded/newer-generation skip is deliberately NOT exercised
// here (no newer generation exists in this fixture) — an obsolete Stop must
// never consume newer work, and this case must not be readable as requiring
// that.
func askStopCase_RetryAfterDurableStop(t *testing.T) {
	al, meta, reg, resume, sink, sid := newAskSelectedStopFixture(t)

	// Land the durable stop FIRST through a real healthy StopSession (no
	// cards parked yet): the record must come back durably stopped.
	healthyReq := StopRequest{
		SessionID: sid,
		By:        steer.Principal{Kind: steer.PrincipalKindHuman, ID: "qa-1343"},
		Channel:   "web",
	}
	res, err := al.StopSession(context.Background(), healthyReq)
	if err != nil {
		t.Fatalf("the fixture's healthy stop could-not-start: %v", err)
	}
	if res.RootErr != nil {
		t.Fatalf("SETUP: the fixture's healthy stop failed: %v", res.RootErr)
	}
	record, loadErr := al.GetSessionLifecycleStore().Load(sid)
	if loadErr != nil {
		t.Fatalf("SETUP: the stopped root has no lifecycle record: %v", loadErr)
	}
	if record.State != session.LifecycleStopped {
		t.Fatalf("SETUP: the healthy stop did not land the durable stopped state (state=%q) — the retry fixture requires an already-stopped record", record.State)
	}

	// Only NOW admit the still-pending owner card pair on the stopped
	// session.
	cardA := askStopCard(sid, "Retained head on a stopped session?")
	cardB := askStopCard(sid, "Retained queue on a stopped session?")
	if createErr := reg.CreatePending(cardA); createErr != nil {
		t.Fatalf("fixture: CreatePending(A): %v", createErr)
	}
	if createErr := reg.CreatePending(cardB); createErr != nil {
		t.Fatalf("fixture: CreatePending(B): %v", createErr)
	}

	faultedHooks := &askStopHookTally{}
	healthyReq.HooksFor = faultedHooks.hooksFor()

	// Stop on the already-stopped record with the fault armed: the retained
	// card cancellation must be attempted (re-driven), so the save failure
	// must REACH the caller — on RootErr where the callback ran, else on the
	// returned error. Today nothing is attempted and nothing surfaces: the
	// cards are silently skipped as already stopped — that silence is the
	// RED.
	meta.arm()
	res, err = al.StopSession(context.Background(), healthyReq)
	meta.injected(t)
	if !errors.Is(res.RootErr, errAskStopSetMeta) && !errors.Is(err, errAskStopSetMeta) {
		t.Fatalf("a Stop whose retained-card cancellation fails must surface the failure to the caller (RootErr or returned error); got err=%v RootErr=%v — silently skipping the retained cards as already stopped leaks them forever", err, res.RootErr)
	}
	active, ok := reg.PendingForSession(sid)
	if !ok || active.CardID != cardA.CardID || active.Status != askuser.StatusPending {
		t.Fatalf("the failed retained-card save must retain the head; got ok=%v card=%v", ok, active)
	}
	if _, visible := reg.Get(cardB.CardID); visible {
		t.Fatal("the failed retained-card save must retain the queue hidden")
	}
	if len(res.Report.SkippedNewerGeneration) != 0 {
		t.Fatalf("fixture sanity: no newer generation exists in this fixture; got skipped-newer=%v", res.Report.SkippedNewerGeneration)
	}

	// Retry with the fault cleared: A must cancel and B promote ONCE.
	meta.disarm()
	res2, err := al.StopSession(context.Background(), healthyReq)
	if err != nil {
		t.Fatalf("retried StopSession could-not-start: %v", err)
	}
	if res2.RootErr != nil {
		t.Fatalf("retried StopSession.RootErr = %v, want nil", res2.RootErr)
	}
	active, ok = reg.PendingForSession(sid)
	if !ok || active.CardID != cardB.CardID {
		t.Fatalf("the retry must re-drive the retained card cancellation and promote the queued head; got ok=%v card=%v", ok, active)
	}
	if _, still := reg.Get(cardA.CardID); still {
		t.Fatal("the retry must cancel the retained head even though the durable stop had already landed before the cards were admitted")
	}

	// After ONE healthy retry: A cancelled exactly once, B promoted exactly
	// once (count 1). A subsequent NEW explicit Stop captures the NEW head B
	// and cancels it — a fresh Stop is never asserted to be a no-op merely
	// because the lifecycle is stopped (round-4 correction B). Idempotence is
	// deliberately NOT asserted here: it would require the same real accepted
	// Continue control, for which no lawful fixture is identified (see the
	// payload README's gap note).
	res3, err := al.StopSession(context.Background(), healthyReq)
	if err != nil {
		t.Fatalf("third (fresh) StopSession could-not-start: %v", err)
	}
	if res3.RootErr != nil {
		t.Fatalf("third (fresh) StopSession.RootErr = %v, want nil — a fresh Stop captures B and cancels it", res3.RootErr)
	}
	if _, still := reg.PendingForSession(sid); still {
		t.Fatal("the fresh explicit Stop after the retry must capture and cancel the promoted head B")
	}
	if got := reg.PendingCount(); got != 0 {
		t.Fatalf("pending count after the fresh stop = %d, want 0", got)
	}
	if n := sink.countWhere(cardA.CardID, askuser.StatusCancelled); n != 1 {
		t.Fatalf("A's cancelled terminal record was emitted %d time(s), want exactly 1 — a double promotion/double consumption is a defect", n)
	}
	if n := sink.countWhere(cardB.CardID, askuser.StatusPending); n != 1 {
		t.Fatalf("B's promoted pending record was emitted %d time(s), want exactly 1 — no double promotion", n)
	}
	if n := sink.countWhere(cardB.CardID, askuser.StatusCancelled); n != 1 {
		t.Fatalf("B's cancelled terminal record was emitted %d time(s), want exactly 1 (the fresh Stop's own capture)", n)
	}
	if n := resume.count(); n != 0 {
		t.Fatalf("session-stop cancellation dispatched %d resume(s); a session-stop cancel never resumes", n)
	}
}

// askStopResolveDecorator wraps the REAL SteerCanceller behind the
// production StopRequest.Canceller seam and answers the captured head
// through the REAL registry at a deterministic point INSIDE the Stop's
// durable cascade — before the cascade's stopTurn (and thus the card
// cancellation re-drive) runs. No sleep, no clock, no mock of StopSession or
// the registry: the only injected thing is WHEN a lawful registry Submit
// happens, which is exactly the race window the target-identity requirement
// closes.
type askStopResolveDecorator struct {
	inner    StopTurnsCanceller
	reg      *askuser.Registry
	sid      string
	cardA    string
	answered bool
}

func (d *askStopResolveDecorator) StopTurnsWithCause(
	ctx context.Context, sessionID string, by steer.Principal, subtree bool, cause session.StopCause, stopTurn GenerationCancelFunc,
) (steer.CancelReport, error) {
	if !d.answered {
		d.answered = true
		if err := d.reg.Submit(d.cardA, d.sid, "", []askuser.SubmittedAnswer{
			{Header: "Scope", Selected: []string{"Backend"}},
		}); err != nil {
			return steer.CancelReport{}, fmt.Errorf("fixture: deterministic pre-re-drive answer failed: %w", err)
		}
	}
	return d.inner.StopTurnsWithCause(ctx, sessionID, by, subtree, cause, stopTurn)
}

func (d *askStopResolveDecorator) ContinueAcceptedStop(
	ctx context.Context, intent session.UnfinishedStopIntent, stopTurn GenerationCancelFunc,
) (steer.CancelReport, error) {
	return d.inner.ContinueAcceptedStop(ctx, intent, stopTurn)
}

// askStopCase_StaleCapture pins the round-4 F1 target-identity requirement
// through a REAL StopSession: a fresh Stop captures the head it intends to
// cancel, and the cancellation must use THAT exact card id. If the captured
// head A resolves and promotes B before the cancellation re-drives (forced
// here deterministically at the real canceller boundary), B MUST NOT be
// cancelled and the resulting no-op must surface as an error on RootErr. A
// fresh explicit Stop afterwards captures B and cancels B — it is never a
// silent no-op.
//
// The measured path is disclosed: the deterministic boundary is the
// production StopRequest.Canceller seam (a real input of the lifecycle Stop
// path) wrapped around the REAL SteerCanceller; the registry, store, launch
// and turn machinery are all real. The plain (no-lifecycle) path has no
// canceller seam to interpose on, so stale-capture there is covered only by
// the registry-layer exact-ID contract (pkg/askuser payload) — disclosed as
// such.
func askStopCase_StaleCapture(t *testing.T) {
	al, meta, reg, resume, sink, sid := newAskSelectedStopFixture(t)

	cardA := askStopCard(sid, "Capture head question?")
	cardB := askStopCard(sid, "Queued behind the capture head?")
	if err := reg.CreatePending(cardA); err != nil {
		t.Fatalf("fixture: CreatePending(A): %v", err)
	}
	if err := reg.CreatePending(cardB); err != nil {
		t.Fatalf("fixture: CreatePending(B): %v", err)
	}

	hooks := &askStopHookTally{}
	req := StopRequest{
		SessionID: sid,
		By:        steer.Principal{Kind: steer.PrincipalKindHuman, ID: "qa-1343"},
		Channel:   "web",
		HooksFor:  hooks.hooksFor(),
		Canceller: &askStopResolveDecorator{
			inner: al.steerCanceller(),
			reg:   reg, sid: sid, cardA: cardA.CardID,
		},
	}

	// Fresh Stop #1 captures A; the decorator answers A (promoting B) inside
	// the cascade; the re-drive must then refuse: no mutation of B, and the
	// no-op surfaced on RootErr. The storage fault is deliberately NOT armed
	// here — clean writes are what make a WRONG cancellation land and be
	// caught; arming the fault would mask the defect behind a failed save.
	res, err := al.StopSession(context.Background(), req)
	if err != nil {
		t.Fatalf("StopSession returned a could-not-start error: %v", err)
	}
	if res.RootErr == nil {
		t.Fatalf("a Stop whose captured head changed before the re-drive must surface the no-op as an error on RootErr; got nil — today the promoted card B is silently cancelled instead (the defect)")
	}
	// B must be untouched by the stale capture: still the live pending head.
	active, ok := reg.PendingForSession(sid)
	if !ok || active.CardID != cardB.CardID || active.Status != askuser.StatusPending {
		t.Fatalf("the promoted card B must NOT be cancelled by a Stop that captured A; got ok=%v card=%v status=%v", ok, active, activeStatus(active))
	}
	if n := sink.countWhere(cardB.CardID, askuser.StatusCancelled); n != 0 {
		t.Fatalf("B carried %d cancelled emission(s) — the stale capture must never mutate the promoted card", n)
	}
	if n := sink.countWhere(cardA.CardID, askuser.StatusAnswered); n != 1 {
		t.Fatalf("the decorator's lawful answer of A must be the only A terminal record; got %d", n)
	}
	// The captured head A's own answer resumed exactly once; the stale
	// cancellation added no resume.
	if n := resume.count(); n != 1 {
		t.Fatalf("resume count = %d, want exactly 1 (A's human answer); the stale no-op must not resume anything", n)
	}
	if got := hooks.approvalCount(); got != 1 {
		t.Fatalf("approval hook ran %d time(s) on the stale-capture stop, want 1 — other Stop effects continue", got)
	}

	// Fresh Stop #2 (fault cleared): captures the NEW head B and cancels it.
	meta.disarm()
	res2, err := al.StopSession(context.Background(), req)
	if err != nil {
		t.Fatalf("fresh StopSession #2 could-not-start: %v", err)
	}
	if res2.RootErr != nil {
		t.Fatalf("fresh Stop #2 RootErr = %v, want nil — it captures B and cancels it", res2.RootErr)
	}
	if _, still := reg.PendingForSession(sid); still {
		t.Fatal("the fresh Stop must cancel the head it captured (B)")
	}
	if got := reg.PendingCount(); got != 0 {
		t.Fatalf("pending count after the fresh Stop = %d, want 0", got)
	}
	if n := sink.countWhere(cardB.CardID, askuser.StatusCancelled); n != 1 {
		t.Fatalf("B's cancelled terminal record emitted %d time(s), want exactly 1 (from the fresh Stop)", n)
	}
	if n := resume.count(); n != 1 {
		t.Fatalf("resume count = %d, want still 1 — session-stop cancels never resume", n)
	}
}

// activeStatus is a tiny helper keeping the stale-capture assertion readable
// when the head is absent.
func activeStatus(set *askuser.PendingSet) askuser.SetStatus {
	if set == nil {
		return ""
	}
	return set.Status
}

// TestSetAskUserRegistry_WiresSessionDeletedObserver pins team-lead F4
// Decision A's wiring contract in the package that owns the entry point:
// AgentLoop.SetAskUserRegistry wires the session store's post-success
// deletion observer to the registry's ForgetDeletedSession, so a REAL
// store.DeleteSession (the primitive every delete path funnels through)
// clears the registry's head+FIFO without Stop being involved.
//
// Memory-only is asserted with the round-2 corrected instrument: the
// ALWAYS-ON pending-ask write counter and GetMeta read counter are
// snapshotted AFTER fixture admission and compared across the deletion —
// no delta means the observer performed no store access at all. The
// counters are never reset. (The proposed UnifiedStore.SetSessionDeletedObserver
// API is deliberately NOT referenced here — its absence is asserted by the
// pkg/session instrument payload, not by this wiring test, which must stay
// compilable.)
func TestSetAskUserRegistry_WiresSessionDeletedObserver(t *testing.T) {
	f := newAskStopFixture(t, &mockProvider{}, nil)
	f.admitTwoCards(t)

	// Snapshot AFTER admission: the fixture's own CreatePending writes are
	// the baseline; the deletion must add zero writes and zero reads.
	before := f.meta.snapshot()

	if err := f.store.DeleteSession(f.sid); err != nil {
		t.Fatalf("store.DeleteSession: %v", err)
	}
	after := f.meta.snapshot()

	if meta, metaErr := f.store.GetMeta(f.sid); metaErr == nil && meta != nil {
		t.Fatal("SETUP: the session row survived DeleteSession — the delete precondition failed")
	}
	if got := f.reg.PendingCount(); got != 0 {
		t.Fatalf("after the confirmed store deletion the registry still holds %d pending set(s) — the wired deletion observer must clear head+FIFO for the deleted session", got)
	}
	if _, ok := f.reg.Get(f.cardA.CardID); ok {
		t.Fatal("deleted session's head card is still registry-visible")
	}
	if _, ok := f.reg.Get(f.cardB.CardID); ok {
		t.Fatal("deleted session's queued card is still registry-visible")
	}
	if n := f.resume.count(); n != 0 {
		t.Fatalf("the deletion observer dispatched %d resume(s); the observer is memory-only (F4 Decision A)", n)
	}
	if after.pendingAskWrites != before.pendingAskWrites {
		t.Fatalf("the deletion observer performed %d pending-ask store write(s) (baseline %d -> %d); the observer is memory-only — no persistence after deletion",
			after.pendingAskWrites-before.pendingAskWrites, before.pendingAskWrites, after.pendingAskWrites)
	}
	if after.metaReads != before.metaReads {
		t.Fatalf("the deletion observer performed %d store read(s) (baseline %d -> %d); the observer is memory-only — no store access after deletion",
			after.metaReads-before.metaReads, before.metaReads, after.metaReads)
	}
}
