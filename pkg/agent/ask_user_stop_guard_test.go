// Omnipus — CHECK finding F-1 closure: behavioral coverage of the Stop
// continuation and newer/superseded-generation non-re-drive guards (#1343).
// Target location when applied: pkg/agent/ask_user_stop_guard_test.go
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// LAYER STATEMENT (required by the finding): these are UNIT-LEVEL guard
// tests over the production guards themselves —
// AgentLoop.bindPendingAskStop / cancelPendingAskForScope /
// retryStoppedPendingAsk (pkg/agent/ask_user_stop_target.go) — driven on a
// REAL AgentLoop, REAL pkg/askuser.Registry, REAL UnifiedStore and a REAL
// owner-root lifecycle record (the settlement-pack fixture helpers from
// ask_user_stop_failure_test.go). They are NOT a real continuation E2E: no
// accepted stop-control intent is staged (that fixture remains the named
// gap), and no StopSession is faked.
//
// ROUND-8 FIXTURE CORRECTION (measured, disclosed — detection NOT claimed
// until backend re-executes): the round-7 report-skip case staged its cards
// on the owner-root fixture's IDLE record (real turn completed, record NOT
// stopped). The initial green passed (initial-green-stop-guards.log exit 0,
// all six cases), but the dispatcher's MEASURED mutant — disabling only the
// first report condition in retryStoppedPendingAsk — SURVIVED it
// (initial-oracle-survives-report-skip-mutant.log, exit 0): with the report
// guard gone, the flow fell through to the `record.State != Stopped`
// early-out and still returned nil, so the missing guard was never observed.
// Corrected FIXTURE ONLY (no production change, no expectation change): all
// three negative guard cases and the same-generation positive now stage a
// REAL healthy StopSession BEFORE admitting A/B (guardStageStoppedRoot —
// the same lawful already-stopped shape the retry case uses), assert the
// record's durable stopped state, and use its ACTUAL generation (never a
// hardcoded one). Under the SAME disabled-report-guard mutant the flow now
// reaches the real cancellation, consumes A, and the untouched assertions
// kill it. The same staging means removing a GENERATION guard can also
// actually reach cancellation instead of an unrelated early-out.
//
// Oracle sources (never the implementation's output): the round-4/round-6
// target-identity requirements and CHECK finding F-1's required behaviors —
//   - a continuation binding carries NO new card intent: the bound cancel
//     must leave the real active head A AND the real queued FIFO B untouched
//     (B counted as queued, not merely Get-invisible), dispatch no resume
//     and no card, and touch neither store seam nor registry;
//   - retryStoppedPendingAsk must SKIP (nil) without mutation when the
//     report names the session in SkippedNewerGeneration or Superseded;
//   - it must SKIP an actually newer lifecycle generation by appending the
//     session to report.SkippedNewerGeneration;
//   - it must return an EXPLICIT error on an unexpected generation mismatch
//     (obsolete request, neither same-generation nor newer);
//   - the explicit POSITIVE: a fresh binding and an exact-matching stopped
//     generation DO cancel the captured A and promote B — an inert helper
//     cannot pass everything.
//
// Counter discipline: the always-on askStopFaultMeta counters are compared
// before/after on every path. The lifecycle read inside the generation guard
// goes to the SEPARATE LifecycleStore, not the wrapped MetaStore seam, so it
// is legitimately invisible to these counters; the counters prove the
// REGISTRY mutation side (which path durably wrote and which did not). No
// path is asserted to "read nothing" beyond what its seam can see.
//
// No fake StopSession, no fake Registry, no production test setter, no
// fabricated lifecycle fields: every record comes from the real Launch/
// dispatch/completion machinery or a real healthy StopSession. Cards are
// non-default-safe — no long timer is ever armed; cleanup is registered
// before any assertion.
//
// Provenance: authored round 7 to close CHECK finding F-1 (auditor session
// ae24ed92-3122-429b-b677-790c6561ea69, qa-check-current-fix-report.md);
// round 8 corrects the report-skip/generation fixtures per the dispatcher's
// measured surviving-mutant receipt. Mutation is the legitimate RED
// instrument here (the guards are already implemented per the dispatcher);
// backend runs the deliberate mutants and the initial/final greens — QA
// executes nothing and claims no corrected detection until those receipts
// exist.
//
// Skill note: per elicify-test-writing, mutation proof for these
// already-implemented requirements is backend's to run; QA claims no
// execution.
//
// Recommended narrow gate (serial):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestAskUserStopGuards$' -count=1 -v -p 1 ./pkg/agent/

