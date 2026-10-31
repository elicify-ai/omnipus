// request_timeout_1485_test.go — issue #1485: a non-streaming call (the path
// every multi-candidate fallback chain uses) was cut by the client's DEFAULT
// 120 s request timeout even when the caller granted a longer, size-scaled
// per-attempt deadline. Rule under test: the shipped default request timeout
// never undercuts a longer caller deadline; an operator's explicit
// request_timeout is always honoured; with no caller deadline the default
// still bounds the call.
package openai_compat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
)

const issue1485OKBody = `{"choices":[{"message":{"role":"assistant","content":"slow but fine"},"finish_reason":"stop"}]}`

// issue1485SlowServer answers after delay — longer than the shortened
// client timeout the cases use, shorter than the caller deadline.
func issue1485SlowServer(t *testing.T, delay time.Duration) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(issue1485OKBody))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func issue1485Messages() []protocoltypes.Message {
	return []protocoltypes.Message{{Role: "user", Content: "a very large prompt"}}
}

func TestIssue1485_DefaultRequestTimeoutDefersToLongerCallerDeadline(t *testing.T) {
	const (
		shortDefault   = 100 * time.Millisecond // stands in for the shipped 120 s default
		serverDelay    = 400 * time.Millisecond // a slow large-prompt answer
		callerDeadline = 5 * time.Second        // the chain's size-scaled per-attempt limit
	)
	srv := issue1485SlowServer(t, serverDelay)

	t.Run("default timeout does not undercut a longer caller deadline", func(t *testing.T) {
		p, err := NewProvider("k", srv.URL, "")
		require.NoError(t, err)
		p.httpClient.Timeout = shortDefault // NOT an operator setting: the default, shortened for the test
		ctx, cancel := context.WithTimeout(context.Background(), callerDeadline)
		defer cancel()
		resp, err := p.Chat(ctx, issue1485Messages(), nil, "m", nil)
		require.NoError(t, err, "#1485: the shipped default must not cut a request inside the caller's longer per-attempt deadline")
		require.Equal(t, "slow but fine", resp.Content)
	})

	t.Run("an explicit request_timeout is still honoured", func(t *testing.T) {
		p, err := NewProvider("k", srv.URL, "", WithRequestTimeout(shortDefault))
		require.NoError(t, err)
		ctx, cancel := context.WithTimeout(context.Background(), callerDeadline)
		defer cancel()
		_, err = p.Chat(ctx, issue1485Messages(), nil, "m", nil)
		require.Error(t, err, "an operator-set request_timeout is a deliberate ceiling and must still apply")
	})

	t.Run("without a caller deadline the default still bounds the call", func(t *testing.T) {
		p, err := NewProvider("k", srv.URL, "")
		require.NoError(t, err)
		p.httpClient.Timeout = shortDefault
		_, err = p.Chat(context.Background(), issue1485Messages(), nil, "m", nil)
		require.Error(t, err, "a call with no deadline of its own must keep the default bound")
	})

	t.Run("a caller deadline shorter than the default changes nothing", func(t *testing.T) {
		p, err := NewProvider("k", srv.URL, "")
		require.NoError(t, err)
		p.httpClient.Timeout = 10 * time.Second
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		defer cancel()
		_, err = p.Chat(ctx, issue1485Messages(), nil, "m", nil)
		require.ErrorIs(t, err, context.DeadlineExceeded, "the caller's own shorter deadline still ends the call")
	})
}
