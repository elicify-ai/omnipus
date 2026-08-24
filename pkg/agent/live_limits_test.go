// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// T066-10 — the live limits rung (ADR-066 D2 rung 4; FR-003, FR-001, FR-007;
// B-04, B-08; DS-4 #7, #9; E9, E17).
//
// Every upstream endpoint is a mocked HTTP server. The LiveLimits client is
// built with a transport that rewrites EVERY request to that server while
// keeping the path, so the catalog rows keep their real base URLs (the
// cache key carries the row's base URL, which is what the ladder tests pin)
// and no test ever reaches the network.
// ---------------------------------------------------------------------------

// liveTestUpstream is a fake provider endpoint. It counts every request and
// serves an OpenAI-compatible /models list, an xAI-style /language-models
// list and Ollama's /api/ps + /api/show, all from one handler.
type liveTestUpstream struct {
	srv      *httptest.Server
	requests atomic.Int64
	mu       sync.Mutex
	paths    []string
	headers  []http.Header
	// openaiModels is served at GET /models (data[]); ollamaPS at GET
	// /api/ps (models[]); ollamaShow at POST /api/show (model_info).
	openaiModels []map[string]any
	ollamaPS     []map[string]any
	ollamaShow   map[string]any
	status       int
}

func newLiveTestUpstream(t *testing.T) *liveTestUpstream {
	t.Helper()
	u := &liveTestUpstream{status: http.StatusOK}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.requests.Add(1)
		u.mu.Lock()
		u.paths = append(u.paths, r.URL.Path)
		u.headers = append(u.headers, r.Header.Clone())
		u.mu.Unlock()
		if u.status != http.StatusOK {
			w.WriteHeader(u.status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		var body any
		switch {
		case strings.HasSuffix(r.URL.Path, "/api/ps"):
			body = map[string]any{"models": u.ollamaPS}
		case strings.HasSuffix(r.URL.Path, "/api/show"):
			body = u.ollamaShow
		default:
			body = map[string]any{"data": u.openaiModels}
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(u.srv.Close)
	return u
}

// rewriteTransport sends every request to the test server, path intact.
type rewriteTransport struct {
	target *url.URL
	inner  http.RoundTripper
}

func (rt rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = rt.target.Scheme
	clone.URL.Host = rt.target.Host
	clone.Host = rt.target.Host
	return rt.inner.RoundTrip(clone)
}

// liveTestClock is the injectable clock; the 25 h idle test advances it.
type liveTestClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *liveTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *liveTestClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// newTestLiveLimits builds a LiveLimits against the fake upstream with the
// given credential resolver, on a fresh cache path unless one is given.
func newTestLiveLimits(t *testing.T, up *liveTestUpstream, clock *liveTestClock, cred func(string) string, cachePath string) *LiveLimits {
	t.Helper()
	if cachePath == "" {
		cachePath = filepath.Join(t.TempDir(), "cache", "model_limits.json")
	}
	target, err := url.Parse(up.srv.URL)
	require.NoError(t, err)
	ll := NewLiveLimits(LiveLimitsOptions{
		CachePath:  cachePath,
		Credential: cred,
		Client:     &http.Client{Transport: rewriteTransport{target: target, inner: http.DefaultTransport}},
		Now:        clock.Now,
	})
	return ll
}

func noCredential(string) string { return "" }

func credentialFor(provider, key string) func(string) string {
	return func(p string) string {
		if p == provider {
			return key
		}
		return ""
	}
}

// TestLiveLimits_OnDemandCacheKeyTTLCredential — spec test 5 (B-04; FR-003,
// FR-001's live rung). Cache key, 24 h TTL, the no-credential skip, and zero
// fetches at boot and across a 25 h idle period.
func TestLiveLimits_OnDemandCacheKeyTTLCredential(t *testing.T) {
	installWindowTestCatalog(t, 1_048_576)
	clock := &liveTestClock{now: time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)}

	t.Run("boot and a 25 h idle period perform zero fetches", func(t *testing.T) {
		up := newLiveTestUpstream(t)
		up.openaiModels = []map[string]any{{"id": "z-ai/glm-5.2", "context_length": 200_000}}
		ll := newTestLiveLimits(t, up, clock, noCredential, "")
		// "Boot": constructing and installing the rung. No warm-up, no
		// catalog sweep, no timer.
		prev := liveWindowLookup()
		SetLiveWindowLookup(ll.Lookup)
		t.Cleanup(func() { SetLiveWindowLookup(prev) })
		ll.Wait()
		assert.Equal(t, int64(0), up.requests.Load(), "installing the rung must not fetch")
		clock.Advance(25 * time.Hour)
		time.Sleep(20 * time.Millisecond) // a timer, if one existed, would fire here
		ll.Wait()
		assert.Equal(t, int64(0), up.requests.Load(), "an idle period must not fetch (no timer)")
	})

	t.Run("no scheduler exists at all (the discrete property, not a stopwatch)", func(t *testing.T) {
		// The subtest above advances an INJECTED clock and then sleeps 20 ms
		// of wall time. The injected clock feeds only LiveLimits' TTL
		// arithmetic — it does not drive Go's runtime timers — so a
		// background refresher scheduled on real time would need 24 REAL
		// hours to fire and 20 ms proves nothing about it (false-green
		// patterns §3: a stopwatch standing in for a discrete property).
		//
		// Assert the property itself: rung 4 registers no scheduler, so
		// there is nothing that could fire. Adding
		// time.AfterFunc(24*time.Hour, ...) to NewLiveLimits leaves both
		// assertions above reading 0 requests and green, while the shipped
		// binary starts calling every configured provider's models endpoint
		// unsolicited — exactly what FR-003 forbids.
		src, err := os.ReadFile("live_limits.go")
		require.NoError(t, err)
		for _, forbidden := range []string{
			"time.AfterFunc(", "time.NewTicker(", "time.NewTimer(", "time.Tick(",
		} {
			assert.NotContains(t, string(src), forbidden,
				"live_limits.go must register no scheduler: %s would make the rung fire on a timer (FR-003)", forbidden)
		}
	})

	t.Run("DS-4 #7: resolved twice within 24 h → one fetch, 200,000 / live (B-04)", func(t *testing.T) {
		up := newLiveTestUpstream(t)
		up.openaiModels = []map[string]any{
			{"id": "some/other", "context_length": 4096},
			{"id": "z-ai/glm-5.2", "context_length": 200_000},
		}
		ll := newTestLiveLimits(t, up, clock, noCredential, "")
		installLiveWindowStub(t, ll.Lookup)
		cfg := windowTestConfig()

		// Cold cache: the next rung answers NOW (catalog), the fetch runs in
		// the background and the live value applies at the next resolution.
		first := ResolveWindow(cfg, "openrouter", "z-ai/glm-5.2", "mia")
		assert.Equal(t, 1_048_576, first.Window, "cold cache resolves from the next rung now")
		assert.Equal(t, WindowSourceCatalog, first.Source)
		ll.Wait()
		assert.Equal(t, int64(1), up.requests.Load(), "exactly one fetch for the first resolution")

		second := ResolveWindow(cfg, "openrouter", "z-ai/glm-5.2", "mia")
		assert.Equal(t, 200_000, second.Window)
		assert.Equal(t, WindowSourceLive, second.Source)
		third := ResolveWindow(cfg, "openrouter", "z-ai/glm-5.2", "mia")
		assert.Equal(t, 200_000, third.Window)
		ll.Wait()
		assert.Equal(t, int64(1), up.requests.Load(), "resolving again within 24 h performs no fetch")

		// 23 h later: still cached.
		clock.Advance(23 * time.Hour)
		got := ResolveWindow(cfg, "openrouter", "z-ai/glm-5.2", "mia")
		assert.Equal(t, 200_000, got.Window)
		ll.Wait()
		assert.Equal(t, int64(1), up.requests.Load())

		// 25 h after the fetch: expired → next rung now, one refetch.
		clock.Advance(2 * time.Hour)
		expired := ResolveWindow(cfg, "openrouter", "z-ai/glm-5.2", "mia")
		assert.Equal(t, WindowSourceCatalog, expired.Source, "an expired entry is not served")
		ll.Wait()
		assert.Equal(t, int64(2), up.requests.Load(), "an expired entry is refetched once")
		assert.Equal(t, WindowSourceLive, ResolveWindow(cfg, "openrouter", "z-ai/glm-5.2", "mia").Source)
		clock.Advance(-25 * time.Hour)
	})

	t.Run("key is (provider id, base URL, model)", func(t *testing.T) {
		up := newLiveTestUpstream(t)
		up.openaiModels = []map[string]any{{"id": "z-ai/glm-5.2", "context_length": 200_000}}
		ll := newTestLiveLimits(t, up, clock, noCredential, "")

		w, ok := ll.Lookup("openrouter", "https://openrouter.ai/api/v1", "z-ai/glm-5.2")
		assert.False(t, ok)
		assert.Equal(t, 0, w)
		ll.Wait()
		require.Equal(t, int64(1), up.requests.Load())
		w, ok = ll.Lookup("openrouter", "https://openrouter.ai/api/v1", "z-ai/glm-5.2")
		assert.True(t, ok)
		assert.Equal(t, 200_000, w)

		// Same provider + model, a different base URL (a catalog `api`
		// change) → a new key → a new fetch.
		_, ok = ll.Lookup("openrouter", "https://openrouter.ai/api/v2", "z-ai/glm-5.2")
		assert.False(t, ok)
		ll.Wait()
		assert.Equal(t, int64(2), up.requests.Load())

		// Same provider + base URL, a different model → a new key.
		_, ok = ll.Lookup("openrouter", "https://openrouter.ai/api/v1", "z-ai/glm-5.2-flash")
		assert.False(t, ok)
		ll.Wait()
		assert.Equal(t, int64(3), up.requests.Load())
		// …and the endpoint did not list that model: nothing cached, rung skipped.
		_, ok = ll.Lookup("openrouter", "https://openrouter.ai/api/v1", "z-ai/glm-5.2-flash")
		assert.False(t, ok)
	})

	t.Run("the cache is on disk at cache/model_limits.json and survives a restart", func(t *testing.T) {
		up := newLiveTestUpstream(t)
		up.openaiModels = []map[string]any{{"id": "z-ai/glm-5.2", "context_length": 200_000}}
		home := t.TempDir()
		path := filepath.Join(home, "cache", "model_limits.json")
		ll := newTestLiveLimits(t, up, clock, noCredential, path)
		ll.Lookup("openrouter", "https://openrouter.ai/api/v1", "z-ai/glm-5.2")
		ll.Wait()
		require.Equal(t, int64(1), up.requests.Load())

		raw, err := os.ReadFile(path)
		require.NoError(t, err, "the cache file must exist after a fetch")
		assert.Contains(t, string(raw), `"z-ai/glm-5.2"`)
		assert.Contains(t, string(raw), `"https://openrouter.ai/api/v1"`)
		assert.Contains(t, string(raw), `200000`)

		// A fresh process (new LiveLimits, same path) serves the entry with
		// no fetch.
		restarted := newTestLiveLimits(t, up, clock, noCredential, path)
		w, ok := restarted.Lookup("openrouter", "https://openrouter.ai/api/v1", "z-ai/glm-5.2")
		assert.True(t, ok)
		assert.Equal(t, 200_000, w)
		restarted.Wait()
		assert.Equal(t, int64(1), up.requests.Load(), "a warm on-disk cache performs no fetch")

		// A corrupt file is a cold cache, never a crash.
		require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o600))
		corrupt := newTestLiveLimits(t, up, clock, noCredential, path)
		_, ok = corrupt.Lookup("openrouter", "https://openrouter.ai/api/v1", "z-ai/glm-5.2")
		assert.False(t, ok)
		corrupt.Wait()
		assert.Equal(t, int64(2), up.requests.Load())
	})

	t.Run("no credential → rung skipped, zero fetches; credential → sent, one fetch", func(t *testing.T) {
		up := newLiveTestUpstream(t)
		up.openaiModels = []map[string]any{{"id": "gpt-5.4", "context_length": 272_000}}
		// openai-chatgpt is an openai-compatible cloud row that needs a key.
		ll := newTestLiveLimits(t, up, clock, noCredential, "")
		_, ok := ll.Lookup("openai-chatgpt", "https://chatgpt.com/backend-api/codex", "gpt-5.4")
		assert.False(t, ok)
		ll.Wait()
		assert.Equal(t, int64(0), up.requests.Load(), "no credential in the store → the rung is skipped, not queried")

		withKey := newTestLiveLimits(t, up, clock, credentialFor("openai-chatgpt", "sk-test-123"), "")
		_, ok = withKey.Lookup("openai-chatgpt", "https://chatgpt.com/backend-api/codex", "gpt-5.4")
		assert.False(t, ok)
		withKey.Wait()
		require.Equal(t, int64(1), up.requests.Load())
		up.mu.Lock()
		hdr := up.headers[0]
		up.mu.Unlock()
		assert.Equal(t, "Bearer sk-test-123", hdr.Get("Authorization"))
		w, ok := withKey.Lookup("openai-chatgpt", "https://chatgpt.com/backend-api/codex", "gpt-5.4")
		assert.True(t, ok)
		assert.Equal(t, 272_000, w)
	})

	t.Run("endpoint down → nothing cached, next rung now, no hammering (E9)", func(t *testing.T) {
		up := newLiveTestUpstream(t)
		up.status = http.StatusBadGateway
		ll := newTestLiveLimits(t, up, clock, noCredential, "")
		installLiveWindowStub(t, ll.Lookup)
		got := ResolveWindow(windowTestConfig(), "openrouter", "z-ai/glm-5.2", "mia")
		assert.Equal(t, WindowSourceCatalog, got.Source)
		ll.Wait()
		assert.Equal(t, int64(1), up.requests.Load())
		for i := 0; i < 3; i++ {
			ResolveWindow(windowTestConfig(), "openrouter", "z-ai/glm-5.2", "mia")
		}
		ll.Wait()
		assert.Equal(t, int64(1), up.requests.Load(), "a failed fetch backs off; resolutions do not hammer the endpoint")
		_, err := os.Stat(ll.cachePath)
		assert.True(t, os.IsNotExist(err), "a failure writes nothing to the cache")
	})

	t.Run("a window the endpoint reports as 0 or absent is not cached", func(t *testing.T) {
		up := newLiveTestUpstream(t)
		up.openaiModels = []map[string]any{{"id": "z-ai/glm-5.2", "context_length": 0}, {"id": "x", "owned_by": "y"}}
		ll := newTestLiveLimits(t, up, clock, noCredential, "")
		ll.Lookup("openrouter", "https://openrouter.ai/api/v1", "z-ai/glm-5.2")
		ll.Wait()
		_, ok := ll.Lookup("openrouter", "https://openrouter.ai/api/v1", "z-ai/glm-5.2")
		assert.False(t, ok)
		_, err := os.Stat(ll.cachePath)
		assert.True(t, os.IsNotExist(err))
	})
}

