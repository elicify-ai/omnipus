// Integration tests for the workspace-heartbeat feature (ADR-027) after
// session-core U1 retargeted the standing session.
//
// Coverage kept from the pre-U1 file, moved onto the main-session model:
//   - T-G2: workspace PUT member_config validation bounds (DS-1 rows)
//   - T-G3: workspace PUT establishes the member's MAIN session (idempotent)
//   - first-fire resolution: a heartbeat runs in the member's main, and the
//     cron job carries no session address at all
//   - T-I1: workspace DELETE cascades the workspace's main sessions
//   - T-I2: boot reconcile removes legacy heartbeat jobs (no workspace segment)
//   - T-I3: memory settings PUT does not clobber sibling fields
//   - T-Finding2: a client cannot forge a session address through the PUT
//   - FIX-3 -> FR-002: disabling the heartbeat KEEPS the same protected main
//   - FIX-4a -> FR-003: core_team shrink prunes the member but RETAINS the main
//   - FIX-4b: computeDesiredHeartbeats skips off-team agents
//   - FIX-pickSession: a reconciled heartbeat job resolves the computed main
//
// Coverage deliberately NOT carried over: the dual-store sweep
// (deleteHeartbeatSessionAnyStore) and the heartbeat-typed standing session.
// Both mechanisms are deleted by DEL-01 — a main is created in the shared
// store by exactly one code path, so the dual-copy population they existed to
// remediate can no longer arise.

package gateway

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// buildHeartbeatTestAPI creates a restAPI with an audit logger, two agents ("mia"
// and "jim"), and a wired cron service. The agent stores are registered in the
// agent loop so GetAgentStore returns a real *session.UnifiedStore for each.
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
		if errors.Is(err, os.ErrNotExist) {
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

// writeWorkspaceRecord writes ws to the workspace store on disk.
func hbWriteWorkspaceRecord(t *testing.T, api *restAPI, ws workspace.Workspace) {
	t.Helper()
	wsDir := filepath.Join(api.homePath, "workspaces")
	require.NoError(t, os.MkdirAll(wsDir, 0o700))
	data, err := json.MarshalIndent(ws, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, ws.ID+".json"), data, 0o600))
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
	r := httptest.NewRequest(http.MethodDelete, workspaceDeleteURL(t, api, wsID), nil)
	r.URL.Path = "/api/v1/workspaces/" + wsID
	api.HandleWorkspaces(w, r)
	return w
}

// putMemberConfigs calls PUT /api/v1/workspaces/{id} with the given member_configs JSON.
func putMemberConfigs(t *testing.T, api *restAPI, wsID, memberConfigsJSON string) *httptest.ResponseRecorder {
	t.Helper()
	state, err := workspace.ReadState(api.homePath, wsID)
	require.NoError(t, err)
	body := fmt.Sprintf(`{"revision":%q,"member_configs":%s}`, state.Revision, memberConfigsJSON)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, workspaceDeleteURL(t, api, wsID), strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/workspaces/" + wsID
	api.HandleWorkspaces(w, r)
	return w
}

// putWorkspaceBody calls PUT /api/v1/workspaces/{id} with a raw JSON body.
func putWorkspaceBody(t *testing.T, api *restAPI, wsID, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, workspaceDeleteURL(t, api, wsID),
		strings.NewReader(withWorkspaceRevisionJSON(t, api, wsID, body)))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/workspaces/" + wsID
	api.HandleWorkspaces(w, r)
	return w
}

