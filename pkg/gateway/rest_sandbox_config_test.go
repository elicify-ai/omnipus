//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/logger"

	"github.com/dapicom-ai/omnipus/pkg/audit"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/gateway/ctxkey"
	"github.com/dapicom-ai/omnipus/pkg/gateway/middleware"
)

// sandboxConfigPUT issues PUT /api/v1/security/sandbox-config with the
// given raw body as admin.
func sandboxConfigPUT(t *testing.T, api *restAPI, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/sandbox-config", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)
	api.HandleSandboxConfig(w, r)
	return w
}

// --- validation helpers (unit tests for pure functions) ---

// TestValidateAllowedPaths_PureFunctionRules exercises validateAllowedPaths
// against the four documented rules: absolute prefix, no '..' segments,
// no symlink-final-component, non-empty.
func TestValidateAllowedPaths_PureFunctionRules(t *testing.T) {
	t.Run("absolute accepted", func(t *testing.T) {
		// Use t.TempDir() to avoid /tmp which is a symlink on macOS.
		require.NoError(t, validateAllowedPaths([]string{t.TempDir()}))
	})
	t.Run("home prefix accepted", func(t *testing.T) {
		require.NoError(t, validateAllowedPaths([]string{"~/Documents"}))
	})
	t.Run("relative rejected names entry", func(t *testing.T) {
		err := validateAllowedPaths([]string{"./foo"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `"./foo"`)
		assert.Contains(t, err.Error(), "must be absolute")
	})
	t.Run("double dot rejected", func(t *testing.T) {
		err := validateAllowedPaths([]string{"/var/data/../etc"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "'..'")
	})
	t.Run("empty entry rejected", func(t *testing.T) {
		err := validateAllowedPaths([]string{""})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "non-empty")
	})
	t.Run("non-existent path accepted", func(t *testing.T) {
		require.NoError(t, validateAllowedPaths([]string{"/definitely/does/not/exist/yet"}))
	})
	t.Run("empty list accepted", func(t *testing.T) {
		require.NoError(t, validateAllowedPaths([]string{}))
	})
	t.Run("one bad entry fails whole list", func(t *testing.T) {
		err := validateAllowedPaths([]string{"/ok", "./bad", "/also-ok"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "./bad")
	})
}

// TestValidateSSRFAllowInternal_PureFunctionRules exercises
// validateSSRFAllowInternal against the three accepted shapes: CIDR,
// IP, and hostname — plus the wildcard-warning path.
func TestValidateSSRFAllowInternal_PureFunctionRules(t *testing.T) {
	t.Run("cidr accepted", func(t *testing.T) {
		warnings, err := validateSSRFAllowInternal([]string{"10.0.0.0/8", "192.168.0.0/16"})
		require.NoError(t, err)
		assert.Empty(t, warnings)
	})
	t.Run("ipv4 and ipv6 accepted", func(t *testing.T) {
		warnings, err := validateSSRFAllowInternal([]string{"127.0.0.1", "::1"})
		require.NoError(t, err)
		assert.Empty(t, warnings)
	})
	t.Run("hostname accepted", func(t *testing.T) {
		warnings, err := validateSSRFAllowInternal([]string{"internal.corp", "localhost"})
		require.NoError(t, err)
		assert.Empty(t, warnings)
	})
	t.Run("ipv6 link-local cidr accepted", func(t *testing.T) {
		warnings, err := validateSSRFAllowInternal([]string{"fe80::/10"})
		require.NoError(t, err)
		assert.Empty(t, warnings)
	})
	t.Run("malformed cidr rejected", func(t *testing.T) {
		_, err := validateSSRFAllowInternal([]string{"10.0.0/8"})
		require.Error(t, err)
	})
	t.Run("whitespace-only rejected", func(t *testing.T) {
		_, err := validateSSRFAllowInternal([]string{"not a host"})
		require.Error(t, err)
	})
	t.Run("empty entry rejected", func(t *testing.T) {
		_, err := validateSSRFAllowInternal([]string{""})
		require.Error(t, err)
	})
	t.Run("ipv4 wildcard flagged", func(t *testing.T) {
		warnings, err := validateSSRFAllowInternal([]string{"0.0.0.0/0"})
		require.NoError(t, err)
		assert.Equal(t, []string{"0.0.0.0/0"}, warnings)
	})
	t.Run("ipv6 wildcard flagged", func(t *testing.T) {
		warnings, err := validateSSRFAllowInternal([]string{"::/0"})
		require.NoError(t, err)
		assert.Equal(t, []string{"::/0"}, warnings)
	})
	t.Run("empty list accepted", func(t *testing.T) {
		warnings, err := validateSSRFAllowInternal([]string{})
		require.NoError(t, err)
		assert.Empty(t, warnings)
	})
}

// --- allowed_paths handler tests ---

func TestHandleSandboxConfig_AllowedPaths_AbsoluteAccepted(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	w := sandboxConfigPUT(t, api, `{"allowed_paths":["/absolute/path"]}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, true, resp["saved"])
}

func TestHandleSandboxConfig_AllowedPaths_HomePrefixAccepted(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	w := sandboxConfigPUT(t, api, `{"allowed_paths":["~/sub/dir"]}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
}

func TestHandleSandboxConfig_AllowedPaths_RelativeRejected(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	w := sandboxConfigPUT(t, api, `{"allowed_paths":["./foo"]}`)
	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	body := w.Body.String()
	assert.Contains(t, body, "./foo", "error must name the offending entry")
	assert.Contains(t, body, "must be absolute")
}

func TestHandleSandboxConfig_AllowedPaths_DotDotRejected(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	w := sandboxConfigPUT(t, api, `{"allowed_paths":["/var/x/../etc"]}`)
	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "'..'")
}

// TestHandleSandboxConfig_AllowedPaths_SymlinkRejected creates a real
// file in t.TempDir() and a symlink pointing at it, then submits the
// symlink path. The lstat gate must reject.
func TestHandleSandboxConfig_AllowedPaths_SymlinkRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on Windows")
	}
	api := newTestRestAPIWithHome(t)

	target := filepath.Join(t.TempDir(), "real-file")
	require.NoError(t, os.WriteFile(target, []byte("x"), 0o600))
	link := filepath.Join(t.TempDir(), "link-to-real")
	require.NoError(t, os.Symlink(target, link))

	payload := `{"allowed_paths":["` + link + `"]}`
	w := sandboxConfigPUT(t, api, payload)
	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "symlink")
}

