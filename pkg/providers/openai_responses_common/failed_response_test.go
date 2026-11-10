package openai_responses_common

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/openai/openai-go/v3/responses"

	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// Issue #1278 sweep: the Responses API reports a failure after the stream has
// started as a `response.failed` event whose response carries status "failed"
// and an `error` object (documented shape). The codex stream loop hands that
// response to ParseResponseFromStruct; it used to be returned as a normal
// (possibly partial) answer.
func TestParseResponse_FailedResponseIsAnErrorNotAnAnswer(t *testing.T) {
	const failed = `{
	  "id": "resp_123", "object": "response", "status": "failed",
	  "error": {"code": "server_error", "message": "The model failed to generate a valid response."},
	  "output": [{"type": "message", "id": "msg_1", "role": "assistant", "status": "completed",
	    "content": [{"type": "output_text", "text": "I will now run the command:"}]}],
	  "usage": {"input_tokens": 1000, "output_tokens": 900, "total_tokens": 1900}
	}`
	var resp responses.Response
	if err := json.Unmarshal([]byte(failed), &resp); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	got, err := ParseResponseFromStruct(&resp)
	if err == nil {
		t.Fatalf("a failed response was returned as an answer: %+v", got)
	}
	var spe *common.StreamProviderError
	if !errors.As(err, &spe) {
		t.Fatalf("error = %T %v, want *common.StreamProviderError", err, err)
	}
	if spe.Code != "server_error" || spe.Usage == nil || spe.Usage.CompletionTokens != 900 {
		t.Errorf("error did not carry the provider code and billed usage: %+v", spe)
	}
}

func TestParseResponse_CompletedResponseIsUnchanged(t *testing.T) {
	var resp responses.Response
	if err := json.Unmarshal([]byte(`{"id":"r","status":"completed","output":[{"type":"message","id":"m","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hi"}]}]}`), &resp); err != nil {
		t.Fatal(err)
	}
	got, err := ParseResponseFromStruct(&resp)
	if err != nil || got.Content != "hi" || got.FinishReason != "stop" {
		t.Fatalf("got %+v err %v", got, err)
	}
}