// TestLiveLimits_OllamaLoadedWindow — B-08 / DS-4 #9 / FR-007: a local row's
// window comes from Ollama's native API. Field names verified against a
// running daemon (Ollama 0.32.3, 2026-08-24): GET /api/ps →
// models[].context_length is the window the model is LOADED with (num_ctx;
// 4096 for llama3.2:1b on the reference machine), POST /api/show →
// model_info["<arch>.context_length"] is the architecture maximum (131072 for
// the same model). The loaded window is what a turn actually gets, so /api/ps
// wins when the model is resident; /api/show is the answer for a model that
// is not loaded yet.
func TestLiveLimits_OllamaLoadedWindow(t *testing.T) {
	installWindowTestCatalog(t, 1_048_576)
	clock := &liveTestClock{now: time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)}

	t.Run("DS-4 #9: /api/ps reports the loaded window → 8,192 / live, never floored (B-08)", func(t *testing.T) {
		up := newLiveTestUpstream(t)
		up.ollamaPS = []map[string]any{{
			"name": "llama3:8b", "model": "llama3:8b", "size": 1519292251,
			"details": map[string]any{"family": "llama"}, "context_length": 8192,
		}}
		up.ollamaShow = map[string]any{"model_info": map[string]any{"llama.context_length": 131072}}
		ll := newTestLiveLimits(t, up, clock, noCredential, "")
		installLiveWindowStub(t, ll.Lookup)

		cold := ResolveWindow(windowTestConfig(), "ollama", "llama3:8b", "mia")
		assert.True(t, cold.Unknown, "cold cache on a local row: refused now, usable at the next reload")
		ll.Wait()
		require.Equal(t, int64(1), up.requests.Load(), "the loaded window answered; /api/show not consulted")
		up.mu.Lock()
		paths := append([]string(nil), up.paths...)
		up.mu.Unlock()
		assert.Equal(t, []string{"/api/ps"}, paths, "the native API is at the row's host, not under /v1")

		got := ResolveWindow(windowTestConfig(), "ollama", "llama3:8b", "mia")
		assert.Equal(t, 8192, got.Window)
		assert.Equal(t, WindowSourceLive, got.Source)
		assert.False(t, got.Unknown)
	})

	t.Run("model not loaded → /api/show's architecture maximum", func(t *testing.T) {
		up := newLiveTestUpstream(t)
		up.ollamaPS = []map[string]any{{"name": "other:latest", "model": "other:latest", "context_length": 2048}}
		up.ollamaShow = map[string]any{
			"details":    map[string]any{"family": "llama"},
			"model_info": map[string]any{"general.architecture": "llama", "llama.context_length": 131072, "llama.block_count": 16},
		}
		ll := newTestLiveLimits(t, up, clock, noCredential, "")
		ll.Lookup("ollama", "http://localhost:11434/v1", "llama3.2:1b")
		ll.Wait()
		up.mu.Lock()
		paths := append([]string(nil), up.paths...)
		up.mu.Unlock()
		assert.Equal(t, []string{"/api/ps", "/api/show"}, paths)
		w, ok := ll.Lookup("ollama", "http://localhost:11434/v1", "llama3.2:1b")
		assert.True(t, ok)
		assert.Equal(t, 131072, w)
	})

	t.Run("a bare tag matches Ollama's :latest normalisation", func(t *testing.T) {
		up := newLiveTestUpstream(t)
		up.ollamaPS = []map[string]any{{"name": "llama3.2:latest", "model": "llama3.2:latest", "context_length": 4096}}
		ll := newTestLiveLimits(t, up, clock, noCredential, "")
		ll.Lookup("ollama", "http://localhost:11434/v1", "llama3.2")
		ll.Wait()
		w, ok := ll.Lookup("ollama", "http://localhost:11434/v1", "llama3.2")
		assert.True(t, ok)
		assert.Equal(t, 4096, w)
	})

	t.Run("daemon down → nothing cached; the local row stays refused (E9)", func(t *testing.T) {
		up := newLiveTestUpstream(t)
		up.status = http.StatusServiceUnavailable
		ll := newTestLiveLimits(t, up, clock, noCredential, "")
		installLiveWindowStub(t, ll.Lookup)
		ResolveWindow(windowTestConfig(), "ollama", "llama3:8b", "mia")
		ll.Wait()
		got := ResolveWindow(windowTestConfig(), "ollama", "llama3:8b", "mia")
		assert.True(t, got.Unknown)
		assert.Equal(t, 0, got.Window)
	})
}

