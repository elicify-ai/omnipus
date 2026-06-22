// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package heartbeat

import (
	"context"
	"sync"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/logger"
)

// defaultMailboxDrainInterval is the polling cadence for unhandled inbound mail.
// It mirrors the queued-task drain cadence: short relative to the heartbeat
// interval so new mail becomes a visible Board task promptly.
const defaultMailboxDrainInterval = time.Minute

// MailScanner drains unhandled mail into Board tasks on each tick. Implemented by
// email.Drainer; defined here as an interface to avoid importing pkg/email into
// the heartbeat package's public surface (and any potential cycle).
type MailScanner interface {
	// Drain scans configured mailboxes for unhandled mail, creates Board tasks,
	// and returns the number of tasks created.
	Drain(ctx context.Context) int
}

// MailboxDrainService periodically drains unhandled inbound mail into Board tasks
// (M11). It is decoupled from the HEARTBEAT.md prompt path (exactly like
// TaskDrainService) so email work surfaces on the Board regardless of which
// heartbeat path is active. A nil scanner makes Start a no-op.
type MailboxDrainService struct {
	scanner  MailScanner
	interval time.Duration
	mu       sync.Mutex
	stopChan chan struct{}
}

// NewMailboxDrainService creates a drain service polling scanner every interval.
// A non-positive interval falls back to defaultMailboxDrainInterval. A nil
// scanner makes Start a no-op.
func NewMailboxDrainService(scanner MailScanner, interval time.Duration) *MailboxDrainService {
	if interval <= 0 {
		interval = defaultMailboxDrainInterval
	}
	return &MailboxDrainService{scanner: scanner, interval: interval}
}

// Start begins the drain loop. Idempotent; a nil scanner is a no-op.
func (ds *MailboxDrainService) Start() {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	if ds.scanner == nil {
		logger.InfoC("heartbeat", "Mailbox-drain service: no mail scanner — email drain disabled")
		return
	}
	if ds.stopChan != nil {
		return
	}

	ds.stopChan = make(chan struct{})
	go ds.runLoop(ds.stopChan)

	logger.InfoCF("heartbeat", "Mailbox-drain service started (unhandled mail → Board tasks)", map[string]any{
		"interval_seconds": ds.interval.Seconds(),
	})
}

// Stop halts the drain loop. Idempotent.
func (ds *MailboxDrainService) Stop() {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	if ds.stopChan == nil {
		return
	}
	close(ds.stopChan)
	ds.stopChan = nil
}

// IsRunning reports whether the drain loop is active.
func (ds *MailboxDrainService) IsRunning() bool {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	return ds.stopChan != nil
}

func (ds *MailboxDrainService) runLoop(stopChan chan struct{}) {
	ticker := time.NewTicker(ds.interval)
	defer ticker.Stop()

	// Drain once shortly after start so mail that arrived before boot surfaces
	// without waiting a full interval.
	time.AfterFunc(time.Second, func() {
		select {
		case <-stopChan:
			return
		default:
			ds.scanner.Drain(context.Background())
		}
	})

	for {
		select {
		case <-stopChan:
			return
		case <-ticker.C:
			ds.scanner.Drain(context.Background())
		}
	}
}
