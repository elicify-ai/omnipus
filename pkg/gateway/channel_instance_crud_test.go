//go:build !cgo

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
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
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

// ── F-6: SC-002 persistence assertion on rejection tests ─────────────────────

// TestSetChannelRouting_Bound_EmptyAgentID_NothingPersisted extends the F-6
// requirement: after a 422 (empty agent), cfg.Channels["whatsapp.eu"] must have
// WorkspaceID=="" and Identity==nil (SC-002 "0 persist").
func TestSetChannelRouting_Bound_EmptyAgentID_NothingPersisted(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	addAgentsToAPI(t, api, []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "ray"},
	})
	writeTestWorkspaceJSON(t, api, "sales", "active", []string{"mia", "ray"}, false)
	seedChannelInstance(t, api, "whatsapp.eu")

	w := setChannelRoutingReq(t, api, "whatsapp.eu",
		`{"workspace_id":"sales","default_agent_id":""}`)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code,
		"empty default_agent_id with workspace_id must be 422 (FR-005)")

	// SC-002: nothing must be persisted.
	cfg := api.agentLoop.GetConfig()
	inst, ok := cfg.Channels["whatsapp.eu"]
	require.True(t, ok, "channel entry must still exist after rejection")
	assert.Empty(t, inst.WorkspaceID, "WorkspaceID must NOT be written on 422 rejection (SC-002)")
	assert.Nil(t, inst.Identity, "Identity must NOT be written on 422 rejection (SC-002)")
}

// TestSetChannelRouting_Bound_AgentNotInTeam_NothingPersisted extends F-6:
// after a 422 (agent not in team), cfg.Channels["whatsapp.eu"] must remain unbound.
func TestSetChannelRouting_Bound_AgentNotInTeam_NothingPersisted(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	addAgentsToAPI(t, api, []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "ray"},
		{ID: "jim"},
	})
	writeTestWorkspaceJSON(t, api, "sales", "active", []string{"mia", "ray"}, false)
	seedChannelInstance(t, api, "whatsapp.eu")

	w := setChannelRoutingReq(t, api, "whatsapp.eu",
		`{"workspace_id":"sales","default_agent_id":"jim"}`)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code,
		"agent not in CoreTeam must be 422 (FR-006)")

	cfg := api.agentLoop.GetConfig()
	inst, ok := cfg.Channels["whatsapp.eu"]
	require.True(t, ok)
	assert.Empty(t, inst.WorkspaceID, "WorkspaceID must NOT be written on 422 rejection (SC-002)")
	assert.Nil(t, inst.Identity, "Identity must NOT be written on 422 rejection (SC-002)")
}

// TestSetChannelRouting_Bound_UnknownWorkspace_NothingPersisted extends F-6:
// after a 404 (unknown workspace), the instance must remain unbound.
func TestSetChannelRouting_Bound_UnknownWorkspace_NothingPersisted(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	addAgentsToAPI(t, api, []config.AgentConfig{
		{ID: "mia", Default: true},
	})
	seedChannelInstance(t, api, "whatsapp.eu")

	w := setChannelRoutingReq(t, api, "whatsapp.eu",
		`{"workspace_id":"ghost","default_agent_id":"mia"}`)
	require.Equal(t, http.StatusNotFound, w.Code,
		"unknown workspace_id must be 404 (FR-007)")

	cfg := api.agentLoop.GetConfig()
	inst, ok := cfg.Channels["whatsapp.eu"]
	require.True(t, ok)
	assert.Empty(t, inst.WorkspaceID, "WorkspaceID must NOT be written on 404 rejection (SC-002)")
	assert.Nil(t, inst.Identity, "Identity must NOT be written on 404 rejection (SC-002)")
}

