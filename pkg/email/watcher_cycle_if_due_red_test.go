package email

// N1 (second half) - MC-33 gate: cycleIfDue must never dial while the
// mailbox is backing off (next_attempt_at in the future), and must dial when
// due. Oracle: MC-33/B-43 "no automatic dial while backing off".

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// cycleStubTransport counts INBOX reads. It deliberately does NOT implement
// MailboxStatuser, so probe() takes the ReadInbox fallback - the counter is
// the dial evidence.
type cycleStubTransport struct {
	readInbox atomic.Int32
}

func (s *cycleStubTransport) ReadInbox(ctx context.Context, opts InboxOptions) ([]Message, error) {
	s.readInbox.Add(1)
	return nil, nil
}
func (s *cycleStubTransport) Search(ctx context.Context, query string, opts SearchOptions) (SearchResult, error) {
	return SearchResult{}, nil
}
func (s *cycleStubTransport) ReadMessage(ctx context.Context, uid uint32) (*Message, error) {
	return nil, nil
}
func (s *cycleStubTransport) Send(ctx context.Context, req SendRequest) error { return nil }
func (s *cycleStubTransport) MarkSeen(ctx context.Context, uid uint32) error  { return nil }

func writeWatcherState(t *testing.T, dir string, w WatcherState) string {
	t.Helper()
	p := filepath.Join(dir, "email-watch", keyFor(w.AgentID, w.WorkspaceID)+".json")
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
	b, err := json.Marshal(w)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(p, b, 0o600))
	return p
}

func TestWatcher_CycleIfDue_NeverDialsWhileBackingOff(t *testing.T) {
	dir := t.TempDir()
	const agentID, wsID = "agent-bf", "ws-bf"
	stub := &cycleStubTransport{}
	w, err := NewWatcher(WatcherConfig{AgentID: agentID, WorkspaceID: wsID, Transport: stub, StateDir: dir})
	require.NoError(t, err)
	ctx := context.Background()

	t.Run("future next_attempt_at: no dial", func(t *testing.T) {
		stub.readInbox.Store(0)
		writeWatcherState(t, dir, WatcherState{
			AgentID: agentID, WorkspaceID: wsID,
			State:         "error",
			NextAttemptAt: time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339),
			Attempt:       1,
		})
		if err := w.cycleIfDue(ctx, time.Now()); err != nil {
			t.Fatalf("cycleIfDue while backing off: %v", err)
		}
		if got := stub.readInbox.Load(); got != 0 {
			t.Fatalf("MC-33: watcher dialed %d time(s) while backing off - no automatic dial until next_attempt_at", got)
		}
	})

	t.Run("past next_attempt_at: dials", func(t *testing.T) {
		stub.readInbox.Store(0)
		writeWatcherState(t, dir, WatcherState{
			AgentID: agentID, WorkspaceID: wsID,
			State:         "error",
			NextAttemptAt: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339),
			Attempt:       1,
		})
		if err := w.cycleIfDue(ctx, time.Now()); err != nil {
			t.Fatalf("cycleIfDue when due: %v", err)
		}
		if got := stub.readInbox.Load(); got < 1 {
			t.Fatal("watcher did not dial after the backoff window passed - the gate is stuck closed")
		}
	})

	t.Run("no next_attempt_at: dials (instrument)", func(t *testing.T) {
		stub.readInbox.Store(0)
		writeWatcherState(t, dir, WatcherState{AgentID: agentID, WorkspaceID: wsID, State: "ok"})
		if err := w.cycleIfDue(ctx, time.Now()); err != nil {
			t.Fatalf("cycleIfDue with no backoff state: %v", err)
		}
		if got := stub.readInbox.Load(); got < 1 {
			t.Fatal("instrument: watcher never dialed on a due mailbox - the no-dial assertion above would be vacuous")
		}
	})
}
