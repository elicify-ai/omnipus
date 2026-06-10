package openai_compat

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestChatStream_RequestBodyHasStreamOptionsIncludeUsage asserts that a ChatStream
// call sets stream_options.include_usage=true in the outgoing request body.
//
// BDD:
//
//	Given an OpenAI-compatible provider,
//	When ChatStream is called,
//	Then the request body contains stream_options.include_usage==true.
//
// Guards against: stream_options field being removed from buildRequestBody.
// Traces to: pkg/providers/openai_compat/provider.go ChatStream — stream_options fix #411
func TestChatStream_RequestBodyHasStreamOptionsIncludeUsage(t *testing.T) {
	var capturedBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&capturedBody); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// Send a minimal SSE stream: one chunk + [DONE].
		w.Header().Set("Content-Type", "text/event-stream")
		bw := bufio.NewWriter(w)
		chunk := map[string]any{
			"choices": []map[string]any{
				{
					"delta":         map[string]any{"content": "hello"},
					"finish_reason": "stop",
				},
			},
		}
		chunkJSON, _ := json.Marshal(chunk)
		_, _ = bw.WriteString("data: " + string(chunkJSON) + "\n\n")
		_, _ = bw.WriteString("data: [DONE]\n\n")
		bw.Flush()
	}))
	defer server.Close()

	p := mustNewProvider(t, "key", server.URL, "")

	_, err := p.ChatStream(
		t.Context(),
		[]Message{{Role: "user", Content: "hi"}},
		nil,
		"gpt-4o",
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}

	// Assert stream_options is present and has include_usage:true.
	streamOpts, ok := capturedBody["stream_options"].(map[string]any)
	if !ok {
		t.Fatalf("request body missing stream_options; body=%v", capturedBody)
	}
	includeUsage, _ := streamOpts["include_usage"].(bool)
	if !includeUsage {
		t.Errorf("stream_options.include_usage must be true; stream_options=%v", streamOpts)
	}

	// Also assert stream:true is present.
	stream, _ := capturedBody["stream"].(bool)
	if !stream {
		t.Errorf("stream must be true in streaming request; body=%v", capturedBody)
	}
}

// TestChatStream_FinalChunkUsageParsedIntoResponse asserts that a final SSE chunk
// with a usage object (and empty choices) is parsed into the returned LLMResponse.Usage.
//
// BDD:
//
//	Given an SSE stream that ends with a usage chunk (choices:[], usage:{…}),
//	When ChatStream is called,
//	Then the returned LLMResponse.Usage is non-nil with correct token counts.
//
// Guards against: parseStreamResponse discarding usage from the final usage-only chunk.
// Traces to: pkg/providers/openai_compat/provider.go parseStreamResponse — #411
func TestChatStream_FinalChunkUsageParsedIntoResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		bw := bufio.NewWriter(w)

		// Normal content chunk.
		contentChunk := map[string]any{
			"choices": []map[string]any{
				{"delta": map[string]any{"content": "The answer is 42"}, "finish_reason": nil},
			},
		}
		contentJSON, _ := json.Marshal(contentChunk)
		_, _ = bw.WriteString("data: " + string(contentJSON) + "\n\n")

		// Finish chunk.
		finishChunk := map[string]any{
			"choices": []map[string]any{
				{"delta": map[string]any{}, "finish_reason": "stop"},
			},
		}
		finishJSON, _ := json.Marshal(finishChunk)
		_, _ = bw.WriteString("data: " + string(finishJSON) + "\n\n")

		// Final usage chunk: choices is empty, usage is populated.
		// This is the stream_options.include_usage=true response format.
		usageChunk := map[string]any{
			"choices": []map[string]any{},
			"usage": map[string]any{
				"prompt_tokens":     25,
				"completion_tokens": 10,
				"total_tokens":      35,
			},
		}
		usageJSON, _ := json.Marshal(usageChunk)
		_, _ = bw.WriteString("data: " + string(usageJSON) + "\n\n")

		_, _ = bw.WriteString("data: [DONE]\n\n")
		bw.Flush()
	}))
	defer server.Close()

	p := mustNewProvider(t, "key", server.URL, "")

	var chunks []string
	resp, err := p.ChatStream(
		t.Context(),
		[]Message{{Role: "user", Content: "What is 6×7?"}},
		nil,
		"gpt-4o",
		nil,
		func(accumulated string) { chunks = append(chunks, accumulated) },
	)
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}

	// Content must be accumulated.
	if resp.Content != "The answer is 42" {
		t.Errorf("Content = %q, want %q", resp.Content, "The answer is 42")
	}

	// Usage must be non-nil and have the correct values from the final chunk.
	if resp.Usage == nil {
		t.Fatal("Usage must be non-nil when the stream includes a usage chunk")
	}
	if resp.Usage.TotalTokens != 35 {
		t.Errorf("Usage.TotalTokens = %d, want 35", resp.Usage.TotalTokens)
	}
	if resp.Usage.PromptTokens != 25 {
		t.Errorf("Usage.PromptTokens = %d, want 25", resp.Usage.PromptTokens)
	}
	if resp.Usage.CompletionTokens != 10 {
		t.Errorf("Usage.CompletionTokens = %d, want 10", resp.Usage.CompletionTokens)
	}

	// Differentiation: if we sent a different usage chunk, the response must reflect it.
	// (Verified above — 35 != 0, so the response is not hardcoded to zero.)
}

// TestChatStream_DifferentInputsDifferentContent asserts that two ChatStream calls
// with different messages produce different content — the provider is not returning
// hardcoded responses.
//
// Guards against: ChatStream being a stub that returns the same content always.
// Traces to: pkg/providers/openai_compat/provider.go ChatStream — differentiation test
func TestChatStream_DifferentInputsDifferentContent(t *testing.T) {
	var callCount int
	responses := []string{"Response A", "Response B"}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := callCount
		if callCount < len(responses)-1 {
			callCount++
		} else {
			callCount++
		}
		content := responses[idx%len(responses)]

		w.Header().Set("Content-Type", "text/event-stream")
		bw := bufio.NewWriter(w)
		chunk := map[string]any{
			"choices": []map[string]any{
				{"delta": map[string]any{"content": content}, "finish_reason": "stop"},
			},
		}
		chunkJSON, _ := json.Marshal(chunk)
		_, _ = bw.WriteString("data: " + string(chunkJSON) + "\n\n")
		_, _ = bw.WriteString("data: [DONE]\n\n")
		bw.Flush()
	}))
	defer server.Close()

	p := mustNewProvider(t, "key", server.URL, "")

	resp1, err := p.ChatStream(
		t.Context(),
		[]Message{{Role: "user", Content: "First question"}},
		nil, "gpt-4o", nil, nil,
	)
	if err != nil {
		t.Fatalf("ChatStream call 1 error = %v", err)
	}

	resp2, err := p.ChatStream(
		t.Context(),
		[]Message{{Role: "user", Content: "Second question"}},
		nil, "gpt-4o", nil, nil,
	)
	if err != nil {
		t.Fatalf("ChatStream call 2 error = %v", err)
	}

	if resp1.Content == resp2.Content {
		t.Errorf("ChatStream returned identical content for different inputs: %q", resp1.Content)
	}
}
