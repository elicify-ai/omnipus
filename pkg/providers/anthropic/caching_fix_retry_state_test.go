package anthropicprovider

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// Boundary-state regression: caller-supplied SDK clients may own an internal
// retry policy. A prior failed HTTP attempt must not replace the final result.
// Production factory clients still disable SDK retries (D13's existing test).
func TestCachingFixSDKRetryKeepsOnlyFinalAttempt(t *testing.T) {
	const success = `{"id":"fixture","type":"message","role":"assistant","content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`
	const denied = `{"type":"error","error":{"type":"permission_error","message":"final denial"}}`
	const streamSuccess = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"fixture\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"fixture\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"OK\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	transportErr := errors.New("fixture final transport failure")
	for _, streaming := range []bool{false, true} {
		for _, final := range []string{"success", "http_failure", "transport_failure"} {
			name := "plain/" + final
			if streaming {
				name = "stream/" + final
			}
			t.Run(name, func(t *testing.T) {
				calls := 0
				client := anthropic.NewClient(option.WithAPIKey("fixture"), option.WithAuthToken(""), option.WithMaxRetries(1), option.WithHTTPClient(&http.Client{Transport: cachingFixTransport(func(r *http.Request) (*http.Response, error) {
					calls++
					status, body, kind, id := 500, `{"type":"error","error":{"type":"api_error","message":"first failed attempt"}}`, "application/json", "first-id"
					if calls > 1 {
						switch final {
						case "transport_failure":
							return nil, transportErr
						case "http_failure":
							status, body, id = 403, denied, "final-id"
						case "success":
							status, body = 200, success
							if streaming {
								body, kind = streamSuccess, "text/event-stream"
							}
						}
					}
					return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {kind}, "Request-Id": {id}, "Retry-After": {"0.001"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
				})}))
				p := NewProviderWithClient(&client)
				var response *LLMResponse
				var err error
				if streaming {
					response, err = p.ChatStream(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "fixture", nil, nil, nil)
				} else {
					response, err = p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "fixture", nil)
				}
				if calls != 2 {
					t.Fatalf("SDK retry control made%d calls, want2", calls)
				}
				var pe *common.ProviderError
				switch final {
				case "success":
					if err != nil || response == nil || response.Content != "OK" {
						t.Fatalf("earlier SDK failure masked final success: response=%+v err=%v", response, err)
					}
				case "http_failure":
					if response != nil || !errors.As(err, &pe) || pe.Status != 403 || pe.Body != denied || pe.RequestID != "final-id" {
						t.Fatalf("lost final HTTP failure facts: response=%+v err=%v", response, err)
					}
				case "transport_failure":
					if response != nil || !errors.Is(err, transportErr) || errors.As(err, &pe) {
						t.Fatalf("stale HTTP failure replaced final transport failure: response=%+v err=%v", response, err)
					}
				}
			})
		}
	}
}
