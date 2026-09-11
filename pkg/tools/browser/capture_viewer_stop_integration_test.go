package browser

import "testing"

func TestCaptureViewerStopRetiresRequestListeners(t *testing.T) {
	cs := captureRequestFixture(t, &requestOfferProbe{})
	var retired int
	cs.viewerRequests = map[string]*captureViewerRequest{
		"pending": {pending: true, stop: func() bool { retired++; return true }},
		"active":  {stop: func() bool { retired++; return true }},
	}
	cs.Stop()
	cs.mu.Lock()
	remaining := len(cs.viewerRequests)
	cs.mu.Unlock()
	if retired != 2 || remaining != 0 {
		t.Fatalf("capture Stop retained request listeners: retired=%d remaining=%d", retired, remaining)
	}
	cs.Stop()
	if retired != 2 {
		t.Fatal("repeated Stop retired the same listeners twice")
	}
}
