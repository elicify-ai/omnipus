package email

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// MailboxWatcherSet runs one watcher cycle across every configured mailbox.
// It is the adapter the heartbeat MailWatchService drives; heartbeat sees the
// CycleAll(ctx) method, never this package's internals.
type MailboxWatcherSet struct {
	provider MailboxProvider
	stateDir string
	budget   *MailBudget

	// First-cycle stagger (MC-33): the set's first CycleAll anchors the
	// stagger clock; mailbox i's FIRST cycle waits WatcherInitialOffset(i)
	// (i × the cycle interval) from that anchor, so same-host mailboxes'
	// first cycles spread across consecutive cycle slots instead of firing
	// in lockstep at boot. Once the offset has elapsed it is a no-op — the
	// mailbox cycles on the normal cadence.
	mu    sync.Mutex
	start time.Time
}

// NewMailboxWatcherSet builds the set over a live mailbox provider. The
// provider is consulted on every cycle, so mailboxes added, changed or
// removed at runtime are picked up without a restart. The budget (A8) may be
// nil — a nil budget means ungated cycles, which only tests use.
func NewMailboxWatcherSet(provider MailboxProvider, stateDir string, budget *MailBudget) *MailboxWatcherSet {
	return &MailboxWatcherSet{provider: provider, stateDir: stateDir, budget: budget}
}

// CycleAll runs one cycle for every provided mailbox, skipping a mailbox that
// is still inside its backoff window (MC-33: no automatic dial while backing
// off) and deferring a mailbox whose first-cycle stagger slot has not arrived
// yet (MC-33: WatcherInitialOffset spreads same-host mailboxes' first cycles
// across cycle slots instead of firing in lockstep at boot). Per-mailbox
// errors are logged; one bad mailbox never stalls another. It never mutates
// flags, never creates tasks, never starts a turn (D20/D27/FR-023).
func (s *MailboxWatcherSet) CycleAll(ctx context.Context) {
	if s == nil || s.provider == nil {
		return
	}
	start := s.cycleStart(time.Now())
	now := time.Now()
	mbs := s.provider.Mailboxes()
	for i, mb := range mbs {
		if mb.Transport == nil || mb.AgentID == "" || mb.WorkspaceID == "" {
			slog.Warn("email watcher: skipping malformed mailbox (missing agent, workspace or transport)", "agent_id", mb.AgentID, "workspace_id", mb.WorkspaceID)
			continue
		}
		if off := WatcherInitialOffset(i); off > 0 && start.Add(off).After(now) {
			slog.Debug("email watcher: first cycle deferred to its stagger slot", "agent_id", mb.AgentID, "workspace_id", mb.WorkspaceID, "offset", off)
			continue
		}
		w, err := NewWatcher(WatcherConfig{
			AgentID:     mb.AgentID,
			WorkspaceID: mb.WorkspaceID,
			Transport:   mb.Transport,
			StateDir:    s.stateDir,
			Budget:      s.budget,
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

// cycleStart returns the stagger anchor, setting it on the first CycleAll
// call. The anchor is set lazily (not at construction) so the offsets count
// from the first actual cycle pass, not from when the set was built.
func (s *MailboxWatcherSet) cycleStart(now time.Time) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.start.IsZero() {
		s.start = now
	}
	return s.start
}
