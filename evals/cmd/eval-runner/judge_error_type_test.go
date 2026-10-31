package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Security finding F4 (CI MiniMax switch, 2026-10-29): the judge surfaces a
// provider error identifier only from a fixed allowlist; anything else is
// "other". A canary planted in error.type or error.code must never reach the
// error text, which flows into logs and the committed JSONL results.

const errorTypeCanary = "sk-canary-7f3e1d"

func TestProviderErrorTypeAllowlist(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"anthropic known type", `{"type":"error","error":{"type":"overloaded_error","message":"x"}}`, "overloaded_error"},
		{"openai known code", `{"error":{"code":"rate_limit_exceeded","message":"x"}}`, "rate_limit_exceeded"},
		{"canary in type", `{"error":{"type":"` + errorTypeCanary + `"}}`, "other"},
		{"canary in string code", `{"error":{"code":"` + errorTypeCanary + `"}}`, "other"},
		{"numeric code only", `{"error":{"code":429}}`, "other"},
		{"error is a string", `{"error":"` + errorTypeCanary + `"}`, "other"},
		{"no error field", `{"ok":true}`, ""},
		{"not json", `<html>` + errorTypeCanary + `</html>`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := providerErrorType([]byte(tc.body))
			if got != tc.want {
				t.Fatalf("providerErrorType(%s) = %q, want %q", tc.body, got, tc.want)
			}
		})
	}
}

// TestJudgeErrorNeverCarriesACanaryErrorType drives the real judge call with
// both endpoints answering a canary in error.type / error.code.
func TestJudgeErrorNeverCarriesACanaryErrorType(t *testing.T) {
	clearModelKeyEnv(t)
	t.Setenv("MINIMAX_API_KEY_CI", fakeMinimaxKey)
	t.Setenv("OPENROUTER_API_KEY_EVAL", fakeOpenRouterKey)

	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"` + errorTypeCanary + `"}}`))
	}))
	t.Cleanup(primary.Close)
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":{"code":"` + errorTypeCanary + `"}}`))
	}))
	t.Cleanup(fallback.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	p, f := judgeSides()
	_, err := callJudgeWithFallback(ctx, p, f, "score", JudgeEndpoints{
		PrimaryMessagesURL:         primary.URL,
		FallbackChatCompletionsURL: fallback.URL,
	})
	if err == nil {
		t.Fatal("callJudgeWithFallback() error = nil, want both endpoints to fail")
	}
	if strings.Contains(err.Error(), errorTypeCanary) {
		t.Fatalf("judge error carried the canary error type: %v", err)
	}
	if !strings.Contains(err.Error(), "(other)") {
		t.Fatalf("judge error = %v, want the unknown types reported as (other)", err)
	}
}
