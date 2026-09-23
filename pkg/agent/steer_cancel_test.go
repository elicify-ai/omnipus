package agent

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

func testSteerLifecycleRecord(id, parent string, state session.LifecycleState, generation int) *session.LifecycleRecord {
	rec := &session.LifecycleRecord{
		SessionID:      id,
		Generation:     generation,
		State:          state,
		Origin:         &session.Origin{Kind: session.OriginKindChat},
		OwnerScopeKind: session.OwnerScopeHuman,
	}
	if parent != "" {
		rec.Origin = &session.Origin{Kind: session.OriginKindDelegate}
		rec.SteeredBy = &session.SteeredBy{
			SteeringSessionID: parent,
			RootSessionID:     "root",
		}
	}
	return rec
}

func persistSteerLifecycle(t *testing.T, store *session.LifecycleStore, rec *session.LifecycleRecord) {
	t.Helper()
	if err := store.Persist(rec); err != nil {
		t.Fatalf("persist lifecycle %q: %v", rec.SessionID, err)
	}
}

func TestReserveDispatch_RefusedAfterStop(t *testing.T) {
	rec := testSteerLifecycleRecord("child", "root", session.LifecycleRunning, 3)
	rec.Stop = &session.Stop{Generation: 3}

	ok, reason := reserveDispatch(rec, 3)

	if ok {
		t.Fatal("reserveDispatch admitted a generation stopped by its current marker")
	}
	if reason != steer.ErrDispatchCancelled.Error() {
		t.Fatalf("reason = %q, want %q", reason, steer.ErrDispatchCancelled.Error())
	}
}

func TestReserveDispatch_StaleWakeRefused(t *testing.T) {
	rec := testSteerLifecycleRecord("child", "root", session.LifecycleRunning, 2)

	ok, reason := reserveDispatch(rec, 1)

	if ok {
		t.Fatal("reserveDispatch admitted a wake from an older generation")
	}
	if reason != steer.ErrStaleGeneration.Error() {
		t.Fatalf("reason = %q, want %q", reason, steer.ErrStaleGeneration.Error())
	}
}

func TestReserveDispatch_TerminalWithoutFollowUpRefused(t *testing.T) {
	rec := testSteerLifecycleRecord("child", "root", session.LifecycleCompleted, 1)

	ok, reason := reserveDispatch(rec, 1)

	if ok {
		t.Fatal("reserveDispatch admitted a terminal record without a follow-up generation")
	}
	if reason != steer.ErrTerminal.Error() {
		t.Fatalf("reason = %q, want %q", reason, steer.ErrTerminal.Error())
	}
}

func TestCascade_StampsAndCancelsReenteredChild(t *testing.T) {
	store := session.NewLifecycleStore(t.TempDir())
	for _, rec := range []*session.LifecycleRecord{
		testSteerLifecycleRecord("root", "", session.LifecycleRunning, 1),
		testSteerLifecycleRecord("a", "root", session.LifecycleRunning, 2),
		testSteerLifecycleRecord("b", "a", session.LifecycleRunning, 3),
		testSteerLifecycleRecord("c", "b", session.LifecycleRunning, 4),
	} {
		persistSteerLifecycle(t, store, rec)
	}

	var mu sync.Mutex
	gotGenerations := map[string]int{}
	canceller := NewSteerCanceller(store, func(_ context.Context, id string, generation int) (GenerationCancelResult, error) {
		mu.Lock()
		gotGenerations[id] = generation
		mu.Unlock()
		return GenerationCancelResult{Found: true, Cancelled: true}, nil
	})
	report, err := canceller.CancelSubtree(context.Background(), "root", steer.Principal{Kind: steer.PrincipalKindHuman, ID: "operator"})
	if err != nil {
		t.Fatalf("CancelSubtree: %v", err)
	}

	for id, wantGeneration := range map[string]int{"root": 1, "a": 2, "b": 3, "c": 4} {
		rec, loadErr := store.Load(id)
		if loadErr != nil {
			t.Fatalf("load %q: %v", id, loadErr)
		}
		if rec.Stop == nil || rec.Stop.Generation != wantGeneration {
			t.Fatalf("%s Stop = %#v, want generation %d", id, rec.Stop, wantGeneration)
		}
		if gotGenerations[id] != wantGeneration {
			t.Fatalf("cancel generation for %s = %d, want %d", id, gotGenerations[id], wantGeneration)
		}
	}
	if !slices.Equal(report.Reached, []string{"root", "a", "b", "c"}) {
		t.Fatalf("Reached = %v", report.Reached)
	}
	if len(report.Unreachable) != 0 || len(report.SkippedNewerGeneration) != 0 || len(report.SkippedTerminal) != 0 {
		t.Fatalf("unexpected partial report: %+v", report)
	}
}

