// rest_channels_test.go: tests for connectors — enable, configure, route, and test channel instances

package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	whatsappnative "github.com/elicify-ai/omnipus/pkg/channels/whatsapp_native"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- moved from rest.go tests 2026-09-15 ---

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
	// Content check (anti-shortcut, mirrors channel_routing_binding_test.go's
	// TestSetChannelRouting_Bound_AgentNotInTeam): "agent not found" is ALSO a
	// 422 in the bound flow — pin the actual rejection reason so a regression
	// that silently drops "jim" from the roster is caught here, not masked by
	// the shared status code.
	assert.Contains(t, w.Body.String(), "not a member of workspace",
		"rejection must be the team-membership check (FR-006), not a generic agent-not-found")

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
	// Content check (anti-shortcut, mirrors channel_routing_binding_test.go's
	// TestSetChannelRouting_Bound_WorkerAgent): "agent not found" is ALSO a
	// 422 in the bound flow — pin the actual rejection reason so a regression
	// that silently drops "worker1" from the roster is caught here, not
	// masked by the shared status code.
	assert.Contains(t, w.Body.String(), "workers are not chat targets",
		"rejection must be the worker-type check (FR-008), not a generic agent-not-found")

	cfg := api.agentLoop.GetConfig()
	inst, ok := cfg.Channels["whatsapp.eu"]
	require.True(t, ok)
	assert.Empty(t, inst.WorkspaceID, "WorkspaceID must NOT be written on 422 rejection (SC-002)")
	assert.Nil(t, inst.Identity, "Identity must NOT be written on 422 rejection (SC-002)")
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
	storeDir := filepath.Join(cfg.AgentHomeBasePath(), "whatsapp", "whatsapp.eu")
	require.NoError(t, os.MkdirAll(storeDir, 0o700))
	// Write a fake store file to prove the directory existed.
	fakeStore := filepath.Join(storeDir, "store.db")
	require.NoError(t, os.WriteFile(fakeStore, []byte("fake"), 0o600))

	w := deleteChannelInstanceReq(t, api, "whatsapp.eu")
	require.Equal(t, http.StatusNoContent, w.Code,
		"DELETE must succeed; body=%s", w.Body.String())

	// Store directory must be gone (best-effort; not a hard failure).
	_, statErr := os.Stat(storeDir)
	assert.True(t, errors.Is(statErr, os.ErrNotExist), "WhatsApp store directory must be removed after instance delete (US-10 AC-2)")
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

// TestConfigureChannel_RejectsIdentity covers the FINAL-REVIEW HIGH security fix:
// binding (identity) must NOT be settable via /configure — it bypasses the FR-006
// CoreTeam-membership check that lives only in setChannelRouting. configureChannel
// now rejects any identity field with 400 and directs the caller to the routing
// endpoint, and persists NOTHING (the config entry is untouched).
func TestConfigureChannel_RejectsIdentity(t *testing.T) {
	api := newChannelTestAPI(t, `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{}}`)

	// Even a well-formed agent identity is rejected — binding goes through routing.
	for _, body := range []string{
		`{"identity":{"kind":"agent","id":"concierge"}}`, // well-formed but forbidden here
		`{"identity":{"kind":"user"}}`,                   // user-kind override also forbidden
		`{"identity":null}`,                              // even an explicit clear must route through /routing
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/telegram/configure", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		api.configureChannel(w, r, "telegram")
		assert.Equal(t, http.StatusBadRequest, w.Code,
			"identity via configure %q must be 400, body=%s", body, w.Body.String())
		assert.Contains(t, w.Body.String(), "routing",
			"400 body must direct the caller to the routing endpoint, body=%s", w.Body.String())
	}

	// Nothing was persisted: the channels map is still empty (no telegram entry).
	raw, err := os.ReadFile(api.homePath + "/config.json")
	require.NoError(t, err)
	var diskCfg map[string]any
	require.NoError(t, json.Unmarshal(raw, &diskCfg))
	channels, _ := diskCfg["channels"].(map[string]any)
	_, hasTelegram := channels["telegram"]
	assert.False(t, hasTelegram, "a rejected identity-configure must persist nothing")
}

// TestConfigureChannel_RejectsWorkspaceID covers the FINAL-REVIEW HIGH security
// fix: workspace_id must NOT be settable via /configure. Persisting workspace_id
// (with an identity) makes IsWorkspaceBound() true and routes a workspace's
// inbound traffic at the configured agent WITHOUT the CoreTeam check. configure
// rejects it with 400 and directs the caller to the routing endpoint.
func TestConfigureChannel_RejectsWorkspaceID(t *testing.T) {
	api := newChannelTestAPI(t, `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{}}`)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/telegram/configure",
		strings.NewReader(`{"workspace_id":"sales"}`))
	r.Header.Set("Content-Type", "application/json")
	api.configureChannel(w, r, "telegram")
	assert.Equal(t, http.StatusBadRequest, w.Code,
		"workspace_id via configure must be 400, body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), "routing",
		"400 body must direct the caller to the routing endpoint, body=%s", w.Body.String())

	// Nothing persisted.
	raw, err := os.ReadFile(api.homePath + "/config.json")
	require.NoError(t, err)
	var diskCfg map[string]any
	require.NoError(t, json.Unmarshal(raw, &diskCfg))
	channels, _ := diskCfg["channels"].(map[string]any)
	_, hasTelegram := channels["telegram"]
	assert.False(t, hasTelegram, "a rejected workspace_id-configure must persist nothing")
}

// TestConfigureChannel_StripsFilesystemPathFields covers the FINAL-REVIEW HIGH
// security fix: filesystem-path fields (session_store_path, crypto_database_path,
// service_account_file) are stripped from a configure body so an attacker cannot
// persist an attacker-chosen path that a later deleteChannelInstance would
// os.RemoveAll. The configure succeeds (paths are silently ignored, not rejected)
// but NONE of the path fields land in config.json.
func TestConfigureChannel_StripsFilesystemPathFields(t *testing.T) {
	api := newChannelTestAPI(t, `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{}}`)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/channels/whatsapp/configure",
		strings.NewReader(
			`{"session_store_path":"/etc","crypto_database_path":"/root/.ssh","service_account_file":"/home/victim/secrets.json"}`,
		),
	)
	r.Header.Set("Content-Type", "application/json")
	api.configureChannel(w, r, "whatsapp")
	require.Equal(t, http.StatusOK, w.Code,
		"configure with only path fields must still succeed (paths stripped), body=%s", w.Body.String())

	raw, err := os.ReadFile(api.homePath + "/config.json")
	require.NoError(t, err)
	var diskCfg map[string]any
	require.NoError(t, json.Unmarshal(raw, &diskCfg))
	channels, _ := diskCfg["channels"].(map[string]any)
	wa, _ := channels["whatsapp"].(map[string]any)
	require.NotNil(t, wa, "whatsapp entry must exist (type discriminator persisted)")
	for _, f := range []string{"session_store_path", "crypto_database_path", "service_account_file"} {
		_, present := wa[f]
		assert.False(t, present, "filesystem-path field %q must be stripped, never persisted", f)
	}
}

// TestHandleChannels_SurfacesInstanceIDAndIdentity covers FR-2.5: GET
// /api/v1/channels surfaces instance_id and identity for configured instances.
func TestHandleChannels_SurfacesInstanceIDAndIdentity(t *testing.T) {
	api := newChannelTestAPI(
		t,
		`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{"telegram":{"type":"telegram","enabled":true,"identity":{"kind":"agent","id":"concierge"}}}}`,
	)
	// HandleChannels reads the in-memory loop config; load the on-disk channels
	// (with the configured instance + identity) into it.
	require.NoError(t, api.refreshConfigAndRewireServices(api.configPath()))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/channels", nil)
	api.HandleChannels(w, r)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var entries []gen.ChannelEntry
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &entries))

	var tg *gen.ChannelEntry
	var webchat *gen.ChannelEntry
	for i := range entries {
		switch entries[i].Id {
		case "telegram":
			tg = &entries[i]
		case "webchat":
			webchat = &entries[i]
		}
	}
	require.NotNil(t, tg, "telegram entry must be present")
	require.NotNil(t, tg.InstanceId, "telegram instance_id must be populated (FR-2.5)")
	assert.Equal(t, "telegram", *tg.InstanceId)
	require.NotNil(t, tg.Identity, "telegram identity must be populated (FR-2.5)")
	assert.Equal(t, gen.ChannelEntryIdentityKindAgent, tg.Identity.Kind)
	require.NotNil(t, tg.Identity.Id)
	assert.Equal(t, "concierge", *tg.Identity.Id)

	// A channel with no configured instance has no instance_id/identity.
	require.NotNil(t, webchat, "webchat entry must be present")
	assert.Nil(t, webchat.InstanceId, "webchat (built-in) must have no instance_id")
	assert.Nil(t, webchat.Identity, "webchat (built-in) must have no identity")
}

// ── TDD #13: rejection set (DS-1) ─────────────────────────────────────────────

// TestSetChannelRouting_Bound_EmptyAgentID verifies FR-005:
// workspace_id present but default_agent_id empty → 422.
func TestSetChannelRouting_Bound_EmptyAgentID(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	addAgentsToAPI(t, api, []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "ray"},
	})
	writeTestWorkspaceJSON(t, api, "sales", "active", []string{"mia", "ray"}, false)
	seedChannelInstance(t, api, "whatsapp.eu")

	w := setChannelRoutingReq(t, api, "whatsapp.eu",
		`{"workspace_id":"sales","default_agent_id":""}`)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code,
		"empty default_agent_id with workspace_id must be 422 (FR-005)")
}

