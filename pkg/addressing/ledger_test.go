package addressing

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Oracle: spec C-REPLY "Binding" — the server resolves the request in the
// authenticated responding session/agent; nonexistent, wrong-session, forged,
// discarded or unreadable selection refuses. FR-028: missing usable
// correlation refuses, no fallback.

func capture(session string) Capture {
	return Capture{
		RequestID:         "q1",
		ReceiverSessionID: session,
		Receiver:          Pair{WorkspaceID: "ws-1", AgentID: "jim"},
		Sender:            Sender{CanonicalID: "telegram:42", DisplayName: "Ann"},
		Source: Source{
			Kind: SourceConnector, Owner: Pair{WorkspaceID: "ws-1", AgentID: "mia"},
			SessionID: "main-session-ws-1+mia", InstanceID: "telegram.a", ChatID: "chat-1", PlatformMessageID: "m9",
		},
		AdmittedAt: time.Now().UTC(),
	}
}

func newLedger(t *testing.T, sessions ...string) (*Ledger, string) {
	t.Helper()
	dir := t.TempDir()
	for _, s := range sessions {
		if err := os.MkdirAll(filepath.Join(dir, s), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return NewLedger(dir), dir
}

var jim = Pair{WorkspaceID: "ws-1", AgentID: "jim"}

func TestResolve_ValidRequestRoundTrips(t *testing.T) {
	l, _ := newLedger(t, "jim-main")
	if err := l.Put(capture("jim-main")); err != nil {
		t.Fatal(err)
	}
	got, err := l.Resolve("jim-main", jim, "q1")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Source.InstanceID != "telegram.a" || got.Source.ChatID != "chat-1" || got.Source.Owner.AgentID != "mia" {
		t.Fatalf("source route not preserved: %+v", got.Source)
	}
}

func TestResolve_Refusals(t *testing.T) {
	tests := []struct {
		name      string
		session   string
		responder Pair
		id        string
		want      error
	}{
		{"unknown id", "jim-main", jim, "nope", ErrUnknownRequest},
		{"blank id", "jim-main", jim, "  ", ErrUnknownRequest},
		{"another session of the same agent", "jim-extra", jim, "q1", ErrUnknownRequest},
		{"another agent in the same workspace", "jim-main", Pair{WorkspaceID: "ws-1", AgentID: "ray"}, "q1", ErrUnknownRequest},
		{"same agent id in another workspace", "jim-main", Pair{WorkspaceID: "ws-2", AgentID: "jim"}, "q1", ErrUnknownRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l, _ := newLedger(t, "jim-main", "jim-extra")
			if err := l.Put(capture("jim-main")); err != nil {
				t.Fatal(err)
			}
			if _, err := l.Resolve(tt.session, tt.responder, tt.id); !errors.Is(err, tt.want) {
				t.Fatalf("want %v, got %v", tt.want, err)
			}
		})
	}
}

func TestResolve_DiscardedRefuses(t *testing.T) {
	l, _ := newLedger(t, "jim-main")
	if err := l.Put(capture("jim-main")); err != nil {
		t.Fatal(err)
	}
	if err := l.Discard("jim-main", "q1"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Resolve("jim-main", jim, "q1"); !errors.Is(err, ErrDiscarded) {
		t.Fatalf("want ErrDiscarded, got %v", err)
	}
	if err := l.Discard("jim-main", "never-captured"); err != nil {
		t.Fatalf("discarding an uncaptured request is a no-op, got %v", err)
	}
}

func TestResolve_UnusableCorrelationRefuses(t *testing.T) {
	l, dir := newLedger(t, "jim-main")
	c := capture("jim-main")
	c.Source.ChatID = "" // connector source without a chat
	// Put validates; write the bad record directly to prove Resolve re-checks.
	if err := l.Put(c); err == nil {
		t.Fatal("Put must refuse an incomplete capture")
	}
	raw := `{"request_id":"q1","receiver_session_id":"jim-main","receiver":{"workspace_id":"ws-1","agent_id":"jim"},` +
		`"source":{"kind":"connector","session_id":"s","instance_id":"telegram.a"}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "jim-main", ledgerFileName), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Resolve("jim-main", jim, "q1"); !errors.Is(err, ErrUnusableCorrelation) {
		t.Fatalf("want ErrUnusableCorrelation, got %v", err)
	}
}

func TestLedger_CorruptFileFailsClosed(t *testing.T) {
	l, dir := newLedger(t, "jim-main")
	if err := os.WriteFile(filepath.Join(dir, "jim-main", ledgerFileName), []byte("{not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Resolve("jim-main", jim, "q1"); err == nil || errors.Is(err, ErrUnknownRequest) {
		t.Fatalf("an unreadable ledger must fail closed with a read error, got %v", err)
	}
}

func TestPut_DoesNotCreateASession(t *testing.T) {
	l, dir := newLedger(t)
	if err := l.Put(capture("ghost")); err == nil {
		t.Fatal("Put must refuse a receiver session that does not exist")
	}
	if _, err := os.Stat(filepath.Join(dir, "ghost")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Put resurrected a session directory (stat err=%v)", err)
	}
}

func TestPut_InvalidSessionIDs(t *testing.T) {
	l, _ := newLedger(t)
	for _, id := range []string{"", "../x", "a/b", `a\b`, "."} {
		c := capture(id)
		if err := l.Put(c); err == nil {
			t.Errorf("Put accepted session id %q", id)
		}
	}
}

func TestPairValidate(t *testing.T) {
	long := make([]byte, MaxComponentLen+1)
	for i := range long {
		long[i] = 'a'
	}
	for name, p := range map[string]Pair{
		"empty agent":     {WorkspaceID: "w"},
		"empty workspace": {AgentID: "a"},
		"padded":          {WorkspaceID: " w", AgentID: "a"},
		"too long":        {WorkspaceID: string(long), AgentID: "a"},
	} {
		if err := p.Validate(); err == nil {
			t.Errorf("%s: want refusal", name)
		}
	}
	if err := (Pair{WorkspaceID: "w", AgentID: "a"}).Validate(); err != nil {
		t.Errorf("valid pair refused: %v", err)
	}
}

// S3 M2: a torn last line (a crashed or short append, no newline) was never
// acknowledged. It must not glue onto the next record and poison the session's
// ledger: reads skip it, and the next write cuts it off first.
func TestLedger_TornTailDoesNotPoisonTheSession(t *testing.T) {
	l, dir := newLedger(t, "jim-main")
	first := capture("jim-main")
	first.RequestID = "q1"
	if err := l.Put(first); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "jim-main", ledgerFileName)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"request_id":"q2","receiver_sess`); err != nil {
		t.Fatal(err)
	}
	f.Close()

	if _, ok, err := l.Get("jim-main", "q1"); err != nil || !ok {
		t.Fatalf("a torn tail must not make earlier records unreadable: ok=%v err=%v", ok, err)
	}
	second := capture("jim-main")
	second.RequestID = "q3"
	if err := l.Put(second); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"q1", "q3"} {
		if c, ok, err := l.Get("jim-main", id); err != nil || !ok || c.RequestID != id {
			t.Fatalf("request %s must resolve after the torn tail: ok=%v err=%v", id, ok, err)
		}
	}
	if err := l.Discard("jim-main", "q3"); err != nil {
		t.Fatalf("discard after a torn tail: %v", err)
	}
	if _, err := l.Resolve("jim-main", jim, "q3"); !errors.Is(err, ErrDiscarded) {
		t.Fatalf("q3 must be discarded, got %v", err)
	}
}

