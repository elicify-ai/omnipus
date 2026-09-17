// rest_agents_test.go: tests for agent roster, read paths, delete, and shared wire/config translation

package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/clidetect"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- moved from rest.go tests 2026-09-15 ---

// TestDeleteAgent_DeniesPendingApprovalsAndBroadcasts: deleting an agent
// denies the approvals it is still waiting on (unblocking its turn), tells
// every tab, and leaves other agents' approvals untouched.
func TestDeleteAgent_DeniesPendingApprovalsAndBroadcasts(t *testing.T) {
	api := buildExecutorTestAPI(t)
	handler, _, _ := newTestWSHandler(t)
	reg := newApprovalRegistryV2(64, 300*time.Second)
	reg.terminalRetention = 0
	api.approvalReg = reg
	reg.setResolutionListener(handler.broadcastToolApprovalResolved)
	tabs := apprResAttachConns(t, handler, 2)

	doomed, accepted := reg.requestApproval("tc-doomed", "write_file",
		map[string]any{"path": "e3-marker.txt"}, "test-agent", "sess-doomed", "turn-doomed")
	require.True(t, accepted)
	survivor, accepted := reg.requestApproval("tc-survivor", "write_file",
		map[string]any{"path": "keep.txt"}, "other-agent", "sess-other", "turn-other")
	require.True(t, accepted)
	t.Cleanup(func() { reg.resolve(survivor.ApprovalID, ApprovalActionCancel) })

	w := httptest.NewRecorder()
	api.HandleAgents(w, revisionedDeleteRequest(t, api, "test-agent"))
	require.Equal(t, http.StatusOK, w.Code, "delete must succeed: %s", w.Body.String())

	o := apprResAwaitOutcome(t, doomed)
	assert.Equal(t, ApprovalOutcome{Approved: false, Reason: denialReasonCancel}, o)

	for i, wc := range tabs {
		f := apprResReadFrame(t, wc, "tool_approval_resolved")
		assert.Equal(t, doomed.ApprovalID, f["approval_id"], "tab %d", i)
		assert.Equal(t, string(ApprovalStateDeniedCancel), f["state"], "tab %d", i)
	}

	still := reg.get(survivor.ApprovalID)
	require.NotNil(t, still, "another agent's approval must survive the delete")
	reg.mu.Lock()
	state := still.state
	reg.mu.Unlock()
	assert.Equal(t, ApprovalStatePending, state)
}

// TestGetAgent_FallbackModels_ReflectsPersistedValue proves GET /agents/{id}
// independently reflects a persisted fallback_models chain (not just what a
// PUT response could echo back from the request it just received).
func TestGetAgent_FallbackModels_ReflectsPersistedValue(t *testing.T) {
	api := buildExecutorTestAPI(t)

	putBody := `{"fallback_models":[{"model":"claude-haiku-4.5","provider":"anthropic"}]}`
	putW := httptest.NewRecorder()
	putR := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent", strings.NewReader(putBody))
	putR.Header.Set("Content-Type", "application/json")
	api.HandleAgents(putW, putR)
	require.Equal(t, http.StatusOK, putW.Code, "put body: %s", putW.Body.String())

	getW := httptest.NewRecorder()
	getR := httptest.NewRequest(http.MethodGet, "/api/v1/agents/test-agent", nil)
	api.HandleAgents(getW, getR)
	require.Equal(t, http.StatusOK, getW.Code, "get body: %s", getW.Body.String())

	got := decodeAgentResp(t, getW.Body.Bytes())
	require.NotNil(t, got.FallbackModels, "GET must echo fallback_models — was always omitted pre-fix")
	require.Len(t, *got.FallbackModels, 1)
	assert.Equal(t, "claude-haiku-4.5", (*got.FallbackModels)[0].Model)
	require.NotNil(t, (*got.FallbackModels)[0].Provider)
	assert.Equal(t, "anthropic", *(*got.FallbackModels)[0].Provider)
}

// TestListAgents_FallbackModels_ReflectsPersistedValue proves the agent-list
// endpoint (GET /agents) also reflects a persisted fallback_models chain —
// the third of the three response-construction sites named in the bug report.
func TestListAgents_FallbackModels_ReflectsPersistedValue(t *testing.T) {
	api := buildExecutorTestAPI(t)

	putBody := `{"fallback_models":[{"model":"gemini-2.5-flash","provider":"google"}]}`
	putW := httptest.NewRecorder()
	putR := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent", strings.NewReader(putBody))
	putR.Header.Set("Content-Type", "application/json")
	api.HandleAgents(putW, putR)
	require.Equal(t, http.StatusOK, putW.Code, "put body: %s", putW.Body.String())

	listW := httptest.NewRecorder()
	listR := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	api.HandleAgents(listW, listR)
	require.Equal(t, http.StatusOK, listW.Code, "list body: %s", listW.Body.String())

	var agents []map[string]any
	require.NoError(t, json.Unmarshal(listW.Body.Bytes(), &agents))
	var found map[string]any
	for _, ag := range agents {
		if ag["id"] == "test-agent" {
			found = ag
			break
		}
	}
	require.NotNil(t, found, "test-agent must appear in the agent list")
	fm, ok := found["fallback_models"].([]any)
	require.True(t, ok, "list response must include fallback_models for test-agent")
	require.Len(t, fm, 1)
	entry, ok := fm[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "gemini-2.5-flash", entry["model"])
	assert.Equal(t, "google", entry["provider"])
}

func TestListAgentSessions_IncludesSharedStoreSessions(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	shared := api.agentLoop.GetSessionStore()
	require.NotNil(t, shared, "test harness must wire a shared session store")

	meta, err := shared.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)

	w := httptest.NewRecorder()
	api.listAgentSessions(w, "mia")

	require.Equal(t, 200, w.Code, "body: %s", w.Body.String())
	var got []gen.Session
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))

	var found bool
	for _, s := range got {
		if s.Id == meta.ID {
			found = true
		}
	}
	assert.True(t, found, "a session created in the shared store must appear in GET /agents/{id}/sessions")
}

func TestListAgentSessions_DedupesSessionPresentInBothStores(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	shared := api.agentLoop.GetSessionStore()
	require.NotNil(t, shared, "test harness must wire a shared session store")
	legacy := api.agentLoop.GetAgentStore("mia")
	require.NotNil(t, legacy, "test harness must wire a legacy per-agent session store")

	meta, err := shared.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)

	// Simulate the pre-fix duplicate-mint bug: the exact same session id also
	// has an on-disk meta.json in the legacy per-agent store. Written directly
	// to disk (not via NewSession, which always mints a fresh random id) so
	// both stores genuinely disagree-yet-agree about one session id, exactly
	// the shape the merge's dedup must collapse to one entry.
	dupDir := filepath.Join(legacy.BaseDir(), meta.ID)
	require.NoError(t, os.MkdirAll(dupDir, 0o700))
	dupMeta := *meta
	data, err := json.Marshal(dupMeta)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dupDir, "meta.json"), data, 0o600))

	w := httptest.NewRecorder()
	api.listAgentSessions(w, "mia")

	require.Equal(t, 200, w.Code, "body: %s", w.Body.String())
	var got []gen.Session
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))

	count := 0
	for _, s := range got {
		if s.Id == meta.ID {
			count++
		}
	}
	assert.Equal(t, 1, count, "a session id present in both the shared and legacy stores must appear exactly once")
}

func TestListAgentSessions_UnknownAgent_ReturnsEmpty(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	api.listAgentSessions(w, "no-such-agent")

	require.Equal(t, 200, w.Code, "body: %s", w.Body.String())
	var got []gen.Session
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Empty(t, got)
}

// TestListAgentSessions_AllStoresFail_Returns500 proves a real backend
// failure is still surfaced as 500, not silently presented as "this agent
// has zero sessions". With no data recovered from either store,
// listAgentSessions must not collapse that into a lying 200+[].
//
// The failure is injected by replacing each store's base directory with a
// REGULAR FILE, so os.ReadDir fails with ENOTDIR. Do not switch this back to
// chmod 0o000: root holds CAP_DAC_OVERRIDE and walks straight through the
// permission bits, so the chmod form passes locally (unprivileged dev pod)
// and silently returns 200+[] under CI, which runs as root. ENOTDIR is
// privilege-independent and fails identically for both.
func TestListAgentSessions_AllStoresFail_Returns500(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	shared := api.agentLoop.GetSessionStore()
	require.NotNil(t, shared)
	legacy := api.agentLoop.GetAgentStore("mia")
	require.NotNil(t, legacy)

	for _, dir := range []string{shared.BaseDir(), legacy.BaseDir()} {
		require.NoError(t, os.RemoveAll(dir))
		require.NoError(t, os.WriteFile(dir, []byte("not a directory"), 0o600))
		t.Cleanup(func() { _ = os.Remove(dir) })
	}

	w := httptest.NewRecorder()
	api.listAgentSessions(w, "mia")

	assert.Equal(t, 500, w.Code, "body: %s", w.Body.String())
}

