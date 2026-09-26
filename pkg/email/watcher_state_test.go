package email

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestLoadWatcherState_NotFoundPath pins LoadWatcherState's three outcome
// classes: no state file → the ErrNoWatcherState sentinel (the not-found path
// the unsafe-error-wrap guard flagged at watcher.go:348), a saved state loads,
// and any other read/parse failure keeps its raw error (not the sentinel).
func TestLoadWatcherState_NotFoundPath(t *testing.T) {
	dir := t.TempDir()
	const agentID = "agent-1"
	const wsID = "ws-1"

	t.Run("missing state file yields the sentinel", func(t *testing.T) {
		st, err := LoadWatcherState(dir, agentID, wsID)
		if st != nil {
			t.Fatalf("state = %+v, want nil", st)
		}
		if !errors.Is(err, ErrNoWatcherState) {
			t.Fatalf("err = %v, want ErrNoWatcherState", err)
		}
	})

	t.Run("saved state loads", func(t *testing.T) {
		want := WatcherState{
			AgentID:       agentID,
			WorkspaceID:   wsID,
			UIDValidity:   42,
			LastSeenUID:   7,
			UnseenTotal:   3,
			State:         "ok",
			LastSuccessAt: "2026-09-26T01:02:03Z",
		}
		p := filepath.Join(dir, "email-watch", keyFor(agentID, wsID)+".json")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(want)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}

		got, err := LoadWatcherState(dir, agentID, wsID)
		if err != nil {
			t.Fatalf("LoadWatcherState: %v", err)
		}
		if got == nil || got.State != "ok" || got.UnseenTotal != 3 || got.LastSeenUID != 7 {
			t.Fatalf("state = %+v, want the saved round-trip", got)
		}
	})

	t.Run("corrupt JSON keeps the raw error", func(t *testing.T) {
		p := filepath.Join(dir, "email-watch", keyFor(agentID, wsID)+".json")
		if err := os.WriteFile(p, []byte("{not json"), 0o600); err != nil {
			t.Fatal(err)
		}
		st, err := LoadWatcherState(dir, agentID, wsID)
		if st != nil {
			t.Fatalf("state = %+v, want nil", st)
		}
		if err == nil || errors.Is(err, ErrNoWatcherState) {
			t.Fatalf("err = %v, want a raw (non-sentinel) parse error", err)
		}
	})
}