package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/askuser"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// TestAskUserStopGuards is the single narrow entry point: one top-level test
// whose subtests each pin one guard behavior named by CHECK finding F-1.
func TestAskUserStopGuards(t *testing.T) {
	t.Run("continuation_binding_leaves_active_head_and_fifo_untouched", guardCase_ContinuationUntouched)
	t.Run("fresh_binding_cancels_captured_head_and_promotes_queue", guardCase_FreshBindingCancels)
	t.Run("retry_skips_when_report_names_session_skipped_or_superseded", guardCase_ReportSkip)
	t.Run("retry_skips_actual_newer_generation_and_reports_it", guardCase_NewerGenerationSkip)
	t.Run("retry_returns_explicit_error_on_unexpected_generation", guardCase_GenerationMismatch)
	t.Run("retry_same_generation_stopped_redrives_exact_cancellation", guardCase_SameGenerationRedrive)
}

// guardStageStoppedRoot drives a REAL healthy StopSession on the owner-root
// fixture BEFORE any card is admitted, asserts the record's durable stopped
// state, and returns the record's ACTUAL generation for the guard cases.
// Round-8 correction: the negative guard cases need a genuinely STOPPED
// record — on the fixture's idle (not-stopped) record, a disabled report or
// generation guard is masked by the unrelated `record.State != Stopped`
// early-out and the mutant survives. No record field is fabricated and no
// Stop is faked: the durable stop lands through the real machinery, exactly
// as in the applied retry-after-durable-stop case.
func guardStageStoppedRoot(t *testing.T, al *AgentLoop, sid string) int {
	t.Helper()
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
		t.Fatalf("SETUP: the healthy stop did not land the durable stopped state (state=%q) — the guard fixtures require a genuinely stopped record", record.State)
	}
	return record.Generation
}

// guardAssertQueueIntact pins "A still the active pending head, B still
// QUEUED" on the two-card fixture: B is proven queued by the COUNT (the
// registry counts active+queued), never by Get-invisibility alone.
func guardAssertQueueIntact(t *testing.T, reg *askuser.Registry, sid string, cardA, cardB *askuser.PendingSet) {
	t.Helper()
	active, ok := reg.PendingForSession(sid)
	if !ok || active.CardID != cardA.CardID || active.Status != askuser.StatusPending {
		t.Fatalf("the active head must remain A pending; got ok=%v card=%v", ok, active)
	}
	if got := reg.PendingCount(); got != 2 {
		t.Fatalf("pending count = %d, want 2 — B must remain QUEUED (the count includes hidden waiting sets), not merely Get-invisible", got)
	}
	if _, visible := reg.Get(cardB.CardID); visible {
		t.Fatal("B must stay hidden while queued")
	}
}

// guardCase_ContinuationUntouched pins the continuation skip: a binding made
// with continuation=true carries NO new card intent, so the bound cancel is
// a clean no-op — the real active A and real queued FIFO B are untouched,
// nothing resumes, and neither the registry's durable seam nor its reads are
// touched at all (the skip precedes even the head capture).
func guardCase_ContinuationUntouched(t *testing.T) {
	f := newAskStopFixture(t, &mockProvider{}, nil)
	f.admitTwoCards(t)
	before := f.meta.snapshot()

	ctx := f.al.bindPendingAskStop(context.Background(), f.sid, true)
	if err := f.al.cancelPendingAskForScope(ctx, f.sid); err != nil {
		t.Fatalf("a continuation-bound cancel must be a clean no-op without error: %v", err)
	}
	after := f.meta.snapshot()

	guardAssertQueueIntact(t, f.reg, f.sid, f.cardA, f.cardB)
	if n := f.resume.count(); n != 0 {
		t.Fatalf("resume dispatched %d time(s) on the continuation path; a continuation must not touch any card", n)
	}
	if after.pendingAskWrites != before.pendingAskWrites || after.metaReads != before.metaReads {
		t.Fatalf("the continuation path must not touch the durable seam at all (delta writes=%d reads=%d)",
			after.pendingAskWrites-before.pendingAskWrites, after.metaReads-before.metaReads)
	}
}

// guardCase_FreshBindingCancels is the explicit positive (CHECK F-1): a
// FRESH binding captures the live head A and the bound cancel cancels
// exactly that card, promoting B — an inert helper cannot pass this case.
func guardCase_FreshBindingCancels(t *testing.T) {
	f := newAskStopFixture(t, &mockProvider{}, nil)
	f.admitTwoCards(t)
	before := f.meta.snapshot()

	ctx := f.al.bindPendingAskStop(context.Background(), f.sid, false)
	if err := f.al.cancelPendingAskForScope(ctx, f.sid); err != nil {
		t.Fatalf("a fresh exact-binding cancel must succeed: %v", err)
	}
	after := f.meta.snapshot()

	if _, still := f.reg.Get(f.cardA.CardID); still {
		t.Fatal("the freshly captured head A must be cancelled")
	}
	active, ok := f.reg.PendingForSession(f.sid)
	if !ok || active.CardID != f.cardB.CardID || active.Status != askuser.StatusPending {
		t.Fatalf("B must be promoted to the active pending head; got ok=%v card=%v", ok, active)
	}
	if got := f.reg.PendingCount(); got != 1 {
		t.Fatalf("pending count = %d, want 1 (promoted B only)", got)
	}
	if n := f.resume.count(); n != 0 {
		t.Fatalf("session-stop cancellation dispatched %d resume(s); it never resumes", n)
	}
	if delta := after.pendingAskWrites - before.pendingAskWrites; delta != 1 {
		t.Fatalf("the exact cancellation durably persists exactly one cancel+promotion record; delta=%d", delta)
	}
}

