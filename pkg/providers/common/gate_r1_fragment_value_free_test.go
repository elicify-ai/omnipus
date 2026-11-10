package common

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// TestUndecodableArguments_ErrorNeverCarriesThePayload is Train 2 gate r1
// M24 (security W1; founder: fix in Train 2): a malformed tool-call payload
// can carry a plaintext secret the model put in a credential field; the
// refusal error is logged, so it must never quote the payload. The error
// stays classifiable (errors.Is), keeps the JSON cause reachable, and keeps
// the truncation evidence (ADR-087).
func TestUndecodableArguments_ErrorNeverCarriesThePayload(t *testing.T) {
	const secret = "sk-GATE-R1-M24-SECRET-VALUE"
	for name, payload := range map[string]string{
		"cut off":        `{"api_key": "` + secret,
		"malformed":      `{"api_key": "` + secret + `" "x"}`,
		"double-encoded": `"{\"api_key\": \"` + secret + `"`,
		"wrong shape":    `["` + secret + `"]`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := DecodeToolCallArguments(json.RawMessage(payload), "configure_provider")
			if err == nil {
				t.Fatal("instrument: the payload must be refused")
			}
			if strings.Contains(err.Error(), "GATE-R1-M24") {
				t.Fatalf("the refusal error quotes the payload: %s", err.Error())
			}
			if !errors.Is(err, ErrToolArgumentsUndecodable) {
				t.Fatalf("the error must stay classifiable: %v", err)
			}
			var tae *ToolArgumentsError
			if !errors.As(err, &tae) {
				t.Fatalf("the error must stay a ToolArgumentsError: %T", err)
			}
		})
	}
	_, err := DecodeToolCallArguments(json.RawMessage(`{"api_key": "`+secret), "configure_provider")
	var tae *ToolArgumentsError
	if !errors.As(err, &tae) || !tae.Truncated {
		t.Fatalf("a cut-off fragment keeps its truncation evidence: %v", err)
	}
	var syntax *json.SyntaxError
	_, err = DecodeToolCallArguments(json.RawMessage(`{"api_key": "`+secret+`" "x"}`), "configure_provider")
	if !errors.As(err, &syntax) {
		t.Fatalf("the JSON cause stays reachable for callers: %v", err)
	}
}
