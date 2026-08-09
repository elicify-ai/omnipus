// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// G1 regression coverage: RACE SAFETY.
//
// turnState.recordToolCallProgress is called synchronously from a
// provider's SSE read goroutine on every argument delta of a live stream —
// a high-frequency write path, on a DIFFERENT goroutine than whatever later
// calls turnState.ToolCallProgress() (a delegate action=status poll,
// potentially from another turn's/agent's goroutine reaching in via
// AgentLoop.ProgressForSession). Run with -race to prove the atomics-based
// atomicToolCallProgress type (turn.go) has no data race under concurrent
// write/read.

package agent

import (
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
)

// TestToolCallProgress_RecordAndRead_RaceFree drives many concurrent writers
// (simulating the provider's SSE loop emitting a rapid sequence of argument
// deltas) against many concurrent readers (simulating overlapping status
// polls) on a single turnState, and asserts `go test -race` finds nothing —
// the whole point of atomicToolCallProgress being atomics-based rather than
// guarded by ts.mu (see its doc comment in turn.go).
func TestToolCallProgress_RecordAndRead_RaceFree(t *testing.T) {
	ts := &turnState{}

	const writers = 8
	const readers = 8
	const deltasPerWriter = 200

	var wg sync.WaitGroup
	wg.Add(writers + readers)

	stop := make(chan struct{})

	for w := 0; w < writers; w++ {
		go func(w int) {
			defer wg.Done()
			for i := 1; i <= deltasPerWriter; i++ {
				ts.recordToolCallProgress(protocoltypes.ToolCallProgress{
					Index:          w,
					Name:           "web_serve",
					ArgsBytes:      i,
					TotalArgsBytes: i * writers,
				})
			}
		}(w)
	}

	for r := 0; r < readers; r++ {
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = ts.ToolCallProgress()
					time.Sleep(time.Microsecond)
				}
			}
		}()
	}

	// Give writers a bounded window to run (they are fast — 8*200 plain
	// atomic stores — and finish well within this), then signal readers to
	// stop and wait for the whole group. Readers loop on `stop` rather than
	// a fixed count so they keep contending with the writers for the ENTIRE
	// window, not just a fixed number of reads front-loaded before the
	// writers get going.
	time.Sleep(20 * time.Millisecond)
	close(stop)
	wg.Wait()

	// The final read must reflect SOME recorded delta (writers definitely
	// ran and definitely called recordToolCallProgress at least once each
	// before the 20ms window), never the zero/never-recorded state.
	final := ts.ToolCallProgress()
	if final.LastActivity.IsZero() {
		t.Fatal("expected at least one recorded delta to be observable after concurrent writers ran, " +
			"got the zero/never-recorded snapshot")
	}
	if final.Name != "web_serve" {
		t.Errorf("expected the recorded tool name to survive concurrent access, got %q", final.Name)
	}
	if final.ArgsBytes <= 0 {
		t.Errorf("expected a positive ArgsBytes after concurrent writers ran, got %d", final.ArgsBytes)
	}
}

// TestToolCallProgress_NilTurnState_NoPanic proves both the write and read
// side are nil-safe — recordToolCallProgress and ToolCallProgress must
// degrade to a no-op / zero value rather than panic on a nil *turnState, a
// defensive guard consistent with every other turnState method (see e.g.
// setLastProducedModel/auditUser's own nil checks).
func TestToolCallProgress_NilTurnState_NoPanic(t *testing.T) {
	var ts *turnState

	if got := ts.ToolCallProgress(); !got.LastActivity.IsZero() {
		t.Errorf("expected the zero value from a nil turnState, got %+v", got)
	}

	// Must not panic.
	ts.recordToolCallProgress(protocoltypes.ToolCallProgress{Name: "x", ArgsBytes: 1})
}
