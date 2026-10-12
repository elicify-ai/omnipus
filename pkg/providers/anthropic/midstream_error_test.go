package anthropicprovider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Issue #1278 sweep. Anthropic reports a failure after the stream has started as
// an `error` event (documented: "event: error / data: {type: error, error:
// {type: overloaded_error, ...}}"); a connection that simply ends leaves the
// stream without message_stop. Neither may come back as a finished answer.

func anthropicScriptedStream(t *testing.T, events ...string) *Provider {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join(events, "")))
	}))
	t.Cleanup(srv.Close)
	return NewProviderWithBaseURL("test-token", srv.URL)
}

const (
	evMessageStart = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-sonnet-4-6\",\"stop_reason\":null,\"usage\":{\"input_tokens\":9,\"output_tokens\":0}}}\n\n"
	evBlockStart   = "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"
	evTextDelta    = "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"I will now run the command:\"}}\n\n"
)

func TestChatStream_ErrorEventMidStreamIsAnErrorNotAnAnswer(t *testing.T) {
	p := anthropicScriptedStream(t, evMessageStart, evBlockStart, evTextDelta,
		"event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}\n\n")
	resp, err := p.ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}},
		nil, "claude-sonnet-4-6", map[string]any{"max_tokens": 256}, nil, nil)
	if err == nil {
		t.Fatalf("an error event ended the stream, yet it was returned as an answer: %+v", resp)
	}
}

func TestChatStream_StreamEndingWithoutMessageStopIsAnErrorNotAnAnswer(t *testing.T) {
	p := anthropicScriptedStream(t, evMessageStart, evBlockStart, evTextDelta)
	resp, err := p.ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}},
		nil, "claude-sonnet-4-6", map[string]any{"max_tokens": 256}, nil, nil)
	if err == nil {
		t.Fatalf("a stream cut off before message_stop was returned as an answer: %+v", resp)
	}
}