// TestSetChannelRouting_Bound_AgentNotInTeam verifies FR-006:
// agent not in workspace CoreTeam → 422.
func TestSetChannelRouting_Bound_AgentNotInTeam(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	addAgentsToAPI(t, api, []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "ray"},
		{ID: "jim"},
	})
	// CoreTeam has mia and ray, NOT jim.
	writeTestWorkspaceJSON(t, api, "sales", "active", []string{"mia", "ray"}, false)
	seedChannelInstance(t, api, "whatsapp.eu")

	w := setChannelRoutingReq(t, api, "whatsapp.eu",
		`{"workspace_id":"sales","default_agent_id":"jim"}`)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code,
		"agent not in CoreTeam must be 422 (FR-006)")
	// Content check (anti-shortcut): "agent not found" is ALSO a 422 in the
	// bound flow — pin the actual rejection reason so a regression that
	// silently drops "jim" from the roster (making it look "not found" instead
	// of "not in team") is caught by this test, not masked by a shared status code.
	assert.Contains(t, w.Body.String(), "not a member of workspace",
		"rejection must be the team-membership check (FR-006), not a generic agent-not-found")
}

// TestSetChannelRouting_Bound_UnknownWorkspace verifies FR-007:
// workspace_id references a non-existent workspace → 404.
func TestSetChannelRouting_Bound_UnknownWorkspace(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	addAgentsToAPI(t, api, []config.AgentConfig{
		{ID: "mia", Default: true},
	})
	// No workspace "ghost" written.
	seedChannelInstance(t, api, "whatsapp.eu")

	w := setChannelRoutingReq(t, api, "whatsapp.eu",
		`{"workspace_id":"ghost","default_agent_id":"mia"}`)

	assert.Equal(t, http.StatusNotFound, w.Code,
		"unknown workspace_id must be 404 (FR-007)")
}

// TestSetChannelRouting_Bound_ArchivedWorkspace verifies FR-007:
// archived workspace → 404 (treated same as unknown/inactive).
func TestSetChannelRouting_Bound_ArchivedWorkspace(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	addAgentsToAPI(t, api, []config.AgentConfig{
		{ID: "mia", Default: true},
	})
	writeTestWorkspaceJSON(t, api, "archived1", "archived", []string{"mia"}, false)
	seedChannelInstance(t, api, "whatsapp.eu")

	w := setChannelRoutingReq(t, api, "whatsapp.eu",
		`{"workspace_id":"archived1","default_agent_id":"mia"}`)

	assert.Equal(t, http.StatusNotFound, w.Code,
		"archived workspace must be 404 (FR-007)")
}

// TestSetChannelRouting_Bound_WorkerAgent verifies FR-008 / MIN-002:
// worker agent → 422 (standardized from former 400).
func TestSetChannelRouting_Bound_WorkerAgent(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	addAgentsToAPI(t, api, []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "worker1", Type: config.AgentTypeWorker},
	})
	writeTestWorkspaceJSON(t, api, "sales", "active", []string{"mia", "worker1"}, false)
	seedChannelInstance(t, api, "whatsapp.eu")

	w := setChannelRoutingReq(t, api, "whatsapp.eu",
		`{"workspace_id":"sales","default_agent_id":"worker1"}`)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code,
		"worker agent must be 422 (FR-008/MIN-002)")
	// Content check (anti-shortcut): "agent not found" is ALSO a 422 in the
	// bound flow — pin the actual rejection reason so a regression that
	// silently drops "worker1" from the roster is caught here rather than
	// masked by the shared status code.
	assert.Contains(t, w.Body.String(), "workers are not chat targets",
		"rejection must be the worker-type check (FR-008), not a generic agent-not-found")
}

// TestSetChannelRouting_Unbound_Worker_Returns422 verifies MIN-002 in the
// unbound (legacy) flow: worker agent → 422, not 400.
func TestSetChannelRouting_Unbound_Worker_Returns422(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	addAgentsToAPI(t, api, []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "worker1", Type: config.AgentTypeWorker},
	})

	// No workspace_id → unbound flow.
	w := setChannelRoutingReq(t, api, "telegram",
		`{"default_agent_id":"worker1"}`)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code,
		"unbound worker must be 422 (MIN-002)")
	// Content check (anti-shortcut): in the UNBOUND flow "agent not found" is
	// a 404, not a 422 (see rest.go's unbound-flow lookup) — so this
	// particular pair can't be confused by status code alone, but pin the
	// message anyway so a future change collapsing the two status codes
	// still gets caught here.
	assert.Contains(t, w.Body.String(), "workers are not chat targets",
		"rejection must be the worker-type check (MIN-002), not a generic agent-not-found")
}

// ── TDD #14: valid bound binding persists ─────────────────────────────────────

// TestSetChannelRouting_ValidBinding_SetsIdentity verifies FR-010/FR-029:
// a valid bound PUT persists WorkspaceID + Identity and removes any stale
// wildcard binding for the same instance.
func TestSetChannelRouting_ValidBinding_SetsIdentity(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	addAgentsToAPI(t, api, []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "ray"},
	})
	writeTestWorkspaceJSON(t, api, "sales", "active", []string{"mia", "ray"}, false)

	seedChannelInstance(t, api, "whatsapp.eu")

	// Pre-plant a stale wildcard binding for the same channel instance.
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

	w := setChannelRoutingReq(t, api, "whatsapp.eu",
		`{"workspace_id":"sales","default_agent_id":"ray"}`)

	require.Equal(t, http.StatusOK, w.Code, "valid bound routing must return 200")

	// Verify response body.
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "sales", resp["workspace_id"], "response must include workspace_id")
	assert.Equal(t, "ray", resp["default_agent_id"], "response must include default_agent_id")

	// Verify the stale wildcard binding for whatsapp.eu is removed.
	liveCfg := api.agentLoop.GetConfig()
	for _, b := range liveCfg.Bindings {
		if b.Match.Channel == "whatsapp.eu" && b.Match.AccountID == "*" &&
			b.Match.Peer == nil && b.Match.GuildID == "" && b.Match.TeamID == "" {
			t.Errorf("stale wildcard binding for 'whatsapp.eu' must be removed by the PUT: %+v", b)
		}
	}

	// Verify cfg.Channels entry has WorkspaceID + Identity.
	inst, ok := liveCfg.Channels["whatsapp.eu"]
	require.True(t, ok, "cfg.Channels['whatsapp.eu'] must exist after binding")
	assert.Equal(t, "sales", inst.WorkspaceID, "WorkspaceID must be set")
	require.NotNil(t, inst.Identity, "Identity must be set")
	assert.Equal(t, "agent", inst.Identity.Kind, "Identity.Kind must be 'agent'")
	assert.Equal(t, "ray", inst.Identity.ID, "Identity.ID must be 'ray'")
}

// ── TDD #15: GET round-trip ────────────────────────────────────────────────────

// TestGetChannelRouting_BoundReadsIdentity verifies FR-029 MAJ-004:
// after a valid bound PUT, GET reads from cfg.Channels (Identity), not wildcard.
func TestGetChannelRouting_BoundReadsIdentity(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	addAgentsToAPI(t, api, []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "ray"},
	})
	writeTestWorkspaceJSON(t, api, "sales", "active", []string{"mia", "ray"}, false)
	seedChannelInstance(t, api, "whatsapp.eu")

	// PUT a valid binding.
	putW := setChannelRoutingReq(t, api, "whatsapp.eu",
		`{"workspace_id":"sales","default_agent_id":"ray"}`)
	require.Equal(t, http.StatusOK, putW.Code)

	// GET the routing.
	getW := getChannelRoutingReq(t, api, "whatsapp.eu")
	require.Equal(t, http.StatusOK, getW.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(getW.Body.Bytes(), &resp))
	assert.Equal(t, "sales", resp["workspace_id"], "GET must return workspace_id from Identity")
	assert.Equal(t, "ray", resp["default_agent_id"], "GET must return agent_id from Identity")
}

// TestGetChannelRouting_UnboundReadsWildcard verifies that for an UNBOUND instance
// GET returns the legacy wildcard binding agent_id and no workspace_id.
func TestGetChannelRouting_UnboundReadsWildcard(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	addAgentsToAPI(t, api, []config.AgentConfig{
		{ID: "mia", Default: true},
	})

	// Plant a wildcard binding for telegram (unbound).
	require.NoError(t, api.safeUpdateConfigJSON(func(m map[string]any) error {
		m["bindings"] = []any{
			map[string]any{
				"agent_id": "mia",
				"match": map[string]any{
					"channel":    "telegram",
					"account_id": "*",
				},
			},
		}
		return nil
	}))

	getW := getChannelRoutingReq(t, api, "telegram")
	require.Equal(t, http.StatusOK, getW.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(getW.Body.Bytes(), &resp))
	assert.Equal(t, "mia", resp["default_agent_id"],
		"unbound GET must read the wildcard binding agent_id")
	// workspace_id must be absent or empty for unbound instances.
	wsVal, hasWS := resp["workspace_id"]
	if hasWS && wsVal != nil && wsVal != "" {
		t.Errorf("unbound GET must not return workspace_id; got %v", wsVal)
	}
}

// ── S-1+S-2: HandleChannels slug grammar enforcement ─────────────────────────