// TestLiveLimits_TamperedCacheCanOnlyLower — E17: a hand-edited
// model_limits.json can only LOWER the window. A cached value above the
// catalog's capability is clamped to the catalog; a value at or below it
// stands as the live capability.
func TestLiveLimits_TamperedCacheCanOnlyLower(t *testing.T) {
	installWindowTestCatalog(t, 1_048_576)
	clock := &liveTestClock{now: time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)}
	up := newLiveTestUpstream(t)
	home := t.TempDir()
	path := filepath.Join(home, "cache", "model_limits.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))

	writeCache := func(window int) {
		doc := map[string]any{
			"version": 1,
			"entries": []map[string]any{{
				"provider":   "openrouter",
				"base_url":   "https://openrouter.ai/api/v1",
				"model":      "z-ai/glm-5.2",
				"window":     window,
				"fetched_at": clock.Now().Add(-time.Hour).Format(time.RFC3339),
			}},
		}
		raw, err := json.Marshal(doc)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, raw, 0o600))
	}

	t.Run("raised above the catalog → clamped to the catalog", func(t *testing.T) {
		writeCache(10_000_000)
		ll := newTestLiveLimits(t, up, clock, noCredential, path)
		installLiveWindowStub(t, ll.Lookup)
		got := ResolveWindow(windowTestConfig(), "openrouter", "z-ai/glm-5.2", "mia")
		assert.Equal(t, 1_048_576, got.Window, "a tampered cache cannot raise the window")
		assert.Equal(t, WindowSourceCatalog, got.Source)
		ll.Wait()
		assert.Equal(t, int64(0), up.requests.Load(), "a fresh entry is served, never refetched")
	})

	t.Run("lowered below the catalog → the lower value stands", func(t *testing.T) {
		writeCache(64_000)
		ll := newTestLiveLimits(t, up, clock, noCredential, path)
		installLiveWindowStub(t, ll.Lookup)
		got := ResolveWindow(windowTestConfig(), "openrouter", "z-ai/glm-5.2", "mia")
		assert.Equal(t, 64_000, got.Window)
		assert.Equal(t, WindowSourceLive, got.Source)
	})

	t.Run("a non-positive or absurd cached value is ignored", func(t *testing.T) {
		writeCache(-5)
		ll := newTestLiveLimits(t, up, clock, noCredential, path)
		_, ok := ll.Lookup("openrouter", "https://openrouter.ai/api/v1", "z-ai/glm-5.2")
		assert.False(t, ok)
	})
}