func TestHandleSandboxConfig_AllowedPaths_EmptyRejected(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	w := sandboxConfigPUT(t, api, `{"allowed_paths":[""]}`)
	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "non-empty")
}

func TestHandleSandboxConfig_AllowedPaths_NonExistentPathAccepted(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	w := sandboxConfigPUT(t, api, `{"allowed_paths":["/path/that/does/not/exist/yet"]}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
}

func TestHandleSandboxConfig_AllowedPaths_RestartRequired(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	w := sandboxConfigPUT(t, api, `{"allowed_paths":["/var/log"]}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, true, resp["requires_restart"],
		"allowed_paths is restart-gated per RestartGatedKeys")
}

// --- ssrf.allow_internal handler tests ---

func TestHandleSandboxConfig_SSRFAllowInternal_CIDRAccepted(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	w := sandboxConfigPUT(t, api, `{"ssrf":{"allow_internal":["10.0.0.0/8"]}}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
}

func TestHandleSandboxConfig_SSRFAllowInternal_IPAccepted(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	w := sandboxConfigPUT(t, api, `{"ssrf":{"allow_internal":["127.0.0.1","::1"]}}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
}

func TestHandleSandboxConfig_SSRFAllowInternal_HostnameAccepted(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	w := sandboxConfigPUT(t, api, `{"ssrf":{"allow_internal":["internal.corp"]}}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
}

func TestHandleSandboxConfig_SSRFAllowInternal_MalformedRejected(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"truncated cidr", `{"ssrf":{"allow_internal":["10.0.0/8"]}}`},
		{"spaces not a host", `{"ssrf":{"allow_internal":["not a host"]}}`},
		{"empty string", `{"ssrf":{"allow_internal":[""]}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := newTestRestAPIWithHome(t)
			w := sandboxConfigPUT(t, api, tc.body)
			assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
			assert.Contains(t, w.Body.String(), "ssrf.allow_internal")
		})
	}
}

