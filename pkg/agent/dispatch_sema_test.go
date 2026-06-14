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
	// Verify that at most cap goroutines hold the semaphore simultaneously.
	const cap = 3
	ds := newDispatchSemaphore(cap)

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
			if current > cap {
				t.Errorf("concurrency %d exceeded cap %d", current, cap)
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
	if maxSeen > cap {
		t.Fatalf("max concurrent holders %d exceeded cap %d", maxSeen, cap)
	}
}
