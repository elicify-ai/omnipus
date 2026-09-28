package agent

import (
	"context"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// activeRequestTracker closes intake before a shutdown waiter observes the
// active count. Its zero value is ready for use so focused tests that build an
// AgentLoop literal retain the production shutdown semantics. The tracker is
// terminal once intake closes; construct a new AgentLoop to restart. Every
// done call must be paired with a successful, begin-gated admission, making
// done's underflow panic unreachable unless that pairing invariant is broken.
type activeRequestTracker struct {
	mu          sync.Mutex
	active      int
	closing     bool
	waitStarted bool
	drained     chan struct{}
	drainClosed bool
}

func (t *activeRequestTracker) begin() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closing {
		return false
	}
	t.active++
	return true
}

func (t *activeRequestTracker) done() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.active == 0 {
		panic("agent: active request tracker underflow")
	}
	t.active--
	if t.closing && t.active == 0 {
		t.closeDrainedLocked()
	}
}

func (t *activeRequestTracker) stopIntake() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stopIntakeLocked()
}

// startWait atomically closes intake and registers that a drain was attempted.
// alreadyWaiting lets Close avoid stacking its own budget after the gateway's
// longer bounded wait has already consumed the shutdown allowance.
func (t *activeRequestTracker) startWait() (done <-chan struct{}, alreadyWaiting bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	alreadyWaiting = t.waitStarted
	t.waitStarted = true
	t.stopIntakeLocked()
	return t.drained, alreadyWaiting
}

func (t *activeRequestTracker) stopIntakeLocked() {
	if t.drained == nil {
		t.drained = make(chan struct{})
	}
	t.closing = true
	if t.active == 0 {
		t.closeDrainedLocked()
	}
}

func (t *activeRequestTracker) closeDrainedLocked() {
	if t.drainClosed {
		return
	}
	if t.drained == nil {
		t.drained = make(chan struct{})
	}
	close(t.drained)
	t.drainClosed = true
}

func (al *AgentLoop) beginActiveRequest() bool {
	return al.activeRequests.begin()
}

func (al *AgentLoop) endActiveRequest() {
	al.activeRequests.done()
}

func (al *AgentLoop) stopActiveRequestIntake() {
	al.activeRequests.stopIntake()
}

// WaitForActiveRequestsContext closes intake and waits until existing work
// drains or ctx ends. It creates no helper goroutine, so a timeout cannot leave
// an abandoned waiter behind.
func (al *AgentLoop) WaitForActiveRequestsContext(ctx context.Context) bool {
	done, _ := al.activeRequests.startWait()
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	}
}

func (al *AgentLoop) launchSystemMessage(runCtx context.Context, msg bus.InboundMessage) {
	if !al.beginActiveRequest() {
		sessionID := msg.AsyncTranscriptSessionID
		if sessionID == "" {
			sessionID = msg.SessionID
		}
		logger.WarnCF("agent", "active request admission refused after intake closed", map[string]any{
			"site":        "launchSystemMessage",
			"channel":     msg.Channel,
			"chat_id":     msg.ChatID,
			"session_id":  sessionID,
			"session_key": msg.SessionKey,
			"agent_id":    msg.AsyncOriginAgentID,
		})
		return
	}
	go func() {
		defer al.endActiveRequest()
		defer func() {
			if r := recover(); r != nil {
				logger.ErrorCF("agent", "Panic in system-message goroutine", map[string]any{
					"panic": r, "channel": msg.Channel, "chat_id": msg.ChatID,
				})
			}
		}()
		if _, err := al.processSystemMessage(runCtx, msg); err != nil {
			logger.WarnCF("agent", "processSystemMessage returned error", map[string]any{
				"channel": msg.Channel, "chat_id": msg.ChatID, "error": err.Error(),
			})
		}
	}()
}
