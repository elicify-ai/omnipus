// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// Oracle: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-1104-1105/design.md,
// Decision #1105, Contract-first changes and QA acceptance list.
// Until the diagnostic contract is generated, inspect HTTP JSON rather than
// inventing a parallel wire-format Go type. All checks go through the actual
// registered mux and authentication, not a guessed diagnostic handler method.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/elicify-ai/omnipus/pkg/config"
)

const diagnosticBearer = "search-diagnostic-test-bearer"
const discardedSearchContent = "private-upstream-result-must-be-discarded"

func newSearchCheckMux(t *testing.T) (*restAPI, *config.Config, http.Handler) {
	t.Helper()
	api, user, cfg := newSearchSettingsAPI(t)
	hash, err := bcrypt.GenerateFromPassword([]byte(diagnosticBearer), bcrypt.MinCost)
	require.NoError(t, err)
	user.TokenHash = config.BcryptHash(hash)
	cfg.Gateway.Users = []config.UserConfig{*user}
	cfg.Gateway.DevModeBypass = false
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	t.Setenv("NO_PROXY", "127.0.0.1,localhost")
	mux := http.NewServeMux()
	api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})
	handler := api.configSnapshotMiddleware(mux)
	// The positive control proves both the registration and bearer instrument
	// can reach a known existing authenticated Settings route.
	r := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/providers", nil)
	r.Header.Set("Authorization", "Bearer "+diagnosticBearer)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code, "positive route/auth control; body=%s", w.Body.String())
	return api, cfg, handler
}

func postSearchCheck(ctx context.Context, mux http.Handler, id, body, bearer string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/integrations/providers/"+id+"/check", strings.NewReader(body)).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func requireSearchCheckResult(t *testing.T, w *httptest.ResponseRecorder, id, status string) map[string]any {
	t.Helper()
	if w.Code == http.StatusMethodNotAllowed || w.Code == http.StatusNotFound {
		t.Fatalf("BLOCKED: POST search connection diagnostic not implemented — required by design Decision #1105; want HTTP 200, got %d: %s", w.Code, w.Body.String())
	}
	require.Equal(t, http.StatusOK, w.Code, "completed diagnostics use 200 even for upstream failure; body=%s", w.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, id, body["provider_id"])
	require.Equal(t, status, body["status"])
	stamp, ok := body["checked_at"].(string)
	require.True(t, ok, "checked_at is a required date-time")
	_, err := time.Parse(time.RFC3339Nano, stamp)
	require.NoError(t, err, "checked_at must be a date-time, not an opaque label")
	want := map[string]any{"provider_id": id, "status": status, "checked_at": stamp}
	if status == "rate_limited" {
		if delay, exists := body["retry_after_seconds"]; exists {
			want["retry_after_seconds"] = delay
		}
	}
	assert.Equal(t, want, body, "closed response must expose no key, results or arbitrary upstream error")
	assert.NotContains(t, w.Body.String(), searchSettingsSecret)
	assert.NotContains(t, w.Body.String(), discardedSearchContent)
	return body
}

