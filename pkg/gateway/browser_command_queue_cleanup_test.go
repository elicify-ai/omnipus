package gateway

import (
	"testing"
	"time"
)

// A negotiation failure can reach reset cleanup before a transport is attached.
// That path must close the local connection state without dereferencing the
// absent WebSocket.
func TestBrowserFailInputAllowsUnattachedTransport(t *testing.T) {
	wc := &browserWSConn{sendCh: make(chan browserOutboundFrame, 1), doneCh: make(chan struct{})}
	state := &browserConnState{}

	failBrowserInput(wc, state, "viewer", "test reset")

	select {
	case <-wc.doneCh:
	case <-time.After(time.Second):
		t.Fatal("connection-local cleanup did not run")
	}
	state.commands.mu.Lock()
	closed := state.commands.closed
	state.commands.mu.Unlock()
	if !closed {
		t.Fatal("command queue did not close")
	}
}