// readWorkspaceFromDisk decodes the persisted workspace record.
func readWorkspaceFromDisk(t *testing.T, api *restAPI, wsID string) workspace.Workspace {
	t.Helper()
	diskData, err := os.ReadFile(filepath.Join(api.homePath, "workspaces", wsID+".json"))
	require.NoError(t, err)
	var diskWS workspace.Workspace
	require.NoError(t, json.Unmarshal(diskData, &diskWS))
	return diskWS
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
	wsID := "01JXBOUNDTESTWSID0000001"
	hbWriteWorkspaceRecord(t, api, workspace.Workspace{
		ID: wsID, Name: "Bounds WS", Status: "active",
		CoreTeam:  []string{"mia", "worker1"},
		CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
	})
	wsData, err := os.ReadFile(filepath.Join(tmpDir, "workspaces", wsID+".json"))
	require.NoError(t, err)

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
			require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "workspaces", wsID+".json"), wsData, 0o600))

			w := putMemberConfigs(t, api, wsID, tc.memberJSON)
			assert.Equal(t, tc.wantCode, w.Code,
				"[%s] expected %d; body=%s", tc.name, tc.wantCode, w.Body.String())

			if tc.wantCode != http.StatusOK {
				// On reject: workspace on disk must be unchanged (no partial persist).
				diskWS := readWorkspaceFromDisk(t, api, wsID)
				assert.Empty(t, diskWS.MemberConfigs,
					"[%s] rejected PUT must not persist member_configs", tc.name)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// T-G3: the membership write establishes the main session
// ---------------------------------------------------------------------------

// TestWorkspacePUT_EagerSession verifies FR-002's get-or-create over the real
// PUT handler, replacing the pre-U1 eager-HEARTBEAT-session test:
//   - enabling the heartbeat leaves the member's computed main in place
//   - the response names it in member_configs[mia].main_session_id
//   - enabling again reuses the SAME identity (no second session)
//   - disabling and re-enabling still reuses it: protection is a property of
//     the identity, not of the enable toggle
func TestWorkspacePUT_EagerSession(t *testing.T) {
	api, _ := buildHeartbeatTestAPI(t)
	const agentID = "mia"

	wsID := "01JXEAGERTESTWSID000001"
	hbWriteWorkspaceRecord(t, api, workspace.Workspace{
		ID: wsID, Name: "Eager WS", Status: "active",
		CoreTeam:  []string{agentID},
		CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
	})

	enableBody := `{"member_configs":{"mia":{"heartbeat":{"enabled":true,"interval_minutes":10,"body":"Check tasks."}}}}`

	// ── PUT 1: enable heartbeat ── //
	w1 := putWorkspaceBody(t, api, wsID, enableBody)
	require.Equal(t, http.StatusOK, w1.Code, "PUT 1 body=%s", w1.Body.String())

	var resp1 gen.Workspace
	require.NoError(t, json.Unmarshal(w1.Body.Bytes(), &resp1))
	require.NotNil(t, resp1.MemberConfigs, "member_configs must be present in response")
	miaConfig1 := (*resp1.MemberConfigs)[agentID]
	require.NotNil(t, miaConfig1.MainSessionId, "main_session_id must be set")
	mainID := *miaConfig1.MainSessionId
	assert.Equal(t, "main-session-"+wsID+"+"+agentID, mainID, "spec FR-002 literal id")

	// The main exists in the SHARED session store, typed and stamped.
	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store)
	meta1, err := store.GetMeta(mainID)
	require.NoError(t, err, "main must exist in the shared store after the PUT")
	assert.Equal(t, session.SessionTypeMain, meta1.Type)
	assert.Equal(t, wsID, meta1.WorkspaceID)
	assert.Equal(t, agentID, meta1.AgentID)

	// ── PUT 2: enable again → same id (idempotent) ── //
	w2 := putWorkspaceBody(t, api, wsID, enableBody)
	require.Equal(t, http.StatusOK, w2.Code, "PUT 2 body=%s", w2.Body.String())

	var resp2 gen.Workspace
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp2))
	require.NotNil(t, resp2.MemberConfigs)
	miaConfig2 := (*resp2.MemberConfigs)[agentID]
	require.NotNil(t, miaConfig2.MainSessionId)
	assert.Equal(t, mainID, *miaConfig2.MainSessionId, "second enable must reuse the same main (idempotent)")
	meta2, err := store.GetMeta(mainID)
	require.NoError(t, err)
	assert.Equal(t, meta1.CreatedAt, meta2.CreatedAt, "same identity, not a replacement")

	// ── PUT 3: disable ── //
	disableBody := `{"member_configs":{"mia":{"heartbeat":{"enabled":false,"interval_minutes":10,"body":"Check tasks."}}}}`
	w3 := putWorkspaceBody(t, api, wsID, disableBody)
	require.Equal(t, http.StatusOK, w3.Code, "PUT 3 (disable) body=%s", w3.Body.String())

	// ── PUT 4: re-enable → the SAME main (FR-002) ── //
	w4 := putWorkspaceBody(t, api, wsID, enableBody)
	require.Equal(t, http.StatusOK, w4.Code, "PUT 4 (re-enable) body=%s", w4.Body.String())

	var resp4 gen.Workspace
	require.NoError(t, json.Unmarshal(w4.Body.Bytes(), &resp4))
	require.NotNil(t, resp4.MemberConfigs)
	miaConfig4 := (*resp4.MemberConfigs)[agentID]
	require.NotNil(t, miaConfig4.MainSessionId)
	assert.Equal(t, mainID, *miaConfig4.MainSessionId,
		"re-enable must keep the same main: protection is independent of the heartbeat toggle")
	meta4, err := store.GetMeta(mainID)
	require.NoError(t, err)
	assert.Equal(t, meta1.CreatedAt, meta4.CreatedAt, "no replacement identity was minted")
}

