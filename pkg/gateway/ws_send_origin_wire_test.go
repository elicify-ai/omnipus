package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// soMalformedOriginFrames preserves the original six P1a equivalence classes
// from spec r9 §4.3.2 M-1/M-2. The gateway test and live wire control use the
// SAME frame bytes: a valid JSON object with a deliberately invalid origin,
// never a malformed outer frame or a JSON/base64 string.
func soMalformedOriginFrames(sid string) []struct {
	name string
	raw  []byte
} {
	frames := make([]struct {
		name string
		raw  []byte
	}, 0, 6) // the six P1a equivalence classes below
	for _, row := range []struct{ name, origin string }{
		{"null origin", `null`},
		{"unknown kind", `{"kind":"room","session_id":"` + sid + `"}`},
		{"both fields", `{"kind":"chat","session_id":"` + sid + `","new_chat_key":"` + soKeyA + `"}`},
		{"short key", `{"kind":"new_chat","new_chat_key":"short"}`},
		{"extra property", `{"kind":"chat","session_id":"` + sid + `","workspace_id":"w"}`},
		{"key on chat kind", `{"kind":"chat","new_chat_key":"` + soKeyA + `"}`},
	} {
		frames = append(frames, struct {
			name string
			raw  []byte
		}{row.name, []byte(`{"type":"message","agent_id":"mia","session_id":"` + sid +
			`","client_message_id":"so-malformed-1","origin":` + row.origin + `,"content":"x"}`)})
	}
	return frames
}

// TestSendOriginRawFramesOnWire is a live-positive instrument control, not a
// mock of the gateway. An independent real WebSocket receiver captures what
// soSend actually transmits, using the gateway test's exact raw-frame dataset.
// JSON-marshalling []byte again turns every object into a base64 JSON string;
// exact byte equality here must catch that before a refusal result is trusted.
func TestSendOriginRawFramesOnWire(t *testing.T) {
	for _, row := range soMalformedOriginFrames("so-raw-wire-chat") {
		t.Run(row.name, func(t *testing.T) {
			require.True(t, json.Valid(row.raw), "instrument: outer frame must be valid JSON")
			type capture struct {
				messageType int
				raw         []byte
				err         error
			}
			received := make(chan capture, 1)
			upgrader := websocket.Upgrader{}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					received <- capture{err: err}
					return
				}
				defer conn.Close()
				if deadlineErr := conn.SetReadDeadline(time.Now().Add(lifecycleEW)); deadlineErr != nil {
					received <- capture{err: deadlineErr}
					return
				}
				messageType, raw, err := conn.ReadMessage()
				received <- capture{messageType: messageType, raw: raw, err: err}
			}))
			t.Cleanup(srv.Close)
			conn, resp, err := websocket.DefaultDialer.Dial("ws"+trimHTTPForWS(srv.URL), nil)
			if resp != nil {
				require.NoError(t, resp.Body.Close())
			}
			require.NoError(t, err, "instrument: real WebSocket must upgrade")
			t.Cleanup(func() { _ = conn.Close() })

			soSend(t, conn, row.raw)
			select {
			case got := <-received:
				require.NoError(t, got.err, "instrument: receiver must capture one frame")
				require.Equal(t, websocket.TextMessage, got.messageType, "instrument: text opcode")
				require.Equal(t, string(row.raw), string(got.raw),
					"instrument: raw malformed-origin frame must arrive verbatim, not JSON/base64 encoded")
				t.Logf("instrument: exact raw frame received on wire: %s", got.raw)
			case <-time.After(lifecycleEW):
				t.Fatal("instrument: real WebSocket receiver did not observe the frame")
			}
		})
	}
}
