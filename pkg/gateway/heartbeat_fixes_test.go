// Integration tests for the 7-reviewer fix wave on the workspace-heartbeat-memory
// feature (ADR-027). Covers T-G1 through T-I2 + T-Finding2.
//
// Tests are grouped by traceability:
//   - T-G1: deleteSession heartbeat guard + audit (C-1, MEDIUM-1)
//   - T-G2: workspace PUT member_config validation bounds (DS-1 rows)
//   - T-G3: workspace PUT eager-session idempotence (+ fix-wave extensions)
//   - T-I1: workspace DELETE cascades heartbeat sessions (HIGH-1)
//   - T-I3: memory settings PUT does not clobber sibling fields
//   - T-I2: boot reconcile removes legacy heartbeat jobs (no workspace segment)
//   - T-Finding2: PUT with forged session_id is not honored
//   - FIX-3: disable-path releases the standing session
//   - FIX-4a: core_team shrink releases heartbeat sessions and removes cron jobs
//   - FIX-4b: computeDesiredHeartbeats skips off-team agents
//   - FIX-pickSession: continue mode preserves SessionTypeHeartbeat on existing session

package gateway

import (
	"bufio"
	"bytes"
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
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// buildHeartbeatTestAPI creates a restAPI with an audit logger, two agents ("mia"
// and "jim"), and a wired cron service. The agent stores are registered in the
// agent loop so GetAgentStore returns a real *session.UnifiedStore for each.
//
// ADR-054 FIXTURE-VACUITY fix: this used to os.WriteFile a raw config.json
// blob with a non-empty "agents.list" array (mia/jim) alongside building the
// in-memory cfg directly. That on-disk write was already dead weight before
// ADR-054 lands here too: none of the handlers this file exercises
// (HandleWorkspaces' member-config PUT, HandleSessions) ever call
// safeUpdateConfigJSON/refreshConfigAndRewireServices — they read the
// in-memory a.agentLoop.GetConfig() and write workspace JSON directly via
// writeWorkspaceFile, never reloading config.json from disk. ADR-054 makes
// this permanent (config.LoadConfig now strips any on-disk "agents.list"
// unconditionally), so the splice is replaced with real entity records via
// agentstore.Create, per the operator's fixture-vacuity directive. The
// in-memory cfg.Agents.List construction below (required for
// mustAgentLoop/registry construction) is unchanged.
func buildHeartbeatTestAPI(t *testing.T) (*restAPI, *cron.CronService) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_HOME", tmpDir)

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
			List: []config.AgentConfig{
				{ID: "mia", Name: "Mia", Type: config.AgentTypeCustom},
				{ID: "jim", Name: "Jim", Type: config.AgentTypeCustom},
			},
		},
	}
	seedAgentEntities(t, tmpDir, cfg.Agents.List)

	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})

	// Cron service.
	cs := cron.NewCronService(filepath.Join(tmpDir, "cron.json"))
	require.NoError(t, cs.Start())
	t.Cleanup(cs.Stop)

	// Audit logger.
	auditDir := filepath.Join(tmpDir, "system")
	require.NoError(t, os.MkdirAll(auditDir, 0o700))
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: auditDir, RetentionDays: 90})
	require.NoError(t, err)
	t.Cleanup(func() { _ = logger.Close() })

	api := &restAPI{
		agentLoop: al,
		homePath:  tmpDir,
		auditor:   logger,
	}
	api.cronService.Store(cs)
	return api, cs
}

// readAuditEvents reads all JSONL audit entries from the audit log file.
func readAuditEvents(t *testing.T, auditDir string) []map[string]any {
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

// seedWorkspaceWithHeartbeat writes a workspace JSON file with a single member
// config for agentID with an enabled heartbeat. sessionID is stored as-is in
// the heartbeat's session_id field. Returns the workspace ID.
func seedWorkspaceWithHeartbeat(t *testing.T, homePath, agentID, sessionID string) string {
	t.Helper()
	wsDir := filepath.Join(homePath, "workspaces")
	require.NoError(t, os.MkdirAll(wsDir, 0o700))
	wsID := "01JXHBTESTWSID0000000001"
	ws := workspace.Workspace{
		ID:       wsID,
		Name:     "Heartbeat WS",
		Status:   "active",
		CoreTeam: []string{agentID},
		MemberConfigs: map[string]workspace.MemberConfig{
			agentID: {
				Heartbeat: &workspace.MemberHeartbeat{
					Enabled:         true,
					IntervalMinutes: 10,
					Body:            "Check tasks.",
					SessionID:       sessionID,
				},
			},
		},
		CreatedAt: "2026-01-01T00:00:00Z",
		UpdatedAt: "2026-01-01T00:00:00Z",
	}
	data, err := json.MarshalIndent(ws, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, wsID+".json"), data, 0o600))
	return wsID
}

// createHeartbeatSessionForAgent creates a heartbeat session in the agent's store
// and returns the session metadata.
func createHeartbeatSessionForAgent(t *testing.T, al interface {
	GetAgentStore(agentID string) *session.UnifiedStore
}, agentID, wsID string,
) *session.UnifiedMeta {
	t.Helper()
	store := al.GetAgentStore(agentID)
	require.NotNil(t, store, "agent store for %q must exist", agentID)
	meta, err := store.NewHeartbeatSession(wsID, agentID)
	require.NoError(t, err)
	return meta
}

// deleteSessionViaAPI calls DELETE /api/v1/sessions/{id} on the api.
func deleteSessionViaAPI(t *testing.T, api *restAPI, sessionID string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/sessions/"+sessionID, nil)
	r.URL.Path = "/api/v1/sessions/" + sessionID
	api.HandleSessions(w, r)
	return w
}

// deleteWorkspaceViaAPI calls DELETE /api/v1/workspaces/{id} on the api.
func deleteWorkspaceViaAPI(t *testing.T, api *restAPI, wsID string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/workspaces/"+wsID, nil)
	r.URL.Path = "/api/v1/workspaces/" + wsID
	api.HandleWorkspaces(w, r)
	return w
}

// putMemberConfigs calls PUT /api/v1/workspaces/{id} with the given member_configs JSON.
func putMemberConfigs(t *testing.T, api *restAPI, wsID, memberConfigsJSON string) *httptest.ResponseRecorder {
	t.Helper()
	body := fmt.Sprintf(`{"member_configs":%s}`, memberConfigsJSON)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/"+wsID, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/workspaces/" + wsID
	api.HandleWorkspaces(w, r)
	return w
}

// ---------------------------------------------------------------------------
// T-G1: DeleteSession_HeartbeatGuard
// ---------------------------------------------------------------------------