// TestListAgentSessions_SharedStoreFailsLegacyOk_Returns500NotPartial200 is
// the CRITICAL regression this file's other tests didn't cover: ONE store
// errors while the OTHER still produces real data. Before this fix,
// listAgentSessions escalated to 500 only when len(metas)==0 AND len(errs)>0
// (see the pre-fix condition in listAgentSessions's git history) — so a
// legacy-store session alongside a shared-store failure produced a 200 with
// ONLY the legacy session, silently omitting whatever the (healthy-by-
// comparison but actually broken) shared store — the PRIMARY home for
// sessions minted after the shared-store migration — could not report. That
// re-introduces, one layer up, the exact "silent incomplete list presented
// as complete" bug this function's own doc comment says it was written to
// fix.
//
// The failure is injected the same ENOTDIR way as
// TestListAgentSessions_AllStoresFail_Returns500 (see its doc for why not
// chmod 0o000), applied to ONLY the shared store's directory. The legacy
// store is left healthy and seeded with one real session, so a pre-fix
// implementation would return 200 with that one session — this test asserts
// the caller instead gets an honest 500, never a partial list.
func TestListAgentSessions_SharedStoreFailsLegacyOk_Returns500NotPartial200(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	shared := api.agentLoop.GetSessionStore()
	require.NotNil(t, shared)
	legacy := api.agentLoop.GetAgentStore("mia")
	require.NotNil(t, legacy)

	// Seed the legacy store with a real, healthy session.
	_, err := legacy.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)

	// Break ONLY the shared store's directory.
	require.NoError(t, os.RemoveAll(shared.BaseDir()))
	require.NoError(t, os.WriteFile(shared.BaseDir(), []byte("not a directory"), 0o600))
	t.Cleanup(func() { _ = os.Remove(shared.BaseDir()) })

	w := httptest.NewRecorder()
	api.listAgentSessions(w, "mia")

	require.Equal(t, 500, w.Code,
		"a shared-store read failure must escalate to 500 even though the legacy store "+
			"still has data — body: %s", w.Body.String())

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.NotEmpty(t, body["error"], "500 response must carry an attributable error message")
}

// TestListAgentSessions_LegacyStoreFailsSharedOk_Returns500NotPartial200 is
// the mirror of the test above: the legacy store errors while the shared
// store (the primary store for this agent's sessions) is healthy and has a
// real session. Even though the shared store alone would already be the
// "usually complete" answer, this endpoint has no wire-shape slot to mark a
// 200 response "may be missing legacy-store data" (see listAgentSessions's
// own escalation-check comment), so it must still 500 rather than guess.
func TestListAgentSessions_LegacyStoreFailsSharedOk_Returns500NotPartial200(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	shared := api.agentLoop.GetSessionStore()
	require.NotNil(t, shared)
	legacy := api.agentLoop.GetAgentStore("mia")
	require.NotNil(t, legacy)

	// Seed the shared store with a real, healthy session.
	_, err := shared.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)

	// Break ONLY the legacy store's directory.
	require.NoError(t, os.RemoveAll(legacy.BaseDir()))
	require.NoError(t, os.WriteFile(legacy.BaseDir(), []byte("not a directory"), 0o600))
	t.Cleanup(func() { _ = os.Remove(legacy.BaseDir()) })

	w := httptest.NewRecorder()
	api.listAgentSessions(w, "mia")

	assert.Equal(t, 500, w.Code,
		"a legacy-store read failure must escalate to 500 even though the shared store "+
			"still has data — body: %s", w.Body.String())
}

// --- /api/v1/system/cli-detect ---

// TestHandleSystemCliDetect_OK verifies the handler returns 200 with the new
// per-CLI {installed, path, source} shape. The detection function is replaced
// with a deterministic stub (claude present via $PATH, the others missing) so
// the test does not depend on the developer's local host layout.
//
// BDD: Given detection returns {claude: installed on PATH, codex: missing,
// opencode: missing}, When GET /api/v1/system/cli-detect is called, Then the
// response is 200 with claude.installed=true (path + source="path") and the
// others installed=false with null path/source.
// Traces to: external-executor-cli-path-detection-spec.md FR-001/FR-011.
func TestHandleSystemCliDetect_OK(t *testing.T) {
	orig := cliDetectAll
	t.Cleanup(func() { cliDetectAll = orig })

	cliDetectAll = func() map[string]clidetect.Result {
		return map[string]clidetect.Result{
			"claude-code": {Installed: true, Path: "/usr/local/bin/claude", Source: clidetect.SourcePath},
			"codex":       {},
			"opencode":    {},
		}
	}

	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/system/cli-detect", nil)
	api.HandleSystemCliDetect(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	var resp gen.CliDetect
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Claude.Installed, "claude should be reported installed")
	require.NotNil(t, resp.Claude.Path)
	assert.Equal(t, "/usr/local/bin/claude", *resp.Claude.Path)
	require.NotNil(t, resp.Claude.Source)
	assert.Equal(t, gen.CliDetectClaudeSource("path"), *resp.Claude.Source)

	assert.False(t, resp.Codex.Installed, "codex should be reported missing")
	assert.Nil(t, resp.Codex.Path, "missing CLI must have null path")
	assert.Nil(t, resp.Codex.Source, "missing CLI must have null source")
	assert.False(t, resp.Opencode.Installed, "opencode should be reported missing")
	assert.Nil(t, resp.Opencode.Path)
	assert.Nil(t, resp.Opencode.Source)
}

// TestHandleSystemCliDetect_AllPresent verifies all three CLIs are reported
// installed, exercising both source variants ("path" and "well-known").
func TestHandleSystemCliDetect_AllPresent(t *testing.T) {
	orig := cliDetectAll
	t.Cleanup(func() { cliDetectAll = orig })

	cliDetectAll = func() map[string]clidetect.Result {
		return map[string]clidetect.Result{
			"claude-code": {Installed: true, Path: "/usr/local/bin/claude", Source: clidetect.SourcePath},
			"codex":       {Installed: true, Path: "/home/dev/.local/bin/codex", Source: clidetect.SourceWellKnown},
			"opencode":    {Installed: true, Path: "/opt/homebrew/bin/opencode", Source: clidetect.SourceWellKnown},
		}
	}

	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/system/cli-detect", nil)
	api.HandleSystemCliDetect(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	var resp gen.CliDetect
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Claude.Installed)
	assert.True(t, resp.Codex.Installed)
	assert.True(t, resp.Opencode.Installed)
	require.NotNil(t, resp.Codex.Source)
	assert.Equal(t, gen.CliDetectCodexSource("well-known"), *resp.Codex.Source)
	require.NotNil(t, resp.Opencode.Path)
	assert.Equal(t, "/opt/homebrew/bin/opencode", *resp.Opencode.Path)
}

// TestHandleSystemCliDetect_AllMissing verifies the SPA grey-out state: every
// CLI installed=false with null path/source.
func TestHandleSystemCliDetect_AllMissing(t *testing.T) {
	orig := cliDetectAll
	t.Cleanup(func() { cliDetectAll = orig })

	cliDetectAll = func() map[string]clidetect.Result {
		return map[string]clidetect.Result{
			"claude-code": {},
			"codex":       {},
			"opencode":    {},
		}
	}

	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/system/cli-detect", nil)
	api.HandleSystemCliDetect(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	var resp gen.CliDetect
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp.Claude.Installed)
	assert.Nil(t, resp.Claude.Path)
	assert.False(t, resp.Codex.Installed)
	assert.False(t, resp.Opencode.Installed)
}

