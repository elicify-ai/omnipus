package openai_compat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers/common"
	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
)

func mustNewProvider(t *testing.T, apiKey, apiBase, proxy string, opts ...Option) *Provider {
	t.Helper()
	p, err := NewProvider(apiKey, apiBase, proxy, opts...)
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}
	return p
}

func mustNewProviderWithMaxTokensFieldAndTimeout(
	t *testing.T,
	apiKey, apiBase, proxy, maxTokensField string,
	requestTimeoutSeconds int,
) *Provider {
	t.Helper()
	p, err := NewProviderWithMaxTokensFieldAndTimeout(apiKey, apiBase, proxy, maxTokensField, requestTimeoutSeconds)
	if err != nil {
		t.Fatalf("NewProviderWithMaxTokensFieldAndTimeout() error = %v", err)
	}
	return p
}

func TestProviderChat_UsesMaxCompletionTokensForGLM(t *testing.T) {
	var requestBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp := map[string]any{
			"choices": []map[string]any{
				{
					"message":       map[string]any{"content": "ok"},
					"finish_reason": "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := mustNewProvider(t, "key", server.URL, "")
	_, err := p.Chat(
		t.Context(),
		[]Message{{Role: "user", Content: "hi"}},
		nil,
		"glm-4.7",
		map[string]any{"max_tokens": 1234},
	)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if _, ok := requestBody["max_completion_tokens"]; !ok {
		t.Fatalf("expected max_completion_tokens in request body")
	}
	if _, ok := requestBody["max_tokens"]; ok {
		t.Fatalf("did not expect max_tokens key for glm model")
	}
}

func TestProviderChat_ParsesToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"content": "",
						"tool_calls": []map[string]any{
							{
								"id":   "call_1",
								"type": "function",
								"function": map[string]any{
									"name":      "get_weather",
									"arguments": "{\"city\":\"SF\"}",
								},
							},
						},
					},
					"finish_reason": "tool_calls",
				},
			},
			"usage": map[string]any{
				"prompt_tokens":     10,
				"completion_tokens": 5,
				"total_tokens":      15,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := mustNewProvider(t, "key", server.URL, "")
	out, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "gpt-4o", nil)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if len(out.ToolCalls) != 1 {
		t.Fatalf("len(ToolCalls) = %d, want 1", len(out.ToolCalls))
	}
	if out.ToolCalls[0].Name != "get_weather" {
		t.Fatalf("ToolCalls[0].Name = %q, want %q", out.ToolCalls[0].Name, "get_weather")
	}
	if out.ToolCalls[0].Arguments["city"] != "SF" {
		t.Fatalf("ToolCalls[0].Arguments[city] = %v, want SF", out.ToolCalls[0].Arguments["city"])
	}
}

func TestProviderChat_ParsesToolCallsWithObjectArguments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"content": "",
						"tool_calls": []map[string]any{
							{
								"id":   "call_1",
								"type": "function",
								"function": map[string]any{
									"name": "get_weather",
									"arguments": map[string]any{
										"city":   "SF",
										"metric": true,
									},
								},
							},
						},
					},
					"finish_reason": "tool_calls",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := mustNewProvider(t, "key", server.URL, "")
	out, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "gpt-4o", nil)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if len(out.ToolCalls) != 1 {
		t.Fatalf("len(ToolCalls) = %d, want 1", len(out.ToolCalls))
	}
	if out.ToolCalls[0].Name != "get_weather" {
		t.Fatalf("ToolCalls[0].Name = %q, want %q", out.ToolCalls[0].Name, "get_weather")
	}
	if out.ToolCalls[0].Arguments["city"] != "SF" {
		t.Fatalf("ToolCalls[0].Arguments[city] = %v, want SF", out.ToolCalls[0].Arguments["city"])
	}
	if out.ToolCalls[0].Arguments["metric"] != true {
		t.Fatalf("ToolCalls[0].Arguments[metric] = %v, want true", out.ToolCalls[0].Arguments["metric"])
	}
}

func TestProviderChat_ParsesReasoningContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"content":           "The answer is 2",
						"reasoning_content": "Let me think step by step... 1+1=2",
						"tool_calls": []map[string]any{
							{
								"id":   "call_1",
								"type": "function",
								"function": map[string]any{
									"name":      "calculator",
									"arguments": "{\"expr\":\"1+1\"}",
								},
							},
						},
					},
					"finish_reason": "tool_calls",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := mustNewProvider(t, "key", server.URL, "")
	out, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "1+1=?"}}, nil, "kimi-k2.5", nil)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if out.ReasoningContent != "Let me think step by step... 1+1=2" {
		t.Fatalf("ReasoningContent = %q, want %q", out.ReasoningContent, "Let me think step by step... 1+1=2")
	}
	if out.Content != "The answer is 2" {
		t.Fatalf("Content = %q, want %q", out.Content, "The answer is 2")
	}
	if len(out.ToolCalls) != 1 {
		t.Fatalf("len(ToolCalls) = %d, want 1", len(out.ToolCalls))
	}
}

