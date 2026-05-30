// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"fmt"
	"sync"
	"testing"
)

// TestAdmissionController_DefaultSoftCap verifies that a zero-arg controller
// picks up a positive default soft cap.
func TestAdmissionController_DefaultSoftCap(t *testing.T) {
	a := newAdmissionController(0)
	if a.SoftCap() <= 0 {
		t.Fatalf("SoftCap() = %d, want > 0", a.SoftCap())
	}
}

// TestAdmissionController_ExplicitSoftCap verifies the caller-supplied cap is used.
func TestAdmissionController_ExplicitSoftCap(t *testing.T) {
	a := newAdmissionController(7)
	if a.SoftCap() != 7 {
		t.Fatalf("SoftCap() = %d, want 7", a.SoftCap())
	}
}

// TestAdmissionController_TryAdmit_BelowCap verifies that TryAdmit admits new
// scopes while the active scope count is below the soft cap.
//
// BDD: Given a controller with cap=3 and no active scopes,
//
//	When two distinct scopes are admitted,
//	Then both succeed and a third new scope is also admitted.
//
// Traces to: pkg/agent/admission.go — TryAdmit below-cap path
func TestAdmissionController_TryAdmit_BelowCap(t *testing.T) {
	a := newAdmissionController(3)

	ok, release1 := a.TryAdmit("scope-1")
	if !ok {
		t.Fatal("TryAdmit scope-1 = false on empty controller, want true")
	}
	defer release1()

	if a.ActiveScopes() != 1 {
		t.Fatalf("ActiveScopes() = %d, want 1 after first admit", a.ActiveScopes())
	}

	ok, release2 := a.TryAdmit("scope-2")
	if !ok {
		t.Fatalf("TryAdmit scope-2 = false at 1/%d, want true", a.SoftCap())
	}
	defer release2()

	if a.ActiveScopes() != 2 {
		t.Fatalf("ActiveScopes() = %d, want 2 after second admit", a.ActiveScopes())
	}

	ok, release3 := a.TryAdmit("scope-3")
	if !ok {
		t.Fatalf("TryAdmit scope-3 = false at 2/%d, want true", a.SoftCap())
	}
	defer release3()
}

// TestAdmissionController_TryAdmit_AtCap verifies that TryAdmit returns false
// when the active scope count equals the soft cap.
//
// BDD: Given a controller with cap=2 and 2 active scopes,
//
//	When a third distinct scope attempts admission,
//	Then TryAdmit returns false.
//
// Traces to: pkg/agent/admission.go — TryAdmit at-cap path
func TestAdmissionController_TryAdmit_AtCap(t *testing.T) {
	a := newAdmissionController(2)

	_, release1 := a.TryAdmit("scope-A")
	defer release1()
	_, release2 := a.TryAdmit("scope-B")
	defer release2()

	ok, rel := a.TryAdmit("scope-C")
	if ok {
		rel()
		t.Fatal("TryAdmit scope-C = true at cap=2, want false")
	}
	if rel != nil {
		t.Error("TryAdmit returned non-nil release when admission was denied")
	}
}

// TestAdmissionController_Release_FreesSlot verifies that calling the release
// func returned by TryAdmit decrements ActiveScopes and allows a new admission.
//
// BDD: Given a controller with cap=1 with scope-X admitted,
//
//	When release is called,
//	Then ActiveScopes drops to 0 and a new scope can be admitted.
//
// Traces to: pkg/agent/admission.go — TryAdmit release path
func TestAdmissionController_Release_FreesSlot(t *testing.T) {
	a := newAdmissionController(1)

	ok, release := a.TryAdmit("scope-X")
	if !ok {
		t.Fatal("first TryAdmit failed on empty controller")
	}
	if a.ActiveScopes() != 1 {
		t.Fatalf("ActiveScopes() = %d after admit, want 1", a.ActiveScopes())
	}

	// Cap is full — new scope must be denied.
	ok2, _ := a.TryAdmit("scope-Y")
	if ok2 {
		t.Fatal("TryAdmit scope-Y = true at cap=1, want false")
	}

	// Release scope-X.
	release()

	if a.ActiveScopes() != 0 {
		t.Fatalf("ActiveScopes() = %d after release, want 0", a.ActiveScopes())
	}

	// Now scope-Y should be admitted.
	ok3, release3 := a.TryAdmit("scope-Y")
	if !ok3 {
		t.Fatal("TryAdmit scope-Y = false after release, want true")
	}
	release3()
}