// TestHandleSystemCliDetect_MethodNotAllowed verifies the handler rejects
// non-GET methods with 405. The probe function is irrelevant — the method
// guard short-circuits before the probe runs.
func TestHandleSystemCliDetect_MethodNotAllowed(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/system/cli-detect", nil)
	api.HandleSystemCliDetect(w, r)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// --- DELETE /api/v1/agents/{id} ---

// TestHandleAgentsDelete_OK verifies DELETE on a custom (non-locked) agent
// returns the truthful complete/active state and removes the agent.
//
// BDD: Given a custom agent exists in config, When DELETE /api/v1/agents/{id}
// is called, Then the response is complete/active and the agent no longer
// appears in the list.
// Traces to: agent-form-requirements.md §6.1 — Edit slide-over Delete flow.
func TestHandleAgentsDelete_OK(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	// ADR-054: deleteAgent removes the agent's entity record directly via
	// agentstore (entities/agents/<id>.json), bypassing config.json entirely,
	// then calls a.triggerReloadAndWait to refresh the in-memory
	// cfg.Agents.List (populateAgentsListFromStore) so the deletion is
	// reflected before the handler returns. newTestRestAPIWithHome's bare
	// AgentLoop never wires SetReloadFunc (mirrors production: AgentLoop.Run()
	// is never started in these unit tests), so without this,
	// triggerReloadAndWait's "reload not configured" branch treats the reload
	// as an intentional no-op (see rest_auth.go) and cfg.Agents.List is never
	// refreshed — the deleted agent would keep appearing to GET requests even
	// though its entity record is genuinely gone from disk. Wire the real
	// reload path (same as gateway.go's boot-time SetReloadFunc(reloadTrigger))
	// so this test exercises the actual delete-then-404 contract, matching
	// the established pattern already used elsewhere in this package (e.g.
	// rest_test.go, rest_auth_test.go) for tests that care about reload
	// semantics rather than wiring a no-op.
	api.agentLoop.SetReloadFunc(func() error {
		// Mirror gateway.go's real reloadTrigger: clear the pending flag once
		// this reload completes (defer runs even on error) so
		// triggerReloadAndWait's poll loop returns immediately instead of
		// spinning for its full 5-second deadline waiting for a flag that
		// TriggerReload itself only clears on the ERROR path.
		defer api.agentLoop.ClearReloadPending()
		return api.refreshConfigAndRewireServices(api.configPath())
	})

	// Create a custom agent first so we have something to delete. model + soul
	// are required on POST (mirrors the create-agent contract: soul is
	// MANDATORY EVERYWHERE per agent-form-requirements.md §4.7).
	createW := httptest.NewRecorder()
	createR := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/agents",
		strings.NewReader(
			`{"name": "Deletable", "type": "Main", "model": "claude-sonnet-4-6", "soul": "I am deletable."}`,
		),
	)
	createR.Header.Set("Content-Type", "application/json")
	createR.URL.Path = "/api/v1/agents"
	api.HandleAgents(createW, createR)
	require.Equal(t, http.StatusCreated, createW.Code,
		"create step failed: body=%s", createW.Body.String())

	var created gen.Agent
	require.NoError(t, json.Unmarshal(createW.Body.Bytes(), &created))
	require.NotEmpty(t, created.Id)

	// Delete the agent.
	delW := httptest.NewRecorder()
	delR := httptest.NewRequest(http.MethodDelete, "/api/v1/agents/"+created.Id+"?revision="+created.Revision, nil)
	delR.URL.Path = "/api/v1/agents/" + created.Id
	api.HandleAgents(delW, delR)
	require.Equal(t, http.StatusOK, delW.Code, "delete body=%s", delW.Body.String())
	var deletion gen.ConfigurationMutationState
	require.NoError(t, json.Unmarshal(delW.Body.Bytes(), &deletion))
	assert.Equal(t, gen.ConfigurationMutationStatePersistenceStatusComplete, deletion.PersistenceStatus)
	assert.Equal(t, gen.ConfigurationMutationStateActivationStatusActive, deletion.ActivationStatus)
	assert.Equal(t, []string{"entity", "soul"}, deletion.ChangedFields)
	assert.Regexp(t, "^[0-9a-f]{64}$", deletion.Revision)
	if _, err := os.Stat(filepath.Join(api.homePath, "agents", created.Id, "SOUL.md")); !os.IsNotExist(err) {
		t.Fatalf("SOUL.md stat error=%v want not exist", err)
	}

	// GET on the same id should now return 404.
	getW := httptest.NewRecorder()
	getR := httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+created.Id, nil)
	getR.URL.Path = "/api/v1/agents/" + created.Id
	api.HandleAgents(getW, getR)
	assert.Equal(t, http.StatusNotFound, getW.Code,
		"GET on the deleted agent must return 404")
}

// TestHandleAgentsDelete_LockedForbidden verifies DELETE on a locked core
// agent returns 403 with the `agent_locked` error code.
//
// BDD: Given a locked (core/system) agent exists, When DELETE /api/v1/agents/
// {id} is called, Then the response is 403 with body { error, code: "agent_locked" }.
// Traces to: agent-form-requirements.md §6.1 — "locked core agents cannot be
// deleted". Hard requirement: built-ins MUST be protected.
func TestHandleAgentsDelete_LockedForbidden(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	// `seedTestAgents` (called by newTestRestAPI) seeds the system agent +
	// core agents. `mia` is locked (built-in roster) — use it.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/agents/mia", nil)
	r.URL.Path = "/api/v1/agents/mia"
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusForbidden, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "agent_locked", resp["code"],
		"locked-agent 403 must surface code=agent_locked so the SPA can distinguish it from generic forbidden")
	assert.Contains(t, resp["error"], "locked")
}

// TestHandleAgentsDelete_NotFound verifies DELETE on a non-existent agent
// returns 404.
//
// BDD: Given no agent exists with the given id, When DELETE /api/v1/agents/
// {id} is called, Then the response is 404.
func TestHandleAgentsDelete_NotFound(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/agents/does-not-exist", nil)
	r.URL.Path = "/api/v1/agents/does-not-exist"
	api.HandleAgents(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestHandleAgentsCreate_201IsJSON drives the real 201 handler end-to-end
// (not just the writeJSON helper) and asserts the Content-Type, guarding #96 at
// the handler layer: the bug was a handler calling w.WriteHeader(201) before
// jsonOK, so a future edit reintroducing that ordering must fail a test that
// exercises the handler — the helper-level test alone would stay green.
func TestHandleAgentsCreate_201IsJSON(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/agents",
		strings.NewReader(`{"type":"Main","name":"Scout","soul":"Scout soul"}`),
	)
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body=%s)", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("201 Content-Type = %q, want application/json (#96)", ct)
	}
	var resp gen.Agent
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("201 body is not valid JSON: %v (body=%s)", err, w.Body.String())
	}
	if resp.Name != "Scout" {
		t.Errorf("created agent name = %q, want Scout", resp.Name)
	}
}

// TestDeleteAgent_EndsTheDeletedAgentsActiveGoals is UAT E-3's wiring oracle:
// DELETE /api/v1/agents/{id} must end the active chat goals that agent was
// working — an honest `cleared` transition naming the agent, the record
// retained — instead of leaving them active for the keeper to push at an agent
// that no longer exists.
func TestDeleteAgent_EndsTheDeletedAgentsActiveGoals(t *testing.T) {
	api := buildExecutorTestAPI(t)
	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store, "harness must provide a session store")
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "test-agent")
	require.NoError(t, err)

	gs := goal.NewStore(config.OmnipusHomeDir())
	now := time.Now().UTC()
	g, err := goal.New(gen.GoalOwnerKindSession, meta.ID, gen.GoalSourceChatCompiled,
		"write e3-marker.txt with three made-up octopus facts", "", nil,
		[]task.AcceptanceCriterion{{
			Kind: task.KindProse, Judgment: task.JudgmentBoolean, Provenance: task.ProvenanceFloor,
			Text: "No secrets appear in the output.", Author: task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: "test-agent"},
		}}, 20, now)
	require.NoError(t, err)
	require.NoError(t, gs.Create(g))
	_, err = gs.Update(g.GoalID, func(cur *goal.Goal) error { return cur.Activate(meta.ID, now) })
	require.NoError(t, err)

	w := httptest.NewRecorder()
	api.HandleAgents(w, revisionedDeleteRequest(t, api, "test-agent"))
	require.Equal(t, http.StatusOK, w.Code, "delete must succeed: %s", w.Body.String())

	after, err := gs.Get(g.GoalID)
	require.NoError(t, err, "the goal record must be retained, not erased")
	assert.Equal(t, gen.GoalStateCleared, after.State, "the deleted agent's goal must not stay active")
	assert.True(t, strings.Contains(after.TerminalReason, "test-agent") && strings.Contains(after.TerminalReason, "deleted"),
		"terminal reason %q must name the deleted agent", after.TerminalReason)
}