// TestHandleChannels_BadSlug_Returns400_NothingWritten verifies that
// PUT /channels/whatsapp.BAD/<action> (uppercase slug) is rejected at the
// HandleChannels gate with 400 "malformed channel id" BEFORE any credential or
// config write, closing the boot-brick / orphaned-credential path (S-1+S-2).
//
// Uses /routing (a read-only probe for config) so nothing is written even
// before the grammar check — the assertion then proves the check fired first.
func TestHandleChannels_BadSlug_Returns400_NothingWritten(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Snapshot the raw config.json before the request.
	cfgPathBefore := api.homePath + "/config.json"
	before, err := os.ReadFile(cfgPathBefore)
	require.NoError(t, err)

	// PUT /api/v1/channels/whatsapp.BAD/routing — uppercase slug violates ADR-029
	// grammar (slugPattern requires [a-z0-9-]{1,32}).
	r := httptest.NewRequest(http.MethodPut,
		"/api/v1/channels/whatsapp.BAD/routing",
		strings.NewReader(`{"default_agent_id":"mia"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.HandleChannels(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code,
		"uppercase slug must be rejected with 400 (S-1+S-2); body=%s", w.Body.String())

	// Confirm no config write occurred (on-disk content unchanged).
	after, err := os.ReadFile(cfgPathBefore)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after),
		"config.json must not be modified when the channel id grammar check fires")
}

// ── Fix 2: bound→unbound transition clears WorkspaceID and Identity ──────────

// TestSetChannelRouting_UnbindClears_WorkspaceID_And_Identity verifies FR-029:
// a PUT without workspace_id on a previously-bound instance clears WorkspaceID
// and Identity so the next getChannelRouting returns the unbound representation.
//
// Without the fix, safeUpdateConfigJSON preserved the bound representation and
// RepairStaleChannelWildcardBindings dropped the new wildcard on next load,
// silently losing the unbind intent.
func TestSetChannelRouting_UnbindClears_WorkspaceID_And_Identity(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	addAgentsToAPI(t, api, []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "ray"},
	})
	writeTestWorkspaceJSON(t, api, "sales", "active", []string{"mia", "ray"}, false)
	seedChannelInstance(t, api, "whatsapp.eu")

	// Step 1: bind the instance.
	putW := setChannelRoutingReq(t, api, "whatsapp.eu",
		`{"workspace_id":"sales","default_agent_id":"ray"}`)
	require.Equal(t, http.StatusOK, putW.Code, "initial bind must succeed: %s", putW.Body.String())

	// Verify it is bound.
	cfg := api.agentLoop.GetConfig()
	inst, ok := cfg.Channels["whatsapp.eu"]
	require.True(t, ok, "channel entry must exist after bind")
	require.Equal(t, "sales", inst.WorkspaceID, "pre-condition: WorkspaceID must be 'sales'")
	require.NotNil(t, inst.Identity, "pre-condition: Identity must be set")

	// Step 2: unbind by sending a PUT without workspace_id.
	unbindW := setChannelRoutingReq(t, api, "whatsapp.eu",
		`{"default_agent_id":"mia"}`)
	require.Equal(t, http.StatusOK, unbindW.Code, "unbind must succeed (200): %s", unbindW.Body.String())

	// Assert WorkspaceID and Identity are cleared in the live config.
	afterCfg := api.agentLoop.GetConfig()
	afterInst, instExists := afterCfg.Channels["whatsapp.eu"]
	if instExists {
		assert.Empty(t, afterInst.WorkspaceID,
			"WorkspaceID must be cleared after unbound PUT (FR-029 mutual-exclusivity)")
		assert.Nil(t, afterInst.Identity,
			"Identity must be cleared after unbound PUT (FR-029 mutual-exclusivity)")
	}
	// If the entry is fully absent that is also a valid cleared state.

	// Assert GET returns the unbound (wildcard) representation, not the old bound one.
	getW := getChannelRoutingReq(t, api, "whatsapp.eu")
	require.Equal(t, http.StatusOK, getW.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(getW.Body.Bytes(), &resp))
	wsVal, hasWS := resp["workspace_id"]
	if hasWS && wsVal != nil && wsVal != "" {
		t.Errorf("GET after unbind must not return workspace_id; got %v", wsVal)
	}
	assert.Equal(t, "mia", resp["default_agent_id"],
		"GET after unbind must return the new wildcard agent_id")
}

// TestSetChannelRouting_Bound_RestampsExistingSessions verifies that binding
// an UNBOUND channel to a workspace re-stamps sessions that already existed
// on that channel before the bind, not only sessions created afterward.
func TestSetChannelRouting_Bound_RestampsExistingSessions(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	addAgentsToAPI(t, api, []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "ray"},
	})
	writeTestWorkspaceJSON(t, api, "sales", "active", []string{"mia", "ray"}, false)
	seedChannelInstance(t, api, "whatsapp.eu")

	// A conversation that already exists BEFORE the channel is ever bound.
	existing := seedExistingChannelSession(t, api, "whatsapp.eu", "peer-1", "mia", "")
	require.Empty(t, existing.WorkspaceID, "precondition: session starts with no workspace")

	w := setChannelRoutingReq(t, api, "whatsapp.eu",
		`{"workspace_id":"sales","default_agent_id":"ray"}`)
	require.Equal(t, http.StatusOK, w.Code, "bind must succeed: %s", w.Body.String())

	store := api.agentLoop.GetSessionStore()
	after, err := store.GetMeta(existing.ID)
	require.NoError(t, err)
	assert.Equal(t, "sales", after.WorkspaceID,
		"binding the channel to a workspace must re-stamp an already-existing session's workspace_id")
}

// TestSetChannelRouting_Rebind_UpdatesExistingSessions verifies that MOVING
// a channel from workspace W1 to W2 re-stamps sessions off W1 onto W2 —
// not just newly-bound state that only affects future sessions.
func TestSetChannelRouting_Rebind_UpdatesExistingSessions(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	addAgentsToAPI(t, api, []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "ray"},
	})
	writeTestWorkspaceJSON(t, api, "w1", "active", []string{"mia", "ray"}, false)
	writeTestWorkspaceJSON(t, api, "w2", "active", []string{"mia", "ray"}, false)
	seedChannelInstance(t, api, "whatsapp.eu")

	// Bind to w1 first.
	w := setChannelRoutingReq(t, api, "whatsapp.eu",
		`{"workspace_id":"w1","default_agent_id":"ray"}`)
	require.Equal(t, http.StatusOK, w.Code)

	// A conversation created while w1 was bound.
	existing := seedExistingChannelSession(t, api, "whatsapp.eu", "peer-2", "ray", "w1")
	require.Equal(t, "w1", existing.WorkspaceID)

	// Re-bind the SAME instance to w2.
	w2 := setChannelRoutingReq(t, api, "whatsapp.eu",
		`{"workspace_id":"w2","default_agent_id":"ray"}`)
	require.Equal(t, http.StatusOK, w2.Code, "rebind must succeed: %s", w2.Body.String())

	store := api.agentLoop.GetSessionStore()
	after, err := store.GetMeta(existing.ID)
	require.NoError(t, err)
	assert.Equal(t, "w2", after.WorkspaceID,
		"re-binding a channel to a different workspace must move existing sessions off the stale workspace")
}

// TestSetChannelRouting_Unbind_ClearsExistingSessions verifies that
// explicitly UNBINDING a previously-bound channel clears the stale
// workspace_id off existing sessions rather than leaving them pinned to a
// workspace the channel is no longer routed through.
func TestSetChannelRouting_Unbind_ClearsExistingSessions(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	addAgentsToAPI(t, api, []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "ray"},
	})
	writeTestWorkspaceJSON(t, api, "sales", "active", []string{"mia", "ray"}, false)
	seedChannelInstance(t, api, "whatsapp.eu")

	w := setChannelRoutingReq(t, api, "whatsapp.eu",
		`{"workspace_id":"sales","default_agent_id":"ray"}`)
	require.Equal(t, http.StatusOK, w.Code)

	existing := seedExistingChannelSession(t, api, "whatsapp.eu", "peer-3", "ray", "sales")
	require.Equal(t, "sales", existing.WorkspaceID)

	// Unbind: PUT with no workspace_id at all (legacy/unbound flow).
	wu := setChannelRoutingReq(t, api, "whatsapp.eu", `{"default_agent_id":"ray"}`)
	require.Equal(t, http.StatusOK, wu.Code, "unbind must succeed: %s", wu.Body.String())

	store := api.agentLoop.GetSessionStore()
	after, err := store.GetMeta(existing.ID)
	require.NoError(t, err)
	assert.Empty(t, after.WorkspaceID,
		"unbinding a channel must clear the stale workspace_id off its existing sessions")
}

// TestSetChannelRouting_Bound_UnboundChannelSessionsUnaffected verifies that
// binding one channel instance's workspace does NOT touch sessions
// belonging to a different, still-unbound channel type — the restamp must
// be scoped to the channel actually being bound.
func TestSetChannelRouting_Bound_UnboundChannelSessionsUnaffected(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	addAgentsToAPI(t, api, []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "ray"},
	})
	writeTestWorkspaceJSON(t, api, "sales", "active", []string{"mia", "ray"}, false)
	seedChannelInstance(t, api, "whatsapp.eu")
	seedChannelInstance(t, api, "telegram")

	// An existing session on telegram, which stays unbound throughout.
	telegramSession := seedExistingChannelSession(t, api, "telegram", "peer-9", "mia", "")
	require.Empty(t, telegramSession.WorkspaceID)

	w := setChannelRoutingReq(t, api, "whatsapp.eu",
		`{"workspace_id":"sales","default_agent_id":"ray"}`)
	require.Equal(t, http.StatusOK, w.Code)

	store := api.agentLoop.GetSessionStore()
	after, err := store.GetMeta(telegramSession.ID)
	require.NoError(t, err)
	assert.Empty(t, after.WorkspaceID,
		"an unrelated, never-bound channel's existing sessions must be unaffected by another channel's bind")
}

// TestSetChannelRouting_SiblingInstanceIsUntouched is the defect this fix
// exists to prevent, and the reason the session record needed an instance key.
//
// An install can hold a hundred WhatsApp numbers, each bound to its own
// (workspace, agent) pair under ADR-029. Every one of their sessions records
// Channel=="whatsapp". A restamp that matched on the bare TYPE would relabel
// all hundred when an operator re-bound one of them — inflicting on the other
// ninety-nine exactly the stale-workspace bug the restamp was written to fix,
// and silently moving their delegation trust, memory rooms and task placement
// to the wrong workspace.
//
// The first version of this fix did match by type. It only became visible
// because someone asked whether that was acceptable behaviour.
func TestSetChannelRouting_SiblingInstanceIsUntouched(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	addAgentsToAPI(t, api, []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "ray"},
	})
	writeTestWorkspaceJSON(t, api, "emea", "active", []string{"mia", "ray"}, false)
	seedChannelInstance(t, api, "whatsapp.eu")
	seedChannelInstance(t, api, "whatsapp.us")

	eu := seedExistingChannelSession(t, api, "whatsapp.eu", "peer-eu", "mia", "")
	us := seedExistingChannelSession(t, api, "whatsapp.us", "peer-us", "ray", "americas")

	w := setChannelRoutingReq(t, api, "whatsapp.eu",
		`{"workspace_id":"emea","default_agent_id":"mia"}`)
	require.Equal(t, http.StatusOK, w.Code, "bind must succeed: %s", w.Body.String())

	store := api.agentLoop.GetSessionStore()

	afterEU, err := store.GetMeta(eu.ID)
	require.NoError(t, err)
	assert.Equal(t, "emea", afterEU.WorkspaceID, "the bound instance's own session must be restamped")

	afterUS, err := store.GetMeta(us.ID)
	require.NoError(t, err)
	assert.Equal(t, "americas", afterUS.WorkspaceID,
		"a SIBLING instance of the same channel type must be left completely alone — "+
			"matching on the bare type would have moved this conversation to the wrong workspace")
}

// TestConfigureChannel_RoutesSecretToCredentialStore is the core #289 guard:
// configuring a token-based channel via the UI must store the secret in the
// encrypted credential store and persist only its <field>_ref in config.json —
// never the plaintext (SEC-23) — and "Test" must then report success.
func TestConfigureChannel_RoutesSecretToCredentialStore(t *testing.T) {
	api := newChannelTestAPI(t, `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{}}`)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/telegram/configure",
		strings.NewReader(`{"token":"super-secret-123"}`))
	r.Header.Set("Content-Type", "application/json")
	api.configureChannel(w, r, "telegram")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	// 1. The plaintext secret must NOT appear anywhere in config.json (SEC-23).
	raw, err := os.ReadFile(api.homePath + "/config.json")
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "super-secret-123", "plaintext token leaked into config.json")

	// 2. token_ref must be set to the conventional credential name; no inline token.
	var diskCfg map[string]any
	require.NoError(t, json.Unmarshal(raw, &diskCfg))
	channelsMap, channelsMapOk := diskCfg["channels"].(map[string]any)
	require.True(t, channelsMapOk, "config.channels must be an object")
	tg, tgOk := channelsMap["telegram"].(map[string]any)
	require.True(t, tgOk, "config.channels.telegram must be an object")
	assert.Equal(t, "channel_telegram_token", tg["token_ref"])
	_, hasInline := tg["token"]
	assert.False(t, hasInline, "inline token must not be persisted to config.json")

	// 3. The secret must be retrievable from the credential store.
	got, err := api.credStore.Get("channel_telegram_token")
	require.NoError(t, err)
	assert.Equal(t, "super-secret-123", got)

	// 4. testChannel now reports success because the ref resolves.
	w2 := httptest.NewRecorder()
	api.testChannel(w2, "telegram")
	require.Equal(t, http.StatusOK, w2.Code)
	var resp gen.ChannelTestResponse
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp))
	assert.True(t, resp.Success, "test should pass once the credential is stored: %s", resp.Message)
}

// TestConfigureChannel_MultiSecretChannel covers a channel with two secret
// fields (slack: bot_token + app_token) — both must be routed to refs.
func TestConfigureChannel_MultiSecretChannel(t *testing.T) {
	api := newChannelTestAPI(t, `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{}}`)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/slack/configure",
		strings.NewReader(`{"bot_token":"xoxb-aaa","app_token":"xapp-bbb"}`))
	r.Header.Set("Content-Type", "application/json")
	api.configureChannel(w, r, "slack")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	raw, err := os.ReadFile(api.homePath + "/config.json")
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "xoxb-aaa")
	assert.NotContains(t, string(raw), "xapp-bbb")

	for field, want := range map[string]string{"bot_token": "xoxb-aaa", "app_token": "xapp-bbb"} {
		got, err := api.credStore.Get("channel_slack_" + field)
		require.NoError(t, err, "field %s", field)
		assert.Equal(t, want, got, "field %s", field)
	}
}

// TestTestChannel_FailsWhenSecretMissing confirms "Test" reports failure (not a
// false success) when a required secret has no stored credential — the exact
// lie #289 set out to fix.
func TestTestChannel_FailsWhenSecretMissing(t *testing.T) {
	api := newChannelTestAPI(t,
		`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{"telegram":{"enabled":false}}}`)

	w := httptest.NewRecorder()
	api.testChannel(w, "telegram")
	require.Equal(t, http.StatusOK, w.Code)
	var resp gen.ChannelTestResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp.Success, "test must fail when the token credential is absent")
	assert.Contains(t, resp.Message, "token")
}

// TestConfigureChannel_ScrubsStaleInlinePlaintext is the key security-remediation
// guard: a config left with an inline plaintext secret by the pre-#289 bug must
// be scrubbed on the next save — even when the user edits an UNRELATED field and
// does not re-supply the secret (the realistic "[configured]" UI flow).
//
// The fixture also carries a legitimate token_ref (with its credential
// pre-stored) alongside the stale inline plaintext: since Stage 1 of the
// channel-Test redesign rejects an incomplete save, the config here must be
// genuinely complete (required "token" satisfied via token_ref) for the save
// to succeed — the scrub assertions below are otherwise unchanged.
func TestConfigureChannel_ScrubsStaleInlinePlaintext(t *testing.T) {
	api := newChannelTestAPI(
		t,
		`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{"telegram":{"enabled":false,"token":"LEAKED-OLD-PLAINTEXT","token_ref":"channel_telegram_token","parse_mode":"Markdown"}}}`,
	)
	_, err := api.storeCredential("channel_telegram_token", "legit-secret")
	require.NoError(t, err)

	// Edit only a non-secret field; do NOT re-send the token.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/telegram/configure",
		strings.NewReader(`{"parse_mode":"HTML"}`))
	r.Header.Set("Content-Type", "application/json")
	api.configureChannel(w, r, "telegram")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	raw, err := os.ReadFile(api.homePath + "/config.json")
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "LEAKED-OLD-PLAINTEXT", "stale inline plaintext survived the save (SEC-23)")

	var diskCfg map[string]any
	require.NoError(t, json.Unmarshal(raw, &diskCfg))
	channelsMap, channelsMapOk := diskCfg["channels"].(map[string]any)
	require.True(t, channelsMapOk, "config.channels must be an object")
	tg, tgOk := channelsMap["telegram"].(map[string]any)
	require.True(t, tgOk, "config.channels.telegram must be an object")
	_, hasInline := tg["token"]
	assert.False(t, hasInline, "stale inline token key must be removed")
	assert.Equal(t, "HTML", tg["parse_mode"], "the unrelated edit must still apply")
}

// TestConfigureChannel_ClearSecretDeletesCredential covers the clear/rotate path:
// re-configuring with an empty value clears the ref AND deletes the stored
// credential so a "removed" secret does not linger encrypted at rest.
//
// Clears a NON-required sensitive field — matrix's crypto_passphrase
// (channelSensitiveFields["matrix"] includes it, channelRequiredFields does
// not) — because Stage 1 of the channel-Test redesign now rejects a save that
// would clear a REQUIRED secret (see
// TestConfigureChannel_RejectsClearingRequiredSecret for that path).
func TestConfigureChannel_ClearSecretDeletesCredential(t *testing.T) {
	api := newChannelTestAPI(t, `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{}}`)

	// Configure matrix fully (all three required fields) plus the optional
	// crypto_passphrase, then clear only crypto_passphrase.
	for _, body := range []string{
		`{"homeserver":"https://m.org","user_id":"@a:m.org","access_token":"syt-token","crypto_passphrase":"pp-1"}`,
		`{"crypto_passphrase":"   "}`,
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/matrix/configure", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		api.configureChannel(w, r, "matrix")
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	}

	// Ref cleared in config; credential gone from the store.
	raw, err := os.ReadFile(api.homePath + "/config.json")
	require.NoError(t, err)
	var diskCfg map[string]any
	require.NoError(t, json.Unmarshal(raw, &diskCfg))
	channelsMap, channelsMapOk := diskCfg["channels"].(map[string]any)
	require.True(t, channelsMapOk, "config.channels must be an object")
	mx, mxOk := channelsMap["matrix"].(map[string]any)
	require.True(t, mxOk, "config.channels.matrix must be an object")
	assert.Equal(t, "", mx["crypto_passphrase_ref"], "crypto_passphrase_ref must be cleared")
	_, err = api.credStore.Get("channel_matrix_crypto_passphrase")
	assert.Error(t, err, "the stored credential must be deleted on clear")

	// Test still reports success: crypto_passphrase is not required, and
	// homeserver/user_id/access_token remain configured.
	w := httptest.NewRecorder()
	api.testChannel(w, "matrix")
	var resp gen.ChannelTestResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(
		t,
		resp.Success,
		"matrix must still pass Test after clearing the non-required crypto_passphrase: %s",
		resp.Message,
	)
}

// TestConfigureChannel_RejectsClearingRequiredSecret guards Stage 1 of the
// channel-Test redesign: clearing a REQUIRED secret field must be rejected —
// it would leave the persisted config unable to construct the channel — and
// the rejection must be a true no-op: the existing credential is left
// resolvable, unchanged, in the store.
func TestConfigureChannel_RejectsClearingRequiredSecret(t *testing.T) {
	api := newChannelTestAPI(t, `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{}}`)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/telegram/configure",
		strings.NewReader(`{"token":"secret-1"}`))
	r.Header.Set("Content-Type", "application/json")
	api.configureChannel(w, r, "telegram")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodPut, "/api/v1/channels/telegram/configure",
		strings.NewReader(`{"token":"   "}`))
	r2.Header.Set("Content-Type", "application/json")
	api.configureChannel(w2, r2, "telegram")
	assert.Equal(
		t,
		http.StatusBadRequest,
		w2.Code,
		"clearing a required secret must be rejected, body=%s",
		w2.Body.String(),
	)
	assert.Contains(t, w2.Body.String(), "token")

	// Zero-side-effect proof: the existing credential still resolves unchanged.
	got, err := api.credStore.Get("channel_telegram_token")
	require.NoError(t, err)
	assert.Equal(t, "secret-1", got, "a rejected clear must not touch the existing credential")
}

// TestTestChannel_MatrixMixedRequiredFields locks in the inline-vs-ref
// discrimination: matrix requires homeserver + user_id (non-secret, inline) AND
// access_token (secret, ref). Both kinds must be validated in their own way.
func TestTestChannel_MatrixMixedRequiredFields(t *testing.T) {
	// access_token routed to a stored ref; homeserver + user_id inline.
	api := newChannelTestAPI(
		t,
		`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{"matrix":{"enabled":false,"homeserver":"https://m.org","user_id":"@a:m.org","access_token_ref":"channel_matrix_access_token"}}}`,
	)
	_, err := api.storeCredential("channel_matrix_access_token", "syt-token")
	require.NoError(t, err)

	w := httptest.NewRecorder()
	api.testChannel(w, "matrix")
	var resp gen.ChannelTestResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Success, "all required fields present (inline + ref): %s", resp.Message)

	// Drop the non-secret user_id → Test must fail on the inline field.
	api2 := newChannelTestAPI(
		t,
		`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{"matrix":{"enabled":false,"homeserver":"https://m.org","access_token_ref":"channel_matrix_access_token"}}}`,
	)
	_, err = api2.storeCredential("channel_matrix_access_token", "syt-token")
	require.NoError(t, err)
	w2 := httptest.NewRecorder()
	api2.testChannel(w2, "matrix")
	var resp2 gen.ChannelTestResponse
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp2))
	assert.False(t, resp2.Success)
	assert.Contains(t, resp2.Message, "user_id")
}

// TestGetChannelConfig_ShowsConfiguredMarker guards MAJ-001: after the secret
// moves to the credential store (only token_ref in config), GET must still show
// the "[configured]" marker so the UI knows a secret is set — never the secret.
func TestGetChannelConfig_ShowsConfiguredMarker(t *testing.T) {
	api := newChannelTestAPI(t, `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{}}`)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/telegram/configure",
		strings.NewReader(`{"token":"super-secret-123"}`))
	r.Header.Set("Content-Type", "application/json")
	api.configureChannel(w, r, "telegram")
	require.Equal(t, http.StatusOK, w.Code)

	w2 := httptest.NewRecorder()
	api.getChannelConfig(w2, "telegram")
	require.Equal(t, http.StatusOK, w2.Code)
	var got map[string]any
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &got))
	assert.Equal(t, "[configured]", got["token"], "GET must show the configured marker via the ref")
	assert.NotContains(t, w2.Body.String(), "super-secret-123", "GET must never echo the secret")
}

// TestConfigureChannel_RejectsNonStringSecret guards MIN-002: a non-string
// secret value must be rejected, not silently treated as a clear (which would
// revoke a working credential).
func TestConfigureChannel_RejectsNonStringSecret(t *testing.T) {
	api := newChannelTestAPI(t, `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{}}`)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/telegram/configure",
		strings.NewReader(`{"token":12345}`))
	r.Header.Set("Content-Type", "application/json")
	api.configureChannel(w, r, "telegram")
	assert.Equal(t, http.StatusBadRequest, w.Code, "non-string secret must be rejected")
}

// TestConfigureChannel_StoreFaultFailsClosed guards MIN-001: when the credential
// store is unavailable (locked), configure must fail with 500 and NOT modify
// config.json — no ref written, no plaintext written (fail-closed).
func TestConfigureChannel_StoreFaultFailsClosed(t *testing.T) {
	api := newLockedStoreChannelAPI(t, `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{}}`)
	before, err := os.ReadFile(api.homePath + "/config.json")
	require.NoError(t, err)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/telegram/configure",
		strings.NewReader(`{"token":"secret-x"}`))
	r.Header.Set("Content-Type", "application/json")
	api.configureChannel(w, r, "telegram")
	assert.Equal(t, http.StatusInternalServerError, w.Code, "store fault must fail the request")

	after, err := os.ReadFile(api.homePath + "/config.json")
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "config.json must be unchanged on store fault (fail-closed)")
	assert.NotContains(t, string(after), "secret-x")
}

// TestTestChannel_StoreUnavailableIsDistinct guards MIN-001 + the (bool,error)
// contract: a locked store must report "credential store unavailable", NOT a
// misleading "missing token", so the user isn't told to re-enter a present secret.
func TestTestChannel_StoreUnavailableIsDistinct(t *testing.T) {
	api := newLockedStoreChannelAPI(
		t,
		`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{"telegram":{"enabled":false,"token_ref":"channel_telegram_token"}}}`,
	)

	w := httptest.NewRecorder()
	api.testChannel(w, "telegram")
	var resp gen.ChannelTestResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp.Success)
	assert.Contains(t, resp.Message, "unavailable", "store fault must be reported distinctly, not as 'missing'")
	assert.NotContains(t, resp.Message, "missing")
}

// TestConfigureChannel_GoogleChatWebhookRoutesToCredentialStore is the MAJ-1
// guard (Epic #314): a Google Chat incoming-webhook URL is a possession-based
// bearer secret (SEC-23 class). Configuring it via the UI must route it into the
// encrypted credential store and persist only webhook_url_ref — never the
// plaintext URL — and "Test" must then report the secret as resolvable.
func TestConfigureChannel_GoogleChatWebhookRoutesToCredentialStore(t *testing.T) {
	api := newChannelTestAPI(t, `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{}}`)

	const secretURL = "https://chat.googleapis.com/v1/spaces/AAA/messages?key=KKK&token=TTT"
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/google-chat/configure",
		strings.NewReader(`{"mode":"webhook","webhook_url":"`+secretURL+`"}`))
	r.Header.Set("Content-Type", "application/json")
	api.configureChannel(w, r, "google-chat")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	// 1. The plaintext webhook URL must NOT appear anywhere in config.json (SEC-23).
	raw, err := os.ReadFile(api.homePath + "/config.json")
	require.NoError(t, err)
	assert.NotContains(t, string(raw), secretURL, "plaintext webhook_url leaked into config.json")
	assert.NotContains(t, string(raw), "token=TTT", "webhook secret query param leaked into config.json")

	// 2. webhook_url_ref must be set; no inline webhook_url.
	var diskCfg map[string]any
	require.NoError(t, json.Unmarshal(raw, &diskCfg))
	channelsMap, channelsMapOk := diskCfg["channels"].(map[string]any)
	require.True(t, channelsMapOk, "config.channels must be an object")
	gc, gcOk := channelsMap["google-chat"].(map[string]any)
	require.True(t, gcOk, "config.channels.google-chat must be an object")
	assert.Equal(t, "channel_google-chat_webhook_url", gc["webhook_url_ref"])
	_, hasInline := gc["webhook_url"]
	assert.False(t, hasInline, "inline webhook_url must not be persisted to config.json")

	// 3. The secret must be retrievable from the credential store.
	got, err := api.credStore.Get("channel_google-chat_webhook_url")
	require.NoError(t, err)
	assert.Equal(t, secretURL, got)

	// 4. credentialRefResolves (the Test path) must report the webhook ref as resolvable.
	ok, err := api.credentialRefResolves("channel_google-chat_webhook_url")
	require.NoError(t, err)
	assert.True(t, ok, "Test must see the stored webhook credential as resolvable")
}

// TestConfigureChannel_GoogleChatServiceAccountRoutesToCredentialStore covers the
// service-account-JSON secret path, which was effectively broken before MAJ-1:
// configureChannel wrote service_account_json_ref, but GoogleChatConfig had no
// *Ref struct field and ResolveAll did not list it, so the secret was dropped.
func TestConfigureChannel_GoogleChatServiceAccountRoutesToCredentialStore(t *testing.T) {
	api := newChannelTestAPI(t, `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{}}`)

	const saJSON = `{"client_email":"bot@proj.iam.gserviceaccount.com","private_key":"-----BEGIN PRIVATE KEY-----\nSECRETKEYMATERIAL\n-----END PRIVATE KEY-----\n"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/google-chat/configure",
		strings.NewReader(`{"mode":"bot","service_account_json":`+mustJSONString(t, saJSON)+`}`))
	r.Header.Set("Content-Type", "application/json")
	api.configureChannel(w, r, "google-chat")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	raw, err := os.ReadFile(api.homePath + "/config.json")
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "SECRETKEYMATERIAL", "plaintext service account key leaked into config.json")

	var diskCfg map[string]any
	require.NoError(t, json.Unmarshal(raw, &diskCfg))
	channelsMap, channelsMapOk := diskCfg["channels"].(map[string]any)
	require.True(t, channelsMapOk, "config.channels must be an object")
	gc, gcOk := channelsMap["google-chat"].(map[string]any)
	require.True(t, gcOk, "config.channels.google-chat must be an object")
	assert.Equal(t, "channel_google-chat_service_account_json", gc["service_account_json_ref"])
	_, hasInline := gc["service_account_json"]
	assert.False(t, hasInline, "inline service_account_json must not be persisted to config.json")

	got, err := api.credStore.Get("channel_google-chat_service_account_json")
	require.NoError(t, err)
	assert.Equal(t, saJSON, got)
}

