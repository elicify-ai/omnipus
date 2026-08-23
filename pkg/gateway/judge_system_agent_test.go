// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-049 D3 (Planning & Goals epic): System Agents category + the seeded,
// locked, non-privileged Judge. These gateway tests exercise the lifecycle
// guards (uncreatable / undeletable / undisable-able) and the enumeration
// exclusions (default-fallback / routing-binding / team roster / delegation).

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// newJudgeRosterAPI builds a restAPI whose live config carries the base roster
// PLUS a seeded type:system Judge, so the lifecycle-guard and enumeration-
// exclusion handlers can be exercised without a full boot.
func newJudgeRosterAPI(t *testing.T) *restAPI {
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
				{ID: "mia", Name: "Mia", Type: config.AgentTypeCore, Default: true},
				{ID: "jim", Name: "Jim", Type: config.AgentTypeCore},
				{ID: "ava", Name: "Ava", Type: config.AgentTypeCore},
				{ID: "ray", Name: "Ray", Type: config.AgentTypeCore},
				{ID: "worker", Name: "Worker", Type: config.AgentTypeWorker},
				{
					ID: "judge", Name: "Judge", Type: config.AgentTypeSystem,
					Locked: true,
				},
			},
		},
	}
	cfgJSON, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config.json"), cfgJSON, 0o600))

	// ADR-054 D2: agents are per-entity records under entities/agents/<id>.json,
	// not config.json's agents.list. This fixture's cfg.Agents.List splice
	// above is still the correct, legitimate way to seed the in-memory roster
	// agent.NewAgentLoop/AgentRegistry read at construction time (mirrors
	// rest_mailbox_test.go's newMailboxTestAPI) — but any handler write path
	// that reaches rest.go's withToolPolicyCoverageGuard persist closure does
	// a REAL agentstore.Update(id, ...) read-modify-write against
	// entities/agents/<id>.json, which 404s ("agent no longer exists") if no
	// entity record exists on disk. Reproduced live: TestAgents_JudgeRubricAbsent's
	// "stray rubric field" sub-test (a PUT with no recognized fields at all,
	// against "mia") failed with exactly that 404 before this fixture also
	// persisted a matching entity record for every seeded agent.
	store := agentstore.New(tmpDir)
	for i := range cfg.Agents.List {
		ac := cfg.Agents.List[i]
		require.NoError(t, store.Create(ac.ID, &ac), "seed agentstore entity record for %q", ac.ID)
	}

	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	return &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		homePath:      tmpDir,
		taskStore:     task.New(filepath.Join(tmpDir, "tasks")),
		taskLock:      task.TaskFileLock,
	}
}

// TestAgents_SystemTypeUncreatable verifies POST /api/v1/agents with
// {"type":"system"} is rejected 400 (System Agents are seed-only, ADR-049 D3).
func TestAgents_SystemTypeUncreatable(t *testing.T) {
	api := newJudgeRosterAPI(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents",
		strings.NewReader(`{"type":"system","name":"Rogue Judge","model":"m"}`))
	r.Header.Set("Content-Type", "application/json")
	api.createAgent(w, r)

	require.Equal(t, http.StatusBadRequest, w.Code, "POST type:system must be 400; body=%s", w.Body.String())
	assert.Contains(t, strings.ToLower(w.Body.String()), "not creatable")
}

// TestAgents_JudgeUndeletable verifies DELETE /api/v1/agents/judge is rejected
// 400 (System Agents are non-deletable) — before the generic locked 403.
func TestAgents_JudgeUndeletable(t *testing.T) {
	api := newJudgeRosterAPI(t)

	w := httptest.NewRecorder()
	api.deleteAgent(w, "judge")

	require.Equal(t, http.StatusBadRequest, w.Code, "DELETE judge must be 400; body=%s", w.Body.String())
	assert.Contains(t, strings.ToLower(w.Body.String()), "not deletable")
}

// TestAgents_JudgeUndisable verifies PUT /api/v1/agents/judge with a disable
// intent ({"enabled":false} or {"disabled":true}) is rejected 400.
func TestAgents_JudgeUndisable(t *testing.T) {
	for _, body := range []string{`{"enabled":false}`, `{"disabled":true}`} {
		t.Run(body, func(t *testing.T) {
			api := newJudgeRosterAPI(t)
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/judge", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			api.updateAgent(w, r, "judge")

			require.Equal(t, http.StatusBadRequest, w.Code, "PUT %s must be 400; body=%s", body, w.Body.String())
			assert.Contains(t, strings.ToLower(w.Body.String()), "cannot be disabled")
		})
	}
}