// TestListExecutorDefaults_ReturnsAllThreeCLIs proves the endpoint returns
// exactly one entry per supported subagent_3p external CLI, each with a
// non-empty, ordered flag list and a non-empty note.
func TestListExecutorDefaults_ReturnsAllThreeCLIs(t *testing.T) {
	api := executorDefaultsTestAPI(t)
	code, entries := getExecutorDefaults(t, api)
	require.Equal(t, http.StatusOK, code)
	require.Len(t, entries, 3)

	seen := map[gen.ExternalCliTool]gen.ExecutorDefaults{}
	for _, e := range entries {
		seen[e.Cli] = e
		assert.NotEmpty(t, e.AutoAppliedFlags, "cli %q must have a non-empty flag list", e.Cli)
		assert.NotEmpty(t, e.Notes, "cli %q must have a non-empty notes field", e.Cli)
	}
	for _, cli := range []gen.ExternalCliTool{
		gen.ExternalCliToolClaudeCode,
		gen.ExternalCliToolCodex,
		gen.ExternalCliToolOpencode,
	} {
		_, ok := seen[cli]
		assert.True(t, ok, "expected an entry for cli %q", cli)
	}
}

// TestListExecutorDefaults_ClaudeMatchesRealBuildArgs cross-checks the claude
// entry against the REAL ClaudeDriver.buildArgs() output (ADR-032 fix C/D,
// issue #488): -p, --output-format stream-json, --verbose, --no-chrome, a
// conditional --model, --dangerously-skip-permissions (unconditional as of
// 2026-07-05, reversing the original FR-5.3/US-5 acceptEdits stance), and a
// conditional --max-turns — in that order, verified against a live call with
// both conditional fields set. A second call with neither field set confirms
// both stay ABSENT, matching the endpoint's "(only when ...)" documentation.
func TestListExecutorDefaults_ClaudeMatchesRealBuildArgs(t *testing.T) {
	api := executorDefaultsTestAPI(t)
	_, entries := getExecutorDefaults(t, api)
	claude := findExecutorDefaultsEntry(t, entries, gen.ExternalCliToolClaudeCode)
	require.NotEmpty(t, claude.AutoAppliedFlags)

	configured := runner.BuildClaudeArgs(runner.RunOptions{
		Input: "task", Model: "test-model-x", MaxTurns: 7,
	})
	assertRealArgsMatchEndpoint(t, "claude", configured, claude.AutoAppliedFlags)

	found := false
	for _, a := range configured {
		if a == "--dangerously-skip-permissions" {
			found = true
		}
	}
	assert.True(t, found, "issue #488: claude driver now passes --dangerously-skip-permissions unconditionally")

	bare := runner.BuildClaudeArgs(runner.RunOptions{Input: "task"})
	for _, a := range bare {
		assert.NotEqual(t, "--model", a, "conditional --model must be absent when RunOptions.Model is empty")
		assert.NotEqual(t, "--max-turns", a, "conditional --max-turns must be absent when RunOptions.MaxTurns is 0")
	}
}

// TestListExecutorDefaults_CodexMatchesRealBuildArgs cross-checks the codex
// entry against the REAL CodexDriver.buildArgs() output: --ask-for-approval
// never MUST precede the exec subcommand (a global codex flag), --json,
// --sandbox workspace-write, --skip-git-repo-check, --color never all
// follow, then the conditional -m and -C flags — verified against a live
// call with model and working directory both set. A second call with
// neither set confirms both stay ABSENT.
// --dangerously-bypass-approvals-and-sandbox must never appear (FR-5.3).
func TestListExecutorDefaults_CodexMatchesRealBuildArgs(t *testing.T) {
	api := executorDefaultsTestAPI(t)
	_, entries := getExecutorDefaults(t, api)
	codex := findExecutorDefaultsEntry(t, entries, gen.ExternalCliToolCodex)
	require.NotEmpty(t, codex.AutoAppliedFlags)

	configured := runner.BuildCodexArgs(runner.RunOptions{
		Input: "task", Model: "gpt-5-codex", WorkDir: "/tmp/agent-workdir",
	})
	assertRealArgsMatchEndpoint(t, "codex", configured, codex.AutoAppliedFlags)

	for _, a := range configured {
		assert.NotEqual(t, "--dangerously-bypass-approvals-and-sandbox", a,
			"FR-5.3: codex driver never passes --dangerously-bypass-approvals-and-sandbox")
	}

	// Flag-ordering regression (ADR-032): --ask-for-approval is a GLOBAL codex
	// flag and errors if placed after `exec`; --sandbox is an exec-subcommand
	// flag and must follow it.
	execIdx := indexAtOrAfter(configured, 0, "exec")
	approvalIdx := indexAtOrAfter(configured, 0, "--ask-for-approval")
	sandboxIdx := indexAtOrAfter(configured, 0, "--sandbox")
	require.NotEqual(t, -1, execIdx, "expected \"exec\" subcommand in real argv; got %v", configured)
	require.NotEqual(t, -1, approvalIdx, "expected --ask-for-approval in real argv; got %v", configured)
	require.NotEqual(t, -1, sandboxIdx, "expected --sandbox in real argv; got %v", configured)
	assert.Less(t, approvalIdx, execIdx, "--ask-for-approval must precede the exec subcommand; got %v", configured)
	assert.Greater(t, sandboxIdx, execIdx, "--sandbox must follow the exec subcommand; got %v", configured)

	bare := runner.BuildCodexArgs(runner.RunOptions{Input: "task"})
	for _, a := range bare {
		assert.NotEqual(t, "-m", a, "conditional -m must be absent when RunOptions.Model is empty")
		assert.NotEqual(t, "-C", a, "conditional -C must be absent when RunOptions.WorkDir is empty")
	}
}

// TestListExecutorDefaults_OpencodeMatchesRealBuildArgs cross-checks the
// opencode entry against the REAL OpencodeDriver.buildArgs() output: run
// --format json, a conditional --model (only for a "provider/model"-shaped
// value), --dangerously-skip-permissions (the deliberate ADR-032 fix D
// posture for THIS CLI only), and the trailing "--" end-of-options separator
// as the LAST auto-applied entry. A second call with a bare (non-"provider/
// model"-shaped) model value confirms --model stays ABSENT.
func TestListExecutorDefaults_OpencodeMatchesRealBuildArgs(t *testing.T) {
	api := executorDefaultsTestAPI(t)
	_, entries := getExecutorDefaults(t, api)
	opencode := findExecutorDefaultsEntry(t, entries, gen.ExternalCliToolOpencode)
	require.NotEmpty(t, opencode.AutoAppliedFlags)

	configured := runner.BuildOpencodeArgs(runner.RunOptions{
		Input: "task", Model: "anthropic/claude-3-5-sonnet",
	})
	assertRealArgsMatchEndpoint(t, "opencode", configured, opencode.AutoAppliedFlags)

	last := opencode.AutoAppliedFlags[len(opencode.AutoAppliedFlags)-1]
	assert.Equal(t, "--", last, "the -- end-of-options separator must be the last advertised AUTO-APPLIED flag (N-1)")
	// The real argv has ONE more token after "--": the prompt itself
	// (opts.Input), delivered as the positional argument immediately
	// following the separator — this is intentional (see buildArgs' own
	// doc comment) and is documented in the endpoint's "notes" field, not
	// the auto_applied_flags list, so it is correctly absent from
	// opencode.AutoAppliedFlags. Assert the REAL argv's shape matches that
	// design exactly: "--" is the second-to-last token, and the prompt is
	// the last.
	sepIdx := indexAtOrAfter(configured, 0, "--")
	require.NotEqual(t, -1, sepIdx, "expected \"--\" end-of-options separator in real argv; got %v", configured)
	require.Equal(t, len(configured)-2, sepIdx,
		"\"--\" must be the second-to-last token, immediately followed by the prompt only; got %v", configured)
	assert.Equal(t, "task", configured[len(configured)-1],
		"the prompt (opts.Input) must be the single positional argument after \"--\"")

	bareModel := runner.BuildOpencodeArgs(runner.RunOptions{Input: "task", Model: "not-provider-shaped"})
	for _, a := range bareModel {
		assert.NotEqual(t, "--model", a,
			"a Model not shaped like \"provider/model\" must be omitted, not passed as garbage")
	}
}

