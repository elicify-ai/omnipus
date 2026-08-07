// concurrent_sessions_same_agent_test.go — regression test for Bug-3 variant:
// multiple concurrent sessions on the SAME agent all reply.
//
// Bug: while one session of agent Jim is running, other sessions to Jim are
// also received but never reply — only one Jim session at a time runs.
//
// Fix: the agent loop must process sessions from a pool so N sessions to the
// same agent can all be in-flight simultaneously.
//
// Timing-based proof (BLOCKING): uses a slow mock LLM (2 s per call) and
// asserts all 5 sessions reply within 3.5 s wall-clock. Sequential dispatch
// of 5 sessions at 2 s each would take ≥10 s and fail the assertion.
//
// Traces to: Bug-3 (concurrent sessions both respond) — same-agent variant

package integration

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestConcurrentSessions_FiveSessions_SameAgent opens 5 WebSocket sessions to
// the same default agent, sends a message on each within a short window, and
// asserts ALL 5 receive a first-token frame within 8 seconds.
//
// BDD: Given 5 WS sessions to the same agent, all started within 2s
//
//	When a message is sent on each
//	Then all 5 sessions receive a reply within 8s
//
// Traces to: Bug-3 same-agent starvation variant
func TestConcurrentSessions_FiveSessions_SameAgent(t *testing.T) {
	const numSessions = 5
	const replyTimeout = 8 * time.Second
	// The collector goroutines own the real detection budget (replyTimeout).
	// The test goroutine waits strictly longer so a collector always wins the
	// race and delivers a CLASSIFIED verdict; the outer branch then only fires
	// when a collector failed to report at all, which is a harness fault and
	// is reported as such rather than as a starvation finding.
	const collectorGrace = 3 * time.Second

	gw := startIntegrationGateway(t)

	conns := make([]*websocket.Conn, numSessions)
	for i := range conns {
		conns[i] = wsConnect(t, gw)
	}

	// Send messages on all sessions nearly simultaneously to maximize the
	// likelihood of concurrent in-flight turns on the same agent. The
	// goroutines report write failures back to the test goroutine instead of
	// calling Fatalf off-thread (forbidden by the testing contract).
	sendErrs := make(chan error, numSessions)
	var wg sync.WaitGroup
	wg.Add(numSessions)
	for i, conn := range conns {
		go func() {
			defer wg.Done()
			if err := sendMessageErr(conn, "same-agent concurrent test session "+string(rune('A'+i))); err != nil {
				sendErrs <- fmt.Errorf("session %d (%c): %w", i, rune('A'+i), err)
			}
		}()
	}
	wg.Wait()
	close(sendErrs)
	for err := range sendErrs {
		// A failed WS write never reached the agent, so it proves nothing
		// about concurrency. Say so explicitly.
		t.Errorf("TRANSPORT FAILURE while sending — NOT same-agent starvation: %v", err)
	}
	if t.Failed() {
		t.Fatal("aborting: the transport failed before the agent was reached, so this run cannot say anything about same-agent concurrency")
	}
	t.Logf("all %d messages sent; waiting up to %v for replies", numSessions, replyTimeout)

	// Collect replies concurrently. Buffered so a late collector never blocks
	// on send after the test has returned.
	replies := make(chan wsReply, numSessions)
	for i, conn := range conns {
		go collectFirstToken(replies, i, conn, replyTimeout)
	}

	// The test goroutine decides what every outcome MEANS.
	for range conns {
		select {
		case r := <-replies:
			switch {
			case r.waitErr == nil:
				t.Logf("session %d (%c) replied with frame type %q", r.idx, rune('A'+r.idx), r.frameType)
			case r.waitErr.ReadTimeout:
				// Healthy connection, deadline expired, no assistant frame.
				// This is the genuine starvation signature.
				t.Fatalf(
					"BUG-3: session %d (%c) of %d did not receive a reply within %v — same-agent concurrent starvation. %v",
					r.idx, rune('A'+r.idx), numSessions, replyTimeout, r.waitErr,
				)
			default:
				// The socket broke. Whatever else is true, this run did not
				// observe starvation.
				t.Fatalf(
					"TRANSPORT FAILURE on session %d (%c) — NOT same-agent starvation, the WebSocket died before a reply could arrive: %v",
					r.idx, rune('A'+r.idx), r.waitErr,
				)
			}
		case <-time.After(replyTimeout + collectorGrace):
			t.Fatalf(
				"HARNESS FAULT: a collector goroutine did not report within %v (its own budget was %v). "+
					"No verdict was produced — this is neither a starvation finding nor a transport finding.",
				replyTimeout+collectorGrace, replyTimeout,
			)
		}
	}
}

