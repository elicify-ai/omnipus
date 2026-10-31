// request_timeout_1485_test.go — issue #1485, Anthropic Messages transport:
// the shipped default request timeout must never undercut a longer caller
// deadline (the fallback chain's size-scaled per-attempt limit); an explicit
// request_timeout is always honoured; with no caller deadline the default
// still bounds the call.
package anthropicprovider

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const issue1485AnthropicOK = `{"id":"fixture","type":"message","role":"assistant","content":[{"type":"text","text":"slow but fine"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`

func issue1485AnthropicSlowServer(t *testing.T, delay time.Duration) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, issue1485AnthropicOK)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// issue1485ShortDefault shortens the package default for one test; the
// default stands in for the shipped 120 s.
func issue1485ShortDefault(t *testing.T, d time.Duration) {
	t.Helper()
	saved := defaultRequestTimeout
	defaultRequestTimeout = d
	t.Cleanup(func() { defaultRequestTimeout = saved })
}

func TestIssue1485_AnthropicDefaultTimeoutDefersToLongerCallerDeadline(t *testing.T) {
	const (
		shortDefault   = 100 * time.Millisecond
		serverDelay    = 400 * time.Millisecond
		callerDeadline = 5 * time.Second
	)
	srv := issue1485AnthropicSlowServer(t, serverDelay)
	msgs := []Message{{Role: "user", Content: "a very large prompt"}}

	t.Run("default timeout does not undercut a longer caller deadline", func(t *testing.T) {
		issue1485ShortDefault(t, shortDefault)
		p := NewProviderWithBaseURL("fixture", srv.URL)
		ctx, cancel := context.WithTimeout(context.Background(), callerDeadline)
		defer cancel()
		resp, err := p.Chat(ctx, msgs, nil, "fixture", nil)
		require.NoError(t, err, "#1485: the shipped default must not cut a request inside the caller's longer per-attempt deadline")
		require.Equal(t, "slow but fine", resp.Content)
	})

	t.Run("without a caller deadline the default still bounds the call", func(t *testing.T) {
		issue1485ShortDefault(t, shortDefault)
		p := NewProviderWithBaseURL("fixture", srv.URL)
		_, err := p.Chat(context.Background(), msgs, nil, "fixture", nil)
		require.Error(t, err, "a call with no deadline of its own must keep the default bound")
	})

	t.Run("an explicit request_timeout is still honoured", func(t *testing.T) {
		// Explicit timeouts are whole seconds on this constructor; a 1 s
		// explicit limit against a 1.5 s answer must still fail even though
		// the caller grants 5 s.
		slow := issue1485AnthropicSlowServer(t, 1500*time.Millisecond)
		p := NewProviderWithBaseURL("fixture", slow.URL, 1)
		ctx, cancel := context.WithTimeout(context.Background(), callerDeadline)
		defer cancel()
		_, err := p.Chat(ctx, msgs, nil, "fixture", nil)
		require.Error(t, err, "an operator-set request_timeout is a deliberate ceiling and must still apply")
	})
}