// ---------------------------------------------------------------------------
// Regression: a heartbeat resolves the member's main, with no stored address
// ---------------------------------------------------------------------------

// TestWorkspaceHeartbeat_FirstFireResolvesTheMain is the post-U1 replacement
// for the eager-session store-mismatch regression. What it guards now:
//  1. the reconciled heartbeat cron job carries NO session address — the id is
//     computed from the (workspace, agent) pair, so it cannot drift;
//  2. the job runs in MAIN mode, so pickSession resolves the computed main;
//  3. that resolution returns exactly the id the workspace wire advertises;
//  4. the shared store holds exactly one session for the pair — no duplicate
//     minted anywhere, and nothing lands in the legacy per-agent store.
func TestWorkspaceHeartbeat_FirstFireResolvesTheMain(t *testing.T) {
	api, cs := buildHeartbeatTestAPI(t)
	const agentID = "mia"

	wsID := "01JXFIRSTFIRETESTWSID0001"
	hbWriteWorkspaceRecord(t, api, workspace.Workspace{
		ID: wsID, Name: "First Fire WS", Status: "active",
		CoreTeam:  []string{agentID},
		CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
	})

	// Step 1: enable the heartbeat through the real PUT handler.
	w1 := putMemberConfigs(t, api, wsID,
		`{"mia":{"heartbeat":{"enabled":true,"interval_minutes":10,"body":"Check tasks."}}}`)
	require.Equal(t, http.StatusOK, w1.Code, "enable body=%s", w1.Body.String())
	var resp1 gen.Workspace
	require.NoError(t, json.Unmarshal(w1.Body.Bytes(), &resp1))
	advertised := *(*resp1.MemberConfigs)[agentID].MainSessionId
	require.NotEmpty(t, advertised)

	// The reconciler wired the heartbeat cron job in MAIN mode with no stored id.
	jobs := heartbeatJobsFor(cs)
	require.Len(t, jobs, 1, "enable must create exactly one heartbeat cron job")
	assert.Empty(t, jobs[0].SessionID,
		"the heartbeat job must carry no session address: the main id is computed from the pair")
	assert.Equal(t, cron.SessionModeMain, jobs[0].SessionMode,
		"a heartbeat runs in the member's main session")

	sharedStore := api.agentLoop.GetSessionStore()
	require.NotNil(t, sharedStore)

	// Step 2: resolve the session exactly the way pickSession's main-mode branch does.
	resolved, err := sharedStore.GetOrCreateMainSession(wsID, agentID)
	require.NoError(t, err)
	assert.Equal(t, advertised, resolved.ID,
		"the fired run must resolve the SAME id the workspace advertises")

	// Exactly one session exists for the pair — no duplicate minted anywhere.
	allShared, err := sharedStore.ListSessions()
	require.NoError(t, err)
	assert.Len(t, allShared, 1,
		"the shared store must hold exactly one session for the pair (no duplicate minted)")
	assert.Equal(t, session.SessionTypeMain, resolved.Type)
	assert.Equal(t, wsID, resolved.WorkspaceID,
		"the workspace stamp must survive to the fired turn's session meta")

	// Nothing was created in the legacy per-agent store.
	legacyStore := api.agentLoop.GetAgentStore(agentID)
	require.NotNil(t, legacyStore)
	legacySessions, err := legacyStore.ListSessions()
	require.NoError(t, err)
	assert.Empty(t, legacySessions, "no session should ever land in the legacy per-agent store")
}

