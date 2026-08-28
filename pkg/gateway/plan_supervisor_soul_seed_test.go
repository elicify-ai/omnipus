// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-055 / plan-supervisor-spec FR-005 + FR-003.
//
// FR-005: the PlanSupervisor's adjudication rubric must REACH DISK as its
// SOUL.md. It exists as coreagent.PlanSupervisorDefaultRubric, but
// coreagent.SeedConfig does zero filesystem I/O by design, and — unlike the
// Judge — FR-005 rev 2 deliberately adds NO lazy backstop (pkg/agent's
// ensureVerifierSoul is Judge-gated and sits on the verifier-dispatch path a
// bus-woken PlanSupervisor never reaches). The gateway's boot-time eager seed
// is therefore the ONLY path that gives the adjudicator a prompt at all: if it
// does not fire, PlanSupervisor wakes with an EMPTY system prompt. Every test
// below asserts the OUTCOME — the bytes on disk, read back independently —
// never that some seeder function was called.
//
// FR-003: no seeded System Agent may be disabled through the agent API. The
// guard used to be an `== IDJudge` id equality test, so the PlanSupervisor —
// the sole holder of the plan-correction grant — could be switched off
// silently.
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

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

// planSupervisorSoulPath resolves the PlanSupervisor's SOUL.md via
// agentWorkspacePath — the resolution the REST read path (getAgent/listAgents)
// uses — deliberately NOT via agent.ResolveAgentHome, which is what the seeder
// itself uses. Asserting against the READER's path is what proves the seed
// lands where an operator actually sees it, rather than merely proving the
// writer agrees with itself.
func planSupervisorSoulPath(t *testing.T, cfg *config.Config, homePath string) string {
	t.Helper()
	ws, err := agentWorkspacePath(cfg, string(coreagent.IDPlanSupervisor), "", homePath)
	require.NoError(t, err, "resolve the PlanSupervisor workspace")
	return filepath.Join(ws, "SOUL.md")
}

// TestSeedSystemAgentEagerSouls_PlanSupervisorRubricReachesDisk is the direct
// regression test for the FR-005 gap: PlanSupervisorDefaultRubric existed only
// as a Go constant that no write path ever materialized, because both seed
// paths were hardcoded to the Judge.
//
// newSeededJudgeAPI runs the REAL boot sequence (coreagent.SeedConfig then
// seedSystemAgentEagerSouls), so this reads the actual file the actual boot
// wrote — no re-implementation, no assertion that a function ran.
func TestSeedSystemAgentEagerSouls_PlanSupervisorRubricReachesDisk(t *testing.T) {
	require.NotEmpty(t, strings.TrimSpace(coreagent.PlanSupervisorDefaultRubric),
		"the rubric constant itself must be non-empty, or 'it reached disk' would be vacuous")

	api := newSeededJudgeAPI(t)
	cfg := api.agentLoop.GetConfig()
	soulPath := planSupervisorSoulPath(t, cfg, api.homePath)

	onDisk, readErr := os.ReadFile(soulPath)
	require.NoError(t, readErr,
		"PlanSupervisor SOUL.md must exist immediately after boot — there is no lazy backstop (FR-005 rev 2), "+
			"so a missing file here means the adjudicator would run on an EMPTY prompt")
	assert.Equal(t, coreagent.PlanSupervisorDefaultRubric, string(onDisk),
		"the on-disk soul must be the compiled adjudication rubric, byte for byte")

	// The same boot must still seed the Judge — generalising the loop must not
	// have traded one hardcoded agent for another.
	judgeWS, wsErr := agentWorkspacePath(cfg, string(coreagent.IDJudge), "", api.homePath)
	require.NoError(t, wsErr)
	judgeSoul, judgeErr := os.ReadFile(filepath.Join(judgeWS, "SOUL.md"))
	require.NoError(t, judgeErr, "the Judge's soul must still be seeded by the same boot")
	assert.Equal(t, coreagent.JudgeDefaultRubric, string(judgeSoul))

	// And the operator must actually SEE it: getAgent reads SOUL.md from the
	// workspace and (ac.IsSystem()) does not blank it out for a locked System
	// Agent, so a fresh install shows the standards it is running under.
	w := httptest.NewRecorder()
	api.getAgent(w, string(coreagent.IDPlanSupervisor))
	require.Equal(t, http.StatusOK, w.Code, "GET plansupervisor; body=%s", w.Body.String())
	var got gen.Agent
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, coreagent.PlanSupervisorDefaultRubric, got.Soul,
		"GET /api/v1/agents/plansupervisor on a fresh install must show the default rubric, not an empty soul")
	assert.Equal(t, gen.AgentTypeSystem, got.Type)
	assert.True(t, got.Locked)
}

