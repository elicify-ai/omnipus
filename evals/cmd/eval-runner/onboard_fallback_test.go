package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Oracle: design.md §4.2 (eval-runner), §4.3 (key names) and §7 R2.
// The exact HTTP shape is /workspace/ci-minimax/api-contract.md §4.
// Keys below are fake. The test never reads a real secret from the environment.

const (
	fakeMinimaxKey    = "test-key-aaaa"
	fakeOpenRouterKey = "test-key-bbbb"
)

const centralModelFixture = `{
  "primary": { "provider": "minimax-coding-plan", "model": "MiniMax-M3.1-Flash-Preview", "api_key_ref": "MINIMAX_API_KEY" },
  "fallback": { "provider": "openrouter", "model": "deepseek/deepseek-v4.1-flash", "api_key_ref": "OPENROUTER_API_KEY" }
}
`

type recordedCall struct {
	Method string
	Path   string
	Body   map[string]any
}

func chdirWithCentralModel(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "tests", "e2e")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "e2e-model.json"), []byte(centralModelFixture), 0o600); err != nil {
		t.Fatalf("write e2e-model.json: %v", err)
	}
	t.Chdir(root)
}

func clearModelKeyEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"MINIMAX_API_KEY_CI", "MINIMAX_API_KEY_EVAL", "MINIMAX_API_KEY",
		"OPENROUTER_API_KEY_EVAL", "OPENROUTER_API_KEY_CI", "OPENROUTER_API_KEY",
	} {
		t.Setenv(name, "")
	}
}