// TestDeleteSession_HeartbeatGuard verifies:
//   - DELETE on a heartbeat session whose workspace heartbeat is ENABLED → 409
//     AND an audit entry with event="session.delete.blocked" is written (C-1).
//   - DELETE on a heartbeat session whose workspace heartbeat is DISABLED → 200.
//   - DELETE on a normal chat session → 200 (regression).
func TestDeleteSession_HeartbeatGuard(t *testing.T) {
	api, _ := buildHeartbeatTestAPI(t)
	auditDir := filepath.Join(api.homePath, "system")

	const agentID = "mia"

	// ── subcase 1: protected heartbeat session ── //
	t.Run("enabled heartbeat blocks delete and emits audit", func(t *testing.T) {
		// Create the heartbeat session.
		meta := createHeartbeatSessionForAgent(t, api.agentLoop, agentID, "01JXHBTESTWSID0000000001")

		// Write workspace with enabled heartbeat pointing at this session.
		seedWorkspaceWithHeartbeat(t, api.homePath, agentID, meta.ID)

		w := deleteSessionViaAPI(t, api, meta.ID)
		assert.Equal(t, http.StatusConflict, w.Code,
			"DELETE protected heartbeat session must return 409; body=%s", w.Body.String())
		assert.Contains(t, w.Body.String(), "heartbeat",
			"409 body must mention heartbeat")

		// Flush audit log and check for the blocked event.
		require.NoError(t, api.auditor.Close())
		entries := readAuditEvents(t, auditDir)
		found := false
		for _, e := range entries {
			if e["event"] == "session.delete.blocked" {
				found = true
				assert.Equal(t, audit.DecisionDeny, e["decision"])
				details, _ := e["details"].(map[string]any)
				require.NotNil(t, details, "audit entry must have details")
				assert.Equal(t, meta.ID, details["session_id"])
				assert.Equal(t, agentID, details["agent_id"])
				assert.Equal(t, "heartbeat enabled", details["reason"])
			}
		}
		assert.True(t, found, "audit entry session.delete.blocked must be written; entries=%v", entries)
	})

	// ── subcase 2: disabled heartbeat allows delete ── //
	t.Run("disabled heartbeat allows delete", func(t *testing.T) {
		api2, _ := buildHeartbeatTestAPI(t)
		const agent2 = "mia"
		meta2 := createHeartbeatSessionForAgent(t, api2.agentLoop, agent2, "01JXHBTESTWSID0000000002")

		// Workspace with disabled heartbeat (session_id does NOT match the live one).
		wsDir2 := filepath.Join(api2.homePath, "workspaces")
		require.NoError(t, os.MkdirAll(wsDir2, 0o700))
		wsID2 := "01JXHBTESTWSID0000000002"
		ws2 := workspace.Workspace{
			ID: wsID2, Name: "WS2", Status: "active",
			CoreTeam: []string{agent2},
			MemberConfigs: map[string]workspace.MemberConfig{
				agent2: {Heartbeat: &workspace.MemberHeartbeat{
					Enabled:         false,
					IntervalMinutes: 10,
					Body:            "body",
					SessionID:       meta2.ID,
				}},
			},
			CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
		}
		data, err := json.MarshalIndent(ws2, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(wsDir2, wsID2+".json"), data, 0o600))

		w2 := deleteSessionViaAPI(t, api2, meta2.ID)
		assert.Equal(t, http.StatusOK, w2.Code,
			"DELETE session with disabled heartbeat must return 200; body=%s", w2.Body.String())
	})

	// ── subcase 3: normal chat session always deletable ── //
	t.Run("normal chat session is deletable", func(t *testing.T) {
		api3, _ := buildHeartbeatTestAPI(t)
		const agent3 = "mia"
		store3 := api3.agentLoop.GetAgentStore(agent3)
		require.NotNil(t, store3)
		chatMeta, err := store3.NewSession("chat", "webchat", agent3)
		require.NoError(t, err)

		w3 := deleteSessionViaAPI(t, api3, chatMeta.ID)
		assert.Equal(t, http.StatusOK, w3.Code,
			"DELETE normal chat session must return 200; body=%s", w3.Body.String())
	})
}

// ---------------------------------------------------------------------------
// T-G2: WorkspacePUT_MemberConfigBounds
// ---------------------------------------------------------------------------

