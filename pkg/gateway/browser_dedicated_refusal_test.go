package gateway

import (
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/require"
)

// R3 retirement needs an explicit negative control acknowledgment after the
// local peer closes. Generic transport failure is not a retirement result.
// Counter expectations come from BrowserInputControlAckFrame's safe-integer
// range; malformed counters must never create an invalid outbound frame.
// The real dispatcher, schema validator and attachment fencing remain active.
// Fault targets: established-peer state instead of ack, current rather than
// requested control identity, and unchecked malformed counter reflection.
func TestDedicatedControlRefusalHasUnambiguousBoundedIdentity(t *testing.T) {
	const maximum = 9007199254740991
	for _, tc := range []struct {
		name        string
		epoch       int
		requested   *int
		wantAck     bool
		wantControl int
	}{
		{"established next control", 4, payloadInt(4), true, 4},
		{"older request is not relabeled", 4, payloadInt(2), true, 2},
		{"zero", 4, payloadInt(0), true, 0},
		{"one", 4, payloadInt(1), true, 1},
		{"maximum minus one", 4, payloadInt(maximum - 1), true, maximum - 1},
		{"maximum", 4, payloadInt(maximum), true, maximum},
		{"negative established", 4, payloadInt(-1), false, 3},
		{"overflow established", 4, payloadInt(maximum + 1), false, 3},
		{"missing established", 4, nil, false, 3},
		{"initial valid", 0, payloadInt(1), true, 1},
		{"initial negative", 0, payloadInt(-1), true, 0},
		{"initial overflow", 0, payloadInt(maximum + 1), true, 0},
		{"initial missing", 0, nil, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newHandlerContextFixture(t, false)
			f.state.setDedicatedInput(true)
			t.Cleanup(func() { f.state.setDedicatedInput(false) })
			d := f.state.dedicatedInput()
			d.mu.Lock()
			d.epoch = tc.epoch
			if tc.epoch != 0 {
				d.offer, d.control, d.applied = 6, 3, 3
			}
			d.mu.Unlock()
			frame := map[string]any{"type": "browser_control", "action": "invalid-action", "input_epoch": tc.epoch}
			if tc.requested != nil {
				frame["control_epoch"] = *tc.requested
			}
			raw, err := json.Marshal(frame)
			require.NoError(t, err)
			f.handler.dispatchDedicatedControl(f.conn, f.state, "fixture-viewer", "user", raw, "browser_control", f.cfg)
			select {
			case envelope := <-f.conn.sendCh:
				require.True(t, f.conn.canSendFrame(envelope))
				if tc.wantAck {
					var ack generated.BrowserInputControlAckFrame
					require.NoError(t, json.Unmarshal(envelope.data, &ack))
					reason := "Invalid browser control message."
					require.Equal(t, generated.BrowserInputControlAckFrame{Type: "browser_input_control_ack", SessionId: "chat", InputEpoch: tc.epoch, ControlEpoch: tc.wantControl, Ok: false, Reason: &reason}, ack)
					message, unavailable := ValidateInboundFrameJSON("BrowserInputControlAckFrame", envelope.data)
					require.False(t, unavailable)
					require.Empty(t, message)
				} else {
					var state generated.BrowserInputStateFrame
					require.NoError(t, json.Unmarshal(envelope.data, &state))
					reason := "Invalid browser control message."
					require.Equal(t, generated.BrowserInputStateFrame{Type: "browser_input_state", SessionId: "chat", InputEpoch: 4, OfferId: 6, ControlEpoch: 3, State: "failed", Reason: &reason}, state)
					message, unavailable := ValidateInboundFrameJSON("BrowserInputStateFrame", envelope.data)
					require.False(t, unavailable)
					require.Empty(t, message)
				}
				// A queued refusal must never be delivered to a replacement owner.
				d.mu.Lock()
				d.epoch++
				d.mu.Unlock()
				require.False(t, f.conn.canSendFrame(envelope))
			default:
				t.Fatal("control refusal produced no response")
			}
			require.Empty(t, f.conn.sendCh, "one refusal must produce exactly one response")
		})
	}
}