// ---------------------------------------------------------------------------
// T-I1: WorkspaceDelete_Cascade
// ---------------------------------------------------------------------------

// TestWorkspaceDelete_Cascade verifies that deleting a workspace removes the
// heartbeat cron job AND the workspace's main sessions — a main lives in the
// shared session store, not under the workspace directory, so the workspace
// RemoveAll does not reach it.
func TestWorkspaceDelete_Cascade(t *testing.T) {
	api, cs := buildHeartbeatTestAPI(t)
	const agentID = "mia"
	const wsID = "01JXCASCTESTWSID0000001"

	hbWriteWorkspaceRecord(t, api, workspace.Workspace{
		ID: wsID, Name: "Cascade WS", Status: "active",
		CoreTeam: []string{agentID},
		MemberConfigs: map[string]workspace.MemberConfig{
			agentID: {Heartbeat: &workspace.MemberHeartbeat{
				Enabled: true, IntervalMinutes: 10, Body: "Check.",
			}},
		},
		CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
	})

	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.GetOrCreateMainSession(wsID, agentID)
	require.NoError(t, err)

	// Seed the cron job for this workspace heartbeat.
	everyMS := int64(10) * 60_000
	enabled := true
	cronJobName := heartbeatJobName(wsID, agentID)
	_, err = cs.AddJobFull(cron.JobSpec{
		Name:     cronJobName,
		Schedule: cron.CronSchedule{Kind: "every", EveryMS: &everyMS},
		Message:  "Check.",
		AgentID:  agentID,
		Enabled:  &enabled,
	})
	require.NoError(t, err, "seed heartbeat cron job")
	for _, j := range cs.ListJobs(true) {
		if j.Name == cronJobName {
			j.Payload.Kind = heartbeatJobKind
			_ = cs.UpdateJob(&j)
		}
	}

	// Verify setup: session and cron job exist before delete.
	_, err = store.GetMeta(meta.ID)
	require.NoError(t, err, "main must exist before workspace delete")
	require.Len(t, heartbeatJobsFor(cs), 1, "heartbeat cron job must exist before workspace delete")

	w := deleteWorkspaceViaAPI(t, api, wsID)
	require.Equal(t, http.StatusOK, w.Code, "DELETE workspace must return a 200 ConfigurationMutationState envelope (ADR-090 §5.2); body=%s", w.Body.String())

	assert.Empty(t, heartbeatJobsFor(cs), "heartbeat cron job must be removed by cascade delete")

	_, err = store.GetMeta(meta.ID)
	assert.Error(t, err, "the workspace's main session must be deleted by cascade delete")
}

