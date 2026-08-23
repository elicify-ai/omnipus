package webrtc

// In-package unit tests for forwardPLIThrottled's cross-viewer throttle
// (fix-wave finding 1). These hit the unexported throttle state directly so
// the timing assertions are fast and deterministic; the full Go<->Go wire
// path (a real viewer RTCP write reaching the ingest connection) is covered
// by session_test.go (external package).

import (
	"testing"
	"time"
)

func setPLIForwardInterval(t *testing.T, d time.Duration) {
	t.Helper()
	old := pliForwardMinInterval
	pliForwardMinInterval = d
	t.Cleanup(func() { pliForwardMinInterval = old })
}

func TestForwardPLIThrottled_FirstCallAlwaysForwards(t *testing.T) {
	setPLIForwardInterval(t, time.Hour) // never let a second call through
	s := NewSession(Config{}, nil, nil)
	t.Cleanup(func() { _ = s.Close() })

	// No ingest connection is set up -- sendPLI (called by
	// forwardPLIThrottled) simply no-ops with no ingest pc to write to; this
	// test is about the throttle BOOKKEEPING, not the RTCP send itself.
	before := s.lastPLIForwardAt
	s.forwardPLIThrottled("[test]")
	s.pliForwardMu.Lock()
	after := s.lastPLIForwardAt
	s.pliForwardMu.Unlock()

	if !after.After(before) {
		t.Fatalf("forwardPLIThrottled did not record a forward on the first call: before=%v after=%v", before, after)
	}
}

func TestForwardPLIThrottled_SuppressesWithinInterval(t *testing.T) {
	setPLIForwardInterval(t, time.Hour)
	s := NewSession(Config{}, nil, nil)
	t.Cleanup(func() { _ = s.Close() })

	s.forwardPLIThrottled("[test]")
	s.pliForwardMu.Lock()
	first := s.lastPLIForwardAt
	s.pliForwardMu.Unlock()

	// A burst of further calls, all well within the (hour-long) throttle
	// window, must not move lastPLIForwardAt at all -- this is the "PLI
	// storm" scenario the throttle exists to prevent.
	for i := 0; i < 20; i++ {
		s.forwardPLIThrottled("[test]")
	}

	s.pliForwardMu.Lock()
	got := s.lastPLIForwardAt
	s.pliForwardMu.Unlock()

	if !got.Equal(first) {
		t.Fatalf("forwardPLIThrottled forwarded again within the throttle window: first=%v got=%v", first, got)
	}
}

func TestForwardPLIThrottled_ForwardsAgainAfterInterval(t *testing.T) {
	setPLIForwardInterval(t, 30*time.Millisecond)
	s := NewSession(Config{}, nil, nil)
	t.Cleanup(func() { _ = s.Close() })

	s.forwardPLIThrottled("[test]")
	s.pliForwardMu.Lock()
	first := s.lastPLIForwardAt
	s.pliForwardMu.Unlock()

	time.Sleep(60 * time.Millisecond)
	s.forwardPLIThrottled("[test]")

	s.pliForwardMu.Lock()
	second := s.lastPLIForwardAt
	s.pliForwardMu.Unlock()

	if !second.After(first) {
		t.Fatalf(
			"forwardPLIThrottled did not forward again once the interval elapsed: first=%v second=%v",
			first,
			second,
		)
	}
}
