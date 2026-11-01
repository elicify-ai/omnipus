package agent

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func TestCapRED_SteerQueue_8to2_NoPromotionAboveLimit(t *testing.T) {
	high, ok := false, true
	capRedMemory(t, &high, &ok)
	g := newSteerAdmission(func() int { return 8 })
	holders := capRedFillSteer(t, g, 8)
	for i := 0; i < 5; i++ {
		d := capRedSteer(t, g, fmt.Sprintf("wait-%d", i), 1, fmt.Sprintf("wait-run-%d", i), 7)
		capRedAssert(t, "S33 queue position", d.position, i+1)
	}
	high = true
	// S31/B4: six completions take occupancy 8->2; none may transfer a slot.
	for i := 0; i < 6; i++ {
		batch := capRedRelease(t, g, holders[i])
		capRedAssert(t, fmt.Sprintf("S31 completion %d grants", i+1), len(batch), 0)
		capRedAssert(t, "no preemption and no growth", g.activeCount(), 7-i)
	}
	batch := capRedRelease(t, g, holders[6])
	capRedAssert(t, "seventh completion grants oldest only", capRedIDs(batch), []string{"wait-0"})
	if len(batch) == 1 {
		capRedAssert(t, "promotion retains full claim", batch[0].executionClaim(), executionClaim{SessionID: "wait-0", Generation: 1, RunID: "wait-run-0", BootSeq: 7})
	}
	capRedAssert(t, "pressure holds at two", g.activeCount(), 2)
	// Sustained pressure progresses only on a release, never above the floor.
	for i := 0; i < 4; i++ {
		c := executionClaim{SessionID: fmt.Sprintf("wait-%d", i), Generation: 1, RunID: fmt.Sprintf("wait-run-%d", i), BootSeq: 7}
		batch = capRedRelease(t, g, c)
		capRedAssert(t, "S33 sustained FIFO", capRedIDs(batch), []string{fmt.Sprintf("wait-%d", i+1)})
		capRedAssert(t, "S33 bounded occupancy", g.activeCount(), 2)
	}
	capRedAssert(t, "all five waiters progressed", g.queueLen(), 0)
}

func TestCapRED_SteerQueue_RecoveryDrainsOlderBeforeNewcomer(t *testing.T) {
	high, ok := true, true
	capRedMemory(t, &high, &ok)
	g := newSteerAdmission(func() int { return 8 })
	capRedFillSteer(t, g, 2)
	capRedSteer(t, g, "older-a", 1, "run-a", 1)
	capRedSteer(t, g, "older-b", 1, "run-b", 1)
	capRedAssert(t, "SETUP two genuinely queued", g.queueLen(), 2)
	high = false
	d := capRedSteer(t, g, "newcomer", 1, "run-c", 1)
	capRedAssert(t, "S32 grant batch has promotion owner", capRedIDs(d.grants), []string{"older-a", "older-b", "newcomer"})
	capRedAssert(t, "newcomer admitted after older work", d.granted, true)
	capRedAssert(t, "queue fully drained", g.queueLen(), 0)
	for _, c := range []executionClaim{
		{SessionID: "older-a", Generation: 1, RunID: "run-a", BootSeq: 1},
		{SessionID: "older-b", Generation: 1, RunID: "run-b", BootSeq: 1},
	} {
		capRedAssert(t, "older reservation installed before return", g.hasExecutionReservation(c), true)
	}
}

func TestCapRED_SteerPromotion_BatchInstallsAllReservations(t *testing.T) {
	high, ok := false, true
	capRedMemory(t, &high, &ok)
	capNow := 2
	g := newSteerAdmission(func() int { return capNow })
	holders := capRedFillSteer(t, g, 2)
	for i := 0; i < 4; i++ {
		capRedSteer(t, g, fmt.Sprintf("q%d", i), 3, fmt.Sprintf("run-q%d", i), 4)
	}
	capNow = 6
	// S32a: an admission event can return four older grants plus its own.
	d := capRedSteer(t, g, "arriving", 3, "arriving-run", 4)
	capRedAssert(t, "S32a ordered older batch", capRedIDs(d.grants), []string{"q0", "q1", "q2", "q3"})
	capRedAssert(t, "new arrival queued when older grants exhaust six", []any{d.granted, d.position, g.activeCount()}, []any{false, 1, 6})
	for i := 0; i < 4; i++ {
		capRedAssert(t, "reservation visible at batch return", g.hasExecutionReservation(executionClaim{SessionID: fmt.Sprintf("q%d", i), Generation: 3, RunID: fmt.Sprintf("run-q%d", i), BootSeq: 4}), true)
	}
	// Independent S58 release batch: free three places by raising configured cap.
	g2 := newSteerAdmission(func() int { return capNow })
	capNow = 2
	h2 := capRedFillSteer(t, g2, 2)
	for i := 0; i < 3; i++ {
		capRedSteer(t, g2, fmt.Sprintf("release-q%d", i), 1, fmt.Sprintf("release-run%d", i), 1)
	}
	capNow = 4
	batch := capRedRelease(t, g2, h2[0])
	capRedAssert(t, "S58 full release batch", capRedIDs(batch), []string{"release-q0", "release-q1", "release-q2"})
	for _, e := range batch {
		capRedAssert(t, "S58 installed atomically", g2.hasExecutionReservation(e.executionClaim()), true)
	}
	_ = holders // Holders deliberately remain admitted: a capacity change never preempts.
}

