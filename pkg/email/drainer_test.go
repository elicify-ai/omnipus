package email

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/task"
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
func (f *fakeTransport) ReadMessage(context.Context, uint32) (*Message, error)  { return nil, nil }
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