func TestProviderChat_PreservesReasoningContentInHistory(t *testing.T) {
	var requestBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp := map[string]any{
			"choices": []map[string]any{
				{
					"message":       map[string]any{"content": "ok"},
					"finish_reason": "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := mustNewProvider(t, "key", server.URL, "")

	// Simulate a multi-turn conversation where the assistant's previous
	// reply included reasoning_content (e.g. from kimi-k2.5).
	messages := []Message{
		{Role: "user", Content: "What is 1+1?"},
		{Role: "assistant", Content: "2", ReasoningContent: "Let me think... 1+1=2"},
		{Role: "user", Content: "What about 2+2?"},
	}

	_, err := p.Chat(t.Context(), messages, nil, "kimi-k2.5", nil)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	// Verify reasoning_content is preserved in the serialized request.
	reqMessages, ok := requestBody["messages"].([]any)
	if !ok {
		t.Fatalf("messages is not []any: %T", requestBody["messages"])
	}
	assistantMsg, ok := reqMessages[1].(map[string]any)
	if !ok {
		t.Fatalf("assistant message is not map[string]any: %T", reqMessages[1])
	}
	if assistantMsg["reasoning_content"] != "Let me think... 1+1=2" {
		t.Errorf("reasoning_content not preserved in request, got %v", assistantMsg["reasoning_content"])
	}
}

func TestProviderChat_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer server.Close()

	p := mustNewProvider(t, "key", server.URL, "")
	_, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "gpt-4o", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestProviderChat_JSONHTTPErrorDoesNotReportHTML(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad request"}`))
	}))
	defer server.Close()

	p := mustNewProvider(t, "key", server.URL, "")
	_, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "gpt-4o", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var pe *common.ProviderError
	if !errors.As(err, &pe) {
		t.Fatalf("expected *common.ProviderError, got %T", err)
	}
	if pe.Status != http.StatusBadRequest {
		t.Fatalf("expected status %d in ProviderError.Status, got %d", http.StatusBadRequest, pe.Status)
	}
	if !strings.Contains(pe.Body, "bad request") {
		t.Fatalf("expected JSON body content in ProviderError.Body, got %q", pe.Body)
	}
	if strings.Contains(pe.ContentType, "text/html") {
		t.Fatalf("expected non-HTML content-type in ProviderError.ContentType, got %q", pe.ContentType)
	}
	// A JSON error must not be misclassified as an HTML error.
	if pe.Err != nil && strings.Contains(pe.Err.Error(), "returned HTML instead of JSON") {
		t.Fatalf("expected non-HTML http error, got %v", pe.Err)
	}
}

func TestProviderChat_HTMLResponsesReturnHelpfulError(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		statusCode  int
		body        string
	}{
		{
			name:        "html success response",
			contentType: "text/html; charset=utf-8",
			statusCode:  http.StatusOK,
			body:        "<!DOCTYPE html><html><body>gateway login</body></html>",
		},
		{
			name:        "html error response",
			contentType: "text/html; charset=utf-8",
			statusCode:  http.StatusBadGateway,
			body:        "<!DOCTYPE html><html><body>bad gateway</body></html>",
		},
		{
			name:        "mislabeled html success response",
			contentType: "application/json",
			statusCode:  http.StatusOK,
			body:        "   \r\n\t<!DOCTYPE html><html><body>gateway login</body></html>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tt.contentType)
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			p := mustNewProvider(t, "key", server.URL, "")
			_, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "gpt-4o", nil)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			var pe *common.ProviderError
			if !errors.As(err, &pe) {
				t.Fatalf("expected *common.ProviderError, got %T", err)
			}
			if pe.Status != tt.statusCode {
				t.Fatalf("expected status %d in ProviderError.Status, got %d", tt.statusCode, pe.Status)
			}
			if !strings.Contains(pe.Body, "<html") {
				t.Fatalf("expected HTML body content in ProviderError.Body, got %q", pe.Body)
			}
			if pe.Err == nil || !strings.Contains(pe.Err.Error(), "returned HTML instead of JSON") {
				t.Fatalf("expected helpful HTML error in ProviderError.Err, got %v", pe.Err)
			}
			if pe.Err == nil || !strings.Contains(pe.Err.Error(), "check api_base or proxy configuration") {
				t.Fatalf("expected configuration hint in ProviderError.Err, got %v", pe.Err)
			}
		})
	}
}

func TestProviderChat_SuccessResponseUsesStreamingDecoder(t *testing.T) {
	content := strings.Repeat("a", 1024)
	body := `{"choices":[{"message":{"content":"` + content + `"},"finish_reason":"stop"}]}`

	p := mustNewProvider(t, "key", "https://example.com/v1", "")
	p.httpClient = &http.Client{
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body: &errAfterDataReadCloser{
					data:      []byte(body),
					chunkSize: 64,
				},
			}, nil
		}),
	}

	out, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "gpt-4o", nil)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if out.Content != content {
		t.Fatalf("Content = %q, want %q", out.Content, content)
	}
}

func TestProviderChat_LargeHTMLResponsePreviewIsTruncated(t *testing.T) {
	body := append([]byte("<!DOCTYPE html><html><body>"), bytes.Repeat([]byte("A"), 2048)...)
	body = append(body, []byte("</body></html>")...)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write(body)
	}))
	defer server.Close()

	p := mustNewProvider(t, "key", server.URL, "")
	_, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "gpt-4o", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var pe *common.ProviderError
	if !errors.As(err, &pe) {
		t.Fatalf("expected *common.ProviderError, got %T", err)
	}
	if pe.Status != http.StatusBadGateway {
		t.Fatalf("expected status %d in ProviderError.Status, got %d", http.StatusBadGateway, pe.Status)
	}
	// The full body is carried for classification...
	if !strings.Contains(pe.Body, "<!DOCTYPE html><html><body>") {
		t.Fatalf("expected HTML body content in ProviderError.Body, got %q", pe.Body)
	}
	// ...but the log preview is truncated.
	if !strings.HasSuffix(pe.BodyPreview, "...") {
		t.Fatalf("expected truncated preview (\"...\" suffix) in ProviderError.BodyPreview, got %q", pe.BodyPreview)
	}
	if len(pe.BodyPreview) >= len(pe.Body) {
		t.Fatalf("expected preview shorter than full body (preview=%d, body=%d)", len(pe.BodyPreview), len(pe.Body))
	}
}