// TestSetChannelRouting_Bound_WorkerAgent_NothingPersisted extends F-6:
// after a 422 (worker agent), the instance must remain unbound.
func TestSetChannelRouting_Bound_WorkerAgent_NothingPersisted(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	addAgentsToAPI(t, api, []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "worker1", Type: config.AgentTypeWorker},
	})
	writeTestWorkspaceJSON(t, api, "sales", "active", []string{"mia", "worker1"}, false)
	seedChannelInstance(t, api, "whatsapp.eu")

	w := setChannelRoutingReq(t, api, "whatsapp.eu",
		`{"workspace_id":"sales","default_agent_id":"worker1"}`)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code,
		"worker agent must be 422 (FR-008/MIN-002)")

	cfg := api.agentLoop.GetConfig()
	inst, ok := cfg.Channels["whatsapp.eu"]
	require.True(t, ok)
	assert.Empty(t, inst.WorkspaceID, "WorkspaceID must NOT be written on 422 rejection (SC-002)")
	assert.Nil(t, inst.Identity, "Identity must NOT be written on 422 rejection (SC-002)")
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
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/workspaces/sales", nil)
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

// ── F-14: routing-change audit event (FR-030) ────────────────────────────────

// TestSetChannelRouting_EmitsAuditEvent verifies FR-030 / STRIDE repudiation:
// a valid re-bind PUT emits a channel.routing.changed audit entry with the
// correct channel_id, workspace_id, and agent_id.
func TestSetChannelRouting_EmitsAuditEvent(t *testing.T) {
	api, auditDir := newTestAPIWithAuditor(t)
	addAgentsToAPI(t, api, []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "ray"},
	})
	writeTestWorkspaceJSON(t, api, "sales", "active", []string{"mia", "ray"}, false)
	seedChannelInstance(t, api, "whatsapp.eu")

	// Issue a valid bound routing PUT.
	w := setChannelRoutingReq(t, api, "whatsapp.eu",
		`{"workspace_id":"sales","default_agent_id":"ray"}`)
	require.Equal(t, http.StatusOK, w.Code, "valid bound routing must return 200; body=%s", w.Body.String())

	// Flush the audit logger (Close + reopen is not needed; Log is synchronous).
	// Read the audit log and find the channel.routing.changed entry.
	entries := readAuditEventsForTest(t, auditDir)
	require.NotEmpty(t, entries, "audit log must be non-empty after a routing PUT")

	var found bool
	for _, e := range entries {
		if e["event"] != string(audit.EventChannelRoutingChanged) {
			continue
		}
		found = true
		details, _ := e["details"].(map[string]any)
		require.NotNil(t, details, "audit entry must have a details map")
		assert.Equal(t, "whatsapp.eu", details["channel_id"],
			"audit entry must carry channel_id=whatsapp.eu (FR-030)")
		assert.Equal(t, "sales", details["workspace_id"],
			"audit entry must carry workspace_id=sales (FR-030)")
		assert.Equal(t, "ray", details["agent_id"],
			"audit entry must carry agent_id=ray (FR-030)")
		break
	}
	assert.True(t, found,
		"a channel.routing.changed audit event must be emitted by setChannelRouting (FR-030); got events: %v",
		func() []string {
			evts := make([]string, 0, len(entries))
			for _, e := range entries {
				evts = append(evts, fmt.Sprintf("%v", e["event"]))
			}
			return evts
		}())
}

// ── Create-instance endpoint tests ───────────────────────────────────────────

// TestCreateChannelInstance_HappyPath verifies POST /channels creates a new
// instance with the correct key, type, and enabled=false.
func TestCreateChannelInstance_HappyPath(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := createChannelInstanceReq(t, api, `{"type":"whatsapp","slug":"eu"}`)

	require.Equal(t, http.StatusCreated, w.Code,
		"POST /channels must return 201 on success; body=%s", w.Body.String())

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "whatsapp.eu", resp["id"], "response id must be '<type>.<slug>'")
	assert.Equal(t, "whatsapp", resp["type"], "response type must match")
	assert.Equal(t, false, resp["enabled"], "new instance must start disabled")

	// Verify the entry persists in the live config.
	cfg := api.agentLoop.GetConfig()
	inst, exists := cfg.Channels["whatsapp.eu"]
	require.True(t, exists, "whatsapp.eu must exist in cfg.Channels after create")
	assert.Equal(t, "whatsapp", inst.Type, "persisted type must be whatsapp")
	assert.False(t, inst.Enabled, "persisted instance must be disabled")
}