// Use configuration endpoint overrides, not a diagnostic stub or mocked Search.
// Load Brave/Perplexity's optional base_url through real configuration parsing
// so missing support fails an assertion rather than referring to absent Go fields.
func wireSearchCheckEdge(t *testing.T, api *restAPI, cfg *config.Config, id, url string) {
	t.Helper()
	def, ok := config.SearchProviderDefByID(id)
	require.True(t, ok)
	require.True(t, def.Keyed)
	require.NoError(t, api.credStore.Set(def.CredRef, searchSettingsSecret))
	t.Setenv(def.CredRef, searchSettingsSecret)
	def.SetEnabled(&cfg.Tools.Web, true)
	web := readRolesWebConfig(t, api)
	section := map[string]any{"enabled": true, "api_key_ref": def.CredRef, "base_url": url}
	switch id {
	case "tavily":
		cfg.Tools.Web.Tavily.APIKeyRef = def.CredRef
		cfg.Tools.Web.Tavily.BaseURL = url
		cfg.Tools.Web.Tavily.SearchDepth = "basic"
		section["search_depth"] = "basic"
	case "glm":
		cfg.Tools.Web.GLMSearch.APIKeyRef = def.CredRef
		cfg.Tools.Web.GLMSearch.BaseURL = url
		cfg.Tools.Web.GLMSearch.ContentSize = "medium"
		section["content_size"] = "medium"
	case "baidu":
		cfg.Tools.Web.BaiduSearch.APIKeyRef = def.CredRef
		cfg.Tools.Web.BaiduSearch.BaseURL = url
	case "exa":
		cfg.Tools.Web.Exa.APIKeyRef = def.CredRef
		cfg.Tools.Web.Exa.BaseURL = url
	case "brave", "perplexity":
		// The #1105 team-lead ruling specifies optional base_url for both.
		// Use the same persisted key as the existing controllable clients.
	default:
		t.Fatalf("BLOCKED: controlled gateway transport for %s not implemented — required by QA acceptance Actual check; do not invent a config field or mock Search", id)
	}
	web[def.Section] = section
	writeRolesWebConfig(t, api, web)
	if id == "brave" || id == "perplexity" {
		fresh, err := config.LoadConfig(api.configPath())
		require.NoError(t, err)
		cfg.Tools.Web = fresh.Tools.Web
		loaded, err := json.Marshal(cfg.Tools.Web)
		require.NoError(t, err)
		var loadedWeb map[string]any
		require.NoError(t, json.Unmarshal(loaded, &loadedWeb))
		require.Equal(t, url, roleSection(t, loadedWeb, def.Section)["base_url"],
			"missing base_url support: tools.web.%s.base_url must survive configuration loading to address the controlled provider", def.Section)
	}
	require.True(t, cfg.Tools.Web.UsableSearchProvider(id), "fixture must be runtime-usable")
	requireSaved, err := api.credStore.Get(def.CredRef)
	require.NoError(t, err)
	require.Equal(t, searchSettingsSecret, requireSaved, "fixture must contain a saved key")
}