// TestTestChannel_SlackRequiresAppToken guards the channelRequiredFields fix:
// the slack constructor (pkg/channels/slack/slack.go NewSlackChannel) hard
// requires BOTH bot_token and app_token, so "Test" reporting success with only
// bot_token configured was a lie — an operator would enable slack and it would
// fail to construct at boot. Both refs must resolve for success.
//
// Both fields are supplied in a single configure call because Stage 1 of the
// channel-Test redesign now rejects a save that would leave a
// multi-field-required channel partially configured — see
// TestConfigureChannel_RejectsPartialMultiFieldRequired for that rejection.
func TestTestChannel_SlackRequiresAppToken(t *testing.T) {
	api := newChannelTestAPI(t, `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{}}`)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/slack/configure",
		strings.NewReader(`{"bot_token":"xoxb-aaa","app_token":"xapp-bbb"}`))
	r.Header.Set("Content-Type", "application/json")
	api.configureChannel(w, r, "slack")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	w2 := httptest.NewRecorder()
	api.testChannel(w2, "slack")
	require.Equal(t, http.StatusOK, w2.Code)
	var resp gen.ChannelTestResponse
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp))
	assert.True(
		t,
		resp.Success,
		"slack must pass Test once both bot_token and app_token are configured: %s",
		resp.Message,
	)
}

