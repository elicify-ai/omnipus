package rooms

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// WU-15 fix round 1 (gate on 4735ec68d): one test per finding, each written
// to fail on the reviewed code. Expected values come from the spec (FR-170,
// FR-171/§6.14, FR-176, FR-204, FR-216, FR-217, §6.11, §6.13, K23) and the
// founder's fix decisions, not from the implementation.

var errFault = errors.New("injected storage fault")

// faultJournal wraps the file journal; each hook, when set, may fail one
// kind of write. hits counts the faults it actually injected, so a test can
// prove its seam fired.
type faultJournal struct {
	*FileJournal
	mu          sync.Mutex
	append      func(stream string, record any) error
	writeRecord func() error
	remove      func() error
	hits        int
}

func (j *faultJournal) Append(roomID, stream string, record any) error {
	j.mu.Lock()
	hook := j.append
	j.mu.Unlock()
	if hook != nil {
		if err := hook(stream, record); err != nil {
			j.hit()
			return err
		}
	}
	return j.FileJournal.Append(roomID, stream, record)
}

func (j *faultJournal) WriteRecord(roomID string, record any) error {
	j.mu.Lock()
	hook := j.writeRecord
	j.mu.Unlock()
	if hook != nil {
		if err := hook(); err != nil {
			j.hit()
			return err
		}
	}
	return j.FileJournal.WriteRecord(roomID, record)
}

func (j *faultJournal) Remove(roomID string) error {
	j.mu.Lock()
	hook := j.remove
	j.mu.Unlock()
	if hook != nil {
		if err := hook(); err != nil {
			j.hit()
			return err
		}
	}
	return j.FileJournal.Remove(roomID)
}

func (j *faultJournal) hit() {
	j.mu.Lock()
	j.hits++
	j.mu.Unlock()
}

func (j *faultJournal) set(fn func(*faultJournal)) {
	j.mu.Lock()
	fn(j)
	j.mu.Unlock()
}

func (j *faultJournal) injected() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.hits
}

// failState fails delivery-state appends that move a delivery to state.
func failState(state string) func(string, any) error {
	return func(stream string, record any) error {
		if d, ok := record.(*deliveryRecord); ok && stream == StreamDeliveries && d.State == state {
			return errFault
		}
		return nil
	}
}

func newFaultFixture(t *testing.T) (*fixture, *faultJournal) {
	t.Helper()
	dir := t.TempDir()
	deps := newFakeDeps()
	j := &faultJournal{FileJournal: NewFileJournal(dir)}
	return &fixture{t: t, dir: dir, deps: deps, svc: New(j, DepsFrom(deps))}, j
}

func (f *fixture) reopen() *fixture {
	return &fixture{t: f.t, dir: f.dir, deps: f.deps, svc: New(NewFileJournal(f.dir), DepsFrom(f.deps))}
}

func (f *fixture) answer(roomID, requestID, agentID, text string) error {
	_, err := f.svc.DeliverAnswer(context.Background(), Answer{
		RoomID: roomID, RequestID: requestID, Responder: addressing.Pair{WorkspaceID: "sales", AgentID: agentID},
		ResponderSessionID: "main-" + agentID, Content: text,
	})
	f.drain(roomID)
	return err
}

func (f *fixture) admittedIDs() []string {
	f.deps.mu.Lock()
	defer f.deps.mu.Unlock()
	out := make([]string, 0, len(f.deps.admits))
	for _, a := range f.deps.admits {
		out = append(out, a.DeliveryID)
	}
	return out
}