func TestSearchConnectionCheck_OneRealAddressedAttemptForEveryControllableKeyedClient(t *testing.T) {
	for _, tc := range []struct{ id, protocolReply string }{
		{"tavily", `{"results":[{"title":"private-upstream-result-must-be-discarded","url":"https://example.invalid/result","content":"private-upstream-result-must-be-discarded"}]}`},
		{"glm", `{"search_result":[{"title":"private-upstream-result-must-be-discarded","link":"https://example.invalid/result","content":"private-upstream-result-must-be-discarded"}]}`},
		{"baidu", `{"references":[{"title":"private-upstream-result-must-be-discarded","url":"https://example.invalid/result","content":"private-upstream-result-must-be-discarded"}]}`},
		{"exa", `{"results":[{"title":"private-upstream-result-must-be-discarded","url":"https://example.invalid/result"}]}`},
	} {
		t.Run(tc.id, func(t *testing.T) {
			api, cfg, mux := newSearchCheckMux(t)
			var calls atomic.Int32
			seen := make(chan []byte, 8)
			headers := make(chan http.Header, 8)
			edge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				data, err := io.ReadAll(r.Body)
				assert.NoError(t, err)
				if tc.id == "exa" {
					assert.NotContains(t, r.URL.RawQuery, searchSettingsSecret, "Exa key must not appear in the URL query")
					assert.NotContains(t, r.URL.Query().Encode(), searchSettingsSecret, "Exa key must not appear in the decoded URL query")
					assert.NotContains(t, string(data), searchSettingsSecret, "Exa key must not appear in the request body")
				}
				seen <- data
				headers <- r.Header.Clone()
				w.Header().Set("Content-Type", "application/json")
				_, err = io.WriteString(w, tc.protocolReply)
				assert.NoError(t, err)
			}))
			t.Cleanup(edge.Close)
			wireSearchCheckEdge(t, api, cfg, tc.id, edge.URL)
			before := readRolesWebConfig(t, api)
			w := postSearchCheck(t.Context(), mux, tc.id, `{}`, diagnosticBearer)
			requireSearchCheckResult(t, w, tc.id, "success")
			require.Equal(t, int32(1), calls.Load(), "one real attempt: no retries, discovery, result fetches or cached success")
			var payload map[string]any
			require.NoError(t, json.Unmarshal(<-seen, &payload))
			h := <-headers
			switch tc.id {
			case "tavily":
				assert.Equal(t, "Omnipus", payload["query"])
				assert.Equal(t, float64(1), payload["max_results"])
				assert.Equal(t, searchSettingsSecret, payload["api_key"])
				assert.Equal(t, "fast", payload["search_depth"], "ADR-096 supported lowest depth is fast; it is below the saved basic cap")
				assert.Equal(t, false, payload["include_answer"])
				assert.Equal(t, false, payload["include_images"])
				assert.Equal(t, false, payload["include_raw_content"])
			case "glm":
				assert.Equal(t, "Omnipus", payload["search_query"])
				assert.Equal(t, float64(1), payload["count"])
				assert.Equal(t, "medium", payload["content_size"], "medium is GLM's lowest supported depth")
				assert.Equal(t, "Bearer "+searchSettingsSecret, h.Get("Authorization"))
			case "baidu":
				assert.Equal(t, []any{map[string]any{"role": "user", "content": "Omnipus"}}, payload["messages"])
				assert.Equal(t, []any{map[string]any{"type": "web", "top_k": float64(1)}}, payload["resource_type_filter"])
				assert.Equal(t, "Bearer "+searchSettingsSecret, h.Get("Authorization"))
			case "exa":
				assert.Equal(t, "Omnipus", payload["query"])
				assert.Equal(t, float64(1), payload["numResults"])
				// Exa documents both auth headers: https://exa.ai/docs/reference/search.
				// The saved key must appear exactly once, in one accepted header only.
				if values := h.Values("X-Api-Key"); len(values) != 0 {
					assert.Equal(t, []string{searchSettingsSecret}, values, "Exa X-Api-Key must contain exactly one saved key")
					assert.Len(t, h.Values("Authorization"), 0, "Exa must not send both accepted auth headers")
				} else {
					assert.Equal(t, []string{"Bearer " + searchSettingsSecret}, h.Values("Authorization"), "Exa must send exactly one documented auth header with the saved key")
				}
			}
			assert.Equal(t, before, readRolesWebConfig(t, api), "diagnostics do not mutate configuration or roles")
		})
	}
}

func TestSearchConnectionCheck_BraveAndPerplexityUseBaseURL(t *testing.T) {
	// Service IDs: IntegrationProvider contract enum.
	// base_url: #1105 team-lead ruling; one attempt and success: design Decision #1105.
	// Protocol/auth oracles, not observed client output:
	// https://api-dashboard.search.brave.com/app/documentation/web-search/get-started
	// https://docs.perplexity.ai/api-reference/chat-completions-post
	for _, tc := range []struct{ id, protocolReply, authHeader, authPrefix string }{
		{"brave", `{"web":{"results":[{"title":"private-upstream-result-must-be-discarded","url":"https://example.invalid/result","description":"private-upstream-result-must-be-discarded"}]}}`, "X-Subscription-Token", ""},
		{"perplexity", `{"choices":[{"message":{"content":"private-upstream-result-must-be-discarded"}}],"citations":["https://example.invalid/result"]}`, "Authorization", "Bearer "},
	} {
		t.Run(tc.id, func(t *testing.T) {
			api, cfg, mux := newSearchCheckMux(t)
			var calls atomic.Int32
			headers := make(chan http.Header, 8)
			edge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				headers <- r.Header.Clone()
				w.Header().Set("Content-Type", "application/json")
				_, err := io.WriteString(w, tc.protocolReply)
				assert.NoError(t, err)
			}))
			t.Cleanup(edge.Close)
			wireSearchCheckEdge(t, api, cfg, tc.id, edge.URL)
			before := readRolesWebConfig(t, api)
			w := postSearchCheck(t.Context(), mux, tc.id, `{}`, diagnosticBearer)
			requireSearchCheckResult(t, w, tc.id, "success")
			require.Equal(t, int32(1), calls.Load(), "one real addressed attempt through base_url: no retries, discovery, result fetches or cached success")
			assert.Equal(t, tc.authPrefix+searchSettingsSecret, (<-headers).Get(tc.authHeader), "the addressed request must carry the saved key")
			assert.Equal(t, before, readRolesWebConfig(t, api), "diagnostics do not mutate configuration or roles")
		})
	}
}

