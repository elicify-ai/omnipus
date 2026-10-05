// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// W1 historical direct-parent notice RED pack, part 3 — CRASH/RETRY WITNESS.
// ADR-20260928 sub-agent control plane (frozen asset cd20cf8b):
//
//   - D6 round-3 MAJ-001: "delivery failure remains pending, visibly reported
//     and retried at boot/periodic delivery, never silently considered
//     applied" — so a failed notice append must not un-land the stop: the
//     lifecycle stays stopped and the landed-stop history stays on the ledger
//     (W2a: the applied receipt waits for the notice, the STATE does not).
//   - Founder Q2=A: the pending notice survives a same-generation RESUME as a
//     historical event; the resume clears the ACTIVE note, never the ledger.
//   - D8.4: the boot notice pass dedups per (parent, child, generation,
//     stop_seq) and never wakes a stopped parent; a retry delivers the ONE
//     original notice with the ORIGINAL id/at/cause/actor.
//   - D8.5: a notice retry is not a restart — it must never stop (or
//     re-stop) the resumed child, and boot never resumes anything.
//
// Scenario (oracle fixed before touching this tree): a real live child is
// stopped through the production cascade while the DIRECT PARENT's inbox
// file is unwritable — a plain file permission on a normal dependency, no
// test-only hook. The stop must still land durably with its ledger event;
// the failure must be visible. An explicit same-generation RESUME then clears
// the active note while the historical event remains. After repairing the
// inbox, the NORMAL W1 retry/boot publisher
// (boot_sweep.go::SteerBootRecovery.recoverStoppedChildNotice — the only
// retry entry W1 has) must deliver exactly ONE notice carrying the ORIGINAL
// identity and content, without stopping the resumed child and without a
// duplicate wake.
//
// Known-red reason at this pin (verified by reading, before any run): W1's
// publisher reads the record's CURRENT TAIL ONLY. After the resume cleared
// the note, recoverStoppedChildNotice sees a queued record, falls into
// restartStopAndNotice, FABRICATES a restart stop note (By system/restart,
// At time.Now, Seq = generation stand-in) and re-lands the resumed child
// stopped — the opposite of delivering the original historical notice. W1
// has no retry entry that reads ListStoppedTransitions; that missing
// ledger-driven reading is the precise interface gap this RED names (the
// entry point itself exists and runs, so this is a defect, not a blocked
// test). No W3b boot-epoch/seq stand-in is used or needed here.
package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// w1hSignalProvider is parkedProvider plus one test-owned witness: a channel
// closed when the parked model call RETURNS — the deterministic signal that
// the cancelled turn has exited into its completion disposition. It is a test
// double at the provider edge, not a production hook.
type w1hSignalProvider struct {
	entered  chan string
	release  chan struct{}
	exited   chan struct{}
	exitOnce sync.Once
}

func (p *w1hSignalProvider) Chat(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	select {
	case p.entered <- "in":
	default:
	}
	select {
	case <-p.release:
		return &providers.LLMResponse{Content: "finished"}, nil
	case <-ctx.Done():
		p.exitOnce.Do(func() { close(p.exited) })
		return nil, ctx.Err()
	}
}

func (p *w1hSignalProvider) GetDefaultModel() string { return "w1h-signal-provider" }

func w1hInstallSignalProvider(t *testing.T, al *AgentLoop) (*w1hSignalProvider, func()) {
	t.Helper()
	agentInst, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("test agent is not registered")
	}
	provider := &w1hSignalProvider{
		entered: make(chan string, 8),
		release: make(chan struct{}),
		exited:  make(chan struct{}),
	}
	agentInst.Provider = provider
	var once sync.Once
	return provider, func() { once.Do(func() { close(provider.release) }) }
}

