package gateway

import "context"

type browserLatestKind uint8

const (
	browserLatestTabs browserLatestKind = iota
	browserLatestVideoState
	browserLatestKindCount
)

type browserLatestFrame = browserOutboundFrame

// sendLatestGen accepts a complete replaceable snapshot without waiting for
// transport or critical-queue capacity. The caller retains its observer's
// original attachment scope; looking up a replacement scope here is unsafe.
func (c *browserWSConn) sendLatestGen(kind browserLatestKind, frame any, attachment context.Context) bool {
	return c.sendLatestScopedGen(kind, frame, attachment, nil)
}

// takeLatest exposes encoded state to existing internal consumers. Transport
// writers use takeLatestFrame to retain the scope through final admission.
func (c *browserWSConn) takeLatest() ([]byte, bool) {
	frame, ok := c.takeLatestFrame()
	return frame.data, ok
}

// takeLatestFrame removes at most one valid snapshot. Alternating the starting kind
// prevents a stream of tab metadata from starving video/frame-health state.
// Its lock is released before the writer performs any network I/O.
func (c *browserWSConn) takeLatestFrame() (browserOutboundFrame, bool) {
	c.latestMu.Lock()
	defer c.latestMu.Unlock()
	select {
	case <-c.doneCh:
		c.latestSlots = [browserLatestKindCount]browserLatestFrame{}
		return browserOutboundFrame{}, false
	default:
	}
	for step := 0; step < len(c.latestSlots); step++ {
		index := (c.latestNext + step) % len(c.latestSlots)
		frame := c.latestSlots[index]
		if frame.data == nil {
			continue
		}
		c.latestSlots[index] = browserLatestFrame{}
		if !c.canSendFrame(frame) {
			continue
		}
		c.latestNext = (index + 1) % len(c.latestSlots)
		for _, pending := range c.latestSlots {
			if pending.data != nil {
				c.notifyLatestLocked()
				break
			}
		}
		return frame, true
	}
	return browserOutboundFrame{}, false
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