func TestSearchConnectionCheck_NormalizesCompletedOutcomesWithoutRetry(t *testing.T) {
	for _, tc := range []struct {
		name, reply, want, retryAfter string
		code                          int
	}{
		{"empty_results", `{"results":[]}`, "success", "", http.StatusOK},
		{"unauthorized", `{"error":"private-upstream-result-must-be-discarded"}`, "auth_error", "", http.StatusUnauthorized},
		{"forbidden", `{"error":"private-upstream-result-must-be-discarded"}`, "auth_error", "", http.StatusForbidden},
		{"rate_limited", `{"error":"private-upstream-result-must-be-discarded"}`, "rate_limited", "61", http.StatusTooManyRequests},
		{"provider_error", `{"error":"private-upstream-result-must-be-discarded"}`, "provider_error", "", http.StatusServiceUnavailable},
		{"malformed", `not-json-private-upstream-result-must-be-discarded`, "invalid_response", "", http.StatusOK},
		{"wrong_shape", `{"results":"private-upstream-result-must-be-discarded"}`, "invalid_response", "", http.StatusOK},
		{"null_response", `null`, "invalid_response", "", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api, cfg, mux := newSearchCheckMux(t)
			var calls atomic.Int32
			edge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				if tc.retryAfter != "" {
					w.Header().Set("Retry-After", tc.retryAfter)
				}
				w.WriteHeader(tc.code)
				_, err := io.WriteString(w, tc.reply)
				assert.NoError(t, err)
			}))
			t.Cleanup(edge.Close)
			wireSearchCheckEdge(t, api, cfg, "tavily", edge.URL)
			before := readRolesWebConfig(t, api)
			w := postSearchCheck(t.Context(), mux, "tavily", `{}`, diagnosticBearer)
			body := requireSearchCheckResult(t, w, "tavily", tc.want)
			assert.Equal(t, int32(1), calls.Load(), "upstream failure must not retry or rotate")
			if tc.retryAfter != "" {
				assert.Equal(t, float64(61), body["retry_after_seconds"], "honor provider guidance without scheduling another request")
			}
			assert.Equal(t, before, readRolesWebConfig(t, api))
			assert.True(t, cfg.Tools.Web.UsableSearchProvider("tavily"), "a remote rejection does not switch off the service")
		})
	}
}

func TestSearchConnectionCheck_IneligibleOrInvalidRequestNeverCallsProvider(t *testing.T) {
	for _, tc := range []struct {
		name, id, body, bearer string
		want                   int
	}{
		{"unauthenticated", "tavily", `{}`, "", http.StatusUnauthorized},
		{"unknown", "not-a-provider", `{}`, diagnosticBearer, http.StatusNotFound},
		{"voice", "elevenlabs", `{}`, diagnosticBearer, http.StatusBadRequest},
		{"keyless", "duckduckgo", `{}`, diagnosticBearer, http.StatusBadRequest},
		{"no_saved_key", "tavily", `{}`, diagnosticBearer, http.StatusConflict},
		{"disabled", "tavily", `{}`, diagnosticBearer, http.StatusConflict},
		{"no_loaded_key", "tavily", `{}`, diagnosticBearer, http.StatusConflict},
		{"query_in_body", "tavily", `{"query":"private conversation"}`, diagnosticBearer, http.StatusBadRequest},
		{"key_in_body", "tavily", `{"api_key":"untrusted-key"}`, diagnosticBearer, http.StatusBadRequest},
		{"url_in_body", "tavily", `{"url":"http://127.0.0.1"}`, diagnosticBearer, http.StatusBadRequest},
		{"null_body", "tavily", `null`, diagnosticBearer, http.StatusBadRequest},
		{"empty_body", "tavily", ``, diagnosticBearer, http.StatusBadRequest},
		{"array_body", "tavily", `[]`, diagnosticBearer, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api, cfg, mux := newSearchCheckMux(t)
			var calls atomic.Int32
			edge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				_, err := io.WriteString(w, `{"results":[]}`)
				assert.NoError(t, err)
			}))
			t.Cleanup(edge.Close)
			wireSearchCheckEdge(t, api, cfg, "tavily", edge.URL)
			switch tc.name {
			case "no_saved_key":
				require.NoError(t, api.credStore.Delete("TAVILY_API_KEY"))
			case "disabled":
				cfg.Tools.Web.Tavily.Enabled = false
				web := readRolesWebConfig(t, api)
				roleSection(t, web, "tavily")["enabled"] = false
				writeRolesWebConfig(t, api, web)
			case "no_loaded_key":
				t.Setenv("TAVILY_API_KEY", "")
			}
			before := readRolesWebConfig(t, api)
			w := postSearchCheck(t.Context(), mux, tc.id, tc.body, tc.bearer)
			assert.Equal(t, tc.want, w.Code, "design's eligibility/auth/closed-contract rejection; body=%s", w.Body.String())
			assert.Equal(t, int32(0), calls.Load(), "rejected checks spend no provider request")
			assert.Equal(t, before, readRolesWebConfig(t, api))
		})
	}
}

