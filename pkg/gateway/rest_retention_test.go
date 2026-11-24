// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// rest_retention_test.go — Settings redesign #1519, test-side reconciliation.
//
// GET/PUT /api/v1/security/retention and HandleRetention are REMOVED (C-3,
// FR-047). Per the spec's regression table ("`/security/retention` GET/PUT —
// rest_retention_test.go, api_e2e_test.go, routes_admin_test.go — those cases
// now expect 404; sweep cases stay") and the move instruction, this file's
// write checks moved to the REPLACING endpoint GET/PUT /api/v1/settings/
// retention; the sweep cases stay on POST /security/retention/sweep, which
// FR-047 keeps working unchanged.
//
// Replaced assertions, each with the FR that replaces it:
//   - the old {saved, requires_restart} response shape is retired with the
//     endpoint (FR-047); a 200 now answers the RetentionSettings read shape
//     with all four required fields (FR-041, FR-043). The old
//     requires_restart=false "retention is hot" fact is carried by that
//     shape: the answer has no requires_restart member.
//   - session_days=0 accepted ("use the default 90") is replaced by the
//     1–365 range (FR-042) — pinned at the replacing endpoint by
//     TestRetentionSettings_Ranges D1 row 2 (both validate_inbound flags).
//   - session_days=-1 / 10.5 / "30" refusals — same ranges rows (FR-042):
//     D1 rows 7, 8 and 9 of TestRetentionSettings_Ranges.
//   - writing `disabled` is replaced by read-only refusal (FR-042 refuses
//     the read-only member, FR-044 refuses changing it through PUT /config,
//     FR-048 makes it config-file only) — TestRetentionSettings_Ranges
//     D2 row 9 (keep_everything_forever).
//   - `{}` as a no-op is replaced by 400 "at least one field required"
//     (FR-042) — TestRetentionSettings_Ranges D2 row 10.
//
// The moved checks drive the REAL production route registration
// (registerAdditionalEndpoints — the real-mux pattern of
// rest_settings_retention_endpoint_test.go), so they compile against both
// the pre-merge code (404 → loud RED naming the FR) and the implemented
// endpoint. The sweep tests keep calling HandleRetentionSweep, which exists
// on both sides, and seed the retention window in the RUNNING config: the
// sweep reads the config the loop holds
// (pkg/gateway/retention_goroutine.go::executeRetentionSweepTick), and the
// old seeding went through the removed PUT.

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// retentionSweepPOST is a helper that issues a POST /api/v1/security/retention/sweep as admin.
func retentionSweepPOST(t *testing.T, api *restAPI) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/security/retention/sweep", nil)
	r = withAdminRole(r)
	api.HandleRetentionSweep(w, r)
	return w
}

// createRetentionSessionFile writes a .jsonl file in a session subdirectory under
// the UnifiedStore's base directory, then sets its mtime to simulate age.
func createRetentionSessionFile(t *testing.T, baseDir, sessionID, filename string, age time.Duration) string {
	t.Helper()
	sessionDir := filepath.Join(baseDir, sessionID)
	require.NoError(t, os.MkdirAll(sessionDir, 0o700))
	filePath := filepath.Join(sessionDir, filename)
	require.NoError(t, os.WriteFile(filePath, []byte(`{"id":"test"}`+"\n"), 0o600))
	mtime := time.Now().Add(-age)
	require.NoError(t, os.Chtimes(filePath, mtime, mtime))
	return filePath
}