// TestSeedSystemAgentEagerSouls_PreservesOperatorEditedPlanSupervisorSoul
// locks the operator-editable contract: seedSystemAgents re-enforces
// identity/type/locked/tool-policy on EVERY boot, but Model/Provider and the
// SOUL are preserved once written. Since the eager seed now runs on every boot
// (not just the first), an overwrite here would silently revert an operator's
// tuned rubric on the next restart.
func TestSeedSystemAgentEagerSouls_PreservesOperatorEditedPlanSupervisorSoul(t *testing.T) {
	api := newSeededJudgeAPI(t)
	cfg := api.agentLoop.GetConfig()
	soulPath := planSupervisorSoulPath(t, cfg, api.homePath)

	// The edit must land on a file the FIRST boot actually seeded — otherwise
	// this test would pass vacuously against a build where the eager seed
	// never writes the PlanSupervisor's soul at all.
	seeded, readErr := os.ReadFile(soulPath)
	require.NoError(t, readErr, "the first boot must have seeded the soul before the operator edits it")
	require.Equal(t, coreagent.PlanSupervisorDefaultRubric, string(seeded))

	const editedSoul = "You are the Plan Supervisor. House rule: never supersede a member on a first failure."
	require.NoError(t, os.WriteFile(soulPath, []byte(editedSoul), 0o644),
		"simulate an operator editing the PlanSupervisor's soul")

	// Simulate a full restart: the config re-seed (tamper protection /
	// identity repair) followed by the eager soul seed, in the same order
	// RunContextWithOptions runs them.
	coreagent.SeedConfig(cfg)
	seedSystemAgentEagerSouls(cfg)

	after, readErr := os.ReadFile(soulPath)
	require.NoError(t, readErr, "SOUL.md must still exist after a restart")
	assert.Equal(t, editedSoul, string(after),
		"a restart must NOT overwrite an operator-edited PlanSupervisor soul with the compiled default")

	// A second restart must be equally inert (the guard is content-based, not
	// a once-only flag).
	coreagent.SeedConfig(cfg)
	seedSystemAgentEagerSouls(cfg)
	afterSecond, readErr := os.ReadFile(soulPath)
	require.NoError(t, readErr)
	assert.Equal(t, editedSoul, string(afterSecond),
		"a second restart must still preserve the operator-edited soul")
}

// TestSeedSystemAgentEagerSouls_PlanSupervisorZeroByteSoulIsBackfilled proves a
// 0-byte SOUL.md (an interrupted write, or a hand-created empty file) counts as
// MISSING, not as "the operator wants an empty prompt" — mirroring the Judge's
// own rule. Without this, the one path that can give the adjudicator a prompt
// would consider a blank file already-seeded forever.
func TestSeedSystemAgentEagerSouls_PlanSupervisorZeroByteSoulIsBackfilled(t *testing.T) {
	tmpDir := t.TempDir()
	// seedSystemAgentEagerSouls resolves each workspace via $OMNIPUS_HOME
	// (agent.ResolveAgentHome), not via the tmpDir threaded into
	// agentWorkspacePath — pin them to the same directory.
	t.Setenv("OMNIPUS_HOME", tmpDir)
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
		},
	}
	coreagent.SeedConfig(cfg)

	soulPath := planSupervisorSoulPath(t, cfg, tmpDir)
	require.NoError(t, os.MkdirAll(filepath.Dir(soulPath), 0o755))
	require.NoError(t, os.WriteFile(soulPath, []byte{}, 0o644),
		"seed a 0-byte SOUL.md before the eager seed runs")

	seedSystemAgentEagerSouls(cfg)

	got, readErr := os.ReadFile(soulPath)
	require.NoError(t, readErr)
	assert.Equal(t, coreagent.PlanSupervisorDefaultRubric, string(got),
		"a 0-byte PlanSupervisor SOUL.md must be treated as missing and backfilled")
}