func TestSearchConnectionCheck_NetworkErrorIsNormalized(t *testing.T) {
	api, cfg, mux := newSearchCheckMux(t)
	// Closing the listener makes a real connection-refused at the process
	// edge; no invented provider exception and no external network access.
	edge := httptest.NewServer(http.NotFoundHandler())
	url := edge.URL
	edge.Close()
	wireSearchCheckEdge(t, api, cfg, "tavily", url)
	w := postSearchCheck(t.Context(), mux, "tavily", `{}`, diagnosticBearer)
	requireSearchCheckResult(t, w, "tavily", "network_error")
	assert.NotContains(t, w.Body.String(), url, "internal transport exception text must not escape")
}

func TestSearchConnectionCheck_FifteenSecondTotalContextDeadline(t *testing.T) {
	api, cfg, mux := newSearchCheckMux(t)
	var calls atomic.Int32
	release := make(chan struct{})
	edge := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(edge.Close)
	t.Cleanup(func() { close(release) })
	wireSearchCheckEdge(t, api, cfg, "baidu", edge.URL)
	// newBaiduSearchProvider uses the 30s perplexityTimeout, so unlike Tavily's
	// 10s client it cannot mask the design's 15s diagnostic context budget.
	// 17s is only a harness watchdog, not the allowed diagnostic budget. The
	// design's 15s child context must end this earlier. The 1s upper tolerance
	// permits scheduling and HTTP bookkeeping, not a longer provider timeout.
	ctx, cancel := context.WithTimeout(t.Context(), 17*time.Second)
	defer cancel()
	start := time.Now()
	w := postSearchCheck(ctx, mux, "baidu", `{}`, diagnosticBearer)
	requireSearchCheckResult(t, w, "baidu", "timeout")
	elapsed := time.Since(start)
	assert.LessOrEqual(t, elapsed, 16*time.Second, "total budget is 15 seconds, not a per-attempt timeout")
	assert.Equal(t, int32(1), calls.Load())
}

func TestSearchConnectionCheck_ParentCancellationClosesRealRequest(t *testing.T) {
	api, cfg, mux := newSearchCheckMux(t)
	started := make(chan struct{}, 8)
	cancelled := make(chan struct{}, 8)
	release := make(chan struct{})
	edge := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		// Drain the body before signalling readiness so net/http can observe the
		// peer close; an unread body hides cancellation from this fixture.
		_, err := io.Copy(io.Discard, r.Body)
		assert.NoError(t, err, "controlled provider must consume the request body before cancellation")
		started <- struct{}{}
		select {
		case <-r.Context().Done():
			cancelled <- struct{}{}
		case <-release:
		}
	}))
	t.Cleanup(edge.Close)
	t.Cleanup(func() { close(release) })
	wireSearchCheckEdge(t, api, cfg, "tavily", edge.URL)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	finished := make(chan *httptest.ResponseRecorder, 1)
	go func() { finished <- postSearchCheck(ctx, mux, "tavily", `{}`, diagnosticBearer) }()
	select {
	case <-started:
	case w := <-finished:
		t.Fatalf("BLOCKED: cancelable diagnostic not implemented — required by Decision #1105; returned HTTP %d before making a real request: %s", w.Code, w.Body.String())
	case <-time.After(time.Second):
		t.Fatal("diagnostic did not reach the controlled provider before cancellation")
	}
	cancel()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("disconnect must cancel the real provider request, not leave it running for 15 seconds")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("gateway did not finish after caller cancellation")
	}
}