// TestAdmissionController_ActiveScopes verifies the counter increments and
// decrements correctly across multiple admit/release cycles.
//
// BDD: Given a controller with cap=10,
//
//	When two scopes are admitted and one is released,
//	Then ActiveScopes reflects the net count.
//
// Traces to: pkg/agent/admission.go — ActiveScopes counter
func TestAdmissionController_ActiveScopes(t *testing.T) {
	a := newAdmissionController(10)

	if a.ActiveScopes() != 0 {
		t.Fatalf("ActiveScopes() = %d, want 0", a.ActiveScopes())
	}

	_, release1 := a.TryAdmit("sc-1")
	_, release2 := a.TryAdmit("sc-2")

	if a.ActiveScopes() != 2 {
		t.Fatalf("ActiveScopes() = %d, want 2", a.ActiveScopes())
	}

	release1()

	if a.ActiveScopes() != 1 {
		t.Fatalf("ActiveScopes() = %d after one release, want 1", a.ActiveScopes())
	}

	release2()

	if a.ActiveScopes() != 0 {
		t.Fatalf("ActiveScopes() = %d after all releases, want 0", a.ActiveScopes())
	}
}

// TestAdmissionController_TryAdmit_NoOvercommit verifies that TryAdmit is
// atomic: with 100 goroutines all competing at softCap=5, at most 5 succeed.
// This is the TOCTOU regression test — a non-atomic check+increment would
// allow more than 5 goroutines to pass the check concurrently.
//
// BDD: Given a controller with cap=5 and 100 goroutines each attempting a
//
//	unique scope simultaneously,
//	When all goroutines are released at once,
//	Then at most 5 are admitted (no overcommit).
//
// Traces to: pkg/agent/admission.go — TryAdmit atomic claim under contention
func TestAdmissionController_TryAdmit_NoOvercommit(t *testing.T) {
	const numGoroutines = 100
	const softCap = 5

	a := newAdmissionController(softCap)

	var (
		wg        sync.WaitGroup
		startGate = make(chan struct{})
		admitMu   sync.Mutex
		admitted  int
		releases  []func()
	)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			scope := fmt.Sprintf("scope-%d", i)
			<-startGate
			ok, release := a.TryAdmit(scope)
			if ok {
				admitMu.Lock()
				admitted++
				releases = append(releases, release)
				admitMu.Unlock()
			}
		}()
	}

	close(startGate) // unleash all goroutines simultaneously
	wg.Wait()

	if admitted > softCap {
		t.Fatalf("TryAdmit admitted %d goroutines, want at most %d (TOCTOU overcommit detected)",
			admitted, softCap)
	}
	if admitted == 0 {
		t.Fatal("TryAdmit admitted 0 goroutines — expected at least 1 to succeed")
	}

	// Release all admitted scopes; counter must return to 0.
	for _, rel := range releases {
		rel()
	}
	if got := a.ActiveScopes(); got != 0 {
		t.Fatalf("ActiveScopes() = %d after all releases, want 0", got)
	}
}

// TestAdmissionController_ExistingScope_AlwaysAdmitted verifies that a
// follow-up message to an already-active scope is always admitted without
// consuming a new slot (same-session re-admission).
//
// BDD: Given a controller with cap=1 with scope-M admitted,
//
//	When scope-M attempts admission again,
//	Then TryAdmit returns true and ActiveScopes does not increase.
//
// Traces to: pkg/agent/admission.go — TryAdmit existing-scope path
func TestAdmissionController_ExistingScope_AlwaysAdmitted(t *testing.T) {
	a := newAdmissionController(1)

	_, release := a.TryAdmit("scope-M")
	defer release()

	if a.ActiveScopes() != 1 {
		t.Fatalf("ActiveScopes() = %d, want 1", a.ActiveScopes())
	}

	// Same scope admitted again — should succeed without increasing count.
	ok2, release2 := a.TryAdmit("scope-M")
	if !ok2 {
		t.Fatal("TryAdmit of existing scope = false, want true (follow-up turn)")
	}
	// release2 is a no-op (existing scope) — call it to avoid leaks.
	release2()

	// ActiveScopes must still be 1 (not 2).
	if a.ActiveScopes() != 1 {
		t.Fatalf("ActiveScopes() = %d after re-admit of existing scope, want 1", a.ActiveScopes())
	}

	// A genuinely new scope should still be denied (cap=1 is full).
	ok3, _ := a.TryAdmit("scope-N")
	if ok3 {
		t.Fatal("TryAdmit new scope = true at cap=1, want false")
	}
}
