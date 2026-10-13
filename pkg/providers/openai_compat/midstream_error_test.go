package openai_compat

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// Issue #1278: a stream that dies mid-response reaches the parser as a 200 whose
// last chunk carries a top-level `error` and finish_reason "error" (OpenRouter's
// documented mid-stream error shape). The fixture is a REAL deepseek/deepseek-v4.1-flash
// stream captured 2026-10-12 (reasoning, then the announcing preamble) cut where the
// tool call would begin, plus that error chunk and the usage chunk that still
// arrives. The parser used to return the preamble as a finished answer with no
// tool calls and no error: the turn then ended mid-sentence, silently.
func TestParseStream_MidStreamProviderErrorIsAnErrorNotAFinalAnswer(t *testing.T) {
	raw, err := os.ReadFile("testdata/1278_midstream_error.sse")
	if err != nil {
		t.Fatal(err)
	}
	got, perr := parseStreamResponse(context.Background(), strings.NewReader(string(raw)), nil, nil, nil)
	if perr == nil {
		t.Fatalf("a stream that ended with an error chunk was returned as a response: content=%q finish=%q tool_calls=%d",
			got.Content, got.FinishReason, len(got.ToolCalls))
	}
	var spe *common.StreamProviderError
	if !errors.As(perr, &spe) {
		t.Fatalf("error = %T %v, want *common.StreamProviderError", perr, perr)
	}
	if spe.Code != "server_error" || !strings.Contains(spe.Message, "Provider disconnected") {
		t.Errorf("error did not carry the provider's code and message: %+v", spe)
	}
	if spe.Usage == nil || spe.Usage.CompletionTokens != 8054 {
		t.Errorf("the billed usage of the failed attempt was dropped: %+v", spe.Usage)
	}
}

// A clean stream is untouched by the error handling.
func TestParseStream_NoErrorChunkStillReturnsTheResponse(t *testing.T) {
	stream := "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
	got, err := parseStreamResponse(context.Background(), strings.NewReader(stream), nil, nil, nil)
	if err != nil || got.Content != "hi" || got.FinishReason != "stop" {
		t.Fatalf("got %+v err %v", got, err)
	}
}

// finish_reason "error" without an error object (a provider that signals it only
// through the finish reason) is the same failure.
func TestParseStream_FinishReasonErrorAloneIsAnError(t *testing.T) {
	stream := "data: {\"choices\":[{\"delta\":{\"content\":\"Running it now:\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"error\"}]}\n\ndata: [DONE]\n\n"
	got, err := parseStreamResponse(context.Background(), strings.NewReader(stream), nil, nil, nil)
	if err == nil || !errors.Is(err, common.ErrStreamProviderError) {
		t.Fatalf("got %+v err %v, want an ErrStreamProviderError", got, err)
	}
}

// R3-SRV-SFH-04: OpenRouter sends `"code": null` for an upstream failure with no
// numeric code. json.RawMessage keeps the literal `null`, which used to become
// the string "null", classify as StreamErrorOther and lose the retry round 2
// gave it. A JSON null is an absent code.
func TestParseStream_NullErrorCodeIsAnAbsentCodeAndStaysRetryable(t *testing.T) {
	stream := "data: {\"choices\":[{\"delta\":{\"content\":\"par\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"error\":{\"code\":null,\"message\":\"Provider returned error\"},\"choices\":[{\"delta\":{},\"finish_reason\":\"error\"}]}\n\n" +
		"data: [DONE]\n\n"
	_, err := parseStreamResponse(context.Background(), strings.NewReader(stream), nil, nil, nil)
	var spe *common.StreamProviderError
	if !errors.As(err, &spe) {
		t.Fatalf("error = %T %v, want *common.StreamProviderError", err, err)
	}
	if spe.Code != "" {
		t.Errorf("a JSON null code must read as absent, got %q", spe.Code)
	}
	if got := spe.Kind(); got != common.StreamErrorTransient {
		t.Errorf("an error chunk with a null code is a transient fault (retried), got kind %d", got)
	}
	// Control: a quoted code still arrives as itself.
	quoted := strings.Replace(stream, `"code":null`, `"code":"server_error"`, 1)
	_, err = parseStreamResponse(context.Background(), strings.NewReader(quoted), nil, nil, nil)
	if !errors.As(err, &spe) || spe.Code != "server_error" {
		t.Fatalf("control: a string code was not carried through: %v %+v", err, spe)
	}
}
