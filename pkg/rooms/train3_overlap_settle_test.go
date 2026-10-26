package rooms

import (
	"context"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// failBound only bounds a failure path (an event that never came). No pass
// below rests on elapsed time: every "has not returned yet" is proven by
// observing the goroutine parked in the wait itself.
const failBound = 10 * time.Second

// parkedIn reports whether a goroutine is blocked (status prefix state, e.g.
// "[chan receive") directly in frame fn - "rooms.(*Service).admitAll(" with
// the parenthesis, so the admission goroutine's own closure
// "admitAll.func…" never matches - with every frame of also on its stack.
func parkedIn(fn, state string, also ...string) bool {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	for _, g := range strings.Split(string(buf), "\n\n") {
		header, _, _ := strings.Cut(g, "\n")
		if !strings.Contains(header, state) || !strings.Contains(g, fn) {
			continue
		}
		all := true
		for _, frame := range also {
			all = all && strings.Contains(g, frame)
		}
		if all {
			return true
		}
	}
	return false
}

// awaitParked waits until a goroutine is parked in fn (true), or until
// returned fires first (false: the caller came back without waiting).
func awaitParked[T any](t *testing.T, returned <-chan T, fn, state string, also ...string) bool {
	t.Helper()
	deadline := time.Now().Add(failBound)
	for {
		select {
		case <-returned:
			return false
		default:
		}
		if parkedIn(fn, state, also...) {
			return true
		}
		if time.Now().After(deadline) {
			t.Fatalf("no goroutine parked in %s %s and the caller never returned", fn, state)
		}
		time.Sleep(time.Millisecond)
	}
}

func within[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(failBound):
		t.Fatalf("%s never happened", what)
	}
	var zero T
	return zero
}

const (
	inAdmitAll       = "rooms.(*Service).admitAll("
	inPostMessage    = "rooms.(*Service).PostMessage("
	inAwaitAdmission = "rooms.(*Service).AwaitAdmission("
)

