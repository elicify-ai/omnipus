// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"sync"
	"testing"
)

func TestDispatchSema_TryAcquire_WithinCap(t *testing.T) {
	ds := newDispatchSemaphore(3)
	ok, rel := ds.TryAcquire()
	if !ok {
		t.Fatal("expected TryAcquire to succeed within cap")
	}
	if ds.InFlight() != 1 {
		t.Fatalf("want InFlight=1, got %d", ds.InFlight())
	}
	rel()
	if ds.InFlight() != 0 {
		t.Fatalf("want InFlight=0 after release, got %d", ds.InFlight())
	}
}

func TestDispatchSema_TryAcquire_AtCap(t *testing.T) {
	ds := newDispatchSemaphore(2)

	ok1, rel1 := ds.TryAcquire()
	ok2, rel2 := ds.TryAcquire()
	if !ok1 || !ok2 {
		t.Fatal("first two acquires should succeed")
	}
	defer rel1()
	defer rel2()

	// Third acquire must fail (cap=2).
	ok3, rel3 := ds.TryAcquire()
	if ok3 {
		rel3()
		t.Fatal("expected TryAcquire to fail at cap")
	}
	if ds.InFlight() != 2 {
		t.Fatalf("want InFlight=2, got %d", ds.InFlight())
	}
}

func TestDispatchSema_Release_AllowsNextAcquire(t *testing.T) {
	ds := newDispatchSemaphore(1)

	ok, rel := ds.TryAcquire()
	if !ok {
		t.Fatal("first acquire must succeed")
	}
	// At cap.
	ok2, _ := ds.TryAcquire()
	if ok2 {
		t.Fatal("should be at cap")
	}
	// Release one slot.
	rel()
	// Now a new acquire should succeed.
	ok3, rel3 := ds.TryAcquire()
	if !ok3 {
		t.Fatal("acquire should succeed after release")
	}
	rel3()
}

func TestDispatchSema_Resize_GrowCap(t *testing.T) {
	ds := newDispatchSemaphore(1)
	ok, rel := ds.TryAcquire()
	if !ok {
		t.Fatal("first acquire must succeed")
	}
	defer rel()

	// At cap. Resize to 2.
	ds.Resize(2)
	if ds.Cap() != 2 {
		t.Fatalf("want cap=2 after resize, got %d", ds.Cap())
	}
	ok2, rel2 := ds.TryAcquire()
	if !ok2 {
		t.Fatal("should succeed after capacity growth")
	}
	rel2()
}

func TestDispatchSema_Resize_ShrinkCap(t *testing.T) {
	ds := newDispatchSemaphore(4)
	ok1, rel1 := ds.TryAcquire()
	ok2, rel2 := ds.TryAcquire()
	if !ok1 || !ok2 {
		t.Fatal("first two acquires must succeed")
	}
	defer rel1()
	defer rel2()

	// Shrink to 2. The two in-flight slots still hold; new acquire fails.
	ds.Resize(2)
	if ds.Cap() != 2 {
		t.Fatalf("want cap=2 after shrink, got %d", ds.Cap())
	}
	ok3, rel3 := ds.TryAcquire()
	if ok3 {
		rel3()
		t.Fatal("should fail — already at effective cap after shrink")
	}
}