func TestCascade_TerminalDescendantSkipped(t *testing.T) {
	store := session.NewLifecycleStore(t.TempDir())
	persistSteerLifecycle(t, store, testSteerLifecycleRecord("root", "", session.LifecycleRunning, 1))
	persistSteerLifecycle(t, store, testSteerLifecycleRecord("done", "root", session.LifecycleCompleted, 1))

	canceller := NewSteerCanceller(store)
	report, err := canceller.CancelSubtree(context.Background(), "root", steer.Principal{Kind: steer.PrincipalKindHuman, ID: "operator"})
	if err != nil {
		t.Fatalf("CancelSubtree: %v", err)
	}

	if !slices.Equal(report.SkippedTerminal, []string{"done"}) {
		t.Fatalf("SkippedTerminal = %v, want [done]", report.SkippedTerminal)
	}
	rec, err := store.Load("done")
	if err != nil {
		t.Fatalf("load done: %v", err)
	}
	if rec.Stop != nil {
		t.Fatalf("terminal record was mutated with Stop %#v", rec.Stop)
	}
}

func TestCascade_CancelCarriesGeneration_RevivedSkipped(t *testing.T) {
	store := session.NewLifecycleStore(t.TempDir())
	persistSteerLifecycle(t, store, testSteerLifecycleRecord("root", "", session.LifecycleRunning, 1))
	persistSteerLifecycle(t, store, testSteerLifecycleRecord("child", "root", session.LifecycleRunning, 1))

	canceller := NewSteerCanceller(store, func(_ context.Context, id string, generation int) (GenerationCancelResult, error) {
		if id != "child" {
			return GenerationCancelResult{}, nil
		}
		var newGeneration int
		if err := store.Mutate(id, func(rec *session.LifecycleRecord) error {
			rec.Generation++
			newGeneration = rec.Generation
			return nil
		}); err != nil {
			return GenerationCancelResult{}, err
		}
		if newGeneration != generation+1 {
			return GenerationCancelResult{}, errors.New("revival did not advance generation")
		}
		return GenerationCancelResult{Found: true, SkippedNewerGeneration: true}, nil
	})

	report, err := canceller.CancelSubtree(context.Background(), "root", steer.Principal{Kind: steer.PrincipalKindHuman, ID: "operator"})
	if err != nil {
		t.Fatalf("CancelSubtree: %v", err)
	}
	if !slices.Equal(report.SkippedNewerGeneration, []string{"child"}) {
		t.Fatalf("SkippedNewerGeneration = %v, want [child]", report.SkippedNewerGeneration)
	}
	rec, err := store.Load("child")
	if err != nil {
		t.Fatalf("load child: %v", err)
	}
	if rec.Generation != 2 || rec.Stop == nil || rec.Stop.Generation != 1 {
		t.Fatalf("child after revival = generation %d Stop %#v", rec.Generation, rec.Stop)
	}
}

func TestCascade_SecondPassStampsLateChild(t *testing.T) {
	store := session.NewLifecycleStore(t.TempDir())
	persistSteerLifecycle(t, store, testSteerLifecycleRecord("root", "", session.LifecycleRunning, 1))
	persistSteerLifecycle(t, store, testSteerLifecycleRecord("a", "root", session.LifecycleRunning, 1))

	var once sync.Once
	canceller := NewSteerCanceller(store, func(_ context.Context, id string, _ int) (GenerationCancelResult, error) {
		if id == "a" {
			once.Do(func() {
				persistSteerLifecycle(t, store, testSteerLifecycleRecord("late", "a", session.LifecycleRunning, 1))
			})
		}
		return GenerationCancelResult{}, nil
	})
	report, err := canceller.CancelSubtree(context.Background(), "root", steer.Principal{Kind: steer.PrincipalKindHuman, ID: "operator"})
	if err != nil {
		t.Fatalf("CancelSubtree: %v", err)
	}

	if !slices.Contains(report.Reached, "late") {
		t.Fatalf("second enumeration did not reach late child: %+v", report)
	}
	rec, err := store.Load("late")
	if err != nil {
		t.Fatalf("load late: %v", err)
	}
	if rec.Stop == nil || rec.Stop.Generation != rec.Generation {
		t.Fatalf("late child Stop = %#v, generation = %d", rec.Stop, rec.Generation)
	}
}