// TestListExecutorDefaults_MethodNotAllowed proves POST is rejected — this
// is read-only reference data, not a writable resource.
func TestListExecutorDefaults_MethodNotAllowed(t *testing.T) {
	api := executorDefaultsTestAPI(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents/executor-defaults", nil)
	api.HandleAgents(w, r)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// TestListExecutorDefaults_DoesNotShadowAgentLookup proves "executor-defaults"
// as a reserved static path segment does not accidentally swallow requests
// for a real agent whose ID happens to be a normal (different) string — i.e.
// the generic agent GET path still works for an unrelated agent ID.
func TestListExecutorDefaults_DoesNotShadowAgentLookup(t *testing.T) {
	api := executorDefaultsTestAPI(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents/some-other-agent-id", nil)
	api.HandleAgents(w, r)
	// Not found (no such agent) — proves this fell through to the generic
	// getAgent path rather than being intercepted as executor-defaults.
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// --- E9: createAgent concurrency test ---

// TestHandleAgentsCreateConcurrent verifies that concurrent POST /api/v1/agents
// requests all succeed (each creates a distinct agent).
// BDD: Given concurrent POST requests to /agents,
// When all are handled simultaneously,
// Then each receives 201 Created.
// Traces to: wave5a-wire-ui-spec.md — Scenario: createAgent concurrency safe (E9)
func TestHandleAgentsCreateConcurrent(t *testing.T) {
	// Use newTestRestAPIWithHome so safeUpdateConfigJSON writes to a temp dir,
	// not the committed pkg/gateway/config.json test fixture.
	api := newTestRestAPIWithHome(t)

	const n = 5
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/agents",
				strings.NewReader(fmt.Sprintf(`{"name":"Agent X","type":"Main","soul":"Agent %d soul"}`, idx)))
			r.Header.Set("Content-Type", "application/json")
			api.HandleAgents(w, r)
			codes[idx] = w.Code
		}(i)
	}
	wg.Wait()

	for i, code := range codes {
		assert.Equal(t, http.StatusCreated, code,
			"concurrent POST /agents[%d] must return 201", i)
	}
}

// TestGetAgentMailbox_NotConfigured404 verifies getAgentMailbox's
// "no mailbox for this agent" branch. getAgentMailbox has THREE distinct 404
// causes in sequence: (1) !a.agentExists(agentID) → "agent %q not found",
// (2) cfg.Mailboxes[agentID] missing, (3) byWorkspace[workspaceID] missing —
// (2) and (3) share the same "no mailbox configured..." message but (1) is
// a DIFFERENT message. "mia" IS a real agent in this fixture (agentExists
// resolves it via cfg.Agents.List), so the only reachable branch here is (2);
// pin the actual message so a regression that broke agent resolution (which
// would silently reroute this test through cause (1) instead) is caught
// rather than masked by the shared 404 status.
func TestGetAgentMailbox_NotConfigured404(t *testing.T) {
	api := newMailboxTestAPI(t, nil)
	w := httptest.NewRecorder()
	api.getAgentMailbox(w, "mia", "ws_my")
	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "no mailbox configured",
		"the 404 must be the no-mailbox-configured branch, not a masked agent-not-found")
}

// TestGetAgentMailbox_WrongWorkspace404 mirrors TestGetAgentMailbox_NotConfigured404
// but for cause (3) above: the agent has a mailbox in ws_my, but requesting it
// under a different workspace must 404 — the pair, not just the agent, must
// match. Pin the message for the same reason: this must not be silently
// satisfied by an "agent not found" regression.
func TestGetAgentMailbox_WrongWorkspace404(t *testing.T) {
	// The agent has a mailbox in ws_my, but requesting it under a different
	// workspace must 404 — the pair, not just the agent, must match.
	api := newMailboxTestAPI(t, map[string]map[string]config.MailboxConfig{
		"mia": {"ws_my": {
			Enabled: true, WorkspaceID: "ws_my", IMAPHost: "imap.x.com",
			SMTPHost: "smtp.x.com", Username: "me@x.com", PasswordRef: "mailbox_mia_ws_my_password",
		}},
	})
	w := httptest.NewRecorder()
	api.getAgentMailbox(w, "mia", "ws_other")
	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "no mailbox configured",
		"the 404 must be the wrong-workspace branch, not a masked agent-not-found")
}

func TestGetAgentMailbox_ReturnsConfigNoSecret(t *testing.T) {
	api := newMailboxTestAPI(t, map[string]map[string]config.MailboxConfig{
		"mia": {"ws_my": {
			Enabled: true, WorkspaceID: "ws_my", IMAPHost: "imap.x.com",
			SMTPHost: "smtp.x.com", Username: "me@x.com", PasswordRef: "mailbox_mia_ws_my_password",
		}},
	})
	// Store the password so `configured` resolves true.
	require.NoError(t, api.credStore.Set("mailbox_mia_ws_my_password", "secret"))

	w := httptest.NewRecorder()
	api.getAgentMailbox(w, "mia", "ws_my")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var resp gen.Mailbox
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "mia", resp.AgentId)
	assert.True(t, resp.Configured)
	require.NotNil(t, resp.Username)
	assert.Equal(t, "me@x.com", *resp.Username)
	assert.NotContains(t, w.Body.String(), "secret", "password must never be returned")
}

func TestDeleteAgentMailbox(t *testing.T) {
	api := newMailboxTestAPI(t, map[string]map[string]config.MailboxConfig{
		"mia": {"ws_my": {
			Enabled: true, WorkspaceID: "ws_my", IMAPHost: "i", SMTPHost: "s",
			Username: "u", PasswordRef: "mailbox_mia_ws_my_password",
		}},
	})
	require.NoError(t, api.credStore.Set("mailbox_mia_ws_my_password", "secret"))
	// Seed the on-disk config with the mailbox so the delete write has something to remove.
	require.NoError(t, api.safeUpdateConfigJSON(func(m map[string]any) error {
		m["mailboxes"] = map[string]any{
			"mia": map[string]any{"ws_my": map[string]any{"enabled": true, "workspace_id": "ws_my"}},
		}
		return nil
	}))

	w := httptest.NewRecorder()
	api.deleteAgentMailbox(w, "mia", "ws_my")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	// Credential deleted.
	_, err := api.credStore.Get("mailbox_mia_ws_my_password")
	require.Error(t, err, "mailbox password must be removed from the store")

	// config.json: the agent's outer key is removed entirely (its only pair emptied).
	raw, err := os.ReadFile(filepath.Join(api.homePath, "config.json"))
	require.NoError(t, err)
	var disk map[string]any
	require.NoError(t, json.Unmarshal(raw, &disk))
	if mbs, ok := disk["mailboxes"].(map[string]any); ok {
		_, exists := mbs["mia"]
		assert.False(t, exists, "agent's mailbox entry must be removed once its last pair is deleted")
	}
}

func TestDeleteAgentMailbox_OnePairLeavesOtherIntact(t *testing.T) {
	// Deleting one (agent, workspace) pair must not disturb a second pair for
	// the SAME agent in a different workspace.
	api := newMailboxTestAPI(t, map[string]map[string]config.MailboxConfig{
		"mia": {
			"ws_a": {
				Enabled:     true,
				WorkspaceID: "ws_a",
				IMAPHost:    "i",
				SMTPHost:    "s",
				Username:    "a@x.com",
				PasswordRef: "mailbox_mia_ws_a_password",
			},
			"ws_b": {
				Enabled:     true,
				WorkspaceID: "ws_b",
				IMAPHost:    "i",
				SMTPHost:    "s",
				Username:    "b@x.com",
				PasswordRef: "mailbox_mia_ws_b_password",
			},
		},
	})
	require.NoError(t, api.credStore.Set("mailbox_mia_ws_a_password", "secret-a"))
	require.NoError(t, api.credStore.Set("mailbox_mia_ws_b_password", "secret-b"))
	require.NoError(t, api.safeUpdateConfigJSON(func(m map[string]any) error {
		m["mailboxes"] = map[string]any{
			"mia": map[string]any{
				"ws_a": map[string]any{
					"enabled":      true,
					"workspace_id": "ws_a",
					"password_ref": "mailbox_mia_ws_a_password",
				},
				"ws_b": map[string]any{
					"enabled":      true,
					"workspace_id": "ws_b",
					"password_ref": "mailbox_mia_ws_b_password",
				},
			},
		}
		return nil
	}))

	w := httptest.NewRecorder()
	api.deleteAgentMailbox(w, "mia", "ws_a")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	// ws_a's credential is gone, ws_b's survives.
	_, err := api.credStore.Get("mailbox_mia_ws_a_password")
	require.Error(t, err, "ws_a mailbox password must be removed from the store")
	gotB, err := api.credStore.Get("mailbox_mia_ws_b_password")
	require.NoError(t, err, "ws_b mailbox password must survive deleting ws_a")
	assert.Equal(t, "secret-b", gotB)

	// GET confirms: ws_a 404s, ws_b still resolves.
	w = httptest.NewRecorder()
	api.getAgentMailbox(w, "mia", "ws_a")
	require.Equal(t, http.StatusNotFound, w.Code)

	w = httptest.NewRecorder()
	api.getAgentMailbox(w, "mia", "ws_b")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	// config.json: agent entry survives with only ws_b.
	raw, err := os.ReadFile(filepath.Join(api.homePath, "config.json"))
	require.NoError(t, err)
	var disk map[string]any
	require.NoError(t, json.Unmarshal(raw, &disk))
	mailboxesMap, mailboxesMapOk := disk["mailboxes"].(map[string]any)
	require.True(t, mailboxesMapOk, "config.mailboxes must be an object")
	agentEntry, agentEntryOk := mailboxesMap["mia"].(map[string]any)
	require.True(t, agentEntryOk, "config.mailboxes.mia must be an object")
	_, hasA := agentEntry["ws_a"]
	assert.False(t, hasA, "ws_a pair must be removed from config.json")
	_, hasB := agentEntry["ws_b"]
	assert.True(t, hasB, "ws_b pair must survive")
}

