package tools

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// BS-6.13: five calls in a minute are allowed and the sixth is refused, on a
// fake clock. Concurrent callers share that cap. A refused call is not stored,
// so the window does not grow because of it.
func TestShowToUserRateLimiter_FakeClockBoundsAndRace(t *testing.T) {
	start := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	at := start
	lim := NewShowToUserRateLimiter(func() time.Time { return at })
	for call := 1; call <= 5; call++ {
		if !lim.Allow("chat") {
			t.Fatalf("call %d refused, want allowed (BS-6.13)", call)
		}
	}
	if lim.Allow("chat") {
		t.Fatal("call 6 allowed, want refused (BS-6.13)")
	}
	if !lim.Allow("other-chat") {
		t.Fatal("a second chat must have its own budget (BS-6.13)")
	}
	at = start.Add(time.Minute - time.Second)
	if lim.Allow("chat") {
		t.Fatal("call 59s after the five must still be refused")
	}
	at = start.Add(time.Minute)
	if !lim.Allow("chat") {
		t.Fatal("call at one minute must be allowed; the window is sliding")
	}

	frozen := start
	raceLim := NewShowToUserRateLimiter(func() time.Time { return frozen })
	var allowed atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if raceLim.Allow("race-chat") {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := allowed.Load(); got != 5 {
		t.Fatalf("concurrent allows = %d, want 5 (BS-6.13)", got)
	}
}