// TestWorkspacePUT_MemberConfigBounds verifies the DS-1 validation table:
//   - valid config → 200
//   - interval = 4 (< 5) → 422
//   - unknown agent → 422
//   - body > 16 KB → 422
//   - enabled + empty body → 422
//   - worker → 422
//   - disabled + interval = 0 + empty body → 200 (M-1 fix: only gate when enabled)
//
// TestWorkspacePUT_MemberConfigBounds's fixture used to ALSO os.WriteFile a
// raw config.json blob with a non-empty "agents.list" array (mia/worker1) —
// dead weight per buildHeartbeatTestAPI's doc comment above (handleWorkspacePut
// never reloads config.json from disk). Replaced with real entity records via
// seedAgentEntities, per the operator's fixture-vacuity directive; the
// in-memory cfg.Agents.List construction is unchanged.
func TestWorkspacePUT_MemberConfigBounds(t *testing.T) {
	// Build an API with a worker agent "worker1" and main agent "mia".
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_HOME", tmpDir)

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
			List: []config.AgentConfig{
				{ID: "mia", Name: "Mia", Type: config.AgentTypeCustom},
				{ID: "worker1", Name: "Worker", Type: config.AgentTypeWorker},
			},
		},
	}
	seedAgentEntities(t, tmpDir, cfg.Agents.List)
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}

	// Create a workspace that has mia + worker1 on the team.
	wsDir := filepath.Join(tmpDir, "workspaces")
	require.NoError(t, os.MkdirAll(wsDir, 0o700))
	wsID := "01JXBOUNDTESTWSID0000001"
	ws := workspace.Workspace{
		ID: wsID, Name: "Bounds WS", Status: "active",
		CoreTeam:  []string{"mia", "worker1"},
		CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
	}
	wsData, err := json.MarshalIndent(ws, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, wsID+".json"), wsData, 0o600))

	cases := []struct {
		name         string
		memberJSON   string
		wantCode     int
		shouldPersit bool // whether the workspace should be updated on success
	}{
		{
			name:       "valid enabled config",
			memberJSON: `{"mia":{"heartbeat":{"enabled":true,"interval_minutes":10,"body":"Check tasks."}}}`,
			wantCode:   http.StatusOK,
		},
		{
			name:       "interval below 5",
			memberJSON: `{"mia":{"heartbeat":{"enabled":true,"interval_minutes":4,"body":"Check tasks."}}}`,
			wantCode:   http.StatusUnprocessableEntity,
		},
		{
			name:       "unknown agent",
			memberJSON: `{"unknown-agent":{"heartbeat":{"enabled":true,"interval_minutes":10,"body":"Check tasks."}}}`,
			wantCode:   http.StatusUnprocessableEntity,
		},
		{
			name: "body over 16KB",
			memberJSON: fmt.Sprintf(
				`{"mia":{"heartbeat":{"enabled":true,"interval_minutes":10,"body":%q}}}`,
				strings.Repeat("x", 16385),
			),
			wantCode: http.StatusUnprocessableEntity,
		},
		{
			name:       "enabled with empty body",
			memberJSON: `{"mia":{"heartbeat":{"enabled":true,"interval_minutes":10,"body":""}}}`,
			wantCode:   http.StatusUnprocessableEntity,
		},
		{
			name:       "worker agent heartbeat",
			memberJSON: `{"worker1":{"heartbeat":{"enabled":true,"interval_minutes":10,"body":"Check tasks."}}}`,
			wantCode:   http.StatusUnprocessableEntity,
		},
		{
			name:       "disabled with interval=0 and empty body is valid (M-1 fix)",
			memberJSON: `{"mia":{"heartbeat":{"enabled":false}}}`,
			wantCode:   http.StatusOK,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Re-create workspace to start clean for each case.
			require.NoError(t, os.WriteFile(filepath.Join(wsDir, wsID+".json"), wsData, 0o600))

			w := putMemberConfigs(t, api, wsID, tc.memberJSON)
			assert.Equal(t, tc.wantCode, w.Code,
				"[%s] expected %d; body=%s", tc.name, tc.wantCode, w.Body.String())

			if tc.wantCode != http.StatusOK {
				// On reject: workspace on disk must be unchanged (no partial persist).
				diskData, err := os.ReadFile(filepath.Join(wsDir, wsID+".json"))
				require.NoError(t, err)
				var diskWS workspace.Workspace
				require.NoError(t, json.Unmarshal(diskData, &diskWS))
				assert.Empty(t, diskWS.MemberConfigs,
					"[%s] rejected PUT must not persist member_configs", tc.name)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// T-G3: WorkspacePUT_EagerSession
// ---------------------------------------------------------------------------

// TestWorkspacePUT_EagerSession verifies FR-010 eager-session mechanics:
//   - enable → heartbeat session exists with the stored session_id
//   - enable again → same id reused (idempotent — no second session)
//   - disable then re-enable → new session created
func TestWorkspacePUT_EagerSession(t *testing.T) {
	api, _ := buildHeartbeatTestAPI(t)
	const agentID = "mia"

	// Create a workspace with mia on the team but no member_configs.
	wsDir := filepath.Join(api.homePath, "workspaces")
	require.NoError(t, os.MkdirAll(wsDir, 0o700))
	wsID := "01JXEAGERTESTWSID000001"
	ws := workspace.Workspace{
		ID: wsID, Name: "Eager WS", Status: "active",
		CoreTeam:  []string{agentID},
		CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
	}
	wsData, err := json.MarshalIndent(ws, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, wsID+".json"), wsData, 0o600))

	enableBody := `{"member_configs":{"mia":{"heartbeat":{"enabled":true,"interval_minutes":10,"body":"Check tasks."}}}}`

	// ── PUT 1: enable heartbeat ── //
	w1 := httptest.NewRecorder()
	r1 := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/"+wsID, strings.NewReader(enableBody))
	r1.Header.Set("Content-Type", "application/json")
	r1.URL.Path = "/api/v1/workspaces/" + wsID
	api.HandleWorkspaces(w1, r1)
	require.Equal(t, http.StatusOK, w1.Code, "PUT 1 body=%s", w1.Body.String())

	var resp1 gen.Workspace
	require.NoError(t, json.Unmarshal(w1.Body.Bytes(), &resp1))
	require.NotNil(t, resp1.MemberConfigs, "member_configs must be present in response")
	miaConfig1 := (*resp1.MemberConfigs)[agentID]
	require.NotNil(t, miaConfig1.Heartbeat, "heartbeat must be set")
	require.NotNil(t, miaConfig1.Heartbeat.SessionId, "session_id must be set after enable")
	sess1 := *miaConfig1.Heartbeat.SessionId
	assert.NotEmpty(t, sess1, "session_id must be non-empty after enable")

	// Verify the session actually exists in the SHARED session store (not the
	// legacy per-agent store — the eager-creation fix targets GetSessionStore
	// so it lands in the same store pickSession's continue-mode lookup reads
	// from; see the FIX comment in HandleWorkspaces).
	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store)
	meta1, err := store.GetMeta(sess1)
	require.NoError(t, err, "heartbeat session must exist in the shared store after enable")
	assert.Equal(t, session.SessionTypeHeartbeat, meta1.Type)
	assert.Equal(t, wsID, meta1.WorkspaceID)

	// It must NOT have been created in the legacy per-agent store — that was
	// the pre-existing defect (the session created there was invisible to
	// pickSession's GetOrCreateScheduledSession, which only checks the shared
	// store, causing a duplicate empty session to be minted on first fire).
	legacyStore := api.agentLoop.GetAgentStore(agentID)
	require.NotNil(t, legacyStore)
	_, legacyErr := legacyStore.GetMeta(sess1)
	assert.Error(t, legacyErr, "heartbeat session must not exist in the legacy per-agent store")

	// ── PUT 2: enable again → same session_id (idempotent) ── //
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/"+wsID, strings.NewReader(enableBody))
	r2.Header.Set("Content-Type", "application/json")
	r2.URL.Path = "/api/v1/workspaces/" + wsID
	api.HandleWorkspaces(w2, r2)
	require.Equal(t, http.StatusOK, w2.Code, "PUT 2 body=%s", w2.Body.String())

	var resp2 gen.Workspace
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp2))
	require.NotNil(t, resp2.MemberConfigs)
	miaConfig2 := (*resp2.MemberConfigs)[agentID]
	require.NotNil(t, miaConfig2.Heartbeat)
	require.NotNil(t, miaConfig2.Heartbeat.SessionId)
	sess2 := *miaConfig2.Heartbeat.SessionId
	assert.Equal(t, sess1, sess2, "second enable must reuse the same session_id (idempotent)")

	// ── PUT 3: disable ── //
	disableBody := `{"member_configs":{"mia":{"heartbeat":{"enabled":false,"interval_minutes":10,"body":"Check tasks."}}}}`
	w3 := httptest.NewRecorder()
	r3 := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/"+wsID, strings.NewReader(disableBody))
	r3.Header.Set("Content-Type", "application/json")
	r3.URL.Path = "/api/v1/workspaces/" + wsID
	api.HandleWorkspaces(w3, r3)
	require.Equal(t, http.StatusOK, w3.Code, "PUT 3 (disable) body=%s", w3.Body.String())

	// ── PUT 4: re-enable → new session_id ── //
	w4 := httptest.NewRecorder()
	r4 := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/"+wsID, strings.NewReader(enableBody))
	r4.Header.Set("Content-Type", "application/json")
	r4.URL.Path = "/api/v1/workspaces/" + wsID
	api.HandleWorkspaces(w4, r4)
	require.Equal(t, http.StatusOK, w4.Code, "PUT 4 (re-enable) body=%s", w4.Body.String())

	var resp4 gen.Workspace
	require.NoError(t, json.Unmarshal(w4.Body.Bytes(), &resp4))
	require.NotNil(t, resp4.MemberConfigs)
	miaConfig4 := (*resp4.MemberConfigs)[agentID]
	require.NotNil(t, miaConfig4.Heartbeat)
	require.NotNil(t, miaConfig4.Heartbeat.SessionId)
	sess4 := *miaConfig4.Heartbeat.SessionId
	assert.NotEmpty(t, sess4, "re-enable must produce a non-empty session_id")
	// The disable cleared the stored session_id, so re-enable must mint a new one.
	// (It can't be the same as sess1 because the old stored id was cleared.)
	assert.NotEqual(t, sess1, sess4, "re-enable after disable must create a new session_id")
}