// guardCase_ReportSkip pins the report-named skips of retryStoppedPendingAsk:
// when the cascade report already names the session in SkippedNewerGeneration
// or Superseded, the retry returns nil WITHOUT any mutation, emission or
// resume. Round-8: the cards are staged on a GENUINELY STOPPED record at its
// actual generation — with the report guard disabled, the flow can no longer
// hide behind the not-stopped early-out; it would reach the real cancellation
// and this case would fail (that is the kill for the measured surviving
// mutant). The report check precedes every store access, so on the correct
// implementation neither the durable seam nor the registry is touched at all.
func guardCase_ReportSkip(t *testing.T) {
	al, meta, reg, resume, _, sid := newAskSelectedStopFixture(t)
	stoppedGeneration := guardStageStoppedRoot(t, al, sid)
	cardA := askStopCard(sid, "Report-skip head question?")
	cardB := askStopCard(sid, "Report-skip queued question?")
	if createErr := reg.CreatePending(cardA); createErr != nil {
		t.Fatalf("fixture: CreatePending(A): %v", createErr)
	}
	if createErr := reg.CreatePending(cardB); createErr != nil {
		t.Fatalf("fixture: CreatePending(B): %v", createErr)
	}

	for name, report := range map[string]*steer.CancelReport{
		"skipped_newer_generation": {SkippedNewerGeneration: []string{sid}},
		"superseded":               {Superseded: []string{sid}},
	} {
		before := meta.snapshot()
		if err := al.retryStoppedPendingAsk(context.Background(), sid, stoppedGeneration, report); err != nil {
			t.Fatalf("%s: a report-named skip must return nil: %v", name, err)
		}
		after := meta.snapshot()
		guardAssertQueueIntact(t, reg, sid, cardA, cardB)
		if n := resume.count(); n != 0 {
			t.Fatalf("%s: resume dispatched %d time(s); a named skip must not touch any card", name, n)
		}
		if after.pendingAskWrites != before.pendingAskWrites || after.metaReads != before.metaReads {
			t.Fatalf("%s: the named skip must precede every store access (delta writes=%d reads=%d)",
				name, after.pendingAskWrites-before.pendingAskWrites, after.metaReads-before.metaReads)
		}
	}
}

// guardCase_NewerGenerationSkip pins the ACTUAL newer-generation skip: when
// the stopped session's real lifecycle generation is NEWER than the
// request's, the retry appends the session to report.SkippedNewerGeneration
// and returns nil without mutating any card. Round-8: the record is genuinely
// STOPPED at its actual generation, so disabled generation guards reach
// the real cancellation and this case fails — the skip is not masked by the
// not-stopped early-out. An obsolete request must never consume the newer
// generation's work.
func guardCase_NewerGenerationSkip(t *testing.T) {
	al, meta, reg, resume, _, sid := newAskSelectedStopFixture(t)
	stoppedGeneration := guardStageStoppedRoot(t, al, sid)
	cardA := askStopCard(sid, "Newer-generation head question?")
	cardB := askStopCard(sid, "Newer-generation queued question?")
	if createErr := reg.CreatePending(cardA); createErr != nil {
		t.Fatalf("fixture: CreatePending(A): %v", createErr)
	}
	if createErr := reg.CreatePending(cardB); createErr != nil {
		t.Fatalf("fixture: CreatePending(B): %v", createErr)
	}
	before := meta.snapshot()

	report := &steer.CancelReport{}
	if err := al.retryStoppedPendingAsk(context.Background(), sid, stoppedGeneration-1, report); err != nil {
		t.Fatalf("an actually newer generation must be a clean skip: %v", err)
	}

	reported := false
	for _, id := range report.SkippedNewerGeneration {
		if id == sid {
			reported = true
		}
	}
	if !reported {
		t.Fatalf("the retry must append the session to report.SkippedNewerGeneration; got %+v", report.SkippedNewerGeneration)
	}
	guardAssertQueueIntact(t, reg, sid, cardA, cardB)
	if n := resume.count(); n != 0 {
		t.Fatalf("resume dispatched %d time(s); a newer-generation skip must not touch any card", n)
	}
	after := meta.snapshot()
	if delta := after.pendingAskWrites - before.pendingAskWrites; delta != 0 {
		t.Fatalf("the newer-generation skip must not durably mutate any card; pending-ask write delta=%d", delta)
	}
	// The generation check legitimately reads the SEPARATE LifecycleStore,
	// which these MetaStore-seam counters cannot see; the counters' oracle
	// here is the registry side: zero durable mutation (asserted above).
}

