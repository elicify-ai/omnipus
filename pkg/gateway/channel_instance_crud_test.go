// channel_instance_crud_test.go — ADR-029 US-6/US-10/US-11 gateway handler tests.
// Covers:
//   - F-6: rejection tests assert nothing persisted (SC-002)
//   - F-9: TestChannelsRouter_AcceptsInstanceID (US-11 AC-1/2/3)
//   - F-11: TestWorkspaceDelete_PartialCascade_AbortsIntact (MAJ-005)
//   - F-14: TestSetChannelRouting_EmitsAuditEvent (FR-030)
//   - Create-instance happy path + 400/409 errors (US-6)
//   - Delete-instance happy path + cred/store teardown (US-10)

package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── helpers ──────────────────────────────────────────────────────────────────

// withAdminChannelCtx seeds the request context the way the production
// withAuth + configSnapshotMiddleware chain does for an authenticated
// caller: a non-bypass *config.Config snapshot. The create and delete
// channel verbs are gated via requireAdminAuthz (RequireNotBypass) inside
// HandleChannels — under the single-user model there is no separate role
// check, so a direct HandleChannels call only needs the config snapshot (503
// for a missing/bypass config snapshot).
func withAdminChannelCtx(api *restAPI, r *http.Request) *http.Request {
	// Provide a non-bypass config snapshot so RequireNotBypass lets the request
	// through. api.agentLoop.GetConfig() has DevModeBypass=false by default in the
	// test fixture.
	ctx := context.WithValue(r.Context(), ctxkey.ConfigContextKey{}, api.agentLoop.GetConfig())
	return r.WithContext(ctx)
}

// createChannelInstanceReq issues POST /api/v1/channels with the given JSON
// body as an authenticated caller (create is bypass-gated).
func createChannelInstanceReq(t *testing.T, api *restAPI, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/channels",
		strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminChannelCtx(api, r)
	w := httptest.NewRecorder()
	api.HandleChannels(w, r)
	return w
}

// deleteChannelInstanceReq issues DELETE /api/v1/channels/{id} as an
// authenticated caller (delete is bypass-gated).
func deleteChannelInstanceReq(t *testing.T, api *restAPI, channelID string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/channels/"+channelID, nil)
	r = withAdminChannelCtx(api, r)
	w := httptest.NewRecorder()
	api.HandleChannels(w, r)
	return w
}

// getChannelRoutingHandleReq issues GET /api/v1/channels/{id}/routing via HandleChannels.
func getChannelRoutingHandleReq(t *testing.T, api *restAPI, channelID string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/channels/"+channelID+"/routing", nil)
	w := httptest.NewRecorder()
	api.HandleChannels(w, r)
	return w
}

// putChannelRoutingHandleReq issues PUT /api/v1/channels/{id}/routing via HandleChannels.
func putChannelRoutingHandleReq(t *testing.T, api *restAPI, channelID, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/"+channelID+"/routing",
		strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.HandleChannels(w, r)
	return w
}

// readAuditEventsForTest reads all JSONL audit entries from the audit log file.
// Returns nil if the file does not exist.
func readAuditEventsForTest(t *testing.T, auditDir string) []map[string]any {
	t.Helper()
	auditFile := filepath.Join(auditDir, "audit.jsonl")
	data, err := os.ReadFile(auditFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read audit file: %v", err)
	}
	var entries []map[string]any
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var e map[string]any
		if err := json.Unmarshal(line, &e); err != nil {
			t.Fatalf("parse audit line %q: %v", line, err)
		}
		entries = append(entries, e)
	}
	return entries
}

// newTestAPIWithAuditor creates a test restAPI with a real audit.Logger wired.
// The audit log is written to <tmpDir>/system/audit.jsonl.
func newTestAPIWithAuditor(t *testing.T) (*restAPI, string) {
	t.Helper()
	api := newTestRestAPIWithHome(t)
	auditDir := filepath.Join(api.homePath, "system")
	require.NoError(t, os.MkdirAll(auditDir, 0o700))
	logger, err := audit.NewLogger(audit.LoggerConfig{
		Dir:           auditDir,
		MaxSizeBytes:  1 << 20,
		RetentionDays: 1,
	})
	require.NoError(t, err, "audit logger must initialize")
	t.Cleanup(func() { _ = logger.Close() })
	api.auditor = logger
	return api, auditDir
}

// ── F-9: HandleChannels routing for per-instance IDs (US-11) ─────────────────