// TestConfigureChannel_RejectsPartialMultiFieldRequired guards Stage 1 of the
// channel-Test redesign: slack requires BOTH bot_token and app_token
// (channelRequiredFields["slack"]); configuring only one must be rejected
// before either the config entry or the credential reaches disk.
func TestConfigureChannel_RejectsPartialMultiFieldRequired(t *testing.T) {
	api := newChannelTestAPI(t, `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{}}`)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/slack/configure",
		strings.NewReader(`{"bot_token":"xoxb-aaa"}`))
	r.Header.Set("Content-Type", "application/json")
	api.configureChannel(w, r, "slack")
	assert.Equal(
		t,
		http.StatusBadRequest,
		w.Code,
		"a partial multi-field-required save must be rejected, body=%s",
		w.Body.String(),
	)
	assert.Contains(t, w.Body.String(), "app_token")

	// Nothing persisted: no slack config entry, no bot_token credential.
	raw, err := os.ReadFile(api.homePath + "/config.json")
	require.NoError(t, err)
	var diskCfg map[string]any
	require.NoError(t, json.Unmarshal(raw, &diskCfg))
	channels, _ := diskCfg["channels"].(map[string]any)
	_, hasSlack := channels["slack"]
	assert.False(t, hasSlack, "a rejected partial save must persist nothing")

	_, err = api.credStore.Get("channel_slack_bot_token")
	assert.Error(t, err, "a rejected partial save must not store the bot_token credential")
}

