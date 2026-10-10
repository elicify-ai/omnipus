package session_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Compile-time interface satisfaction checks.
var (
	_ session.SessionStore = (*session.SessionManager)(nil)
	_ session.SessionStore = (*session.JSONLBackend)(nil)
)

func newBackend(t *testing.T) *session.JSONLBackend {
	t.Helper()
	store, err := memory.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return session.NewJSONLBackend(store)
}

func TestJSONLBackend_AddAndGetHistory(t *testing.T) {
	b := newBackend(t)

	b.AddMessage("s1", "user", "hello")
	b.AddMessage("s1", "assistant", "hi")

	history := b.GetHistory("s1")
	if len(history) != 2 {
		t.Fatalf("got %d messages, want 2", len(history))
	}
	if history[0].Role != "user" || history[0].Content != "hello" {
		t.Errorf("msg[0] = %+v", history[0])
	}
	if history[1].Role != "assistant" || history[1].Content != "hi" {
		t.Errorf("msg[1] = %+v", history[1])
	}
}

func TestJSONLBackend_AddFullMessage(t *testing.T) {
	b := newBackend(t)

	msg := providers.Message{
		Role:    "assistant",
		Content: "done",
		ToolCalls: []providers.ToolCall{
			{ID: "tc1", Function: &providers.FunctionCall{Name: "read_file", Arguments: `{"path":"x"}`}},
		},
	}
	b.AddFullMessage("s1", msg)

	history := b.GetHistory("s1")
	if len(history) != 1 {
		t.Fatalf("got %d, want 1", len(history))
	}
	if len(history[0].ToolCalls) != 1 || history[0].ToolCalls[0].ID != "tc1" {
		t.Errorf("tool calls = %+v", history[0].ToolCalls)
	}
}

// Window trimming now advances Skip through CommitWindow; the in-place
// TruncateHistory rewrite and the SetHistory first-fill primitive are deleted
// (session-core DEL-12). The surviving assertions are kept unchanged.
func TestJSONLBackend_CommitWindowTrimAndSave(t *testing.T) {
	b := newBackend(t)

	for i := 0; i < 10; i++ {
		b.AddMessage("s1", "user", fmt.Sprintf("msg %d", i))
	}
	commitSkip(t, b, "s1", 7)

	history := b.GetHistory("s1")
	if len(history) != 3 {
		t.Fatalf("got %d, want 3", len(history))
	}
	if history[0].Content != "msg 7" {
		t.Errorf("got %q, want %q", history[0].Content, "msg 7")
	}

	// Save is a no-op for the JSONL backend (FR-005: Compact removed from
	// Save path so evicted lines are preserved for recall_conversation).
	if err := b.Save("s1"); err != nil {
		t.Fatal(err)
	}

	// Live window is still accessible after Save.
	history = b.GetHistory("s1")
	if len(history) != 3 {
		t.Fatalf("after save: got %d, want 3", len(history))
	}
}

func TestJSONLBackend_EmptySession(t *testing.T) {
	b := newBackend(t)

	history := b.GetHistory("nonexistent")
	if history == nil {
		t.Fatal("got nil, want empty slice")
	}
	if len(history) != 0 {
		t.Errorf("got %d, want 0", len(history))
	}
}

func TestJSONLBackend_SessionIsolation(t *testing.T) {
	b := newBackend(t)
	b.AddMessage("s1", "user", "session1")
	b.AddMessage("s2", "user", "session2")

	h1 := b.GetHistory("s1")
	h2 := b.GetHistory("s2")

	if len(h1) != 1 || h1[0].Content != "session1" {
		t.Errorf("s1: %+v", h1)
	}
	if len(h2) != 1 || h2[0].Content != "session2" {
		t.Errorf("s2: %+v", h2)
	}
}

func TestJSONLBackend_TrimFlow(t *testing.T) {
	// Simulates the real window-trim flow in the agent loop:
	// CommitWindow (advance Skip) → Save.
	// Save is a no-op (FR-005: Compact removed from Save path) — the
	// live window is still readable via GetHistory after the CommitWindow.
	b := newBackend(t)

	for i := 0; i < 20; i++ {
		b.AddMessage("s1", "user", fmt.Sprintf("msg %d", i))
	}

	commitSkip(t, b, "s1", 16)
	if err := b.Save("s1"); err != nil {
		t.Fatal(err)
	}

	history := b.GetHistory("s1")
	if len(history) != 4 {
		t.Fatalf("got %d messages, want 4", len(history))
	}
	if history[0].Content != "msg 16" {
		t.Errorf("first message = %q, want %q", history[0].Content, "msg 16")
	}
}

// commitSkip advances a JSONLBackend session's window cursor to skip, the
// replacement for the deleted TruncateHistory call: a real, non-destructive
// checkpoint (the earlier lines stay on disk for recall, FR-005).
func commitSkip(t *testing.T, b *session.JSONLBackend, key string, skip int) {
	t.Helper()
	ctx := context.Background()
	snap, err := b.SnapshotWindow(ctx, key)
	if err != nil {
		t.Fatalf("SnapshotWindow: %v", err)
	}
	after := snap.State
	after.Skip = skip
	if err := b.CommitWindow(ctx, key, snap.State, after); err != nil {
		t.Fatalf("CommitWindow: %v", err)
	}
}