// newRetentionAuditAPI is the real-mux retention harness with the audit
// logger armed, for the audit-parity check moved from PUT /security/retention.
// It mirrors newTestRestAPIWithAuditLog (agents home at tmpDir/workspace, so
// the audit writer's directory Dir(agents home)/system is tmpDir/system — the
// folder #1742 made the viewer read) with newRetentionSectionAPI's real route
// registration and bearer token.
func newRetentionAuditAPI(t *testing.T) *retentionSectionAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", retentionTestToken)
	tmpDir := t.TempDir()
	workspaceDir := filepath.Join(tmpDir, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o700))
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080, ValidateInbound: true},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         workspaceDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
		Sandbox: config.OmnipusSandboxConfig{AuditLog: true},
	}
	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"sandbox":{"audit_log":true}}`)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config.json"), minimalCfg, 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		homePath:      tmpDir,
	}
	mux := http.NewServeMux()
	api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})
	return &retentionSectionAPI{api: api, mux: mux, cfg: cfg}
}

// newRetentionSweepAPI is newTestRestAPIWithHome with the retention window a
// sweep test needs seeded in the RUNNING config (the sweep reads the config
// the loop holds — retention_goroutine.go::executeRetentionSweepTick) and in
// config.json. The old seeding went through PUT /security/retention, which
// FR-047 removes; the legacy disabled switch is config-file only (FR-048),
// which is why it is seeded here rather than written through any endpoint.
func newRetentionSweepAPI(t *testing.T, sessionDays int, disabled bool) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
	}
	cfg.Storage.Retention = config.OmnipusRetentionConfig{SessionDays: sessionDays, Disabled: disabled}
	seed := `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"storage":{"retention":{"session_days":` + strconv.Itoa(sessionDays)
	if disabled {
		seed += `,"disabled":true`
	}
	seed += `}}}`
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(seed), 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	return &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		homePath:      tmpDir,
	}
}

// ─── Moved PUT /settings/retention checks ────────────────────────────────────

// TestSettingsRetention_PutAppliesSentFieldsAndAnswersReadShape — moved from
// TestHandleRetention_PUT_ValidShape / _PartialUpdate / _HotReload (the
// removed PUT /security/retention) to the replacing endpoint (FR-041/042/043).
func TestSettingsRetention_PutAppliesSentFieldsAndAnswersReadShape(t *testing.T) {
	h := newRetentionSectionAPI(t, true)

	// A valid write answers the read shape: FR-043 answers 200 with
	// RetentionSettings — exactly the four required fields (FR-041), not the
	// old {saved, requires_restart} shape that retired with the endpoint
	// (FR-047). No requires_restart member is how this file carries the old
	// "retention is hot" fact.
	w := h.do(http.MethodPut, "/api/v1/settings/retention", `{"session_days":30,"memory_retros_days":180}`)
	require.Equal(t, http.StatusOK, w.Code,
		"BLOCKED RED: PUT /api/v1/settings/retention is not implemented — required by FR-042/FR-043; body=%s", w.Body.String())
	var got map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Len(t, got, 4,
		"the 200 must answer exactly the four RetentionSettings fields (FR-041); the old saved/requires_restart shape is retired with /security/retention (FR-047)")
	assert.Equal(t, float64(30), got["session_days"], "the sent session_days must be answered back")
	assert.Equal(t, float64(180), got["memory_retros_days"], "the sent memory_retros_days must be answered back")
	assert.Equal(t, false, got["memory_retros_forever"], "Keep forever stays off")
	assert.Equal(t, false, got["keep_everything_forever"], "the legacy switch stays off")

	// The sent fields are persisted to config.json (the old ValidShape's
	// persistence check, now on the replacing endpoint).
	raw, err := os.ReadFile(filepath.Join(h.api.homePath, "config.json"))
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	storage, _ := m["storage"].(map[string]any)
	require.NotNil(t, storage, "storage key must exist after the write")
	retention, _ := storage["retention"].(map[string]any)
	require.NotNil(t, retention, "retention key must exist after the write")
	assert.EqualValues(t, 30, retention["session_days"], "session_days must be persisted to config.json")
	assert.EqualValues(t, 180, retention["memory_retros_days"], "memory_retros_days must be persisted to config.json")

	// FR-043: the write applies ONLY the sent fields — a partial update
	// preserves the stored sibling values (moved from _PartialUpdate, whose
	// disabled=true body is replaced by memory_retros_forever: the legacy
	// switch is no longer writable at all, FR-044/FR-048).
	w2 := h.do(http.MethodPut, "/api/v1/settings/retention", `{"memory_retros_forever":true}`)
	require.Equal(t, http.StatusOK, w2.Code,
		"BLOCKED RED: PUT /api/v1/settings/retention is not implemented — required by FR-042/FR-043; body=%s", w2.Body.String())
	var got2 map[string]any
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &got2))
	require.Len(t, got2, 4, "the partial write must answer RetentionSettings too (FR-043)")
	assert.Equal(t, float64(30), got2["session_days"], "session_days must be preserved by a partial update (FR-043)")
	assert.Equal(t, float64(180), got2["memory_retros_days"], "memory_retros_days must be preserved by a partial update (FR-043)")
	assert.Equal(t, true, got2["memory_retros_forever"], "memory_retros_forever must be applied by the partial update")
}

// TestSettingsRetention_DeleteMethodNotAllowed — moved from
// TestHandleRetention_PUT_MethodNotAllowed: the replacing route dispatches
// GET and PUT (FR-041/FR-043 name exactly those); DELETE is neither. Carried
// characterization of the route contract; the oracle is unchanged.
func TestSettingsRetention_DeleteMethodNotAllowed(t *testing.T) {
	h := newRetentionSectionAPI(t, true)

	w := h.do(http.MethodDelete, "/api/v1/settings/retention", "")
	require.Equal(t, http.StatusMethodNotAllowed, w.Code,
		"BLOCKED RED: GET/PUT /api/v1/settings/retention is not implemented — required by FR-041/FR-043 (got %d); body=%s", w.Code, w.Body.String())
}

// TestSettingsRetention_WriteEmitsAuditEntry — moved from
// TestHandleRetention_PUT_EmitsAuditEntry: the replacing write path writes
// the same security_setting_change audit record the removed endpoint wrote,
// with resource="storage.retention" (C-3 replaces the endpoint; the audit
// parity is part of the moved check).
//
// Oracle carried from the old test: one record per write, attributed to the
// authenticated caller. The old test injected the caller with adminCtx() on
// a direct handler call; here the caller comes through the real route as the
// harness's env-token identity (pkg/gateway/auth.go::envTokenUser,
// Username "_env_token"), so that is the asserted username.
func TestSettingsRetention_WriteEmitsAuditEntry(t *testing.T) {
	h := newRetentionAuditAPI(t)

	w := h.do(http.MethodPut, "/api/v1/settings/retention", `{"session_days":30}`)
	require.Equal(t, http.StatusOK, w.Code,
		"BLOCKED RED: PUT /api/v1/settings/retention is not implemented — required by FR-042/FR-043; body=%s", w.Body.String())

	records := retentionGatewayAudit(t, h.api.homePath, "storage.retention")
	require.Len(t, records, 1,
		"exactly one security_setting_change record with resource=storage.retention must be written for the retention write; got %d", len(records))
	assert.Equal(t, audit.EventSecuritySettingChange, records[0].Event)
	assert.Equal(t, "_env_token", records[0].Actor,
		"the record must name the authenticated caller (the harness's env-token identity)")
}

// ─── Sweep tests (kept; FR-047 keeps POST /security/retention/sweep unchanged) ──

// TestHandleRetentionSweep_OnDemand creates 3 session files aged 3/10/30 days,
// seeds a 7-day retention window, POSTs to /retention/sweep, and verifies
// {removed:2} with the two aged files deleted.
func TestHandleRetentionSweep_OnDemand(t *testing.T) {
	// Seeded in the running config: session_days=7, disabled off.
	api := newRetentionSweepAPI(t, 7, false)

	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store, "session store must be initialized")
	baseDir := store.BaseDir()

	recent := createRetentionSessionFile(t, baseDir, "sess-recent", "2026-04-20.jsonl", 3*24*time.Hour)
	stale1 := createRetentionSessionFile(t, baseDir, "sess-stale1", "2026-04-12.jsonl", 10*24*time.Hour)
	stale2 := createRetentionSessionFile(t, baseDir, "sess-stale2", "2026-03-23.jsonl", 30*24*time.Hour)

	w := retentionSweepPOST(t, api)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.EqualValues(t, 2, resp["removed"], "two stale files must be removed")

	_, err := os.Stat(recent)
	assert.NoError(t, err, "recent file (3 days) must survive")

	_, err = os.Stat(stale1)
	assert.True(t, errors.Is(err, os.ErrNotExist), "10-day-old file must be deleted")

	_, err = os.Stat(stale2)
	assert.True(t, errors.Is(err, os.ErrNotExist), "30-day-old file must be deleted")
}

// TestHandleRetentionSweep_MutexConflictReturns409 acquires retentionSweepMu in
// the test goroutine, then posts to /retention/sweep and asserts 409 with
// error="sweep in progress". Releases the mutex after the check.
func TestHandleRetentionSweep_MutexConflictReturns409(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	retentionSweepMu.Lock()
	defer retentionSweepMu.Unlock()

	w := retentionSweepPOST(t, api)
	require.Equal(t, http.StatusConflict, w.Code, "body: %s", w.Body.String())

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "sweep in progress", resp["error"])
}

// TestHandleRetentionSweep_DisabledReturnsSkipped seeds the legacy
// storage.retention.disabled switch (config-file only since FR-044/FR-048),
// then posts to /retention/sweep and asserts {removed:0,
// skipped_reason:"disabled"} with no files touched.
func TestHandleRetentionSweep_DisabledReturnsSkipped(t *testing.T) {
	api := newRetentionSweepAPI(t, 7, true)

	// Create a session file that would otherwise be deleted.
	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store, "session store must be initialized")
	baseDir := store.BaseDir()
	oldFile := createRetentionSessionFile(t, baseDir, "sess-disabled-test", "2026-01-01.jsonl", 30*24*time.Hour)

	w := retentionSweepPOST(t, api)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.EqualValues(t, 0, resp["removed"])
	assert.Equal(t, "disabled", resp["skipped_reason"])

	// File must still exist.
	_, err := os.Stat(oldFile)
	assert.NoError(t, err, "file must not be deleted when retention is disabled")
}

// TestHandleRetentionSweep_MethodNotAllowed verifies that GET returns 405.
func TestHandleRetentionSweep_MethodNotAllowed(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/security/retention/sweep", nil)
	r = withAdminRole(r)
	w := httptest.NewRecorder()
	api.HandleRetentionSweep(w, r)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// TestHandleRetentionSweep_OnDemand_ExpiresAnAgedArchivePartition is row 8:
// the on-demand endpoint must drive the WIRED archive-expiry path, not only the
// generic file walk.
//
// A rolled day partition (a "<YYYY-MM-DD>.jsonl" file inside a session
// directory) is skipped by the generic walk by design — "a rolled day
// partition: owned by expireArchiveSessions" — so it can only disappear when
// the archive expiry pass ran. Its removal through this endpoint is therefore
// proof that RetentionSweep reached pkg/session/archive_expiry.go.
func TestHandleRetentionSweep_OnDemand_ExpiresAnAgedArchivePartition(t *testing.T) {
	api := newRetentionSweepAPI(t, 7, false)

	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store, "session store must be initialized")

	rolled := createRetentionSessionFile(t, store.BaseDir(), "sess-arch", "2026-03-23.jsonl", 30*24*time.Hour)

	w := retentionSweepPOST(t, api)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.EqualValues(t, 1, resp["removed"],
		"the endpoint must report the archive partition the expiry pass removed")

	if _, err := os.Stat(rolled); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the aged rolled archive partition must be expired by the wired path; stat err=%v", err)
	}
}
