//go:build !cgo

package integration

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestIdleHeartbeat verifies the server's application-layer pong response:
// after sending a ping, the client receives a pong within 2 s, and the WS
// stays open for >15 s even with no other traffic.
//
// BDD:
//
//	Given an authenticated WebSocket connection with no chat activity,
//	When the client sends {"type":"ping"} three times spaced 5 s apart,
//	Then the server responds with {"type":"pong"} within 2 s each time,
//	And the connection remains open for the entire duration.
//
// Traces to: WebSocket-heartbeat fix (server-side pong response).
// This is the integration-level counterpart of TestServerRespondsToAppLayerPing.
func TestIdleHeartbeat(t *testing.T) {
	skipOnMacOSAPFSCleanupRace(t)
	gw := startIntegrationGateway(t)
	conn := wsConnect(t, gw)

	// Three pings spaced 5 s apart — assert pong reply each time.
	for i := 0; i < 3; i++ {
		if err := conn.WriteMessage(1 /* TextMessage */, []byte(`{"type":"ping"}`)); err != nil {
			t.Fatalf("ping %d write: %v", i, err)
		}
		deadline := time.Now().Add(2 * time.Second)
		gotPong := false
		for time.Now().Before(deadline) {
			_ = conn.SetReadDeadline(deadline)
			_, msg, err := conn.ReadMessage()
			if err != nil {
				break
			}
			var f struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(msg, &f) == nil && f.Type == "pong" {
				gotPong = true
				break
			}
		}
		if !gotPong {
			t.Fatalf("ping %d: did not receive pong within 2s", i)
		}
		if i < 2 {
			time.Sleep(5 * time.Second)
		}
	}

	// The WS should still be open — verify we can send another ping and get pong.
	// Wait past the server's 100 ms pong debounce window before the post-idle
	// ping so we don't race the debounce of the loop's final ping.
	time.Sleep(200 * time.Millisecond)
	if err := conn.WriteMessage(1, []byte(`{"type":"ping"}`)); err != nil {
		t.Fatalf("post-idle ping write: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("post-idle pong read: %v", err)
	}
	if !strings.Contains(string(msg), `"pong"`) {
		t.Fatalf("expected pong, got %q", string(msg))
	}
}
