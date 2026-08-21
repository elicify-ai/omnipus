package email

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// Drainer turns un-handled inbound mail into Board tasks (M11). Email is a TOOL
// surface: the owning agent works its inbox on heartbeat. Rather than push mail
// into a chat session, the drainer scans each enabled mailbox for unseen mail on
// a periodic tick and creates a visible Board task (status `next`, assigned to
// the mailbox-owning agent, in its workspace) for each message. The normal
// per-agent task drain then dispatches the task, and the agent uses its email
// tools (read_message, reply, …) to handle it. After a task is created the
// message is marked \Seen so it is not re-enqueued on the next tick. This is how
// email work stays VISIBLE with no inbox UI in 0.1.0.
type Drainer struct {
	store    *task.Store
	mailbox  MailboxProvider
	maxPerMb int
}

// Mailbox describes one configured, enabled mailbox the drainer should scan: its
// owning agent, surfacing workspace, and a Transport to read/mark mail.
type Mailbox struct {
	AgentID     string
	WorkspaceID string
	Transport   Transport
}

// MailboxProvider supplies the set of mailboxes to drain on each tick. It is a
// function-shaped interface so the caller (gateway) can build live transports
// from current config + resolved credentials without this package importing
// config or credentials.
type MailboxProvider interface {
	Mailboxes() []Mailbox
}

// MailboxProviderFunc adapts a plain func to MailboxProvider.
type MailboxProviderFunc func() []Mailbox

// Mailboxes implements MailboxProvider.
func (f MailboxProviderFunc) Mailboxes() []Mailbox { return f() }

// defaultMaxPerMailbox bounds how many unhandled messages are converted to tasks
// in a single drain pass per mailbox, so a large unread backlog cannot flood the
// Board in one tick.
const defaultMaxPerMailbox = 25

// NewDrainer constructs a Drainer. A nil store or nil provider makes Drain a
// no-op. maxPerMailbox <= 0 falls back to defaultMaxPerMailbox.
func NewDrainer(store *task.Store, provider MailboxProvider, maxPerMailbox int) *Drainer {
	if maxPerMailbox <= 0 {
		maxPerMailbox = defaultMaxPerMailbox
	}
	return &Drainer{store: store, mailbox: provider, maxPerMb: maxPerMailbox}
}

// Drain scans every provided mailbox for unseen mail and creates a Board task for
// each message, marking handled messages \Seen. It is safe to call repeatedly
// (idempotent across ticks via the \Seen flag). Returns the number of tasks
// created. Per-mailbox and per-message errors are logged and skipped so one bad
// mailbox cannot stall the others.
func (d *Drainer) Drain(ctx context.Context) int {
	if d == nil || d.store == nil || d.mailbox == nil {
		return 0
	}
	created := 0
	for _, mb := range d.mailbox.Mailboxes() {
		if mb.Transport == nil || mb.AgentID == "" || mb.WorkspaceID == "" {
			continue
		}
		created += d.drainMailbox(ctx, mb)
	}
	return created
}

// drainMailbox drains a single mailbox and returns the number of tasks created.
func (d *Drainer) drainMailbox(ctx context.Context, mb Mailbox) int {
	msgs, err := mb.Transport.ReadInbox(ctx, InboxOptions{Limit: d.maxPerMb, UnseenOnly: true})
	if err != nil {
		slog.Warn("email drainer: read inbox failed", "agent_id", mb.AgentID, "error", err)
		return 0
	}
	created := 0
	for i := range msgs {
		msg := msgs[i]
		t := mailToTask(mb, msg)
		if cErr := d.store.Create(t); cErr != nil {
			slog.Warn("email drainer: create board task failed",
				"agent_id", mb.AgentID, "uid", msg.UID, "error", cErr)
			// Leave the message UNSEEN so it is retried next tick rather than lost.
			continue
		}
		// Mark the source message \Seen only after the task is durably created, so a
		// create failure never silently drops the mail.
		if sErr := mb.Transport.MarkSeen(ctx, msg.UID); sErr != nil {
			slog.Warn("email drainer: mark seen failed (task created; may re-enqueue next tick)",
				"agent_id", mb.AgentID, "uid", msg.UID, "task_id", t.ID, "error", sErr)
		}
		created++
	}
	if created > 0 {
		slog.Info("email drainer: enqueued board tasks from unhandled mail",
			"agent_id", mb.AgentID, "workspace_id", mb.WorkspaceID, "count", created)
	}
	return created
}

// mailToTask builds a Board task from an inbound message. The task is assigned to
// the mailbox-owning agent, lands in `next` (dispatchable), and carries the email
// uid in its prompt so the agent can read/reply with its email tools.
func mailToTask(mb Mailbox, msg Message) *task.Task {
	subject := strings.TrimSpace(msg.Subject)
	if subject == "" {
		subject = "(no subject)"
	}
	title := "Email: " + subject
	if len(title) > 200 {
		title = title[:200]
	}

	var sb strings.Builder
	sb.WriteString("Handle an incoming email in your mailbox.\n\n")
	fmt.Fprintf(&sb, "From: %s\n", firstNonEmpty(msg.FromName, msg.From))
	if msg.From != "" && msg.From != msg.FromName {
		fmt.Fprintf(&sb, "Address: %s\n", msg.From)
	}
	fmt.Fprintf(&sb, "Subject: %s\n", subject)
	if msg.Date != "" {
		fmt.Fprintf(&sb, "Date: %s\n", msg.Date)
	}
	fmt.Fprintf(&sb, "Message uid: %d\n\n", msg.UID)
	sb.WriteString("Use read_message with this uid to read the full body, then decide how to handle it. ")
	sb.WriteString("Reply with the reply tool if a response is warranted, or take any other appropriate action. ")
	sb.WriteString("Mark this task done when the email has been handled.")

	return &task.Task{
		Title:       title,
		Prompt:      sb.String(),
		Action:      task.ActionLLM,
		Status:      task.StatusNext,
		AgentID:     mb.AgentID,
		CreatedBy:   mb.AgentID,
		WorkspaceID: mb.WorkspaceID,
		Priority:    3,
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