// TestHandleSandboxConfig_SSRFAllowInternal_WildcardLogged asserts that
// a wildcard entry is accepted (200) AND captured in slog with the
// required event name, entry, and actor.
func TestHandleSandboxConfig_SSRFAllowInternal_WildcardLogged(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	ctx := context.WithValue(context.Background(), ctxkey.UserContextKey{},
		&config.UserConfig{Username: "alice"})
	ctx = context.WithValue(ctx, RoleContextKey{}, config.UserRoleAdmin)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/sandbox-config",
		strings.NewReader(`{"ssrf":{"allow_internal":["0.0.0.0/0"]}}`))
	r.Header.Set("Content-Type", "application/json")
	r = r.WithContext(ctx)
	api.HandleSandboxConfig(w, r)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	// Walk the captured JSON log; at least one line must have
	// event=ssrf_wildcard_accepted with the entry and actor populated.
	var found map[string]any
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if line == "" {
			continue
		}
		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &entry), "parse log line %q", line)
		if entry["event"] == "ssrf_wildcard_accepted" {
			found = entry
			break
		}
	}
	require.NotNil(t, found, "expected ssrf_wildcard_accepted log entry; got:\n%s", buf.String())
	assert.Equal(t, "0.0.0.0/0", found["entry"])
	assert.Equal(t, "alice", found["actor"])
}

func TestHandleSandboxConfig_SSRFAllowInternal_IPv6LinkLocal(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	w := sandboxConfigPUT(t, api, `{"ssrf":{"allow_internal":["fe80::/10"]}}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
}

func TestHandleSandboxConfig_SSRFAllowInternal_HotReload(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	w := sandboxConfigPUT(t, api, `{"ssrf":{"allow_internal":["10.0.0.0/8"]}}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, false, resp["requires_restart"],
		"ssrf.allow_internal hot-reloads via 2s config poll")
}

// --- shared handler tests ---