func TestDeleteAgentMailbox_AlsoRemovesLegacyCredential(t *testing.T) {
	// A pre-pair-addressing install may still have a blob under the OLD
	// per-agent key ("mailbox_<agentID>_password"). Deleting a pair must also
	// attempt to clean that up so migrated installs don't orphan it. A
	// missing legacy entry (the common case) must not be treated as an error.
	api := newMailboxTestAPI(t, map[string]map[string]config.MailboxConfig{
		"mia": {"ws_my": {
			Enabled: true, WorkspaceID: "ws_my", IMAPHost: "i", SMTPHost: "s",
			Username: "u", PasswordRef: "mailbox_mia_password", // legacy ref, still honored as-stored
		}},
	})
	require.NoError(t, api.credStore.Set("mailbox_mia_password", "legacy-secret"))
	require.NoError(t, api.safeUpdateConfigJSON(func(m map[string]any) error {
		m["mailboxes"] = map[string]any{
			"mia": map[string]any{
				"ws_my": map[string]any{
					"enabled":      true,
					"workspace_id": "ws_my",
					"password_ref": "mailbox_mia_password",
				},
			},
		}
		return nil
	}))

	w := httptest.NewRecorder()
	api.deleteAgentMailbox(w, "mia", "ws_my")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	_, err := api.credStore.Get("mailbox_mia_password")
	require.Error(t, err, "legacy mailbox password must be removed from the store")
}

func TestDeleteAgentMailbox_MalformedMixedEntry500(t *testing.T) {
	api := newMailboxTestAPI(t, map[string]map[string]config.MailboxConfig{
		// Live cfg (used by the pre-flight existence check) has a normal pair…
		"mia": {"ws_my": {Enabled: true, WorkspaceID: "ws_my"}},
	})
	// …but the raw on-disk config.json entry is malformed (mixed legacy/
	// nested). See TestSetAgentMailbox_MalformedMixedEntry500 for why this is
	// written directly rather than via safeUpdateConfigJSON.
	before := `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{},` +
		`"mailboxes":{"mia":{"ws_other":{"enabled":true,"workspace_id":"ws_other"},"enabled":true}}}`
	require.NoError(t, os.WriteFile(filepath.Join(api.homePath, "config.json"), []byte(before), 0o600))

	w := httptest.NewRecorder()
	api.deleteAgentMailbox(w, "mia", "ws_my")
	require.Equal(t, http.StatusInternalServerError, w.Code, "body=%s", w.Body.String())
	var errResp gen.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Contains(t, errResp.Error, `mailboxes entry for agent "mia" is malformed`)

	after, err := os.ReadFile(filepath.Join(api.homePath, "config.json"))
	require.NoError(t, err)
	assert.Equal(t, before, string(after), "malformed entry must abort the write with nothing persisted")
}

// --- Route-level dispatch through HandleAgents (fix 7: rest.go's
// SplitN/validateEntityID mailbox routing had zero coverage) ---