// ---------------------------------------------------------------------------
// Regression: WorkspaceHeartbeat_EagerCreation_FirstFireResolvesSameSession
// ---------------------------------------------------------------------------

// TestWorkspaceHeartbeat_EagerCreation_FirstFireResolvesSameSession is the
// regression test for the pre-existing store-mismatch defect: eager creation
// (HandleWorkspaces, enable path) used to call GetAgentStore (the legacy
// per-agent store) while the heartbeat cron's continue-mode session lookup —
// pickSession in schedules.go, via GetOrCreateScheduledSession — reads
// exclusively from GetSessionStore (the shared store). Because the two are
// different UnifiedStore instances rooted at different directories for the
// same sessionKey (see loop.go's GetSessionStore/GetAgentStore doc comment),
// the eagerly-created session was invisible to the first heartbeat fire,
// which silently minted a second, empty session under the SAME id in the
// shared store while the original (holding the WorkspaceID stamp) sat
// orphaned in the per-agent store.
//
// This test drives the real eager-creation path (PUT /workspaces/{id} with
// an enabled heartbeat) and then resolves the session exactly the way
// pickSession's continue-mode branch does on first fire —
// GetSessionStore().GetOrCreateScheduledSession(sessionID, agentID), the same
// method call schedules.go makes — asserting:
//  1. the resolved session id is IDENTICAL to the eagerly-created one (no
//     new id minted);
//  2. the shared store holds exactly ONE session total (no duplicate sitting
//     alongside it);
//  3. the resolved session's Type is still SessionTypeHeartbeat, not
//     re-stamped to SessionTypeScheduled (proving GetOrCreateScheduledSession
//     found the existing session rather than creating a fresh one); and
//  4. the WorkspaceID stamped at eager-creation time survives unchanged into
//     what the fired turn would read via transcriptStore.GetMeta (loop.go's
//     ProcessScheduled FIX-1 read).
//
// It also asserts the legacy per-agent store ends up with ZERO sessions for
// the agent — the failure mode this regression guards against is a session
// silently living in the wrong store, not merely an extra one appearing.
func TestWorkspaceHeartbeat_EagerCreation_FirstFireResolvesSameSession(t *testing.T) {
	api, cs := buildHeartbeatTestAPI(t)
	const agentID = "mia"

	wsDir := filepath.Join(api.homePath, "workspaces")
	require.NoError(t, os.MkdirAll(wsDir, 0o700))
	wsID := "01JXFIRSTFIRETESTWSID0001"
	ws := workspace.Workspace{
		ID: wsID, Name: "First Fire WS", Status: "active",
		CoreTeam:  []string{agentID},
		CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
	}
	wsData, err := json.MarshalIndent(ws, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, wsID+".json"), wsData, 0o600))

	// Step 1: enable heartbeat via the real PUT handler — eager creation.
	w1 := putMemberConfigs(t, api, wsID,
		`{"mia":{"heartbeat":{"enabled":true,"interval_minutes":10,"body":"Check tasks."}}}`)
	require.Equal(t, http.StatusOK, w1.Code, "enable body=%s", w1.Body.String())
	var resp1 gen.Workspace
	require.NoError(t, json.Unmarshal(w1.Body.Bytes(), &resp1))
	eagerSessionID := *(*resp1.MemberConfigs)[agentID].Heartbeat.SessionId
	require.NotEmpty(t, eagerSessionID, "eager creation must assign a session_id")

	// The reconciler must have wired the heartbeat cron job to reference this
	// exact session id in continue mode — the same wiring heartbeat_schedule.go
	// uses in production (SessionMode: cron.SessionModeContinue, SessionID: d.sessionID).
	jobs := heartbeatJobsFor(cs)
	require.Len(t, jobs, 1, "enable must create exactly one heartbeat cron job")
	assert.Equal(t, eagerSessionID, jobs[0].SessionID,
		"heartbeat cron job must carry the eagerly-created session id")
	assert.Equal(t, cron.SessionModeContinue, jobs[0].SessionMode,
		"heartbeat jobs always run in continue mode")

	sharedStore := api.agentLoop.GetSessionStore()
	require.NotNil(t, sharedStore)

	// Step 2: simulate the FIRST heartbeat fire's session resolution —
	// exactly the call pickSession's continue-mode branch makes
	// (schedules.go: store.GetOrCreateScheduledSession(id, owner)).
	resolved, err := sharedStore.GetOrCreateScheduledSession(eagerSessionID, agentID)
	require.NoError(t, err)

	// 1. No new id minted.
	assert.Equal(t, eagerSessionID, resolved.ID,
		"first fire must resolve to the SAME session id, not mint a fresh one")

	// 2. Exactly one session exists in the shared store — no duplicate.
	allShared, err := sharedStore.ListSessions()
	require.NoError(t, err)
	assert.Len(t, allShared, 1,
		"the shared store must hold exactly one session after the first fire (no duplicate minted)")

	// 3. Type preserved (proves the existing session was found, not recreated).
	assert.Equal(t, session.SessionTypeHeartbeat, resolved.Type,
		"first fire must not re-stamp the heartbeat session to scheduled")

	// 4. WorkspaceID stamp survives to what the fired turn would read.
	assert.Equal(t, wsID, resolved.WorkspaceID,
		"the eager-creation WorkspaceID stamp must survive to the fired turn's session meta")

	// Nothing was ever created in the legacy per-agent store.
	legacyStore := api.agentLoop.GetAgentStore(agentID)
	require.NotNil(t, legacyStore)
	legacySessions, err := legacyStore.ListSessions()
	require.NoError(t, err)
	assert.Empty(t, legacySessions,
		"no heartbeat session should ever land in the legacy per-agent store post-fix")
}

// ---------------------------------------------------------------------------
// T-I1: WorkspaceDelete_Cascade
// ---------------------------------------------------------------------------

