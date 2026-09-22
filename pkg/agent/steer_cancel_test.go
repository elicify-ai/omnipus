package agent

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

func testSteerLifecycleRecord(id, parent string, state session.LifecycleState, generation int) *session.LifecycleRecord {
	rec := &session.LifecycleRecord{
		SessionID:        id,
		Generation:       generation,
		State:            state,
		Origin:           &session.Origin{Kind: session.OriginKindChat},
		OwnerScopeKind:   session.OwnerScopeHuman,
		ParentDurableKey: "deliberately-not-the-edge",
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