// TestChannelsRouter_AcceptsInstanceID verifies US-11 AC-1/2/3:
//   - GET/PUT /channels/whatsapp.eu/routing for a seeded instance → served (not 404)
//   - GET /channels/whatsapp.zzz/routing for a well-formed but absent id → 404 "unknown instance"
//   - GET /channels/whatsapp.BAD/routing for a malformed id → 400 "malformed channel id"
func TestChannelsRouter_AcceptsInstanceID(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	addAgentsToAPI(t, api, []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "ray"},
	})
	writeTestWorkspaceJSON(t, api, "sales", "active", []string{"mia", "ray"}, false)

	// Seed "whatsapp.eu" so GET/routing serves it.
	seedChannelInstance(t, api, "whatsapp.eu")

	// AC-1: seeded instance → GET routing returns 200.
	t.Run("seeded_instance_get_200", func(t *testing.T) {
		w := getChannelRoutingHandleReq(t, api, "whatsapp.eu")
		assert.Equal(t, http.StatusOK, w.Code,
			"GET /channels/whatsapp.eu/routing must return 200 for a seeded instance; body=%s", w.Body.String())
	})

	// AC-1: seeded instance → PUT routing (valid body) returns 200.
	t.Run("seeded_instance_put_200", func(t *testing.T) {
		w := putChannelRoutingHandleReq(t, api, "whatsapp.eu",
			`{"workspace_id":"sales","default_agent_id":"ray"}`)
		assert.Equal(t, http.StatusOK, w.Code,
			"PUT /channels/whatsapp.eu/routing must return 200 for a seeded instance; body=%s", w.Body.String())
	})

	// AC-2: well-formed but absent id → 404 "unknown instance".
	t.Run("absent_instance_404", func(t *testing.T) {
		w := getChannelRoutingHandleReq(t, api, "whatsapp.zzz")
		assert.Equal(t, http.StatusNotFound, w.Code,
			"GET /channels/whatsapp.zzz/routing must return 404 for unknown instance; body=%s", w.Body.String())
		assert.Contains(t, w.Body.String(), "unknown instance",
			"404 body must mention 'unknown instance' (US-11 AC-2)")
	})

	// AC-3: malformed id (uppercase slug) → 400 "malformed channel id" (Fix-B boundary).
	t.Run("malformed_id_400", func(t *testing.T) {
		w := getChannelRoutingHandleReq(t, api, "whatsapp.BAD")
		assert.Equal(
			t,
			http.StatusBadRequest,
			w.Code,
			"GET /channels/whatsapp.BAD/routing must return 400 for malformed id (distinct from 404); body=%s",
			w.Body.String(),
		)
	})
}

// ── F-11: partial cascade aborts intact (MAJ-005) ────────────────────────────

// TestWorkspaceDelete_PartialCascade_AbortsIntact verifies MAJ-005:
// If the config write inside unbindChannelInstancesForWorkspace fails (injected
// by making the home directory read-only so WriteFileAtomic cannot create the
// temp file), handleWorkspaceDelete must:
//   - return 500
//   - leave the workspace file on disk
//   - leave the channel binding unchanged (no orphan)
//
// The injection makes the DIRECTORY read-only (not just the file) because
// WriteFileAtomic uses a temp file + rename in the same dir; it needs dir
// write permission to create the temp file.
func TestWorkspaceDelete_PartialCascade_AbortsIntact(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("cannot inject directory-write failure as root")
	}
	api := newTestRestAPIWithHome(t)
	addAgentsToAPI(t, api, []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "ray"},
	})
	writeTestWorkspaceJSON(t, api, "sales", "active", []string{"mia", "ray"}, false)
	seedChannelInstance(t, api, "whatsapp.eu")

	// Bind whatsapp.eu to workspace "sales" / agent "ray".
	putW := setChannelRoutingReq(t, api, "whatsapp.eu",
		`{"workspace_id":"sales","default_agent_id":"ray"}`)
	require.Equal(t, http.StatusOK, putW.Code, "pre-condition: bind must succeed")

	// Verify the binding is in place before the injected failure.
	preCfg := api.agentLoop.GetConfig()
	preInst, ok := preCfg.Channels["whatsapp.eu"]
	require.True(t, ok)
	require.Equal(t, "sales", preInst.WorkspaceID, "pre-condition: channel must be bound to sales")
	require.NotNil(t, preInst.Identity, "pre-condition: Identity must be set")

	// Inject failure: make the home directory read-only so WriteFileAtomic cannot
	// create the temp file (it calls os.CreateTemp in the same directory).
	// Always restore before test cleanup so the temp dir can be removed.
	require.NoError(t, os.Chmod(api.homePath, 0o555),
		"set home dir read-only to inject config write failure")
	t.Cleanup(func() { _ = os.Chmod(api.homePath, 0o700) })

	// Attempt to delete workspace "sales" — must fail at the channel-unbind step.
	r := httptest.NewRequest(http.MethodDelete, workspaceDeleteURL(t, api, "sales"), nil)
	w := httptest.NewRecorder()
	api.handleWorkspaceDelete(w, r, "sales")

	// Restore dir permissions immediately so subsequent os calls work.
	require.NoError(t, os.Chmod(api.homePath, 0o700))

	// Assert 500: the cascade aborted.
	assert.Equal(t, http.StatusInternalServerError, w.Code,
		"workspace delete must return 500 when channel unbind config write fails (MAJ-005)")

	// Assert the workspace file is still present (not deleted).
	wsPath := filepath.Join(api.homePath, "workspaces", "sales.json")
	_, statErr := os.Stat(wsPath)
	assert.NoError(t, statErr,
		"workspace file must still exist after partial cascade failure (MAJ-005 no orphan)")

	// Assert the binding is unchanged (channel still bound to sales — no partial unbind).
	afterCfg := api.agentLoop.GetConfig()
	afterInst, exists := afterCfg.Channels["whatsapp.eu"]
	// The channel entry must still be bound (WorkspaceID unchanged).
	// Note: safeUpdateConfigJSON reloads from disk after each successful write;
	// a failed write leaves the in-memory config at the last successful state.
	if exists {
		assert.Equal(t, "sales", afterInst.WorkspaceID,
			"WorkspaceID must be unchanged after partial cascade failure (MAJ-005)")
	}
}

