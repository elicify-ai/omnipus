// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// PW-D6/PW-D13 (founder 2026-10-15): cause-stop never rings; a native restart
// cut-off resumes after its durable landing. The historical plan below keeps
// its ledger/identity guarantees, not its superseded ring/no-dispatch clauses.
// C3/C5 of ADR-20261004 (steering commands: no person question) — the
// fence-less stop's durable identity and boot's two independent obligations.
//
// C3: EVERY actual stop transition — fenced or fence-less — is appended to
// the existing control ledger in the same landing that writes the stop note.
// The fence-less line fabricates NO accepted Stop control; it names the
// landing execution instead, and its stop_seq is the next MONOTONIC ledger
// sequence (never the generation stand-in), so two fence-less stops in one
// generation never share one notice id. Untaken notices are discovered from
// that ledger, so a RESUME clearing the active note can never lose one; the
// note-derived fallback is retired. Same notice format; no timer.
//
// C5: at boot the two obligations are INDEPENDENT — pass one stops every
// interrupted queued/running helper (cause restart, its transition ledgered)
// even when an older notice is still untaken or its replay fails, and it
// runs BEFORE any parent wake; pass two re-rings every untaken notice from
// ledger history. A replay outcome never skips the current-run stop and
// never starts a run (D8.5 kept).
//
// TEST PLAN (elicify-test-writing step 1, filled before the first assertion)
//
// Specification source for every expected value: the ADR's locked decision 1
// ("a stop notice rings until the parent takes it, including a stop with no
// saved control history, and including after a restart; a second ring does
// not make the parent do the work twice; no periodic timer, no second
// notice format") and Corrections C3/C5 — never the code under test.
//
// Unit boundary — what is real: the production completion boundary
// (steer_completion.go::completeSteeredTurn) for the fence-less landings,
// the production cascade (steer_cancel.go::SteerCanceller.StopTurns/Revive)
// for the fenced stop and the resume, the real file-backed LifecycleStore +
// control ledger + MessageInboxStore, the production boot entry
// (boot_sweep.go::SteerBootRecovery.recoverSteered) over a fresh loop on the
// same durable home. Injected only at existing production seams: the parked
// provider double, the boot notice callback, and the notifier observer.
//
// Case table (one row per oracle):
//   - a fence-less landing ledgered its transition in the landing itself:
//     ControlID empty (no fabricated control), the landing execution's
//     run_id present, stop_seq the store allocated
//   - two fence-less stops in ONE generation -> two ledger transitions with
//     strictly advancing seqs -> two DISTINCT notice ids
//   - the parent takes the FIRST notice -> a further delivery pass rings
//     the SECOND still (and the first zero times)
//   - a ring re-rings the stored line, never re-appends, never re-stops the
//     child, never fabricates history
//   - an untaken fence-less notice still rings after a RESUME cleared the
//     record's active note (discovered from the ledger, not the note)
//   - stop -> untaken notice -> resume (message-shape: dispatch) -> crash
//     -> boot leaves the resumed run STOPPED (no run started, D8.5), lands
//     the restart stop's own ledgered transition, and the OLD notice is
//     still owed and rings again
//
// Mutations that would break these (CHECK's duty): derive the fence-less
// notice from the retained note again (the resume-clears-note case loses the
// ring); reuse the generation as the fence-less stop_seq (the two-stops case
// collides on one id); let the replay outcome gate the current-run stop (the
// boot case leaves the resumed run running); skip the fence-less ledger
// write (every fence-less case loses its identity and its ring).
package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// c3LandedLine is one control-ledger line's on-disk shape — the only place
// the fence-less line's landing-execution identity (run_id beside an absent
// control_id) is observable.
type c3LandedLine struct {
	ControlID  string `json:"control_id"`
	Generation int    `json:"generation"`
	Cause      string `json:"cause"`
	Actor      string `json:"actor"`
	RunID      string `json:"run_id,omitempty"`
	LandedStop *struct {
		Generation int    `json:"generation"`
		Cause      string `json:"cause"`
		Actor      string `json:"actor"`
		RunID      string `json:"run_id,omitempty"`
	} `json:"landed_stop"`
}