// TestWorkspaceDelete_Cascade_AfterMembershipPut is the sibling of
// TestWorkspaceDelete_Cascade that drives the real PUT path, so the main is
// created by the production code path rather than seeded directly.
func TestWorkspaceDelete_Cascade_AfterMembershipPut(t *testing.T) {
	api, cs := buildHeartbeatTestAPI(t)
	const agentID = "mia"
	const wsID = "01JXCASCSHAREDTESTWSID001"

	hbWriteWorkspaceRecord(t, api, workspace.Workspace{
		ID: wsID, Name: "Cascade Shared WS", Status: "active",
		CoreTeam:  []string{agentID},
		CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
	})

	w1 := putMemberConfigs(t, api, wsID,
		`{"mia":{"heartbeat":{"enabled":true,"interval_minutes":10,"body":"Check tasks."}}}`)
	require.Equal(t, http.StatusOK, w1.Code, "enable body=%s", w1.Body.String())
	var resp1 gen.Workspace
	require.NoError(t, json.Unmarshal(w1.Body.Bytes(), &resp1))
	mainID := *(*resp1.MemberConfigs)[agentID].MainSessionId
	require.NotEmpty(t, mainID)

	sharedStore := api.agentLoop.GetSessionStore()
	require.NotNil(t, sharedStore)
	_, err := sharedStore.GetMeta(mainID)
	require.NoError(t, err, "main must exist in the shared store before workspace delete")

	require.Len(t, heartbeatJobsFor(cs), 1,
		"heartbeat cron job must exist before workspace delete (reconciled by the enable PUT)")

	w := deleteWorkspaceViaAPI(t, api, wsID)
	require.Equal(t, http.StatusOK, w.Code, "DELETE workspace must return a 200 ConfigurationMutationState envelope (ADR-090 §5.2); body=%s", w.Body.String())

	assert.Empty(t, heartbeatJobsFor(cs), "heartbeat cron job must be removed by cascade delete")

	_, err = sharedStore.GetMeta(mainID)
	assert.Error(t, err, "the workspace's main session must be deleted from the shared store by cascade delete")
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
// T-Finding2: no client-supplied session address survives a PUT
// ---------------------------------------------------------------------------

// TestWorkspacePUT_SessionIDReadOnly verifies that a client cannot introduce a
// session address through a PUT member_configs payload. Since U1 the field is
// gone from both contracts entirely, so the assertion is stronger than the
// original "cannot overwrite": the address the server reports is the COMPUTED
// main id, and nothing named session_id is persisted at all.
func TestWorkspacePUT_SessionIDReadOnly(t *testing.T) {
	api, _ := buildHeartbeatTestAPI(t)
	const agentID = "mia"

	wsID := "01JXREADONLYTESTWSID0001"
	hbWriteWorkspaceRecord(t, api, workspace.Workspace{
		ID: wsID, Name: "ReadOnly WS", Status: "active",
		CoreTeam:  []string{agentID},
		CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
	})

	enableBody := `{"member_configs":{"mia":{"heartbeat":{"enabled":true,"interval_minutes":10,"body":"Check tasks."}}}}`
	w1 := putWorkspaceBody(t, api, wsID, enableBody)
	require.Equal(t, http.StatusOK, w1.Code, "enable body=%s", w1.Body.String())

	var resp1 gen.Workspace
	require.NoError(t, json.Unmarshal(w1.Body.Bytes(), &resp1))
	require.NotNil(t, resp1.MemberConfigs)
	realMainID := *(*resp1.MemberConfigs)[agentID].MainSessionId
	require.NotEmpty(t, realMainID, "the server must have computed a main_session_id")

	// Now PUT with a forged session address (the retired field name) alongside
	// a forged main_session_id.
	const forgedID = "01JXFAKEFORGEDSESSIONID00"
	forgeBody := fmt.Sprintf(
		`{"member_configs":{"mia":{"main_session_id":"%s","heartbeat":{"enabled":true,"interval_minutes":10,"body":"Check tasks.","session_id":%q}}}}`,
		forgedID, forgedID,
	)
	w2 := putWorkspaceBody(t, api, wsID, forgeBody)
	require.Equal(t, http.StatusOK, w2.Code, "PUT with forged address body=%s", w2.Body.String())

	// Nothing forged reached disk: the persisted member config carries only the
	// heartbeat settings, and the raw bytes contain no session address at all.
	diskWS := readWorkspaceFromDisk(t, api, wsID)
	mc := diskWS.MemberConfigs[agentID]
	require.NotNil(t, mc.Heartbeat)
	raw, err := os.ReadFile(filepath.Join(api.homePath, "workspaces", wsID+".json"))
	require.NoError(t, err)
	assert.NotContains(t, string(raw), forgedID, "forged id must never be persisted")
	assert.NotContains(t, string(raw), "session_id", "no session address is stored beside heartbeat config")

	// And the server still advertises its own computed main.
	var resp2 gen.Workspace
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp2))
	require.NotNil(t, resp2.MemberConfigs)
	require.NotNil(t, (*resp2.MemberConfigs)[agentID].MainSessionId)
	assert.Equal(t, realMainID, *(*resp2.MemberConfigs)[agentID].MainSessionId,
		"the computed main id is server-owned and unaffected by the forged payload")
}