func TestSearchConnectionCheck_OneInFlightAcrossAccountsAndNoFallback(t *testing.T) {
	api, cfg, mux := newSearchCheckMux(t)
	var calls, fallbackCalls atomic.Int32
	started := make(chan struct{}, 8)
	release := make(chan struct{})
	var releaseOnce sync.Once
	edge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		started <- struct{}{}
		<-release
		w.WriteHeader(http.StatusUnauthorized)
		_, err := io.WriteString(w, `{"error":"key rejected"}`)
		assert.NoError(t, err)
	}))
	t.Cleanup(edge.Close)
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fallbackCalls.Add(1)
		_, err := io.WriteString(w, `{"search_result":[]}`)
		assert.NoError(t, err)
	}))
	t.Cleanup(fallback.Close)
	wireSearchCheckEdge(t, api, cfg, "tavily", edge.URL)
	wireSearchCheckEdge(t, api, cfg, "glm", fallback.URL)
	cfg.Tools.Web.DefaultProvider, cfg.Tools.Web.FallbackProvider = "tavily", "glm"
	web := readRolesWebConfig(t, api)
	web["fallback_provider"] = "glm"
	writeRolesWebConfig(t, api, web)
	otherHash, err := bcrypt.GenerateFromPassword([]byte("other-tab-token"), bcrypt.MinCost)
	require.NoError(t, err)
	cfg.Gateway.Users = append(cfg.Gateway.Users, config.UserConfig{Username: "other-account", TokenHash: config.BcryptHash(otherHash)})
	finished := make(chan *httptest.ResponseRecorder, 1)
	go func() { finished <- postSearchCheck(t.Context(), mux, "tavily", `{}`, diagnosticBearer) }()
	select {
	case <-started:
	case w := <-finished:
		t.Fatalf("BLOCKED: admitted diagnostic not implemented — required by Decision #1105 rate safety; HTTP %d: %s", w.Code, w.Body.String())
	case <-time.After(time.Second):
		t.Fatal("first admitted check never reached the provider")
	}
	second := postSearchCheck(t.Context(), mux, "tavily", `{}`, "other-tab-token")
	assert.Equal(t, http.StatusTooManyRequests, second.Code, "admission is per instance/service, not per user/tab")
	delay, err := strconv.Atoi(second.Header().Get("Retry-After"))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, delay, 1)
	assert.LessOrEqual(t, delay, 30)
	assert.Equal(t, int32(1), calls.Load())
	releaseOnce.Do(func() { close(release) })
	first := <-finished
	requireSearchCheckResult(t, first, "tavily", "auth_error")
	assert.Equal(t, int32(0), fallbackCalls.Load(), "addressed rejection must never route to the usable configured fallback")
	third := postSearchCheck(t.Context(), mux, "tavily", `{}`, diagnosticBearer)
	assert.Equal(t, http.StatusTooManyRequests, third.Code, "failed checks consume the same 30-second admission window")
	assert.Equal(t, int32(1), calls.Load(), "cooldown rejection makes no second attempt")
}

