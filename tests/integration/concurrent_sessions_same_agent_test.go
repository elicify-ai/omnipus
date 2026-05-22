//go:build !cgo

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

	gw := startIntegrationGateway(t)

	type sessionSlot struct {
		conn    *websocket.Conn
		replied chan string
	}

	slots := make([]sessionSlot, numSessions)
	for i := range slots {
		slots[i] = sessionSlot{
			conn:    wsConnect(t, gw),
			replied: make(chan string, 1),
		}
	}

	// Send messages on all sessions nearly simultaneously to maximize the
	// likelihood of concurrent in-flight turns on the same agent.
	var wg sync.WaitGroup
	wg.Add(numSessions)
	for i, s := range slots {
		i, s := i, s
		go func() {
			defer wg.Done()
			sendMessage(t, s.conn, "same-agent concurrent test session "+string(rune('A'+i)))
		}()
	}
	wg.Wait()
	t.Logf("all %d messages sent; waiting up to %v for replies", numSessions, replyTimeout)

	// Collect replies concurrently.
	for _, s := range slots {
		s := s
		go func() {
			frameType := waitForFirstToken(t, s.conn, replyTimeout)
			s.replied <- frameType
		}()
	}

	// Assert all received replies; a timeout means a session was starved.
	for i, s := range slots {
		select {
		case ft := <-s.replied:
			t.Logf("session %d (%c) replied with frame type %q", i, rune('A'+i), ft)
		case <-time.After(replyTimeout):
			t.Fatalf(
				"BUG-3: session %d (%c) of %d did not receive a reply within %v — same-agent concurrent starvation",
				i, rune('A'+i), numSessions, replyTimeout,
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
	// Parallel deadline: 2 s LLM + 1.5 s overhead. Sequential would be ≥10 s.
	const parallelDeadline = 3500 * time.Millisecond

	gw := startSlowIntegrationGateway(t, slowDelay)

	type sessionSlot struct {
		conn    *websocket.Conn
		replied chan string
	}

	slots := make([]sessionSlot, numSessions)
	for i := range slots {
		slots[i] = sessionSlot{
			conn:    wsConnect(t, gw),
			replied: make(chan string, 1),
		}
	}

	start := time.Now()

	// Send messages on all sessions nearly simultaneously.
	var wg sync.WaitGroup
	wg.Add(numSessions)
	for i, s := range slots {
		i, s := i, s
		go func() {
			defer wg.Done()
			sendMessage(t, s.conn, "slow same-agent concurrent timing test "+string(rune('A'+i)))
		}()
	}
	wg.Wait()
	t.Logf("all %d messages sent at t=+%v", numSessions, time.Since(start).Round(time.Millisecond))

	// Collect replies concurrently.
	for _, s := range slots {
		s := s
		go func() {
			ft := waitForFirstToken(t, s.conn, parallelDeadline+2*time.Second)
			s.replied <- ft
		}()
	}

	// All must reply within the parallel deadline.
	overallDeadline := time.NewTimer(parallelDeadline)
	defer overallDeadline.Stop()

	replies := make([]string, numSessions)
	remaining := numSessions
	repliedCh := make(chan struct {
		idx int
		ft  string
	}, numSessions)

	// Re-launch collectors to a single fan-in channel for deadline checking.
	for i, s := range slots {
		i, s := i, s
		go func() {
			select {
			case ft := <-s.replied:
				repliedCh <- struct {
					idx int
					ft  string
				}{i, ft}
			case <-time.After(parallelDeadline + 3*time.Second):
				repliedCh <- struct {
					idx int
					ft  string
				}{i, ""}
			}
		}()
	}

	for remaining > 0 {
		select {
		case r := <-repliedCh:
			replies[r.idx] = r.ft
			remaining--
			t.Logf("session %d (%c) replied (%q) at t=+%v",
				r.idx, rune('A'+r.idx), r.ft, time.Since(start).Round(time.Millisecond))
		case <-overallDeadline.C:
			elapsed := time.Since(start).Round(time.Millisecond)
			t.Fatalf(
				"BUG-3 TIMING PROOF FAILED (same-agent): wall-clock elapsed=%v > parallelDeadline=%v. "+
					"Expected ≤%v for %d parallel sessions at %v/session. "+
					"Sequential dispatch lower-bound=%v. Replies so far: %v",
				elapsed, parallelDeadline, parallelDeadline, numSessions, slowDelay,
				time.Duration(numSessions)*slowDelay, replies,
			)
		}
	}

	elapsed := time.Since(start)
	t.Logf("TIMING PROOF (same-agent): all %d sessions replied in %v (parallel deadline=%v, sequential lower-bound=%v)",
		numSessions, elapsed.Round(time.Millisecond), parallelDeadline, time.Duration(numSessions)*slowDelay)

	// Belt-and-suspenders: all replies must be non-empty.
	for i, reply := range replies {
		if reply == "" {
			t.Errorf("BUG-3: session %d (%c) got no reply (empty frame type)", i, rune('A'+i))
		}
	}
}
