package email

import (
	"context"
	"log/slog"
	"time"
)

// MailboxWatcherSet runs one watcher cycle across every configured mailbox.
// It is the adapter the heartbeat MailWatchService drives; heartbeat sees the
// CycleAll(ctx) method, never this package's internals.
type MailboxWatcherSet struct {
	provider MailboxProvider
	stateDir string
}

// NewMailboxWatcherSet builds the set over a live mailbox provider. The
// provider is consulted on every cycle, so mailboxes added, changed or
// removed at runtime are picked up without a restart.
func NewMailboxWatcherSet(provider MailboxProvider, stateDir string) *MailboxWatcherSet {
	return &MailboxWatcherSet{provider: provider, stateDir: stateDir}
}

// CycleAll runs one cycle for every provided mailbox, skipping a mailbox that
// is still inside its backoff window (MC-33: no automatic dial while backing
// off). Per-mailbox errors are logged; one bad mailbox never stalls another.
// It never mutates flags, never creates tasks, never starts a turn
// (D20/D27/FR-023).
func (s *MailboxWatcherSet) CycleAll(ctx context.Context) {
	if s == nil || s.provider == nil {
		return
	}
	for _, mb := range s.provider.Mailboxes() {
		if mb.Transport == nil || mb.AgentID == "" || mb.WorkspaceID == "" {
			continue
		}
		w, err := NewWatcher(WatcherConfig{
			AgentID:     mb.AgentID,
			WorkspaceID: mb.WorkspaceID,
			Transport:   mb.Transport,
			StateDir:    s.stateDir,
		})
		if err != nil {
			slog.Warn("email watcher: bad mailbox config", "agent_id", mb.AgentID, "error", err)
			continue
		}
		if err := w.cycleIfDue(ctx, time.Now()); err != nil {
			slog.Warn("email watcher: cycle failed", "agent_id", mb.AgentID, "error", err)
		}
	}
}
