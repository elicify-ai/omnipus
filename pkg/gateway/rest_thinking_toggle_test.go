// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// WP-C RED tests for the per-login show_thinking toggle endpoint (spec §6
// boundary constraint, C1, FR-003, FR-004; §16 items 11 and 12).
//
// Spec oracles (docs/internal/specs/thinking-reasoning-spec.md):
//   - "A toggle change MUST persist immediately, take effect on the next
//     read without a gateway restart, and record an audit row under the
//     toggle's own event (indicatively settings.show_thinking.changed,
//     dotted lowercase per pkg/audit/events.go convention) carrying the
//     login and the new boolean value — never a token or hash."
//   - FR-003: the preference is OFF by default.
//   - FR-004 + C1: live flip, idempotent PUT, response carries the
//     persisted state.
//   - Boundary: a CLI-token principal has no per-login preference and is
//     refused (the gate keys on authentication method, never username).
//
// Pinned production symbols:
//   restAPI.HandleGetThinkingPreference / restAPI.HandlePutThinkingPreference
//     (rest_auth.go or sibling — backend-lead's placement)
//   config.UserConfig.ShowThinking
//
// RED: compile-fail on the pinned symbols — the handlers and the config
// field do not exist yet.
//
// Disclosures:
//   - CSRF double-submit is enforced by the shared mux middleware chain, not
//     by the handler; a handler-level test cannot exercise it.
//   - The PUT path's reload (triggerReloadAndWaitOutcome) is not observable
//     at handler level; the gate's live-read across surfaces is already
//     pinned by TestThinkingVisible_GatePredicate's live-flip subtest, and
//     the reload call itself is GREEN-review scope.
//   - The generic config-write refusal for the users area is ALREADY covered
//     by pkg/gateway/blocked_paths_test.go — reported, no new test written.
//   - The audit event NAME and emission function are backend-lead's choice;
//     the tests pin the CONTENT oracle (a row naming the toggle, carrying
//     the login and the new boolean, never token/hash material), not an
//     event string.
//
// Harness note: wpcToggleAPI builds the restAPI with the AgentLoop booted
// with audit ON (config.DefaultConfig + coreagent.SeedConfig marks audit
// default-on), because AgentLoop exposes AuditLogger() but no setter — the
// same reason rest_sandbox_config_test.go's audit test emits records
// directly. Unlike that precedent, here the loop OWNS the logger, so the
// handler's own emission is what lands in audit.jsonl.

const wpcToggleUser = "alice"

// wpcToggleIdentity describes the request's injected identity.
type wpcToggleIdentity struct {
	username     string
	showThinking bool
	cliToken     bool
}

// wpcToggleAPI builds a restAPI whose AgentLoop booted with audit logging
// enabled, writing to <home>/system/audit.jsonl. Defaults.Home is nested
// under home so that filepath.Dir(cfg.AgentHomeBasePath()) — the AgentLoop's
// homePath and the audit dir's parent — IS home, and configPath()
// (homePath/config.json) matches OMNIPUS_HOME. Mirrors
// newTestRestAPIWithAgent's wiring.
func wpcToggleAPI(t *testing.T) *restAPI {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := config.DefaultConfig()
	coreagent.SeedConfig(cfg)
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
	cfg.Agents.Defaults.MaxTokens = 4096
	cfg.Agents.Defaults.Home = filepath.Join(home, "agents")
	require.NoError(t, os.MkdirAll(filepath.Join(home, "agents"), 0o700))

	// A real users row for alice with NO show_thinking key — the FR-003
	// default-off state. Written before boot so in-memory and on-disk reads
	// agree.
	cfgJSON := `{"version":1,"gateway":{"users":[{"username":"alice","password_hash":"x"}]},"agents":{"defaults":{},"list":[]},"providers":[]}`
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.json"), []byte(cfgJSON), 0o600))

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })

	al, err := agent.NewAgentLoop(cfg, msgBus, &restMockProvider{})
	require.NoError(t, err)
	t.Cleanup(func() { al.Close() })

	api := &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(home),
		homePath:      home,
		taskStore:     task.New(filepath.Join(home, "tasks")),
		taskLock:      task.TaskFileLock,
	}
	t.Cleanup(func() { api.agentLoop.WaitForActiveRequests() })
	return api
}