// TestCreateChannelInstance_UnknownType verifies POST /channels returns 400
// when the type is not a known channel type.
func TestCreateChannelInstance_UnknownType(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := createChannelInstanceReq(t, api, `{"type":"nonexistent","slug":"eu"}`)

	assert.Equal(t, http.StatusBadRequest, w.Code,
		"unknown channel type must return 400; body=%s", w.Body.String())
}

// TestCreateChannelInstance_MalformedSlug_Uppercase verifies POST /channels
// returns 400 when the slug contains uppercase characters (FR-017 locked).
func TestCreateChannelInstance_MalformedSlug_Uppercase(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := createChannelInstanceReq(t, api, `{"type":"whatsapp","slug":"EU"}`)

	assert.Equal(t, http.StatusBadRequest, w.Code,
		"uppercase slug must return 400 (FR-017); body=%s", w.Body.String())
}

// TestCreateChannelInstance_MalformedSlug_TooLong verifies POST /channels
// returns 400 when the slug exceeds 32 characters.
func TestCreateChannelInstance_MalformedSlug_TooLong(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	longSlug := strings.Repeat("a", 33) // 33 chars, exceeds 32-char max

	w := createChannelInstanceReq(t, api, fmt.Sprintf(`{"type":"whatsapp","slug":%q}`, longSlug))

	assert.Equal(t, http.StatusBadRequest, w.Code,
		"overlong slug must return 400 (FR-017); body=%s", w.Body.String())
}

// TestCreateChannelInstance_Duplicate_Returns409 verifies POST /channels returns
// 409 Conflict when the instance key already exists.
func TestCreateChannelInstance_Duplicate_Returns409(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// First create succeeds.
	w1 := createChannelInstanceReq(t, api, `{"type":"whatsapp","slug":"eu"}`)
	require.Equal(t, http.StatusCreated, w1.Code, "first create must succeed; body=%s", w1.Body.String())

	// Second create with the same key must return 409.
	w2 := createChannelInstanceReq(t, api, `{"type":"whatsapp","slug":"eu"}`)
	assert.Equal(t, http.StatusConflict, w2.Code,
		"duplicate instance key must return 409; body=%s", w2.Body.String())
}

// TestCreateChannelInstance_Telegram verifies a non-WhatsApp channel type works.
func TestCreateChannelInstance_Telegram(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := createChannelInstanceReq(t, api, `{"type":"telegram","slug":"main-bot"}`)

	require.Equal(t, http.StatusCreated, w.Code,
		"telegram instance must be created; body=%s", w.Body.String())

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "telegram.main-bot", resp["id"])
	assert.Equal(t, "telegram", resp["type"])
}

// ── Delete-instance endpoint tests ───────────────────────────────────────────

// TestDeleteChannelInstance_HappyPath verifies DELETE /channels/{id} removes
// the config entry, leaves no stale wildcard binding, and returns 204.
func TestDeleteChannelInstance_HappyPath(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	seedChannelInstance(t, api, "whatsapp.eu")

	// Pre-plant a wildcard binding that must be cleaned up.
	require.NoError(t, api.safeUpdateConfigJSON(func(m map[string]any) error {
		m["bindings"] = []any{
			map[string]any{
				"agent_id": "mia",
				"match": map[string]any{
					"channel":    "whatsapp.eu",
					"account_id": "*",
				},
			},
		}
		return nil
	}))

	w := deleteChannelInstanceReq(t, api, "whatsapp.eu")

	require.Equal(t, http.StatusNoContent, w.Code,
		"DELETE /channels/whatsapp.eu must return 204; body=%s", w.Body.String())

	// Config entry must be gone.
	cfg := api.agentLoop.GetConfig()
	_, exists := cfg.Channels["whatsapp.eu"]
	assert.False(t, exists, "whatsapp.eu must be removed from cfg.Channels after delete")

	// Stale wildcard binding must be cleaned up.
	for _, b := range cfg.Bindings {
		if b.Match.Channel == "whatsapp.eu" && b.Match.AccountID == "*" &&
			b.Match.Peer == nil && b.Match.GuildID == "" && b.Match.TeamID == "" {
			t.Errorf("stale wildcard binding for 'whatsapp.eu' must be removed by DELETE: %+v", b)
		}
	}
}

