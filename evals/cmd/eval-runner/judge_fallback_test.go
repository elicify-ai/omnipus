package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// Oracle: design.md §7 R3 and §4.2 (judge tries primary, then fallback, and
// records which model answered). Request shape: api-contract.md §4.
// Both servers are local. The keys are the fake strings from the onboard tests.

const judgeFallbackToken = "fallback-token-9c2e"

type capturedJudgeCall struct {
	Path             string
	APIKey           string
	Authorization    string
	AnthropicVersion string
	Body             map[string]any
}

func captureJudge(t *testing.T, status int, body string) (*httptest.Server, *[]capturedJudgeCall) {
	t.Helper()
	var mu sync.Mutex
	got := []capturedJudgeCall{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		parsed := map[string]any{}
		_ = json.Unmarshal(raw, &parsed)
		mu.Lock()
		got = append(got, capturedJudgeCall{
			Path:             r.URL.Path,
			APIKey:           r.Header.Get("X-Api-Key"),
			Authorization:    r.Header.Get("Authorization"),
			AnthropicVersion: r.Header.Get("Anthropic-Version"),
			Body:             parsed,
		})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func judgeSides() (E2EModelSide, E2EModelSide) {
	primary := E2EModelSide{Provider: "minimax-coding-plan", Model: "MiniMax-M3.1-Flash-Preview", KeyRef: "MINIMAX_API_KEY"}
	fallback := E2EModelSide{Provider: "openrouter", Model: "deepseek/deepseek-v4.1-flash", KeyRef: "OPENROUTER_API_KEY"}
	return primary, fallback
}

// TestCallJudgeFallsBackFromAnthropic429 records judge_model_used as the
// fallback slug and returns only the fallback stub's token. design.md §7 R3.
func TestCallJudgeFallsBackFromAnthropic429(t *testing.T) {
	clearModelKeyEnv(t)
	t.Setenv("MINIMAX_API_KEY_CI", fakeMinimaxKey)
	t.Setenv("OPENROUTER_API_KEY_EVAL", fakeOpenRouterKey)

	primarySrv, primaryHits := captureJudge(t, http.StatusTooManyRequests, `{"type":"error","error":{"type":"rate_limit_error","message":"test-key-aaaa"}}`)
	fallbackBody := `{"choices":[{"message":{"role":"assistant","content":"` + judgeFallbackToken + `"}}],"usage":{"prompt_tokens":3,"completion_tokens":4}}`
	fallbackSrv, fallbackHits := captureJudge(t, http.StatusOK, fallbackBody)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	primary, fallback := judgeSides()
	result, err := callJudgeWithFallback(ctx, primary, fallback, "score this", JudgeEndpoints{
		PrimaryMessagesURL:         primarySrv.URL,
		FallbackChatCompletionsURL: fallbackSrv.URL,
	})
	if err != nil {
		t.Fatalf("callJudgeWithFallback() error = %v", err)
	}
	if result.Text != judgeFallbackToken {
		t.Errorf("Text = %q, want %q (the token only the fallback stub returns)", result.Text, judgeFallbackToken)
	}
	if result.JudgeModelUsed != fallback.Slug() {
		t.Errorf("JudgeModelUsed = %q, want %q", result.JudgeModelUsed, fallback.Slug())
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if !strings.Contains(string(raw), `"judge_model_used":"`+fallback.Slug()+`"`) {
		t.Errorf("JSON result = %s, want judge_model_used %s", raw, fallback.Slug())
	}
	if strings.Contains(result.Text, fakeMinimaxKey) || strings.Contains(string(raw), fakeMinimaxKey) {
		t.Errorf("result leaked the API key: %s", raw)
	}
	if len(*primaryHits) < 1 {
		t.Fatal("primary stub received no request")
	}
	if (*primaryHits)[0].APIKey != fakeMinimaxKey {
		t.Errorf("primary x-api-key = %q, want the MiniMax key", (*primaryHits)[0].APIKey)
	}
	if (*primaryHits)[0].AnthropicVersion != "2023-06-01" {
		t.Errorf("primary anthropic-version = %q, want 2023-06-01", (*primaryHits)[0].AnthropicVersion)
	}
	if (*primaryHits)[0].Body["model"] != "MiniMax-M3.1-Flash-Preview" {
		t.Errorf("primary model = %v, want MiniMax-M3.1-Flash-Preview", (*primaryHits)[0].Body["model"])
	}
	if len(*fallbackHits) != 1 {
		t.Fatalf("fallback stub received %d requests, want 1", len(*fallbackHits))
	}
	if (*fallbackHits)[0].Authorization != "Bearer "+fakeOpenRouterKey {
		t.Errorf("fallback Authorization = %q, want the OpenRouter eval key", (*fallbackHits)[0].Authorization)
	}
	if (*fallbackHits)[0].Body["model"] != "deepseek/deepseek-v4.1-flash" {
		t.Errorf("fallback model = %v, want deepseek/deepseek-v4.1-flash", (*fallbackHits)[0].Body["model"])
	}
}

// TestCallJudgeBothFailNamesBothModels: neither stub answers, so the error
// names both model ids and does not echo the key. design.md §7 R3.
func TestCallJudgeBothFailNamesBothModels(t *testing.T) {
	clearModelKeyEnv(t)
	t.Setenv("MINIMAX_API_KEY_CI", fakeMinimaxKey)
	t.Setenv("OPENROUTER_API_KEY_EVAL", fakeOpenRouterKey)

	primarySrv, _ := captureJudge(t, http.StatusTooManyRequests, `{"error":"test-key-aaaa"}`)
	fallbackSrv, _ := captureJudge(t, http.StatusServiceUnavailable, `{"error":"test-key-bbbb"}`)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	primary, fallback := judgeSides()
	_, err := callJudgeWithFallback(ctx, primary, fallback, "score this", JudgeEndpoints{
		PrimaryMessagesURL:         primarySrv.URL,
		FallbackChatCompletionsURL: fallbackSrv.URL,
	})
	if err == nil {
		t.Fatal("callJudgeWithFallback() error = nil, want an error naming both models")
	}
	msg := err.Error()
	if !strings.Contains(msg, primary.Model) || !strings.Contains(msg, fallback.Model) {
		t.Errorf("error = %q, want it to name %s and %s", msg, primary.Model, fallback.Model)
	}
	if strings.Contains(msg, fakeMinimaxKey) || strings.Contains(msg, fakeOpenRouterKey) {
		t.Errorf("error leaked a key: %s", msg)
	}
}
