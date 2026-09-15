// websocket_error_frame_scrub_test.go — a provider error body forwarded as the
// error frame's Verbose-chat detail must never carry a registered credential.
//
// The frame's detail is the one live surface that shows a slice of the
// provider's own response body (contracts/components/schemas/LLMError.yaml:
// "Live-only technical detail; never persisted or replayed"). It crosses the
// WebSocket on every error frame, whether or not the browser shows it, so a
// provider echoing an API key in that body must not put the key on the wire.

package gateway

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

func TestEventForwarder_ErrorFrame_DetailScrubsRegisteredCredential(t *testing.T) {
	const secret = "sk-live-WSDETAIL-9pQ2x7"
	const body = `{"error":{"message":"Incorrect API key provided: ` + secret + `"}}`

	// Registering a credential publishes the process-wide replacer; leave the
	// process as this test found it.
	t.Cleanup(logger.SetSensitiveValueReplacer(nil))
	cfg := &config.Config{}
	cfg.RegisterSensitiveValues([]string{secret})

	pe := &agent.ProviderError{Status: 503, Body: body}
	translated := agent.TranslateLLMError(pe, "LLM call failed")

	bus := agent.NewEventBus()
	defer bus.Close()
	h := makeMinimalHandler()
	wc, ch := makeForwarderTestConn(4)
	done := runForwarder(h, wc, "chat-1", bus)

	bus.Emit(agent.Event{Kind: agent.EventKindError, Payload: agent.ErrorPayload{
		Stage:         "llm",
		ChatID:        "chat-1",
		Code:          string(translated.Code),
		Message:       translated.Message,
		ProviderError: pe,
	}})
	bus.Close()
	<-done

	require.Len(t, ch, 1)
	raw := <-ch
	assert.NotContains(t, string(raw), secret, "the error frame must not carry the registered credential anywhere")

	var frame generated.ErrorFrame
	require.NoError(t, json.Unmarshal(raw, &frame))
	require.NotNil(t, frame.Payload)
	require.NotNil(t, frame.Payload.LlmError.Detail)
	detail := *frame.Payload.LlmError.Detail
	assert.Contains(t, detail, "status=503", "the detail must still carry the provider's status for the operator")
	assert.Contains(t, detail, "Incorrect API key provided", "the detail must still carry the provider's own words")
	assert.Contains(t, detail, "[FILTERED]", "the detail must show where the credential was scrubbed")
	assert.Equal(t, agent.UserMessageForCode(agent.CodeNetwork), frame.Payload.LlmError.Message,
		"the frame's message is the plain message for the error's code")
}
