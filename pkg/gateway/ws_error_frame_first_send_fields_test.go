// ws_error_frame_first_send_fields_test.go: S2-24 (architect ruling). The
// contract states two cross-field rules for ErrorFrame in prose and (for the
// first) as dependentRequired: first_message_error requires client_message_id,
// and answer_not_started also requires session_id. ORACLE: contracts
// ErrorFrame.yaml (first_message_error description). This test pins the
// producer; the schema keyword alone does not run at the producer.

package gateway

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSendFirstMessageError_HonoursTheContractsCrossFieldRules(t *testing.T) {
	for _, kind := range []string{"not_saved", "delivery_unknown", "answer_not_started"} {
		t.Run(kind, func(t *testing.T) {
			handler, _, _, wc := newProvenanceHarness(t)
			hcm := &wsHandlerHandleChatMessage{h: handler, wc: wc, clientMessageID: "fm-cid-1", sessionID: "session-fm-1"}
			hcm.sendFirstMessageError(kind)
			frames := issue1090DrainQueuedFrames(t, wc)
			require.Len(t, frames, 1)
			f := frames[0]
			assert.Equal(t, kind, f["first_message_error"])
			assert.Equal(t, "fm-cid-1", f["client_message_id"], "first_message_error requires client_message_id")
			if kind == "answer_not_started" {
				assert.Equal(t, "session-fm-1", f["session_id"], "answer_not_started requires its confirmed session_id")
			} else {
				assert.NotContains(t, f, "session_id", "an unsaved or uncertain append never names the session")
			}
		})
	}
}

func TestSendFirstMessageError_WithoutAClientIDCarriesNoFirstSendFields(t *testing.T) {
	handler, _, _, wc := newProvenanceHarness(t)
	hcm := &wsHandlerHandleChatMessage{h: handler, wc: wc}
	hcm.sendFirstMessageError("not_saved")
	f := issue1090DrainQueuedFrames(t, wc)[0]
	assert.NotContains(t, f, "first_message_error", "no client_message_id, so no first_message_error (dependentRequired)")
	assert.NotContains(t, f, "client_message_id")
}