// newOnboardGateway records every request. GET /api/v1/agents returns mia then jim.
// Every other route returns 200 with a bearer token so onboard can proceed.
func newOnboardGateway(t *testing.T) (*httptest.Server, *[]recordedCall) {
	t.Helper()
	var mu sync.Mutex
	got := []recordedCall{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		body := map[string]any{}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Errorf("body is not a JSON object: %v\n%s", err, raw)
			}
		}
		mu.Lock()
		got = append(got, recordedCall{Method: r.Method, Path: r.URL.Path, Body: body})
		mu.Unlock()

		http.SetCookie(w, &http.Cookie{Name: csrfCookieName, Value: "csrf-test"})
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/agents" {
			_, _ = w.Write([]byte(`[{"id":"mia"},{"id":"jim"}]`))
			return
		}
		_, _ = w.Write([]byte(`{"token":"eval-token"}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func callSummary(calls []recordedCall) string {
	var b strings.Builder
	for i, c := range calls {
		raw, _ := json.Marshal(c.Body)
		fmt.Fprintf(&b, "  [%d] %s %s %s\n", i, c.Method, c.Path, raw)
	}
	if b.Len() == 0 {
		return "  (no requests)\n"
	}
	return b.String()
}

func requireCall(t *testing.T, calls []recordedCall, i int, method, path string) recordedCall {
	t.Helper()
	if i >= len(calls) {
		t.Fatalf("request %d = missing, want %s %s\nactual:\n%s", i, method, path, callSummary(calls))
	}
	got := calls[i]
	if got.Method != method || got.Path != path {
		t.Fatalf("request %d = %s %s, want %s %s\nactual:\n%s", i, got.Method, got.Path, method, path, callSummary(calls))
	}
	return got
}

func requireJSONEqual(t *testing.T, what string, got any, want any) {
	t.Helper()
	gotRaw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal got %s: %v", what, err)
	}
	wantRaw, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal want %s: %v", what, err)
	}
	if string(gotRaw) != string(wantRaw) {
		t.Errorf("%s = %s, want %s", what, gotRaw, wantRaw)
	}
}

// TestOnboardUsesTheProviderKeyAndSeedsTheFallback pins R2: the MiniMax slug
// is onboarded with the MiniMax key, then the OpenRouter fallback row is
// added and every agent receives fallback_models.
func TestOnboardUsesTheProviderKeyAndSeedsTheFallback(t *testing.T) {
	chdirWithCentralModel(t)
	clearModelKeyEnv(t)
	t.Setenv("MINIMAX_API_KEY_CI", fakeMinimaxKey)
	t.Setenv("OPENROUTER_API_KEY_EVAL", fakeOpenRouterKey)

	srv, got := newOnboardGateway(t)
	h := &gatewayHandle{baseURL: srv.URL}
	if err := h.onboard("minimax-coding-plan/MiniMax-M3.1-Flash-Preview"); err != nil {
		t.Fatalf("onboard() error = %v\nrequests:\n%s", err, callSummary(*got))
	}
	if len(*got) != 5 {
		t.Fatalf("onboard made %d requests, want 5 (onboard, PUT fallback provider, GET agents, PUT mia, PUT jim)\n%s", len(*got), callSummary(*got))
	}

	onboard := requireCall(t, *got, 0, http.MethodPost, "/api/v1/onboarding/complete")
	requireJSONEqual(t, "onboarding provider", onboard.Body["provider"], map[string]any{
		"auth_method": "api_key",
		"id":          "minimax-coding-plan",
		"api_key":     fakeMinimaxKey,
		"model":       "MiniMax-M3.1-Flash-Preview",
	})
	admin, _ := onboard.Body["admin"].(map[string]any)
	if admin["username"] != "eval-admin" {
		t.Errorf("admin.username = %v, want eval-admin", admin["username"])
	}

	fallback := requireCall(t, *got, 1, http.MethodPut, "/api/v1/providers/openrouter")
	requireJSONEqual(t, "fallback provider body", fallback.Body, map[string]any{
		"auth_method": "api_key",
		"api_key":     fakeOpenRouterKey,
		"model":       "deepseek/deepseek-v4.1-flash",
	})

	requireCall(t, *got, 2, http.MethodGet, "/api/v1/agents")

	wantAgent := map[string]any{
		"fallback_models": []any{
			map[string]any{"provider": "openrouter", "model": "deepseek/deepseek-v4.1-flash"},
		},
	}
	mia := requireCall(t, *got, 3, http.MethodPut, "/api/v1/agents/mia")
	requireJSONEqual(t, "mia fallback_models", mia.Body, wantAgent)
	jim := requireCall(t, *got, 4, http.MethodPut, "/api/v1/agents/jim")
	requireJSONEqual(t, "jim fallback_models", jim.Body, wantAgent)
}

// TestOnboardMissingMinimaxKeyNamesBothEnvVars: a MiniMax primary with neither
// MiniMax env var set must fail before any HTTP, and the error must name both
// variables. An OpenRouter key being present must not be used instead.
func TestOnboardMissingMinimaxKeyNamesBothEnvVars(t *testing.T) {
	chdirWithCentralModel(t)
	clearModelKeyEnv(t)
	t.Setenv("OPENROUTER_API_KEY_EVAL", fakeOpenRouterKey)

	srv, got := newOnboardGateway(t)
	h := &gatewayHandle{baseURL: srv.URL}
	err := h.onboard("minimax-coding-plan/MiniMax-M3.1-Flash-Preview")
	if err == nil {
		t.Fatalf("onboard() error = nil, want an error naming MINIMAX_API_KEY_CI and MINIMAX_API_KEY_EVAL\nrequests:\n%s", callSummary(*got))
	}
	if !strings.Contains(err.Error(), "MINIMAX_API_KEY_CI") || !strings.Contains(err.Error(), "MINIMAX_API_KEY_EVAL") {
		t.Errorf("error = %q, want it to name MINIMAX_API_KEY_CI and MINIMAX_API_KEY_EVAL", err.Error())
	}
	if len(*got) != 0 {
		t.Errorf("onboard sent %d requests before reporting the missing key; want 0\n%s", len(*got), callSummary(*got))
	}
	_ = srv
}

// TestOnboardMissingFallbackKeyNamesTheFallback: the primary key is present and
// the OpenRouter eval key is not. The error must say this is the fallback key.
func TestOnboardMissingFallbackKeyNamesTheFallback(t *testing.T) {
	chdirWithCentralModel(t)
	clearModelKeyEnv(t)
	t.Setenv("MINIMAX_API_KEY_CI", fakeMinimaxKey)

	srv, got := newOnboardGateway(t)
	h := &gatewayHandle{baseURL: srv.URL}
	err := h.onboard("minimax-coding-plan/MiniMax-M3.1-Flash-Preview")
	if err == nil {
		t.Fatalf("onboard() error = nil, want an error naming the fallback key OPENROUTER_API_KEY_EVAL\nrequests:\n%s", callSummary(*got))
	}
	if !strings.Contains(err.Error(), "OPENROUTER_API_KEY_EVAL") {
		t.Errorf("error = %q, want it to name OPENROUTER_API_KEY_EVAL", err.Error())
	}
	if !strings.Contains(err.Error(), "fallback") {
		t.Errorf("error = %q, want it to say this missing key is the fallback (substring \"fallback\")", err.Error())
	}
	_ = srv
}
