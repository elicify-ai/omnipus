// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"bufio"
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

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// newTestRestAPIWithAuditLog creates a restAPI where the agent loop has a real
// audit logger wired (Sandbox.AuditLog = true). The workspace is placed at
// tmpDir/workspace so that the agent loop calculates homePath = tmpDir and
// writes audit entries to tmpDir/system/.
func newTestRestAPIWithAuditLog(t *testing.T) (*restAPI, string) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	workspaceDir := filepath.Join(tmpDir, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o700))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         workspaceDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
		Sandbox: config.OmnipusSandboxConfig{
			AuditLog: true,
		},
	}

	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"sandbox":{"audit_log":true}}`)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config.json"), minimalCfg, 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		homePath:      tmpDir,
		taskStore:     task.New(filepath.Join(tmpDir, "tasks")),
	}
	return api, tmpDir
}

// adminCtx returns a context with an authenticated user set — used for PUT
// tests. Under the single-user model there is no separate role to inject;
// the handlers under test here are gated purely by RequireNotBypass at
// route-registration time (not exercised by these direct-handler-call
// tests) and read the actor identity from ctxkey.UserContextKey for audit
// attribution.
func adminCtx() context.Context {
	ctx := context.WithValue(context.Background(), ctxkey.UserContextKey{}, &config.UserConfig{Username: "admin"})
	return ctx
}

// TestHandleSandboxAuditLog_PUTPersists verifies that PUT {enabled:true}
// writes sandbox.audit_log=true to config.json on disk.
func TestHandleSandboxAuditLog_PUTPersists(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	body := strings.NewReader(`{"enabled":true}`)
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/audit-log", body)
	r = r.WithContext(adminCtx())
	w := httptest.NewRecorder()

	api.HandleSandboxAuditLog(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	raw, err := os.ReadFile(api.homePath + "/config.json")
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	sandbox, _ := m["sandbox"].(map[string]any)
	require.NotNil(t, sandbox, "sandbox key must exist in config.json after PUT")
	assert.Equal(t, true, sandbox["audit_log"], "audit_log must be persisted as true")
}

// TestHandleSandboxAuditLog_ResponseShape verifies the response JSON contains
// saved, requires_restart:true, and applied_enabled with correct types.
func TestHandleSandboxAuditLog_ResponseShape(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	body := strings.NewReader(`{"enabled":true}`)
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/audit-log", body)
	r = r.WithContext(adminCtx())
	w := httptest.NewRecorder()

	api.HandleSandboxAuditLog(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	saved, hasSaved := resp["saved"]
	assert.True(t, hasSaved, "response must have 'saved' field")
	assert.Equal(t, true, saved, "'saved' must be true")

	rr, hasRR := resp["requires_restart"]
	assert.True(t, hasRR, "response must have 'requires_restart' field")
	assert.Equal(t, true, rr, "'requires_restart' must be true")

	_, hasAE := resp["applied_enabled"]
	assert.True(t, hasAE, "response must have 'applied_enabled' field")
}

// TestHandleSandboxAuditLog_MethodNotAllowed verifies that POST and DELETE
// receive 405 Method Not Allowed.
func TestHandleSandboxAuditLog_MethodNotAllowed(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			r := httptest.NewRequest(method, "/api/v1/security/audit-log", nil)
			w := httptest.NewRecorder()
			api.HandleSandboxAuditLog(w, r)
			assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
		})
	}
}

// TestHandleSandboxAuditLog_InvalidBody verifies that missing the 'enabled'
// field or sending malformed JSON yields 400 Bad Request.
func TestHandleSandboxAuditLog_InvalidBody(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	t.Run("missing enabled field", func(t *testing.T) {
		// Regression: gen.AuditLogToggleRequest.Enabled is bool (not *bool), so an
		// absent "enabled" key would decode as false and silently disable audit
		// logging. The pre-check in HandleSandboxAuditLog now rejects {} with 400
		// before decoding, preventing the silent-disable regression.
		r := httptest.NewRequest(http.MethodPut, "/api/v1/security/audit-log", strings.NewReader(`{}`))
		r = r.WithContext(adminCtx())
		w := httptest.NewRecorder()
		api.HandleSandboxAuditLog(w, r)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		var resp map[string]string
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Contains(t, resp["error"], "enabled")
	})

	t.Run("malformed JSON", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPut, "/api/v1/security/audit-log", strings.NewReader(`{not json}`))
		r = r.WithContext(adminCtx())
		w := httptest.NewRecorder()
		api.HandleSandboxAuditLog(w, r)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

// TestHandleSandboxAuditLog_EmitsAuditEntry verifies that when audit logging is
// already enabled (precondition: sandbox.audit_log=true), a PUT {enabled:false}
// emits a security_setting_change JSONL record with the correct resource and
// actor fields.
func TestHandleSandboxAuditLog_EmitsAuditEntry(t *testing.T) {
	api, tmpDir := newTestRestAPIWithAuditLog(t)

	body := strings.NewReader(`{"enabled":false}`)
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/audit-log", body)
	r = r.WithContext(adminCtx())
	w := httptest.NewRecorder()

	api.HandleSandboxAuditLog(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	systemDir := filepath.Join(tmpDir, "system")
	entries, err := os.ReadDir(systemDir)
	require.NoError(t, err, "system dir must exist after audit emit")
	require.NotEmpty(t, entries, "at least one audit file must exist")

	var found bool
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		f, err := os.Open(filepath.Join(systemDir, entry.Name()))
		require.NoError(t, err)
		defer f.Close()

		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			var record map[string]any
			if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
				continue
			}
			if record["event"] != "security_setting_change" {
				continue
			}
			assert.Equal(t, "sandbox.audit_log", record["resource"], "resource must match")
			assert.Equal(t, "admin", record["actor"], "actor must be the admin username")
			found = true
		}
		require.NoError(t, scanner.Err())
	}

	assert.True(t, found, "security_setting_change record must appear in audit JSONL")
}
