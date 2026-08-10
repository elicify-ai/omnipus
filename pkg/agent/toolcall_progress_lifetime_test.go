package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
)

// TestToolCallProgress_ClearedSoTheSignalCannotLie pins the lifetime of the
// tool-argument progress record.
//
// The record answers exactly one question: "is this worker still producing a
// tool call right now?" It stopped being able to answer that honestly the
// moment it was written once and never reset — because a worker that streamed
// a 300-byte `bash` argument in two seconds and then BLOCKED for twenty
// minutes inside that command kept rendering:
//
//	generating tool call "bash" — 300 bytes, last update 3m41s ago
//
// It is not generating. It is stuck executing. And "generating" is the word
// this whole feature teaches an orchestrator to read as "leave it alone", so
// the lie suppresses exactly the intervention a hung worker needs. That is the
// original incident inverted — it killed healthy workers, this would spare a
// dead one.
func TestToolCallProgress_ClearedSoTheSignalCannotLie(t *testing.T) {
	ts := &turnState{}

	// Nothing recorded yet: the reader must report "no progress".
	if snap := ts.ToolCallProgress(); !snap.LastActivity.IsZero() {
		t.Fatalf("a fresh turnState must report no progress, got LastActivity=%v", snap.LastActivity)
	}

	ts.recordToolCallProgress(protocoltypes.ToolCallProgress{
		Index:          0,
		Name:           "bash",
		ArgsBytes:      300,
		TotalArgsBytes: 300,
	})

	snap := ts.ToolCallProgress()
	if snap.LastActivity.IsZero() {
		t.Fatal("progress was recorded but the reader reports none")
	}
	if snap.Name != "bash" || snap.ArgsBytes != 300 {
		t.Fatalf("recorded progress did not round-trip: %+v", snap)
	}

	// The round ends. From here on the worker may be executing the tool for
	// minutes; it is NOT generating arguments.
	ts.clearToolCallProgress()

	if snap := ts.ToolCallProgress(); !snap.LastActivity.IsZero() {
		t.Fatalf("progress survived the end of the LLM round — a worker blocked in tool "+
			"execution would keep reporting as 'generating' (LastActivity=%v, name=%q)",
			snap.LastActivity, snap.Name)
	}
}

// TestProgressForSession_IgnoresAFinishedTurn covers the second way the signal
// could lie: activeTurnStates legitimately holds COMPLETED turns.
//
// spawnSubTurn re-registers the child after runTurn returns, for a persist-retry
// window of roughly a second, and during that window the delegate task is still
// marked running — so the caller's own status guard does not exclude it. At the
// poll rate the incident exhibited (75 polls in 46s, ~one every 600ms) a window
// that size is hit routinely. Without a liveness check the reader would report
// a finished turn as actively generating.
func TestProgressForSession_IgnoresAFinishedTurn(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled // only al+cleanup used here
	defer cleanup()

	const key = "session_progress_lifetime"

	ts := &turnState{}
	ts.recordToolCallProgress(protocoltypes.ToolCallProgress{
		Name: "bash", ArgsBytes: 300, TotalArgsBytes: 300,
	})
	al.activeTurnStates.Store(key, ts)
	t.Cleanup(func() { al.activeTurnStates.Delete(key) })

	// While alive, the reader reports progress.
	if _, ok := al.ProgressForSession(key); !ok {
		t.Fatal("a live turn with recorded progress must be reported as progressing")
	}

	// Mark the turn finished, leaving it registered exactly as spawnSubTurn does.
	ts.markFinishedForProgressTest()

	if snap, ok := al.ProgressForSession(key); ok {
		t.Fatalf("a FINISHED turn still reported as generating (name=%q, age=%v) — "+
			"activeTurnStates holds completed turns during the persist-retry window, so "+
			"this reader must check liveness like every other consumer of that registry",
			snap.Name, snap.Age)
	}

	// An unknown key must never invent progress.
	if _, ok := al.ProgressForSession("no-such-session"); ok {
		t.Error("an unknown session key must not report progress")
	}
}

// markFinishedForProgressTest flips the same flag production's Finish() sets,
// so the test exercises the real IsAlive predicate rather than a stand-in.
func (ts *turnState) markFinishedForProgressTest() {
	ts.isFinished.Store(true)
}