// TestWorkspaceDelete_Cascade verifies that deleting a workspace removes both
// the heartbeat cron job (existing behavior) AND the standing heartbeat session
// (HIGH-1 fix: sessions live in a session store, not the workspace dir). This
// variant seeds the session directly in the LEGACY per-agent store (bypassing
// the real eager-creation code path) to prove deleteHeartbeatSessionAnyStore's
// per-agent fallback branch: a heartbeat session provisioned before the
// eager-creation fix (which now targets the shared store) must still be
// found and released by the workspace-delete cascade. The companion test
// TestWorkspaceDelete_Cascade_SharedStore proves the primary (post-fix) path.
func TestWorkspaceDelete_Cascade(t *testing.T) {
	api, cs := buildHeartbeatTestAPI(t)
	const agentID = "mia"

	// Create the heartbeat session.
	meta := createHeartbeatSessionForAgent(t, api.agentLoop, agentID, "01JXCASCTESTWSID0000001")
	wsID := "01JXCASCTESTWSID0000001"

	// Seed the workspace on disk with the heartbeat pointing at meta.ID.
	wsDir := filepath.Join(api.homePath, "workspaces")
	require.NoError(t, os.MkdirAll(wsDir, 0o700))
	ws := workspace.Workspace{
		ID: wsID, Name: "Cascade WS", Status: "active",
		CoreTeam: []string{agentID},
		MemberConfigs: map[string]workspace.MemberConfig{
			agentID: {Heartbeat: &workspace.MemberHeartbeat{
				Enabled:         true,
				IntervalMinutes: 10,
				Body:            "Check.",
				SessionID:       meta.ID,
			}},
		},
		CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
	}
	wsData, err := json.MarshalIndent(ws, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, wsID+".json"), wsData, 0o600))

	// Seed the cron job for this workspace heartbeat.
	everyMS := int64(10) * 60_000
	enabled := true
	cronJobName := heartbeatJobName(wsID, agentID)
	_, err = cs.AddJobFull(cron.JobSpec{
		Name:      cronJobName,
		Schedule:  cron.CronSchedule{Kind: "every", EveryMS: &everyMS},
		Message:   "Check.",
		AgentID:   agentID,
		SessionID: meta.ID,
		Enabled:   &enabled,
	})
	require.NoError(t, err, "seed heartbeat cron job")
	// Stamp kind.
	for _, j := range cs.ListJobs(true) {
		if j.Name == cronJobName {
			j.Payload.Kind = heartbeatJobKind
			_ = cs.UpdateJob(&j)
		}
	}

	// Verify setup: session and cron job exist before delete.
	store := api.agentLoop.GetAgentStore(agentID)
	require.NotNil(t, store)
	_, err = store.GetMeta(meta.ID)
	require.NoError(t, err, "heartbeat session must exist before workspace delete")
	require.Len(t, heartbeatJobsFor(cs), 1, "heartbeat cron job must exist before workspace delete")

	// DELETE /api/v1/workspaces/{wsID}
	w := deleteWorkspaceViaAPI(t, api, wsID)
	require.Equal(t, http.StatusNoContent, w.Code, "DELETE workspace must return 204; body=%s", w.Body.String())

	// Assert: cron job gone.
	assert.Empty(t, heartbeatJobsFor(cs), "heartbeat cron job must be removed by cascade delete")

	// Assert: standing session gone (HIGH-1).
	_, err = store.GetMeta(meta.ID)
	assert.Error(t, err, "heartbeat session must be deleted by cascade delete (HIGH-1)")
}

// TestWorkspaceDelete_Cascade_SharedStore is the primary-path sibling of
// TestWorkspaceDelete_Cascade: it enables the heartbeat through the real PUT
// handler (so the session is eagerly created in the SHARED store, matching
// the fixed behavior) and asserts workspace-delete cascade still finds and
// releases it there — proving deleteHeartbeatSessionAnyStore's shared-store
// branch, not just its legacy fallback.
func TestWorkspaceDelete_Cascade_SharedStore(t *testing.T) {
	api, cs := buildHeartbeatTestAPI(t)
	const agentID = "mia"

	wsDir := filepath.Join(api.homePath, "workspaces")
	require.NoError(t, os.MkdirAll(wsDir, 0o700))
	wsID := "01JXCASCSHAREDTESTWSID001"
	ws := workspace.Workspace{
		ID: wsID, Name: "Cascade Shared WS", Status: "active",
		CoreTeam:  []string{agentID},
		CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
	}
	wsData, err := json.MarshalIndent(ws, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, wsID+".json"), wsData, 0o600))

	// Enable heartbeat via the real PUT handler — eager creation lands in the
	// shared store.
	w1 := putMemberConfigs(t, api, wsID,
		`{"mia":{"heartbeat":{"enabled":true,"interval_minutes":10,"body":"Check tasks."}}}`)
	require.Equal(t, http.StatusOK, w1.Code, "enable body=%s", w1.Body.String())
	var resp1 gen.Workspace
	require.NoError(t, json.Unmarshal(w1.Body.Bytes(), &resp1))
	sessID := *(*resp1.MemberConfigs)[agentID].Heartbeat.SessionId
	require.NotEmpty(t, sessID)

	sharedStore := api.agentLoop.GetSessionStore()
	require.NotNil(t, sharedStore)
	_, err = sharedStore.GetMeta(sessID)
	require.NoError(t, err, "heartbeat session must exist in the shared store before workspace delete")

	require.Len(t, heartbeatJobsFor(cs), 1,
		"heartbeat cron job must exist before workspace delete (reconciled by the enable PUT)")

	w := deleteWorkspaceViaAPI(t, api, wsID)
	require.Equal(t, http.StatusNoContent, w.Code, "DELETE workspace must return 204; body=%s", w.Body.String())

	assert.Empty(t, heartbeatJobsFor(cs), "heartbeat cron job must be removed by cascade delete")

	_, err = sharedStore.GetMeta(sessID)
	assert.Error(t, err, "heartbeat session must be deleted from the shared store by cascade delete")
}

