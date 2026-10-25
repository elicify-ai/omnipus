package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// SFH F3/F9 and CR F2: independent wrong-model evidence is not inferred
// from incidental credential/rate/transport words or attachment restrictions.
func TestRouting1340_ReviewClassificationEvidence(t *testing.T) {
	cases := []struct {
		name, body, model string
		want              FailoverReason
	}{
		{"explicit_expired_name", `{"error":{"code":"model_not_found","message":"Model 'expired-model' not found"}}`, "expired-model", FailoverReason("bad_model")},
		{"explicit_timeout_word", `{"error":{"code":"model_not_found","message":"model timeout-model not found"}}`, "timeout-model", FailoverReason("bad_model")},
		{"explicit_rate_word", `{"error":{"code":"model_not_found","message":"rate limit for missing model"}}`, "missing", FailoverReason("bad_model")},
		{"explicit_media_word", `{"error":{"code":"model_not_found","message":"image dimensions exceed max for model"}}`, "missing", FailoverReason("bad_model")},
		{"json_double_quotes", `{"error":{"message":"Model \"missing\" not found"}}`, "missing", FailoverReason("bad_model")},
		{"json_single_quotes", `{"error":{"message":"Model 'missing' not found"}}`, "missing", FailoverReason("bad_model")},
		{"plain_text", `Model "missing" not found`, "missing", FailoverReason("bad_model")},
		{"malformed_json_text", `{ Model "missing" not found`, "missing", FailoverReason("bad_model")},
		{"image_capability", `{"error":{"message":"Model 'm' is not available for image input"}}`, "m", FailoverFormat},
		{"pdf_capability", `{"error":{"message":"Model 'm' is not available with PDF attachments"}}`, "m", FailoverFormat},
		{"unquoted_capability", `{"error":{"message":"model m is not available for image input"}}`, "m", FailoverFormat},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cause := &common.ProviderError{Status: 400, Body: tc.body}
			got := ClassifyError(cause, "openrouter", tc.model)
			if got == nil || got.Reason != tc.want || got.Status != 400 {
				t.Fatalf("classification=%+v, want %s/status400", got, tc.want)
			}
			// Spec change (founder Q70, 2026-10-24): bad_model is now
			// retriable (falls back to the backup, rule 1); format stays
			// terminal (rule 4). Evidence classification itself is unchanged.
			if wantRetriable := tc.want != FailoverFormat; got.IsRetriable() != wantRetriable {
				t.Fatalf("retriability=%t, want %t for %s", got.IsRetriable(), wantRetriable, tc.want)
			}
		})
	}
}

// Spec change (founder Q70, 2026-10-24): OLD —
// TestRouting1340_ExplicitBadModelNeverFallsBackOnProse required structured
// model_not_found evidence to STOP the chain (bad_model was terminal), whatever
// incidental words rode along. NEW — the classifier verdict is unchanged
// (still bad_model; incidental words neither create nor destroy the verdict),
// and the chain now moves to the backup, which answers (rule 1).
func TestRouting1340_ExplicitBadModelEvidenceFallsBackToBackup(t *testing.T) {
	for _, word := range []string{"expired", "timeout", "rate limit"} {
		t.Run(word, func(t *testing.T) {
			// Evidence oracle (unchanged): the structured code, not the
			// incidental word, decides the verdict.
			body := `{"error":{"code":"model_not_found","message":"` + word + ` model not found"}}`
			fe := ClassifyError(&common.ProviderError{Status: 400, Body: body}, "openrouter", "missing")
			if fe == nil || fe.Reason != FailoverReason("bad_model") {
				t.Fatalf("structured verdict lost for %q: %+v", word, fe)
			}
			// Chain oracle (NEW): that same verdict now falls back (rule 1).
			var attempted []string
			result, err := NewFallbackChain(NewCooldownTracker()).Execute(context.Background(), []FallbackCandidate{{Provider: "openrouter", Model: "missing"}, {Provider: "other", Model: "good"}}, func(_ context.Context, p, m string) (*LLMResponse, error) {
				attempted = append(attempted, p+"/"+m)
				if p == "other" {
					return &LLMResponse{Content: "answered by the backup"}, nil
				}
				return nil, &common.ProviderError{Status: 400, Body: body}
			})
			if err != nil || result == nil || result.Response == nil || result.Response.Content != "answered by the backup" || result.Provider != "other" {
				t.Fatalf("explicit bad_model evidence must fall back to the backup: result=%+v err=%v", result, err)
			}
			if !reflect.DeepEqual(attempted, []string{"openrouter/missing", "other/good"}) {
				t.Fatalf("chain must try the primary, then the backup: %v", attempted)
			}
		})
	}
}

// PTA F4: the actual Codex adapter/SDK404 crosses the real classifier and
// declared chain into a distinct HTTP client; order is observed at HTTP edges.
func TestRouting1340_Codex404ThenNamedFallback(t *testing.T) {
	var mu sync.Mutex
	var order []string
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		} // not-wire-format: vendor request inspection
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode Codex request: %v", err)
		}
		mu.Lock()
		order = append(order, "openai-chatgpt/"+body.Model)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"error":{"code":"model_not_found","message":"not offered here"}}`))
	}))
	t.Cleanup(first.Close)
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		} // not-wire-format: vendor request inspection
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode fallback request: %v", err)
		}
		mu.Lock()
		order = append(order, "openrouter/"+body.Model)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"fallback-answer"},"finish_reason":"stop"}]}`))
	}))
	t.Cleanup(second.Close)
	fallback, err := NewHTTPProviderWithTimeouts("test-key", second.URL, "", "", 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	clients := map[string]LLMProvider{"openai-chatgpt": newCodexProviderWithBaseURL("test-key", "test-account", first.URL), "openrouter": fallback}
	result, err := NewFallbackChain(NewCooldownTracker()).Execute(context.Background(), []FallbackCandidate{{Provider: "openai-chatgpt", Model: "gpt-6.1-sol"}, {Provider: "openrouter", Model: "z-ai/glm-5.2"}}, func(ctx context.Context, p, m string) (*LLMResponse, error) {
		return clients[p].Chat(ctx, []Message{{Role: "user", Content: "hi"}}, nil, m, nil)
	})
	if err != nil || result == nil || result.Response == nil || result.Response.Content != "fallback-answer" || result.Provider != "openrouter" || result.Model != "z-ai/glm-5.2" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(order, []string{"openai-chatgpt/gpt-6.1-sol", "openrouter/z-ai/glm-5.2"}) {
		t.Fatalf("request order=%v", order)
	}
	if len(result.Attempts) != 1 || result.Attempts[0].Reason != FailoverUnknown {
		t.Fatalf("404 must retain existing fallback verdict: %+v", result.Attempts)
	}
}
