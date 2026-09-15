// loop_idle.go: Idle timeout tickers

package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// RegisterIdleTicker stores a cancel function for the idle ticker of a session.
// Calling it again for the same session replaces the previous cancel without
// canceling it — use resetIdleTicker for the reset path.
func (al *AgentLoop) RegisterIdleTicker(sessionID string, cancel context.CancelFunc) {
	al.idleTickers.Store(sessionID, cancel)
}

// cancelIdleTicker cancels and removes the idle ticker for a session.
// No-op if no ticker is registered.
func (al *AgentLoop) cancelIdleTicker(sessionID string) {
	if v, ok := al.idleTickers.LoadAndDelete(sessionID); ok {
		cancel, ok := v.(context.CancelFunc)
		if !ok {
			logger.ErrorCF("agent", "idleTickers: invariant violated — unexpected value type",
				map[string]any{"session_id": sessionID, "got_type": fmt.Sprintf("%T", v)})
			return
		}
		cancel()
	}
}

// resetIdleTicker cancels any existing idle ticker for sessionID and starts a
// new one. On timeout, CloseSession is called with trigger="idle". The timer
// is driven by cfg.Agents.Defaults.GetIdleTimeoutMinutes(). If AutoRecapEnabled
// is false the ticker is still started but CloseSession will return immediately.
func (al *AgentLoop) resetIdleTicker(sessionID string) {
	if sessionID == "" {
		return
	}
	// Cancel the previous ticker (if any).
	al.cancelIdleTicker(sessionID)

	cfg := al.GetConfig()
	timeoutMinutes := cfg.Agents.Defaults.GetIdleTimeoutMinutes()
	ctx, cancel := context.WithCancel(context.Background())
	al.RegisterIdleTicker(sessionID, cancel)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.ErrorCF("agent", "Idle ticker goroutine panic recovered",
					map[string]any{"session_id": sessionID, "panic": fmt.Sprintf("%v", r)})
			}
		}()
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(timeoutMinutes) * time.Minute):
			al.CloseSession(sessionID, "idle")
		}
	}()
}

// fireIdleTimeout is a TEST SEAM. It triggers the same CloseSession("idle")
// code path that the timer in resetIdleTicker would trigger on expiry, without
// waiting for the real timer to fire. Calling this in production code is wrong
// — it exists solely to let tests exercise the idle→close pipeline in
// milliseconds rather than waiting a whole idle-timeout period (≥1 min).
// Production timing is completely unaffected: this function is never called by
// the runtime path, and adding it introduces no new goroutine or scheduling.
func (al *AgentLoop) fireIdleTimeout(sessionID string) {
	// Cancel the outstanding idle ticker first, mirroring what the timer goroutine
	// does implicitly when its context is the only reference to cancel.
	al.cancelIdleTicker(sessionID)
	al.CloseSession(sessionID, "idle")
}
