package tools

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

// TestUnsanitizeToolNameConcurrentWithRegistration locks in the invariant that
// UnsanitizeToolName may not read r.tools without holding r.mu (issue #660).
//
// WHAT THIS TEST DOES AND DOES NOT PROVE
//
//   - Under `go test -race` it fails deterministically on the unfixed code:
//     the race detector reports the unsynchronised map read in
//     UnsanitizeToolName against the write in registerToolLocked. That is the
//     signal this test is built for.
//   - Without -race it relies on the Go runtime's own "concurrent map read and
//     map write" fatal, which is probabilistic and reproduces only with at
//     least two Ps (schedulable OS threads). The GOMAXPROCS guard below exists
//     for exactly that reason; at GOMAXPROCS=1 the interleave does not occur
//     and the test would silently pass on broken code.
//   - It does NOT reproduce the production failure described in #660 (a tool
//     dispatch racing an MCP reconcile that takes down the gateway). It
//     reproduces the same *underlying* unsynchronised access at the unit
//     level, which is what the fix addresses.
//
// The test also asserts all three resolution branches of UnsanitizeToolName
// keep working while registration churns, so a fix that adds a lock but
// changes resolution semantics fails here too.
func TestUnsanitizeToolNameConcurrentWithRegistration(t *testing.T) {
	// The runtime's concurrent-map-access detector needs >= 2 Ps to have a
	// realistic chance of interleaving the reader and the writer. Raise
	// GOMAXPROCS for the duration of this test and restore it afterwards.
	// GOMAXPROCS(n) returns the PREVIOUS value, so this one-liner sets 4 now
	// and defers a restore of whatever was in effect before.
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(4))

	r := NewToolRegistry()

	// Stable fixtures — registered once, never unregistered — so the three
	// resolution branches have deterministic expected answers even while
	// other names are being churned in and out concurrently.
	//
	//   branch 1: exact hit on the name as given (no dots involved)
	//   branch 2: every underscore replaced by a dot
	//   branch 3: only the FIRST underscore replaced by a dot
	r.Register(newMockTool("read_file", "branch 1 fixture: dotless name"))
	r.Register(newMockTool("browser.navigate", "branch 2 fixture"))
	r.Register(newMockTool("browser.navigate_url", "branch 3 fixture"))

	branches := []struct {
		branch int
		probe  string
		want   string
	}{
		// Branch 1: "read_file" is registered verbatim, so it returns before
		// any underscore rewriting is attempted.
		{1, "read_file", "read_file"},
		// Branch 2: "browser_navigate" is not registered; replacing all
		// underscores yields "browser.navigate", which is.
		{2, "browser_navigate", "browser.navigate"},
		// Branch 3: "browser_navigate_url" is not registered, and replacing
		// ALL underscores yields "browser.navigate.url", which is not either.
		// Only replacing the first underscore yields "browser.navigate_url".
		{3, "browser_navigate_url", "browser.navigate_url"},
	}

	// Sanity-check the fixtures on a quiescent registry before adding churn,
	// so a fixture mistake is reported as a fixture mistake rather than as a
	// mysterious concurrency failure.
	for _, b := range branches {
		if got := r.UnsanitizeToolName(b.probe); got != b.want {
			t.Fatalf("fixture check (branch %d): UnsanitizeToolName(%q) = %q, want %q",
				b.branch, b.probe, got, b.want)
		}
	}

	const duration = 300 * time.Millisecond
	deadline := time.Now().Add(duration)

	var wg sync.WaitGroup
	// errCh is buffered so a failing reader never blocks on a full channel
	// while the writer is still running.
	errCh := make(chan string, 64)

	// Reader: the dispatch-path caller. Hammers all three branches.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for time.Now().Before(deadline) {
			for _, b := range branches {
				got := r.UnsanitizeToolName(b.probe)
				if got != b.want {
					select {
					case errCh <- fmt.Sprintf(
						"branch %d: UnsanitizeToolName(%q) = %q, want %q",
						b.branch, b.probe, got, b.want):
					default:
					}
				}
			}
		}
	}()

	// Writer: the MCP-reconcile-shaped caller. Registers and unregisters a
	// rotating set of names, taking the write lock each time. The churned
	// names are deliberately disjoint from the three fixtures above.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; time.Now().Before(deadline); i++ {
			name := fmt.Sprintf("churn.tool_%d", i%16)
			r.Register(newMockTool(name, "churn fixture"))
			r.Unregister(name)
		}
	}()

	wg.Wait()
	close(errCh)

	for msg := range errCh {
		t.Error(msg)
	}
}