// guardCase_GenerationMismatch pins the unexpected-mismatch arm: a request
// naming a generation ahead of the stored record must return an EXPLICIT error — never silently cancel —
// and must not record a skip (the session was not superseded; something is
// genuinely wrong). Round-8: the record is genuinely STOPPED, so a mutant
// that returns nil instead of the required mismatch error fails the explicit
// error assertion; removing the mismatch guard entirely reaches cancellation.
func guardCase_GenerationMismatch(t *testing.T) {
	al, meta, reg, resume, _, sid := newAskSelectedStopFixture(t)
	stoppedGeneration := guardStageStoppedRoot(t, al, sid)
	cardA := askStopCard(sid, "Mismatch head question?")
	cardB := askStopCard(sid, "Mismatch queued question?")
	if createErr := reg.CreatePending(cardA); createErr != nil {
		t.Fatalf("fixture: CreatePending(A): %v", createErr)
	}
	if createErr := reg.CreatePending(cardB); createErr != nil {
		t.Fatalf("fixture: CreatePending(B): %v", createErr)
	}
	before := meta.snapshot()

	report := &steer.CancelReport{}
	if err := al.retryStoppedPendingAsk(context.Background(), sid, stoppedGeneration+1, report); err == nil {
		t.Fatal("an unexpected generation mismatch must return an explicit error, never silently cancel or skip")
	}
	if len(report.SkippedNewerGeneration) != 0 {
		t.Fatalf("a mismatch is not a superseded skip; SkippedNewerGeneration must stay empty, got %+v", report.SkippedNewerGeneration)
	}
	guardAssertQueueIntact(t, reg, sid, cardA, cardB)
	if n := resume.count(); n != 0 {
		t.Fatalf("resume dispatched %d time(s); a refused mismatch must not touch any card", n)
	}
	after := meta.snapshot()
	if delta := after.pendingAskWrites - before.pendingAskWrites; delta != 0 {
		t.Fatalf("the refused mismatch must not durably mutate any card; pending-ask write delta=%d", delta)
	}
}

// guardCase_SameGenerationRedrive is the second explicit positive: a FRESH
// binding for the session's CURRENT, durably-stopped generation re-drives
// the retained card cancellation — A is cancelled and B promoted — which is
// exactly the founder retry rule the guards exist to permit. The stopped
// precondition is the same lawful helper the negative cases use.
func guardCase_SameGenerationRedrive(t *testing.T) {
	al, meta, reg, resume, _, sid := newAskSelectedStopFixture(t)
	stoppedGeneration := guardStageStoppedRoot(t, al, sid)

	// Retained cards on the stopped session, then the fresh binding + retry.
	cardA := askStopCard(sid, "Same-generation retained head?")
	cardB := askStopCard(sid, "Same-generation retained queue?")
	if createErr := reg.CreatePending(cardA); createErr != nil {
		t.Fatalf("fixture: CreatePending(A): %v", createErr)
	}
	if createErr := reg.CreatePending(cardB); createErr != nil {
		t.Fatalf("fixture: CreatePending(B): %v", createErr)
	}
	before := meta.snapshot()

	ctx := al.bindPendingAskStop(context.Background(), sid, false)
	report := &steer.CancelReport{}
	if err := al.retryStoppedPendingAsk(ctx, sid, stoppedGeneration, report); err != nil {
		t.Fatalf("a fresh exact-generation stopped retry must re-drive the retained cancellation: %v", err)
	}
	after := meta.snapshot()

	if _, still := reg.Get(cardA.CardID); still {
		t.Fatal("the same-generation retry must cancel the captured retained head A")
	}
	active, ok := reg.PendingForSession(sid)
	if !ok || active.CardID != cardB.CardID || active.Status != askuser.StatusPending {
		t.Fatalf("B must be promoted to the active pending head; got ok=%v card=%v", ok, active)
	}
	if got := reg.PendingCount(); got != 1 {
		t.Fatalf("pending count = %d, want 1 (promoted B only)", got)
	}
	if n := resume.count(); n != 0 {
		t.Fatalf("session-stop cancellation dispatched %d resume(s); it never resumes", n)
	}
	if delta := after.pendingAskWrites - before.pendingAskWrites; delta != 1 {
		t.Fatalf("the re-driven cancellation durably persists exactly one cancel+promotion record; delta=%d", delta)
	}
}
