package agent

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

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
