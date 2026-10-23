package gateway

import (
	"context"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// Observe only the external Context boundary, like C1. This gate neither
// returns a fabricated read error nor replaces a session/gateway operation.
// The immediate historyReadEvent caller must be the page attempt itself,
// excluding recovery-anchor and body-I/O events nested deeper in the reader.
func r1CleanupAtPinnedCut() bool {
	var pcs [64]uintptr
	n := runtime.Callers(2, pcs[:])
	frames := runtime.CallersFrames(pcs[:n])
	for {
		frame, more := frames.Next()
		if strings.HasSuffix(frame.Function, "/pkg/session.historyReadEvent") && more {
			caller, _ := frames.Next()
			return strings.Contains(caller.Function, "/pkg/session.(*UnifiedStore).readChatHistoryPage")
		}
		if !more {
			return false
		}
	}
}

type r1CleanupContext struct {
	context.Context
	entered, release chan struct{}
	once, free       sync.Once
}

func (c *r1CleanupContext) Err() error {
	if err := c.Context.Err(); err != nil {
		return err
	}
	if r1CleanupAtPinnedCut() {
		c.once.Do(func() {
			close(c.entered)
			select {
			case <-c.release:
			case <-c.Context.Done():
			}
		})
	}
	return c.Context.Err()
}

func (c *r1CleanupContext) unblock() { c.free.Do(func() { close(c.release) }) }

type r1CleanupRead struct {
	ctx    *r1CleanupContext
	cancel context.CancelFunc
	done   chan error
	joined bool
}

func r1CleanupStart(t *testing.T, run func(context.Context) error) *r1CleanupRead {
	t.Helper()
	base, cancel := context.WithCancel(context.Background())
	observed := &r1CleanupContext{Context: base, entered: make(chan struct{}), release: make(chan struct{})}
	work := &r1CleanupRead{ctx: observed, cancel: cancel, done: make(chan error, 1)}
	go func() { work.done <- run(observed) }()
	t.Cleanup(func() {
		cancel()
		observed.unblock()
		if !work.joined {
			select {
			case <-work.done:
				work.joined = true
			case <-time.After(30 * time.Second):
				t.Error("R1-1 actual owned request failed to join after cancellation/release")
			}
		}
	})
	select {
	case <-observed.entered:
	case err := <-work.done:
		work.joined = true
		t.Fatalf("R1-1 prerequisite: real read returned before post-pin cut observation: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("R1-1 actual post-pin cut did not rendezvous; no timing-based substitute")
	}
	return work
}

func r1CleanupJoin(t *testing.T, work *r1CleanupRead) error {
	t.Helper()
	work.ctx.unblock()
	select {
	case err := <-work.done:
		work.joined = true
		return err
	case <-time.After(30 * time.Second):
		t.Fatal("R1-1 actual reader/REST handler did not join after cut release")
		return nil
	}
}