// c3LedgerRawLines reads the child's control ledger and returns every line.
func c3LedgerRawLines(t *testing.T, al *AgentLoop, childID string) []c3LandedLine {
	t.Helper()
	path := filepath.Join(al.GetConfig().Agents.Defaults.Home, "session_lifecycle", "controls", childID+".jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read control ledger %s: %v", path, err)
	}
	ledgerLines := jsonLines(raw)
	lines := make([]c3LandedLine, 0, len(ledgerLines))
	for _, line := range ledgerLines {
		var l c3LandedLine
		if err := json.Unmarshal(line, &l); err != nil {
			t.Fatalf("parse control ledger line of %s: %v", childID, err)
		}
		lines = append(lines, l)
	}
	return lines
}

// jsonLines splits raw JSONL bytes into non-empty lines.
func jsonLines(raw []byte) [][]byte {
	var out [][]byte
	start := 0
	for i := 0; i <= len(raw); i++ {
		if i == len(raw) || raw[i] == '\n' {
			if i > start {
				out = append(out, raw[start:i])
			}
			start = i + 1
		}
	}
	return out
}

// c3ResumeLiveDispatch resumes a stopped child through the production Revive
// (the explicit resume command's durable half) and dispatches it again — a
// message-shaped resume into a genuinely live run, the state C5's boot case
// crashes from.
func c3ResumeLiveDispatch(t *testing.T, al *AgentLoop, lifecycle *session.LifecycleStore, childID string, generation int, entered <-chan string) int {
	t.Helper()
	owner := w1hOwner("c3-resume-owner")
	gen, err := NewSteerCanceller(lifecycle).Revive(context.Background(), childID, owner)
	if err != nil {
		t.Fatalf("Revive(%s): %v", childID, err)
	}
	if gen != generation {
		t.Fatalf("Revive(%s) minted generation %d, want the same-generation resume %d", childID, gen, generation)
	}
	res, err := NewSteerLauncher(al).Dispatch(context.Background(), childID, gen)
	if err != nil {
		t.Fatalf("Dispatch(resumed %s): %v", childID, err)
	}
	if res.State != steer.DispatchRunning {
		t.Fatalf("Dispatch(resumed %s) = %+v, want running — the resume must produce a live run for the boot case", childID, res)
	}
	select {
	case <-entered:
	case <-time.After(30 * time.Second):
		t.Fatalf("resumed child %s never reached its provider — no live run to crash", childID)
	}
	return gen
}