// TestDeleteChannelInstance_Unknown_Returns404 verifies DELETE /channels/{id}
// returns 404 for an instance that does not exist in cfg.Channels.
func TestDeleteChannelInstance_Unknown_Returns404(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// "whatsapp.zzz" passes grammar but doesn't exist in config.
	w := deleteChannelInstanceReq(t, api, "whatsapp.zzz")

	assert.Equal(t, http.StatusNotFound, w.Code,
		"unknown instance must return 404; body=%s", w.Body.String())
}

// TestDeleteChannelInstance_MalformedID_Returns400 verifies DELETE
// /channels/{id} returns 400 for a malformed instance key.
func TestDeleteChannelInstance_MalformedID_Returns400(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// "whatsapp.BAD" has an uppercase slug — malformed.
	w := deleteChannelInstanceReq(t, api, "whatsapp.BAD")

	assert.Equal(t, http.StatusBadRequest, w.Code,
		"malformed channel id must return 400; body=%s", w.Body.String())
}

// TestDeleteChannelInstance_RemovesStateDir verifies that DELETE removes the
// WhatsApp per-instance state directory (US-10 AC-2 / FR-025).
func TestDeleteChannelInstance_RemovesStateDir(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Seed the channel instance and create the WhatsApp store directory.
	seedChannelInstance(t, api, "whatsapp.eu")
	cfg := api.agentLoop.GetConfig()
	storeDir := filepath.Join(cfg.WorkspacePath(), "whatsapp", "whatsapp.eu")
	require.NoError(t, os.MkdirAll(storeDir, 0o700))
	// Write a fake store file to prove the directory existed.
	fakeStore := filepath.Join(storeDir, "store.db")
	require.NoError(t, os.WriteFile(fakeStore, []byte("fake"), 0o600))

	w := deleteChannelInstanceReq(t, api, "whatsapp.eu")
	require.Equal(t, http.StatusNoContent, w.Code,
		"DELETE must succeed; body=%s", w.Body.String())

	// Store directory must be gone (best-effort; not a hard failure).
	_, statErr := os.Stat(storeDir)
	assert.True(t, os.IsNotExist(statErr),
		"WhatsApp store directory must be removed after instance delete (US-10 AC-2)")
}

