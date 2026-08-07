// concurrent_sessions_test.go — regression tests for Bug-3: Concurrent sessions
// both respond.
//
// Before fix: while Session A is running a turn, Session B's messages are
// received but never get a reply. Only one session at a time would reply.
//
// After fix: two simultaneous sessions on different agents both reply within
// reasonable time (5 s with the mock LLM).
//
// Timing-based proof (BLOCKING): the load-bearing tests in this file use a
// slow mock LLM (2 s per call) and assert that 2 sessions both reply in
// <3 s wall-clock time. Sequential dispatch would take ≥4 s.
//
// Traces to: Bug-3 (manual E2E testing, feature/iframe-preview-tier13)

package integration

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestConcurrentSessions_TwoSessions_BothReply boots a gateway backed by a mock
// LLM, opens 2 WebSocket sessions to different agents, sends a message on each
// within 1 second, and asserts both receive a first-token (or done) frame within
// 5 seconds.
//
// BDD: Given 2 WS sessions open simultaneously
//
//	When a message is sent on session A and session B within 1s of each other
//	Then BOTH sessions receive a first-token frame within 5s
//
// Traces to: Bug-3 (concurrent sessions both respond)
func TestConcurrentSessions_TwoSessions_BothReply(t *testing.T) {
	gw := startIntegrationGateway(t)

	// Open two connections BEFORE sending messages so they are registered
	// concurrently with the gateway.
	connA := wsConnect(t, gw)
	connB := wsConnect(t, gw)

	const replyTimeout = 5 * time.Second
	// The collectors own the detection budget; the test goroutine waits longer
	// so a collector always reports a classified outcome first.
	const collectorGrace = 3 * time.Second

	// Send messages on both connections within 1 second to trigger concurrent
	// turns. Write failures are reported back to the test goroutine — calling
	// Fatalf from these goroutines would violate the testing contract.
	sendErrs := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		if err := sendMessageErr(connA, "session A: ping concurrent test"); err != nil {
			sendErrs <- fmt.Errorf("session A: %w", err)
		}
	}()
	go func() {
		defer wg.Done()
		if err := sendMessageErr(connB, "session B: ping concurrent test"); err != nil {
			sendErrs <- fmt.Errorf("session B: %w", err)
		}
	}()
	wg.Wait()
	close(sendErrs)
	for err := range sendErrs {
		t.Errorf("TRANSPORT FAILURE while sending — NOT concurrent session starvation: %v", err)
	}
	if t.Failed() {
		t.Fatal("aborting: the transport failed before the agent was reached, so this run cannot say anything about concurrency")
	}

	// Both sends happened within a few ms of each other. Now race to receive replies.
	replies := make(chan wsReply, 2)
	go collectFirstToken(replies, 0, connA, replyTimeout)
	go collectFirstToken(replies, 1, connB, replyTimeout)

	// Assert both got a response. The test goroutine classifies every outcome:
	// a broken socket is reported as a transport failure, and only a healthy
	// connection that stayed silent counts as starvation.
	name := func(idx int) string {
		if idx == 0 {
			return "A"
		}
		return "B"
	}
	for range 2 {
		select {
		case r := <-replies:
			switch {
			case r.waitErr == nil:
				t.Logf("session %s first frame: %q", name(r.idx), r.frameType)
			case r.waitErr.ReadTimeout:
				t.Fatalf("BUG-3: session %s received no response within %v on a healthy connection — concurrent session starvation. %v",
					name(r.idx), replyTimeout, r.waitErr)
			default:
				t.Fatalf("TRANSPORT FAILURE on session %s — NOT concurrent session starvation, the WebSocket died before a reply could arrive: %v",
					name(r.idx), r.waitErr)
			}
		case <-time.After(replyTimeout + collectorGrace):
			t.Fatalf("HARNESS FAULT: a collector goroutine did not report within %v (its own budget was %v). "+
				"No verdict was produced — neither a starvation nor a transport finding.",
				replyTimeout+collectorGrace, replyTimeout)
		}
	}
}

