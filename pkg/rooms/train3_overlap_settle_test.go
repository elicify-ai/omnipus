package rooms

import (
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// Train 3 / FR-216: a Retry that overlaps an admission of the same delivery
// already in flight (for example the room resuming a crash-cut mention on
// load) admits it once, and still returns only once that admission has
// settled - admitAll's contract for its callers. Before the fix the Retry
// skipped the in-flight delivery and returned at once, so the caller saw
// the member still "sending" and no receiver capture yet
// (TestWU15Rooms_CrashAfterMainMirrorBeforeMentionAdmission, CI run
// 40268220854).
func TestTrain3_RetryOverlappingInFlightAdmissionReturnsSettled(t *testing.T) {
	f := newFixture(t)
	room := f.squad("mia", "jim")
	gate := make(chan struct{})
	entered := make(chan string, 8)
	f.deps.mu.Lock()
	f.deps.gate["jim"] = gate
	f.deps.entered = entered
	f.deps.mu.Unlock()
	req := generated.RoomMessageRequest{ClientMessageId: "c1", Text: "@Jim go", Mentions: []string{}}
	firstDone := make(chan error, 1)
	go func() { firstDone <- f.svc.PostMessage(room.Id, req, "admin") }()
	first := <-entered // Jim's admission is in flight and held at the gate
	retryDone := make(chan error, 1)
	go func() { retryDone <- f.svc.PostMessage(room.Id, req, "admin") }()
	select {
	case err := <-retryDone:
		close(gate)
		<-firstDone
		f.drain(room.Id)
		t.Fatalf("FR-216: the Retry returned (err %v) while Jim's admission %s was still in flight; "+
			"it must return with Jim's state decided", err, first)
	case second := <-entered:
		close(gate)
		<-firstDone
		<-retryDone
		f.drain(room.Id)
		t.Fatalf("FR-216: the overlapping Retry started a second admission of %s (first %s)", second, first)
	case <-time.After(300 * time.Millisecond):
	}
	close(gate)
	select {
	case err := <-retryDone:
		if err != nil {
			t.Fatalf("Retry: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the Retry never returned after the in-flight admission settled")
	}
	// Read before any drain: the Retry's return alone must mean settled.
	if got := f.states(room.Id)["jim"]; got != StateAdmitted+"/" {
		t.Fatalf("Jim's state when the Retry returned = %q, want %q", got, StateAdmitted+"/")
	}
	f.deps.mu.Lock()
	captured := f.deps.facts[first].Found
	f.deps.mu.Unlock()
	if !captured {
		t.Fatal("no receiver capture for Jim when the Retry returned")
	}
	if err := <-firstDone; err != nil {
		t.Fatalf("first post: %v", err)
	}
	f.drain(room.Id)
	if got := f.admittedIDs(); countOf(got, first) != 1 {
		t.Fatalf("delivery admitted %d times; want once (%v)", countOf(got, first), got)
	}
}
