package anthropicprovider

// Supplemental fix-round regressions; CHECK must audit these tests.
// Oracles: dispatcher findings 1/3/4/5, existing 8 KiB error cap and 512B preview.
// Only the external HTTP transport is fake; the adapter and SDK remain real.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

type cachingFixTransport func(*http.Request) (*http.Response, error)

func (f cachingFixTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type cachingFixBody struct {
	io.Reader
	read   int
	closed bool
}

func (b *cachingFixBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	b.read += n
	return n, err
}
func (b *cachingFixBody) Close() error { b.closed = true; return nil }

func TestCachingFixSDKErrorBoundary(t *testing.T) {
	// Sizes straddle both independent limits. JSON is valid before bounding.
	for _, size := range []int{511, 512, 513, 8191, 8192, 8193, 12 * 1024} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("bytes_%d/stream_%t", size, stream), func(t *testing.T) {
				const prefix = `{"type":"error","error":{"type":"rate_limit_error","message":"`
				const suffix = `"}}`
				raw := prefix + strings.Repeat("X", size-len(prefix)-len(suffix)) + suffix
				body := &cachingFixBody{Reader: strings.NewReader(raw)}
				client := anthropic.NewClient(option.WithMaxRetries(0), option.WithAPIKey("fixture"), option.WithAuthToken(""), option.WithHTTPClient(&http.Client{Transport: cachingFixTransport(func(r *http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: 429, Header: http.Header{"Content-Type": {"application/json"}, "Retry-After": {"60"}, "X-Request-Id": {"req-boundary-fixture"}}, Body: body, Request: r}, nil
				})}))
				p := NewProviderWithClient(&client)
				var response *LLMResponse
				var err error
				if stream {
					response, err = p.ChatStream(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "fixture", nil, nil, nil)
				} else {
					response, err = p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "fixture", nil)
				}
				if err == nil || response != nil {
					t.Fatalf("HTTP 429 must remain a failure: response=%+v error=%v", response, err)
				}
				var pe *common.ProviderError
				if !errors.As(err, &pe) {
					t.Fatalf("missing common.ProviderError at SDK boundary: %T", err)
				}
				if pe.Status != 429 || pe.RetryAfterSeconds != 60 || pe.RequestID != "req-boundary-fixture" || pe.ContentType != "application/json" {
					t.Fatalf("lost HTTP retry/correlation facts: %+v", pe)
				}
				wantBody := raw[:min(size, 8192)]
				wantPreview := raw
				if size > 512 {
					wantPreview = raw[:512] + "..."
				}
				if pe.Body != wantBody || pe.BodyPreview != wantPreview || pe.BodyTruncated != (size >= 8192) {
					t.Errorf("bounded body/preview/truncation=%d/%d/%t, want %d/%d/%t", len(pe.Body), len(pe.BodyPreview), pe.BodyTruncated, len(wantBody), len(wantPreview), size >= 8192)
				}
				if body.read != min(size, 8192) || !body.closed {
					t.Errorf("upstream body read=%d closed=%t, want %d/true", body.read, body.closed, min(size, 8192))
				}
				var sdkError *anthropic.Error
				if !errors.As(err, &sdkError) || sdkError.StatusCode != 429 || len(sdkError.RawJSON()) > 8192 {
					t.Errorf("SDK HTTP cause absent or retains an oversized raw error: %T", err)
				}
			})
		}
	}
}

func TestCachingFixDecodedMaxTokens(t *testing.T) {
	for _, limit := range []int{256, 20000} {
		for _, numeric := range []string{"decoded", "int64", "int"} {
			t.Run(fmt.Sprintf("%s_%d", numeric, limit), func(t *testing.T) {
				opts := map[string]any{"max_tokens": limit}
				switch numeric {
				case "decoded":
					if err := json.Unmarshal([]byte(fmt.Sprintf(`{"max_tokens":%d}`, limit)), &opts); err != nil {
						t.Fatal(err)
					}
				case "int64":
					opts["max_tokens"] = int64(limit)
				}
				captured := make(chan int, 1)
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var request struct {
						MaxTokens int `json:"max_tokens"`
					}
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
					}
					captured <- request.MaxTokens
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"id":"fixture","type":"message","role":"assistant","content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
				}))
				t.Cleanup(srv.Close)
				response, err := NewProviderWithBaseURL("fixture", srv.URL).Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "fixture", opts)
				if err != nil || response == nil || response.Content != "OK" {
					t.Fatalf("healthy control: response=%+v err=%v", response, err)
				}
				if got := <-captured; got != limit {
					t.Fatalf("decoded hook limit on wire=%d, want exactly %d", got, limit)
				}
			})
		}
	}
}

func TestCachingFixFailedStreamKeepsUsage(t *testing.T) {
	const opening = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"fixture\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"fixture\",\"usage\":{\"input_tokens\":100,\"output_tokens\":0,\"cache_read_input_tokens\":800,\"cache_creation_input_tokens\":100,\"cache_creation\":{\"ephemeral_5m_input_tokens\":40,\"ephemeral_1h_input_tokens\":60}}}}\n\n"
	const delta = "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{},\"usage\":{\"input_tokens\":1,\"cache_read_input_tokens\":2,\"cache_creation_input_tokens\":3,\"output_tokens\":7}}\n\n"
	cases := []struct{ name, ending string }{
		{"error_event", "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"fixture overload\"}}\n\n"},
		{"early_eof", ""},
		{"invalid_block", "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":9,\"delta\":{\"type\":\"text_delta\",\"text\":\"invalid\"}}\n\n"},
	}
	want := UsageInfo{PromptTokens: 100, CompletionTokens: 7, CacheReadTokens: 800, CacheWriteTokens: 100, CacheWrite5mTokens: 40, CacheWrite1hTokens: 60, TotalTokens: 1007}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := anthropicScriptedStream(t, opening, delta, tc.ending)
			response, err := p.ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, "fixture", nil, nil, nil)
			if err == nil || response != nil {
				t.Fatalf("failed stream became an answer: %+v / %v", response, err)
			}
			var billed *common.StreamProviderError
			if !errors.As(err, &billed) || billed.Usage == nil {
				t.Fatalf("failed stream discarded received message_start usage: %T / %v", err, err)
			}
			if !reflect.DeepEqual(*billed.Usage, want) {
				t.Fatalf("billed usage=%+v, want opening input/cache and latest output %+v", *billed.Usage, want)
			}
		})
	}
}