// TestDispatchSema_TryAcquire_DoubleReleaseIsIdempotent is the regression
// proof for fix-wave finding #5: TryAcquire used to return the shared
// ds.release METHOD VALUE directly — every call got the literal same
// function, so releasing one caller's slot twice silently freed a slot that
// some OTHER, still-legitimately-running holder currently occupied. Acquire
// two slots (A, B) at a cap of 2, release A twice, and confirm InFlight
// reflects exactly "A released, B still held" (1), never "both released" (0)
// — the double-release of A must never touch B's slot.
func TestDispatchSema_TryAcquire_DoubleReleaseIsIdempotent(t *testing.T) {
	ds := newDispatchSemaphore(2)

	okA, relA := ds.TryAcquire()
	okB, relB := ds.TryAcquire()
	if !okA || !okB {
		t.Fatal("both acquires must succeed at cap=2")
	}
	if ds.InFlight() != 2 {
		t.Fatalf("want InFlight=2 after both acquires, got %d", ds.InFlight())
	}

	relA()
	relA() // double-release of the SAME slot: must be a no-op the second time

	if got := ds.InFlight(); got != 1 {
		t.Fatalf("want InFlight=1 after releasing A once (double-release must not double-count) "+
			"— B's slot must still be held — got %d", got)
	}

	relB()
	if got := ds.InFlight(); got != 0 {
		t.Fatalf("want InFlight=0 after both real slots are released, got %d", got)
	}
}

// TestDispatchSema_WaitAndAcquire_ReleaseIsAlsoIndependentPerCall pins that
// WaitAndAcquire's release (fix-wave finding #5's newRelease, shared with
// TryAcquire) is likewise a fresh closure per acquisition, not the same
// shared method value two different WaitAndAcquire callers could conflate.
func TestDispatchSema_WaitAndAcquire_ReleaseIsAlsoIndependentPerCall(t *testing.T) {
	ds := newDispatchSemaphore(2)

	relA := ds.WaitAndAcquire()
	relB := ds.WaitAndAcquire()
	if ds.InFlight() != 2 {
		t.Fatalf("want InFlight=2 after both WaitAndAcquire calls, got %d", ds.InFlight())
	}

	relA()
	relA() // double-release must not free B's slot

	if got := ds.InFlight(); got != 1 {
		t.Fatalf("want InFlight=1 after double-releasing A, got %d", got)
	}
	relB()
	if got := ds.InFlight(); got != 0 {
		t.Fatalf("want InFlight=0 after releasing both, got %d", got)
	}
}

func TestDispatchSema_ConcurrentAcquireRelease(t *testing.T) {
	// 50 goroutines each try to acquire and release once. With cap=5, at most 5
	// are in-flight at any moment. Verify no slot counter overflows or panics.
	const total = 50
	ds := newDispatchSemaphore(5)
	var wg sync.WaitGroup
	wg.Add(total)
	for i := 0; i < total; i++ {
		go func() {
			defer wg.Done()
			// WaitAndAcquire blocks until a slot is free.
			rel := ds.WaitAndAcquire()
			// Verify we're within bounds.
			if in := ds.InFlight(); in < 1 || in > 5 {
				t.Errorf("InFlight=%d out of bounds [1,5]", in)
			}
			rel()
		}()
	}
	wg.Wait()
	if ds.InFlight() != 0 {
		t.Fatalf("want InFlight=0 after all goroutines finish, got %d", ds.InFlight())
	}
}

func TestDispatchSema_BoundedConcurrency(t *testing.T) {
	// Verify that at most capLimit goroutines hold the semaphore simultaneously.
	const capLimit = 3
	ds := newDispatchSemaphore(capLimit)

	var mu sync.Mutex
	maxSeen := 0
	current := 0

	var wg sync.WaitGroup
	wg.Add(20)
	for i := 0; i < 20; i++ {
		go func() {
			defer wg.Done()
			rel := ds.WaitAndAcquire()
			mu.Lock()
			current++
			if current > maxSeen {
				maxSeen = current
			}
			if current > capLimit {
				t.Errorf("concurrency %d exceeded cap %d", current, capLimit)
			}
			mu.Unlock()
			// Yield briefly to increase chance of overlap.
			for j := 0; j < 1000; j++ {
				_ = j
			}
			mu.Lock()
			current--
			mu.Unlock()
			rel()
		}()
	}
	wg.Wait()

	if maxSeen < 1 {
		t.Fatal("expected at least 1 concurrent holder")
	}
	if maxSeen > capLimit {
		t.Fatalf("max concurrent holders %d exceeded cap %d", maxSeen, capLimit)
	}
}
