package anthropicprovider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
)

// thinkingStreamServer serves an Anthropic SSE stream that opens a thinking
// block, streams its text in several thinking_delta events, closes it, and
// then answers with a short text block. It is the shape of a model thinking
// for a long time before it says anything at all.
func thinkingStreamServer(t *testing.T, thinkingChunks []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		write := func(s string) {
			if _, err := w.Write([]byte(s)); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		write("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\"," +
			"\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-sonnet-4-6\"," +
			"\"stop_reason\":null,\"usage\":{\"input_tokens\":9,\"output_tokens\":0}}}\n\n")
		write("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0," +
			"\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\",\"signature\":\"\"}}\n\n")
		for _, c := range thinkingChunks {
			b, err := json.Marshal(c)
			if err != nil {
				t.Errorf("marshalling thinking chunk: %v", err)
				return
			}
			write(fmt.Sprintf("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,"+
				"\"delta\":{\"type\":\"thinking_delta\",\"thinking\":%s}}\n\n", b))
		}
		write("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0," +
			"\"delta\":{\"type\":\"signature_delta\",\"signature\":\"sig\"}}\n\n")
		write("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
		write("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1," +
			"\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		write("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1," +
			"\"delta\":{\"type\":\"text_delta\",\"text\":\"Done.\"}}\n\n")
		write("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\n")
		write("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}," +
			"\"usage\":{\"output_tokens\":7}}\n\n")
		write("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
}

// TestChatStream_ThinkingCountsAsProgress is the Anthropic half of the founder
// decision of 2026-09-14: extended thinking is forward progress. Each
// thinking_delta must produce a reasoning progress event WHILE the block is
// still open — the running byte total after each delta, not one lump when the
// block closes — and none of it may carry the thinking text.
func TestChatStream_ThinkingCountsAsProgress(t *testing.T) {
	chunks := []string{"The user wants", " a summary of", strings.Repeat("t", 400)}
	server := thinkingStreamServer(t, chunks)
	defer server.Close()

	p := NewProviderWithBaseURL("test-token", server.URL)

	var progress []protocoltypes.ToolCallProgress
	resp, err := p.ChatStream(
		t.Context(),
		[]Message{{Role: "user", Content: "summarise"}},
		nil,
		"claude-sonnet-4-6",
		map[string]any{"max_tokens": 1024},
		func(string) {},
		func(ev protocoltypes.ToolCallProgress) { progress = append(progress, ev) },
	)
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}
	if resp.Content != "Done." {
		t.Errorf("resp.Content = %q, want %q", resp.Content, "Done.")
	}

	// Oracle: the running totals of the fixture's own chunk lengths.
	want := make([]int, 0, len(chunks))
	total := 0
	for _, c := range chunks {
		total += len(c)
		want = append(want, total)
	}

	var got []int
	for _, ev := range progress {
		if ev.Index != protocoltypes.ReasoningProgressIndex {
			t.Errorf("unexpected non-reasoning progress event in a thinking+text stream: %+v", ev)
			continue
		}
		if ev.Name != "" || ev.ArgsBytes != 0 {
			t.Errorf("reasoning event claims a tool call: %+v", ev)
		}
		got = append(got, ev.ReasoningBytes)
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("reasoning progress totals = %v, want one event per thinking delta with running totals %v", got, want)
	}
}