// ---------------------------------------------------------------------------
// FR-002: disabling the heartbeat keeps the same protected main
// ---------------------------------------------------------------------------

// TestWorkspacePUT_DisableReleasesSession verifies FR-002's "protection
// independent of heartbeat": the pre-U1 behaviour (delete the standing session
// on disable) is gone, and disabling now leaves the SAME main in place —
// protected, navigable, and still refused by DELETE.
func TestWorkspacePUT_DisableReleasesSession(t *testing.T) {
	api, _ := buildHeartbeatTestAPI(t)
	const agentID = "mia"

	wsID := "01JXFIXDISABLETESTWSID001"
	hbWriteWorkspaceRecord(t, api, workspace.Workspace{
		ID: wsID, Name: "Disable Test WS", Status: "active",
		CoreTeam:  []string{agentID},
		CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
	})

	// Step 1: enable heartbeat.
	enableBody := `{"member_configs":{"mia":{"heartbeat":{"enabled":true,"interval_minutes":10,"body":"Check tasks."}}}}`
	w1 := putWorkspaceBody(t, api, wsID, enableBody)
	require.Equal(t, http.StatusOK, w1.Code, "enable body=%s", w1.Body.String())

	var resp1 gen.Workspace
	require.NoError(t, json.Unmarshal(w1.Body.Bytes(), &resp1))
	require.NotNil(t, resp1.MemberConfigs)
	mainID := *(*resp1.MemberConfigs)[agentID].MainSessionId
	require.NotEmpty(t, mainID)

	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store)
	before, err := store.GetMeta(mainID)
	require.NoError(t, err, "main must exist after enable")

	// Step 2: disable heartbeat — the main must SURVIVE.
	disableBody := `{"member_configs":{"mia":{"heartbeat":{"enabled":false,"interval_minutes":10,"body":"Check tasks."}}}}`
	w2 := putWorkspaceBody(t, api, wsID, disableBody)
	require.Equal(t, http.StatusOK, w2.Code, "disable body=%s", w2.Body.String())

	after, err := store.GetMeta(mainID)
	require.NoError(t, err, "FR-002: disabling the heartbeat must not delete the main")
	assert.Equal(t, before.CreatedAt, after.CreatedAt, "same retained identity")

	// Still protected: DELETE is refused with a 409.
	assert.Equal(t, http.StatusConflict, deleteSessionViaAPI(t, api, mainID).Code,
		"FR-002: the main stays protected with the heartbeat off")

	// The workspace on disk stores no session address beside the heartbeat.
	raw, err := os.ReadFile(filepath.Join(api.homePath, "workspaces", wsID+".json"))
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "session_id",
		"member configuration carries no stored session address")
}

// ---------------------------------------------------------------------------
// FR-003: core_team shrink prunes the member but retains the main
// ---------------------------------------------------------------------------

