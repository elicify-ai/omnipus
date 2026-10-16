package providers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	anthropicprovider "github.com/elicify-ai/omnipus/pkg/providers/anthropic"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// Dispatch #1: use the real SDK error in the real fallback chain; cancel at the
// retry observer so the assertion is a discrete wait decision, not a stopwatch.
func TestCachingFixSDKRetryAfterFlowsToChain(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "60")
		w.Header().Set("Request-Id", "req-retry-fixture")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`)
	}))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var decisions []RetryInfo
	ctx = WithRetryObserver(ctx, func(info RetryInfo) { decisions = append(decisions, info); cancel() })
	adapter := anthropicprovider.NewProviderWithBaseURL("fixture", srv.URL)
	var boundary error
	_, err := NewFallbackChain(NewCooldownTracker()).Execute(ctx, []FallbackCandidate{{Provider: "anthropic", Model: "fixture"}}, func(ctx context.Context, _, model string) (*LLMResponse, error) {
		response, callErr := adapter.Chat(ctx, []Message{{Role: "user", Content: "hi"}}, nil, model, nil)
		boundary = callErr
		return response, callErr
	})
	if !errors.Is(err, context.Canceled) || requests != 1 || len(decisions) != 1 {
		t.Fatalf("retry control: err=%v requests=%d decisions=%+v", err, requests, decisions)
	}
	if wait := decisions[0].RetryAt.Sub(decisions[0].SentAt); wait != 60*time.Second {
		t.Fatalf("SDK429 Retry-After:60 schedules %s, want exactly1m0s, not the jittered2s fallback", wait)
	}
	var pe *common.ProviderError
	if !errors.As(boundary, &pe) || pe.RequestID != "req-retry-fixture" {
		t.Fatalf("request correlation lost at adapter boundary: %T", boundary)
	}
}
