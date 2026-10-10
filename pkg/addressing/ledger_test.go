package addressing

import (
	"errors"
	"os"
	"path/filepath"
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