// TestWorkspacePUT_CoreTeamShrinkReleasesHeartbeat verifies that a workspace PUT
// that shrinks core_team to drop an agent (without touching member_configs)
// prunes the agent's member_config entry and removes its cron job, while the
// MAIN is RETAINED on disk (FR-003) — hidden, not deleted, so re-adding the
// same pair would reveal the same id.
func TestWorkspacePUT_CoreTeamShrinkReleasesHeartbeat(t *testing.T) {
	api, cs := buildHeartbeatTestAPI(t)
	const agentA = "mia"

	wsID := "01JXFIXSHRINKTEST00000001"
	hbWriteWorkspaceRecord(t, api, workspace.Workspace{
		ID: wsID, Name: "Shrink WS", Status: "active",
		CoreTeam: []string{agentA, "jim"},
		MemberConfigs: map[string]workspace.MemberConfig{
			agentA: {Heartbeat: &workspace.MemberHeartbeat{
				Enabled: true, IntervalMinutes: 10, Body: "Check.",
			}},
		},
		CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
	})

	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.GetOrCreateMainSession(wsID, agentA)
	require.NoError(t, err)

	// Seed a heartbeat cron job for agent A.
	everyMS := int64(10) * 60_000
	enabled := true
	cronJobName := heartbeatJobName(wsID, agentA)
	_, err = cs.AddJobFull(cron.JobSpec{
		Name:     cronJobName,
		Schedule: cron.CronSchedule{Kind: "every", EveryMS: &everyMS},
		Message:  "Check.",
		AgentID:  agentA,
		Enabled:  &enabled,
	})
	require.NoError(t, err)
	for _, j := range cs.ListJobs(true) {
		if j.Name == cronJobName {
			j.Payload.Kind = heartbeatJobKind
			_ = cs.UpdateJob(&j)
		}
	}
	require.Len(t, heartbeatJobsFor(cs), 1, "heartbeat cron job must exist before shrink")

	_, err = store.GetMeta(meta.ID)
	require.NoError(t, err, "main must exist before core_team shrink")

	// PUT with core_team shrunk to drop agentA (no member_configs).
	w := putWorkspaceBody(t, api, wsID, `{"core_team":["jim"]}`)
	require.Equal(t, http.StatusOK, w.Code, "shrink body=%s", w.Body.String())

	// The workspace on disk must no longer have agentA in member_configs.
	diskWS := readWorkspaceFromDisk(t, api, wsID)
	_, hasA := diskWS.MemberConfigs[agentA]
	assert.False(t, hasA, "member_config for dropped agent must be pruned")

	// FR-003: the identity is RETAINED, and hidden from the list.
	_, err = store.GetMeta(meta.ID)
	require.NoError(t, err, "FR-003: the main is retained until normal retention, not deleted")

	// The cron job must be gone (reconcile ran after the shrink).
	assert.Empty(t, heartbeatJobsFor(cs),
		"heartbeat cron job for dropped agent must be removed after core_team shrink")
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
// FIX-pickSession: a reconciled heartbeat job resolves the computed main
// ---------------------------------------------------------------------------

// TestPickSession_ContinuePreservesHeartbeatType verifies that a heartbeat cron
// job produced by the reconciler resolves, through the real pickSession, to the
// (workspace, agent) pair's computed main — not to a per-agent
// `sched-main-<owner>` id and not to a fresh session. This joins the reconciler
// and pickSession, which the unit-level U1 schedule test does not.
func TestPickSession_ContinuePreservesHeartbeatType(t *testing.T) {
	cfg := baseConfig()
	r, exec, _, _ := newRunnerHarness(t, cfg, map[string]bool{"mia": true})

	const wsID = "01JXPICKSESSIONTESTWSID00"
	workspaces := []workspace.Workspace{{
		ID: wsID, Name: "HB WS", Status: "active", CoreTeam: []string{"mia"},
		MemberConfigs: map[string]workspace.MemberConfig{
			"mia": {Heartbeat: &workspace.MemberHeartbeat{
				Enabled: true, IntervalMinutes: 10, Body: "Check.",
			}},
		},
	}}

	cs := newReconcileCron(t)
	require.NoError(t, ReconcileHeartbeatSchedules(cs, workspaces, neverWorker))
	jobs := heartbeatJobsFor(cs)
	require.Len(t, jobs, 1, "the reconciler must produce one heartbeat job")

	job := jobs[0]
	sid, err := r.pickSession(&job, "mia")
	require.NoError(t, err)

	want := "main-session-" + wsID + "+mia"
	assert.Equal(t, want, sid, "a heartbeat job must resolve the pair's computed main")
	assert.NotEqual(t, "sched-main-mia", sid, "the retired per-agent standing id is never minted")

	meta, err := exec.store.GetMeta(sid)
	require.NoError(t, err)
	assert.Equal(t, session.SessionTypeMain, meta.Type)
	assert.Equal(t, wsID, meta.WorkspaceID)
	assert.Equal(t, "mia", meta.AgentID)

	// The retired id is never minted, even by the reconciliation path.
	_, err = exec.store.GetMeta("sched-main-mia")
	assert.Error(t, err, "the retired standing-session id is never created")
}
