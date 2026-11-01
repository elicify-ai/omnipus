package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

func TestCapRED_SteerRelease_BatchDrainsAllConsumers(t *testing.T) {
	for _, consumer := range []string{"drainSteerQueue", "finishExecutionDisposition", "retainSelectedStop"} {
		t.Run(consumer, func(t *testing.T) { capRedReleaseConsumer(t, consumer, false) })
	}
}
func TestCapRED_SteerPromotion_FailureIsolatedAcrossBatch(t *testing.T) {
	for _, consumer := range []string{"drainSteerQueue", "finishExecutionDisposition", "retainSelectedStop"} {
		t.Run(consumer, func(t *testing.T) { capRedReleaseConsumer(t, consumer, true) })
	}
}
func capRedReleaseConsumer(t *testing.T, consumer string, failOne bool) {
	t.Helper()
	high, ok := false, true
	capRedMemory(t, &high, &ok)
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	provider, release := installParkedProvider(t, al)
	al.GetConfig().Performance.MaxParallelAgents = 1
	parent := newTestSteeringSession(t, al, testHarnessWorkspaceMembershipID)
	holder := capRedLaunchChild(t, al, parent, "holding-reservation")
	claim := executionClaim{SessionID: holder.SessionID, Generation: holder.Generation, RunID: "release-owner", BootSeq: al.bootEpochFor()}
	if err := al.GetSessionLifecycleStore().Mutate(holder.SessionID, func(r *session.LifecycleRecord) error {
		r.State = session.LifecycleRunning
		r.ExecutionID = &session.ExecutionIdentity{RunID: claim.RunID, BootSeq: claim.BootSeq}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	capRedAssert(t, "real holder reservation", capRedSteer(t, al.steerAdmission(), claim.SessionID, claim.Generation, claim.RunID, claim.BootSeq).granted, true)
	var execution *executionDisposition
	if consumer == "finishExecutionDisposition" {
		var err error
		g := al.steerAdmission()
		g.entryMu.Lock()
		execution, err = al.attachSteeredExecution(claim)
		g.entryMu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	}
	children := make([]steer.LaunchResult, 3)
	for i := range children {
		children[i] = capRedLaunchChild(t, al, parent, fmt.Sprintf("batch-child-%d", i))
		d, err := NewSteerLauncher(al).Dispatch(context.Background(), children[i].SessionID, children[i].Generation)
		if err != nil {
			t.Fatal(err)
		}
		capRedAssert(t, "child really queued", d.State, steer.DispatchQueued)
	}
	claims := make([]executionClaim, 3)
	for i, c := range children {
		claims[i] = capRedClaim(t, al, c.SessionID)
	}
	if failOne {
		path := filepath.Join(al.GetSessionLifecycleStore().Dir(), children[0].SessionID+".jsonl")
		// Read and preserve the real file, then inject an actual filesystem loss.
		bytes, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := os.WriteFile(path, bytes, 0o600); err != nil {
				t.Errorf("restore injected lifecycle loss: %v", err)
			}
		}()
	}
	registered := make(chan string, 4)
	var seenMu sync.Mutex
	seen := map[string]int{}
	turnRegisteredTestHook = func(id string, _ *turnState) {
		for i, c := range children {
			if c.SessionID == id {
				seenMu.Lock()
				seen[id]++
				seenMu.Unlock()
				// Every successful sibling must already own its reservation when
				// any member of the returned grant batch starts registration.
				for j, c2 := range claims {
					if failOne && j == 0 {
						continue
					}
					if !al.steerAdmission().hasExecutionReservation(c2) {
						t.Errorf("S58 consumer %s dispatched child %d before reserving batch sibling %d", consumer, i, j)
					}
				}
				registered <- id
			}
		}
	}
	defer func() { release(); al.drainSteeredTurns(3 * time.Second); turnRegisteredTestHook = nil }()
	al.GetConfig().Performance.MaxParallelAgents = 3
	switch consumer {
	case "drainSteerQueue":
		al.drainSteerQueue(claim)
	case "finishExecutionDisposition":
		if err := al.finishExecutionDisposition(execution); err != nil {
			t.Fatal(err)
		}
	case "retainSelectedStop":
		c := NewSteerCanceller(al.GetSessionLifecycleStore(), nil)
		if _, err := c.StopTurns(context.Background(), holder.SessionID, steer.Principal{Kind: steer.PrincipalKindHuman, ID: "cap-test"}, false, nil); err != nil {
			t.Fatal(err)
		}
		selected, current, err := al.durableStopSelectionFor(claim)
		if err != nil || !current {
			t.Fatalf("actual accepted Stop pair: current=%t err=%v", current, err)
		}
		ctx := withStopSelection(context.Background(), selected)
		if _, current, err := al.retainSelectedStop(ctx, holder.SessionID, holder.Generation, nil); err != nil || !current {
			t.Fatalf("real selected-stop consumer: current=%t err=%v", current, err)
		}
	}
	want := 3
	if failOne {
		want = 2
	}
	for i := 0; i < want; i++ {
		select {
		case <-registered:
		case <-time.After(3 * time.Second):
			t.Fatalf("S58 %s dispatched fewer than %d successful batch members", consumer, want)
		}
		select {
		case <-provider.entered:
		case <-time.After(3 * time.Second):
			t.Fatal("registered promoted child did not enter its real body")
		}
	}
	seenMu.Lock()
	defer seenMu.Unlock()
	for i, c := range children {
		wantCount := 1
		if failOne && i == 0 {
			wantCount = 0
		}
		capRedAssert(t, "each successful promotion exactly once", seen[c.SessionID], wantCount)
	}
	capRedAssert(t, "whole batch removed from queue", al.steerAdmission().queueLen(), 0)
}
