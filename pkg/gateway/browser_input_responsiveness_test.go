package gateway

import (
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// FR-011: browser command execution must not prevent the real socket reader
// from servicing its next frame. The existing handler boundary stands in for
// a blocked Chrome call; the socket, reader and dispatcher remain real.
func TestBrowserInputReaderResponsive(t *testing.T) {
	for _, tc := range []struct {
		name  string
		kind  browserConnWorkKind
		frame string
	}{
		{"input", workKindInput, `{"type":"browser_input","kind":"navigate","url":"https://example.com"}`},
		{"tab-action", workKindTabAction, `{"type":"browser_tab_action","action":"open"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := dialAuthedBrowserWS(t)
			entered, release := blockSlowHandler(t, tc.kind)
			require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(tc.frame)))
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("browser command never reached its execution boundary")
			}
			assertReadLoopStillReads(t, conn)
			release()
		})
	}
}