// A complete record that lost only its newline is a real record: it is kept.
func TestLedger_CompleteRecordMissingNewlineIsKept(t *testing.T) {
	l, dir := newLedger(t, "jim-main")
	c := capture("jim-main")
	c.RequestID = "q1"
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "jim-main", ledgerFileName)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := l.Get("jim-main", "q1"); err != nil || !ok {
		t.Fatalf("a complete unterminated record must be readable: ok=%v err=%v", ok, err)
	}
	next := capture("jim-main")
	next.RequestID = "q2"
	if err := l.Put(next); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"q1", "q2"} {
		if _, ok, err := l.Get("jim-main", id); err != nil || !ok {
			t.Fatalf("%s must survive: ok=%v err=%v", id, ok, err)
		}
	}
}

func heldCapture(id, sess string) Capture {
	return Capture{
		RequestID: id, ReceiverSessionID: sess,
		Receiver: Pair{WorkspaceID: "ws", AgentID: "ava"},
		Source:   Source{Kind: SourceConversation, SessionID: "asker-session"},
	}
}

// Founder ruling 2026-10-11 (R1 #5): a request for a stopped agent is held in the
// ledger until the agent runs again. Undelivered lists the held ones in admission
// order; a delivered or discarded request is not held.
func TestLedger_Undelivered_ListsOnlyHeldRequestsInAdmissionOrder(t *testing.T) {
	dir := t.TempDir()
	l := NewLedger(dir)
	if err := os.MkdirAll(filepath.Join(dir, "recv"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"r1", "r2", "r3", "r4"} {
		if err := l.Put(heldCapture(id, "recv")); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.MarkDelivered("recv", "r2"); err != nil {
		t.Fatalf("MarkDelivered: %v", err)
	}
	if err := l.Discard("recv", "r3"); err != nil {
		t.Fatal(err)
	}
	held, err := l.Undelivered("recv")
	if err != nil {
		t.Fatalf("Undelivered: %v", err)
	}
	ids := make([]string, 0, len(held))
	for _, c := range held {
		ids = append(ids, c.RequestID)
	}
	if got := strings.Join(ids, ","); got != "r1,r4" {
		t.Fatalf("held = %q, want r1,r4 (r2 delivered, r3 discarded)", got)
	}
	c, ok, err := l.Get("recv", "r2")
	if err != nil || !ok || !c.Delivered {
		t.Fatalf("r2 = %+v ok=%v err=%v, want the durable delivered mark", c, ok, err)
	}
	// A delivered request that is later discarded stays delivered (no regression).
	if err := l.Discard("recv", "r2"); err != nil {
		t.Fatal(err)
	}
	if c, _, _ := l.Get("recv", "r2"); !c.Delivered || !c.Discarded {
		t.Fatalf("r2 after discard = %+v, want delivered and discarded", c)
	}
}

func TestLedger_MarkDelivered_UnknownRequestIsNotAnError(t *testing.T) {
	l := NewLedger(t.TempDir())
	if err := l.MarkDelivered("nobody", "r9"); err != nil {
		t.Fatalf("MarkDelivered on an unknown request = %v, want nil", err)
	}
}

// R3-SRV-SFH-07: the delivery mark and the read of the capture it transitions
// are ONE ledger operation. It reports the capture as it was BEFORE the mark and
// whether this call performed the held-to-delivered transition, so a caller can
// move the sender's row on exactly once; a read failure marks nothing.
func TestLedger_MarkDeliveredFirst_ReportsThePriorCaptureOnceAndNeverMarksOnAFailure(t *testing.T) {
	l, dir := newLedger(t, "recv")
	c := capture("recv")
	c.Held = true
	c.SenderMessageID = "cm-1"
	if err := l.Put(c); err != nil {
		t.Fatal(err)
	}

	prior, first, err := l.MarkDeliveredFirst("recv", "q1")
	if err != nil || !first || !prior.Held || prior.Delivered || prior.SenderMessageID != "cm-1" {
		t.Fatalf("first mark = %+v first=%v err=%v, want the held capture, first=true", prior, first, err)
	}
	if _, again, rErr := l.MarkDeliveredFirst("recv", "q1"); rErr != nil || again {
		t.Fatalf("a repeat mark must report first=false, got first=%v err=%v", again, rErr)
	}
	if _, unk, uErr := l.MarkDeliveredFirst("recv", "never-captured"); uErr != nil || unk {
		t.Fatalf("an unknown request is not an error and not a first delivery, got first=%v err=%v", unk, uErr)
	}

	// A failure marks nothing: the transition is still available afterwards.
	c2 := capture("recv")
	c2.RequestID = "q2"
	c2.Held = true
	if pErr := l.Put(c2); pErr != nil {
		t.Fatal(pErr)
	}
	file := filepath.Join(dir, "recv", ledgerFileName)
	saved, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(file, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, first, err := l.MarkDeliveredFirst("recv", "q2"); err == nil || first {
		t.Fatalf("an unreadable ledger must fail the mark, got first=%v err=%v", first, err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, saved, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, first, err := l.MarkDeliveredFirst("recv", "q2"); err != nil || !first {
		t.Fatalf("after the fault the transition must still be available, got first=%v err=%v", first, err)
	}
}

// R3 N1 / R4-SRV-CR-02: a request recorded but not woken is marked held after
// the fact; the answer says whether the sender may be told "waiting".
func TestLedger_MarkHeld_SetsHeldOnceAndNeverResurrectsADeliveredRequest(t *testing.T) {
	l, dir := newLedger(t, "recv")
	if err := l.Put(capture("recv")); err != nil {
		t.Fatal(err)
	}
	lines := func() int {
		data, err := os.ReadFile(filepath.Join(dir, "recv", ledgerFileName))
		if err != nil {
			t.Fatal(err)
		}
		return strings.Count(string(data), "\n")
	}
	before := lines()
	held, err := l.MarkHeld("recv", "q1")
	if err != nil || !held {
		t.Fatalf("first MarkHeld = held %v err %v, want held", held, err)
	}
	got, ok, err := l.Get("recv", "q1")
	if err != nil || !ok || !got.Held {
		t.Fatalf("after MarkHeld: %+v ok=%v err=%v, want Held", got, ok, err)
	}
	if lines() != before+1 {
		t.Fatalf("the first MarkHeld must append exactly one record: %d -> %d", before, lines())
	}
	held, err = l.MarkHeld("recv", "q1")
	if err != nil || !held {
		t.Fatalf("repeat MarkHeld = held %v err %v, want still held", held, err)
	}
	if lines() != before+1 {
		t.Fatalf("a repeat MarkHeld must append nothing: %d lines, want %d", lines(), before+1)
	}
	if _, _, err = l.MarkDeliveredFirst("recv", "q1"); err != nil {
		t.Fatal(err)
	}
	held, err = l.MarkHeld("recv", "q1")
	if err != nil || held {
		t.Fatalf("MarkHeld after delivery = held %v err %v, want not held", held, err)
	}
	got, _, _ = l.Get("recv", "q1")
	if !got.Delivered {
		t.Fatalf("MarkHeld after delivery must not clear Delivered: %+v", got)
	}
	if held, err := l.MarkHeld("recv", "never-captured"); err != nil || held {
		t.Fatalf("an unknown request is not an error and not held: held %v err %v", held, err)
	}
}
