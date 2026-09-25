// Issue #711: a chat error frame must not carry the provider's raw body, or an
// unregistered key-shaped fragment of it, unless Verbose chat is on.
//
// Verbose chat is a browser setting and defaults to off
// (src/store/chatPreferences.ts). The server has no verbose opt-in today, so
// the default connection — the only one this test can name without inventing
// a switch the issue does not define — is verbose-off. How a viewer turns
// verbose on (request flag, per-session setting, or dropping detail entirely)
// is a specification gap in #711 and is not encoded here.

package gateway

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

func TestErrorFrame_VerboseOff_OmitsRawProviderBody(t *testing.T) {
	// The marker is ordinary provider text. The key shape is the unregistered
	// fragment #711 names (a provider-masked key, not a value this process
	// registered with the credential scrubber).
	const marker = "raw-provider-body-MARKER"
	const keyShape = "sk-or-v1-UNREGISTEREDFRAGMENT"
	const userMessage = "the model could not complete the request"
	rawBody := marker + " " + keyShape

	cases := []struct {
		name        string
		payload     agent.ErrorPayload
		wantMessage string
	}{
		{
			name: "curated code",
			payload: agent.ErrorPayload{
				Stage:         "runTurn",
				Code:          string(agent.CodeUnknown),
				Message:       userMessage,
				ProviderError: &agent.ProviderError{Status: 502, Body: rawBody},
				ChatID:        "chat-1",
			},
			wantMessage: userMessage,
		},
		{
			name: "unclassified provider error",
			payload: agent.ErrorPayload{
				Stage:         "runTurn",
				Message:       "provider request failed",
				ProviderError: &agent.ProviderError{Status: 502, Body: rawBody},
				ChatID:        "chat-1",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := makeMinimalHandler()
			wc, ch := makeForwarderTestConn(4)
			bindTestConnToSession(h, "chat-1", "chat-1", wc)

			h.hubSyncTap(agent.Event{Kind: agent.EventKindError, Payload: tc.payload})

			require.Len(t, ch, 1, "the error frame must still be delivered; only the raw detail is forbidden")
			raw := <-ch
			body := string(raw)
			assert.NotContains(t, body, marker,
				"issue #711: raw provider body must not cross the wire when verbose chat is off")
			assert.NotContains(t, body, keyShape,
				"issue #711: an unregistered key-shaped fragment must not cross the wire")

			var frame generated.ErrorFrame
			require.NoError(t, json.Unmarshal(raw, &frame))
			if frame.Payload != nil && frame.Payload.LlmError.Detail != nil {
				assert.Empty(t, *frame.Payload.LlmError.Detail,
					"issue #711: detail is absent unless verbose chat is on")
			}
			if tc.wantMessage != "" {
				assert.Equal(t, tc.wantMessage, frame.Message,
					"the user-facing message stays; only the raw detail is removed")
			}
		})
	}
}