// TestWorkspaceDelete_Cascade_DualCopy is the FIX 1 regression test: it seeds
// the SAME session ID in BOTH the legacy per-agent store and the shared
// session store — the exact population deleteHeartbeatSessionAnyStore's
// legacy fallback exists to remediate, per its own doc comment, and the
// scenario neither TestWorkspaceDelete_Cascade (legacy-only) nor
// TestWorkspaceDelete_Cascade_SharedStore (shared-only) above exercises.
//
// This dual-copy state arises in production when an install provisioned a
// heartbeat session under the OLD code (legacy per-agent store) before the
// eager-creation fix shipped: the first heartbeat fire's pickSession then
// calls GetSessionStore().GetOrCreateScheduledSession(id, owner), which does
// not find the legacy copy (a different UnifiedStore instance) and mints a
// SECOND, independent session directory under the IDENTICAL id in the shared
// store instead of reusing the legacy one. From that point the shared copy
// is the live one the workspace's session_id addresses, but the legacy copy
// still sits on disk.
//
// Before FIX 1, deleteHeartbeatSessionAnyStore tried the shared store first,
// succeeded, and returned immediately on that single success — leaving the
// legacy copy permanently orphaned. This test asserts workspace-delete
// cascade now removes BOTH copies.
func TestWorkspaceDelete_Cascade_DualCopy(t *testing.T) {
	api, cs := buildHeartbeatTestAPI(t)
	const agentID = "mia"
	const wsID = "01JXDUALCOPYTESTWSID00001"

	// Seed the LEGACY copy first (simulating the OLD eager-creation code
	// path), capturing the session id it was assigned.
	legacyMeta := createHeartbeatSessionForAgent(t, api.agentLoop, agentID, wsID)

	// Seed a SECOND, independent copy under the IDENTICAL id in the shared
	// store — exactly what pickSession's first-fire continue-mode lookup
	// does in production (schedules.go, GetOrCreateScheduledSession) when it
	// cannot find the legacy copy under that id.
	sharedStore := api.agentLoop.GetSessionStore()
	require.NotNil(t, sharedStore)
	sharedMeta, err := sharedStore.GetOrCreateScheduledSession(legacyMeta.ID, agentID)
	require.NoError(t, err)
	require.Equal(t, legacyMeta.ID, sharedMeta.ID,
		"the dual-copy setup requires an identical session id in both stores")

	// Seed the workspace on disk with the heartbeat pointing at that shared
	// (now-live) id — same id the legacy copy also uses.
	wsDir := filepath.Join(api.homePath, "workspaces")
	require.NoError(t, os.MkdirAll(wsDir, 0o700))
	ws := workspace.Workspace{
		ID: wsID, Name: "Dual Copy WS", Status: "active",
		CoreTeam: []string{agentID},
		MemberConfigs: map[string]workspace.MemberConfig{
			agentID: {Heartbeat: &workspace.MemberHeartbeat{
				Enabled:         true,
				IntervalMinutes: 10,
				Body:            "Check.",
				SessionID:       legacyMeta.ID,
			}},
		},
		CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
	}
	wsData, err := json.MarshalIndent(ws, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, wsID+".json"), wsData, 0o600))

	// Seed the cron job too, keeping this test structurally aligned with its
	// two siblings above (not itself under test here).
	everyMS := int64(10) * 60_000
	enabled := true
	cronJobName := heartbeatJobName(wsID, agentID)
	_, err = cs.AddJobFull(cron.JobSpec{
		Name:      cronJobName,
		Schedule:  cron.CronSchedule{Kind: "every", EveryMS: &everyMS},
		Message:   "Check.",
		AgentID:   agentID,
		SessionID: legacyMeta.ID,
		Enabled:   &enabled,
	})
	require.NoError(t, err, "seed heartbeat cron job")
	for _, j := range cs.ListJobs(true) {
		if j.Name == cronJobName {
			j.Payload.Kind = heartbeatJobKind
			require.NoError(t, cs.UpdateJob(&j))
		}
	}

	// Verify setup: BOTH copies exist before delete.
	legacyStore := api.agentLoop.GetAgentStore(agentID)
	require.NotNil(t, legacyStore)
	_, err = legacyStore.GetMeta(legacyMeta.ID)
	require.NoError(t, err, "legacy copy must exist before workspace delete")
	_, err = sharedStore.GetMeta(legacyMeta.ID)
	require.NoError(t, err, "shared copy must exist before workspace delete")

	// DELETE /api/v1/workspaces/{wsID}
	w := deleteWorkspaceViaAPI(t, api, wsID)
	require.Equal(t, http.StatusNoContent, w.Code, "DELETE workspace must return 204; body=%s", w.Body.String())

	// FIX 1: BOTH copies must be gone — not just the shared one.
	_, sharedErrAfter := sharedStore.GetMeta(legacyMeta.ID)
	assert.Error(t, sharedErrAfter, "shared copy must be deleted by cascade delete")
	_, legacyErrAfter := legacyStore.GetMeta(legacyMeta.ID)
	assert.Error(t, legacyErrAfter,
		"FIX 1: legacy copy must ALSO be deleted by cascade delete — the pre-fix short-circuit "+
			"returned as soon as the shared copy succeeded and left this one orphaned forever")
}

// ---------------------------------------------------------------------------
// T-I3: MemorySettingsPUT_NoSiblingClobber
// ---------------------------------------------------------------------------

