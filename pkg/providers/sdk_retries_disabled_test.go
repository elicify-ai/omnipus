/**
 * sdk_retries_disabled_test.go — D13 (provider-messages spec §7.2): every
 * SDK-backed adapter disables its internal retries so the fallback chain
 * owns every retry decision.
 *
 * Implemented by backend-lead under team-lead's dispatch order ("ONE TEST
 * PER ADAPTER"), flagged for CHECK: this is a test file written outside the
 * RED pack — qa-lead owns test files and must audit it before it counts as
 * suite (test-integrity-audit).
 *
 * Oracle: the SDKs' documented default is 2 retries (3 requests) on a 500.
 * A request-counting server that sees exactly ONE request proves the client
 * was built with option.WithMaxRetries(0); the test injects no retry posture
 * of its own, so the count can only come from the production constructor.
 * Driven through the newCodexProviderWithBaseURL seam — the same option
 * list the public NewCodexProvider builds — so the test cannot pass against
 * a constructor that skipped the seam.
 */

package providers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestSDKRetriesDisabled_Codex_D13(t *testing.T) {
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"upstream 500"}}`))
	}))
	t.Cleanup(srv.Close)

	p := newCodexProviderWithBaseURL("test-token", "acct-1", srv.URL)
	_, err := p.Chat(context.Background(),
		[]Message{{Role: "user", Content: "Hello"}},
		nil, "gpt-5.3-codex", map[string]any{})
	if err == nil {
		t.Fatal("Chat returned nil error against a 500 server; want an error")
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("codex adapter made %d requests against a 500, want exactly 1 — the SDK's default 2 retries are still enabled (D13)", got)
	}
}