// wpcToggleReq builds a request whose context carries the identity the way
// the auth middleware installs it: a login principal is a *config.UserConfig
// under ctxkey.UserContextKey (the HandleChangePassword identity pattern); a
// CLI-token principal additionally carries ctxkey.CLITokenContextKey with a
// synthetic UserConfig (see ctxkey.CLITokenContextKey's doc).
func wpcToggleReq(t *testing.T, method, body string, ident wpcToggleIdentity) *http.Request {
	t.Helper()
	r := httptest.NewRequest(method, "/api/v1/auth/preferences/thinking", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	ctx := r.Context()
	if ident.cliToken {
		ctx = context.WithValue(ctx, ctxkey.UserContextKey{}, &config.UserConfig{
			Username:     "cli",
			ShowThinking: ident.showThinking,
		})
		ctx = context.WithValue(ctx, ctxkey.CLITokenContextKey{}, true)
	} else if ident.username != "" {
		ctx = context.WithValue(ctx, ctxkey.UserContextKey{}, &config.UserConfig{
			Username:     ident.username,
			ShowThinking: ident.showThinking,
		})
	}
	return r.WithContext(ctx)
}

// wpcReadUsers returns config.json's gateway.users rows.
func wpcReadUsers(t *testing.T, homePath string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(homePath, "config.json"))
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	gw, ok := m["gateway"].(map[string]any)
	require.True(t, ok, "config.json must keep its gateway section")
	users, ok := gw["users"].([]any)
	require.True(t, ok, "config.json must keep its gateway.users array")
	var out []map[string]any
	for _, u := range users {
		um, ok := u.(map[string]any)
		require.True(t, ok, "users rows must be objects")
		out = append(out, um)
	}
	return out
}

// wpcUserFlag returns (value, found) for the named user's show_thinking key.
func wpcUserFlag(t *testing.T, homePath, username string) (bool, bool) {
	t.Helper()
	for _, um := range wpcReadUsers(t, homePath) {
		if um["username"] == username {
			v, ok := um["show_thinking"].(bool)
			return v, ok
		}
	}
	return false, false
}

// wpcWalkKeys visits every object key at any depth of a decoded JSON value.
func wpcWalkKeys(v any, fn func(string)) {
	switch t := v.(type) {
	case map[string]any:
		for k, e := range t {
			fn(k)
			wpcWalkKeys(e, fn)
		}
	case []any:
		for _, e := range t {
			wpcWalkKeys(e, fn)
		}
	}
}

// wpcHasBoolValue reports whether any value at any depth equals target.
func wpcHasBoolValue(v any, target bool) bool {
	switch t := v.(type) {
	case bool:
		return t == target
	case map[string]any:
		for _, e := range t {
			if wpcHasBoolValue(e, target) {
				return true
			}
		}
	case []any:
		for _, e := range t {
			if wpcHasBoolValue(e, target) {
				return true
			}
		}
	}
	return false
}

// TestHandleThinkingPreference_GETDefaultOff pins FR-003: with no
// show_thinking key on the user's row, the preference reads OFF.
func TestHandleThinkingPreference_GETDefaultOff(t *testing.T) {
	api := wpcToggleAPI(t)

	w := httptest.NewRecorder()
	api.HandleGetThinkingPreference(w, wpcToggleReq(t, http.MethodGet, "", wpcToggleIdentity{username: wpcToggleUser}))
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var got map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, false, got["show_thinking"],
		"FR-003: the preference is OFF by default (row carries no show_thinking key)")
}

// TestHandleThinkingPreference_PutFlipsPersistsAndAudits pins the §6
// constraint in its three parts: the response carries the persisted state
// (C1), the flip is on disk immediately (FR-004), and audit.jsonl carries a
// toggle row with the login and the new boolean and no token/hash material.
func TestHandleThinkingPreference_PutFlipsPersistsAndAudits(t *testing.T) {
	api := wpcToggleAPI(t)

	w := httptest.NewRecorder()
	api.HandlePutThinkingPreference(w, wpcToggleReq(t, http.MethodPut, `{"show_thinking":true}`,
		wpcToggleIdentity{username: wpcToggleUser}))
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var got map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, true, got["show_thinking"],
		"the response carries the PERSISTED state (C1), not the pre-PUT value")

	v, ok := wpcUserFlag(t, api.homePath, wpcToggleUser)
	require.True(t, ok, "the PUT must add the show_thinking key to alice's row")
	assert.True(t, v, "FR-004/§6: the flip persists to config.json immediately")

	// Audit content oracle. The row is decoded JSON; the login must appear
	// (as a value), the new boolean must be a real true somewhere, and no
	// key at any depth may carry token/hash material.
	auditPath := filepath.Join(api.homePath, "system", "audit.jsonl")
	require.FileExists(t, auditPath, "audit.jsonl must exist after the PUT (audit is on in this fixture)")
	data, err := os.ReadFile(auditPath)
	require.NoError(t, err)
	var matched bool
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, "show_thinking") {
			continue
		}
		matched = true
		var row map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &row), "toggle audit row must be valid JSON: %s", line)
		assert.Contains(t, line, wpcToggleUser, "the toggle row carries the login")
		assert.True(t, wpcHasBoolValue(row, true),
			"the toggle row carries the new boolean value (true) — not a string echo of the request")
		wpcWalkKeys(row, func(k string) {
			lk := strings.ToLower(k)
			for _, banned := range []string{"token", "hash", "password"} {
				assert.NotContains(t, lk, banned,
					"the toggle row never carries token/hash material (key %q)", k)
			}
		})
	}
	assert.True(t, matched, "an audit row naming show_thinking must exist after the PUT")
}

