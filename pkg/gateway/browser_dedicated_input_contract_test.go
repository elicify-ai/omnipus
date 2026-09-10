package gateway

import (
	"encoding/json"
	"testing"
)

func TestDedicatedInputWireContracts(t *testing.T) {
	cases := []struct {
		name  string
		frame map[string]any
	}{
		{"BrowserInputOfferFrame", map[string]any{"type": "browser_input_offer", "agent_id": "agent", "session_id": "session", "offer_id": 1, "input_epoch": 1, "control_epoch": 0, "sdp": "offer"}},
		{"BrowserInputAnswerFrame", map[string]any{"type": "browser_input_answer", "session_id": "session", "offer_id": 1, "input_epoch": 1, "control_epoch": 0, "sdp": "answer"}},
		{"BrowserInputStateFrame", map[string]any{"type": "browser_input_state", "session_id": "session", "offer_id": 1, "input_epoch": 1, "control_epoch": 0, "state": "ready"}},
		{"BrowserInputControlAckFrame", map[string]any{"type": "browser_input_control_ack", "session_id": "session", "input_epoch": 1, "control_epoch": 1, "ok": true, "capture_id": "capture", "capture_generation": 2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			check := func(frame map[string]any, wantValid bool) {
				t.Helper()
				raw, err := json.Marshal(frame)
				if err != nil {
					t.Fatal(err)
				}
				message, serverError := ValidateInboundFrameJSON(tc.name, raw)
				if serverError {
					t.Fatalf("schema unavailable: %s", message)
				}
				if (message == "") != wantValid {
					t.Fatalf("valid=%v want=%v message=%s", message == "", wantValid, message)
				}
			}
			check(tc.frame, true)
			clone := func() map[string]any {
				m := map[string]any{}
				for k, v := range tc.frame {
					m[k] = v
				}
				return m
			}
			missing := clone()
			delete(missing, "input_epoch")
			check(missing, false)
			for _, value := range []any{-1, 9007199254740992.0, 1.5, "1", nil} {
				bad := clone()
				bad["input_epoch"] = value
				check(bad, false)
			}
			max := clone()
			max["input_epoch"] = 9007199254740991.0
			check(max, true)
			zero := clone()
			zero["control_epoch"] = 0
			check(zero, true)
			unknown := clone()
			unknown["unexpected"] = true
			check(unknown, false)
		})
	}
}