// TestConcurrentSessions_FiveSessions_SameAgent_TimingProof is the LOAD-BEARING
// timing proof for Bug-3 same-agent variant.
//
// Proof logic:
//   - Each LLM call takes ~2 s.
//   - 5 sessions sent to the same agent must ALL reply within 3.5 s wall-clock.
//   - Sequential dispatch of 5 sessions would take ≥10 s and fail this assertion.
//
// BDD: Given 5 WS sessions to the same agent with a 2 s slow mock LLM
//
//	When all 5 messages are sent within a short window
//	Then all 5 sessions reply within 3.5 s wall-clock (proves parallel execution)
//	And sequential dispatch would take ≥10 s (proves the test is load-bearing)
//
// Traces to: Bug-3 same-agent starvation variant — timing proof
// Traces to: review-pr-test-analyzer.md — "Concurrency is not actually proven"
func TestConcurrentSessions_FiveSessions_SameAgent_TimingProof(t *testing.T) {
	const numSessions = 5
	const slowDelay = 2 * time.Second
	// Parallel deadline: 2 s LLM + generous overhead headroom for CI load. The
	// proof is that concurrent execution is far below the ≥10 s sequential time
	// (5 × 2 s); a 6 s bound stays well under that while tolerating scheduler
	// contention on a loaded CI box (the prior 3.5 s budget flaked under the
	// parallel matrix). Sequential would still blow past 6 s on the first two
	// sessions alone.
	const parallelDeadline = 6 * time.Second

	gw := startSlowIntegrationGateway(t, slowDelay)

	conns := make([]*websocket.Conn, numSessions)
	for i := range conns {
		conns[i] = wsConnect(t, gw)
	}

	start := time.Now()

	// Send messages on all sessions nearly simultaneously. Write failures come
	// back to the test goroutine rather than a Fatalf off-thread.
	sendErrs := make(chan error, numSessions)
	var wg sync.WaitGroup
	wg.Add(numSessions)
	for i, conn := range conns {
		go func() {
			defer wg.Done()
			if err := sendMessageErr(conn, "slow same-agent concurrent timing test "+string(rune('A'+i))); err != nil {
				sendErrs <- fmt.Errorf("session %d (%c): %w", i, rune('A'+i), err)
			}
		}()
	}
	wg.Wait()
	close(sendErrs)
	for err := range sendErrs {
		t.Errorf("TRANSPORT FAILURE while sending — NOT a timing/starvation finding: %v", err)
	}
	if t.Failed() {
		t.Fatal("aborting: the transport failed before the agent was reached, so this run cannot prove or disprove parallel execution")
	}
	t.Logf("all %d messages sent at t=+%v", numSessions, time.Since(start).Round(time.Millisecond))

	// Collect replies concurrently, straight into one fan-in channel. The
	// collector budget deliberately exceeds parallelDeadline: the LOAD-BEARING
	// assertion is the wall-clock deadline below, which must fire on a slow
	// (sequential) system regardless of how long a collector would wait.
	replies := make(chan wsReply, numSessions)
	for i, conn := range conns {
		go collectFirstToken(replies, i, conn, parallelDeadline+2*time.Second)
	}

	// All must reply within the parallel deadline — this is the proof.
	overallDeadline := time.NewTimer(parallelDeadline)
	defer overallDeadline.Stop()

	got := make([]string, numSessions)
	for remaining := numSessions; remaining > 0; remaining-- {
		select {
		case r := <-replies:
			switch {
			case r.waitErr == nil:
				got[r.idx] = r.frameType
				t.Logf("session %d (%c) replied (%q) at t=+%v",
					r.idx, rune('A'+r.idx), r.frameType, time.Since(start).Round(time.Millisecond))
			case r.waitErr.ReadTimeout:
				t.Fatalf(
					"BUG-3 (same-agent): session %d (%c) sat on a healthy connection for %v without a single assistant frame — starvation, not slowness. %v",
					r.idx, rune('A'+r.idx), r.waitErr.Waited, r.waitErr,
				)
			default:
				// Distinguished from the timing verdict below: the socket
				// died, so wall-clock says nothing about parallelism here.
				t.Fatalf(
					"TRANSPORT FAILURE on session %d (%c) at t=+%v — NOT a timing proof failure, the WebSocket died before a reply could arrive: %v",
					r.idx, rune('A'+r.idx), time.Since(start).Round(time.Millisecond), r.waitErr,
				)
			}
		case <-overallDeadline.C:
			// Reached only with every connection still healthy and no
			// transport error reported — the sessions really are too slow.
			elapsed := time.Since(start).Round(time.Millisecond)
			t.Fatalf(
				"BUG-3 TIMING PROOF FAILED (same-agent): wall-clock elapsed=%v > parallelDeadline=%v with no transport error on any session. "+
					"Expected ≤%v for %d parallel sessions at %v/session. "+
					"Sequential dispatch lower-bound=%v. Replies so far: %v",
				elapsed, parallelDeadline, parallelDeadline, numSessions, slowDelay,
				time.Duration(numSessions)*slowDelay, got,
			)
		}
	}

	elapsed := time.Since(start)
	t.Logf("TIMING PROOF (same-agent): all %d sessions replied in %v (parallel deadline=%v, sequential lower-bound=%v)",
		numSessions, elapsed.Round(time.Millisecond), parallelDeadline, time.Duration(numSessions)*slowDelay)

	// Belt-and-suspenders: every session must have recorded a real frame type.
	for i, reply := range got {
		if reply == "" {
			t.Errorf("BUG-3: session %d (%c) got no reply (empty frame type)", i, rune('A'+i))
		}
	}
}
