// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// ADR-094 FR-015 (founder decision 2026-11-09, option A): every sign-in point
// stores the session's issue time next to its hash, so the planted-cookie
// guard can re-send the genuine cookie with its ORIGINAL expiry (issue time +
// SessionCookieMaxAge). Two sign-in points mint a session:
// HandleLogin (rest_auth.go) and middleware.IssueSessionCookie, which both
// onboarding and the provider sign-in host (signin_provider.go::
// restAPIHost.IssueSession) call.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// requireIssuedWithin parses an RFC 3339 issue time and checks it lies in
// [before, after] at second precision.
func requireIssuedWithin(t *testing.T, raw string, before, after time.Time) time.Time {
	t.Helper()
	require.NotEmpty(t, raw, "session_issued_at must be stored with the session hash")
	got, err := time.Parse(time.RFC3339, raw)
	require.NoError(t, err, "session_issued_at must be RFC 3339, got %q", raw)
	require.False(t, got.Before(before.Truncate(time.Second)), "issue time %s is before the request (%s)", got, before)
	require.False(t, got.After(after), "issue time %s is after the response (%s)", got, after)
	return got
}

func TestHandleLogin_PersistsSessionIssuedAt(t *testing.T) {
	tmpDir := t.TempDir()
	hash, err := bcrypt.GenerateFromPassword([]byte("TestPass123"), bcrypt.DefaultCost)
	require.NoError(t, err)
	createTestConfigWithUser(t, tmpDir, "issueduser", string(hash))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
		},
	}
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	api := &restAPI{
		agentLoop:     al,
		homePath:      tmpDir,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		taskStore:     task.New(tmpDir + "/tasks"),
	}

	before := time.Now().UTC()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		strings.NewReader(`{"username":"issueduser","password":"TestPass123"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.HandleLogin(w, req)
	after := time.Now().UTC()
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	users := loadDiskUsers(t, tmpDir)
	require.Len(t, users, 1)
	raw, _ := users[0]["session_issued_at"].(string)
	requireIssuedWithin(t, raw, before, after)

	// The in-memory config the planted-cookie guard reads carries the same value.
	mem := al.GetConfig().Gateway.Users
	require.Len(t, mem, 1)
	require.Equal(t, raw, mem[0].SessionIssuedAt, "the live config must carry the stored issue time")
}

func TestIssueSessionCookie_PersistsSessionIssuedAt(t *testing.T) {
	doc := map[string]any{"gateway": map[string]any{"users": []any{
		map[string]any{"username": "alice"},
	}}}
	mutator := func(fn func(map[string]any) error) error { return fn(doc) }

	before := time.Now().UTC()
	w := httptest.NewRecorder()
	token, err := middleware.IssueSessionCookie(w, httptest.NewRequest(http.MethodPost, "/", nil), "alice", mutator)
	after := time.Now().UTC()
	require.NoError(t, err)

	gw, ok := doc["gateway"].(map[string]any)
	require.True(t, ok)
	users, ok := gw["users"].([]any)
	require.True(t, ok)
	um, ok := users[0].(map[string]any)
	require.True(t, ok)
	raw, _ := um["session_issued_at"].(string)
	requireIssuedWithin(t, raw, before, after)
	hash, _ := um["session_token_hash"].(string)
	require.NoError(t, bcrypt.CompareHashAndPassword([]byte(hash), []byte(token)),
		"the issue time is written together with the hash of the issued token")
}
