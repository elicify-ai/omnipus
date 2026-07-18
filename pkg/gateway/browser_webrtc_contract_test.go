//go:build !cgo

// browser_webrtc_contract_test.go — ADR-047 / wave-plan W2-C: contract
// round-trip coverage for the 7 new WebRTC signaling frame types (D4/D6).
//
// For each frame: marshal a REALISTIC instance of the generated Go struct
// (pkg/api/generated), then validate the resulting raw JSON against the SAME
// inboundschemas validator the gateway applies at runtime
// (ValidateInboundFrameJSON — the function browser_ws.go's readLoop and
// browser_webrtc.go's captureIngestWSHandler.serveConn both call when
// gateway.validate_inbound is enabled). This proves the generated Go type
// and the hand-authored JSON Schema agree on the wire shape in both
// directions:
//
//   - positive: a realistic, correctly-typed frame validates cleanly.
//   - negative (wrong type const): the same payload with `type` set to a
//     bogus string is rejected (proves the schema's `const` discriminator
//     is enforced).
//   - negative (missing required field): the same payload with one
//     REQUIRED property deleted from the raw JSON is rejected (proves
//     `required` + `additionalProperties:false` reject a short frame,
//     not just a wrong-typed one — deleting the key from a generic
//     map[string]any is used rather than a zero-valued Go struct field,
//     since a required non-pointer Go field still serializes as present-
//     but-empty, which is a DIFFERENT schema violation (minLength) than
//     "missing required property").
//
// Traces to: docs/internal/design/webrtc-build/wave-plan.md row W2-C.

package gateway

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// webrtcContractCloneMap returns a shallow copy of m — sufficient here since
// every mutation below only adds/removes/overwrites a top-level key.
func webrtcContractCloneMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func TestWebRTCFrameSchemasRoundTrip(t *testing.T) {
	const (
		sessID   = "e2e-contract-session"
		agentID  = "e2e-contract-agent"
		sdpOffer = "v=0\r\no=- 4611731400430051336 2 IN IP4 127.0.0.1\r\ns=-\r\nt=0 0\r\na=group:BUNDLE 0 1\r\n"
		sdpAns   = "v=0\r\no=- 3547045871053270000 2 IN IP4 127.0.0.1\r\ns=-\r\nt=0 0\r\na=group:BUNDLE 0 1\r\n"
	)
	trueVal := true
	reason := "tab switched"

	offerFrame := generated.BrowserWebRTCOfferFrame{
		Type:      string(generated.WsFrameTypeBrowserWebrtcOffer),
		AgentId:   agentID,
		SessionId: sessID,
		Sdp:       sdpOffer,
	}
	answerFrame := generated.BrowserWebRTCAnswerFrame{
		Type:      string(generated.WsFrameTypeBrowserWebrtcAnswer),
		Sdp:       sdpAns,
		SessionId: strPtrForContractTest(sessID),
	}
	stateFrame := generated.BrowserWebRTCStateFrame{
		Type:      string(generated.WsFrameTypeBrowserWebrtcState),
		Available: true,
		Active:    &trueVal,
		HasAudio:  &trueVal,
		SessionId: strPtrForContractTest(sessID),
	}
	helloFrame := generated.BrowserCaptureHelloFrame{
		Type:       string(generated.WsFrameTypeBrowserCaptureHello),
		Token:      "0123456789abcdef0123456789abcdef",
		ExtVersion: "1.0.0",
	}
	captureOfferFrame := generated.BrowserCaptureOfferFrame{
		Type: string(generated.WsFrameTypeBrowserCaptureOffer),
		Sdp:  sdpOffer,
	}
	captureAnswerFrame := generated.BrowserCaptureAnswerFrame{
		Type: string(generated.WsFrameTypeBrowserCaptureAnswer),
		Sdp:  sdpAns,
	}
	controlFrame := generated.BrowserCaptureControlFrame{
		Type:   string(generated.WsFrameTypeBrowserCaptureControl),
		Action: "recapture",
		Reason: &reason,
	}

	cases := []struct {
		name string
		// schema is the inboundschemas file name (without .yaml) —
		// identical to the struct name for every WS frame, per
		// wsFrameSchemaName/captureFrameSchemaName's own mapping.
		schema string
		frame  any
		// requiredField is a required property (other than "type") whose
		// deletion from the raw JSON must be rejected.
		requiredField string
	}{
		{"BrowserWebRTCOfferFrame", "BrowserWebRTCOfferFrame", offerFrame, "sdp"},
		{"BrowserWebRTCAnswerFrame", "BrowserWebRTCAnswerFrame", answerFrame, "sdp"},
		{"BrowserWebRTCStateFrame", "BrowserWebRTCStateFrame", stateFrame, "available"},
		{"BrowserCaptureHelloFrame", "BrowserCaptureHelloFrame", helloFrame, "token"},
		{"BrowserCaptureOfferFrame", "BrowserCaptureOfferFrame", captureOfferFrame, "sdp"},
		{"BrowserCaptureAnswerFrame", "BrowserCaptureAnswerFrame", captureAnswerFrame, "sdp"},
		{"BrowserCaptureControlFrame", "BrowserCaptureControlFrame", controlFrame, "action"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			validData, err := json.Marshal(tc.frame)
			require.NoError(t, err)

			// Positive: the generated struct's own JSON validates cleanly.
			errMsg, serverErr := ValidateInboundFrameJSON(tc.schema, validData)
			require.False(t, serverErr, "schema compile must succeed for %s", tc.schema)
			assert.Empty(t, errMsg, "a realistic, valid %s must validate cleanly against its schema; got: %s", tc.schema, errMsg)

			var asMap map[string]any
			require.NoError(t, json.Unmarshal(validData, &asMap))

			// Negative 1: wrong type const.
			badType := webrtcContractCloneMap(asMap)
			badType["type"] = "not_a_real_frame_type"
			badTypeData, err := json.Marshal(badType)
			require.NoError(t, err)
			errMsg, serverErr = ValidateInboundFrameJSON(tc.schema, badTypeData)
			require.False(t, serverErr)
			assert.NotEmpty(t, errMsg, "%s with type=%q (wrong const) must be REJECTED, not silently accepted", tc.schema, "not_a_real_frame_type")

			// Negative 2: missing required field (the key itself removed,
			// not merely zero-valued — see file doc comment).
			missingField := webrtcContractCloneMap(asMap)
			require.Contains(t, missingField, tc.requiredField, "sanity: field must be present in the valid payload before deletion")
			delete(missingField, tc.requiredField)
			missingData, err := json.Marshal(missingField)
			require.NoError(t, err)
			errMsg, serverErr = ValidateInboundFrameJSON(tc.schema, missingData)
			require.False(t, serverErr)
			assert.NotEmpty(t, errMsg, "%s with required field %q REMOVED must be REJECTED, not silently accepted", tc.schema, tc.requiredField)
		})
	}
}

func strPtrForContractTest(s string) *string { return &s }
