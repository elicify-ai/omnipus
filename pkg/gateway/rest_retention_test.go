// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// retentionPUT is a helper that issues a PUT /api/v1/security/retention as admin.
func retentionPUT(t *testing.T, api *restAPI, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/retention", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)
	api.HandleRetention(w, r)
	return w
}

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

// ─── PUT tests ───────────────────────────────────────────────────────────────

// TestHandleRetention_PUT_ValidShape verifies that a PUT with both fields returns
// 200, saves=true, requires_restart=false, and persists to config.json.
func TestHandleRetention_PUT_ValidShape(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := retentionPUT(t, api, `{"session_days": 30, "disabled": false}`)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, true, resp["saved"])
	assert.Equal(t, false, resp["requires_restart"])

	raw, err := os.ReadFile(api.homePath + "/config.json")
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	storage, _ := m["storage"].(map[string]any)
	require.NotNil(t, storage, "storage key must exist")
	retention, _ := storage["retention"].(map[string]any)
	require.NotNil(t, retention, "retention key must exist")
	assert.EqualValues(t, 30, retention["session_days"])
	assert.Equal(t, false, retention["disabled"])
}

// TestHandleRetention_PUT_PartialUpdate verifies that a PUT with only disabled=true
// preserves the existing session_days value.
func TestHandleRetention_PUT_PartialUpdate(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Pre-seed session_days=30.
	w := retentionPUT(t, api, `{"session_days": 30, "disabled": false}`)
	require.Equal(t, http.StatusOK, w.Code)

	// Partial update: only change disabled.
	w = retentionPUT(t, api, `{"disabled": true}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	raw, err := os.ReadFile(api.homePath + "/config.json")
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	storage, _ := m["storage"].(map[string]any)
	retention, _ := storage["retention"].(map[string]any)
	require.NotNil(t, retention)
	assert.EqualValues(t, 30, retention["session_days"], "session_days must be preserved")
	assert.Equal(t, true, retention["disabled"], "disabled must be updated to true")
}

// TestHandleRetention_PUT_ZeroSessionDaysAccepted verifies that session_days=0 is
// accepted (0 means "use default 90 days" per RetentionSessionDays()).
func TestHandleRetention_PUT_ZeroSessionDaysAccepted(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := retentionPUT(t, api, `{"session_days": 0}`)
	assert.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
}

// TestHandleRetention_PUT_NegativeRejected verifies that session_days=-1 is rejected
// with 400 Bad Request.
func TestHandleRetention_PUT_NegativeRejected(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := retentionPUT(t, api, `{"session_days": -1}`)
	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
}

// TestHandleRetention_PUT_FloatRejected verifies that session_days=10.5 is rejected
// with 400 Bad Request — fractional values are not allowed.
func TestHandleRetention_PUT_FloatRejected(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := retentionPUT(t, api, `{"session_days": 10.5}`)
	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
}

// TestHandleRetention_PUT_StringRejected verifies that session_days="30" (a JSON
// string) is rejected with 400 Bad Request.
func TestHandleRetention_PUT_StringRejected(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := retentionPUT(t, api, `{"session_days": "30"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
}

// TestHandleRetention_PUT_DisabledNonBoolRejected verifies that disabled="true"
// (a JSON string) is rejected with 400 Bad Request.
func TestHandleRetention_PUT_DisabledNonBoolRejected(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := retentionPUT(t, api, `{"disabled": "true"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
}

// TestHandleRetention_PUT_HotReload verifies that the response always contains
// requires_restart=false, confirming retention is a hot-reload setting.
func TestHandleRetention_PUT_HotReload(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := retentionPUT(t, api, `{"session_days": 14}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, false, resp["requires_restart"])
}

// TestHandleRetention_PUT_MethodNotAllowed verifies that DELETE returns 405.
func TestHandleRetention_PUT_MethodNotAllowed(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	r := httptest.NewRequest(http.MethodDelete, "/api/v1/security/retention", nil)
	w := httptest.NewRecorder()
	api.HandleRetention(w, r)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// TestHandleRetention_PUT_EmitsAuditEntry verifies that a successful PUT emits a
// security_setting_change audit record with resource="storage.retention".
func TestHandleRetention_PUT_EmitsAuditEntry(t *testing.T) {
	api, tmpDir := newTestRestAPIWithAuditLog(t)

	body := strings.NewReader(`{"session_days": 30, "disabled": false}`)
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/retention", body)
	r.Header.Set("Content-Type", "application/json")
	r = r.WithContext(adminCtx())
	w := httptest.NewRecorder()
	api.HandleRetention(w, r)
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
			if record["resource"] == "storage.retention" {
				assert.Equal(t, "admin", record["actor"], "actor must be admin username")
				found = true
			}
		}
		require.NoError(t, scanner.Err())
	}

	assert.True(t, found, "security_setting_change record with resource=storage.retention must appear")
}

// TestHandleRetention_PUT_EmptyBodyNoOp verifies that {} returns 200 and leaves
// the persisted config unchanged.
func TestHandleRetention_PUT_EmptyBodyNoOp(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Pre-seed a known value.
	w := retentionPUT(t, api, `{"session_days": 45}`)
	require.Equal(t, http.StatusOK, w.Code)

	// Empty body must not change anything.
	w = retentionPUT(t, api, `{}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, true, resp["saved"])

	// Persisted value must still be 45.
	raw, err := os.ReadFile(api.homePath + "/config.json")
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	storage, _ := m["storage"].(map[string]any)
	retention, _ := storage["retention"].(map[string]any)
	require.NotNil(t, retention)
	assert.EqualValues(t, 45, retention["session_days"])
}

// ─── Sweep tests ─────────────────────────────────────────────────────────────

// TestHandleRetentionSweep_OnDemand creates 3 session files aged 3/10/30 days,
// sets retention=7, POSTs to /retention/sweep, and verifies {removed:2} with
// the two aged files deleted.
func TestHandleRetentionSweep_OnDemand(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Set session_days=7 in config so the sweep uses 7-day retention.
	w := retentionPUT(t, api, `{"session_days": 7, "disabled": false}`)
	require.Equal(t, http.StatusOK, w.Code)

	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store, "session store must be initialized")
	baseDir := store.BaseDir()

	recent := createRetentionSessionFile(t, baseDir, "sess-recent", "2026-04-20.jsonl", 3*24*time.Hour)
	stale1 := createRetentionSessionFile(t, baseDir, "sess-stale1", "2026-04-12.jsonl", 10*24*time.Hour)
	stale2 := createRetentionSessionFile(t, baseDir, "sess-stale2", "2026-03-23.jsonl", 30*24*time.Hour)

	w = retentionSweepPOST(t, api)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.EqualValues(t, 2, resp["removed"], "two stale files must be removed")

	_, err := os.Stat(recent)
	assert.NoError(t, err, "recent file (3 days) must survive")

	_, err = os.Stat(stale1)
	assert.True(t, os.IsNotExist(err), "10-day-old file must be deleted")

	_, err = os.Stat(stale2)
	assert.True(t, os.IsNotExist(err), "30-day-old file must be deleted")
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

// TestHandleRetentionSweep_DisabledReturnsSkipped sets Disabled=true, then posts
// to /retention/sweep and asserts {removed:0, skipped_reason:"disabled"} with
// no files touched.
func TestHandleRetentionSweep_DisabledReturnsSkipped(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Enable disabled flag.
	w := retentionPUT(t, api, `{"disabled": true, "session_days": 7}`)
	require.Equal(t, http.StatusOK, w.Code)

	// Create a session file that would otherwise be deleted.
	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store, "session store must be initialized")
	baseDir := store.BaseDir()
	oldFile := createRetentionSessionFile(t, baseDir, "sess-disabled-test", "2026-01-01.jsonl", 30*24*time.Hour)

	w = retentionSweepPOST(t, api)
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