func TestSearchConnectionCheck_AfterThirtySecondsMakesFreshAttemptNotCachedSuccess(t *testing.T) {
	api, cfg, mux := newSearchCheckMux(t)
	var calls atomic.Int32
	edge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, err := io.WriteString(w, `{"results":[]}`)
		assert.NoError(t, err)
	}))
	t.Cleanup(edge.Close)
	wireSearchCheckEdge(t, api, cfg, "tavily", edge.URL)
	first := requireSearchCheckResult(t, postSearchCheck(t.Context(), mux, "tavily", `{}`, diagnosticBearer), "tavily", "success")
	blocked := postSearchCheck(t.Context(), mux, "tavily", `{}`, diagnosticBearer)
	require.Equal(t, http.StatusTooManyRequests, blocked.Code)
	require.Equal(t, int32(1), calls.Load())
	// Real-clock process-edge test: one second beyond the specified 30s
	// boundary avoids replacing a not-yet-existing internal admission clock.
	time.Sleep(31 * time.Second)
	second := requireSearchCheckResult(t, postSearchCheck(t.Context(), mux, "tavily", `{}`, diagnosticBearer), "tavily", "success")
	assert.Equal(t, int32(2), calls.Load(), "the repeat must make a fresh provider call")
	assert.NotEqual(t, first["checked_at"], second["checked_at"])
}

// slog can receive background writes, so capture under a mutex rather than
// sharing a plain bytes.Buffer with the real agent loop.
type searchDiagnosticLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *searchDiagnosticLog) Write(data []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(data)
}

func (l *searchDiagnosticLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func TestSearchConnectionCheck_EchoedAndReplacedKeyNeverEscapesResponseLogsOrAudit(t *testing.T) {
	api, cfg, mux := newSearchCheckMux(t)
	capture := &searchDiagnosticLog{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(capture, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	slog.Info("diagnostic-log-positive-control")
	require.Contains(t, capture.String(), "diagnostic-log-positive-control", "capture must see real slog output")
	var calls atomic.Int32
	started := make(chan struct{}, 8)
	release := make(chan struct{})
	var releaseOnce sync.Once
	edge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		data, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		assert.Contains(t, string(data), searchSettingsSecret, "the real attempt pins the original saved key")
		started <- struct{}{}
		<-release
		w.Header().Set("X-Upstream-Debug", searchSettingsSecret)
		w.WriteHeader(http.StatusUnauthorized)
		_, err = io.WriteString(w, `{"error":"Bearer `+searchSettingsSecret+` private-upstream-result-must-be-discarded"}`)
		assert.NoError(t, err)
	}))
	t.Cleanup(edge.Close)
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	wireSearchCheckEdge(t, api, cfg, "tavily", edge.URL)
	finished := make(chan *httptest.ResponseRecorder, 1)
	go func() { finished <- postSearchCheck(t.Context(), mux, "tavily", `{}`, diagnosticBearer) }()
	select {
	case <-started:
	case w := <-finished:
		t.Fatalf("BLOCKED: real in-flight key-redacted diagnostic not implemented — required by Decision #1105 Privacy; HTTP %d: %s", w.Code, w.Body.String())
	case <-time.After(time.Second):
		t.Fatal("diagnostic never reached the controlled upstream")
	}
	// Replace the store/env and refresh the live redaction bundle while the
	// original request is still in flight. A new bundle alone cannot protect
	// the old key: the diagnostic must retain request-local safe redaction.
	require.NoError(t, api.credStore.Set("TAVILY_API_KEY", "replacement-in-flight-secret"))
	require.NoError(t, os.Setenv("TAVILY_API_KEY", "replacement-in-flight-secret"))
	cfg.RegisterSensitiveValues([]string{"replacement-in-flight-secret"})
	releaseOnce.Do(func() { close(release) })
	w := <-finished
	requireSearchCheckResult(t, w, "tavily", "auth_error")
	assert.Equal(t, int32(1), calls.Load(), "no retry with the replacement key")
	assert.NotContains(t, capture.String(), searchSettingsSecret)
	assert.NotContains(t, capture.String(), "replacement-in-flight-secret")
	assert.NotContains(t, capture.String(), discardedSearchContent)
	auditBytes, err := os.ReadFile(filepath.Join(api.homePath, "system", "audit.jsonl"))
	require.NoError(t, err, "real enabled audit sink must exist; absence is not proof of redaction")
	assert.NotContains(t, string(auditBytes), searchSettingsSecret)
	assert.NotContains(t, string(auditBytes), "replacement-in-flight-secret")
	assert.NotContains(t, string(auditBytes), discardedSearchContent)
}
