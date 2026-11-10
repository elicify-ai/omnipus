// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package heartbeat

import (
	"context"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// defaultMailWatchInterval is the polling cadence for the new-mail watcher.
const defaultMailWatchInterval = time.Minute

// MailWatcher runs one watcher cycle for the configured mailboxes. Implemented
// by email.CycleAll through a small adapter; defined here as an interface to
// avoid importing pkg/email into the heartbeat package's public surface.
type MailWatcher interface {
	// CycleAll runs one watcher pass across the configured mailboxes. It never
	// mutates flags, never creates tasks and never starts an agent turn.
	CycleAll(ctx context.Context)
}

// MailWatchService periodically cycles the per-mailbox new-mail watchers
// (D20/#631 — the replacement for the deleted MailboxDrainService). The
// watcher itself owns per-mailbox backoff; the service only supplies the
// cadence and a first pass shortly after start.
type MailWatchService struct {
	watcher  MailWatcher
	interval time.Duration
	mu       sync.Mutex
	stopChan chan struct{}
	// done closes when the loop goroutine has exited; Stop waits on it (see
	// pollerStopJoinBudget).
	done chan struct{}
}

// NewMailWatchService creates a watch service cycling watcher every interval.
// A non-positive interval falls back to defaultMailWatchInterval. A nil
// watcher makes Start a no-op.
func NewMailWatchService(watcher MailWatcher, interval time.Duration) *MailWatchService {
	if interval <= 0 {
		interval = defaultMailWatchInterval
	}
	return &MailWatchService{watcher: watcher, interval: interval}
}

// Start begins the watch loop. Idempotent; a nil watcher is a no-op.
func (s *MailWatchService) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.watcher == nil {
		logger.InfoC("heartbeat", "Mail-watch service: no mail watcher — mail watch disabled")
		return
	}
	if s.stopChan != nil {
		return
	}

	s.stopChan = make(chan struct{})
	s.done = make(chan struct{})
	go s.runLoop(s.stopChan, s.done)

	logger.InfoCF("heartbeat", "Mail-watch service started (new-mail watcher)", map[string]any{
		"interval_seconds": s.interval.Seconds(),
	})
}

// Stop halts the watch loop. Idempotent. It returns
// ErrPollerCycleStillRunning (wrapped) when a cycle in flight outlives the
// bounded wait; nil otherwise.
func (s *MailWatchService) Stop() error {
	s.mu.Lock()
	if s.stopChan == nil {
		s.mu.Unlock()
		return nil
	}
	close(s.stopChan)
	s.stopChan = nil
	done := s.done
	s.done = nil
	s.mu.Unlock()

	// Wait for a cycle already in flight to finish, so it cannot keep doing
	// mailbox I/O or writing mailbox state after Stop returns (failed-boot
	// teardown, shutdown and every config reload call Stop). Never cancelled
	// mid-cycle; the wait is bounded and only happens when a cycle is
	// actually running.
	return joinPollerLoop(done, "mail-watch", "its mail check may still talk to the mail server and save mailbox state")
}

// IsRunning reports whether the watch loop is active.
func (s *MailWatchService) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopChan != nil
}

func (s *MailWatchService) runLoop(stopChan, done chan struct{}) {
	defer close(done)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	// Cycle once shortly after start so mail that arrived before boot is
	// reflected in the badge without waiting a full interval. It runs on
	// this goroutine, so Stop joins it like every later pass.
	first := time.NewTimer(time.Second)
	defer first.Stop()

	for {
		select {
		case <-stopChan:
			return
		case <-first.C:
			s.watcher.CycleAll(context.Background())
		case <-ticker.C:
			s.watcher.CycleAll(context.Background())
		}
	}
}