// TestProviderChat_KimiTemperatureNormalized keeps the Kimi temperature rule
// and drops the prefix-stripping half of the old test.
//
// The retired half — TestProviderChat_StripsGroqOllamaDeepseekVivgridNovitaPrefixes
// and this test's `moonshot/kimi-k2.5` → `kimi-k2.5` assertion — pinned
// `normalizeModel`, a thirteen-vendor prefix table applied on the way out to
// the wire. ADR-067 FR-034 deletes every `provider/` prefix convention on a
// model id, and the table was actively wrong for the new ids: it turned
// `deepseek/deepseek-v3.2` (one OpenRouter model) into `deepseek-v3.2` for
// any non-openrouter host. TestModelSentVerbatim below is the replacement.
func TestProviderChat_KimiTemperatureNormalized(t *testing.T) {
	var requestBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp := map[string]any{
			"choices": []map[string]any{
				{
					"message":       map[string]any{"content": "ok"},
					"finish_reason": "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := mustNewProvider(t, "key", server.URL, "")
	_, err := p.Chat(
		t.Context(),
		[]Message{{Role: "user", Content: "hi"}},
		nil,
		"kimi-k2.5",
		map[string]any{"temperature": 0.3},
	)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if requestBody["model"] != "kimi-k2.5" {
		t.Fatalf("model = %v, want kimi-k2.5", requestBody["model"])
	}
	if requestBody["temperature"] != 1.0 {
		t.Fatalf("temperature = %v, want 1.0", requestBody["temperature"])
	}
}

// TestProviderChat_ModelReachesTheWireVerbatim is the end-to-end half of
// TestModelSentVerbatim: the id in the config is the id in the HTTP body.
func TestProviderChat_ModelReachesTheWireVerbatim(t *testing.T) {
	var requestBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": "ok"}, "finish_reason": "stop"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := mustNewProvider(t, "key", server.URL, "")
	for _, model := range []string{
		"deepseek/deepseek-v3.2",
		"zai-org/glm-5",
		"minimax/minimax-m2.5",
		"qwen2.5:14b",
		"openrouter/auto",
	} {
		t.Run(model, func(t *testing.T) {
			if _, err := p.Chat(
				t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, model, nil,
			); err != nil {
				t.Fatalf("Chat() error = %v", err)
			}
			if requestBody["model"] != model {
				t.Fatalf("model on the wire = %v, want %q verbatim", requestBody["model"], model)
			}
		})
	}
}

func TestProvider_ProxyConfigured(t *testing.T) {
	proxyURL := "http://127.0.0.1:8080"
	p := mustNewProvider(t, "key", "https://example.com", proxyURL)

	transport, ok := p.httpClient.Transport.(*http.Transport)
	if !ok || transport == nil {
		t.Fatalf("expected http transport with proxy, got %T", p.httpClient.Transport)
	}

	req := &http.Request{URL: &url.URL{Scheme: "https", Host: "api.example.com"}}
	gotProxy, err := transport.Proxy(req)
	if err != nil {
		t.Fatalf("proxy function returned error: %v", err)
	}
	if gotProxy == nil || gotProxy.String() != proxyURL {
		t.Fatalf("proxy = %v, want %s", gotProxy, proxyURL)
	}
}

func TestProviderChat_AcceptsNumericOptionTypes(t *testing.T) {
	var requestBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp := map[string]any{
			"choices": []map[string]any{
				{
					"message":       map[string]any{"content": "ok"},
					"finish_reason": "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := mustNewProvider(t, "key", server.URL, "")
	_, err := p.Chat(
		t.Context(),
		[]Message{{Role: "user", Content: "hi"}},
		nil,
		"gpt-4o",
		map[string]any{"max_tokens": float64(512), "temperature": 1},
	)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if requestBody["max_tokens"] != float64(512) {
		t.Fatalf("max_tokens = %v, want 512", requestBody["max_tokens"])
	}
	if requestBody["temperature"] != float64(1) {
		t.Fatalf("temperature = %v, want 1", requestBody["temperature"])
	}
}

// TestModelSentVerbatim replaces TestNormalizeModel_UsesAPIBase.
//
// `normalizeModel` stripped a `<vendor>/` prefix off the model id for a list
// of thirteen hosts before sending it upstream. ADR-067 FR-034 deletes every
// `provider/` prefix convention on a model id: the id is the catalog's, a `/`
// inside it is DATA (`deepseek/deepseek-v3.2` on OpenRouter is one model),
// and the transport must send exactly what it was handed.
//
// The old helper actively corrupted the new ids — it turned
// `deepseek/deepseek-v3.2` into `deepseek-v3.2` for any host that was not
// openrouter.ai, so a correct config produced an upstream 404.
func TestModelSentVerbatim(t *testing.T) {
	for _, model := range []string{
		"deepseek/deepseek-v3.2",
		"z-ai/glm-5.2",
		"openrouter/auto",
		"gpt-4.1",
		"Qwen/Qwen3-235B-A22B-Instruct-2507",
	} {
		t.Run(model, func(t *testing.T) {
			p := mustNewProvider(t, "key", "https://api.deepseek.com/v1", "")
			body := p.buildRequestBody(
				[]Message{{Role: "user", Content: "hi"}}, nil, model, nil)
			if got := body["model"]; got != model {
				t.Errorf("model sent upstream = %v, want %q verbatim", got, model)
			}
		})
	}
}

func TestProvider_RequestTimeoutDefault(t *testing.T) {
	p := mustNewProviderWithMaxTokensFieldAndTimeout(t, "key", "https://example.com/v1", "", "", 0)
	if p.httpClient.Timeout != defaultRequestTimeout {
		t.Fatalf("http timeout = %v, want %v", p.httpClient.Timeout, defaultRequestTimeout)
	}
}

func TestProvider_RequestTimeoutOverride(t *testing.T) {
	p := mustNewProviderWithMaxTokensFieldAndTimeout(t, "key", "https://example.com/v1", "", "", 300)
	if p.httpClient.Timeout != 300*time.Second {
		t.Fatalf("http timeout = %v, want %v", p.httpClient.Timeout, 300*time.Second)
	}
}

func TestProviderChat_ExtraBodyInjected(t *testing.T) {
	var requestBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp := map[string]any{
			"choices": []map[string]any{
				{
					"message":       map[string]any{"content": "ok"},
					"finish_reason": "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	extraBody := map[string]any{"reasoning_split": true, "custom_field": "test"}
	p := mustNewProvider(t, "key", server.URL, "", WithExtraBody(extraBody))

	_, err := p.Chat(
		t.Context(),
		[]Message{{Role: "user", Content: "hi"}},
		nil,
		"minimax/abab7",
		nil,
	)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if got, ok := requestBody["reasoning_split"]; !ok || got != true {
		t.Fatalf("reasoning_split = %v, want true", got)
	}
	if got, ok := requestBody["custom_field"]; !ok || got != "test" {
		t.Fatalf("custom_field = %v, want test", got)
	}
}

func TestProviderChat_ExtraBodyOverridesOptions(t *testing.T) {
	var requestBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp := map[string]any{
			"choices": []map[string]any{
				{
					"message":       map[string]any{"content": "ok"},
					"finish_reason": "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	extraBody := map[string]any{"temperature": 0.9}
	p := mustNewProvider(t, "key", server.URL, "", WithExtraBody(extraBody))

	_, err := p.Chat(
		t.Context(),
		[]Message{{Role: "user", Content: "hi"}},
		nil,
		"gpt-4o",
		map[string]any{"temperature": 0.5},
	)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	// ExtraBody takes precedence over options since it is merged last.
	if got := requestBody["temperature"]; got != float64(0.9) {
		t.Fatalf("temperature = %v, want 0.9 (from extraBody, overriding options)", got)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type errAfterDataReadCloser struct {
	data      []byte
	chunkSize int
	offset    int
}

func (r *errAfterDataReadCloser) Read(p []byte) (int, error) {
	if r.offset >= len(r.data) {
		return 0, io.ErrUnexpectedEOF
	}

	n := r.chunkSize
	if n <= 0 || n > len(p) {
		n = len(p)
	}
	remaining := len(r.data) - r.offset
	if n > remaining {
		n = remaining
	}
	copy(p, r.data[r.offset:r.offset+n])
	r.offset += n
	return n, nil
}

func (r *errAfterDataReadCloser) Close() error {
	return nil
}

func TestProvider_FunctionalOptionMaxTokensField(t *testing.T) {
	p := mustNewProvider(t, "key", "https://example.com/v1", "", WithMaxTokensField("max_completion_tokens"))
	if p.maxTokensField != "max_completion_tokens" {
		t.Fatalf("maxTokensField = %q, want %q", p.maxTokensField, "max_completion_tokens")
	}
}

func TestProvider_FunctionalOptionRequestTimeout(t *testing.T) {
	p := mustNewProvider(t, "key", "https://example.com/v1", "", WithRequestTimeout(45*time.Second))
	if p.httpClient.Timeout != 45*time.Second {
		t.Fatalf("http timeout = %v, want %v", p.httpClient.Timeout, 45*time.Second)
	}
}

func TestProvider_FunctionalOptionRequestTimeoutNonPositive(t *testing.T) {
	p := mustNewProvider(t, "key", "https://example.com/v1", "", WithRequestTimeout(-1*time.Second))
	if p.httpClient.Timeout != defaultRequestTimeout {
		t.Fatalf("http timeout = %v, want %v", p.httpClient.Timeout, defaultRequestTimeout)
	}
}

func TestSerializeMessages_PlainText(t *testing.T) {
	messages := []protocoltypes.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi", ReasoningContent: "thinking..."},
	}
	result := common.SerializeMessages(messages)

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}

	var msgs []map[string]any
	json.Unmarshal(data, &msgs)

	if msgs[0]["content"] != "hello" {
		t.Fatalf("expected plain string content, got %v", msgs[0]["content"])
	}
	if msgs[1]["reasoning_content"] != "thinking..." {
		t.Fatalf("reasoning_content not preserved, got %v", msgs[1]["reasoning_content"])
	}
}

func TestSerializeMessages_WithMedia(t *testing.T) {
	messages := []protocoltypes.Message{
		{Role: "user", Content: "describe this", Media: []string{"data:image/png;base64,abc123"}},
	}
	result := common.SerializeMessages(messages)

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var msgs []map[string]any
	json.Unmarshal(data, &msgs)

	content, ok := msgs[0]["content"].([]any)
	if !ok {
		t.Fatalf("expected array content for media message, got %T", msgs[0]["content"])
	}
	if len(content) != 2 {
		t.Fatalf("expected 2 content parts, got %d", len(content))
	}

	textPart, ok := content[0].(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any content part, got %T", content[0])
	}
	if textPart["type"] != "text" || textPart["text"] != "describe this" {
		t.Fatalf("text part mismatch: %v", textPart)
	}

	imgPart, ok := content[1].(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any content part, got %T", content[1])
	}
	if imgPart["type"] != "image_url" {
		t.Fatalf("expected image_url type, got %v", imgPart["type"])
	}
	imgURL, ok := imgPart["image_url"].(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any image_url, got %T", imgPart["image_url"])
	}
	if imgURL["url"] != "data:image/png;base64,abc123" {
		t.Fatalf("image url mismatch: %v", imgURL["url"])
	}
}

func TestSerializeMessages_MediaWithToolCallID(t *testing.T) {
	messages := []protocoltypes.Message{
		{Role: "tool", Content: "image result", Media: []string{"data:image/png;base64,xyz"}, ToolCallID: "call_1"},
	}
	result := common.SerializeMessages(messages)

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var msgs []map[string]any
	json.Unmarshal(data, &msgs)

	if msgs[0]["tool_call_id"] != "call_1" {
		t.Fatalf("tool_call_id not preserved with media, got %v", msgs[0]["tool_call_id"])
	}
	// Content should be multipart array
	if _, ok := msgs[0]["content"].([]any); !ok {
		t.Fatalf("expected array content, got %T", msgs[0]["content"])
	}
}

// chatWithCacheKey sets up a test server, sends a Chat request with prompt_cache_key,
// and returns the decoded request body for assertion.
func chatWithCacheKey(t *testing.T, apiBase string) map[string]any {
	t.Helper()
	var requestBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp := map[string]any{
			"choices": []map[string]any{
				{
					"message":       map[string]any{"content": "ok"},
					"finish_reason": "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := mustNewProvider(t, "key", server.URL, "")
	p.apiBase = apiBase
	p.httpClient = &http.Client{
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			r.URL, _ = url.Parse(server.URL + r.URL.Path)
			return http.DefaultTransport.RoundTrip(r)
		}),
	}

	_, err := p.Chat(
		t.Context(),
		[]Message{{Role: "user", Content: "hi"}},
		nil,
		"test-model",
		map[string]any{"prompt_cache_key": "agent-main"},
	)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	return requestBody
}

func TestProviderChat_PromptCacheKeySentToOpenAI(t *testing.T) {
	body := chatWithCacheKey(t, "https://api.openai.com/v1")
	if body["prompt_cache_key"] != "agent-main" {
		t.Fatalf("prompt_cache_key = %v, want %q", body["prompt_cache_key"], "agent-main")
	}
}

func TestProviderChat_PromptCacheKeyOmittedForNonOpenAI(t *testing.T) {
	tests := []struct {
		name    string
		apiBase string
	}{
		{"mistral", "https://api.mistral.ai/v1"},
		{"gemini", "https://generativelanguage.googleapis.com/v1beta"},
		{"deepseek", "https://api.deepseek.com/v1"},
		{"groq", "https://api.groq.com/openai/v1"},
		{"minimax", "https://api.minimaxi.com/v1"},
		{"ollama_local", "http://localhost:11434/v1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := chatWithCacheKey(t, tt.apiBase)
			if _, exists := body["prompt_cache_key"]; exists {
				t.Fatalf("prompt_cache_key should NOT be sent to %s, but was included in request", tt.name)
			}
		})
	}
}

func TestSupportsPromptCacheKey(t *testing.T) {
	tests := []struct {
		apiBase string
		want    bool
	}{
		{"https://api.openai.com/v1", true},
		{"https://api.openai.com/v1/", true},
		{"https://myresource.openai.azure.com/openai/deployments/gpt-4", true},
		{"https://eastus.openai.azure.com/v1", true},
		{"https://api.mistral.ai/v1", false},
		{"https://generativelanguage.googleapis.com/v1beta", false},
		{"https://api.deepseek.com/v1", false},
		{"https://api.groq.com/openai/v1", false},
		{"http://localhost:11434/v1", false},
		{"https://openrouter.ai/api/v1", false},
		// Edge cases: proxy URLs with openai.com in path should NOT match
		{"https://my-proxy.com/api.openai.com/v1", false},
		{"https://proxy.example.com/openai.azure.com/v1", false},
		// Malformed or empty
		{"", false},
		{"not-a-url", false},
	}
	for _, tt := range tests {
		if got := supportsPromptCacheKey(tt.apiBase); got != tt.want {
			t.Errorf("supportsPromptCacheKey(%q) = %v, want %v", tt.apiBase, got, tt.want)
		}
	}
}

func TestBuildToolsList_NativeSearchAddsWebSearchPreview(t *testing.T) {
	tools := []ToolDefinition{
		{Type: "function", Function: ToolFunctionDefinition{Name: "read_file", Description: "read"}},
	}
	result := buildToolsList(tools, true)
	if len(result) != 2 {
		t.Fatalf("len(result) = %d, want 2", len(result))
	}
	wsEntry, ok := result[1].(map[string]any)
	if !ok {
		t.Fatalf("web search entry is %T, want map[string]any", result[1])
	}
	if wsEntry["type"] != "web_search_preview" {
		t.Fatalf("type = %v, want web_search_preview", wsEntry["type"])
	}
}

func TestBuildToolsList_NativeSearchFiltersClientWebSearch(t *testing.T) {
	tools := []ToolDefinition{
		{Type: "function", Function: ToolFunctionDefinition{Name: "web_search", Description: "search"}},
		{Type: "function", Function: ToolFunctionDefinition{Name: "read_file", Description: "read"}},
	}
	result := buildToolsList(tools, true)
	for _, entry := range result {
		if td, ok := entry.(ToolDefinition); ok && strings.EqualFold(td.Function.Name, "web_search") {
			t.Fatal("client-side web_search should be filtered out when native search is enabled")
		}
	}
	if len(result) != 2 { // read_file + web_search_preview
		t.Fatalf("len(result) = %d, want 2 (read_file + web_search_preview)", len(result))
	}
}

func TestBuildToolsList_NoNativeSearchPassesThrough(t *testing.T) {
	tools := []ToolDefinition{
		{Type: "function", Function: ToolFunctionDefinition{Name: "web_search", Description: "search"}},
		{Type: "function", Function: ToolFunctionDefinition{Name: "read_file", Description: "read"}},
	}
	result := buildToolsList(tools, false)
	if len(result) != 2 {
		t.Fatalf("len(result) = %d, want 2", len(result))
	}
}

func TestIsNativeSearchHost(t *testing.T) {
	tests := []struct {
		apiBase string
		want    bool
	}{
		{"https://api.openai.com/v1", true},
		{"https://myresource.openai.azure.com/openai/deployments/gpt-4", true},
		{"https://api.mistral.ai/v1", false},
		{"https://api.deepseek.com/v1", false},
		{"https://api.groq.com/openai/v1", false},
		{"http://localhost:11434/v1", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isNativeSearchHost(tt.apiBase); got != tt.want {
			t.Errorf("isNativeSearchHost(%q) = %v, want %v", tt.apiBase, got, tt.want)
		}
	}
}

func TestSupportsNativeSearch_OpenAI(t *testing.T) {
	p := mustNewProvider(t, "key", "https://api.openai.com/v1", "")
	if !p.SupportsNativeSearch() {
		t.Fatal("OpenAI provider should support native search")
	}
}

func TestSupportsNativeSearch_NonOpenAI(t *testing.T) {
	p := mustNewProvider(t, "key", "https://api.deepseek.com/v1", "")
	if p.SupportsNativeSearch() {
		t.Fatal("DeepSeek provider should not support native search")
	}
}

func TestProviderChat_NativeSearchToolInjected(t *testing.T) {
	var requestBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp := map[string]any{
			"choices": []map[string]any{
				{
					"message":       map[string]any{"content": "ok"},
					"finish_reason": "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := mustNewProvider(t, "key", server.URL, "")
	p.apiBase = "https://api.openai.com/v1"
	p.httpClient = &http.Client{
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			r.URL, _ = url.Parse(server.URL + r.URL.Path)
			return http.DefaultTransport.RoundTrip(r)
		}),
	}
	tools := []ToolDefinition{
		{Type: "function", Function: ToolFunctionDefinition{Name: "read_file", Description: "read"}},
	}
	_, err := p.Chat(
		t.Context(),
		[]Message{{Role: "user", Content: "hi"}},
		tools,
		"gpt-5.4",
		map[string]any{"native_search": true},
	)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	toolsRaw, ok := requestBody["tools"].([]any)
	if !ok {
		t.Fatalf("tools is %T, want []any", requestBody["tools"])
	}
	if len(toolsRaw) != 2 {
		t.Fatalf("len(tools) = %d, want 2 (read_file + web_search_preview)", len(toolsRaw))
	}

	lastTool, ok := toolsRaw[1].(map[string]any)
	if !ok {
		t.Fatalf("last tool is %T, want map[string]any", toolsRaw[1])
	}
	if lastTool["type"] != "web_search_preview" {
		t.Fatalf("last tool type = %v, want web_search_preview", lastTool["type"])
	}
}

func TestProviderChat_NativeSearchNotInjectedWithoutOption(t *testing.T) {
	var requestBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp := map[string]any{
			"choices": []map[string]any{
				{
					"message":       map[string]any{"content": "ok"},
					"finish_reason": "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := mustNewProvider(t, "key", server.URL, "")
	tools := []ToolDefinition{
		{Type: "function", Function: ToolFunctionDefinition{Name: "web_search", Description: "search"}},
	}
	_, err := p.Chat(
		t.Context(),
		[]Message{{Role: "user", Content: "hi"}},
		tools,
		"gpt-5.4",
		map[string]any{},
	)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	toolsRaw, ok := requestBody["tools"].([]any)
	if !ok {
		t.Fatalf("tools is %T, want []any", requestBody["tools"])
	}
	if len(toolsRaw) != 1 {
		t.Fatalf("len(tools) = %d, want 1 (web_search only)", len(toolsRaw))
	}
}

// TestProviderChat_NativeSearchIgnoredOnNonOpenAI verifies that when native_search
// is true in options but the provider's apiBase is not OpenAI (e.g. fallback to DeepSeek),
// we do not inject web_search_preview to avoid API errors.
func TestProviderChat_NativeSearchIgnoredOnNonOpenAI(t *testing.T) {
	var requestBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp := map[string]any{
			"choices": []map[string]any{
				{
					"message":       map[string]any{"content": "ok"},
					"finish_reason": "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Use server.URL so host is not api.openai.com — simulates DeepSeek/other provider
	p := mustNewProvider(t, "key", server.URL, "")
	_, err := p.Chat(
		t.Context(),
		[]Message{{Role: "user", Content: "hi"}},
		nil,
		"deepseek-chat",
		map[string]any{"native_search": true},
	)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	// Should not have tools at all (no tools passed, and we must not add web_search_preview)
	if toolsRaw, ok := requestBody["tools"]; ok {
		t.Fatalf("tools should be omitted for non-OpenAI when only native_search was requested, got %v", toolsRaw)
	}
}

// --- parseStreamResponse tests ---

// buildSSEStream constructs a minimal SSE-format body from a slice of data payloads.
// Each payload is rendered as "data: <payload>\n\n". A trailing "data: [DONE]\n\n"
// is appended when done is true.
func buildSSEStream(payloads []string, addDone bool) io.Reader {
	var sb strings.Builder
	for _, p := range payloads {
		sb.WriteString("data: ")
		sb.WriteString(p)
		sb.WriteString("\n\n")
	}
	if addDone {
		sb.WriteString("data: [DONE]\n\n")
	}
	return strings.NewReader(sb.String())
}

// sseChunk constructs a minimal OpenAI-compatible SSE chunk JSON for a text delta.
func sseTextChunk(content string, finishReason *string) string {
	type delta struct {
		Content string `json:"content,omitempty"`
	}
	type choice struct {
		Delta        delta   `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	}
	type chunk struct {
		Choices []choice `json:"choices"`
	}
	c := chunk{Choices: []choice{{Delta: delta{Content: content}, FinishReason: finishReason}}}
	b, err := json.Marshal(c)
	if err != nil {
		// c is built entirely from string/*string fields; Marshal cannot
		// fail for this shape. A panic here means the fixture type changed
		// in a way that broke JSON encoding, which every caller must know
		// about immediately rather than receive a silently empty payload.
		panic(fmt.Sprintf("sseTextChunk: json.Marshal: %v", err))
	}
	return string(b)
}

// strPtr returns a pointer to the given string. Used for FinishReason fields.
func strPtr(s string) *string { return &s }

// TestParseStreamResponse_TextOnly verifies that three text delta chunks plus [DONE]
// produce the correct accumulated text, call onChunk progressively, and set finish_reason "stop".
// Traces to: inferred from parseStreamResponse implementation in provider.go
func TestParseStreamResponse_TextOnly(t *testing.T) {
	// BDD: Given a stream with 3 text delta chunks and [DONE]
	// When parseStreamResponse is called
	// Then accumulated text matches, onChunk called progressively, finish_reason is "stop"
	payloads := []string{
		sseTextChunk("Hello", nil),
		sseTextChunk(", ", nil),
		sseTextChunk("world", strPtr("stop")),
	}
	reader := buildSSEStream(payloads, true)

	var chunks []string
	resp, err := parseStreamResponse(t.Context(), reader, func(acc string) {
		chunks = append(chunks, acc)
	}, nil)
	if err != nil {
		t.Fatalf("parseStreamResponse() error = %v", err)
	}
	if resp.Content != "Hello, world" {
		t.Fatalf("Content = %q, want %q", resp.Content, "Hello, world")
	}
	if resp.FinishReason != "stop" {
		t.Fatalf("FinishReason = %q, want %q", resp.FinishReason, "stop")
	}
	if len(chunks) != 3 {
		t.Fatalf("onChunk called %d times, want 3", len(chunks))
	}
	if chunks[0] != "Hello" {
		t.Fatalf("chunks[0] = %q, want %q", chunks[0], "Hello")
	}
	if chunks[1] != "Hello, " {
		t.Fatalf("chunks[1] = %q, want %q", chunks[1], "Hello, ")
	}
	if chunks[2] != "Hello, world" {
		t.Fatalf("chunks[2] = %q, want %q", chunks[2], "Hello, world")
	}
}

// TestParseStreamResponse_ToolCallDeltas verifies that incremental tool call chunks
// (index, id, name, argument fragments) are reassembled into a single tool call with
// the correct name and parsed arguments.
// Traces to: inferred from parseStreamResponse implementation in provider.go
func TestParseStreamResponse_ToolCallDeltas(t *testing.T) {
	// BDD: Given a stream with tool call chunks for index 0
	// When parseStreamResponse is called
	// Then the tool call has correct name and parsed arguments
	type tcDelta struct {
		Index    int    `json:"index"`
		ID       string `json:"id,omitempty"`
		Function *struct {
			Name      string `json:"name,omitempty"`
			Arguments string `json:"arguments,omitempty"`
		} `json:"function,omitempty"`
	}
	type delta struct {
		ToolCalls []tcDelta `json:"tool_calls,omitempty"`
	}
	type choice struct {
		Delta        delta   `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	}
	type chunk struct {
		Choices []choice `json:"choices"`
	}

	marshalChunk := func(tc tcDelta, fr *string) string {
		c := chunk{Choices: []choice{{Delta: delta{ToolCalls: []tcDelta{tc}}, FinishReason: fr}}}
		b, err := json.Marshal(c)
		if err != nil {
			t.Fatalf("marshalChunk: json.Marshal: %v", err)
		}
		return string(b)
	}

	fnPtr := func(name, args string) *struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} {
		return &struct {
			Name      string `json:"name,omitempty"`
			Arguments string `json:"arguments,omitempty"`
		}{Name: name, Arguments: args}
	}

	payloads := []string{
		marshalChunk(tcDelta{Index: 0, ID: "call_abc", Function: fnPtr("get_weather", "")}, nil),
		marshalChunk(tcDelta{Index: 0, Function: fnPtr("", `{"city":`)}, nil),
		marshalChunk(tcDelta{Index: 0, Function: fnPtr("", `"SF"}`)}, strPtr("tool_calls")),
	}
	reader := buildSSEStream(payloads, true)

	resp, err := parseStreamResponse(t.Context(), reader, nil, nil)
	if err != nil {
		t.Fatalf("parseStreamResponse() error = %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("len(ToolCalls) = %d, want 1", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "call_abc" {
		t.Fatalf("ToolCall.ID = %q, want %q", tc.ID, "call_abc")
	}
	if tc.Name != "get_weather" {
		t.Fatalf("ToolCall.Name = %q, want %q", tc.Name, "get_weather")
	}
	if tc.Arguments["city"] != "SF" {
		t.Fatalf("ToolCall.Arguments[city] = %v, want %q", tc.Arguments["city"], "SF")
	}
}

// TestParseStreamResponse_MultipleToolCalls verifies that two tool calls at index 0 and 1
// are both returned in the response.
// Traces to: inferred from parseStreamResponse implementation in provider.go
func TestParseStreamResponse_MultipleToolCalls(t *testing.T) {
	// BDD: Given a stream with tool call chunks for index 0 and 1
	// When parseStreamResponse is called
	// Then both tool calls are returned
	type tcDelta struct {
		Index    int    `json:"index"`
		ID       string `json:"id,omitempty"`
		Function *struct {
			Name      string `json:"name,omitempty"`
			Arguments string `json:"arguments,omitempty"`
		} `json:"function,omitempty"`
	}
	type delta struct {
		ToolCalls []tcDelta `json:"tool_calls,omitempty"`
	}
	type choice struct {
		Delta        delta   `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	}
	type chunkT struct {
		Choices []choice `json:"choices"`
	}

	fnPtr := func(name, args string) *struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} {
		return &struct {
			Name      string `json:"name,omitempty"`
			Arguments string `json:"arguments,omitempty"`
		}{Name: name, Arguments: args}
	}

	marshalChunks := func(tcs []tcDelta, fr *string) string {
		c := chunkT{Choices: []choice{{Delta: delta{ToolCalls: tcs}, FinishReason: fr}}}
		b, err := json.Marshal(c)
		if err != nil {
			t.Fatalf("marshalChunks: json.Marshal: %v", err)
		}
		return string(b)
	}

	payloads := []string{
		marshalChunks([]tcDelta{
			{Index: 0, ID: "call_0", Function: fnPtr("tool_alpha", `{"x":1}`)},
			{Index: 1, ID: "call_1", Function: fnPtr("tool_beta", `{"y":2}`)},
		}, strPtr("tool_calls")),
	}
	reader := buildSSEStream(payloads, true)

	resp, err := parseStreamResponse(t.Context(), reader, nil, nil)
	if err != nil {
		t.Fatalf("parseStreamResponse() error = %v", err)
	}
	if len(resp.ToolCalls) != 2 {
		t.Fatalf("len(ToolCalls) = %d, want 2", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "tool_alpha" {
		t.Fatalf("ToolCalls[0].Name = %q, want %q", resp.ToolCalls[0].Name, "tool_alpha")
	}
	if resp.ToolCalls[1].Name != "tool_beta" {
		t.Fatalf("ToolCalls[1].Name = %q, want %q", resp.ToolCalls[1].Name, "tool_beta")
	}
}

// TestParseStreamResponse_MalformedChunkSkipped verifies that an invalid JSON line between
// valid chunks is skipped and text still accumulates correctly with no error returned.
// Traces to: inferred from parseStreamResponse implementation in provider.go
func TestParseStreamResponse_MalformedChunkSkipped(t *testing.T) {
	// BDD: Given a stream with one invalid JSON line between valid chunks
	// When parseStreamResponse is called
	// Then text accumulates correctly and no error is returned
	payloads := []string{
		sseTextChunk("good", nil),
		`{bad json :::`,
		sseTextChunk(" chunk", strPtr("stop")),
	}
	reader := buildSSEStream(payloads, true)

	resp, err := parseStreamResponse(t.Context(), reader, nil, nil)
	if err != nil {
		t.Fatalf("parseStreamResponse() error = %v", err)
	}
	if resp.Content != "good chunk" {
		t.Fatalf("Content = %q, want %q", resp.Content, "good chunk")
	}
}

// TestParseStreamResponse_ContextCancellation verifies that canceling the context after
// the first chunk returns context.Canceled.
// Traces to: inferred from parseStreamResponse implementation in provider.go
func TestParseStreamResponse_ContextCancellation(t *testing.T) {
	// BDD: Given a context that gets canceled after the first chunk
	// When parseStreamResponse processes the stream
	// Then context.Canceled error is returned
	// Build a stream that will block after the first chunk using a pipe.
	pr, pw := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())

	// Write first chunk, delay, cancel context, then close pipe.
	go func() {
		firstChunk := sseTextChunk("first", nil)
		fmt.Fprintf(pw, "data: %s\n\n", firstChunk)
		time.Sleep(20 * time.Millisecond)
		cancel()
		time.Sleep(20 * time.Millisecond)
		pw.Close()
	}()

	resp, err := parseStreamResponse(ctx, pr, nil, nil)
	// Race between context cancel and pipe EOF — both outcomes are valid:
	// - context.Canceled if ctx check fires before EOF
	// - nil error with partial content if EOF arrives first
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled or nil", err)
		}
		return
	}
	if resp.Content != "first" {
		t.Fatalf("Content = %q, want %q", resp.Content, "first")
	}
}

// TestParseStreamResponse_EmptyStream verifies that a stream containing only [DONE]
// produces empty content, no error, and finish_reason "stop".
// Traces to: inferred from parseStreamResponse implementation in provider.go
func TestParseStreamResponse_EmptyStream(t *testing.T) {
	// BDD: Given a stream with only [DONE]
	// When parseStreamResponse is called
	// Then empty content, no error, finish_reason "stop"
	reader := buildSSEStream(nil, true)

	resp, err := parseStreamResponse(t.Context(), reader, nil, nil)
	if err != nil {
		t.Fatalf("parseStreamResponse() error = %v", err)
	}
	if resp.Content != "" {
		t.Fatalf("Content = %q, want empty", resp.Content)
	}
	if resp.FinishReason != "stop" {
		t.Fatalf("FinishReason = %q, want %q", resp.FinishReason, "stop")
	}
}

// TestParseStreamResponse_FinishReasonUnknown verifies that a stream with text content but
// no finish_reason in any chunk and no [DONE] returns finish_reason "unknown".
// Traces to: inferred from parseStreamResponse implementation in provider.go (fix 14)
func TestParseStreamResponse_FinishReasonUnknown(t *testing.T) {
	// BDD: Given a stream with text but NO finish_reason and no [DONE]
	// When parseStreamResponse is called
	// Then finish_reason is "unknown"
	payloads := []string{
		sseTextChunk("partial", nil),
	}
	// Do NOT add [DONE] and do NOT provide a finish_reason.
	reader := buildSSEStream(payloads, false)

	resp, err := parseStreamResponse(t.Context(), reader, nil, nil)
	if err != nil {
		t.Fatalf("parseStreamResponse() error = %v", err)
	}
	if resp.FinishReason != "unknown" {
		t.Fatalf("FinishReason = %q, want %q", resp.FinishReason, "unknown")
	}
}

// TestParseStreamResponse_UsageInFinalChunk verifies that a usage object in the last
// chunk is captured in the response.
// Traces to: inferred from parseStreamResponse implementation in provider.go
func TestParseStreamResponse_UsageInFinalChunk(t *testing.T) {
	// BDD: Given a stream with a "usage" object in the last chunk
	// When parseStreamResponse is called
	// Then usage is captured in the response
	type choice struct {
		Delta        struct{} `json:"delta"`
		FinishReason *string  `json:"finish_reason"`
	}
	type usageT struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	}
	type chunkWithUsage struct {
		Choices []choice `json:"choices"`
		Usage   *usageT  `json:"usage,omitempty"`
	}

	textChunk := sseTextChunk("hello", strPtr("stop"))

	finalChunk := chunkWithUsage{
		Choices: []choice{{Delta: struct{}{}, FinishReason: nil}},
		Usage: &usageT{
			PromptTokens:     10,
			CompletionTokens: 5,
			TotalTokens:      15,
		},
	}
	finalChunkJSON, err := json.Marshal(finalChunk)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	payloads := []string{
		textChunk,
		string(finalChunkJSON),
	}
	reader := buildSSEStream(payloads, true)

	resp, err := parseStreamResponse(t.Context(), reader, nil, nil)
	if err != nil {
		t.Fatalf("parseStreamResponse() error = %v", err)
	}
	if resp.Usage == nil {
		t.Fatal("Usage is nil, expected usage to be captured")
	}
	if resp.Usage.PromptTokens != 10 {
		t.Fatalf("Usage.PromptTokens = %d, want 10", resp.Usage.PromptTokens)
	}
	if resp.Usage.CompletionTokens != 5 {
		t.Fatalf("Usage.CompletionTokens = %d, want 5", resp.Usage.CompletionTokens)
	}
	if resp.Usage.TotalTokens != 15 {
		t.Fatalf("Usage.TotalTokens = %d, want 15", resp.Usage.TotalTokens)
	}
}

func TestSerializeMessages_StripsSystemParts(t *testing.T) {
	messages := []protocoltypes.Message{
		{
			Role:    "system",
			Content: "you are helpful",
			SystemParts: []protocoltypes.ContentBlock{
				{Type: "text", Text: "you are helpful"},
			},
		},
	}
	result := common.SerializeMessages(messages)

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	raw := string(data)
	if strings.Contains(raw, "system_parts") {
		t.Fatal("system_parts should not appear in serialized output")
	}
}