// TestConfigureChannel_GoogleChatEitherOr guards Stage 1 of the channel-Test
// redesign applied to Google Chat's either/or auth-path requirement (mirrors
// testChannel's special case below): a save naming neither webhook_url nor a
// service-account field must be rejected, naming both alternatives in the
// message; adding webhook_url then satisfies the requirement.
func TestConfigureChannel_GoogleChatEitherOr(t *testing.T) {
	api := newChannelTestAPI(t, `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{}}`)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/google-chat/configure",
		strings.NewReader(`{"space":"spaces/AAA"}`))
	r.Header.Set("Content-Type", "application/json")
	api.configureChannel(w, r, "google-chat")
	assert.Equal(
		t,
		http.StatusBadRequest,
		w.Code,
		"gchat save with neither auth path must be rejected, body=%s",
		w.Body.String(),
	)
	assert.Contains(t, w.Body.String(), "webhook_url")
	assert.Contains(t, w.Body.String(), "service_account")

	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/channels/google-chat/configure",
		strings.NewReader(
			`{"space":"spaces/AAA","webhook_url":"https://chat.googleapis.com/v1/spaces/AAA/messages?key=K&token=T"}`,
		),
	)
	r2.Header.Set("Content-Type", "application/json")
	api.configureChannel(w2, r2, "google-chat")
	assert.Equal(
		t,
		http.StatusOK,
		w2.Code,
		"adding webhook_url must satisfy the auth-path requirement, body=%s",
		w2.Body.String(),
	)
}

// TestTestChannel_GoogleChatRequiresAuthPath guards the special-case fix in
// testChannel: channelRequiredFields["google-chat"] is deliberately {} because
// the real requirement is either/or (webhook_url OR service_account_json OR
// service_account_file), which the flat AND-list can't express. Before this
// fix "Test" reported success on a completely blank google-chat instance.
func TestTestChannel_GoogleChatRequiresAuthPath(t *testing.T) {
	// A blank instance (no auth path configured at all) must fail with a clear
	// message, not silently report success.
	blank := newChannelTestAPI(
		t,
		`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{"google-chat":{"enabled":false}}}`,
	)
	w := httptest.NewRecorder()
	blank.testChannel(w, "google-chat")
	require.Equal(t, http.StatusOK, w.Code)
	var resp gen.ChannelTestResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp.Success, "a blank google-chat instance must fail Test")
	assert.Contains(t, resp.Message, "webhook_url")
	assert.Contains(t, resp.Message, "service_account")

	// webhook_url configured (via credential store, ref-routed) satisfies the
	// requirement on its own.
	webhookAPI := newChannelTestAPI(t, `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{}}`)
	wc := httptest.NewRecorder()
	rc := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/channels/google-chat/configure",
		strings.NewReader(
			`{"mode":"webhook","webhook_url":"https://chat.googleapis.com/v1/spaces/AAA/messages?key=K&token=T"}`,
		),
	)
	rc.Header.Set("Content-Type", "application/json")
	webhookAPI.configureChannel(wc, rc, "google-chat")
	require.Equal(t, http.StatusOK, wc.Code, "body=%s", wc.Body.String())
	w2 := httptest.NewRecorder()
	webhookAPI.testChannel(w2, "google-chat")
	var resp2 gen.ChannelTestResponse
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp2))
	assert.True(t, resp2.Success, "webhook_url alone must satisfy the auth-path requirement: %s", resp2.Message)

	// service_account_file (a non-secret, inline path field — not credential
	// routed) also satisfies the requirement on its own.
	saFileAPI := newChannelTestAPI(
		t,
		`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{"google-chat":{"enabled":false,"mode":"bot","service_account_file":"/etc/omnipus/gchat-sa.json"}}}`,
	)
	w3 := httptest.NewRecorder()
	saFileAPI.testChannel(w3, "google-chat")
	var resp3 gen.ChannelTestResponse
	require.NoError(t, json.Unmarshal(w3.Body.Bytes(), &resp3))
	assert.True(
		t,
		resp3.Success,
		"service_account_file alone must satisfy the auth-path requirement: %s",
		resp3.Message,
	)
}