// TestAgents_JudgeRubricAbsent verifies ADR-052 FR-038 (soul unification):
// AgentConfig.Rubric was deleted and the generated wire Agent type no longer
// carries a rubric field at all — the Judge's prompt now lives in its
// SOUL.md, lazily seeded from coreagent.JudgeDefaultRubric by pkg/agent's
// ensureVerifierSoul, not echoed via this REST surface. A PUT carrying a
// stray "rubric" field is silently ignored (unknown field, no dedicated
// guard left — soul-editability is a Wave-2 item) and GET must not render a
// "rubric" key at all.
func TestAgents_JudgeRubricAbsent(t *testing.T) {
	api := newJudgeRosterAPI(t)

	t.Run("stray rubric field on a non-system agent PUT is not rejected as a rubric guard", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/mia", strings.NewReader(`{"rubric":"nope"}`))
		r.Header.Set("Content-Type", "application/json")
		api.updateAgent(w, r, "mia")
		// The rubric-specific 400 guard was deleted along with AgentConfig.Rubric
		// (FIX 1, ADR-052 compile cleanup) — a stray "rubric" key is just an
		// unrecognized field now, not a rejected one.
		require.Equal(t, http.StatusOK, w.Code, "PUT with a stray rubric field must not 400; body=%s", w.Body.String())
	})

	t.Run("Judge GET response carries no rubric field", func(t *testing.T) {
		w := httptest.NewRecorder()
		api.getAgent(w, "judge")
		require.Equal(t, http.StatusOK, w.Code, "GET judge must be 200; body=%s", w.Body.String())

		var raw map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))
		_, hasRubric := raw["rubric"]
		assert.False(t, hasRubric, "GET response must not carry a rubric field (ADR-052 FR-038 deleted it)")

		var ag gen.Agent
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &ag))
		require.Equal(t, gen.AgentTypeSystem, ag.Type, "Judge must render as type:system")
	})
}

// TestSystemAgent_ExcludedFromEnumeration verifies FR-078: a type:system agent
// is excluded from default-agent fallback, routing-binding targets, team
// rosters, and (transitively, via team membership) delegation targets.
func TestSystemAgent_ExcludedFromEnumeration(t *testing.T) {
	api := newJudgeRosterAPI(t)
	cfg := api.agentLoop.GetConfig()

	t.Run("default-agent fallback excludes the System Agent", func(t *testing.T) {
		got := firstChatTargetAgentID(cfg)
		assert.NotEqual(t, "judge", got, "the Judge must never be the first chat-target fallback")
		assert.Equal(t, "mia", got, "the first chat target is a base agent, not the Judge")
	})

	t.Run("workspace team roster excludes the System Agent", func(t *testing.T) {
		team := defaultWorkspaceTeam(cfg)
		assert.NotContains(t, team, "judge", "the default workspace team must exclude the Judge")
		assert.Contains(t, team, "mia", "sanity: the default team still contains base agents")
	})

	t.Run("delegation targets (⊆ team) exclude the System Agent", func(t *testing.T) {
		// Delegation edge endpoints are validated against the workspace team set
		// (core_team ∪ existing-edge endpoints). Build a workspace whose team is
		// the seeded default team and assert the Judge is not a member — so it can
		// never be a delegation target.
		ws := storedWorkspace{ID: "ws1", CoreTeam: defaultWorkspaceTeam(cfg)}
		set := workspaceTeamSet(ws)
		assert.False(t, set["judge"], "the Judge must not be in the workspace team set (no delegation target)")
	})

	t.Run("routing-binding target write rejects the System Agent (400)", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/telegram/routing",
			strings.NewReader(`{"default_agent_id":"judge"}`))
		r.Header.Set("Content-Type", "application/json")
		api.setChannelRouting(w, r, "telegram")
		require.Equal(t, http.StatusBadRequest, w.Code,
			"binding a channel to the Judge must be 400; body=%s", w.Body.String())
		assert.Contains(t, strings.ToLower(w.Body.String()), "system agents are not chat targets")
	})
}