// TestFencelessLedger_TwoStopsOneGeneration_TakenFirst_SecondStillRings is
// the C3 identity oracle: a fence-less landing ledgered its transition (no
// fabricated control, the landing execution named), and two fence-less stops
// in one generation carry strictly advancing stop_seqs — so taking the first
// stop's notice leaves the second stop's distinct notice untaken and
// ringing.
func TestFencelessLedger_TwoStopsOneGeneration_TakenFirst_SecondStillRings(t *testing.T) {
	al, _, _ := w1hSetup(t)
	provider, releaseTurns := w1hInstallSignalProvider(t, al)
	t.Cleanup(releaseTurns)
	lifecycle := al.GetSessionLifecycleStore()

	parentID := newTestSteeringSession(t, al, "ws-c3-two-stops")
	wakes := w1hObserveWakes(t, al)
	rec := w1hLaunchLiveChild(t, al, provider.entered, parentID, "call-c3-two-stops")
	childID, generation := rec.SessionID, rec.Generation

	// D2: only the producing execution lands its result. Interrupt the real
	// owner's provider without accepting a Stop control, then join the entire
	// completion/disposal tail before Revive; hand-completing its record while
	// the live execution is registered would leave the old owner in place.
	stopOwner := func(selected *session.LifecycleRecord) {
		t.Helper()
		handle := al.getActiveTurnState(childID)
		if handle == nil || al.tsExecutionClaim(handle, childID) != al.executionClaimFor(selected) {
			t.Fatal("setup: fence-less stop has no matching real admitted owner (D2)")
		}
		turnIDs, stopErr := al.InterruptSessionHard(childID, ScopeSelfOnly, "fence-less owner cancellation")
		if stopErr != nil || len(turnIDs) != 1 || turnIDs[0] != handle.turnID {
			t.Fatalf("InterruptSessionHard reached %v, err=%v; want only owning turn %q", turnIDs, stopErr, handle.turnID)
		}
		joinGoalFixtureRuns(t, al)
		if al.steerAdmission().hasExecutionReservation(al.executionClaimFor(selected)) {
			t.Fatal("setup: stopped owner's reservation survived its joined disposal (D2)")
		}
	}
	stopOwner(rec)
	trs, err := lifecycle.ListStoppedTransitions(childID)
	if err != nil {
		t.Fatalf("ListStoppedTransitions(after stop 1): %v", err)
	}
	if len(trs) != 1 {
		t.Fatalf("landed history after the fence-less landing = %s, want exactly 1 transition — the landing ledgered it in the same lock hold (C3)", w1hFormatTransitions(trs))
	}
	if trs[0].ControlID != "" {
		t.Fatalf("fence-less transition carries control_id %q, want empty — a fence-less stop fabricates no accepted Stop control (C3)", trs[0].ControlID)
	}
	if trs[0].Generation != generation || trs[0].ParentSessionID != parentID || trs[0].Cause != session.StopCauseStop {
		t.Fatalf("fence-less transition = %s, want {gen:%d parent:%q cause:stop}", w1hFormatTransitions(trs), generation, parentID)
	}
	for _, line := range c3LedgerRawLines(t, al, childID) {
		if line.LandedStop != nil && line.LandedStop.RunID == "" {
			t.Fatalf("fence-less landed line of %s names no landing execution (empty run_id) — C3 requires the landing execution's identity on the line", childID)
		}
	}
	notice1 := w1hNoticeID(parentID, childID, generation, trs[0].StopSeq)
	if got := len(w1hNoticesWithID(t, al, parentID, notice1)); got != 1 {
		t.Fatalf("parent inbox holds %d line(s) of %s after stop 1, want exactly 1", got, notice1)
	}
	// PW-D13 (founder 2026-10-15): neither intentional Stop may ring.
	if c := wakes.count(notice1); c != 0 {
		t.Errorf("first intentional Stop rang the parent %d time(s) for %s, want 0 (PW-D13)", c, notice1)
	}

	// Resume (Revive), then dispatch — a live run again, SAME generation.
	if _, loadErr := lifecycle.Load(childID); loadErr != nil {
		t.Fatalf("Load(before resume): %v", loadErr)
	}
	c3ResumeLiveDispatch(t, al, lifecycle, childID, generation, provider.entered)
	resumed, err := lifecycle.Load(childID)
	if err != nil {
		t.Fatalf("Load(after resume): %v", err)
	}
	if resumed.Generation != generation || resumed.State != session.LifecycleRunning {
		t.Fatalf("after the resume the child is state=%q generation=%d, want running at generation %d", resumed.State, resumed.Generation, generation)
	}
	if resumed.StopNote != nil {
		t.Fatalf("after the resume the record still carries StopNote=%v — the note was not cleared, the C3 discovery case is not exercised", resumed.StopNote)
	}

	// Fence-less stop #2, same generation, carried out by its new real owner.
	stopOwner(resumed)
	trs2, err := lifecycle.ListStoppedTransitions(childID)
	if err != nil {
		t.Fatalf("ListStoppedTransitions(after stop 2): %v", err)
	}
	if len(trs2) != 2 {
		t.Fatalf("landed history after two fence-less stops = %s, want exactly 2 transitions — one per actual stop (C3)", w1hFormatTransitions(trs2))
	}
	if trs2[1].Generation != generation {
		t.Fatalf("second transition = %s, want generation %d — both stops are one generation's stops", w1hFormatTransitions(trs2), generation)
	}
	if trs2[1].StopSeq <= trs2[0].StopSeq {
		t.Fatalf("second stop_seq %d does not advance the first %d — the fence-less stop takes the next monotonic stop sequence, never a shared identity (C3)", trs2[1].StopSeq, trs2[0].StopSeq)
	}
	notice2 := w1hNoticeID(parentID, childID, generation, trs2[1].StopSeq)
	if notice2 == notice1 {
		t.Fatalf("two fence-less stops in one generation share one notice id %s — C3 forbids one identity for two stops", notice1)
	}
	if got := len(w1hNoticesWithID(t, al, parentID, notice2)); got != 1 {
		t.Fatalf("parent inbox holds %d line(s) of %s after stop 2, want exactly 1", got, notice2)
	}
	if c := wakes.count(notice2); c != 0 {
		t.Errorf("second intentional Stop rang the parent %d time(s) for %s, want 0 (PW-D13)", c, notice2)
	}
	// PW-D13 keeps the first intentional Stop silent even while it is untaken.
	if c := wakes.count(notice1); c != 0 {
		t.Errorf("second landing left the first Stop notice at %d ring(s), want 0 (PW-D13)", c)
	}

	// The parent TAKES the FIRST notice — the production ack the wake
	// consumer makes.
	if ackErr := al.GetMessageInboxStore().Ack(parentID, []string{notice1}); ackErr != nil {
		t.Fatalf("parent ack of %s: %v", notice1, ackErr)
	}
	if !rerNoteAcked(t, al, parentID, notice1) {
		t.Fatalf("the ack of %s did not persist — the taken-first scenario is unobservable", notice1)
	}

	// The next delivery pass rings the SECOND notice still — the first is
	// taken and stays silent.
	rerStartupPass(t, al, childID)
	if c := wakes.count(notice2); c != 0 {
		t.Errorf("after taking the first notice, the second intentional Stop rang %d time(s), want 0 (PW-D13)", c)
	}
	if c := wakes.count(notice1); c != 0 {
		t.Errorf("the taken first intentional Stop rang %d time(s), want still 0 (PW-D13)", c)
	}
	if rerNoteAcked(t, al, parentID, notice2) {
		t.Errorf("taking the first notice also acknowledged the second distinct Stop notice %s", notice2)
	}
	// The ring is a doorbell: one durable line per notice, no new history,
	// the child untouched by it.
	if got := len(w1hNoticesWithID(t, al, parentID, notice2)); got != 1 {
		t.Errorf("parent inbox holds %d line(s) of %s after the re-ring, want exactly 1 — the ring never re-appends", got, notice2)
	}
	if got := len(w1hNoticesWithID(t, al, parentID, notice1)); got != 1 {
		t.Errorf("parent inbox holds %d line(s) of %s after the pass, want exactly 1", got, notice1)
	}
	if trs3, trs3Err := lifecycle.ListStoppedTransitions(childID); trs3Err != nil || len(trs3) != 2 {
		t.Errorf("landed history after the pass = %s (err %v), want still 2 — a ring never fabricates history", w1hFormatTransitions(trs3), trs3Err)
	}
	cur, err := lifecycle.Load(childID)
	if err != nil {
		t.Fatalf("Load(after the pass): %v", err)
	}
	if cur.State != session.LifecycleStopped || cur.Generation != generation {
		t.Errorf("after the pass the child is state=%q generation=%d, want stopped at generation %d — a ring never applies the stop twice", cur.State, cur.Generation, generation)
	}
}

