package gateway

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"
)

// browserOutboundFrame retains source ownership until the single writer admits
// the transport write. A nil source is reserved for connection-wide messages.
// current must perform only a brief check of the originally captured state,
// without I/O or queue sends.
type browserOutboundFrame struct { // not-wire-format: internal write admission.
	data    []byte
	source  context.Context
	current func() bool
}

func (c *browserWSConn) canSendSource(source context.Context) bool {
	select {
	case <-c.doneCh:
		return false
	default:
	}
	return source == nil || source.Err() == nil
}

func (c *browserWSConn) canSendFrame(frame browserOutboundFrame) bool {
	if !c.canSendSource(frame.source) {
		return false
	}
	if frame.current != nil && !frame.current() {
		return false
	}
	// A current-state check may wait for its state lock while this attachment
	// or connection retires. It must not authorize that retired source.
	return c.canSendSource(frame.source)
}

func (c *browserWSConn) enqueueCritical(frame browserOutboundFrame, dropCtx string) bool {
	if !c.canSendFrame(frame) {
		return false
	}
	var canceled <-chan struct{}
	if frame.source != nil {
		canceled = frame.source.Done()
	}
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case c.sendCh <- frame:
		// Cancellation can race available capacity. The writer retains the same
		// envelope and checks it again, so such a queued frame cannot gain a new scope.
		return c.canSendFrame(frame)
	case <-canceled:
	case <-c.doneCh:
	case <-timer.C:
		slog.Warn("browser-ws: send channel full, dropping critical frame", "context", dropCtx)
	}
	return false
}

func (c *browserWSConn) sendCriticalScopedGen(frame any, dropCtx string, source context.Context, current func() bool) bool {
	pending := browserOutboundFrame{source: source, current: current}
	if source == nil || !c.canSendFrame(pending) {
		return false
	}
	data, err := json.Marshal(frame)
	if err != nil {
		slog.Error("browser-ws: marshal scoped frame failed", "error", err)
		return false
	}
	pending.data = data
	return c.enqueueCritical(pending, dropCtx)
}

func (c *browserWSConn) sendLatestScopedGen(kind browserLatestKind, frame any, source context.Context, current func() bool) bool {
	if kind >= browserLatestKindCount {
		slog.Error("browser-ws: unknown latest-state kind", "kind", kind)
		return false
	}
	pending := browserOutboundFrame{source: source, current: current}
	if source == nil || !c.canSendFrame(pending) {
		return false
	}
	data, err := json.Marshal(frame)
	if err != nil {
		slog.Error("browser-ws: marshal latest state failed", "error", err)
		return false
	}
	pending.data = data
	c.latestMu.Lock()
	defer c.latestMu.Unlock()
	// An old callback may have waited behind the newer state's enqueue.
	if !c.canSendFrame(pending) {
		return false
	}
	c.latestSlots[kind] = pending
	c.notifyLatestLocked()
	return true
}