func TestRevive_NewGeneration_OldMarkerInert(t *testing.T) {
	store := session.NewLifecycleStore(t.TempDir())
	rec := testSteerLifecycleRecord("child", "root", session.LifecycleRunning, 2)
	rec.Stop = &session.Stop{
		At:         time.Now().Add(-time.Minute).UTC(),
		Generation: 2,
		By:         steer.Principal{Kind: steer.PrincipalKindHuman, ID: "operator"},
	}
	persistSteerLifecycle(t, store, rec)

	var emittedGeneration int
	canceller := NewSteerCanceller(store).SetRevivalStateWriter(func(_ context.Context, sessionID string, generation int) error {
		persisted, err := store.Load(sessionID)
		if err != nil {
			return err
		}
		if persisted.Generation != generation || persisted.State != session.LifecycleRunning {
			return errors.New("revival state was emitted before the durable running write")
		}
		emittedGeneration = generation
		return nil
	})

	generation, err := canceller.Revive(context.Background(), "child", steer.Principal{Kind: steer.PrincipalKindHuman, ID: "operator"})
	if err != nil {
		t.Fatalf("Revive: %v", err)
	}
	if generation != 3 || emittedGeneration != 3 {
		t.Fatalf("generation = %d, emitted = %d, want 3", generation, emittedGeneration)
	}
	revived, err := store.Load("child")
	if err != nil {
		t.Fatalf("load revived child: %v", err)
	}
	if revived.State != session.LifecycleRunning || revived.Generation != 3 {
		t.Fatalf("revived record = state %q generation %d", revived.State, revived.Generation)
	}
	if revived.Stop == nil || revived.Stop.Generation != 2 {
		t.Fatalf("old Stop marker = %#v, want generation 2 retained as inert history", revived.Stop)
	}
	if ok, reason := reserveDispatch(revived, 3); !ok {
		t.Fatalf("new generation refused by old marker: %s", reason)
	}
}

func TestRevive_TerminalFollowUpMintsGeneration(t *testing.T) {
	store := session.NewLifecycleStore(t.TempDir())
	rec := testSteerLifecycleRecord("child", "root", session.LifecycleFailed, 4)
	rec.FailedReason = "prior failure"
	rec.NeedsInput = &session.NeedsInput{CorrelationID: "obsolete-question"}
	persistSteerLifecycle(t, store, rec)

	generation, err := NewSteerCanceller(store).Revive(
		context.Background(),
		"child",
		steer.Principal{Kind: steer.PrincipalKindAgent, ID: "parent-agent"},
	)
	if err != nil {
		t.Fatalf("Revive terminal record: %v", err)
	}
	if generation != 5 {
		t.Fatalf("generation = %d, want 5", generation)
	}
	revived, err := store.Load("child")
	if err != nil {
		t.Fatalf("load terminal follow-up: %v", err)
	}
	if revived.State != session.LifecycleRunning || revived.FailedReason != "" || revived.NeedsInput != nil {
		t.Fatalf("terminal follow-up retained terminal state: %+v", revived)
	}
}

func TestStopRevive_OrderUnderLock(t *testing.T) {
	for i := 0; i < 100; i++ {
		store := session.NewLifecycleStore(t.TempDir())
		rec := testSteerLifecycleRecord("child", "root", session.LifecycleRunning, 1)
		rec.Stop = &session.Stop{Generation: 1}
		persistSteerLifecycle(t, store, rec)
		canceller := NewSteerCanceller(store)

		start := make(chan struct{})
		errs := make(chan error, 2)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, _, err := canceller.stampStop("child", time.Now().UTC(), steer.Principal{Kind: steer.PrincipalKindHuman, ID: "stop"})
			errs <- err
		}()
		go func() {
			defer wg.Done()
			<-start
			_, err := canceller.Revive(context.Background(), "child", steer.Principal{Kind: steer.PrincipalKindHuman, ID: "revive"})
			errs <- err
		}()
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("iteration %d concurrent operation: %v", i, err)
			}
		}

		got, err := store.Load("child")
		if err != nil {
			t.Fatalf("iteration %d load: %v", i, err)
		}
		if got.Generation != 2 {
			t.Fatalf("iteration %d generation = %d, want 2", i, got.Generation)
		}
		if got.State != session.LifecycleRunning || got.Stop == nil || (got.Stop.Generation != 1 && got.Stop.Generation != 2) {
			t.Fatalf("iteration %d has torn Stop/Revive state: %+v", i, got)
		}
	}
}