// TestFencelessLedger_UntakenNoticeStillRingsAfterResumeClearsTheNote is the
// C3 discovery oracle: an untaken fence-less notice is discovered from the
// LEDGER, so the RESUME clearing the record's active note cannot lose it —
// the exact hole the retired note-derived fallback had.
func TestFencelessLedger_UntakenNoticeStillRingsAfterResumeClearsTheNote(t *testing.T) {
	al, _, _ := w1hSetup(t)
	provider, releaseTurns := w1hInstallSignalProvider(t, al)
	t.Cleanup(releaseTurns)
	lifecycle := al.GetSessionLifecycleStore()

	parentID := newTestSteeringSession(t, al, "ws-c3-resume-ring")
	wakes := w1hObserveWakes(t, al)
	rec := w1hLaunchLiveChild(t, al, provider.entered, parentID, "call-c3-resume-ring")
	childID, generation := rec.SessionID, rec.Generation

	if err := al.completeSteeredTurn(context.Background(), rec, turnResult{}, context.Canceled); err != nil {
		t.Fatalf("completeSteeredTurn (fence-less stop): %v", err)
	}
	trs, err := lifecycle.ListStoppedTransitions(childID)
	if err != nil {
		t.Fatalf("ListStoppedTransitions(after the fence-less stop): %v", err)
	}
	if len(trs) != 1 {
		t.Fatalf("landed history = %s, want exactly 1 fence-less transition", w1hFormatTransitions(trs))
	}
	noticeID := w1hNoticeID(parentID, childID, generation, trs[0].StopSeq)
	// PW-D13 (founder 2026-10-15): clearing a Stop note cannot make it wake.
	if c := wakes.count(noticeID); c != 0 {
		t.Errorf("intentional Stop landing rang the parent %d time(s) for %s, want 0 (PW-D13)", c, noticeID)
	}
	if rerNoteAcked(t, al, parentID, noticeID) {
		t.Fatalf("%s is already acked — the untaken-notice scenario requires an untaken note", noticeID)
	}

	// The RESUME: Revive clears the record's active note (and effect) —
	// exactly the mutation that lost an untaken notice under the retired
	// note-derived fallback.
	owner := w1hOwner("c3-resume-ring-owner")
	if _, reviveErr := NewSteerCanceller(lifecycle).Revive(context.Background(), childID, owner); reviveErr != nil {
		t.Fatalf("Revive(%s): %v", childID, reviveErr)
	}
	resumed, err := lifecycle.Load(childID)
	if err != nil {
		t.Fatalf("Load(after Revive): %v", err)
	}
	if resumed.StopNote != nil || resumed.StopEffect != nil {
		t.Fatalf("after Revive the record still carries StopNote=%v StopEffect=%v — the note was not cleared, the case is not exercised", resumed.StopNote, resumed.StopEffect)
	}
	if trsAfter, trsAfterErr := lifecycle.ListStoppedTransitions(childID); trsAfterErr != nil || len(trsAfter) != 1 {
		t.Fatalf("landed history after the resume = %s (err %v), want still exactly 1 — clearing the note must not delete the ledgered transition (C3)", w1hFormatTransitions(trsAfter), trsAfterErr)
	}

	// The next delivery pass discovers the transition from the LEDGER and
	// rings the parent again for the untaken notice.
	rerStartupPass(t, al, childID)
	if c := wakes.count(noticeID); c != 0 {
		t.Errorf("after explicit resume cleared the note, the old intentional Stop rang %d time(s), want 0 (PW-D13)", c)
	}
	if got := len(w1hNoticesWithID(t, al, parentID, noticeID)); got != 1 {
		t.Errorf("parent inbox holds %d line(s) of %s after the re-ring, want exactly 1 — the ring re-rings the stored line", got, noticeID)
	}
	// The ring is never a re-stop: the resumed record stays resumed.
	cur, err := lifecycle.Load(childID)
	if err != nil {
		t.Fatalf("Load(after the pass): %v", err)
	}
	if cur.State != session.LifecycleQueued {
		t.Errorf("after the pass the resumed child is state=%q, want queued — a ring must not re-stop the child", cur.State)
	}
	if trsAfter, err := lifecycle.ListStoppedTransitions(childID); err != nil || len(trsAfter) != 1 {
		t.Errorf("landed history after the pass = %s (err %v), want still 1 — a ring never fabricates history", w1hFormatTransitions(trsAfter), err)
	}
}