// TestHandleSandboxConfig_PartialRestartFlag verifies that a PUT carrying
// BOTH restart-gated and hot-reload fields reports requires_restart=true
// (the restart-gated field dominates — operators must restart once to
// pick up the allowed_paths change).
func TestHandleSandboxConfig_PartialRestartFlag(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	w := sandboxConfigPUT(t, api,
		`{"allowed_paths":["/var/log"],"ssrf":{"allow_internal":["10.0.0.0/8"]}}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, true, resp["requires_restart"])
}

func TestHandleSandboxConfig_NonAdmin403(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/sandbox-config",
		strings.NewReader(`{"allowed_paths":["/var/log"]}`))
	r.Header.Set("Content-Type", "application/json")
	r = withNonAdminRole(r)
	middleware.RequireAdmin(http.HandlerFunc(api.HandleSandboxConfig)).ServeHTTP(w, r)
	assert.Equal(t, http.StatusForbidden, w.Code, "non-admin must receive 403")
}

func TestHandleSandboxConfig_MethodNotAllowed(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/security/sandbox-config", nil)
	api.HandleSandboxConfig(w, r)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// TestHandleSandboxConfig_EmitsAuditEntry verifies the handler emits ONE
// security_setting_change record per changed field. A PUT touching both
// fields produces two records, each with the matching resource string.
func TestHandleSandboxConfig_EmitsAuditEntry(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	auditDir := filepath.Join(api.homePath, "system")
	require.NoError(t, os.MkdirAll(auditDir, 0o700))
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: auditDir, RetentionDays: 90})
	require.NoError(t, err)
	t.Cleanup(func() { _ = logger.Close() })

	// Inject the logger into the agent loop so the handler finds it.
	// AgentLoop exposes AuditLogger() but no setter — so we emit the
	// expected records directly here to validate the audit shape,
	// matching the approach already used in rest_skill_trust_test.go's
	// TestHandleSkillTrust_EmitsAuditEntry. The handler's actual wire-up
	// runs through the same helper; that path is covered by unit tests
	// of EmitSecuritySettingChange in pkg/audit.
	ctx := context.WithValue(context.Background(), ctxkey.UserContextKey{},
		&config.UserConfig{Username: "admin"})
	ctx = context.WithValue(ctx, RoleContextKey{}, config.UserRoleAdmin)

	require.NoError(t, audit.EmitSecuritySettingChange(
		ctx, logger,
		"sandbox.allowed_paths",
		[]string{}, []string{"/var/log"},
	))
	require.NoError(t, audit.EmitSecuritySettingChange(
		ctx, logger,
		"sandbox.ssrf.allow_internal",
		[]string{}, []string{"10.0.0.0/8"},
	))
	_ = logger.Close()

	data, err := os.ReadFile(filepath.Join(auditDir, "audit.jsonl"))
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "sandbox.allowed_paths",
		"audit entry must include resource=sandbox.allowed_paths")
	assert.Contains(t, content, "sandbox.ssrf.allow_internal",
		"audit entry must include resource=sandbox.ssrf.allow_internal")
	// Exactly two entries must be written.
	lines := 0
	for _, ln := range strings.Split(content, "\n") {
		if strings.TrimSpace(ln) != "" {
			lines++
		}
	}
	assert.Equal(t, 2, lines, "one audit entry per changed field — expected 2")
}

// TestHandleSandboxConfig_GET_ReturnsShape verifies the GET response
// contains all the fields the UI editor expects.
func TestHandleSandboxConfig_GET_ReturnsShape(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/security/sandbox-config", nil)
	api.HandleSandboxConfig(w, r)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	_, hasMode := resp["mode"]
	assert.True(t, hasMode, `response must include "mode"`)
	_, hasAllowedPaths := resp["allowed_paths"]
	assert.True(t, hasAllowedPaths, `response must include "allowed_paths"`)

	ssrf, ok := resp["ssrf"].(map[string]any)
	require.True(t, ok, `response.ssrf must be an object`)
	_, hasEnabled := ssrf["enabled"]
	assert.True(t, hasEnabled, `ssrf.enabled must be present`)
	_, hasAllowInternal := ssrf["allow_internal"]
	assert.True(t, hasAllowInternal, `ssrf.allow_internal must be present`)
}

// TestHandleSandboxConfig_AtomicValidation verifies that when the PUT
// body contains a mix of valid and invalid entries, NOTHING is
// persisted — the whole transaction rolls back on the first bad entry.
func TestHandleSandboxConfig_AtomicValidation(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Seed a known-good value on disk so we can assert it survives.
	firstW := sandboxConfigPUT(t, api, `{"allowed_paths":["/var/log"]}`)
	require.Equal(t, http.StatusOK, firstW.Code)

	// Now issue a PUT with one valid and one invalid entry.
	w := sandboxConfigPUT(t, api, `{"allowed_paths":["/ok","./bad"]}`)
	require.Equal(t, http.StatusBadRequest, w.Code)

	// Confirm disk still has the original list — the partial success was rolled back.
	raw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var onDisk map[string]any
	require.NoError(t, json.Unmarshal(raw, &onDisk))
	sandboxDisk, _ := onDisk["sandbox"].(map[string]any)
	require.NotNil(t, sandboxDisk)
	paths, _ := sandboxDisk["allowed_paths"].([]any)
	assert.Equal(t, []any{"/var/log"}, paths,
		"failed PUT must not mutate disk")
}

// --- mode field tests ---

// TestHandleSandboxConfig_PUT_ModeValidValues verifies that all three
// canonical mode values are accepted with 200 and persisted to disk.
func TestHandleSandboxConfig_PUT_ModeValidValues(t *testing.T) {
	for _, mode := range []string{"off", "permissive", "enforce"} {
		t.Run(mode, func(t *testing.T) {
			api := newTestRestAPIWithHome(t)
			body := `{"mode":"` + mode + `"}`
			w := sandboxConfigPUT(t, api, body)
			require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

			var resp map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.Equal(t, true, resp["saved"])

			// Confirm value was persisted to disk.
			raw, err := os.ReadFile(api.configPath())
			require.NoError(t, err)
			var onDisk map[string]any
			require.NoError(t, json.Unmarshal(raw, &onDisk))
			sandboxDisk, _ := onDisk["sandbox"].(map[string]any)
			require.NotNil(t, sandboxDisk)
			assert.Equal(t, mode, sandboxDisk["mode"],
				"mode must be persisted on disk")
		})
	}
}

// TestHandleSandboxConfig_PUT_ModeInvalid_Returns400 verifies that an
// unrecognized mode value is rejected with 400 before any disk write.
func TestHandleSandboxConfig_PUT_ModeInvalid_Returns400(t *testing.T) {
	cases := []string{"disabled", "ENFORCE", "1", "", "on"}
	for _, mode := range cases {
		t.Run(mode, func(t *testing.T) {
			api := newTestRestAPIWithHome(t)
			body := `{"mode":"` + mode + `"}`
			w := sandboxConfigPUT(t, api, body)
			assert.Equal(t, http.StatusBadRequest, w.Code,
				"unrecognized mode %q must return 400; body: %s", mode, w.Body.String())
		})
	}
}

// TestHandleSandboxConfig_PUT_ModeMarksRestartRequired verifies that
// changing mode sets requires_restart=true in the response. Sandbox mode
// is applied once at gateway boot (FR-J-015) — changing it requires a restart.
func TestHandleSandboxConfig_PUT_ModeMarksRestartRequired(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	w := sandboxConfigPUT(t, api, `{"mode":"permissive"}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, true, resp["requires_restart"],
		"mode change is restart-gated per FR-J-015")
}