// TestAgents_PlanSupervisorUndisable is the FR-003 regression test: the
// disable guard was `foundAgent.ID == string(coreagent.IDJudge)`, so a PUT
// carrying {"enabled":false} against the PlanSupervisor sailed through with a
// 200 — silently switching off the SOLE holder of the plan-correction grant.
//
// There is no `enabled` field on AgentConfig or on the wire (a client can only
// smuggle one as an unknown field), so "still enabled afterwards" is asserted
// as the real observable outcome: the rejected PUT persisted NOTHING — the
// agent's entity record is byte-identical afterwards — and the agent is still
// served as a locked System Agent.
func TestAgents_PlanSupervisorUndisable(t *testing.T) {
	for _, body := range []string{`{"enabled":false}`, `{"disabled":true}`} {
		t.Run(body, func(t *testing.T) {
			api := newSeededJudgeAPI(t)
			entityPath := filepath.Join(api.homePath, "entities", "agents",
				string(coreagent.IDPlanSupervisor)+".json")
			before, readErr := os.ReadFile(entityPath)
			require.NoError(t, readErr, "the PlanSupervisor must be persisted before the PUT")

			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPut,
				"/api/v1/agents/"+string(coreagent.IDPlanSupervisor), strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			api.updateAgent(w, r, string(coreagent.IDPlanSupervisor))

			require.Equal(t, http.StatusBadRequest, w.Code,
				"PUT %s against the PlanSupervisor must be 400; body=%s", body, w.Body.String())
			assert.Contains(t, strings.ToLower(w.Body.String()), "cannot be disabled")

			after, readErr := os.ReadFile(entityPath)
			require.NoError(t, readErr, "the rejected PUT must not have deleted the agent")
			assert.Equal(t, string(before), string(after),
				"a rejected disable must persist nothing — the agent record must be byte-identical")

			wGet := httptest.NewRecorder()
			api.getAgent(wGet, string(coreagent.IDPlanSupervisor))
			require.Equal(t, http.StatusOK, wGet.Code,
				"the PlanSupervisor must still be served after the rejected disable; body=%s", wGet.Body.String())
			var got gen.Agent
			require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &got))
			assert.Equal(t, gen.AgentTypeSystem, got.Type)
			assert.True(t, got.Locked, "the PlanSupervisor must still be locked")
		})
	}
}

// TestAgents_SystemAgentUndisable_CoversEverySeededSystemAgent is the
// generalisation guard: the disable rule is a property of the System-Agents
// CATEGORY (coreagent.IsSystemAgentID), so adding a third System Agent must
// not require remembering to add a third id to the handler. Driving the table
// off coreagent.SystemAgents() makes a future omission fail here.
func TestAgents_SystemAgentUndisable_CoversEverySeededSystemAgent(t *testing.T) {
	for _, sa := range coreagent.SystemAgents() {
		t.Run(string(sa.ID), func(t *testing.T) {
			api := newSeededJudgeAPI(t)
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+string(sa.ID),
				strings.NewReader(`{"enabled":false}`))
			r.Header.Set("Content-Type", "application/json")
			api.updateAgent(w, r, string(sa.ID))

			require.Equal(t, http.StatusBadRequest, w.Code,
				"every seeded System Agent must reject a disable; %s returned %d, body=%s",
				sa.ID, w.Code, w.Body.String())
			assert.Contains(t, strings.ToLower(w.Body.String()), "cannot be disabled")
		})
	}
}

// TestSeedSystemAgentEagerSouls_SeedsEverySystemAgentWithADefaultSoul is the
// matching generalisation guard for the seed loop: it iterates
// coreagent.SystemAgents(), so any System Agent that declares a default soul
// via coreagent.SystemAgentDefaultSoul gets it on disk with no further edit to
// pkg/gateway. A future agent whose soul silently never lands fails here.
func TestSeedSystemAgentEagerSouls_SeedsEverySystemAgentWithADefaultSoul(t *testing.T) {
	api := newSeededJudgeAPI(t)
	cfg := api.agentLoop.GetConfig()

	for _, sa := range coreagent.SystemAgents() {
		want := coreagent.SystemAgentDefaultSoul(sa.ID)
		if strings.TrimSpace(want) == "" {
			continue // no compiled default soul — nothing to backfill
		}
		ws, wsErr := agentWorkspacePath(cfg, string(sa.ID), "", api.homePath)
		require.NoError(t, wsErr, "resolve workspace for %s", sa.ID)
		got, readErr := os.ReadFile(filepath.Join(ws, "SOUL.md"))
		require.NoErrorf(t, readErr,
			"System Agent %s declares a default soul but none reached disk at boot", sa.ID)
		assert.Equalf(t, want, string(got),
			"System Agent %s's on-disk soul must be its compiled default", sa.ID)
	}
}
