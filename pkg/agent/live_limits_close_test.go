// License: MIT
// Copyright (c) 2026 Omnipus contributors
package agent

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestLiveLimits_CloseAbortsInflightFetchAndNeverWrites pins the shutdown
// contract added 2026-09-12: once Close returns, the instance performs no
// further I/O. Before it, the fetch ran on context.Background() and its
// tail wrote cache/model_limits.json whenever it landed — including after
// the gateway that started it had shut down and its home dir was gone.
//
// DIES ON: fetchAndStore deriving its context from context.Background()
// again; dropping the post-fetch lifetime check (the write lands after
// Close); dropping the pre-start check in Lookup (a fetch starts after
// Close); or Close not waiting on the WaitGroup.
func TestLiveLimits_CloseAbortsInflightFetchAndNeverWrites(t *testing.T) {
	installWindowTestCatalog(t, 1_048_576)

	// An upstream that answers only when the client gives up: the request
	// is parked until its context is cancelled, then a valid window is sent
	// anyway, so a fetch that IGNORED cancellation would land a real answer.
	var hits atomic.Int64
	released := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
		close(released)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"z-ai/glm-5.2","context_length":200000}]}`))
	}))
	t.Cleanup(up.Close)
	target, err := url.Parse(up.URL)
	require.NoError(t, err)

	cachePath := filepath.Join(t.TempDir(), "cache", "model_limits.json")
	ll := NewLiveLimits(LiveLimitsOptions{
		CachePath: cachePath,
		Client:    &http.Client{Transport: rewriteTransport{target: target, inner: http.DefaultTransport}},
	})

	// openrouter needs no credential, so this starts a fetch.
	_, ok := ll.Lookup("openrouter", "https://openrouter.ai/api/v1", "z-ai/glm-5.2")
	require.False(t, ok, "nothing cached yet")
	require.Eventually(t, func() bool { return hits.Load() == 1 }, 2*time.Second, 10*time.Millisecond,
		"the background fetch must reach the upstream")

	start := time.Now()
	ll.Close()
	require.Less(t, time.Since(start), 3*time.Second,
		"Close must abort the parked fetch, not wait out the 10 s request timeout")
	require.True(t, ll.Closed())

	// The upstream released after cancellation; give a write every chance to
	// land, then prove it did not.
	select {
	case <-released:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream handler never observed the cancellation")
	}
	time.Sleep(50 * time.Millisecond)
	_, statErr := os.Stat(cachePath)
	require.True(t, os.IsNotExist(statErr), "cache must not be written after Close; stat: %v", statErr)

	// No fetch may START after Close either.
	_, ok = ll.Lookup("openrouter", "https://openrouter.ai/api/v1", "z-ai/glm-5.3")
	require.False(t, ok)
	ll.Wait()
	require.Equal(t, int64(1), hits.Load(), "a closed instance must not start a new fetch")

	// Idempotent.
	ll.Close()
}
