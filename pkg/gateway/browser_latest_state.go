package gateway

import (
	"context"
	"encoding/json"
	"log/slog"
)

type browserLatestKind uint8

const (
	browserLatestTabs browserLatestKind = iota
	browserLatestVideoState
	browserLatestKindCount
)

type browserLatestFrame struct { // not-wire-format: pending encoded connection state.
	data       []byte
	attachment context.Context
}

// sendLatestGen accepts a complete replaceable snapshot without waiting for
// transport or critical-queue capacity. The caller retains its observer's
// original attachment scope; looking up a replacement scope here is unsafe.
func (c *browserWSConn) sendLatestGen(kind browserLatestKind, frame any, attachment context.Context) bool {
	if kind >= browserLatestKindCount {
		slog.Error("browser-ws: unknown latest-state kind", "kind", kind)
		return false
	}
	if attachment.Err() != nil {
		return false
	}
	select {
	case <-c.doneCh:
		return false
	default:
	}
	data, err := json.Marshal(frame)
	if err != nil {
		slog.Error("browser-ws: marshal latest state failed", "error", err)
		return false
	}
	c.latestMu.Lock()
	defer c.latestMu.Unlock()
	// Recheck after encoding: cancellation may have happened while marshaling.
	if attachment.Err() != nil {
		return false
	}
	select {
	case <-c.doneCh:
		return false
	default:
	}
	c.latestSlots[kind] = browserLatestFrame{data: data, attachment: attachment}
	c.notifyLatestLocked()
	return true
}

// takeLatest removes at most one valid snapshot. Alternating the starting kind
// prevents a stream of tab metadata from starving video/frame-health state.
// Its lock is released before the writer performs any network I/O.
func (c *browserWSConn) takeLatest() ([]byte, bool) {
	c.latestMu.Lock()
	defer c.latestMu.Unlock()
	select {
	case <-c.doneCh:
		c.latestSlots = [browserLatestKindCount]browserLatestFrame{}
		return nil, false
	default:
	}
	for step := 0; step < len(c.latestSlots); step++ {
		index := (c.latestNext + step) % len(c.latestSlots)
		frame := c.latestSlots[index]
		if frame.data == nil {
			continue
		}
		c.latestSlots[index] = browserLatestFrame{}
		if frame.attachment.Err() != nil {
			continue
		}
		c.latestNext = (index + 1) % len(c.latestSlots)
		for _, pending := range c.latestSlots {
			if pending.data != nil {
				c.notifyLatestLocked()
				break
			}
		}
		return frame.data, true
	}
	return nil, false
}

func (c *browserWSConn) latestWakeLocked() chan struct{} {
	if c.latestWakeCh == nil {
		c.latestWakeCh = make(chan struct{}, 1)
	}
	return c.latestWakeCh
}

func (c *browserWSConn) notifyLatestLocked() {
	select {
	case c.latestWakeLocked() <- struct{}{}:
	default:
	}
}

func (c *browserWSConn) latestWake() <-chan struct{} {
	c.latestMu.Lock()
	defer c.latestMu.Unlock()
	return c.latestWakeLocked()
}
