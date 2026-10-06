// Copyright (c) 2026 Omnipus contributors
// License: MIT

package gateway

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/require"
)

// Unlike a generic successful-operation reader, this Stop reader expects the
// contract's user-cancelled error as well as cancel_stage. Every other error
// fails loudly. Oracle: contracts/components/schemas/LLMError.yaml::turn_canceled
// (exact code/text); an observed gateway error is not copied into expectations.
func (f *plainStopUATFixture) awaitStopFrame(t *testing.T, wantType string) []byte {
	t.Helper()
	require.NoError(t, f.conn.SetReadDeadline(time.Now().Add(busDeliveryTimeout)))
	for {
		_, raw, err := f.conn.ReadMessage()
		require.NoError(t, err, "PLAIN STOP: expected real server frame %s", wantType)
		var envelope wsTypeOnly
		require.NoError(t, json.Unmarshal(raw, &envelope))
		t.Logf("PLAIN STOP WIRE server->client: %s", raw)
		if envelope.Type == "error" {
			var failure generated.ErrorFrame
			require.NoError(t, json.Unmarshal(raw, &failure))
			require.Equal(t, strPtr(f.rootID), failure.SessionId, "cancellation error must belong to this chat")
			require.NotNil(t, failure.Payload, "unexpected gateway error instead of a typed Stop acknowledgement: %s", raw)
			require.Equal(t, "turn_canceled", failure.Payload.LlmError.Code, "no non-cancellation error is ignored by the fixture")
			require.Equal(t, "This turn was stopped before it finished.", failure.Message)
		}
		if envelope.Type == wantType {
			return raw
		}
	}
}