// Train 3 / FR-216: a Retry that overlaps an admission of the same delivery
// already in flight (for example the room resuming a crash-cut mention on
// load) admits it once, and returns only once that admission has settled -
// admitAll's contract for its callers. Before the fix the Retry skipped the
// in-flight delivery and returned at once, so the caller saw the member
// still "sending" and no receiver capture yet
// (TestWU15Rooms_CrashAfterMainMirrorBeforeMentionAdmission, CI run
// 40268220854).
func TestTrain3_RetryOverlappingInFlightAdmissionReturnsSettled(t *testing.T) {
	f := newFixture(t)
	room := f.squad("mia", "jim")
	gate := make(chan struct{})
	var openGate sync.Once
	t.Cleanup(func() { openGate.Do(func() { close(gate) }); f.drain(room.Id) })
	entered := make(chan string, 8)
	f.deps.mu.Lock()
	f.deps.gate["jim"] = gate
	f.deps.entered = entered
	f.deps.mu.Unlock()
	req := generated.RoomMessageRequest{ClientMessageId: "c1", Text: "@Jim go", Mentions: []string{}}
	firstDone := make(chan error, 1)
	go func() { firstDone <- f.svc.PostMessage(room.Id, req, "admin") }()
	first := within(t, entered, "the first admission") // in flight, held at the gate
	retryDone := make(chan error, 1)
	go func() { retryDone <- f.svc.PostMessage(room.Id, req, "admin") }()
	// Acknowledgement: the Retry is parked in admitAll's wait on the
	// in-flight record while the first admission is still held.
	if !awaitParked(t, retryDone, inAdmitAll, "[chan receive", inPostMessage) {
		t.Fatalf("FR-216: the Retry returned while Jim's admission %s was still in flight; "+
			"it must return with Jim's state decided", first)
	}
	select {
	case second := <-entered:
		t.Fatalf("FR-216: the overlapping Retry started a second admission of %s (first %s)", second, first)
	case <-retryDone:
		t.Fatal("the Retry returned before the in-flight admission settled")
	default:
	}
	openGate.Do(func() { close(gate) })
	if err := within(t, retryDone, "the Retry's return after the admission settled"); err != nil {
		t.Fatalf("Retry: %v", err)
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
	if err := within(t, firstDone, "the first post's return"); err != nil {
		t.Fatalf("first post: %v", err)
	}
	f.drain(room.Id)
	if got := f.admittedIDs(); countOf(got, first) != 1 {
		t.Fatalf("delivery admitted %d times; want once (%v)", countOf(got, first), got)
	}
}

// approvalDeps is the member's receiver raising an approval card: Jim's
// admission reports "awaiting approval" when preApproval opens, then waits
// for the person's decision before it commits. Everything else is fakeDeps.
type approvalDeps struct {
	*fakeDeps
	entered               chan string
	preApproval, decision chan struct{}
	cards                 int
}

func (d *approvalDeps) Admit(ctx context.Context, a Admission) (AdmitResult, error) {
	if a.Receiver.AgentID != "jim" {
		return d.fakeDeps.Admit(ctx, a)
	}
	d.entered <- a.DeliveryID
	<-d.preApproval
	d.mu.Lock()
	d.cards++
	d.mu.Unlock()
	if a.OnAwaitingApproval != nil {
		a.OnAwaitingApproval()
	}
	<-d.decision
	return d.fakeDeps.Admit(ctx, a)
}

// Train 3 / FR-251, §6.11: when the admission an overlapping Retry joined
// raises the member's approval card, both the original POST and the Retry
// return at "awaiting approval" without waiting for the person's decision;
// only AwaitAdmission (the approval REST call) waits for the admission to
// finish. One admission, one card.
func TestTrain3_OverlappingRetryReturnsAtApprovalCardNotDecision(t *testing.T) {
	f := newFixture(t)
	deps := &approvalDeps{fakeDeps: f.deps, entered: make(chan string, 8), preApproval: make(chan struct{}), decision: make(chan struct{})}
	f.svc = New(NewFileJournal(f.dir), DepsFrom(deps))
	room := f.squad("mia", "jim")
	var openPre, decide sync.Once
	t.Cleanup(func() {
		openPre.Do(func() { close(deps.preApproval) })
		decide.Do(func() { close(deps.decision) })
		f.drain(room.Id)
	})
	req := generated.RoomMessageRequest{ClientMessageId: "c1", Text: "@Jim go", Mentions: []string{}}
	firstDone := make(chan error, 1)
	go func() { firstDone <- f.svc.PostMessage(room.Id, req, "admin") }()
	first := within(t, deps.entered, "the first admission") // in flight, before its card
	retryDone := make(chan error, 1)
	go func() { retryDone <- f.svc.PostMessage(room.Id, req, "admin") }()
	if !awaitParked(t, retryDone, inAdmitAll, "[chan receive", inPostMessage) {
		t.Fatalf("FR-216: the Retry returned while Jim's admission %s was still in flight", first)
	}
	// The card is raised; the person has not decided.
	openPre.Do(func() { close(deps.preApproval) })
	if err := within(t, firstDone, "the original POST's return at the approval card"); err != nil {
		t.Fatalf("first post: %v", err)
	}
	if err := within(t, retryDone, "the Retry's return at the approval card (it waited for the person's decision)"); err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if got := f.states(room.Id)["jim"]; got != StateAwaitingApproval+"/" {
		t.Fatalf("Jim's state with the card pending = %q, want %q", got, StateAwaitingApproval+"/")
	}
	if got := f.deps.admittedTo(); len(got) != 0 {
		t.Fatalf("admitted %v before the person decided", got)
	}
	// The approval REST call still waits for the admission to finish.
	awaitDone := make(chan struct{})
	go func() { f.svc.AwaitAdmission(first, time.Hour); close(awaitDone) }()
	if !awaitParked(t, awaitDone, inAwaitAdmission, "[select") {
		t.Fatal("FR-251: AwaitAdmission returned before the person decided; it must wait for the admission to finish")
	}
	decide.Do(func() { close(deps.decision) })
	within(t, awaitDone, "AwaitAdmission's return after the decision")
	if got := f.states(room.Id)["jim"]; got != StateAdmitted+"/" {
		t.Fatalf("Jim's state when AwaitAdmission returned = %q, want %q", got, StateAdmitted+"/")
	}
	f.drain(room.Id)
	f.deps.mu.Lock()
	cards := deps.cards
	f.deps.mu.Unlock()
	if got := f.admittedIDs(); countOf(got, first) != 1 || cards != 1 {
		t.Fatalf("admissions of %s = %d, cards = %d; want one of each (%v)", first, countOf(got, first), cards, got)
	}
}
