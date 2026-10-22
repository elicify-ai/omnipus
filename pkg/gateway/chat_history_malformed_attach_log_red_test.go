package gateway

import (
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Gate0 security F-2: retain the pre-change malformed attach warning, verified
// in c903's dispatchFrame. A following REAL valid attach is a protocol fence,
// so a missing warning is not inferred from a guessed sleep/quiet period.
func TestHistoryAttach_MalformedFramesAreLoggedWithoutPrivateContent(t *testing.T) {
	for _, fault := range []string{"session_id_wrong_type", "since_seq_wrong_type"} {
		t.Run(fault, func(t *testing.T) {
			home, cfg := historyD16Config(t)
			provider := &historyD16Provider{}
			h := newHistoryD16Harness(t, home, cfg, provider, false)
			sid := h.session()
			peer := h.connect()
			read := pagingLogCapture(t)
			slog.Warn("gate0 malformed-attach warning instrument", "event", "gate0_attach_warn_control")
			control := 0
			for _, row := range read() {
				if row["event"] == "gate0_attach_warn_control" {
					control++
					require.Equal(t, "warn", row["level"], "instrument can actually capture warnings, not only info")
				}
			}
			require.Equal(t, 1, control)
			body, err := json.Marshal(generated.AttachSessionFrame{Type: "attach_session", SessionId: sid})
			require.NoError(t, err)
			var fields map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(body, &fields))
			const private = "gate0-malformed-attach-private-payload"
			if fault == "session_id_wrong_type" {
				fields["session_id"] = json.RawMessage("17")
			} else {
				fields["since_seq"] = json.RawMessage(`"` + private + `"`)
			}
			fields["private_probe"] = json.RawMessage(`"` + private + `"`)
			fields["token"] = json.RawMessage(`"gate0-credential-canary-not-a-real-token"`)
			body, err = json.Marshal(fields)
			require.NoError(t, err)
			require.NoError(t, peer.WriteMessage(websocket.TextMessage, body))
			frames := h.attach(peer, sid) // ordered after bad frame on the same actual server read loop
			require.Equal(t, "catch_up_complete", historyD16Type(t, frames[len(frames)-1]), "server processed the bad frame and continued, no timing assumption")
			warnings := 0
			for _, row := range read() {
				if row["message"] != "ws: malformed attach_session frame" {
					continue
				}
				warnings++
				assert.Equal(t, "warn", row["level"])
				encoded, encodeErr := json.Marshal(row)
				require.NoError(t, encodeErr)
				assert.NotContains(t, string(encoded), private, "malformed frame body is private, not diagnostic content")
				assert.NotContains(t, string(encoded), "gate0-credential-canary-not-a-real-token")
				assert.NotContains(t, string(encoded), sid, "syntax refusal log is content/identity-free; normal accepted attach logs are a separate contract")
			}
			assert.Equal(t, 1, warnings, "F-2: malformed attach may not disappear silently; one sanitized prior-contract warning per failed decode")
			require.Zero(t, provider.count())
		})
	}
}
