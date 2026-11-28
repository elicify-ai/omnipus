package programs

// Developer unit rows for the H50 result sink (FR-021 program.check_result):
// one event per published result, none for a superseded or stale check, and
// the outcome mapping connected / failed / timed_out.

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/credentials"
)

func TestCheckOutcome_Mapping(t *testing.T) {
	rows := []struct {
		res  checkResult
		want string
	}{
		{checkResult{state: StateConnected, reason: CheckReasonAccountNamed}, CheckOutcomeConnected},
		{checkResult{state: StateCheckFailed, reason: CheckReasonTimeout}, CheckOutcomeTimedOut},
		{checkResult{state: StateCheckFailed, reason: CheckReasonExitStatus}, CheckOutcomeFailed},
		{checkResult{state: StateCheckFailed, reason: CheckReasonNoAccountName}, CheckOutcomeFailed},
		{checkResult{state: StateSavedNotChecked, reason: CheckReasonProgramNotFound}, CheckOutcomeFailed},
	}
	for _, r := range rows {
		if got := checkOutcome(r.res); got != r.want {
			t.Errorf("checkOutcome(%+v) = %q, want %q", r.res, got, r.want)
		}
	}
}

func newResultSinkService(t *testing.T) (*Service, *[]CheckResultEvent) {
	t.Helper()
	// No store: the connection keeps the generation set by the test.
	noStore := func() (*credentials.Store, error) { return nil, errors.New("no store in this unit row") }
	s := NewService(nil, noStore, filepath.Join(t.TempDir(), "connections.json"))
	var got []CheckResultEvent
	s.SetResultSink(func(ev CheckResultEvent) { got = append(got, ev) })
	return s, &got
}

func TestFinish_PublishedResultWritesOneEvent(t *testing.T) {
	s, got := newResultSinkService(t)
	r := &reservation{id: 1, generation: 3, revision: 2, cancel: func() {}}
	s.conns["github"] = Connection{State: StateChecking, Generation: 3, CheckRevision: 2}
	s.checks["github"] = r
	s.finish("github", r, checkResult{state: StateCheckFailed, message: "The account check did not finish in 5 s.", reason: CheckReasonTimeout})
	if len(*got) != 1 {
		t.Fatalf("want exactly one result event, got %d: %+v", len(*got), *got)
	}
	want := CheckResultEvent{ProgramID: "github", Generation: 3, CheckRevision: 2, Outcome: CheckOutcomeTimedOut, Reason: CheckReasonTimeout}
	if (*got)[0] != want {
		t.Fatalf("event = %+v, want %+v", (*got)[0], want)
	}
	// A second finish of the same reservation is not current any more.
	s.finish("github", r, checkResult{state: StateConnected, reason: CheckReasonAccountNamed})
	if len(*got) != 1 {
		t.Fatalf("a finished reservation must not write a second event, got %d", len(*got))
	}
}

// ruling-check-delta.md item 5: "replaced" is never a check_result reason.
// Two ways a replaced check can end: through supersedeLocked (the real path:
// it writes the superseded row and the late result is discarded), and, as a
// guard, a replaced result arriving on a reservation that is still current.
// Neither may write a check_result event.
func TestFinish_ReplacedCheckNeverWritesACheckResult(t *testing.T) {
	s, got := newResultSinkService(t)
	var superseded []string
	s.superseded = func(id string, _ int64) { superseded = append(superseded, id) }

	// Real path: supersede, then the cancelled run finishes with replaced.
	r := &reservation{id: 1, generation: 3, revision: 2, cancel: func() {}}
	s.conns["github"] = Connection{State: StateChecking, Generation: 3, CheckRevision: 2}
	s.checks["github"] = r
	s.mu.Lock()
	s.supersedeLocked("github")
	s.mu.Unlock()
	s.finish("github", r, replacedCheckResult)
	if len(superseded) != 1 {
		t.Fatalf("want exactly one superseded row, got %v", superseded)
	}

	// Guard: a replaced result on a still-current reservation.
	r2 := &reservation{id: 2, generation: 4, revision: 1, cancel: func() {}}
	s.conns["gitlab"] = Connection{State: StateChecking, Generation: 4, CheckRevision: 1}
	s.checks["gitlab"] = r2
	s.finish("gitlab", r2, replacedCheckResult)

	for _, ev := range *got {
		if ev.Reason == CheckReasonReplaced {
			t.Fatalf("a check_result event carries the reason %q: %+v", CheckReasonReplaced, ev)
		}
	}
	if len(*got) != 0 {
		t.Fatalf("a replaced check must write no check_result event, got %+v", *got)
	}
}

func TestFinish_SupersededOrStaleCheckWritesNoResult(t *testing.T) {
	s, got := newResultSinkService(t)
	old := &reservation{id: 1, generation: 1, revision: 1, cancel: func() {}}
	cur := &reservation{id: 2, generation: 2, revision: 1, cancel: func() {}}
	s.conns["github"] = Connection{State: StateChecking, Generation: 2, CheckRevision: 1}
	s.checks["github"] = cur
	s.finish("github", old, checkResult{state: StateConnected, reason: CheckReasonAccountNamed})
	// Current reservation, but the connection moved on (generation changed).
	s.conns["github"] = Connection{State: StateChecking, Generation: 5, CheckRevision: 1}
	s.finish("github", cur, checkResult{state: StateConnected, reason: CheckReasonAccountNamed})
	if len(*got) != 0 {
		t.Fatalf("superseded or stale checks must write no result event, got %+v", *got)
	}
}
