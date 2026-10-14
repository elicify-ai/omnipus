package openai_compat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestProvider_RequestTimeoutStillEndsAHungCall pins what issue #1306 KEPT:
// removing every agent-work time limit must not remove the per-network-call
// request timeout. A provider that accepts the connection and then never
// answers is still cut off by the configured request timeout, so a hung
// connection surfaces as an error the turn can retry — it does not hang the
// turn forever.
func TestProvider_RequestTimeoutStillEndsAHungCall(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer srv.Close()
	defer close(release)

	const requestTimeout = 200 * time.Millisecond
	p, err := NewProvider("key", srv.URL, "", WithRequestTimeout(requestTimeout))
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, chatErr := p.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, "test-model", nil)
		done <- chatErr
	}()
	select {
	case chatErr := <-done:
		if chatErr == nil {
			t.Fatal("Chat against a provider that never answers returned no error; want the request timeout to end it")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Chat against a hung provider did not return within 10s: the per-call request timeout no longer applies")
	}
}