func (f *fixture) entriesOf(roomID string) []storedEntry {
	r, err := f.svc.loadRoom(roomID)
	if err != nil {
		f.t.Fatalf("load room: %v", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.entries)
}

func (f *fixture) stateOf(roomID, requestID string) string {
	r, err := f.svc.loadRoom(roomID)
	if err != nil {
		f.t.Fatalf("load room: %v", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.deliveries[requestID]
	if d == nil {
		f.t.Fatalf("no delivery %s", requestID)
	}
	return d.State + "/" + d.Reason
}

// S1 / SF#1 (FR-217): a room that cannot be read at the dequeue fence never
// lets the request run; it stays queued, and runs once the room is readable.
func TestFix1_FenceReadFailureNeverRuns(t *testing.T) {
	f := newFixture(t)
	room := f.squad("mia", "jim")
	if err := f.post(room.Id, "c1", "@Jim plan it"); err != nil {
		t.Fatal(err)
	}
	jim := f.deliveryOf(room.Id, "jim")
	f.deps.mu.Lock()
	f.deps.wsErr = errors.New("workspace read error")
	f.deps.mu.Unlock()
	if f.svc.BeginConsume(room.Id, jim.RequestID, "main-jim") {
		t.Fatal("S1: the fence let a request run although its room could not be read")
	}
	f.deps.mu.Lock()
	f.deps.wsErr = nil
	f.deps.mu.Unlock()
	if got := f.stateOf(room.Id, jim.RequestID); got != "admitted/" {
		t.Fatalf("S1: a deferred request shows %q; want it still queued (admitted)", got)
	}
	if slices.Contains(f.deps.discarded, jim.RequestID) {
		t.Fatal("S1: a passing read fault discarded the request")
	}
	if !f.svc.BeginConsume(room.Id, jim.RequestID, "main-jim") {
		t.Fatal("control: a current request must pass once the room is readable")
	}
}

// F02 / SF#2 (FR-170, FR-217): a "working" state that cannot be saved never
// lets the turn run; the request stays queued.
func TestFix1_FenceWorkingWriteFailureNeverRuns(t *testing.T) {
	f, j := newFaultFixture(t)
	room := f.squad("mia", "jim")
	if err := f.post(room.Id, "c1", "@Jim plan it"); err != nil {
		t.Fatal(err)
	}
	jim := f.deliveryOf(room.Id, "jim")
	j.set(func(j *faultJournal) { j.append = failState(StateWorking) })
	if f.svc.BeginConsume(room.Id, jim.RequestID, "main-jim") {
		t.Fatal("F02: the request ran although its working state was not saved")
	}
	if j.injected() == 0 {
		t.Fatal("instrument: the working-state fault did not fire")
	}
	j.set(func(j *faultJournal) { j.append = nil })
	if got := f.stateOf(room.Id, jim.RequestID); got != "admitted/" {
		t.Fatalf("F02: state %q after a failed working write; want admitted", got)
	}
	if !f.svc.BeginConsume(room.Id, jim.RequestID, "main-jim") {
		t.Fatal("control: the request runs once the state can be saved")
	}
}

// FR-216 / SF#2: a second wake for a request a turn already took never runs
// it again.
func TestFix1_FenceRunsARequestOnce(t *testing.T) {
	f := newFixture(t)
	room := f.squad("mia", "jim")
	if err := f.post(room.Id, "c1", "@Jim plan it"); err != nil {
		t.Fatal(err)
	}
	jim := f.deliveryOf(room.Id, "jim")
	if !f.svc.BeginConsume(room.Id, jim.RequestID, "main-jim") {
		t.Fatal("control: first wake runs")
	}
	if f.svc.BeginConsume(room.Id, jim.RequestID, "main-jim") {
		t.Fatal("a duplicate wake ran a request a turn had already taken")
	}
}

// F01 / N1 (FR-216): a Retry acts only on the retried message - an older
// message whose delivery failed is not admitted by retrying another one.
func TestFix1_RetryOnlyTouchesTheRetriedMessage(t *testing.T) {
	f := newFixture(t)
	room := f.squad("mia", "jim")
	f.deps.admitErr["jim"] = errors.New("receiver write fault")
	if err := f.post(room.Id, "a", "@Jim first"); err != nil {
		t.Fatal(err)
	}
	jimA := f.deliveryOf(room.Id, "jim")
	if got := f.stateOf(room.Id, jimA.RequestID); !strings.HasPrefix(got, StateUnavailable) {
		t.Fatalf("setup: A's delivery is %q, want unavailable", got)
	}
	delete(f.deps.admitErr, "jim")
	if err := f.post(room.Id, "b", "@Jim second"); err != nil {
		t.Fatal(err)
	}
	if err := f.post(room.Id, "b", "@Jim second"); err != nil {
		t.Fatalf("retry of B: %v", err)
	}
	if slices.Contains(f.admittedIDs(), jimA.RequestID) {
		t.Fatal("F01: retrying B admitted A, which the user did not retry")
	}
	if err := f.post(room.Id, "a", "@Jim first"); err != nil {
		t.Fatalf("retry of A: %v", err)
	}
	if !slices.Contains(f.admittedIDs(), jimA.RequestID) {
		t.Fatal("control: retrying A itself admits A")
	}
}

// F01 (FR-216): a delivery recorded without its message (a write cut between
// them) is never admitted on its own; re-sending that message completes it.
func TestFix1_OrphanDeliveryIsNeverAdmitted(t *testing.T) {
	f, j := newFaultFixture(t)
	room := f.squad("mia", "jim")
	j.set(func(j *faultJournal) {
		j.append = func(stream string, _ any) error {
			if stream == StreamEntries {
				return errFault
			}
			return nil
		}
	})
	if err := f.post(room.Id, "a", "@Jim orphan"); err == nil {
		t.Fatal("setup: the message save should have failed")
	}
	j.set(func(j *faultJournal) { j.append = nil })
	orphan := deriveID("delivery", deriveID("entry", room.Id, "a"), "jim")
	if err := f.post(room.Id, "b", "@Mia other"); err != nil {
		t.Fatal(err)
	}
	if err := f.post(room.Id, "b", "@Mia other"); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(f.admittedIDs(), orphan) {
		t.Fatal("F01: a delivery whose message was never saved was admitted")
	}
	if err := f.post(room.Id, "a", "@Jim orphan"); err != nil {
		t.Fatalf("re-send of A: %v", err)
	}
	if got := f.admittedIDs(); countOf(got, orphan) != 1 {
		t.Fatalf("control: re-sending A admits its delivery once; admissions %v", got)
	}
}

// W1 / TA-01 (FR-216, BDD-17.27): two overlapping Retries of the same
// message admit (and so ask to approve, capture and wake) a delivery once.
func TestFix1_OverlappingRetriesAdmitOnce(t *testing.T) {
	f := newFixture(t)
	room := f.squad("mia", "jim")
	gate := make(chan struct{})
	entered := make(chan string, 8)
	f.deps.mu.Lock()
	f.deps.gate["jim"] = gate
	f.deps.entered = entered
	f.deps.mu.Unlock()
	req := generated.RoomMessageRequest{ClientMessageId: "c1", Text: "@Jim go", Mentions: []string{}}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = f.svc.PostMessage(room.Id, req, "admin")
	}()
	first := <-entered
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = f.svc.PostMessage(room.Id, req, "admin")
	}()
	select {
	case second := <-entered:
		close(gate)
		wg.Wait()
		f.drain(room.Id)
		t.Fatalf("W1: an overlapping Retry started a second admission of %s (first %s)", second, first)
	case <-time.After(300 * time.Millisecond):
	}
	close(gate)
	wg.Wait()
	f.drain(room.Id)
	if got := f.admittedIDs(); countOf(got, first) != 1 {
		t.Fatalf("W1: delivery admitted %d times; want once (%v)", countOf(got, first), got)
	}
}

// F03 / architect #1 / SF#5 / TA-02 (§6.14, FR-171): a turn's share is kept
// when the room record cannot be saved after its turn line; a fresh load and
// a repeated report show it exactly once.
func TestFix1_TurnShareSurvivesFailedRecordSave(t *testing.T) {
	f, j := newFaultFixture(t)
	room := f.squad("mia", "jim")
	j.set(func(j *faultJournal) { j.writeRecord = func() error { return errFault } })
	c := TurnCost{RoomID: room.Id, TurnID: "turn-1", CostUSD: 0.03, Priced: true, Inputs: 3, RoomInputs: 1}
	f.svc.AddTurnCost(c)
	if j.injected() == 0 {
		t.Fatal("instrument: the record-save fault did not fire")
	}
	j.set(func(j *faultJournal) { j.writeRecord = nil })
	fresh := f.reopen()
	got, err := fresh.svc.Get(room.Id)
	if err != nil {
		t.Fatal(err)
	}
	if got.AllocatedCostMicroUsd != 10000 {
		t.Fatalf("F03: after a failed record save and a reload the room shows %d µ$, want 10000", got.AllocatedCostMicroUsd)
	}
	fresh.svc.AddTurnCost(c)
	if got, _ = fresh.svc.Get(room.Id); got.AllocatedCostMicroUsd != 10000 || got.AllocatedCostUnpriced {
		t.Fatalf("F03: a repeated report changed the cost to %d (unpriced=%v), want 10000 once", got.AllocatedCostMicroUsd, got.AllocatedCostUnpriced)
	}
}

// F04 (FR-217): a removal whose membership could not be saved cancels
// nothing; the member keeps its pending request.
func TestFix1_FailedRemovalCancelsNothing(t *testing.T) {
	f, j := newFaultFixture(t)
	room := f.squad("mia", "jim", "r2")
	f.deps.held["jim"] = true
	if err := f.post(room.Id, "c1", "@Jim later"); err != nil {
		t.Fatal(err)
	}
	jim := f.deliveryOf(room.Id, "jim")
	j.set(func(j *faultJournal) { j.writeRecord = func() error { return errFault } })
	if _, err := f.svc.UpdateMembers(room.Id, generated.RoomMembersUpdateRequest{Action: "remove", AgentIds: []string{"jim"}}, "admin"); err == nil {
		t.Fatal("setup: the removal should have failed to save")
	}
	j.set(func(j *faultJournal) { j.writeRecord = nil })
	if got := f.stateOf(room.Id, jim.RequestID); got != "held/" {
		t.Fatalf("F04: after a failed removal Jim's request is %q, want still held", got)
	}
	if slices.Contains(f.deps.discarded, jim.RequestID) {
		t.Fatal("F04: a failed removal discarded the member's request")
	}
	got, _ := f.svc.Get(room.Id)
	if !slices.Contains(got.Members, "jim") {
		t.Fatal("setup: Jim must still be a member")
	}
}

// F04 (FR-217): a deletion that could not be saved cancels nothing.
func TestFix1_FailedDeletionCancelsNothing(t *testing.T) {
	f, j := newFaultFixture(t)
	room := f.squad("mia", "jim")
	f.deps.held["jim"] = true
	if err := f.post(room.Id, "c1", "@Jim later"); err != nil {
		t.Fatal(err)
	}
	jim := f.deliveryOf(room.Id, "jim")
	j.set(func(j *faultJournal) {
		j.writeRecord = func() error { return errFault }
		j.remove = func() error { return errFault }
	})
	if err := f.svc.Delete(room.Id, "admin"); err == nil {
		t.Fatal("setup: the deletion should have failed")
	}
	j.set(func(j *faultJournal) { j.writeRecord, j.remove = nil, nil })
	if slices.Contains(f.deps.discarded, jim.RequestID) {
		t.Fatal("F04: a failed deletion discarded a pending request")
	}
	if got := f.stateOf(room.Id, jim.RequestID); got != "held/" {
		t.Fatalf("F04: after a failed deletion the request is %q, want still held", got)
	}
}

// F05 (§6.13, BDD-17.31): picked ids are authoritative - a picked member that
// shares its name with another is addressed, not refused, and the label text
// never adds another member.
func TestFix1_PickerIsAuthoritative(t *testing.T) {
	f := newFixture(t)
	f.deps.ws.Agents = []Agent{{"mia", "Mia"}, {"mia2", "Mia"}, {"jim", "Jim"}}
	f.deps.names["mia2"] = "Mia"
	room := f.squad("mia", "mia2", "jim")
	if err := f.post(room.Id, "dup", "@Mia hello", "mia2"); err != nil {
		t.Fatalf("F05: a picked duplicate name was refused: %v", err)
	}
	if got := f.deps.admittedTo(); !slices.Equal(got, []string{"mia2"}) {
		t.Fatalf("F05: picked mia2, admitted %v", got)
	}
	if err := f.post(room.Id, "label", "@Jim please", "mia"); err != nil {
		t.Fatal(err)
	}
	if got := f.deps.admittedTo(); countOf(got, "jim") != 0 {
		t.Fatalf("F05: the label text added Jim although only mia was picked: %v", got)
	}
}

// F08 (FR-216, §6.11): an answer that reached the room before a crash cut
// its state write is "answered" after the restart, not "interrupted".
func TestFix1_RecoveredAnswerIsAnswered(t *testing.T) {
	f, j := newFaultFixture(t)
	room := f.squad("mia", "jim")
	if err := f.post(room.Id, "c1", "@Jim plan it"); err != nil {
		t.Fatal(err)
	}
	jim := f.deliveryOf(room.Id, "jim")
	if !f.svc.BeginConsume(room.Id, jim.RequestID, "main-jim") {
		t.Fatal("setup: fence")
	}
	j.set(func(j *faultJournal) { j.append = failState(StateAnswered) })
	if err := f.answer(room.Id, jim.RequestID, "jim", "the plan"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	if j.injected() == 0 {
		t.Fatal("instrument: the answered-state fault did not fire")
	}
	fresh := f.reopen()
	if got := fresh.stateOf(room.Id, jim.RequestID); got != "answered/" {
		t.Fatalf("F08: after restart the answered request shows %q", got)
	}
	if slices.Contains(f.deps.discarded, jim.RequestID) {
		t.Fatal("F08: recovery discarded an answered request")
	}
}

// F09 (K23 needs_attention): a newer settled request never hides an older
// pending decision of the same member.
func TestFix1_AttentionKeepsOlderPendingDecision(t *testing.T) {
	f := newFixture(t)
	room := f.squad("mia", "jim")
	r, _ := f.svc.loadRoom(room.Id)
	r.mu.Lock()
	r.putDelivery(&deliveryRecord{RequestID: "older", EntryID: "e1", AgentID: "mia", State: StateAwaitingApproval})
	r.putDelivery(&deliveryRecord{RequestID: "newer", EntryID: "e2", AgentID: "mia", State: StateRefused})
	ws, _, _ := f.deps.LookupWorkspace("sales")
	view := f.svc.roomView(r, ws)
	r.mu.Unlock()
	if !view.NeedsAttention {
		t.Fatal("F09: an older awaiting-approval request lost its attention marker to a newer refused one")
	}
}

// SF#3 (ruling Q-H): a mirror marker that cannot be recorded is an error,
// the answer is not posted twice by the repair, and its mention goes out
// once after the repair.
func TestFix1_MirrorMarkerFailureIsReturnedAndRepairedOnce(t *testing.T) {
	f, j := newFaultFixture(t)
	room := f.squad("mia", "jim")
	if err := f.post(room.Id, "c1", "@Jim plan it"); err != nil {
		t.Fatal(err)
	}
	jim := f.deliveryOf(room.Id, "jim")
	f.svc.BeginConsume(room.Id, jim.RequestID, "main-jim")
	j.set(func(j *faultJournal) {
		j.append = func(stream string, _ any) error {
			if stream == StreamMirrors {
				return errFault
			}
			return nil
		}
	})
	if err := f.answer(room.Id, jim.RequestID, "jim", "plan ready @Mia"); !errors.Is(err, ErrMirrorFailed) {
		t.Fatalf("SF#3: a failed mirror marker returned %v; want ErrMirrorFailed", err)
	}
	j.set(func(j *faultJournal) { j.append = nil })
	if err := f.answer(room.Id, jim.RequestID, "jim", "plan ready @Mia"); err != nil {
		t.Fatalf("repair: %v", err)
	}
	r, _ := f.svc.loadRoom(room.Id)
	if n := countAnswers(r, jim.RequestID); n != 1 {
		t.Fatalf("SF#3: the repair posted the answer %d times", n)
	}
	if got := f.admittedIDsFor("mia"); len(got) != 1 {
		t.Fatalf("SF#3: the answer's mention reached Mia %d times; want once", len(got))
	}
}

func (f *fixture) admittedIDsFor(agentID string) []string {
	f.deps.mu.Lock()
	defer f.deps.mu.Unlock()
	var out []string
	for _, a := range f.deps.admits {
		if a.Receiver.AgentID == agentID {
			out = append(out, a.DeliveryID)
		}
	}
	return out
}

// SF#6 (§6.11): a room whose recovered state cannot be saved is not served
// with a "working" member.
func TestFix1_UnsavableRecoveryIsNotServed(t *testing.T) {
	f := newFixture(t)
	room := f.squad("mia", "jim")
	if err := f.post(room.Id, "c1", "@Jim plan it"); err != nil {
		t.Fatal(err)
	}
	jim := f.deliveryOf(room.Id, "jim")
	f.svc.BeginConsume(room.Id, jim.RequestID, "main-jim")
	j := &faultJournal{FileJournal: NewFileJournal(f.dir)}
	j.append = failState(StateInterrupted)
	fresh := New(j, DepsFrom(f.deps))
	got, err := fresh.Get(room.Id)
	if err == nil {
		t.Fatalf("SF#6: a room whose interrupted state could not be saved was served: %+v", got)
	}
	if StatusOf(err) != http.StatusInternalServerError {
		t.Fatalf("SF#6: status %d, want 500", StatusOf(err))
	}
	j.set(func(j *faultJournal) { j.append = nil })
	if st := fresh.reopenState(t, room.Id, jim.RequestID); st != "interrupted/restart" {
		t.Fatalf("control: once writable, recovery shows %q", st)
	}
}

func (s *Service) reopenState(t *testing.T, roomID, requestID string) string {
	t.Helper()
	if _, err := s.Get(roomID); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	r, _ := s.loadRoom(roomID)
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.deliveries[requestID]
	return d.State + "/" + d.Reason
}

// SF#7 (FR-170): /pause whose cancellations cannot be recorded reports it;
// a repeated /pause records them.
func TestFix1_PauseReportsUnrecordedCancellations(t *testing.T) {
	f, j := newFaultFixture(t)
	room := f.squad("mia", "jim")
	f.deps.held["jim"] = true
	if err := f.post(room.Id, "c1", "@Jim later"); err != nil {
		t.Fatal(err)
	}
	jim := f.deliveryOf(room.Id, "jim")
	j.set(func(j *faultJournal) { j.append = failState(StateCancelled) })
	if err := f.post(room.Id, "p1", "/pause"); err == nil {
		t.Fatal("SF#7: /pause reported success although no cancellation was recorded")
	}
	j.set(func(j *faultJournal) { j.append = nil })
	if err := f.post(room.Id, "p2", "/pause"); err != nil {
		t.Fatalf("repeated /pause: %v", err)
	}
	if got := f.stateOf(room.Id, jim.RequestID); got != "cancelled/paused" {
		t.Fatalf("SF#7: after the repeated /pause the request is %q", got)
	}
}

// S2 (FR-176): an agent answer over the effective message limit is refused
// with a clear error and changes nothing: no entry, mirror or mention.
func TestFix1_AnswerOverLimitIsRefused(t *testing.T) {
	f := newFixture(t)
	f.deps.body = 1000 // a smaller operator limit
	room := f.squad("mia", "jim")
	if err := f.post(room.Id, "c1", "@Jim plan it"); err != nil {
		t.Fatal(err)
	}
	jim := f.deliveryOf(room.Id, "jim")
	f.svc.BeginConsume(room.Id, jim.RequestID, "main-jim")
	before := len(f.entriesOf(room.Id))
	over := "@Mia " + strings.Repeat("é", (1000-5)/2+1) // 1,001+ bytes
	err := f.answer(room.Id, jim.RequestID, "jim", over)
	if err == nil || !strings.Contains(err.Error(), "1000 bytes") {
		t.Fatalf("S2: an over-limit answer returned %v; want a refusal naming the limit", err)
	}
	if len(f.entriesOf(room.Id)) != before || len(f.deps.mirrors) != 0 || countOf(f.deps.admittedTo(), "mia") != 0 {
		t.Fatal("S2: a refused answer changed the room, the main chat or a member")
	}
	at := "@Mia " + strings.Repeat("a", 1000-5)
	if err := f.answer(room.Id, jim.RequestID, "jim", at); err != nil {
		t.Fatalf("control: an at-limit answer: %v", err)
	}
	if len(f.entriesOf(room.Id)) != before+1 || len(f.deps.mirrors) != 1 {
		t.Fatal("control: an at-limit answer is posted and mirrored once")
	}
}

// S3 (FR-162, Room.members maxItems 256): membership changes keep the room
// at most 256 agents and name at most 256, whatever inbound validation does.
func TestFix1_MembershipCaps(t *testing.T) {
	f := newFixture(t)
	agents := make([]Agent, 0, 258)
	for i := 0; i < 258; i++ {
		id := fmt.Sprintf("a%03d", i)
		agents = append(agents, Agent{id, "Agent " + id})
		f.deps.names[id] = "Agent " + id
	}
	f.deps.ws.Agents = agents
	ids := make([]string, 0, 255)
	for _, a := range agents[:255] {
		ids = append(ids, a.ID)
	}
	room := f.squad(ids...)
	if _, err := f.svc.UpdateMembers(room.Id, generated.RoomMembersUpdateRequest{Action: "add", AgentIds: []string{"a255"}}, "admin"); err != nil {
		t.Fatalf("control: 255 -> 256: %v", err)
	}
	_, err := f.svc.UpdateMembers(room.Id, generated.RoomMembersUpdateRequest{Action: "add", AgentIds: []string{"a256"}}, "admin")
	if StatusOf(err) != http.StatusUnprocessableEntity {
		t.Fatalf("S3: 256 -> 257 returned %v; want 422", err)
	}
	if got, _ := f.svc.Get(room.Id); len(got.Members) != 256 {
		t.Fatalf("S3: a refused add changed the room to %d members", len(got.Members))
	}
	many := make([]string, 257)
	for i := range many {
		many[i] = "a000"
	}
	if _, err := f.svc.UpdateMembers(room.Id, generated.RoomMembersUpdateRequest{Action: "mute", AgentIds: many}, "admin"); StatusOf(err) != http.StatusUnprocessableEntity {
		t.Fatalf("S3: a 257-id mute returned %v; want 422", err)
	}
}

// SF#3 / FR-216: a mention a crash left unsent after its answer was copied
// to the main chat is sent when the room is next loaded, once, without a
// person's Retry.
func TestFix1_UnsentMentionIsSentOnLoad(t *testing.T) {
	f := newFixture(t)
	room := f.squad("mia", "jim")
	if err := f.post(room.Id, "c1", "@Jim plan it"); err != nil {
		t.Fatal(err)
	}
	jim := f.deliveryOf(room.Id, "jim")
	f.svc.BeginConsume(room.Id, jim.RequestID, "main-jim")
	f.deps.mu.Lock()
	f.deps.mirrorErr = errFault
	f.deps.mu.Unlock()
	if err := f.answer(room.Id, jim.RequestID, "jim", "plan ready @Mia"); !errors.Is(err, ErrMirrorFailed) {
		t.Fatalf("setup: %v", err)
	}
	f.deps.mu.Lock()
	f.deps.mirrorErr = nil
	f.deps.mu.Unlock()
	fresh := f.reopen()
	if _, err := fresh.svc.Get(room.Id); err != nil {
		t.Fatal(err)
	}
	fresh.drain(room.Id)
	if got := fresh.admittedIDsFor("mia"); len(got) != 1 {
		t.Fatalf("SF#3: after the restart the answer's mention reached Mia %d times; want once", len(got))
	}
}

// TA-06 (FR-176): the pace recovers after its one-minute window - it is a
// pace, not a lifetime cap.
func TestFix1_RateLimitRecoversAfterTheWindow(t *testing.T) {
	f := newFixture(t)
	f.deps.rate = 2
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	f.svc.SetClockForTest(func() time.Time { return now })
	room := f.squad("mia", "jim")
	for i := 1; i <= 3; i++ {
		if err := f.post(room.Id, fmt.Sprintf("m%d", i), "@Jim go"); err != nil {
			t.Fatal(err)
		}
	}
	third := deriveID("delivery", deriveID("entry", room.Id, "m3"), "jim")
	if got := f.stateOf(room.Id, third); got != "unavailable/rate_limit" {
		t.Fatalf("setup: third request %q, want rate-limited", got)
	}
	if err := f.post(room.Id, "other", "@Mia go"); err != nil {
		t.Fatal(err)
	}
	if countOf(f.deps.admittedTo(), "mia") != 1 {
		t.Fatal("control: another target has its own pace")
	}
	now = now.Add(59 * time.Second)
	if err := f.post(room.Id, "m3", "@Jim go"); err != nil {
		t.Fatal(err)
	}
	if got := f.stateOf(room.Id, third); got != "unavailable/rate_limit" {
		t.Fatalf("TA-06: inside the window the retry is %q, want still paced", got)
	}
	now = now.Add(2 * time.Second)
	if err := f.post(room.Id, "m3", "@Jim go"); err != nil {
		t.Fatal(err)
	}
	if got := f.stateOf(room.Id, third); got != "admitted/" {
		t.Fatalf("TA-06: after the window the retry is %q, want admitted", got)
	}
	if got := f.admittedIDs(); countOf(got, third) != 1 {
		t.Fatalf("TA-06: the paced request was admitted %d times", countOf(got, third))
	}
}

// TA-07 (FR-200): the room context holds the newest 20 messages since the
// member's last request, counted independently of the byte cap.
func TestFix1_ContextIsNewestTwentyEntries(t *testing.T) {
	f := newFixture(t)
	room := f.squad("mia", "jim")
	for i := 1; i <= 21; i++ {
		if err := f.post(room.Id, fmt.Sprintf("j%d", i), fmt.Sprintf("@Jim row-%02d-mark", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.post(room.Id, "m", "@Mia go"); err != nil {
		t.Fatal(err)
	}
	f.deps.mu.Lock()
	var mia Admission
	for _, a := range f.deps.admits {
		if a.Receiver.AgentID == "mia" {
			mia = a
		}
	}
	f.deps.mu.Unlock()
	if strings.Contains(mia.ModelText, "row-01-mark") {
		t.Fatal("TA-07: the oldest of 21 rows is in the context; want only the newest 20")
	}
	for i := 2; i <= 21; i++ {
		if mark := fmt.Sprintf("row-%02d-mark", i); !strings.Contains(mia.ModelText, mark) {
			t.Fatalf("TA-07: %s missing from the context", mark)
		}
	}
	if len(mia.ModelText) > maxContextBytes+512 {
		t.Fatalf("instrument: the rows must fit well under the byte cap (%d bytes)", len(mia.ModelText))
	}
}

// S4 / F06 (ruling Q2): the server keeps every live state in the snapshot,
// also past 10,000 (the contract no longer caps member_states; the SPA half
// is tests/wu15-rooms-contract/snapshotLiveStates.test.ts).
func TestFix1_SnapshotKeepsEveryLiveStatePastTenThousand(t *testing.T) {
	r := bigRoom(1)
	for i := 0; i < 10001; i++ {
		r.putDelivery(&deliveryRecord{RequestID: fmt.Sprintf("l%05d", i), EntryID: "e1", AgentID: "mia", State: StateHeld})
	}
	r.putDelivery(&deliveryRecord{RequestID: "done", EntryID: "e1", AgentID: "jim", State: StateAnswered})
	snap := (&Service{}).snapshot(r, WorkspaceInfo{ID: "sales", Agents: []Agent{{"mia", "Mia"}, {"jim", "Jim"}}}, 0)
	live := 0
	for _, d := range snap.MemberStates {
		if d.State == StateHeld {
			live++
		}
	}
	// Over the 10,000 cap only terminal states give way (ruling Q2).
	if live != 10001 || len(snap.MemberStates) != 10001 {
		t.Fatalf("S4: snapshot kept %d of 10001 live states (%d total); want every live state, terminal ones dropped first", live, len(snap.MemberStates))
	}
}