// TestMemorySettingsPUT_NoSiblingClobber verifies that PUT /api/v1/settings/memory
// does not clobber sibling fields in the config that it does not own. Specifically:
// setting an unrelated agents.defaults field (model_name) before the PUT and
// verifying it survives.
func TestMemorySettingsPUT_NoSiblingClobber(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_HOME", tmpDir)

	// Write config with a custom model_name in agents.defaults.
	cfgJSON := fmt.Sprintf(
		`{"version":1,"agents":{"defaults":{"workspace":%q,"model_name":"custom-model","max_tokens":4096},"list":[]}}`,
		tmpDir,
	)
	cfgPath := filepath.Join(tmpDir, "config.json")
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfgJSON), 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "custom-model"}, MaxTokens: 4096},
		},
	}
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}

	// PUT memory settings (session_days only).
	body := `{"session_days":30}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/settings/memory", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleMemorySettings(w, r)
	require.Equal(t, http.StatusOK, w.Code, "PUT memory settings must return 200; body=%s", w.Body.String())

	// Read back the config.json and verify model_name was NOT clobbered.
	rawData, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	var rawCfg map[string]any
	require.NoError(t, json.Unmarshal(rawData, &rawCfg))

	agents, _ := rawCfg["agents"].(map[string]any)
	require.NotNil(t, agents, "agents section must survive PUT memory settings")
	defaults, _ := agents["defaults"].(map[string]any)
	require.NotNil(t, defaults, "agents.defaults must survive PUT memory settings")
	assert.Equal(t, "custom-model", defaults["model_name"],
		"model_name must not be clobbered by PUT /settings/memory")

	// Also verify storage.retention.session_days was written correctly.
	storage, _ := rawCfg["storage"].(map[string]any)
	require.NotNil(t, storage, "storage section must exist after PUT memory settings")
	retention, _ := storage["retention"].(map[string]any)
	require.NotNil(t, retention, "retention section must exist")
	assert.Equal(t, float64(30), retention["session_days"],
		"session_days must be written to storage.retention")
}

// ---------------------------------------------------------------------------
// T-I2: Boot_NoLegacyHeartbeatJobs
// ---------------------------------------------------------------------------

// TestBoot_NoLegacyHeartbeatJobs verifies that the heartbeat reconciler removes
// legacy-keyed cron jobs (name "heartbeat:<agentID>", no workspace segment) when
// they do not appear in the desired set derived from workspaces.
func TestBoot_NoLegacyHeartbeatJobs(t *testing.T) {
	cs := newReconcileCron(t)

	// Seed a legacy job keyed "heartbeat:mia" (no workspace segment).
	everyMS := int64(10) * 60_000
	enabled := true
	_, err := cs.AddJobFull(cron.JobSpec{
		Name:     "heartbeat:mia", // legacy key — no workspace segment
		Schedule: cron.CronSchedule{Kind: "every", EveryMS: &everyMS},
		Message:  "legacy heartbeat",
		AgentID:  "mia",
		Enabled:  &enabled,
	})
	require.NoError(t, err)
	// Stamp as heartbeat kind so the reconciler owns it.
	for _, j := range cs.ListJobs(true) {
		if j.Name == "heartbeat:mia" {
			j.Payload.Kind = heartbeatJobKind
			_ = cs.UpdateJob(&j)
		}
	}
	require.Len(t, heartbeatJobsFor(cs), 1, "legacy job must exist before reconcile")

	// Reconcile against an empty workspace set.
	require.NoError(t, ReconcileHeartbeatSchedules(cs, []workspace.Workspace{}, neverWorker))

	// Legacy job must be removed (it was not in the desired set).
	assert.Empty(t, heartbeatJobsFor(cs),
		"legacy heartbeat job must be removed by reconcile when not in desired set")
}

// ---------------------------------------------------------------------------
// T-Finding2: WorkspacePUT_SessionIDReadOnly
// ---------------------------------------------------------------------------

// TestWorkspacePUT_SessionIDReadOnly verifies that a client cannot overwrite the
// server-managed session_id by including it in a PUT member_configs payload.
func TestWorkspacePUT_SessionIDReadOnly(t *testing.T) {
	api, _ := buildHeartbeatTestAPI(t)
	const agentID = "mia"

	// Create a workspace with an enabled heartbeat (server will mint a real session_id).
	wsDir := filepath.Join(api.homePath, "workspaces")
	require.NoError(t, os.MkdirAll(wsDir, 0o700))
	wsID := "01JXREADONLYTESTWSID0001"
	ws := workspace.Workspace{
		ID: wsID, Name: "ReadOnly WS", Status: "active",
		CoreTeam:  []string{agentID},
		CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
	}
	wsData, err := json.MarshalIndent(ws, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, wsID+".json"), wsData, 0o600))

	// Enable heartbeat so the server mints a real session_id.
	enableBody := `{"member_configs":{"mia":{"heartbeat":{"enabled":true,"interval_minutes":10,"body":"Check tasks."}}}}`
	w1 := httptest.NewRecorder()
	r1 := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/"+wsID, strings.NewReader(enableBody))
	r1.Header.Set("Content-Type", "application/json")
	r1.URL.Path = "/api/v1/workspaces/" + wsID
	api.HandleWorkspaces(w1, r1)
	require.Equal(t, http.StatusOK, w1.Code, "enable body=%s", w1.Body.String())

	var resp1 gen.Workspace
	require.NoError(t, json.Unmarshal(w1.Body.Bytes(), &resp1))
	require.NotNil(t, resp1.MemberConfigs)
	realSessionID := *(*resp1.MemberConfigs)[agentID].Heartbeat.SessionId
	require.NotEmpty(t, realSessionID, "server must have assigned a session_id")

	// Now PUT with a forged session_id.
	const forgedID = "01JXFAKEFORGEDSESSIONID00"
	forgeBody := fmt.Sprintf(
		`{"member_configs":{"mia":{"heartbeat":{"enabled":true,"interval_minutes":10,"body":"Check tasks.","session_id":%q}}}}`,
		forgedID,
	)
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/"+wsID, strings.NewReader(forgeBody))
	r2.Header.Set("Content-Type", "application/json")
	r2.URL.Path = "/api/v1/workspaces/" + wsID
	api.HandleWorkspaces(w2, r2)
	require.Equal(t, http.StatusOK, w2.Code, "PUT with forged session_id body=%s", w2.Body.String())

	// The server must NOT have persisted the forged session_id.
	// Read back directly from disk.
	diskData, err := os.ReadFile(filepath.Join(wsDir, wsID+".json"))
	require.NoError(t, err)
	var diskWS workspace.Workspace
	require.NoError(t, json.Unmarshal(diskData, &diskWS))
	mc := diskWS.MemberConfigs[agentID]
	require.NotNil(t, mc.Heartbeat)
	assert.Equal(t, realSessionID, mc.Heartbeat.SessionID,
		"server-managed session_id must not be overwritten by client-supplied session_id")
	assert.NotEqual(t, forgedID, mc.Heartbeat.SessionID,
		"forged session_id must be ignored")
}

// ---------------------------------------------------------------------------
// FIX-3: TestWorkspacePUT_DisableReleasesSession
// ---------------------------------------------------------------------------

// TestWorkspacePUT_DisableReleasesSession verifies that when a workspace PUT
// transitions a heartbeat from enabled→disabled, the standing session created
// during enable is deleted from the agent's session store (FIX-3).
func TestWorkspacePUT_DisableReleasesSession(t *testing.T) {
	api, _ := buildHeartbeatTestAPI(t)
	const agentID = "mia"

	wsDir := filepath.Join(api.homePath, "workspaces")
	require.NoError(t, os.MkdirAll(wsDir, 0o700))
	wsID := "01JXFIXDISABLETESTWSID001"
	ws := workspace.Workspace{
		ID: wsID, Name: "Disable Test WS", Status: "active",
		CoreTeam:  []string{agentID},
		CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
	}
	wsData, err := json.MarshalIndent(ws, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, wsID+".json"), wsData, 0o600))

	// Step 1: enable heartbeat — session created.
	enableBody := `{"member_configs":{"mia":{"heartbeat":{"enabled":true,"interval_minutes":10,"body":"Check tasks."}}}}`
	w1 := httptest.NewRecorder()
	r1 := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/"+wsID, strings.NewReader(enableBody))
	r1.Header.Set("Content-Type", "application/json")
	r1.URL.Path = "/api/v1/workspaces/" + wsID
	api.HandleWorkspaces(w1, r1)
	require.Equal(t, http.StatusOK, w1.Code, "enable body=%s", w1.Body.String())

	var resp1 gen.Workspace
	require.NoError(t, json.Unmarshal(w1.Body.Bytes(), &resp1))
	require.NotNil(t, resp1.MemberConfigs)
	miaConfig1 := (*resp1.MemberConfigs)[agentID]
	require.NotNil(t, miaConfig1.Heartbeat)
	require.NotNil(t, miaConfig1.Heartbeat.SessionId)
	sess1 := *miaConfig1.Heartbeat.SessionId
	require.NotEmpty(t, sess1, "session_id must be set after enable")

	// Verify the session exists — in the SHARED store (eager creation targets
	// GetSessionStore(), see the FIX comment in HandleWorkspaces).
	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store)
	_, err = store.GetMeta(sess1)
	require.NoError(t, err, "heartbeat session must exist after enable")

	// Step 2: disable heartbeat — session must be released.
	disableBody := `{"member_configs":{"mia":{"heartbeat":{"enabled":false,"interval_minutes":10,"body":"Check tasks."}}}}`
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/"+wsID, strings.NewReader(disableBody))
	r2.Header.Set("Content-Type", "application/json")
	r2.URL.Path = "/api/v1/workspaces/" + wsID
	api.HandleWorkspaces(w2, r2)
	require.Equal(t, http.StatusOK, w2.Code, "disable body=%s", w2.Body.String())

	// The standing session must now be deleted.
	_, err = store.GetMeta(sess1)
	assert.Error(t, err, "heartbeat session must be deleted after disable (FIX-3)")

	// The workspace on disk must have no session_id stored.
	diskData, err := os.ReadFile(filepath.Join(wsDir, wsID+".json"))
	require.NoError(t, err)
	var diskWS workspace.Workspace
	require.NoError(t, json.Unmarshal(diskData, &diskWS))
	mc := diskWS.MemberConfigs[agentID]
	require.NotNil(t, mc.Heartbeat)
	assert.Empty(t, mc.Heartbeat.SessionID,
		"session_id must be cleared on disk after disable (FIX-3)")
}

// ---------------------------------------------------------------------------
// FIX-4a: TestWorkspacePUT_CoreTeamShrinkReleasesHeartbeat
// ---------------------------------------------------------------------------

// TestWorkspacePUT_CoreTeamShrinkReleasesHeartbeat verifies that a workspace PUT
// that shrinks core_team to drop an agent (without touching member_configs) prunes
// the agent's member_config entry, removes its cron job, and deletes its standing
// session (FIX-4a, FR-022).
func TestWorkspacePUT_CoreTeamShrinkReleasesHeartbeat(t *testing.T) {
	api, cs := buildHeartbeatTestAPI(t)
	const agentA = "mia"

	wsDir := filepath.Join(api.homePath, "workspaces")
	require.NoError(t, os.MkdirAll(wsDir, 0o700))
	wsID := "01JXFIXSHRINKTEST00000001"

	// Eagerly create a heartbeat session for agent A.
	storeA := api.agentLoop.GetAgentStore(agentA)
	require.NotNil(t, storeA)
	meta, err := storeA.NewHeartbeatSession(wsID, agentA)
	require.NoError(t, err)

	// Seed workspace with agent A on the team and an enabled heartbeat.
	ws := workspace.Workspace{
		ID: wsID, Name: "Shrink WS", Status: "active",
		CoreTeam: []string{agentA, "jim"},
		MemberConfigs: map[string]workspace.MemberConfig{
			agentA: {Heartbeat: &workspace.MemberHeartbeat{
				Enabled:         true,
				IntervalMinutes: 10,
				Body:            "Check.",
				SessionID:       meta.ID,
			}},
		},
		CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
	}
	wsData, err := json.MarshalIndent(ws, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, wsID+".json"), wsData, 0o600))

	// Seed a heartbeat cron job for agent A.
	everyMS := int64(10) * 60_000
	enabled := true
	cronJobName := heartbeatJobName(wsID, agentA)
	_, err = cs.AddJobFull(cron.JobSpec{
		Name:      cronJobName,
		Schedule:  cron.CronSchedule{Kind: "every", EveryMS: &everyMS},
		Message:   "Check.",
		AgentID:   agentA,
		SessionID: meta.ID,
		Enabled:   &enabled,
	})
	require.NoError(t, err)
	for _, j := range cs.ListJobs(true) {
		if j.Name == cronJobName {
			j.Payload.Kind = heartbeatJobKind
			_ = cs.UpdateJob(&j)
		}
	}
	require.Len(t, heartbeatJobsFor(cs), 1, "heartbeat cron job must exist before shrink")

	// Verify the session exists before the shrink.
	_, err = storeA.GetMeta(meta.ID)
	require.NoError(t, err, "heartbeat session must exist before core_team shrink")

	// PUT with core_team shrunk to drop agentA (no member_configs).
	shrinkBody := `{"core_team":["jim"]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/"+wsID, strings.NewReader(shrinkBody))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/workspaces/" + wsID
	api.HandleWorkspaces(w, r)
	require.Equal(t, http.StatusOK, w.Code, "shrink body=%s", w.Body.String())

	// The workspace on disk must no longer have agentA in member_configs.
	diskData, err := os.ReadFile(filepath.Join(wsDir, wsID+".json"))
	require.NoError(t, err)
	var diskWS workspace.Workspace
	require.NoError(t, json.Unmarshal(diskData, &diskWS))
	_, hasA := diskWS.MemberConfigs[agentA]
	assert.False(t, hasA, "member_config for dropped agent must be pruned (FIX-4a)")

	// The standing session must be deleted.
	_, err = storeA.GetMeta(meta.ID)
	assert.Error(t, err, "heartbeat session for dropped agent must be deleted on core_team shrink (FIX-4a)")

	// The cron job must be gone (reconcile ran after the shrink).
	assert.Empty(t, heartbeatJobsFor(cs),
		"heartbeat cron job for dropped agent must be removed after core_team shrink (FIX-4a)")
}