func TestW1HistoricalLedgerNotice_AppendFailureKeepsStopLandedAndResumedRetryDeliversOriginalOnce(t *testing.T) {
	al, _, release := w1hSetup(t)
	defer release()
	provider, releaseTurns := w1hInstallSignalProvider(t, al)
	defer releaseTurns()
	lifecycle := al.GetSessionLifecycleStore()

	parentID := newTestSteeringSession(t, al, "ws-w1-crash")
	rec := w1hLaunchLiveChild(t, al, provider.entered, parentID, "call-w1-crash")
	childID, generation := rec.SessionID, rec.Generation
	owner := w1hOwner("w1-crash-owner")
	wakes := w1hObserveWakes(t, al)

	// Instrument: create the parent's inbox file through the real store and
	// prove the write path works, then fault exactly that file.
	w1hProbeMessage(t, al, parentID, childID)
	w1hFaultWrites(t, w1hInboxPath(t, al, parentID))

	// A real accepted stop interrupts the real live turn.
	if _, err := NewSteerCanceller(lifecycle).StopTurns(context.Background(), childID, owner, false, al.SteerGenerationCancel); err != nil {
		t.Fatalf("StopTurns: %v", err)
	}
	select {
	case <-provider.exited:
	case <-time.After(15 * time.Second):
		t.Fatal("the stop never reached the live turn — no cancelled completion to witness")
	}
	// The desired behaviour lands the stop despite the failed notice append;
	// give that landing its full window before judging it.
	landed := w1hWaitFor(t, 10*time.Second, "stop landing despite failed notice", func() bool {
		cur, err := lifecycle.Load(childID)
		return err == nil && cur.State == session.LifecycleStopped
	})

	got, err := lifecycle.ListStoppedTransitions(childID)
	if err != nil {
		t.Fatalf("ListStoppedTransitions(after failed-append stop): %v", err)
	}
	// ORACLE (D6): the stop lands durably and its historical event remains,
	// regardless of the notice's delivery failure.
	if !landed {
		cur, _ := lifecycle.Load(childID)
		t.Errorf("child did not land stopped after its append-failed completion: state=%q — D6 keeps the notice PENDING, it never gates the stop's own durable landing", cur.State)
	}
	if len(got) != 1 {
		t.Errorf("landed-stop history = %s, want exactly one transition — the landed stop's real event must remain on the ledger even while its notice append fails", w1hFormatTransitions(got))
	}
	var tr1 session.StoppedTransition
	// Actor spelling convention for a human principal: human:<id> (the same
	// spelling the existing U1 pack's fixtures and the baseline RED log
	// carry); the oracle is that the transition names the principal that
	// ordered the stop.
	wantActor := "human:" + owner.ID
	if len(got) == 1 {
		tr1 = got[0]
		if tr1.StopSeq != 1 || tr1.Generation != generation || tr1.ParentSessionID != parentID || tr1.Cause != session.StopCauseStop || tr1.Actor != wantActor {
			t.Errorf("first transition = %+v, want {seq:1 gen:%d parent:%q cause:stop actor:%q} (D4/D6)", tr1, generation, parentID, wantActor)
		}
	}
	// The notice is pending: not in the (faulted) inbox, and no wake rode it.
	if ids := w1hStoppedNoticeIDsIn(t, al, parentID); len(ids) != 0 {
		t.Errorf("parent inbox holds stopped-notice traffic %v while the inbox file is unwritable — the append cannot have succeeded", ids)
	}
	if c := wakes.count(w1hNoticeID(parentID, childID, generation, tr1.StopSeq)); c != 0 {
		t.Errorf("parent woken %d time(s) for a notice whose append failed — D6's wake rides a durable notice", c)
	}

	// ORACLE (D6/D8.4): the failure is VISIBLE, not swallowed — the normal W1
	// retry/boot publisher reports the pending notice.
	var pass1Failures []string
	recovery := &SteerBootRecovery{
		Lifecycle:  lifecycle,
		Sessions:   al.GetSessionStore(),
		Inbox:      al.GetMessageInboxStore(),
		Classifier: NewSteerRecordClassifier(lifecycle, al.GetSessionStore()),
		Deliverer:  al.getUpwardDeliverer(),
	}
	pass1Rec, err := lifecycle.Load(childID)
	if err != nil {
		t.Fatalf("Load(before retry pass 1): %v", err)
	}
	recovery.recoverStoppedChildNotice(context.Background(), pass1Rec, func(key, message string) {
		pass1Failures = append(pass1Failures, key+": "+message)
	})
	if len(pass1Failures) == 0 {
		t.Errorf("the retry/boot publisher reported no visible failure for the undeliverable notice of %s — D6 requires the pending delivery to be visibly reported, never silently considered applied", childID)
	} else if !strings.Contains(strings.Join(pass1Failures, "; "), childID) {
		t.Errorf("retry/boot publisher failures %v do not name the affected child %s", pass1Failures, childID)
	}

	// Explicit same-generation RESUME: the active note clears, the
	// historical event and its pending notice survive it.
	if _, reviveErr := NewSteerCanceller(lifecycle).Revive(context.Background(), childID, owner); reviveErr != nil {
		t.Fatalf("Revive (explicit RESUME): %v", reviveErr)
	}
	resumedRec, err := lifecycle.Load(childID)
	if err != nil {
		t.Fatalf("Load(after RESUME): %v", err)
	}
	if resumedRec.State != session.LifecycleQueued || resumedRec.Generation != generation || resumedRec.StopNote != nil {
		t.Fatalf("record after RESUME = state %q gen %d note %v, want queued, same generation, active note cleared (D2 CRIT-001)",
			resumedRec.State, resumedRec.Generation, resumedRec.StopNote)
	}
	afterResume, err := lifecycle.ListStoppedTransitions(childID)
	if err != nil {
		t.Fatalf("ListStoppedTransitions(after RESUME): %v", err)
	}
	if len(afterResume) != 1 {
		t.Errorf("landed history after RESUME = %s, want the one original transition (founder Q2=A: the historical event survives the same-generation RESUME)", w1hFormatTransitions(afterResume))
	}

	// Repair the inbox, then run the NORMAL W1 retry/boot publisher again.
	w1hRestoreWrites(t, w1hInboxPath(t, al, parentID))
	var pass2Failures []string
	pass2Rec, err := lifecycle.Load(childID)
	if err != nil {
		t.Fatalf("Load(before retry pass 2): %v", err)
	}
	recovery.recoverStoppedChildNotice(context.Background(), pass2Rec, func(key, message string) {
		pass2Failures = append(pass2Failures, key+": "+message)
	})

	// ORACLE (Q2=A, D8.4): exactly ONE original notice appears.
	id1 := w1hNoticeID(parentID, childID, generation, tr1.StopSeq)
	notices := w1hNoticesWithID(t, al, parentID, id1)
	if len(notices) != 1 {
		t.Errorf("parent inbox holds %d notice(s) with original id %s after the repaired retry, want exactly 1 (D8.4 dedup per transition)", len(notices), id1)
	}
	for _, msg := range notices {
		w1hAssertNoticeMatchesTransition(t, msg, parentID, tr1)
	}
	// ORACLE (D8.5): the notice retry is not a restart — the resumed child
	// must not be stopped by it.
	afterRetry, err := lifecycle.Load(childID)
	if err != nil {
		t.Fatalf("Load(after retry pass 2): %v", err)
	}
	if afterRetry.State == session.LifecycleStopped {
		t.Errorf("the notice retry re-stopped the resumed child (state=%q) — a pending-notice retry must never fabricate a restart stop (D8.5); failures seen: %v", afterRetry.State, pass2Failures)
	}
	afterRetryTransitions, err := lifecycle.ListStoppedTransitions(childID)
	if err != nil {
		t.Fatalf("ListStoppedTransitions(after retry pass 2): %v", err)
	}
	if len(afterRetryTransitions) != 1 {
		t.Errorf("landed history after the retry = %s, want still exactly the original transition — a retry must not fabricate history", w1hFormatTransitions(afterRetryTransitions))
	}
	// ORACLE (D8.4): no duplicate wake for the one original notice.
	if c := wakes.count(id1); c != 1 {
		t.Errorf("wakes for the original notice %s = %d, want exactly 1 across the whole failure/repair/retry cycle", id1, c)
	}
	if len(pass2Failures) != 0 {
		t.Errorf("the repaired retry pass still reported failures %v — after repair the original notice delivers cleanly", pass2Failures)
	}
}