// TestLiveLimits_LandedWindowNotifiesTheInstaller pins FR-007 / US-2.AC2's
// missing half.
//
// Lookup never blocks: the resolution that reached rung 4 has already
// answered by the time the fetch lands, and an AgentInstance CACHES its
// window at construction. For a `locality: local` row the catalog cannot
// size, that first resolution is WindowResolution{Unknown} and runTurn
// refuses every later turn with context_window_unknown. Nothing in
// live_limits.go used to tell anybody the answer had arrived, so the correct
// window sat in the cache, unused, and a healthy Ollama endpoint stayed
// refused indefinitely. The gateway wires this notification to TriggerReload.
func TestLiveLimits_LandedWindowNotifiesTheInstaller(t *testing.T) {
	installWindowTestCatalog(t, 1_048_576)
	clock := &liveTestClock{now: time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)}

	up := newLiveTestUpstream(t)
	up.ollamaPS = []map[string]any{{"name": "llama3.1:8b", "context_length": 8192}}

	target, err := url.Parse(up.srv.URL)
	require.NoError(t, err)

	type landing struct {
		provider string
		model    string
		window   int
	}
	landed := make(chan landing, 4)
	ll := NewLiveLimits(LiveLimitsOptions{
		CachePath:  filepath.Join(t.TempDir(), "cache", "model_limits.json"),
		Credential: noCredential,
		Client:     &http.Client{Transport: rewriteTransport{target: target, inner: http.DefaultTransport}},
		Now:        clock.Now,
		OnWindowLanded: func(provider, _, model string, window int) {
			landed <- landing{provider: provider, model: model, window: window}
		},
	})

	// A cold lookup answers "unknown" NOW and starts the fetch.
	window, ok := ll.Lookup("ollama", "http://127.0.0.1:11434", "llama3.1:8b")
	assert.False(t, ok, "the rung never blocks: a cold cache answers at once")
	assert.Equal(t, 0, window)

	ll.Wait()
	select {
	case got := <-landed:
		assert.Equal(t, "ollama", got.provider)
		assert.Equal(t, "llama3.1:8b", got.model)
		assert.Equal(t, 8192, got.window,
			"the notification must carry the window the endpoint reported")
	case <-time.After(5 * time.Second):
		t.Fatal("a landed live window must notify the installer — without it the resolution that " +
			"was refused never re-runs and the endpoint stays refused forever")
	}

	// The value is now servable, and re-serving it does NOT re-notify.
	window, ok = ll.Lookup("ollama", "http://127.0.0.1:11434", "llama3.1:8b")
	require.True(t, ok)
	assert.Equal(t, 8192, window)
	ll.Wait()
	select {
	case got := <-landed:
		t.Fatalf("a cache hit must not notify again; got %+v", got)
	case <-time.After(50 * time.Millisecond):
	}
}