// TestHandleThinkingPreference_PutIsIdempotent pins C1's idempotency: the
// same PUT twice succeeds identically and the state stays true.
func TestHandleThinkingPreference_PutIsIdempotent(t *testing.T) {
	api := wpcToggleAPI(t)

	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		api.HandlePutThinkingPreference(w, wpcToggleReq(t, http.MethodPut, `{"show_thinking":true}`,
			wpcToggleIdentity{username: wpcToggleUser}))
		require.Equal(t, http.StatusOK, w.Code, "PUT %d: body: %s", i+1, w.Body.String())
		var got map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
		assert.Equal(t, true, got["show_thinking"],
			"PUT %d must report the persisted state (C1 idempotency)", i+1)
	}
	v, ok := wpcUserFlag(t, api.homePath, wpcToggleUser)
	require.True(t, ok)
	assert.True(t, v, "state stays true after the second identical PUT")
}

// TestHandleThinkingPreference_UnauthenticatedRefused pins the identity
// requirement (HandleChangePassword precedent): no identity in context is a
// 401 on both methods.
func TestHandleThinkingPreference_UnauthenticatedRefused(t *testing.T) {
	api := wpcToggleAPI(t)

	t.Run("GET", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/auth/preferences/thinking", nil)
		api.HandleGetThinkingPreference(w, r)
		assert.Equal(t, http.StatusUnauthorized, w.Code,
			"no identity in context must 401 (HandleChangePassword precedent)")
	})

	t.Run("PUT", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPut, "/api/v1/auth/preferences/thinking", strings.NewReader(`{"show_thinking":true}`))
		r.Header.Set("Content-Type", "application/json")
		api.HandlePutThinkingPreference(w, r)
		assert.Equal(t, http.StatusUnauthorized, w.Code,
			"no identity in context must 401 (HandleChangePassword precedent)")
	})
}

// TestHandleThinkingPreference_CLITokenRefused pins the boundary: a
// CLI-token principal has no per-login preference to toggle — the PUT is
// refused (non-2xx; the spec pins only "refused") and no user's row changed.
func TestHandleThinkingPreference_CLITokenRefused(t *testing.T) {
	api := wpcToggleAPI(t)

	before, _ := json.Marshal(wpcReadUsers(t, api.homePath))

	w := httptest.NewRecorder()
	api.HandlePutThinkingPreference(w, wpcToggleReq(t, http.MethodPut, `{"show_thinking":true}`,
		wpcToggleIdentity{cliToken: true}))
	assert.False(t, w.Code >= 200 && w.Code < 300,
		"a CLI-token principal must be refused, got %d: %s", w.Code, w.Body.String())

	after, _ := json.Marshal(wpcReadUsers(t, api.homePath))
	assert.Equal(t, string(before), string(after),
		"the CLI-token PUT must not create or flip any user's row")
}

// TestHandleThinkingPreference_MethodGuard pins endpoint semantics: the GET
// handler takes GET only, the PUT handler PUT only (405 otherwise, as in
// HandleChangePassword).
func TestHandleThinkingPreference_MethodGuard(t *testing.T) {
	api := wpcToggleAPI(t)

	w := httptest.NewRecorder()
	api.HandleGetThinkingPreference(w, httptest.NewRequest(http.MethodPost, "/api/v1/auth/preferences/thinking", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code, "POST to the GET handler must 405")

	w2 := httptest.NewRecorder()
	api.HandlePutThinkingPreference(w2, httptest.NewRequest(http.MethodGet, "/api/v1/auth/preferences/thinking", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, w2.Code, "GET on the PUT handler must 405")
}