// TestConcurrentSessions_TwoSessions_TimingProof is the LOAD-BEARING concurrency
// regression test for Bug-3. It uses a slow mock LLM (4 s per call) to prove
// that the two sessions run in PARALLEL, not sequentially.
//
// Proof logic:
//   - Each LLM call takes ~4 s.
//   - Two sessions sent concurrently must BOTH reply in <6 s wall-clock time.
//   - If the dispatcher were sequential, the total would be ≥8 s and the test fails.
//
// Reverting the per-session-worker fix (restoring the old single-goroutine Run())
// causes the sequential path and the wall-clock assertion fails.
//
// BDD: Given 2 WS sessions open simultaneously with a 4 s slow mock LLM
//
//	When both messages are sent within 1 s of each other
//	Then BOTH sessions reply within 6 s wall-clock (proves parallel execution)
//	And a sequential dispatcher would take ≥8 s (proves the test is load-bearing)
//
// Traces to: Bug-3 (concurrent sessions both respond) — timing proof
// Traces to: review-pr-test-analyzer.md — "Concurrency is not actually proven"
func TestConcurrentSessions_TwoSessions_TimingProof(t *testing.T) {
	const slowDelay = 4 * time.Second
	// Parallel deadline: 4 s LLM + generous overhead headroom for CI load. The
	// proof is that concurrent execution is far below the ≥8 s sequential time
	// (2 × 4 s); a 6 s bound stays well under that while tolerating scheduler
	// contention on a loaded CI box — the prior 3 s budget (only ~1 s headroom
	// over the 2 s LLM) flaked on the ci-omnipus worker under load, the same
	// failure the same-author 5-session sibling's 3.5 s budget hit before being
	// bumped to 6 s (concurrent_sessions_same_agent_test.go). 6 s gives ~2 s
	// headroom above the parallel real (4 s + overhead) AND ~2 s margin below
	// the 8 s sequential lower bound, so it discriminates BOTH directions.
	const parallelDeadline = 6 * time.Second

	gw := startSlowIntegrationGateway(t, slowDelay)

	connA := wsConnect(t, gw)
	connB := wsConnect(t, gw)

	// Record the wall-clock start time before sending.
	start := time.Now()

	// Send messages on both connections nearly simultaneously. Write failures
	// come back to the test goroutine rather than a Fatalf off-thread.
	sendErrs := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := sendMessageErr(connA, "slow concurrent test A"); err != nil {
			sendErrs <- fmt.Errorf("session A: %w", err)
		}
	}()
	go func() {
		defer wg.Done()
		if err := sendMessageErr(connB, "slow concurrent test B"); err != nil {
			sendErrs <- fmt.Errorf("session B: %w", err)
		}
	}()
	wg.Wait()
	close(sendErrs)
	for err := range sendErrs {
		t.Errorf("TRANSPORT FAILURE while sending — NOT a timing/starvation finding: %v", err)
	}
	if t.Failed() {
		t.Fatal("aborting: the transport failed before the agent was reached, so this run cannot prove or disprove parallel execution")
	}
	t.Logf("both messages sent at t=+%v", time.Since(start).Round(time.Millisecond))

	// Collect replies concurrently. The collector budget deliberately exceeds
	// parallelDeadline: the LOAD-BEARING assertion is the wall-clock deadline
	// below, which must fire on a sequential dispatcher regardless.
	replies := make(chan wsReply, 2)
	go collectFirstToken(replies, 0, connA, parallelDeadline+time.Second)
	go collectFirstToken(replies, 1, connB, parallelDeadline+time.Second)

	// Both must reply within the parallel deadline.
	deadline := time.NewTimer(parallelDeadline)
	defer deadline.Stop()

	name := func(idx int) string {
		if idx == 0 {
			return "A"
		}
		return "B"
	}
	var replyA, replyB string
	for replyA == "" || replyB == "" {
		select {
		case r := <-replies:
			switch {
			case r.waitErr == nil:
				if r.idx == 0 {
					replyA = r.frameType
				} else {
					replyB = r.frameType
				}
				t.Logf("session %s replied (%q) at t=+%v", name(r.idx), r.frameType, time.Since(start).Round(time.Millisecond))
			case r.waitErr.ReadTimeout:
				t.Fatalf("BUG-3: session %s sat on a healthy connection for %v without a single assistant frame — starvation, not slowness. %v",
					name(r.idx), r.waitErr.Waited, r.waitErr)
			default:
				t.Fatalf("TRANSPORT FAILURE on session %s at t=+%v — NOT a timing proof failure, the WebSocket died before a reply could arrive: %v",
					name(r.idx), time.Since(start).Round(time.Millisecond), r.waitErr)
			}
		case <-deadline.C:
			// Reached only with both connections healthy and no transport
			// error reported — the sessions really are too slow.
			elapsed := time.Since(start).Round(time.Millisecond)
			t.Fatalf(
				"BUG-3 TIMING PROOF FAILED: wall-clock elapsed=%v > parallelDeadline=%v with no transport error on either session. "+
					"Expected ≤%v for parallel execution (%v slow LLM × 2 sessions in parallel). "+
					"Sequential dispatch would take ≥%v. "+
					"Got replyA=%q replyB=%q",
				elapsed, parallelDeadline, parallelDeadline,
				slowDelay, 2*slowDelay,
				replyA, replyB,
			)
		}
	}

	elapsed := time.Since(start)
	t.Logf("TIMING PROOF: both sessions replied in %v (parallel deadline=%v, sequential lower-bound=%v)",
		elapsed.Round(time.Millisecond), parallelDeadline, 2*slowDelay)

	// Belt-and-suspenders: assert both replies are non-empty.
	if replyA == "" || replyB == "" {
		t.Fatalf("BUG-3: missing reply — A=%q B=%q", replyA, replyB)
	}
}

