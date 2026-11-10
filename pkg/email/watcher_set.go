package email

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// maxConcurrentWatcherCycles bounds one pass's parallelism (spec §4.10.1;
// OQ-4 recommendation A, fixed at implementation per the spec's owner):
// enough to keep many mailboxes' cadence honest under normal latency, small
// enough that a pathological server cannot park many goroutines. The
// per-mailbox single-flight guard below does the rest, and watcher
// acquisition is non-blocking throughout, so effective concurrency is also
// capped by availability.
const maxConcurrentWatcherCycles = 3

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
	// inFlight is the per-mailbox single-flight guard (spec §4.10.1): at
	// most one cycle per mailbox runs at a time — a mailbox whose previous
	// cycle is still running is not started again.
	inFlight map[string]bool
}

// NewMailboxWatcherSet builds the set over a live mailbox provider. The
// provider is consulted on every cycle, so mailboxes added, changed or
// removed at runtime are picked up without a restart. The budget (A8) may be
// nil — a nil budget means ungated cycles, which only tests use.
func NewMailboxWatcherSet(provider MailboxProvider, stateDir string, budget *MailBudget) *MailboxWatcherSet {
	return &MailboxWatcherSet{provider: provider, stateDir: stateDir, budget: budget, inFlight: map[string]bool{}}
}

// CycleAll runs one cycle for every due mailbox with bounded fair scheduling
// (spec §4.10.1): due mailboxes run in parallel up to
// maxConcurrentWatcherCycles, so one stalled mailbox no longer delays the
// others' due checks; a mailbox whose previous cycle is still running is not
// started again. The backoff gate (MC-33: no automatic dial while backing
// off) and the first-cycle stagger (MC-33: WatcherInitialOffset spreads
// same-host mailboxes' first cycles across cycle slots instead of firing in
// lockstep at boot) are preserved exactly. Per-mailbox errors are logged; a
// bad mailbox never stalls another. It never mutates flags, never creates
// tasks, never starts a turn (D20/D27/FR-023).
func (s *MailboxWatcherSet) CycleAll(ctx context.Context) {
	if s == nil || s.provider == nil {
		return
	}
	start := s.cycleStart(time.Now())
	now := time.Now()
	mbs := s.provider.Mailboxes()
	type dueMailbox struct {
		mb  Mailbox
		key string
	}
	var dueMbs []dueMailbox
	for i, mb := range mbs {
		if mb.Transport == nil || mb.AgentID == "" || mb.WorkspaceID == "" {
			slog.Warn("email watcher: skipping malformed mailbox (missing agent, workspace or transport)", "agent_id", mb.AgentID, "workspace_id", mb.WorkspaceID)
			continue
		}
		if off := WatcherInitialOffset(i); off > 0 && start.Add(off).After(now) {
			slog.Debug("email watcher: first cycle deferred to its stagger slot", "agent_id", mb.AgentID, "workspace_id", mb.WorkspaceID, "offset", off)
			continue
		}
		dueMbs = append(dueMbs, dueMailbox{mb: mb, key: mb.AgentID + "\x00" + mb.WorkspaceID})
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, maxConcurrentWatcherCycles)
	for _, d := range dueMbs {
		if !s.tryClaimCycle(d.key) {
			// The mailbox's previous cycle is still running: no second cycle
			// for that mailbox starts (spec §4.10.1; MC-W1-21).
			slog.Debug("email watcher: cycle already in flight; not starting a second", "agent_id", d.mb.AgentID, "workspace_id", d.mb.WorkspaceID)
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(d dueMailbox) {
			defer wg.Done()
			defer func() {
				<-sem
				s.releaseCycle(d.key)
			}()
			w, err := NewWatcher(WatcherConfig{
				AgentID:     d.mb.AgentID,
				WorkspaceID: d.mb.WorkspaceID,
				Transport:   d.mb.Transport,
				StateDir:    s.stateDir,
				Budget:      s.budget,
			})
			if err != nil {
				slog.Warn("email watcher: bad mailbox config", "agent_id", d.mb.AgentID, "error", err)
				return
			}
			if err := w.cycleIfDue(ctx, time.Now()); err != nil {
				slog.Warn("email watcher: cycle failed", "agent_id", d.mb.AgentID, "error", err)
			}
		}(d)
	}
	wg.Wait()
}

// tryClaimCycle claims the mailbox's single-flight slot for this pass.
func (s *MailboxWatcherSet) tryClaimCycle(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inFlight == nil {
		s.inFlight = map[string]bool{}
	}
	if s.inFlight[key] {
		return false
	}
	s.inFlight[key] = true
	return true
}

// releaseCycle frees the mailbox's single-flight slot.
func (s *MailboxWatcherSet) releaseCycle(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.inFlight, key)
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
