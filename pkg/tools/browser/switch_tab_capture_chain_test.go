package browser

import (
	"sync"
)

// Integration coverage for the SEAM the 2026-08-03 tab-switch bug lived in.
//
// Both halves of this chain were already well covered in isolation before the
// bug shipped — SwitchTab had TestSwitchTab_ChangesActiveIndex, and the
// recapture had TestCaptureSession_RecapturePropagatesToRelayAndIngest — but
// NOTHING exercised them together. The defect sat precisely between them: the
// switch updated activeIdx and fired a recapture, yet the recapture re-bound
// to the same tab because Chrome had never been told the active tab moved.
//
// Isolation tests cannot catch that class of bug by construction. These tests
// assert the whole path:
//
//	SwitchTab → activate in Chrome → notifyTabsChanged
//	          → LiveViewRegistry.handleTabsChanged → onTabsChanged
//	          → activeTabChanged → CaptureSession.Recapture
//
// so a future change that severs any link fails here even if every unit test
// on either side still passes.

// chainRecorder captures the ordered sequence of observable effects along the
// switch → capture chain.
type chainRecorder struct {
	mu     sync.Mutex
	events []string
}

func (c *chainRecorder) add(ev string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, ev)
}

func (c *chainRecorder) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.events))
	copy(out, c.events)
	return out
}

func (c *chainRecorder) count(ev string) int {
	n := 0
	for _, e := range c.snapshot() {
		if e == ev {
			n++
		}
	}
	return n
}