// TestSteerGenerationCancel_NeverRanChildUnblocksParent is Finding 5
// (ADR-091 fix lane 2): the cascade terminalises descendants, and a RUNNING
// child's cancelled turn delivers "interrupted:" upward through
// completeSteeredTurn. A child that was only `queued` never ran a turn, so
// nothing ever produced that upward event or terminalised its record — its
// own parent's hasRunningOrQueuedDescendant kept seeing it forever. Produce
// the upward event, and land the record terminal, for a stamped-but-never-ran
// session too.
func TestSteerGenerationCancel_NeverRanChildUnblocksParent(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	lifecycle := al.GetSessionLifecycleStore()
	al.SetSteerAudienceDeps(
		NewSteerAudienceResolver(NewSteerRecordClassifier(lifecycle, al.GetSessionStore())),
		nil,
		NewSteerUpwardDeliverer(),
	)

	parentID := newTestSteeringSession(t, al, "ws-1")
	queuedID, _ := launchSteeredChild(t, al, parentID, "call-mid-queued", "stopped while queued, never runs")

	rec, err := lifecycle.Load(queuedID)
	if err != nil {
		t.Fatalf("Load(queued): %v", err)
	}
	if rec.State != session.LifecycleQueued {
		t.Fatalf("state right after Launch = %q, want queued", rec.State)
	}

	before, err := al.hasRunningOrQueuedDescendant(parentID)
	if err != nil {
		t.Fatalf("hasRunningOrQueuedDescendant (before stop): %v", err)
	}
	if !before {
		t.Fatal("test setup invalid: the parent must see the queued child before it is stopped")
	}

	canceller := al.steerCanceller()
	if _, err := canceller.CancelSubtree(context.Background(), queuedID, steer.Principal{Kind: steer.PrincipalKindHuman, ID: "operator"}); err != nil {
		t.Fatalf("CancelSubtree(queued): %v", err)
	}

	after, err := lifecycle.Load(queuedID)
	if err != nil {
		t.Fatalf("Load(queued after stop): %v", err)
	}
	if after.State != session.LifecycleCancelled {
		t.Fatalf("state after stop = %q, want cancelled — a queued child that never ran must still be terminalised by its own Stop", after.State)
	}

	blocked, err := al.hasRunningOrQueuedDescendant(parentID)
	if err != nil {
		t.Fatalf("hasRunningOrQueuedDescendant (after stop): %v", err)
	}
	if blocked {
		t.Fatal("the parent is still waiting on a subtree that will never report — the never-ran child produced no upward event")
	}
}

// --- ADR-091 fix lane RX-DELIVERY: reportSteeredSessionTerminalUpward ---
//
// The three defects proved below all live in ONE function, and all three have
// the same consequence: a child is written terminal that its parent will
// never hear about, so the parent waits for ever on a descendant that is
// already gone.

// recordingUpwardDeliverer is a steer.UpwardDeliverer test double that
// returns a scripted Delivery/error pair and can run a caller-supplied hook
// DURING Deliver — the window in which the real deliverer does its I/O (an
// inbox append, transcript writes, a parent wake) and in which a concurrent
// Stop or Revive can land.
type recordingUpwardDeliverer struct {
	mu       sync.Mutex
	events   []steer.UpwardEvent
	delivery steer.Delivery
	err      error
	// duringDeliver runs inside Deliver, before it returns.
	duringDeliver func()
}

func (d *recordingUpwardDeliverer) Deliver(_ context.Context, event steer.UpwardEvent) (steer.Delivery, error) {
	d.mu.Lock()
	d.events = append(d.events, event)
	hook := d.duringDeliver
	delivery, err := d.delivery, d.err
	d.mu.Unlock()
	if hook != nil {
		hook()
	}
	return delivery, err
}

func (d *recordingUpwardDeliverer) calls() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.events)
}

// wireTerminalReportDeliverer installs deliverer as al's ONE upward
// deliverer, alongside the real audience resolver the terminal-report path
// expects.
func wireTerminalReportDeliverer(al *AgentLoop, deliverer steer.UpwardDeliverer) {
	lifecycle := al.GetSessionLifecycleStore()
	al.SetSteerAudienceDeps(
		NewSteerAudienceResolver(NewSteerRecordClassifier(lifecycle, al.GetSessionStore())),
		nil,
		deliverer,
	)
}