// TestDeleteChannelInstance_BareTypeKey verifies that bare-type keys (e.g.
// "telegram") can also be deleted, not just namespaced per-instance keys.
func TestDeleteChannelInstance_BareTypeKey(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	// Seed a bare-type entry (the legacy single-instance pattern).
	require.NoError(t, api.safeUpdateConfigJSON(func(m map[string]any) error {
		channels, _ := m["channels"].(map[string]any)
		if channels == nil {
			channels = map[string]any{}
		}
		channels["telegram"] = map[string]any{
			"type":    "telegram",
			"enabled": false,
		}
		m["channels"] = channels
		return nil
	}))

	w := deleteChannelInstanceReq(t, api, "telegram")
	require.Equal(t, http.StatusNoContent, w.Code,
		"DELETE of bare-type key must return 204; body=%s", w.Body.String())

	cfg := api.agentLoop.GetConfig()
	_, exists := cfg.Channels["telegram"]
	assert.False(t, exists, "telegram must be removed from cfg.Channels after delete")
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

// ── FINAL-REVIEW MEDIUM: delete emits an audit event ─────────────────────────

// TestDeleteChannelInstance_EmitsAuditEvent verifies a successful delete emits a
// channel.instance.deleted audit entry with channel_id, type, and
// cleanup_failed=false (happy path — no orphaned credential).
func TestDeleteChannelInstance_EmitsAuditEvent(t *testing.T) {
	api, auditDir := newTestAPIWithAuditor(t)
	seedChannelInstance(t, api, "whatsapp.eu")

	w := deleteChannelInstanceReq(t, api, "whatsapp.eu")
	require.Equal(t, http.StatusNoContent, w.Code, "delete must succeed; body=%s", w.Body.String())

	entries := readAuditEventsForTest(t, auditDir)
	require.NotEmpty(t, entries, "audit log must be non-empty after a delete")

	var found bool
	for _, e := range entries {
		if e["event"] != string(audit.EventChannelInstanceDeleted) {
			continue
		}
		found = true
		assert.Equal(t, string(audit.DecisionAllow), e["decision"],
			"happy-path delete decision must be allow")
		details, _ := e["details"].(map[string]any)
		require.NotNil(t, details, "delete audit entry must have a details map")
		assert.Equal(t, "whatsapp.eu", details["channel_id"], "audit channel_id must match")
		assert.Equal(t, "whatsapp", details["type"], "audit type must be the base type")
		assert.Equal(t, false, details["cleanup_failed"],
			"cleanup_failed must be false on the happy path")
		break
	}
	assert.True(t, found,
		"a channel.instance.deleted audit event must be emitted (ADR-029 FR-025); got events: %v",
		func() []string {
			evts := make([]string, 0, len(entries))
			for _, e := range entries {
				evts = append(evts, fmt.Sprintf("%v", e["event"]))
			}
			return evts
		}())
}

// ── whole-codebase-review Backend-High finding #3: configure emits no audit ──

// TestConfigureChannel_EmitsAuditEvent verifies a successful channel-instance
// configure (a mutating, credential-touching write) emits a
// channel.instance.configured audit entry with channel_id, type,
// cleanup_failed=false, and the set of top-level fields the request
// touched — mirroring TestDeleteChannelInstance_EmitsAuditEvent above.
// configureChannel previously had NO audit.Log call at all (Backend-High finding).
func TestConfigureChannel_EmitsAuditEvent(t *testing.T) {
	api, auditDir := newTestAPIWithAuditor(t)
	// Wire an unlocked credential store so the token-bearing configure below
	// can actually commit (Phase C's storeCredential call needs a working store).
	t.Setenv("OMNIPUS_MASTER_KEY", testMasterKey)
	credStore := credentials.NewStore(api.homePath + "/credentials.json")
	require.NoError(t, credentials.Unlock(credStore), "unlock credential store")
	api.credStore = credStore

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/telegram/configure",
		strings.NewReader(`{"token":"super-secret-123"}`))
	r.Header.Set("Content-Type", "application/json")
	api.configureChannel(w, r, "telegram")
	require.Equal(t, http.StatusOK, w.Code, "configure must succeed; body=%s", w.Body.String())

	entries := readAuditEventsForTest(t, auditDir)
	require.NotEmpty(t, entries, "audit log must be non-empty after a configure")

	var found bool
	for _, e := range entries {
		if e["event"] != string(audit.EventChannelInstanceConfigured) {
			continue
		}
		found = true
		assert.Equal(t, string(audit.DecisionAllow), e["decision"],
			"happy-path configure decision must be allow")
		details, _ := e["details"].(map[string]any)
		require.NotNil(t, details, "configure audit entry must have a details map")
		assert.Equal(t, "telegram", details["channel_id"], "audit channel_id must match")
		assert.Equal(t, "telegram", details["type"], "audit type must be the base type")
		assert.Equal(t, false, details["cleanup_failed"],
			"cleanup_failed must be false on the happy path")
		fields, _ := details["fields"].([]any)
		assert.Contains(t, fields, "token_ref",
			"fields must record the persisted token_ref (never the raw secret)")
		for _, f := range fields {
			assert.NotEqual(t, "super-secret-123", f, "raw secret value must never be logged")
		}
		break
	}
	assert.True(t, found,
		"a channel.instance.configured audit event must be emitted; got events: %v",
		func() []string {
			evts := make([]string, 0, len(entries))
			for _, e := range entries {
				evts = append(evts, fmt.Sprintf("%v", e["event"]))
			}
			return evts
		}())
}
