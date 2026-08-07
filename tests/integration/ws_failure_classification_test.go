// ws_failure_classification_test.go — guards the diagnosis, not the feature.
//
// The concurrency tests in this package (concurrent_sessions_test.go,
// concurrent_sessions_same_agent_test.go) exist to detect session starvation.
// They used to misreport a broken WebSocket AS starvation: the collector
// goroutine called t.Fatalf, which runs runtime.Goexit on the CALLING
// goroutine, so the collector died before it could deliver a result; the
// waiting test then hit its own timeout branch and announced
// "same-agent concurrent starvation" for what was really a dead socket.
// Anyone reading that CI output went hunting for a concurrency bug that did
// not exist.
//
// The fix has two halves, and this file guards the half that is easy to
// regress silently:
//
//  1. Collectors report back to the test goroutine instead of calling Fatalf
//     off-thread (Go's testing contract forbids FailNow from any goroutine
//     other than the test's own).
//  2. The reported failure DISTINGUISHES a broken transport from a healthy
//     connection that stayed silent. Only the latter is starvation evidence.
//
// These tests assert (2) directly: the same helper, given a broken socket vs.
// a healthy-but-silent one, must produce two DIFFERENT classifications and two
// differently-worded messages. If someone collapses that distinction, the
// concurrency suite goes back to blaming the wrong subsystem — and these
// tests fail first.
//
// Traces to: Bug-3 (concurrent sessions) — failure-diagnosis regression guard

package integration

import (
	"strings"
	"testing"
	"time"
)

// TestWSFailureClassification_TransportVsTimeout is the differentiation test:
// two different failure inputs must yield two different verdicts.
//
// BDD: Given a WebSocket that has been closed underneath the reader
//
//	When waitForFirstTokenErr reads from it
//	Then it reports a TRANSPORT error (ReadTimeout=false), promptly
//
// BDD: Given a healthy WebSocket that is simply never sent a message
//
//	When waitForFirstTokenErr reads from it
//	Then it reports a READ TIMEOUT (ReadTimeout=true) after the full budget
func TestWSFailureClassification_TransportVsTimeout(t *testing.T) {
	gw := startIntegrationGateway(t)

	t.Run("broken_transport_is_not_starvation", func(t *testing.T) {
		conn := wsConnect(t, gw)

		// Force the exact fault that used to be misreported: kill the socket
		// before any reply can arrive.
		if err := conn.Close(); err != nil {
			t.Fatalf("closing the connection for fault injection: %v", err)
		}

		const budget = 5 * time.Second
		start := time.Now()
		ft, waitErr := waitForFirstTokenErr(conn, budget)
		elapsed := time.Since(start)

		if waitErr == nil {
			t.Fatalf("expected a failure from a closed connection, got frame type %q", ft)
		}
		if waitErr.ReadTimeout {
			t.Errorf("MISDIAGNOSIS: a closed socket was classified as a read timeout (the starvation signature). Got: %v", waitErr)
		}

		// The message a CI reader sees must name the transport and must not
		// blame starvation.
		msg := waitErr.Error()
		if !strings.Contains(msg, "TRANSPORT ERROR") {
			t.Errorf("transport failure message does not identify itself as a transport error: %q", msg)
		}
		if !strings.Contains(msg, "NOT evidence of starvation") {
			t.Errorf("transport failure message does not disclaim starvation: %q", msg)
		}

		// It must also fail FAST rather than burning the whole budget — the
		// old code sat through the full timeout before mislabelling it.
		if elapsed > budget/2 {
			t.Errorf("a broken transport should be detected promptly, took %v of a %v budget", elapsed, budget)
		}
		t.Logf("closed socket classified in %v as: %v", elapsed.Round(time.Millisecond), waitErr)
	})

	t.Run("healthy_but_silent_is_starvation_evidence", func(t *testing.T) {
		// Connect but never send a message, so the agent has nothing to reply
		// to. The connection stays healthy; the read deadline is what expires.
		conn := wsConnect(t, gw)

		const budget = 1500 * time.Millisecond
		start := time.Now()
		ft, waitErr := waitForFirstTokenErr(conn, budget)
		elapsed := time.Since(start)

		if waitErr == nil {
			t.Fatalf("expected a timeout with no message sent, got frame type %q", ft)
		}
		if !waitErr.ReadTimeout {
			t.Errorf("MISDIAGNOSIS: a healthy-but-silent connection was classified as a transport error, "+
				"which would suppress a genuine starvation finding. Got: %v", waitErr)
		}

		msg := waitErr.Error()
		if !strings.Contains(msg, "READ TIMEOUT") {
			t.Errorf("timeout message does not identify itself as a read timeout: %q", msg)
		}
		if !strings.Contains(msg, "connection stayed healthy") {
			t.Errorf("timeout message does not state the connection was healthy: %q", msg)
		}

		// It must actually have waited — a premature return would mean the
		// starvation budget is not being honoured.
		if elapsed < budget/2 {
			t.Errorf("read timeout returned after %v, well short of its %v budget", elapsed, budget)
		}
		t.Logf("silent healthy socket classified in %v as: %v", elapsed.Round(time.Millisecond), waitErr)
	})
}