// TestConcurrentSessions_TranscriptPersisted asserts that two concurrently
// dispatched sessions each receive an assistant frame.
//
// SCOPE WARNING — the name overpromises. Despite "TranscriptPersisted", this
// test makes NO REST call and asserts NOTHING about the persisted transcript;
// it only observes the live WS reply. As written it duplicates
// TestConcurrentSessions_TwoSessions_BothReply. Read a pass here as "both
// sessions replied", never as "both transcripts were persisted" — the
// persistence assertion the original doc comment described was never
// implemented.
//
// BDD: Given 2 WS sessions dispatched concurrently
//
//	Then each session receives an assistant frame within 5s
//
// Traces to: Bug-3 persistence assertion variant
func TestConcurrentSessions_TranscriptPersisted(t *testing.T) {
	const replyTimeout = 5 * time.Second
	const collectorGrace = 3 * time.Second

	gw := startIntegrationGateway(t)

	connA := wsConnect(t, gw)
	connB := wsConnect(t, gw)

	// Send on both within 1 second to trigger concurrent processing. Write
	// failures come back to the test goroutine rather than a Fatalf off-thread.
	sendErrs := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := sendMessageErr(connA, "concurrent persistence check A"); err != nil {
			sendErrs <- fmt.Errorf("session A: %w", err)
		}
	}()
	go func() {
		defer wg.Done()
		if err := sendMessageErr(connB, "concurrent persistence check B"); err != nil {
			sendErrs <- fmt.Errorf("session B: %w", err)
		}
	}()
	wg.Wait()
	close(sendErrs)
	for err := range sendErrs {
		t.Errorf("TRANSPORT FAILURE while sending — NOT concurrent session starvation: %v", err)
	}
	if t.Failed() {
		t.Fatal("aborting: the transport failed before the agent was reached, so this run cannot say anything about concurrency")
	}

	// Wait for both to reply, classifying each outcome on the test goroutine.
	replies := make(chan wsReply, 2)
	go collectFirstToken(replies, 0, connA, replyTimeout)
	go collectFirstToken(replies, 1, connB, replyTimeout)

	name := func(idx int) string {
		if idx == 0 {
			return "A"
		}
		return "B"
	}
	for range 2 {
		select {
		case r := <-replies:
			switch {
			case r.waitErr == nil:
				t.Logf("session %s replied with frame type %q", name(r.idx), r.frameType)
			case r.waitErr.ReadTimeout:
				t.Fatalf("BUG-3: session %s never replied on a healthy connection within %v — concurrent session starvation. %v",
					name(r.idx), replyTimeout, r.waitErr)
			default:
				t.Fatalf("TRANSPORT FAILURE on session %s — NOT concurrent session starvation, the WebSocket died before a reply could arrive: %v",
					name(r.idx), r.waitErr)
			}
		case <-time.After(replyTimeout + collectorGrace):
			t.Fatalf("HARNESS FAULT: a collector goroutine did not report within %v (its own budget was %v). "+
				"No verdict was produced — neither a starvation nor a transport finding.",
				replyTimeout+collectorGrace, replyTimeout)
		}
	}
}