func TestCapRED_SteerQueue_SkipsBusyOwnerAndCancelsMiddle(t *testing.T) {
	high, ok := false, true
	capRedMemory(t, &high, &ok)
	capNow := 1
	g := newSteerAdmission(func() int { return capNow })
	holders := capRedFillSteer(t, g, 1)
	capRedSteer(t, g, holders[0].SessionID, 1, "followup", 1)
	capRedSteer(t, g, "eligible-a", 1, "a", 1)
	capRedSteer(t, g, "cancel-me", 1, "b", 1)
	capRedSteer(t, g, "eligible-c", 1, "c", 1)
	g.removeQueuedExecution(executionClaim{SessionID: "cancel-me", Generation: 1, RunID: "b", BootSeq: 1})
	capRedAssert(t, "S34 remove exactly middle claim", g.queueLen(), 3)
	capNow = 3
	d := capRedSteer(t, g, "arriving", 1, "d", 1)
	capRedAssert(t, "S32b skip active owner, not eligible older work", capRedIDs(d.grants), []string{"eligible-a", "eligible-c"})
	g.mu.Lock()
	ids := capRedIDs(g.queue)
	g.mu.Unlock()
	capRedAssert(t, "busy followup stays and newcomer joins behind", ids, []string{holders[0].SessionID, "arriving"})
}

func TestCapRED_SteerQueue_QueuedCancelRecomputesPositions(t *testing.T) {
	high, ok := false, true
	capRedMemory(t, &high, &ok)
	g := newSteerAdmission(func() int { return 1 })
	holders := capRedFillSteer(t, g, 1)
	for _, id := range []string{"a", "b", "c"} {
		capRedSteer(t, g, id, 1, "run-"+id, 1)
	}
	g.removeQueuedExecution(executionClaim{SessionID: "b", Generation: 1, RunID: "run-b", BootSeq: 1})
	g.mu.Lock()
	ids := capRedIDs(g.queue)
	g.mu.Unlock()
	capRedAssert(t, "S34 remaining FIFO", ids, []string{"a", "c"})
	batch := capRedRelease(t, g, holders[0])
	capRedAssert(t, "S34 oldest remaining promoted", capRedIDs(batch), []string{"a"})
}

func TestCapRED_SteerQueue_SameSessionSerializationNotCapacity(t *testing.T) {
	high, ok := false, true
	capRedMemory(t, &high, &ok)
	g := newSteerAdmission(func() int { return 8 })
	capRedSteer(t, g, "same", 1, "first", 1)
	d := capRedSteer(t, g, "same", 1, "followup", 1)
	capRedAssert(t, "S35 full decision present", d.complete, true)
	capRedAssert(t, "S35 serialization cause", []any{d.granted, d.position, d.cause}, []any{false, 1, "session_busy"})
	capRedAssert(t, "no extra reservation", g.activeCount(), 1)
}

func TestCapRED_SteerQueue_AlternatingReadings_OrderPreserved(t *testing.T) {
	high, ok := false, true
	capRedMemory(t, &high, &ok)
	configured := 3
	g := newSteerAdmission(func() int { return configured })
	holders := capRedFillSteer(t, g, 3)
	for i := 0; i < 8; i++ {
		capRedSteer(t, g, fmt.Sprintf("q%d", i), 1, fmt.Sprintf("qrun%d", i), 1)
	}
	configured = 8
	high = true
	first := capRedRelease(t, g, holders[0])
	capRedAssert(t, "S43a pressure at occupancy two grants none", len(first), 0)
	high = false
	second := capRedRelease(t, g, holders[1])
	capRedAssert(t, "S43a healthy drain oldest seven", capRedIDs(second), []string{"q0", "q1", "q2", "q3", "q4", "q5", "q6"})
	capRedAssert(t, "healthy occupancy eight", g.activeCount(), 8)
	high = true
	third := capRedRelease(t, g, holders[2])
	capRedAssert(t, "return to pressure cannot revoke or grow", []int{len(third), g.activeCount(), g.queueLen()}, []int{0, 7, 1})
}

func TestCapRED_DispatchSema_TwoAcquiresRaceRelease(t *testing.T) {
	high, ok := true, true
	capRedMemory(t, &high, &ok)
	ds := newDispatchSemaphore(8)
	held := capRedHold(t, ds, 2)
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan capRedDecision, 2)
	wg.Add(3)
	go func() { defer wg.Done(); <-start; held[0]() }()
	for i := 0; i < 2; i++ {
		go func() { defer wg.Done(); <-start; results <- capRedAcquire(t, ds) }()
	}
	close(start)
	wg.Wait()
	close(results)
	admitted := 0
	for d := range results {
		if d.granted {
			admitted++
			t.Cleanup(d.release)
		}
	}
	if admitted > 1 {
		t.Errorf("S31 race granted %d new slots with only one released at memory limit two", admitted)
	}
	if ds.InFlight() > 2 {
		t.Errorf("S31 pressure growth: occupancy=%d, want <=2", ds.InFlight())
	}
}

func TestCapRED_SteerRelease_StaleFullClaimCannotReleaseReplacement(t *testing.T) {
	high, ok := false, true
	capRedMemory(t, &high, &ok)
	g := newSteerAdmission(func() int { return 1 })
	capRedSteer(t, g, "same", 2, "replacement", 3)
	batch := capRedRelease(t, g, executionClaim{SessionID: "same", Generation: 2, RunID: "stale", BootSeq: 3})
	capRedAssert(t, "stale claim never grants", len(batch), 0)
	capRedAssert(t, "replacement reservation survives", g.activeCount(), 1)
	capRedAssert(t, "batch equality", reflect.DeepEqual(capRedIDs(batch), []string{}), true)
}