func TestHandleAgents_MailboxRoute_GetPutDelete(t *testing.T) {
	api := newMailboxTestAPI(t, nil)
	seedWorkspaceFile(t, api.homePath, "ws_my")

	// PUT via the real dispatcher.
	body := `{"enabled":true,"imap_host":"imap.x.com","smtp_host":"smtp.x.com","username":"me@x.com","password":"app-pass-123"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/mia/mailboxes/ws_my", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusOK, w.Code, "PUT body=%s", w.Body.String())

	// GET via the real dispatcher.
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodGet, "/api/v1/agents/mia/mailboxes/ws_my", nil)
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusOK, w.Code, "GET body=%s", w.Body.String())
	var resp gen.Mailbox
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "mia", resp.AgentId)
	assert.Equal(t, "ws_my", resp.WorkspaceId)

	// DELETE via the real dispatcher.
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodDelete, "/api/v1/agents/mia/mailboxes/ws_my", nil)
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusOK, w.Code, "DELETE body=%s", w.Body.String())

	// GET after DELETE → 404 (round-trip confirms the delete really persisted).
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodGet, "/api/v1/agents/mia/mailboxes/ws_my", nil)
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandleAgents_MailboxRoute_BareMailboxesPath400(t *testing.T) {
	api := newMailboxTestAPI(t, nil)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents/mia/mailboxes", nil)
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
}

func TestHandleAgents_MailboxRoute_ExtraSegment400(t *testing.T) {
	api := newMailboxTestAPI(t, nil)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents/mia/mailboxes/ws_my/extra", nil)
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
}

func TestHandleAgents_MailboxRoute_InvalidWorkspaceIDChars400(t *testing.T) {
	api := newMailboxTestAPI(t, nil)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents/mia/mailboxes/x", nil)
	// Set the raw path directly so the ".." survives without any URL-parse
	// normalization ambiguity — HandleAgents reads r.URL.Path verbatim.
	r.URL.Path = "/api/v1/agents/mia/mailboxes/.."
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
}

// TestDeleteAgentMailbox_ReloadCompletesBeforeResponse proves site 4:
// deleteAgentMailbox must not report success until the registry rebuild that
// actually deregisters the removed mailbox's email tools has completed.
// Before the fix, the running agent instance could keep the email tools live
// (registered at the PRIOR construction, before the mailbox was removed) for
// as long as the async rebuild took to run, even though the handler already
// reported the mailbox gone.
func TestDeleteAgentMailbox_ReloadCompletesBeforeResponse(t *testing.T) {
	api := newMailboxTestAPI(t, map[string]map[string]config.MailboxConfig{
		"mia": {"ws_my": {
			Enabled: true, WorkspaceID: "ws_my", IMAPHost: "i", SMTPHost: "s",
			Username: "u", PasswordRef: "mailbox_mia_ws_my_password",
		}},
	})
	require.NoError(t, api.credStore.Set("mailbox_mia_ws_my_password", "secret"))
	require.NoError(t, api.safeUpdateConfigJSON(func(m map[string]any) error {
		m["mailboxes"] = map[string]any{
			"mia": map[string]any{"ws_my": map[string]any{"enabled": true, "workspace_id": "ws_my"}},
		}
		return nil
	}))
	wireAsyncReload(t, api, 30*time.Millisecond)

	w := httptest.NewRecorder()
	api.deleteAgentMailbox(w, "mia", "ws_my")

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	assert.False(t, api.agentLoop.IsReloadPending(),
		"deleteAgentMailbox reported success while a config reload was still pending — the removed "+
			"mailbox's email tools could remain registered on the running agent instance")
}

// TestDeleteAgent_OwningActivePlan_Rejected verifies the agent-delete guard
// (rest.go's deleteAgent): an agent that owns >=1 running plan cannot be
// deleted.
func TestDeleteAgent_OwningActivePlan_Rejected(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Owner WS")

	// Wire a real PlanEngine so HasActivePlansOwnedBy is reachable — mirrors
	// gateway.go's boot wiring (agent.NewPlanEngine + SetPlanEngine), but
	// without Start()ing background goroutines (HasActivePlansOwnedBy is a
	// synchronous store scan, not engine-loop-dependent).
	pe := agent.NewPlanEngine(api.agentLoop, api.planStore, api.taskStore, api.taskExecutor)
	api.agentLoop.SetPlanEngine(pe)
	// This SECOND engine displaces the harness's own in the loop, so the
	// harness cleanup's pe.Stop() drains that one, not this one. Drain this
	// one too — it is a live engine bound to the same stores and the same
	// tmpDir, and any wake it dispatches outlives the test otherwise.
	// Registered here (later than the harness's) so it runs first; Stop on an
	// engine that dispatched nothing is a zero-counter wait.
	t.Cleanup(pe.Stop)

	wCreate := postPlan(t, api, wsID,
		`{"workspace_id":"`+wsID+`","title":"Owned plan","owner_agent_id":"`+testPlansAgentID+`"}`)
	require.Equal(t, http.StatusCreated, wCreate.Code)
	var p gen.Plan
	require.NoError(t, json.Unmarshal(wCreate.Body.Bytes(), &p))

	// A plan needs at least one member to be approvable (plan-lint's
	// LintEmptyPlan). This test's subject is agent deletion, not approval —
	// the member is fixture, not assertion.
	mustCreateTask(t, api, wsID, "owned plan member", p.Id)

	require.Equal(t, http.StatusOK, postPlanAction(t, api, p.Id, "approve").Code)
	// PUT can no longer set state at all (ADR-052 FR-007/A1) — drive the
	// approved->running transition directly via the store instead.
	running := plan.StateRunning
	_, rerr := api.planStore.Update(p.Id, plan.Patch{State: &running})
	require.NoError(t, rerr)

	hasActive, haErr := pe.HasActivePlansOwnedBy(testPlansAgentID)
	require.NoError(t, haErr)
	assert.True(t, hasActive)

	wDel := httptest.NewRecorder()
	rDel := httptest.NewRequest(http.MethodDelete, "/api/v1/agents/"+testPlansAgentID, nil)
	rDel.URL.Path = "/api/v1/agents/" + testPlansAgentID
	api.HandleAgents(wDel, rDel)
	assert.Equal(t, http.StatusBadRequest, wDel.Code, "body=%s", wDel.Body.String())

	var body map[string]any
	require.NoError(t, json.Unmarshal(wDel.Body.Bytes(), &body))
	assert.Equal(t, "agent_owns_active_plans", body["code"])
}

// TestDeleteAgent_PlanStoreListError_FailsClosed is the fix-wave regression
// for finding 1 (14-reviewer sign-off): when HasActivePlansOwnedBy cannot
// determine plan ownership (a plan-store List() error), the delete-guard
// must refuse the delete (503) rather than silently treating "unknown" as
// "no active plans" and letting a possibly-owning agent be deleted out from
// under a live plan.
func TestDeleteAgent_PlanStoreListError_FailsClosed(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Owner WS 2")

	pe := agent.NewPlanEngine(api.agentLoop, api.planStore, api.taskStore, api.taskExecutor)
	api.agentLoop.SetPlanEngine(pe)
	t.Cleanup(pe.Stop)

	wCreate := postPlan(t, api, wsID,
		`{"workspace_id":"`+wsID+`","title":"Owned plan 2","owner_agent_id":"`+testPlansAgentID+`"}`)
	require.Equal(t, http.StatusCreated, wCreate.Code)
	var p gen.Plan
	require.NoError(t, json.Unmarshal(wCreate.Body.Bytes(), &p))
	// See above: a member is required for approval; the subject here is the
	// plan-store list error, not the member.
	mustCreateTask(t, api, wsID, "owned plan 2 member", p.Id)
	require.Equal(t, http.StatusOK, postPlanAction(t, api, p.Id, "approve").Code)
	running := plan.StateRunning
	_, rerr := api.planStore.Update(p.Id, plan.Patch{State: &running})
	require.NoError(t, rerr)

	// Force plan.Store.List to fail with a genuine (non-ENOENT) read error,
	// UID-independently: root (this repo's CI worker runs as uid=0) has
	// CAP_DAC_OVERRIDE and ignores permission bits entirely, so an
	// os.Chmod(dir, 0o000) injection succeeds in listing anyway on CI while
	// failing correctly on a non-root dev machine — a root-only false RED,
	// not a real product bug (mirrors the os.Getuid()==0 skip-guard pattern
	// used elsewhere, e.g. pkg/tools/write_file_reason_test.go,
	// pkg/agent/list_all_sessions_test.go). Swap the plans directory for a
	// regular file instead: os.ReadDir on a path that is not a directory
	// returns ENOTDIR unconditionally, root included, because it is a
	// path-type error rather than a DAC permission check.
	dir := api.planStore.Dir()
	backupDir := dir + ".bak"
	require.NoError(t, os.Rename(dir, backupDir))
	require.NoError(t, os.WriteFile(dir, []byte("not a directory"), 0o600))
	t.Cleanup(func() {
		_ = os.Remove(dir)
		_ = os.Rename(backupDir, dir)
	})

	wDel := httptest.NewRecorder()
	rDel := httptest.NewRequest(http.MethodDelete, "/api/v1/agents/"+testPlansAgentID, nil)
	rDel.URL.Path = "/api/v1/agents/" + testPlansAgentID
	api.HandleAgents(wDel, rDel)
	assert.Equal(t, http.StatusServiceUnavailable, wDel.Code, "body=%s", wDel.Body.String())
}

// --- HandleAgents tests ---

// TestHandleAgentsListAlwaysIncludesSystemAgent verifies that GET /api/v1/agents
// always includes the omnipus-system agent regardless of config.
// BDD: Given no agents are configured,
// When GET /api/v1/agents is called,
// Then the response includes the system agent with id "omnipus-system".
// Traces to: wave5a-wire-ui-spec.md — Scenario: Agent list always includes system agent (US-6 AC1)
func TestHandleAgentsListAlwaysIncludesSystemAgent(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	var agents []struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &agents))

	found := false
	for _, ag := range agents {
		if ag.ID == "omnipus-system" && ag.Type == "system" {
			found = true
			break
		}
	}
	assert.True(t, found, "system agent must always be present in the agents list")
}

// TestHandleAgentsListIncludesConfiguredAgents verifies custom agents from config appear in the list.
// BDD: Given one custom agent is configured,
// When GET /api/v1/agents is called,
// Then the response includes the system agent plus the custom agent.
// Traces to: wave5a-wire-ui-spec.md — Scenario: Agent list includes configured agents (US-6 AC2)
func TestHandleAgentsListIncludesConfiguredAgents(t *testing.T) {
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
			List: []config.AgentConfig{
				{ID: "my-agent", Name: "My Agent"},
			},
		},
	}
	// Seed omnipus-system and core agents to mirror gateway startup.
	seedTestAgents(cfg)
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	var agents []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &agents))

	assert.GreaterOrEqual(t, len(agents), 2, "must include system agent + custom agent")

	ids := make(map[string]bool)
	for _, ag := range agents {
		ids[ag.ID] = true
	}
	assert.True(t, ids["omnipus-system"], "system agent must be present")
	assert.True(t, ids["my-agent"], "custom agent must be present")
}

// TestHandleAgentsGetByIDSystemAgent verifies GET /api/v1/agents/omnipus-system returns the system agent.
// BDD: Given agent id "omnipus-system",
// When GET /api/v1/agents/omnipus-system is called,
// Then the response has id "omnipus-system" and type "system".
// Traces to: wave5a-wire-ui-spec.md — Scenario: Get agent by ID (US-7 AC1)
func TestHandleAgentsGetByIDSystemAgent(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents/omnipus-system", nil)
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "omnipus-system", resp.ID)
	assert.Equal(t, "system", resp.Type)
}

// TestHandleAgentsGetByIDNotFound verifies GET /api/v1/agents/{unknown} returns 404.
// BDD: Given agent id "does-not-exist",
// When GET /api/v1/agents/does-not-exist is called,
// Then the response has status 404.
// Traces to: wave5a-wire-ui-spec.md — Scenario: Get agent by ID not found (US-7 AC2)
func TestHandleAgentsGetByIDNotFound(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents/does-not-exist", nil)
	api.HandleAgents(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestHandleAgentsCreateValidation verifies POST /api/v1/agents with empty name returns 422.
// Traces to: wave5a-wire-ui-spec.md — A3+A4: agent creation via API
func TestHandleAgentsCreateValidation(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	body := `{"type": "Main", "name": "", "soul": "s"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)

	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "name is required")
}