// TestReportSteeredSessionTerminalUpward_FailedDeliveryLeavesRecordRunnable
// is DEFECT 1 (CRITICAL). reportSteeredSessionTerminalUpward's own doc
// comment promises it "Refuses ... whenever the record ... is already
// terminal, OR THE UPWARD DELIVERY ITSELF FAILS — never overwrites state it
// cannot also report." The code logged a WARN on a failed Deliver and then
// fell straight through to the terminal write, so the child landed terminal
// with NO inbox entry: nothing left to recover from in process, and the
// parent never told that a descendant went away.
func TestReportSteeredSessionTerminalUpward_FailedDeliveryLeavesRecordRunnable(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	lifecycle := al.GetSessionLifecycleStore()
	deliverer := &recordingUpwardDeliverer{err: errors.New("inbox append failed: disk full")}
	wireTerminalReportDeliverer(al, deliverer)
	parentID := newTestSteeringSession(t, al, "ws-1")
	childID, childGen := launchSteeredChild(t, al, parentID, "call-undeliverable", "work nobody will ever hear about")

	al.reportSteeredSessionTerminalUpward(context.Background(), childID, childGen,
		session.LifecycleFailed, steer.OutcomeFailed, "dispatch_failed: disk I/O error")

	if deliverer.calls() != 1 {
		t.Fatalf("Deliver called %d times, want exactly 1", deliverer.calls())
	}
	rec, err := lifecycle.Load(childID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	if rec.Terminal() {
		t.Fatalf("state = %q: the record was written TERMINAL after the upward report failed — "+
			"the parent will never learn this child died and nothing in process can repair it", rec.State)
	}
	if rec.FailedReason != "" {
		t.Fatalf("FailedReason = %q, want empty — no terminal disposition may be recorded for an undelivered report", rec.FailedReason)
	}
}

// TestReportSteeredSessionTerminalUpward_NoDelivererLeavesRecordRunnable is
// DEFECT 1's second undelivered branch: with no upward deliverer wired there
// is no inbox entry at all, so the terminal write must be refused for exactly
// the same reason.
func TestReportSteeredSessionTerminalUpward_NoDelivererLeavesRecordRunnable(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	lifecycle := al.GetSessionLifecycleStore()
	parentID := newTestSteeringSession(t, al, "ws-1")
	childID, childGen := launchSteeredChild(t, al, parentID, "call-no-deliverer", "work nobody will ever hear about")

	// Deliberately NOT wiring an upward deliverer.
	al.reportSteeredSessionTerminalUpward(context.Background(), childID, childGen,
		session.LifecycleCancelled, steer.OutcomeInterrupted, "interrupted: the session was cancelled")

	rec, err := lifecycle.Load(childID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	if rec.Terminal() {
		t.Fatalf("state = %q: the record was written TERMINAL with no upward deliverer wired — "+
			"no inbox entry exists and the parent will wait for ever", rec.State)
	}
}

// TestReportSteeredSessionTerminalUpward_StopLandingDuringDeliveryIsNotErased
// is DEFECT 2 (CRITICAL) — the stale read-then-write shape fix lane 1 already
// replaced with LifecycleStore.Mutate in completeSteeredTurn (Finding D) and
// steer_launcher.go::commitSteeredDispatchState. The function did
// Load -> Deliver (real I/O) -> mutate the PRE-Deliver snapshot -> Persist,
// so a Stop pressed while Deliver was running was silently erased: the
// snapshot's Stop == nil was written straight back over it.
func TestReportSteeredSessionTerminalUpward_StopLandingDuringDeliveryIsNotErased(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	lifecycle := al.GetSessionLifecycleStore()
	parentID := newTestSteeringSession(t, al, "ws-1")
	childID, childGen := launchSteeredChild(t, al, parentID, "call-stop-race", "work interrupted mid-report")

	stopAt := time.Now().UTC()
	deliverer := &recordingUpwardDeliverer{
		delivery: steer.Delivery{MessageID: childID + ":1:final", Outcome: steer.DeliveryWoke},
	}
	deliverer.duringDeliver = func() {
		// A human presses Stop while Deliver is still doing its I/O.
		if err := lifecycle.Mutate(childID, func(cur *session.LifecycleRecord) error {
			cur.Stop = &session.Stop{
				At:         stopAt,
				Generation: cur.Generation,
				By:         steer.Principal{Kind: steer.PrincipalKindHuman, ID: "operator"},
			}
			return nil
		}); err != nil {
			t.Errorf("stamp Stop during Deliver: %v", err)
		}
	}
	wireTerminalReportDeliverer(al, deliverer)

	al.reportSteeredSessionTerminalUpward(context.Background(), childID, childGen,
		session.LifecycleFailed, steer.OutcomeFailed, "dispatch_failed: disk I/O error")

	rec, err := lifecycle.Load(childID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	if rec.Stop == nil {
		t.Fatal("the Stop pressed during Deliver was ERASED: the durable record that Stop was ever pressed is gone")
	}
	if rec.Stop.Generation != childGen || !rec.Stop.At.Equal(stopAt) {
		t.Fatalf("Stop = %#v, want the marker stamped during delivery at generation %d", rec.Stop, childGen)
	}
	if rec.State == session.LifecycleFailed {
		t.Fatalf("state = %q: the pre-Deliver snapshot was written back over a record a Stop had just landed on", rec.State)
	}
}

// TestReportSteeredSessionTerminalUpward_StoredNotWokenLoggedAtError is
// DEFECT 3 (HIGH). steer.Delivery.Outcome carries the one fact that
// distinguishes "the parent knows" (DeliveryWoke) from "the parent will never
// know" (DeliveryStoredNotWoken), and every caller in the codebase discarded
// it with `_, err := deliverer.Deliver(...)`. On a TERMINAL report that is a
// parent stalled indefinitely, and it was completely invisible.
func TestReportSteeredSessionTerminalUpward_StoredNotWokenLoggedAtError(t *testing.T) {
	readLog := captureLogFile(t, logger.ERROR)
	al, cleanup := newSteerAL(t)
	defer cleanup()
	parentID := newTestSteeringSession(t, al, "ws-1")
	childID, childGen := launchSteeredChild(t, al, parentID, "call-stored-not-woken", "work whose parent is never woken")
	messageID := childID + ":1:final"
	wireTerminalReportDeliverer(al, &recordingUpwardDeliverer{
		delivery: steer.Delivery{MessageID: messageID, Outcome: steer.DeliveryStoredNotWoken},
	})

	al.reportSteeredSessionTerminalUpward(context.Background(), childID, childGen,
		session.LifecycleFailed, steer.OutcomeFailed, "dispatch_failed: disk I/O error")

	captured := readLog()
	if !strings.Contains(captured, `"level":"error"`) {
		t.Fatalf("a terminal report the parent was never woken for produced no ERROR line; captured log:\n%s", captured)
	}
	for _, want := range []string{childID, parentID, messageID, string(steer.DeliveryStoredNotWoken)} {
		if !strings.Contains(captured, want) {
			t.Errorf("captured ERROR log does not name %q; captured log:\n%s", want, captured)
		}
	}
	if !strings.Contains(captured, `"generation":`) {
		t.Errorf("captured ERROR log does not carry the generation; captured log:\n%s", captured)
	}
}

// TestReportSteeredSessionTerminalUpward_WokenDeliveryStaysQuiet is the
// negative half of DEFECT 3: a report the parent was actually woken for must
// produce no ERROR line at all, so the new signal stays worth reading.
func TestReportSteeredSessionTerminalUpward_WokenDeliveryStaysQuiet(t *testing.T) {
	readLog := captureLogFile(t, logger.ERROR)
	al, cleanup := newSteerAL(t)
	defer cleanup()
	parentID := newTestSteeringSession(t, al, "ws-1")
	childID, childGen := launchSteeredChild(t, al, parentID, "call-woken", "work whose parent is woken")
	wireTerminalReportDeliverer(al, &recordingUpwardDeliverer{
		delivery: steer.Delivery{MessageID: childID + ":1:final", Outcome: steer.DeliveryWoke},
	})

	al.reportSteeredSessionTerminalUpward(context.Background(), childID, childGen,
		session.LifecycleFailed, steer.OutcomeFailed, "dispatch_failed: disk I/O error")

	if captured := readLog(); strings.Contains(captured, `"level":"error"`) {
		t.Fatalf("a delivered-and-woken terminal report logged at ERROR; captured log:\n%s", captured)
	}
	rec, err := al.GetSessionLifecycleStore().Load(childID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	if rec.State != session.LifecycleFailed || rec.FailedReason != "dispatch_failed: disk I/O error" {
		t.Fatalf("state = %q / FailedReason = %q, want failed with the dispatch reason — "+
			"a successfully delivered report must still land terminal", rec.State, rec.FailedReason)
	}
}
