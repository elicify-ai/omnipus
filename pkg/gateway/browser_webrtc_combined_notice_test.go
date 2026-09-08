package gateway

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/require"
)

func TestWebRTCOfferCombinesTransportFailuresWithinStatusSchema(t *testing.T) {
	f := newHandlerContextFixture(t, false)
	f.handler.mediaConnMu.Lock()
	f.handler.mediaPortFallback = &mediaPortFallbackState{configured: 65535, bound: 0, lastProbed: 65535}
	f.handler.mediaTCPBindErr = errors.New("configured TCP port unavailable")
	f.handler.turnStartErr = errors.New("configured TURN relay unavailable")
	f.handler.mediaConnMu.Unlock()
	epoch := f.state.beginWebRTCOffer()
	f.handler.handleWebRTCOffer(f.conn, f.state, "viewer", "user", f.offer(t, nil), f.cfg, epoch)
	var statuses []browserOutboundFrame
	answered := false
	for len(f.conn.sendCh) > 0 {
		queued := <-f.conn.sendCh
		var header struct {
			Type string `json:"type"`
		}
		require.NoError(t, json.Unmarshal(queued.data, &header))
		if header.Type == "browser_webrtc_answer" {
			answered = true
		}
		if header.Type == "browser_status" {
			statuses = append(statuses, queued)
		}
	}
	require.True(t, answered, "notice must be tested after an actual admitted viewer offer")
	require.Len(t, statuses, 1, "separate notices overwrite one another in the UI")
	queued := statuses[0]
	var status generated.BrowserStatusFrame
	require.NoError(t, json.Unmarshal(queued.data, &status))
	require.NotNil(t, status.Message)
	require.Contains(t, *status.Message, "UDP")
	require.Contains(t, *status.Message, "TCP")
	require.Contains(t, *status.Message, "TURN")
	require.Contains(t, *status.Message, "65535")
	require.LessOrEqual(t, len(*status.Message), browserStatusMessageMaxLength(t))
	message, serverError := ValidateInboundFrameJSON("BrowserStatusFrame", queued.data)
	require.False(t, serverError)
	require.Empty(t, message, "the actual outbound notice must satisfy the frontend's wire schema")
	require.True(t, f.conn.canSendFrame(queued))
	f.state.beginAttach()
	require.False(t, f.conn.canSendFrame(queued), "notice escaped its originating attachment")
}