// TestSetChannelEnabled_RejectsIncompleteConfig guards Stage 1 of the
// channel-Test redesign: enabling a channel whose persisted config is
// incomplete must be rejected — an enabled-but-incomplete channel would fail
// to construct on the next reload/boot. Completing the config then allows
// enable to succeed. Disabling never validates (s1_whatsapp_enable_reload_test.go
// exercises that path; whatsapp's empty required-fields list also always
// passes here regardless).
func TestSetChannelEnabled_RejectsIncompleteConfig(t *testing.T) {
	api := newChannelTestAPI(
		t,
		`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{"telegram":{"enabled":false}}}`,
	)

	w := httptest.NewRecorder()
	api.setChannelEnabled(w, "telegram", true)
	assert.Equal(
		t,
		http.StatusBadRequest,
		w.Code,
		"enabling an incomplete telegram config must be rejected, body=%s",
		w.Body.String(),
	)
	assert.Contains(t, w.Body.String(), "token")

	// Complete the config, then enable must succeed.
	wc := httptest.NewRecorder()
	rc := httptest.NewRequest(http.MethodPut, "/api/v1/channels/telegram/configure",
		strings.NewReader(`{"token":"secret-1"}`))
	rc.Header.Set("Content-Type", "application/json")
	api.configureChannel(wc, rc, "telegram")
	require.Equal(t, http.StatusOK, wc.Code, "body=%s", wc.Body.String())

	w2 := httptest.NewRecorder()
	api.setChannelEnabled(w2, "telegram", true)
	assert.Equal(t, http.StatusOK, w2.Code, "enabling a complete config must succeed, body=%s", w2.Body.String())
}

// TestHandleChannelsGET verifies GET /api/v1/channels returns 200 with an array.
// Traces to: wave5b-system-agent-spec.md — E4: channels endpoint
func TestHandleChannelsGET(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/channels", nil)
	api.HandleChannels(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var channels []any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &channels))
	assert.NotEmpty(t, channels, "channels must include at least webchat")
}

// TestSetChannelRouting_RejectsWorkerTarget verifies M1/MIN-002: PUT
// /api/v1/channels/{id}/routing targeting a worker agent is rejected with 422 —
// a worker is not a chat target and cannot be a channel's default agent. A
// control PUT targeting a base agent must still succeed (200).
//
// BDD: Given a base agent and a worker agent,
//
//	When PUT /api/v1/channels/telegram/routing with {"default_agent_id": "<worker>"},
//	Then the request fails with 422 (MIN-002, standardized from 400) and the error mentions workers/chat targets;
//	And a control PUT with the base agent returns 200.
func TestSetChannelRouting_RejectsWorkerTarget(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	cfgPath := tmpDir + "/config.json"

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 20,
			},
			List: []config.AgentConfig{
				{ID: "mia", Name: "Mia", Type: config.AgentTypeCore, Default: true},
				{ID: "worker", Name: "Worker", Type: config.AgentTypeWorker},
			},
		},
	}
	require.NoError(t, os.WriteFile(cfgPath, marshalConfigForDisk(t, cfg), 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}
	seedRoutingAgentEntities(t, tmpDir, cfg.Agents.List)

	// Worker target → 400.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/channels/telegram/routing",
		strings.NewReader(`{"default_agent_id": "worker"}`),
	)
	api.HandleChannels(w, r)
	// MIN-002: worker rejection standardized to 422 (was 400).
	require.Equal(t, http.StatusUnprocessableEntity, w.Code,
		"a worker target for channel routing must be rejected with 422 (MIN-002)")
	assert.Contains(t, strings.ToLower(w.Body.String()), "worker",
		"the error must explain a worker cannot be a channel's default agent")

	// Confirm no binding was persisted for telegram.
	liveCfg := api.agentLoop.GetConfig()
	assert.Less(t, channelWildcardIdx(liveCfg.Bindings, "telegram"), 0,
		"rejected worker target must not persist a binding")

	// Control: base agent target → 200.
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/channels/telegram/routing",
		strings.NewReader(`{"default_agent_id": "mia"}`),
	)
	api.HandleChannels(w2, r2)
	require.Equal(t, http.StatusOK, w2.Code, "a base agent target must succeed (control)")
	var resp gen.ChannelRouting
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&resp))
	require.NotNil(t, resp.DefaultAgentId)
	assert.Equal(t, "mia", *resp.DefaultAgentId)
}

// --- HandleChannels tests ---