// PW-D6/PW-D13 (founder 2026-10-15): after explicit resume and a later crash,
// boot retains the old Stop notice silently, lands a distinct restart event,
// then resumes the native run. The old notice cannot suppress that new event.
// Preserve both landed transitions and their distinct stop-sequence identities.
func TestFencelessLedger_BootStopsResumedRun_OldUntakenNoticeStillOwed(t *testing.T) {
	al, _, _ := w1hSetup(t)
	provider, releaseTurns := w1hInstallSignalProvider(t, al)
	t.Cleanup(releaseTurns)
	lifecycle := al.GetSessionLifecycleStore()

	provisionTestWorkspace(t, "ws-c5-boot", testDefaultAgentID) // PW-SEC-01 fixture provisioning
	parentID := newTestSteeringSession(t, al, "ws-c5-boot")
	wakes := w1hObserveWakes(t, al)
	rec := w1hLaunchLiveChild(t, al, provider.entered, parentID, "call-c5-boot")
	childID, generation := rec.SessionID, rec.Generation

	// A real accepted stop interrupts the real live turn; its notice rings
	// once in this process and is never taken.
	owner := w1hOwner("c5-boot-owner")
	if _, err := NewSteerCanceller(lifecycle).StopTurns(context.Background(), childID, owner, false, al.SteerGenerationCancel); err != nil {
		t.Fatalf("StopTurns: %v", err)
	}
	select {
	case <-provider.exited:
	case <-time.After(15 * time.Second):
		t.Fatal("the stop never reached the live turn — no completing stop to witness")
	}
	var oldNoticeID string
	if settled := w1hWaitFor(t, 10*time.Second, "stop 1 settled (landed + notice + ring)", func() bool {
		trs, err := lifecycle.ListStoppedTransitions(childID)
		if err != nil || len(trs) != 1 {
			return false
		}
		oldNoticeID = w1hNoticeID(parentID, childID, generation, trs[0].StopSeq)
		return len(w1hNoticesWithID(t, al, parentID, oldNoticeID)) == 1
	}); !settled {
		trs, _ := lifecycle.ListStoppedTransitions(childID)
		t.Fatalf("setup: stop 1 never settled: transitions=%s", w1hFormatTransitions(trs))
	}
	// PW-D13 (founder 2026-10-15): the old Stop is durable but never a wake.
	joinGoalFixtureRuns(t, al)
	if got := wakes.count(oldNoticeID); got != 0 {
		t.Errorf("old intentional Stop rang %d time(s), want 0 (PW-D13)", got)
	}
	if rerNoteAcked(t, al, parentID, oldNoticeID) {
		t.Fatalf("setup: %s is already acked — the owed-notice scenario requires an untaken notice", oldNoticeID)
	}

	// Resume and run again, then "crash": a fresh loop over the SAME durable
	// home, its doorbell born silent.
	c3ResumeLiveDispatch(t, al, lifecycle, childID, generation, provider.entered)
	resumed, err := lifecycle.Load(childID)
	if err != nil {
		t.Fatalf("Load(resumed): %v", err)
	}
	if resumed.State != session.LifecycleRunning {
		t.Fatalf("setup: resumed child is state=%q, want running — nothing to interrupt at boot otherwise", resumed.State)
	}
	home := al.GetConfig().Agents.Defaults.Home
	restart := rerRestartLoop(t, home)
	resumeGate := newGoalRunGate("native continuation after restart", nil)
	installGoalRunProvider(t, restart, resumeGate)
	rings := w1hObserveWakes(t, restart)
	if c := rings.count(oldNoticeID); c != 0 {
		t.Fatalf("setup: the fresh process's doorbell already rang %d time(s) for %s — it must start silent", c, oldNoticeID)
	}
	// The restarted process mints ONE genuine epoch over the SAME real boot
	// epoch directory the interrupted process minted from; that persisted
	// Current is the WRITING boot, strictly after the interrupted admitting boot.
	writingBoot := session.NewBootEpochStore(filepath.Join(home, "boot_epoch"))
	writingEpoch, mintErr := writingBoot.Mint()
	if mintErr != nil || writingEpoch <= resumed.ExecutionID.BootSeq {
		t.Fatalf("setup: restart Mint=%d err=%v, interrupted admitting boot=%d", writingEpoch, mintErr, resumed.ExecutionID.BootSeq)
	}
	restart.SetBootEpochStore(writingBoot)

	// The BOOT pass — recoverSteered, the full entry a restarted process
	// runs. C5 pass one (the current-run stop) must land even though the old
	// notice above is still untaken; pass two (the historical replay) rings
	// everything still owed.
	recovery := &SteerBootRecovery{
		Lifecycle:  restart.GetSessionLifecycleStore(),
		Sessions:   restart.GetSessionStore(),
		Inbox:      restart.GetMessageInboxStore(),
		BootEpoch:  writingBoot,
		Classifier: NewSteerRecordClassifier(restart.GetSessionLifecycleStore(), restart.GetSessionStore()),
		Deliverer:  restart.getUpwardDeliverer(),
	}
	recovery.recoverSteered(context.Background(), childID, func(string, string) {})

	// PW-D6 (founder 2026-10-15): the durable restart stop precedes a native resume.
	landing := pwLegacyRestartLanding(t, restart, childID, writingEpoch)
	if landing.Generation != generation || landing.Stop != nil || landing.Terminal() || landing.StopNote.By != session.StopActorRestart {
		t.Fatalf("restart landing=%+v, want the original stopped, unfenced, same-generation event", landing)
	}
	after, err := restart.GetSessionLifecycleStore().Load(childID)
	if err != nil {
		t.Fatalf("Load(after boot): %v", err)
	}
	if after.State != session.LifecycleRunning || after.Generation != generation || after.ExecutionID == nil ||
		after.ExecutionID.RunID == resumed.ExecutionID.RunID || after.ExecutionID.BootSeq != writingEpoch {
		t.Fatalf("after boot native helper=%q generation=%d execution=%+v, want a fresh admitted continuation in boot %d (PW-D6)", after.State, after.Generation, after.ExecutionID, writingEpoch)
	}
	awaitGoalProvider(t, resumeGate)

	// ORACLE (C3 + C5 pass one): the restart stop is its OWN ledgered
	// transition — cause restart, same generation, seq advancing the old
	// stop's — and its notice is discovered from that ledger history.
	trs, err := restart.GetSessionLifecycleStore().ListStoppedTransitions(childID)
	if err != nil {
		t.Fatalf("ListStoppedTransitions(after boot): %v", err)
	}
	if len(trs) != 2 {
		t.Fatalf("landed history after boot = %s, want exactly 2 transitions — the old stop and the boot's restart stop", w1hFormatTransitions(trs))
	}
	restartTr := trs[1]
	if restartTr.Cause != session.StopCauseRestart || restartTr.Actor != session.StopActorRestart || restartTr.Generation != generation {
		t.Fatalf("boot transition = %s, want {cause:restart actor:restart gen:%d}", w1hFormatTransitions([]session.StoppedTransition{restartTr}), generation)
	}
	if restartTr.StopSeq <= trs[0].StopSeq {
		t.Fatalf("restart stop_seq %d does not advance the old stop's %d — the fence-less restart stop takes the next monotonic sequence (C3)", restartTr.StopSeq, trs[0].StopSeq)
	}
	restartNoticeID := w1hNoticeID(parentID, childID, generation, restartTr.StopSeq)
	if got := len(w1hNoticesWithID(t, restart, parentID, restartNoticeID)); got != 1 {
		t.Errorf("parent inbox holds %d line(s) of the restart notice %s after boot, want exactly 1 — the fresh stop's notice is discovered from its ledgered transition", got, restartNoticeID)
	}

	// ORACLE (C5 pass two): the OLD notice is still owed — one durable line
	// under its UNCHANGED id, still untaken, and the fresh process rang it.
	if got := len(w1hNoticesWithID(t, restart, parentID, oldNoticeID)); got != 1 {
		t.Errorf("parent inbox holds %d line(s) of the old notice %s after boot, want exactly 1 — the ring re-rings the stored line under the same id", got, oldNoticeID)
	}
	if c := rings.count(oldNoticeID); c != 0 {
		t.Errorf("the fresh process rang the parent %d time(s) for old intentional Stop %s, want 0 (PW-D13)", c, oldNoticeID)
	}
	if c := rings.count(restartNoticeID); c != 1 {
		t.Errorf("fresh restart notice rang %d time(s), want 1 distinct event wake (PW-D6/PW-D17)", c)
	}
	if rerNoteAcked(t, restart, parentID, oldNoticeID) {
		t.Errorf("the old notice %s came back acked — nothing in a boot pass takes a notice on the parent's behalf", oldNoticeID)
	}
}