// TestHandleAgentsCreate verifies POST /api/v1/agents creates an agent and returns 201.
// Traces to: wave5a-wire-ui-spec.md — A3+A4: agent creation via API
func TestHandleAgentsCreate(t *testing.T) {
	// Use newTestRestAPIWithHome so safeUpdateConfigJSON writes to a temp dir,
	// not the committed pkg/gateway/config.json test fixture.
	api := newTestRestAPIWithHome(t)

	body := `{"type": "Main", "name": "Scout", "model": "claude-sonnet-4-6", "soul": "Scout soul"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	assert.Equal(t, http.StatusCreated, w.Code)

	var resp gen.Agent
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "Scout", resp.Name)
	assert.Equal(t, gen.AgentTypeMain, resp.Type)
	assert.NotEmpty(t, resp.Id)
}

// TestHandleAgentsCreateWithExplicitID_Rejected verifies POST /api/v1/agents
// rejects a body carrying a client-supplied "id" field. Superseded by the
// unconditional strict-decode enforcement: "id" is not a property on
// AgentCreateRequestMain (the id is always server-generated via
// uuid.New()), so a caller-supplied "id" key is now a 400 rather than being
// silently dropped by a plain json.Unmarshal. The agent identity contract
// (server always mints its own UUID) is otherwise unaffected — see
// TestHandleAgentsCreate for the happy path.
// Traces to: wave5a-wire-ui-spec.md — A3+A4: agent creation via API
func TestHandleAgentsCreateWithExplicitID_Rejected(t *testing.T) {
	// Use newTestRestAPIWithHome so safeUpdateConfigJSON writes to a temp dir,
	// not the committed pkg/gateway/config.json test fixture.
	api := newTestRestAPIWithHome(t)

	body := `{"id": "my-scout", "type": "Main", "name": "Scout", "soul": "Scout soul"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "id")
	assert.Contains(t, resp["error"], "AgentCreateRequestMain")
}

// TestGetAgentTools_SystemAgent verifies GET /api/v1/agents/omnipus-system/tools returns
// agent_type "system" and a config object.
// BDD: Given the system agent,
// When GET /api/v1/agents/omnipus-system/tools is called,
// Then the response includes agent_type "system", config, and effective_tools.
// Traces to: parsed-inventing-gem.md — PR 2 REST endpoints
func TestGetAgentTools_SystemAgent(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents/omnipus-system/tools", nil)
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		AgentType      string           `json:"agent_type"`
		Config         map[string]any   `json:"config"`
		EffectiveTools []map[string]any `json:"effective_tools"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "system", resp.AgentType)
	assert.NotNil(t, resp.Config)
	assert.Contains(t, resp.Config, "builtin")
}

// TestGetAgentTools_CustomAgent verifies GET /api/v1/agents/{id}/tools for a custom agent.
// BDD: Given a custom agent with tools config,
// When GET /api/v1/agents/{id}/tools is called,
// Then the response includes agent_type "custom" and the stored config.
// Traces to: parsed-inventing-gem.md — PR 2 REST endpoints
func TestGetAgentTools_CustomAgent(t *testing.T) {
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
			List: []config.AgentConfig{
				{
					ID:   "tool-agent",
					Name: "Tool Agent",
					Tools: &config.AgentToolsCfg{
						Builtin: config.AgentBuiltinToolsCfg{
							Policies: map[string]config.ToolPolicy{
								"read_file":  config.ToolPolicyAllow,
								"search_web": config.ToolPolicyAllow,
							},
						},
					},
				},
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents/tool-agent/tools", nil)
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		AgentType string         `json:"agent_type"`
		Config    map[string]any `json:"config"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "Main", resp.AgentType)
	builtin, ok := resp.Config["builtin"].(map[string]any)
	require.True(t, ok)
	// There is no default_policy field any more (CLAUDE.md hard constraint
	// 6) — the response carries only the agent's explicit policies map.
	_, hasDefaultPolicy := builtin["default_policy"]
	assert.False(t, hasDefaultPolicy, "default_policy no longer exists on the wire")
	policies, ok := builtin["policies"].(map[string]any)
	require.True(t, ok, "policies must be a map")
	assert.Equal(t, "allow", policies["read_file"])
	assert.Equal(t, "allow", policies["search_web"])
}

// TestDeleteAgent_SuccessAndLocked403 verifies DELETE /agents/{id}: an unlocked
// custom agent is removed with a truthful state envelope and a locked core agent is rejected with 403
// plus the agent_locked code.
func TestDeleteAgent_SuccessAndLocked403(t *testing.T) {
	// buildExecutorTestAPI seeds a writable config.json with an unlocked custom agent.
	api1 := buildExecutorTestAPI(t)

	w := httptest.NewRecorder()
	r := revisionedDeleteRequest(t, api1, "test-agent")
	api1.HandleAgents(w, r)
	assert.Equal(t, http.StatusOK, w.Code, "custom agent delete must report state")
	var deletion gen.ConfigurationMutationState
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &deletion))
	assert.Equal(t, gen.ConfigurationMutationStatePersistenceStatusComplete, deletion.PersistenceStatus)

	// newTestRestAPI seeds the locked core roster (Mia).
	api2, _ := newTestRestAPI(t)
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodDelete, "/api/v1/agents/mia", nil)
	api2.HandleAgents(w2, r2)
	assert.Equal(t, http.StatusForbidden, w2.Code, "locked agent delete must 403")
	var errResp map[string]any
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &errResp))
	assert.Equal(t, "agent_locked", errResp["code"])
}

func revisionedDeleteRequest(t *testing.T, api *restAPI, id string) *http.Request {
	t.Helper()
	state, err := agentstore.New(api.homePath).ReadState(id)
	require.NoError(t, err)
	return httptest.NewRequest(http.MethodDelete, "/api/v1/agents/"+id+"?revision="+state.Revision, nil)
}

// --- GET /api/v1/agents/{id}/tools round-trip ---

// TestGetAgentTools_ConfigPoliciesCoverFullCatalog closes the loop the write-side
// guards open. The SPA builds its PUT body by spreading the map this GET returns
// and overwriting one key (ToolsAndPermissions.tsx's cfgToValue → valueToCfg).
// If this response reported only the agent's own, possibly-sparse stored map,
// then any agent whose stored map predates a catalog addition — or was emptied
// by the malformed-body defect above — would round-trip an incomplete map and be
// rejected by the very guards added here. Read must therefore return a body that
// is a VALID write.
func TestGetAgentTools_ConfigPoliciesCoverFullCatalog(t *testing.T) {
	const agentID = "01JXTESTAGENTSTARTTEST001"
	api := newTestRestAPIWithAgent(t)
	seedGlobalCeiling(t, api)

	// A deliberately sparse stored map — one entry only.
	store := agentstore.New(api.homePath)
	require.NoError(t, store.Create(agentID, &config.AgentConfig{
		ID:   agentID,
		Name: "Sparse Agent",
		Type: config.AgentTypeCustom,
		Tools: &config.AgentToolsCfg{Builtin: config.AgentBuiltinToolsCfg{
			Policies: map[string]config.ToolPolicy{"bash": config.ToolPolicyDeny},
		}},
	}))
	cfg := api.agentLoop.GetConfig()
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == agentID {
			cfg.Agents.List[i].Tools = &config.AgentToolsCfg{Builtin: config.AgentBuiltinToolsCfg{
				Policies: map[string]config.ToolPolicy{"bash": config.ToolPolicyDeny},
			}}
		}
	}

	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+agentID+"/tools", nil)
	w := httptest.NewRecorder()
	api.HandleAgentToolsRegistry(w, r, agentID)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var resp struct {
		Config struct {
			Builtin struct {
				Policies map[string]string `json:"policies"`
			} `json:"builtin"`
		} `json:"config"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))

	defects := config.ValidateSubmittedToolPolicyMap(
		resp.Config.Builtin.Policies, buildKnownBuiltinToolNames())
	assert.True(t, defects.Empty(),
		"the GET response's config.builtin.policies must itself be an acceptable PUT body, "+
			"or the SPA's read/modify/write cycle breaks: %s", defects.String())
	assert.Equal(t, "deny", resp.Config.Builtin.Policies["bash"],
		"the agent's own explicit entry must be reported as-is, not overwritten by the ceiling")
}
