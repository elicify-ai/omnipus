package webrtc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestWaitForGatherOrProceed_TimeoutFallsBackToPartialAnswer verifies that an
// elapsed ICE-gathering timeout falls back to a partial answer instead of
// failing the negotiation, matching the sibling media legs
// (ingest.go::handleIngestOfferOnce, viewer_candidate.go).
func TestWaitForGatherOrProceed_TimeoutFallsBackToPartialAnswer(t *testing.T) {
	// No deadline on either context, mirroring production: Answer() does not
	// cap its own ctx at gatherTimeout (see Answer()'s doc comment), so only
	// the timeout parameter below can end a slow gather.
	ctx := context.Background()
	peerCtx := context.Background()
	gathered := make(chan struct{}) // deliberately never closed: gathering never completes
	var logged []string
	logf := func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) }

	start := time.Now()
	err := waitForGatherOrProceed(ctx, peerCtx, gathered, 20*time.Millisecond, "[input-test]", logf)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("waitForGatherOrProceed returned an error on a merely slow gather: %v — "+
			"this is the exact defect Lane L5b diagnosed: an incomplete ICE gather must proceed "+
			"with a partial answer, not fail the whole negotiation, matching ingest.go and "+
			"viewer_candidate.go", err)
	}
	if elapsed < 20*time.Millisecond {
		t.Fatalf("returned before the gather timeout elapsed (%s) — did not actually wait", elapsed)
	}
	found := false
	for _, line := range logged {
		if strings.Contains(line, "sending partial answer") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no log line reported the partial-answer fallback; logged=%v", logged)
	}
}

// TestWaitForGatherOrProceed_GatheredCompletesFirst is the healthy-path
// regression guard: when gathering finishes well inside the budget, the
// function must return immediately (not wait out the timeout) and report
// success, not a partial answer.
func TestWaitForGatherOrProceed_GatheredCompletesFirst(t *testing.T) {
	ctx := context.Background()
	peerCtx := context.Background()
	gathered := make(chan struct{})
	close(gathered)
	var logged []string
	logf := func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) }

	start := time.Now()
	err := waitForGatherOrProceed(ctx, peerCtx, gathered, 200*time.Millisecond, "[input-test]", logf)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error on a completed gather: %v", err)
	}
	if elapsed >= 200*time.Millisecond {
		t.Fatalf("waited out the full timeout even though gathering had already completed: %s", elapsed)
	}
	for _, line := range logged {
		if strings.Contains(line, "partial answer") {
			t.Fatalf("logged a partial-answer fallback on a gather that completed cleanly: %q", line)
		}
	}
}

// TestWaitForGatherOrProceed_CallerCancelStillFails verifies that caller
// cancellation (WS attachment torn down, 30s overall budget from
// browser_dedicated_input.go elapsing, etc.) is a real failure, not a merely
// slow gather, and must still return an error rather than proceed.
func TestWaitForGatherOrProceed_CallerCancelStillFails(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	peerCtx := context.Background()
	gathered := make(chan struct{}) // never closes
	logf := func(string, ...any) {}

	err := waitForGatherOrProceed(ctx, peerCtx, gathered, time.Hour, "[input-test]", logf)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("caller cancellation must still fail negotiation, got err=%v", err)
	}
}

// TestWaitForGatherOrProceed_PeerCloseStillFails mirrors the caller-cancel
// guard above for the peer's own lifetime ending mid-gather.
func TestWaitForGatherOrProceed_PeerCloseStillFails(t *testing.T) {
	ctx := context.Background()
	peerCtx, cancel := context.WithCancel(context.Background())
	cancel()
	gathered := make(chan struct{}) // never closes
	logf := func(string, ...any) {}

	err := waitForGatherOrProceed(ctx, peerCtx, gathered, time.Hour, "[input-test]", logf)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("peer close must still fail negotiation, got err=%v", err)
	}
}