// ---------------------------------------------------------------------------
// FIX-4b: TestComputeDesiredHeartbeats_SkipsOffTeam
// ---------------------------------------------------------------------------

// TestComputeDesiredHeartbeats_SkipsOffTeam verifies that computeDesiredHeartbeats
// emits no desired job when a member_config entry exists for an agent NOT in the
// workspace's CoreTeam (FIX-4b: defense-in-depth against stale entries).
func TestComputeDesiredHeartbeats_SkipsOffTeam(t *testing.T) {
	// A workspace whose CoreTeam is ["jim"] but member_configs has "mia" (off-team).
	ws := workspace.Workspace{
		ID:       "wsX",
		Name:     "Off-team WS",
		Status:   "active",
		CoreTeam: []string{"jim"},
		MemberConfigs: map[string]workspace.MemberConfig{
			"mia": {Heartbeat: &workspace.MemberHeartbeat{
				Enabled:         true,
				IntervalMinutes: 10,
				Body:            "Check.",
			}},
		},
	}

	desired := computeDesiredHeartbeats([]workspace.Workspace{ws}, neverWorker)
	assert.Empty(t, desired,
		"off-team member_config must yield no desired heartbeat (FIX-4b)")
}

// ---------------------------------------------------------------------------
// FIX-pickSession: type-preservation for heartbeat sessions via continue mode
// ---------------------------------------------------------------------------

// TestPickSession_ContinuePreservesHeartbeatType verifies that when a heartbeat
// session (SessionTypeHeartbeat) already exists and is used as a continue job's
// SessionID, pickSession returns the existing session's ID without re-stamping
// the type to SessionTypeScheduled (i.e. GetOrCreateScheduledSession returns the
// existing meta as-is when the session already exists on disk).
func TestPickSession_ContinuePreservesHeartbeatType(t *testing.T) {
	cfg := baseConfig()
	r, exec, _, _ := newRunnerHarness(t, cfg, map[string]bool{"mia": true})

	// Create a heartbeat session directly in the store.
	const wsID = "01JXPICKSESSIONTESTWSID00"
	meta, err := exec.store.NewHeartbeatSession(wsID, "mia")
	require.NoError(t, err)
	require.Equal(t, session.SessionTypeHeartbeat, meta.Type)

	// Build a continue-mode cron job carrying the heartbeat session id.
	job := &cron.CronJob{
		ID:          "hb-job",
		AgentID:     "mia",
		SessionMode: cron.SessionModeContinue,
		SessionID:   meta.ID,
		Payload:     cron.CronPayload{Message: "heartbeat check"},
	}

	// pickSession must return the same session id.
	sid, err := r.pickSession(job, "mia")
	require.NoError(t, err)
	assert.Equal(t, meta.ID, sid, "continue mode must reuse the existing heartbeat session id")

	// The returned session must still have type heartbeat (not re-stamped to scheduled).
	returnedMeta, err := exec.store.GetMeta(sid)
	require.NoError(t, err)
	assert.Equal(t, session.SessionTypeHeartbeat, returnedMeta.Type,
		"continue mode must not re-stamp an existing heartbeat session to scheduled (type-preservation)")
}
