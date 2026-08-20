package email

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// fakeTransport is an in-memory Transport for drainer tests.
type fakeTransport struct {
	msgs     []Message
	seen     map[uint32]bool
	failRead bool
}

func newFakeTransport(msgs ...Message) *fakeTransport {
	return &fakeTransport{msgs: msgs, seen: map[uint32]bool{}}
}

func (f *fakeTransport) ReadInbox(_ context.Context, limit int, unseenOnly bool) ([]Message, error) {
	if f.failRead {
		return nil, errors.New("imap down")
	}
	var out []Message
	for _, m := range f.msgs {
		if unseenOnly && f.seen[m.UID] {
			continue
		}
		out = append(out, m)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (f *fakeTransport) Search(context.Context, string, int) ([]Message, error) { return nil, nil }
func (f *fakeTransport) ReadMessage(context.Context, uint32) (*Message, error) {
	return nil, errors.New("fakeTransport: ReadMessage not implemented")
}
func (f *fakeTransport) Send(context.Context, SendRequest) error                { return nil }
func (f *fakeTransport) MarkSeen(_ context.Context, uid uint32) error {
	f.seen[uid] = true
	return nil
}

func TestDrainer_UnhandledMailBecomesBoardTask(t *testing.T) {
	store := task.New(t.TempDir())
	ft := newFakeTransport(
		Message{UID: 1, From: "alice@x.com", FromName: "Alice", Subject: "Need help"},
		Message{UID: 2, From: "bob@x.com", Subject: "Invoice"},
	)
	provider := MailboxProviderFunc(func() []Mailbox {
		return []Mailbox{{AgentID: "mia", WorkspaceID: "ws1", Transport: ft}}
	})
	d := NewDrainer(store, provider, 0)

	created := d.Drain(context.Background())
	if created != 2 {
		t.Fatalf("expected 2 tasks created, got %d", created)
	}

	tasks, err := store.List(task.Filter{AgentID: "mia"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 board tasks for mia, got %d", len(tasks))
	}
	for _, tk := range tasks {
		if tk.Status != task.StatusNext {
			t.Errorf("task %q must be in `next`, got %q", tk.ID, tk.Status)
		}
		if tk.WorkspaceID != "ws1" {
			t.Errorf("task %q must be in ws1, got %q", tk.ID, tk.WorkspaceID)
		}
		if tk.Action != task.ActionLLM {
			t.Errorf("task %q must be an llm action", tk.ID)
		}
		if !strings.HasPrefix(tk.Title, "Email:") {
			t.Errorf("task title must be prefixed Email:, got %q", tk.Title)
		}
		if !strings.Contains(tk.Prompt, "uid") {
			t.Errorf("task prompt must reference the message uid, got %q", tk.Prompt)
		}
	}
}

func TestDrainer_IdempotentAcrossTicks(t *testing.T) {
	store := task.New(t.TempDir())
	ft := newFakeTransport(Message{UID: 1, From: "a@x.com", Subject: "Once"})
	provider := MailboxProviderFunc(func() []Mailbox {
		return []Mailbox{{AgentID: "mia", WorkspaceID: "ws1", Transport: ft}}
	})
	d := NewDrainer(store, provider, 0)

	if got := d.Drain(context.Background()); got != 1 {
		t.Fatalf("first drain: expected 1, got %d", got)
	}
	// Second tick: the message is now \Seen, so no new task.
	if got := d.Drain(context.Background()); got != 0 {
		t.Fatalf("second drain: expected 0 (message marked seen), got %d", got)
	}
	tasks, _ := store.List(task.Filter{AgentID: "mia"})
	if len(tasks) != 1 {
		t.Fatalf("expected exactly 1 task across two ticks, got %d", len(tasks))
	}
}

func TestDrainer_ReadErrorDoesNotPanic(t *testing.T) {
	store := task.New(t.TempDir())
	ft := newFakeTransport()
	ft.failRead = true
	provider := MailboxProviderFunc(func() []Mailbox {
		return []Mailbox{{AgentID: "mia", WorkspaceID: "ws1", Transport: ft}}
	})
	d := NewDrainer(store, provider, 0)
	if got := d.Drain(context.Background()); got != 0 {
		t.Fatalf("expected 0 tasks on read error, got %d", got)
	}
}

func TestDrainer_NilSafe(t *testing.T) {
	var d *Drainer
	if got := d.Drain(context.Background()); got != 0 {
		t.Fatalf("nil drainer must be a no-op, got %d", got)
	}
	d2 := NewDrainer(nil, nil, 0)
	if got := d2.Drain(context.Background()); got != 0 {
		t.Fatalf("nil store/provider must be a no-op, got %d", got)
	}
}

func TestDrainer_SkipsIncompleteMailbox(t *testing.T) {
	store := task.New(t.TempDir())
	ft := newFakeTransport(Message{UID: 1, From: "a@x.com", Subject: "x"})
	provider := MailboxProviderFunc(func() []Mailbox {
		return []Mailbox{
			{AgentID: "", WorkspaceID: "ws1", Transport: ft},     // no agent
			{AgentID: "mia", WorkspaceID: "", Transport: ft},     // no workspace
			{AgentID: "mia", WorkspaceID: "ws1", Transport: nil}, // no transport
		}
	})
	d := NewDrainer(store, provider, 0)
	if got := d.Drain(context.Background()); got != 0 {
		t.Fatalf("incomplete mailboxes must be skipped, got %d", got)
	}
}

// TestDrainer_MarkSeenFailureDoesNotLoseTasks verifies that a MarkSeen failure
// after successful task creation still counts the task as created (the task is
// durable even if re-enqueue may occur on next tick).
// Traces to: drainer.go drainMailbox (MarkSeen error path)
func TestDrainer_MarkSeenFailureDoesNotLoseTasks(t *testing.T) {
	store := task.New(t.TempDir())
	ft := &failingMarkSeenTransport{
		msgs: []Message{{UID: 1, From: "a@x.com", Subject: "Need help"}},
	}
	provider := MailboxProviderFunc(func() []Mailbox {
		return []Mailbox{{AgentID: "mia", WorkspaceID: "ws1", Transport: ft}}
	})
	d := NewDrainer(store, provider, 0)
	created := d.Drain(context.Background())
	// Task is created; MarkSeen failure must not zero the counter.
	if created != 1 {
		t.Fatalf("expected 1 task despite MarkSeen failure, got %d", created)
	}
	tasks, err := store.List(task.Filter{AgentID: "mia"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 board task, got %d", len(tasks))
	}
}

// failingMarkSeenTransport is a fake Transport whose MarkSeen always errors.
type failingMarkSeenTransport struct {
	msgs []Message
}

func (f *failingMarkSeenTransport) ReadInbox(_ context.Context, limit int, unseenOnly bool) ([]Message, error) {
	out := make([]Message, 0, len(f.msgs))
	for _, m := range f.msgs {
		out = append(out, m)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (f *failingMarkSeenTransport) Search(context.Context, string, int) ([]Message, error) {
	return nil, nil
}

func (f *failingMarkSeenTransport) ReadMessage(context.Context, uint32) (*Message, error) {
	return nil, errors.New("failingMarkSeenTransport: ReadMessage not implemented")
}
func (f *failingMarkSeenTransport) Send(context.Context, SendRequest) error { return nil }
func (f *failingMarkSeenTransport) MarkSeen(_ context.Context, uid uint32) error {
	return errors.New("mark seen: connection reset")
}

// TestMailToTask_LongSubjectTruncated verifies that subjects producing a title
// over 200 characters are truncated to 200.
// Traces to: drainer.go mailToTask (title length cap)
func TestMailToTask_LongSubjectTruncated(t *testing.T) {
	longSubj := strings.Repeat("x", 300) // "Email: " (7) + 300 = 307 chars
	mb := Mailbox{AgentID: "ava", WorkspaceID: "ws2"}
	msg := Message{UID: 99, From: "a@x.com", Subject: longSubj}
	tk := mailToTask(mb, msg)
	if len(tk.Title) != 200 {
		t.Fatalf("expected title capped at 200 chars, got %d: %q", len(tk.Title), tk.Title)
	}
}

// TestMailToTask_EmptySubjectFallback verifies that an empty subject becomes
// "(no subject)" in the task title.
// Traces to: drainer.go mailToTask (empty subject fallback)
func TestMailToTask_EmptySubjectFallback(t *testing.T) {
	mb := Mailbox{AgentID: "mia", WorkspaceID: "ws1"}
	msg := Message{UID: 1, From: "a@x.com", Subject: ""}
	tk := mailToTask(mb, msg)
	if tk.Title != "Email: (no subject)" {
		t.Fatalf("expected 'Email: (no subject)', got %q", tk.Title)
	}
}

// TestMailToTask_FieldsPopulatedCorrectly verifies all mandatory task fields
// (AgentID, WorkspaceID, Action, Status, Priority, UID in prompt).
// Traces to: drainer.go mailToTask (field mapping)
func TestMailToTask_FieldsPopulatedCorrectly(t *testing.T) {
	mb := Mailbox{AgentID: "ray", WorkspaceID: "ws3"}
	msg := Message{
		UID:      55,
		From:     "boss@x.com",
		FromName: "The Boss",
		Subject:  "Quarterly Report",
		Date:     "2024-01-01T00:00:00Z",
	}
	tk := mailToTask(mb, msg)
	if tk.AgentID != "ray" {
		t.Errorf("AgentID: got %q, want ray", tk.AgentID)
	}
	if tk.CreatedBy != "ray" {
		t.Errorf("CreatedBy: got %q, want ray", tk.CreatedBy)
	}
	if tk.WorkspaceID != "ws3" {
		t.Errorf("WorkspaceID: got %q, want ws3", tk.WorkspaceID)
	}
	if tk.Action != task.ActionLLM {
		t.Errorf("Action: got %q, want %q", tk.Action, task.ActionLLM)
	}
	if tk.Status != task.StatusNext {
		t.Errorf("Status: got %q, want %q", tk.Status, task.StatusNext)
	}
	if tk.Priority != 3 {
		t.Errorf("Priority: got %d, want 3", tk.Priority)
	}
	if !strings.Contains(tk.Prompt, "55") {
		t.Errorf("Prompt must contain uid 55, got:\n%s", tk.Prompt)
	}
	if !strings.Contains(tk.Prompt, "The Boss") {
		t.Errorf("Prompt must include FromName, got:\n%s", tk.Prompt)
	}
	if !strings.Contains(tk.Prompt, "2024-01-01") {
		t.Errorf("Prompt must include Date, got:\n%s", tk.Prompt)
	}
}

// TestMailToTask_Differentiation ensures two different messages produce two
// different tasks (rules out hardcoded prompt).
func TestMailToTask_Differentiation(t *testing.T) {
	mb := Mailbox{AgentID: "mia", WorkspaceID: "ws1"}
	a := mailToTask(mb, Message{UID: 1, From: "a@x.com", Subject: "Alpha"})
	b := mailToTask(mb, Message{UID: 2, From: "b@x.com", Subject: "Beta"})
	if a.Title == b.Title {
		t.Fatalf("different subjects must produce different titles")
	}
	if a.Prompt == b.Prompt {
		t.Fatalf("different messages must produce different prompts")
	}
}

// TestFirstNonEmpty_TableDriven covers all branches of the firstNonEmpty helper.
// Traces to: drainer.go firstNonEmpty (pure function)
func TestFirstNonEmpty_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		vals []string
		want string
	}{
		{"first non-empty wins", []string{"hello", "world"}, "hello"},
		{"skips blank first", []string{"", "second"}, "second"},
		{"skips whitespace", []string{"   ", "\t", "found"}, "found"},
		{"all empty returns empty", []string{"", "  ", ""}, ""},
		{"nil slice", nil, ""},
		{"single value", []string{"only"}, "only"},
		// Differentiation: two different inputs return two different values.
		{"first of different", []string{"alpha", "beta"}, "alpha"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := firstNonEmpty(tc.vals...)
			if got != tc.want {
				t.Errorf("firstNonEmpty(%v) = %q, want %q", tc.vals, got, tc.want)
			}
		})
	}
}

// TestFirstNonEmpty_Differentiation ensures different inputs produce different
// outputs (rules out constant return).
func TestFirstNonEmpty_Differentiation(t *testing.T) {
	a := firstNonEmpty("alpha", "beta")
	b := firstNonEmpty("", "beta")
	if a == b {
		t.Fatalf("firstNonEmpty returned same value %q for different inputs", a)
	}
}

// TestDrainer_MultipleMailboxesIndependent verifies that a read failure in one
// mailbox does not prevent processing of another.
// Traces to: drainer.go Drain (per-mailbox error isolation)
func TestDrainer_MultipleMailboxesIndependent(t *testing.T) {
	store := task.New(t.TempDir())
	badFT := newFakeTransport()
	badFT.failRead = true
	goodFT := newFakeTransport(Message{UID: 1, From: "a@x.com", Subject: "Hello"})
	provider := MailboxProviderFunc(func() []Mailbox {
		return []Mailbox{
			{AgentID: "failing-agent", WorkspaceID: "ws-bad", Transport: badFT},
			{AgentID: "good-agent", WorkspaceID: "ws-good", Transport: goodFT},
		}
	})
	d := NewDrainer(store, provider, 0)
	created := d.Drain(context.Background())
	if created != 1 {
		t.Fatalf("expected 1 task from the good mailbox, got %d", created)
	}
	tasks, _ := store.List(task.Filter{AgentID: "good-agent"})
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task for good-agent, got %d", len(tasks))
	}
}

// TestDrainer_MaxPerMailboxRespected verifies the per-mailbox limit applies.
// Traces to: drainer.go NewDrainer + drainMailbox (maxPerMb passed to ReadInbox)
func TestDrainer_MaxPerMailboxRespected(t *testing.T) {
	store := task.New(t.TempDir())
	ft := newFakeTransport(
		Message{UID: 1, From: "a@x.com", Subject: "A"},
		Message{UID: 2, From: "b@x.com", Subject: "B"},
		Message{UID: 3, From: "c@x.com", Subject: "C"},
	)
	provider := MailboxProviderFunc(func() []Mailbox {
		return []Mailbox{{AgentID: "mia", WorkspaceID: "ws1", Transport: ft}}
	})
	// Limit to 2 per mailbox.
	d := NewDrainer(store, provider, 2)
	created := d.Drain(context.Background())
	if created != 2 {
		t.Fatalf("expected max 2 tasks from 3 messages with maxPerMailbox=2, got %d", created)
	}
}

// TestDrainer_StoreCreateFailureSkipsMessage verifies that when the task store
// fails to create a task, the message is left UNSEEN and the counter is not
// incremented, but processing continues for the next message.
// Traces to: drainer.go drainMailbox (store.Create failure branch, continue)
func TestDrainer_StoreCreateFailureSkipsMessage(t *testing.T) {
	// Force store.Create to fail deterministically for ANY user (including root,
	// which bypasses chmod-based read-only dirs): point the store at a path *under
	// a regular file*, so the store's MkdirAll-on-write fails with ENOTDIR.
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	store := task.New(filepath.Join(blocker, "tasks")) // parent is a file → writes fail

	ft := newFakeTransport(
		Message{UID: 1, From: "a@x.com", Subject: "Fails"},
		Message{UID: 2, From: "b@x.com", Subject: "Also fails"},
	)
	provider := MailboxProviderFunc(func() []Mailbox {
		return []Mailbox{{AgentID: "mia", WorkspaceID: "ws1", Transport: ft}}
	})
	d := NewDrainer(store, provider, 0)
	created := d.Drain(context.Background())
	// Both task creates fail → created should be 0.
	if created != 0 {
		t.Fatalf("expected 0 tasks created on store failure, got %d", created)
	}
	// Messages must NOT be marked seen (so they retry next tick).
	if ft.seen[1] || ft.seen[2] {
		t.Fatal("messages must remain unseen when task create fails")
	}
}
