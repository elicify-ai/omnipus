package providers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers/common"
	"github.com/openai/openai-go/v3"
)

// R2's oracle is the HTTP response itself. Real Codex adapter and SDK, fake
// HTTP service only; no real token source, credentials, or provider requests.
func TestRouting1340_CodexStructuredErrors(t *testing.T) {
	for _, status := range []int{400, 401, 402, 404, 408, 429, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			const body = `{"error":{"code":"model_not_found","type":"invalid_request_error","message":"the model does not exist"}}`
			var calls atomic.Int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Request-Id", "routing1340-request")
				w.Header().Set("Retry-After", "7")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(body))
			}))
			t.Cleanup(srv.Close)
			p := newCodexProviderWithBaseURL("test-token", "test-account", srv.URL)
			resp, err := p.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, "gpt-6.1-sol", nil)
			if resp != nil || err == nil || calls.Load() != 1 {
				t.Fatalf("expected one failed Codex HTTP call: resp=%+v err=%v calls=%d", resp, err, calls.Load())
			}
			var pe *common.ProviderError
			if !errors.As(err, &pe) {
				t.Fatalf("Codex returned %T instead of structured provider error: %v", err, err)
			}
			if pe.Status != status || common.StructuredErrorCode(pe.Body) != "model_not_found" || pe.RequestID != "routing1340-request" || pe.RetryAfterSeconds != 7 {
				t.Errorf("boundary facts lost: %+v", pe)
			}
			var sdkErr *openai.Error
			if !errors.As(pe, &sdkErr) || sdkErr.StatusCode != status || sdkErr.Code != "model_not_found" {
				t.Errorf("SDK cause must remain reachable: %v", err)
			}
		})
	}
}

// R3: wrong-model evidence on400 stops this request. Other statuses and
// existing400 overrides retain their independent routing decisions.
func TestRouting1340_Classification(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		body      string
		want      FailoverReason
		retriable bool
	}{
		{"bad_model_code", 400, `{"error":{"code":"model_not_found"}}`, "bad_model", false},
		{"bad_model_message", 400, `{"error":{"message":"Model 'missing' not found"}}`, "bad_model", false},
		{"bad_model_named_unquoted", 400, `{"error":{"message":"Model requested-model not found"}}`, "bad_model", false},
		{"invalid_model_id", 400, `{"error":{"message":"missing is not a valid model ID"}}`, "bad_model", false},
		{"not_offered", 400, `{"error":{"message":"Model 'missing' is not available on this provider"}}`, "bad_model", false},
		{"empty400", 400, "", FailoverFormat, false},
		{"arbitrary400", 400, `{"error":{"message":"account header missing"}}`, FailoverFormat, false},
		{"unrelated_not_found", 400, `{"error":{"message":"file not found for model input"}}`, FailoverFormat, false},
		{"model_response_missing", 400, `{"error":{"message":"model response not found"}}`, FailoverFormat, false},
		{"model_input_missing", 400, `{"error":{"message":"model input not found"}}`, FailoverFormat, false},
		{"model_input_format", 400, `{"error":{"message":"model does not support image input"}}`, FailoverFormat, false},
		{"context_override", 400, `{"error":{"message":"context length exceeded"}}`, FailoverContextOverflow, false},
		{"billing_override", 400, `{"error":{"message":"credit balance is too low"}}`, FailoverBilling, true},
		{"auth_status_wins", 401, `{"error":{"code":"model_not_found"}}`, FailoverAuth, true},
		{"forbidden", 403, "", FailoverAuth, true},
		{"billing", 402, "", FailoverBilling, true},
		{"quota", 429, `{"error":{"code":"insufficient_quota"}}`, FailoverBilling, true},
		{"rate_limit", 429, `{"error":{"code":"model_not_found"}}`, FailoverRateLimit, true},
		{"timeout", 408, "", FailoverTimeout, true},
		{"upstream", 500, `{"error":{"code":"model_not_found"}}`, FailoverTimeout, true},
		{"not_found404_unchanged", 404, `{"error":{"code":"model_not_found"}}`, FailoverUnknown, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := fmt.Errorf("wrapped: %w", &common.ProviderError{Status: tc.status, Body: tc.body})
			got := ClassifyError(err, "openrouter", "requested-model")
			if got == nil {
				t.Fatal("known HTTP failure was not classified")
			}
			if got.Reason != tc.want || got.Status != tc.status || got.Provider != "openrouter" || got.Model != "requested-model" || got.IsRetriable() != tc.retriable {
				t.Errorf("classification=%+v retriable=%t; want reason=%s status=%d retriable=%t", got, got.IsRetriable(), tc.want, tc.status, tc.retriable)
			}
		})
	}
}

func TestRouting1340_UnquotedSelectedModel(t *testing.T) {
	got := ClassifyError(&common.ProviderError{Status: 400, Body: `{"error":{"message":"Model gpt-6.1 not found"}}`}, "openai-chatgpt", "gpt-6.1")
	if got == nil || got.Reason != FailoverReason("bad_model") || got.IsRetriable() {
		t.Fatalf("positive evidence naming the requested model must be bad_model: %+v", got)
	}
}

func TestRouting1340_TypedClassification(t *testing.T) {
	cause := &FailoverError{Reason: "bad_model", Status: 400, Provider: "openrouter", Model: "missing"}
	got := ClassifyError(fmt.Errorf("wrapped: %w", cause), "", "")
	if got == nil || got.Reason != FailoverReason("bad_model") || got.Status != 400 || got.Provider != "openrouter" || got.Model != "missing" || got.IsRetriable() {
		t.Fatalf("typed routing verdict was lost on reclassification: %+v", got)
	}
}

func TestRouting1340_BadModelFailsFast(t *testing.T) {
	var attempted []string
	fc := NewFallbackChain(NewCooldownTracker())
	result, err := fc.Execute(context.Background(), []FallbackCandidate{
		{Provider: "openrouter", Model: "bad"}, {Provider: "other", Model: "good"},
	}, func(_ context.Context, provider, model string) (*LLMResponse, error) {
		attempted = append(attempted, provider+"/"+model)
		if provider == "other" {
			return &LLMResponse{Content: "must not fall through"}, nil
		}
		return nil, &common.ProviderError{Status: 400, Body: `{"error":{"code":"model_not_found"}}`}
	})
	var fe *FailoverError
	if result != nil || !errors.As(err, &fe) || fe.Reason != FailoverReason("bad_model") || fe.IsRetriable() {
		t.Fatalf("wrong model must fail fast as typed bad_model, result=%+v err=%v", result, err)
	}
	if !reflect.DeepEqual(attempted, []string{"openrouter/bad"}) {
		t.Fatalf("wrong model called another provider: %v", attempted)
	}
}