// TestHandleChannels_WhatsApp_NativeAvailable verifies that GET /api/v1/channels
// returns native_available on the whatsapp entry matching the compile-time
// whatsappnative.NativeAvailable constant.
//
// BDD:
//
//	Given a default config with WhatsApp disabled,
//	When GET /api/v1/channels is called,
//	Then the response contains a "whatsapp" entry with native_available set to
//	  the value of whatsappnative.NativeAvailable (true in default builds).
//
// Traces to: issue #299 — surface NativeAvailable in channels API.
func TestHandleChannels_WhatsApp_NativeAvailable(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/channels", nil)
	api.HandleChannels(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	var entries []struct {
		ID              string `json:"id"`
		NativeAvailable *bool  `json:"native_available"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &entries))

	var whatsapp *struct {
		ID              string `json:"id"`
		NativeAvailable *bool  `json:"native_available"`
	}
	for i := range entries {
		if entries[i].ID == "whatsapp" {
			whatsapp = &entries[i]
			break
		}
	}
	require.NotNil(t, whatsapp, "channels list must contain a 'whatsapp' entry")
	require.NotNil(t, whatsapp.NativeAvailable, "whatsapp entry must have native_available set")
	// In the default (non-lite) build, NativeAvailable is true; in the lite
	// variant the whatsappnative package stub sets it to false.  Either way,
	// the value must match the compile-time constant.
	assert.Equal(t, whatsappnative.NativeAvailable, *whatsapp.NativeAvailable,
		"native_available must equal whatsappnative.NativeAvailable compile-time constant")
}

// TestApplyDegradedOverlay_MarksDegradedEntry verifies that applyDegradedOverlay
// sets degraded=true and degraded_reason on a channel whose registry id appears
// in the failed list.
//
// BDD:
//
//	Given a channels slice containing "telegram" and "whatsapp",
//	When applyDegradedOverlay is called with a failure for "telegram",
//	Then the telegram entry has degraded=true and degraded_reason set,
//	And the whatsapp entry is unchanged.
//
// Traces to: issue #299 — degraded overlay in HandleChannels.
func TestApplyDegradedOverlay_MarksDegradedEntry(t *testing.T) {
	entries := []gen.ChannelEntry{
		{Id: "telegram", Name: "Telegram", Transport: "webhook", Enabled: true, Description: "TG"},
		{Id: "whatsapp", Name: "WhatsApp", Transport: "bridge", Enabled: false, Description: "WA"},
	}

	failed := []channels.ChannelInitError{
		{Name: "telegram", Channel: "Telegram", Err: fmt.Errorf("bot token invalid")},
	}
	applyDegradedOverlay(entries, failed)

	require.NotNil(t, entries[0].Degraded, "telegram must have degraded set")
	assert.True(t, *entries[0].Degraded, "telegram degraded must be true")
	require.NotNil(t, entries[0].DegradedReason, "telegram must have degraded_reason set")
	assert.Equal(t, "bot token invalid", *entries[0].DegradedReason)

	assert.Nil(t, entries[1].Degraded, "whatsapp must not be marked degraded")
	assert.Nil(t, entries[1].DegradedReason, "whatsapp must not have degraded_reason")
}

// TestApplyDegradedOverlay_WhatsAppNativeNormalisedToWhatsApp verifies that a
// failure recorded under the registry id "whatsapp_native" is mapped to the
// "whatsapp" channel entry (both share one list entry in the channels API).
//
// BDD:
//
//	Given a channels slice containing "whatsapp",
//	When applyDegradedOverlay is called with a failure whose Name is "whatsapp_native",
//	Then the whatsapp entry is marked degraded with the failure reason.
//
// Traces to: issue #299 — whatsapp_native → whatsapp normalisation.
func TestApplyDegradedOverlay_WhatsAppNativeNormalisedToWhatsApp(t *testing.T) {
	entries := []gen.ChannelEntry{
		{Id: "whatsapp", Name: "WhatsApp", Transport: "bridge", Enabled: true, Description: "WA"},
	}

	failed := []channels.ChannelInitError{
		{Name: "whatsapp_native", Channel: "WhatsApp Native", Err: fmt.Errorf("not compiled in lite build")},
	}
	applyDegradedOverlay(entries, failed)

	require.NotNil(t, entries[0].Degraded, "whatsapp must be marked degraded when whatsapp_native fails")
	assert.True(t, *entries[0].Degraded)
	require.NotNil(t, entries[0].DegradedReason)
	assert.Equal(t, "not compiled in lite build", *entries[0].DegradedReason)
}

// TestApplyDegradedOverlay_EmptyFailed verifies that applyDegradedOverlay is a
// no-op when the failed list is empty (nil-safety / baseline behavior).
func TestApplyDegradedOverlay_EmptyFailed(t *testing.T) {
	entries := []gen.ChannelEntry{
		{Id: "telegram", Name: "Telegram", Transport: "webhook", Enabled: true, Description: "TG"},
	}
	applyDegradedOverlay(entries, nil)
	assert.Nil(t, entries[0].Degraded, "no degraded field when failed list is empty")
	assert.Nil(t, entries[0].DegradedReason)
}

// TestApplyDegradedOverlay_MultipleSimultaneousFailures verifies that
// applyDegradedOverlay marks ALL entries when multiple channels fail at once.
//
// BDD:
//
//	Given a channels slice containing "telegram" and "whatsapp",
//	When applyDegradedOverlay is called with failures for both channels,
//	Then both entries have degraded=true and the correct degraded_reason.
//
// Traces to: pr-test-analyzer finding — #299 overlay tests.
func TestApplyDegradedOverlay_MultipleSimultaneousFailures(t *testing.T) {
	entries := []gen.ChannelEntry{
		{Id: "telegram", Name: "Telegram", Transport: "webhook", Enabled: true, Description: "TG"},
		{Id: "whatsapp", Name: "WhatsApp", Transport: "bridge", Enabled: false, Description: "WA"},
	}

	failed := []channels.ChannelInitError{
		{Name: "telegram", Channel: "Telegram", Err: fmt.Errorf("bot token invalid")},
		{Name: "whatsapp", Channel: "WhatsApp", Err: fmt.Errorf("bridge unreachable")},
	}
	applyDegradedOverlay(entries, failed)

	require.NotNil(t, entries[0].Degraded, "telegram must be marked degraded")
	assert.True(t, *entries[0].Degraded)
	require.NotNil(t, entries[0].DegradedReason)
	assert.Equal(t, "bot token invalid", *entries[0].DegradedReason)

	require.NotNil(t, entries[1].Degraded, "whatsapp must be marked degraded")
	assert.True(t, *entries[1].Degraded)
	require.NotNil(t, entries[1].DegradedReason)
	assert.Equal(t, "bridge unreachable", *entries[1].DegradedReason)
}

// TestApplyDegradedOverlay_OrphanFailedID verifies that a failed channel whose
// registry id has no matching ChannelEntry (e.g. "google-chat" or "signal",
// which may be recordable by the manager but absent from the HandleChannels
// list) does NOT panic and leaves all present entries unmarked.
//
// BDD:
//
//	Given a channels slice containing only "telegram",
//	When applyDegradedOverlay is called with a failure for "google-chat",
//	Then telegram is NOT marked degraded (the orphan is silently skipped in the
//	  pure function; HandleChannels emits a WARN log for it separately).
//
// Traces to: code-reviewer + architect finding — #299 silent-drop of unmatched failures.
func TestApplyDegradedOverlay_OrphanFailedID(t *testing.T) {
	entries := []gen.ChannelEntry{
		{Id: "telegram", Name: "Telegram", Transport: "webhook", Enabled: true, Description: "TG"},
	}

	failed := []channels.ChannelInitError{
		{Name: "google-chat", Channel: "Google Chat", Err: fmt.Errorf("service account missing")},
		{Name: "signal", Channel: "Signal", Err: fmt.Errorf("signal-cli not found")},
	}

	// Must not panic; all entries stay unmarked.
	applyDegradedOverlay(entries, failed)

	assert.Nil(t, entries[0].Degraded,
		"telegram must not be marked degraded by an orphan failure for google-chat/signal")
	assert.Nil(t, entries[0].DegradedReason)
}

// TestApplyDegradedOverlay_DegradedReasonImpliesDegradedTrue is a contract test
// asserting the invariant: for every ChannelEntry, if DegradedReason is set
// then Degraded must also be set and its value must be true.
//
// BDD:
//
//	Given applyDegradedOverlay has been called with any combination of failures,
//	When iterating the resulting channel list,
//	Then every entry satisfies: DegradedReason != nil ⇒ Degraded != nil && *Degraded == true.
//
// Traces to: type-design-analyzer finding — invariant guard for degraded fields.
func TestApplyDegradedOverlay_DegradedReasonImpliesDegradedTrue(t *testing.T) {
	entries := []gen.ChannelEntry{
		{Id: "telegram", Name: "Telegram", Transport: "webhook", Enabled: true, Description: "TG"},
		{Id: "whatsapp", Name: "WhatsApp", Transport: "bridge", Enabled: false, Description: "WA"},
		{Id: "discord", Name: "Discord", Transport: "websocket", Enabled: false, Description: "DC"},
	}

	// Mix: telegram fails, discord is healthy, whatsapp_native maps to whatsapp and fails.
	failed := []channels.ChannelInitError{
		{Name: "telegram", Channel: "Telegram", Err: fmt.Errorf("bot token invalid")},
		{Name: "whatsapp_native", Channel: "WhatsApp Native", Err: fmt.Errorf("not compiled")},
	}
	applyDegradedOverlay(entries, failed)

	for i, e := range entries {
		if e.DegradedReason != nil {
			require.NotNil(t, e.Degraded,
				"entry[%d] id=%q: DegradedReason is set but Degraded is nil — invariant violated", i, e.Id)
			assert.True(t, *e.Degraded,
				"entry[%d] id=%q: DegradedReason is set but *Degraded is false — invariant violated", i, e.Id)
		}
		// Converse sanity: if Degraded is nil, DegradedReason must also be nil.
		if e.Degraded == nil {
			assert.Nil(t, e.DegradedReason,
				"entry[%d] id=%q: Degraded is nil but DegradedReason is set — invariant violated", i, e.Id)
		}
	}
}

// TestHandleChannels_WithDegradedChannel_IntegrationPath verifies the handler-level
// path: HandleChannels → GetChannelManager() → FailedChannels() → applyDegradedOverlay.
// This exercises the REAL wiring from the HTTP handler through to the channel
// manager, not just the pure overlay helper in isolation.
//
// BDD:
//
//	Given an agent loop whose channel manager has telegram pre-seeded as failed,
//	When GET /api/v1/channels is called,
//	Then the response contains the "telegram" entry with degraded=true and
//	  degraded_reason matching the seeded error.
//
// Traces to: pr-test-analyzer finding — handler-level integration test for #299.
func TestHandleChannels_WithDegradedChannel_IntegrationPath(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	// Build a Manager with a pre-seeded telegram failure.  We use
	// channels.NewManagerForTesting which bypasses NewManager's initChannels so
	// the test does not need a real token or a live Telegram connection.
	seededErr := fmt.Errorf("bot token not resolved (token_ref=%q)", "tg-ref-test")
	mgr := channels.NewManagerForTesting([]channels.ChannelInitError{
		{Name: "telegram", Channel: "Telegram", Err: seededErr},
	})
	api.agentLoop.SetChannelManager(mgr)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/channels", nil)
	api.HandleChannels(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	var entries []struct {
		ID             string  `json:"id"`
		Degraded       *bool   `json:"degraded"`
		DegradedReason *string `json:"degraded_reason"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &entries))

	var telegram *struct {
		ID             string  `json:"id"`
		Degraded       *bool   `json:"degraded"`
		DegradedReason *string `json:"degraded_reason"`
	}
	for i := range entries {
		if entries[i].ID == "telegram" {
			telegram = &entries[i]
			break
		}
	}
	require.NotNil(t, telegram, "channels list must contain a 'telegram' entry")
	require.NotNil(t, telegram.Degraded,
		"telegram must have degraded set when manager reports it as failed")
	assert.True(t, *telegram.Degraded,
		"telegram.degraded must be true when it appears in FailedChannels()")
	require.NotNil(t, telegram.DegradedReason,
		"telegram must have degraded_reason when it appears in FailedChannels()")
	assert.Equal(t, seededErr.Error(), *telegram.DegradedReason,
		"degraded_reason must match the seeded error message")
}

// TestHandleChannels_WhatsApp_NativeAvailableAndTelegramOmitted extends
// TestHandleChannels_WhatsApp_NativeAvailable to additionally assert that a
// non-whatsapp entry (telegram) does NOT have native_available set.
//
// This verifies the "omitted elsewhere" contract: native_available is a
// whatsapp-specific field and must be absent on all other channel entries.
//
// Traces to: pr-test-analyzer finding — assert NativeAvailable omitted on
// non-whatsapp entries.
func TestHandleChannels_WhatsApp_NativeAvailableAndTelegramOmitted(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/channels", nil)
	api.HandleChannels(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	var entries []struct {
		ID              string `json:"id"`
		NativeAvailable *bool  `json:"native_available"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &entries))

	for _, e := range entries {
		if e.ID == "whatsapp" {
			// whatsapp must have native_available set.
			require.NotNil(t, e.NativeAvailable,
				"whatsapp entry must have native_available set")
			continue
		}
		assert.Nil(t, e.NativeAvailable,
			"entry %q must NOT have native_available set (whatsapp-only field)", e.ID)
	}
}

// TestSetChannelEnabled_TriggersReload is the #358 regression guard: enabling a
// channel must fire a config reload (so the channel actually starts), and the
// handler must still return 200 with the persisted flag.
//
// BDD (US-8 / AC1):
//
//	Given the WhatsApp channel is disabled
//	When PUT /api/v1/channels/whatsapp/enable is called
//	Then the config reload pipeline is triggered (ChannelManager.Reload → channel.Start)
//	And the response is 200 with enabled=true.
func TestSetChannelEnabled_TriggersReload(t *testing.T) {
	api, reloadCalls := newEnableReloadTestAPI(t, nil)

	w := httptest.NewRecorder()
	api.setChannelEnabled(w, "whatsapp", true)

	require.Equal(t, http.StatusOK, w.Code, "enable must succeed")
	assert.Equal(t, int32(1), atomic.LoadInt32(reloadCalls),
		"enabling a channel must trigger exactly one config reload (#358)")

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, true, resp["enabled"], "response must report enabled=true")
}

// TestSetChannelEnabled_ReloadFailure_Returns500 verifies the handler surfaces a
// reload failure rather than reporting a false success: the flag is persisted but
// the channel did not start, so the caller must learn the enable did not take effect.
func TestSetChannelEnabled_ReloadFailure_Returns500(t *testing.T) {
	api, reloadCalls := newEnableReloadTestAPI(t, func() error {
		return fmt.Errorf("simulated reload failure")
	})

	w := httptest.NewRecorder()
	api.setChannelEnabled(w, "whatsapp", true)

	assert.Equal(t, http.StatusInternalServerError, w.Code,
		"a reload failure on enable must surface as 500, not a false 200")
	assert.Equal(t, int32(1), atomic.LoadInt32(reloadCalls))
}