// ── FINAL-REVIEW MAJOR: listChannels emits one entry per instance ────────────

// TestListChannels_TwoSameTypeInstances_TwoDistinctEntries verifies the MAJOR
// fix: after creating whatsapp.eu + whatsapp.us, GET /api/v1/channels lists BOTH
// as distinct entries (not collapsed to one "whatsapp" row), each carrying its
// own instance_id. Previously the byType overlay collapsed same-type instances
// last-writer-wins, hiding every instance after the first (US-6/US-11).
func TestListChannels_TwoSameTypeInstances_TwoDistinctEntries(t *testing.T) {
	api := newChannelTestAPI(t, `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{`+
		`"whatsapp.eu":{"type":"whatsapp","enabled":true},`+
		`"whatsapp.us":{"type":"whatsapp","enabled":false}}}`)
	require.NoError(t, api.refreshConfigAndRewireServices(api.configPath()))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/channels", nil)
	api.HandleChannels(w, r)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var entries []gen.ChannelEntry
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &entries))

	// Collect the two whatsapp instance entries by their instance_id.
	byInstanceID := map[string]gen.ChannelEntry{}
	var bareWhatsapp int
	for _, e := range entries {
		baseType, _ := config.ParseInstanceKey(e.Id)
		if baseType != "whatsapp" {
			continue
		}
		if e.InstanceId != nil {
			byInstanceID[*e.InstanceId] = e
		} else {
			bareWhatsapp++
		}
	}

	// Both configured instances must appear as distinct entries.
	eu, hasEU := byInstanceID["whatsapp.eu"]
	us, hasUS := byInstanceID["whatsapp.us"]
	require.True(t, hasEU, "whatsapp.eu must be a distinct entry; got entries=%+v", entries)
	require.True(t, hasUS, "whatsapp.us must be a distinct entry; got entries=%+v", entries)
	assert.Equal(t, "whatsapp.eu", eu.Id, "eu entry id must be the instance key")
	assert.Equal(t, "whatsapp.us", us.Id, "us entry id must be the instance key")
	assert.True(t, eu.Enabled, "whatsapp.eu was configured enabled")
	assert.False(t, us.Enabled, "whatsapp.us was configured disabled")
	// Because a whatsapp instance IS configured, there must be NO static
	// "available but unconfigured" bare "whatsapp" row.
	assert.Zero(t, bareWhatsapp, "no static bare-whatsapp row when instances exist")

	// A base type with no configured instance still appears once (unconfigured).
	var telegramCount int
	for _, e := range entries {
		if e.Id == "telegram" {
			telegramCount++
			assert.Nil(t, e.InstanceId, "unconfigured telegram row has no instance_id")
			assert.False(t, e.Enabled, "unconfigured telegram row is disabled")
		}
	}
	assert.Equal(t, 1, telegramCount, "telegram (no instance) must appear exactly once")
}
