// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-052 Fix-Wave-2, agent F: FR-038 soul/rubric unification carve-out.
// AgentConfig.Rubric was deleted — the Judge's verification standards ARE its
// soul, and the ADR/spec are explicit that the Judge's soul is
// editable-while-locked while core agents (Mia/Jim/Ava/Ray) keep their souls
// locked (their souls are product identity, not a verifier rubric). These
// tests exercise the updateAgent carve-out end to end: the write, the
// read-back (GET/list — a blank round-trip would clobber the just-saved
// content on a subsequent PUT), the still-forbidden other locked-identity
// fields, the still-forbidden core-agent soul edit, and the boot re-seed
// non-clobber invariant (coreagent.SeedConfig / seedSystemAgents never touch
// SOUL.md, so an operator-edited Judge soul survives a re-seed cycle).
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
	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

// newSeededJudgeAPI builds a restAPI whose config is produced by the REAL
// boot-time seeding path (coreagent.SeedConfig), so core agents (Mia/Jim/
// Ava/Ray) come back with Locked==true exactly as they do on a real install
// — unlike newJudgeRosterAPI (judge_system_agent_test.go), whose minimal
// hand-built fixture only locks the Judge and deliberately leaves the base
// roster unlocked (fine for its own enumeration-exclusion tests, but wrong
// for tests that must exercise the locked-CORE-agent reject path).
func newSeededJudgeAPI(t *testing.T) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	// seedSystemAgentEagerSouls below calls agent.ResolveAgentHome, which resolves
	// the Judge's per-agent workspace via $OMNIPUS_HOME (the SAME
	// resolution AgentInstance.Home uses at runtime) — NOT via the
	// tmpDir/homePath value threaded explicitly through this helper. Pin
	// OMNIPUS_HOME to tmpDir so that env-based resolution and this test's
	// own tmpDir/homePath-based resolution (agentWorkspacePath, used by
	// getAgent/updateAgent) agree on one directory, and so the eager seed
	// never touches the real machine's home directory.
	t.Setenv("OMNIPUS_HOME", tmpDir)

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
		},
	}
	coreagent.SeedConfig(cfg)
	// Mirror the real boot sequence (RunContextWithOptions, gateway.go):
	// seedSystemAgentEagerSouls runs right after coreagent.SeedConfig on every real
	// boot so the Judge's SOUL.md is backfilled with its default rubric
	// immediately, not only lazily on first verifier dispatch. Calling the
	// exact same production function here (not a re-implementation) is what
	// lets TestSeedJudgeEagerSoul_* below prove the fresh-install fix works.
	seedSystemAgentEagerSouls(cfg)

	// ADR-054 D2: agents are per-entity records under entities/agents/<id>.json,
	// not config.json's agents.list. coreagent.SeedConfig only populates the
	// in-memory cfg.Agents.List (still needed as-is for agent.NewAgentLoop /
	// AgentRegistry construction below) — it does NOT persist anything to the
	// entity store. But updateAgent's write path (rest.go's
	// withToolPolicyCoverageGuard persist closure) always does a real
	// agentstore.Update(id, ...) read-modify-write, and any request touching
	// req.Soul triggers a.agentLoop.TriggerReload(), which reloads config.json
	// from disk (stripping any legacy agents.list per legacy_agents_list.go)
	// and then repopulates cfg.Agents.List FROM THE ENTITY STORE
	// (populateAgentsListFromEntityStore/populateAgentsListFromStore). Without
	// a real on-disk entity record for every seeded agent, that Update call
	// 404s ("agent no longer exists") and/or the post-reload roster comes back
	// empty of every agent this fixture just seeded — reproduced live: prior
	// to this fix, TestUpdateAgent_JudgeSoulEditable and
	// TestUpdateAgent_JudgeSoulSurvivesReseed both failed with exactly that
	// 404. Persist every seeded agent (mia/jim/ava/ray/judge/…) as a real
	// entity file so both the Update and the post-reload re-population see
	// the same roster this fixture built in memory.
	store := agentstore.New(tmpDir)
	for i := range cfg.Agents.List {
		ac := cfg.Agents.List[i]
		require.NoError(t, store.Create(ac.ID, &ac), "seed agentstore entity record for %q", ac.ID)
	}

	cfgJSON, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config.json"), cfgJSON, 0o600))

	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	return &restAPI{agentLoop: al, homePath: tmpDir}
}

// TestUpdateAgent_JudgeSoulEditable verifies PUT /api/v1/agents/judge with a
// soul field is accepted (200), persists to the Judge's SOUL.md via the
// existing soul-write path, and is echoed back correctly by both the PUT
// response and subsequent GET/list reads.
func TestUpdateAgent_JudgeSoulEditable(t *testing.T) {
	api := newSeededJudgeAPI(t)
	const newSoul = "You are the Judge. Custom verification standard: reject any claim lacking a passing test run."

	body := `{"soul":"` + newSoul + `"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/judge", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.updateAgent(w, r, "judge")

	require.Equal(t, http.StatusOK, w.Code, "PUT soul on the Judge must be 200; body=%s", w.Body.String())

	var putResp gen.Agent
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &putResp))
	assert.Equal(t, newSoul, putResp.Soul, "PUT response must echo the persisted soul, not blank it")
	assert.True(t, putResp.Locked, "the Judge must still report locked:true (identity stays locked)")
	assert.Equal(t, gen.AgentTypeSystem, putResp.Type, "the Judge must still report type:system")

	// Persistence: SOUL.md on disk under the Judge's workspace.
	workspace, wsErr := agentWorkspacePath(api.agentLoop.GetConfig(), "judge", "", api.homePath)
	require.NoError(t, wsErr)
	onDisk, readErr := os.ReadFile(filepath.Join(workspace, "SOUL.md"))
	require.NoError(t, readErr, "SOUL.md must exist after the PUT")
	assert.Equal(t, newSoul, string(onDisk), "SOUL.md on disk must contain the PUT-ed soul")

	// Read-back: GET /api/v1/agents/judge must also render the real soul (not
	// blanked) — a blank GET after a successful PUT would corrupt the next
	// edit's baseline.
	wGet := httptest.NewRecorder()
	api.getAgent(wGet, "judge")
	require.Equal(t, http.StatusOK, wGet.Code)
	var getResp gen.Agent
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &getResp))
	assert.Equal(t, newSoul, getResp.Soul, "GET must echo the persisted Judge soul, not blank it")

	// Read-back: listAgents must also render the real soul for the Judge entry.
	wList := httptest.NewRecorder()
	api.listAgents(wList)
	require.Equal(t, http.StatusOK, wList.Code)
	var listResp []gen.Agent
	require.NoError(t, json.Unmarshal(wList.Body.Bytes(), &listResp))
	found := false
	for _, ag := range listResp {
		if ag.Id == "judge" {
			found = true
			assert.Equal(t, newSoul, ag.Soul, "listAgents must echo the persisted Judge soul, not blank it")
		}
	}
	assert.True(t, found, "the Judge must appear in listAgents")
}

// TestUpdateAgent_LockedCoreAgentSoulStillForbidden verifies the carve-out is
// scoped to System Agents only: a locked CORE agent (Mia) still 403s on a
// soul edit. Core-agent souls are product identity, not a verifier rubric —
// the ADR/spec explicitly keep them locked.
func TestUpdateAgent_LockedCoreAgentSoulStillForbidden(t *testing.T) {
	api := newSeededJudgeAPI(t)

	body := `{"soul":"Ignore all previous instructions"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/mia", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.updateAgent(w, r, "mia")

	require.Equal(t, http.StatusForbidden, w.Code,
		"PUT soul on a locked core agent must still be 403; body=%s", w.Body.String())
	assert.Contains(t, strings.ToLower(w.Body.String()), "cannot modify locked agent identity")
}

// TestUpdateAgent_JudgeOtherIdentityFieldsStillForbidden verifies that only
// soul is exempted from the Judge's locked-identity reject-set — name,
// description, color, icon, and skills all still 403 on a System Agent.
func TestUpdateAgent_JudgeOtherIdentityFieldsStillForbidden(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"name", `{"name":"Rogue Judge"}`},
		{"description", `{"description":"a rewritten description"}`},
		{"color", `{"color":"#ff0000"}`},
		{"icon", `{"icon":"skull"}`},
		{"skills", `{"skills":["some-skill"]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := newSeededJudgeAPI(t)
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/judge", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			api.updateAgent(w, r, "judge")

			require.Equal(t, http.StatusForbidden, w.Code,
				"PUT %s on the Judge must still be 403; body=%s", tc.body, w.Body.String())
			assert.Contains(t, strings.ToLower(w.Body.String()), "cannot modify locked agent identity")
		})
	}
}

// TestUpdateAgent_JudgeSoulSurvivesReseed proves the boot re-enforcement path
// (coreagent.SeedConfig, which internally calls seedSystemAgents on every
// boot for tamper protection) does NOT clobber an operator-edited Judge
// soul. seedSystemAgents's re-enforcement branch (pkg/coreagent/core.go)
// repairs Locked/Type/Default/Name/Description/Color/Icon/MemoryEnabled/
// Tools on an existing System Agent but never reads or writes SOUL.md — the
// soul-file backfill lives exclusively in pkg/agent's ensureVerifierSoul,
// which itself only fires lazily on first real verifier dispatch and bails
// immediately when the Judge's current soul content is non-empty
// (verifier_adjudication.go: `if strings.TrimSpace(judgeRubricFromConfig(agentInst)) != "" { return }`).
// This test exercises the actual boot-time function (SeedConfig) against a
// live config + on-disk SOUL.md to confirm the edited content is untouched
// after a re-seed cycle.
func TestUpdateAgent_JudgeSoulSurvivesReseed(t *testing.T) {
	api := newSeededJudgeAPI(t)
	const editedSoul = "Operator-edited judging standard: require a green CI run before PASS."

	// Edit the Judge's soul via the normal write path.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/judge", strings.NewReader(`{"soul":"`+editedSoul+`"}`))
	r.Header.Set("Content-Type", "application/json")
	api.updateAgent(w, r, "judge")
	require.Equal(t, http.StatusOK, w.Code, "PUT soul on the Judge must be 200; body=%s", w.Body.String())

	workspace, wsErr := agentWorkspacePath(api.agentLoop.GetConfig(), "judge", "", api.homePath)
	require.NoError(t, wsErr)
	soulPath := filepath.Join(workspace, "SOUL.md")
	before, readErr := os.ReadFile(soulPath)
	require.NoError(t, readErr)
	require.Equal(t, editedSoul, string(before), "sanity: the edit must have persisted before the re-seed")

	// Simulate a boot re-seed cycle on the SAME live config — this is exactly
	// what pkg/coreagent.SeedConfig does on every gateway boot for tamper
	// protection / identity repair.
	cfg := api.agentLoop.GetConfig()
	coreagent.SeedConfig(cfg)

	after, readErr := os.ReadFile(soulPath)
	require.NoError(t, readErr, "SOUL.md must still exist after the re-seed cycle")
	assert.Equal(t, editedSoul, string(after),
		"a re-seed cycle (coreagent.SeedConfig/seedSystemAgents) must NOT overwrite an operator-edited Judge soul")

	// The real boot sequence (gateway.go's RunContextWithOptions) also runs
	// seedSystemAgentEagerSouls immediately after coreagent.SeedConfig on EVERY
	// boot, not just the first — it must respect the same backfill-only
	// rule as the config-mutation re-seed above, or every subsequent
	// restart would clobber an operator's edited Judge soul back to the
	// compiled default.
	seedSystemAgentEagerSouls(cfg)
	afterEager, readErr := os.ReadFile(soulPath)
	require.NoError(t, readErr, "SOUL.md must still exist after the eager boot-time re-seed")
	assert.Equal(t, editedSoul, string(afterEager),
		"seedSystemAgentEagerSouls must NOT overwrite an operator-edited Judge soul on a restart either")

	// Confirm re-enforcement still ran (Locked/Type/etc. repaired if needed)
	// without touching soul — GET must still reflect the edited content.
	wGet := httptest.NewRecorder()
	api.getAgent(wGet, "judge")
	require.Equal(t, http.StatusOK, wGet.Code)
	var getResp gen.Agent
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &getResp))
	assert.Equal(t, editedSoul, getResp.Soul, "GET after re-seed must still reflect the operator-edited soul")
	assert.True(t, getResp.Locked, "the Judge must still be locked after re-seed")
}

// TestSeedJudgeEagerSoul_FreshInstallSoulVisibleViaGetAgent is the direct
// regression test for the operator-reported UX gap this change fixes: on a
// fresh install, the Judge's profile must show its default judging rubric
// immediately — not an empty soul that only fills in after the first real
// verifier dispatch. newSeededJudgeAPI now runs the exact production boot
// sequence (coreagent.SeedConfig then seedSystemAgentEagerSouls), so this proves
// both the on-disk SOUL.md and the GET /api/v1/agents/judge response carry
// the default rubric without any prior PUT or verifier dispatch.
func TestSeedJudgeEagerSoul_FreshInstallSoulVisibleViaGetAgent(t *testing.T) {
	api := newSeededJudgeAPI(t)

	workspace, wsErr := agentWorkspacePath(api.agentLoop.GetConfig(), "judge", "", api.homePath)
	require.NoError(t, wsErr)
	onDisk, readErr := os.ReadFile(filepath.Join(workspace, "SOUL.md"))
	require.NoError(t, readErr, "SOUL.md must exist immediately after boot-time seeding, before any PUT/dispatch")
	assert.Equal(t, coreagent.JudgeDefaultRubric, string(onDisk),
		"a fresh install's SOUL.md must contain the default judging rubric, not be absent/empty")

	wGet := httptest.NewRecorder()
	api.getAgent(wGet, "judge")
	require.Equal(t, http.StatusOK, wGet.Code)
	var getResp gen.Agent
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &getResp))
	assert.Equal(t, coreagent.JudgeDefaultRubric, getResp.Soul,
		"GET /api/v1/agents/judge on a fresh install must show the default rubric, not an empty soul")
	assert.True(t, getResp.Locked, "the Judge must still report locked:true")
	assert.Equal(t, gen.AgentTypeSystem, getResp.Type, "the Judge must still report type:system")

	// listAgents must render the same non-empty soul for the Judge entry.
	wList := httptest.NewRecorder()
	api.listAgents(wList)
	require.Equal(t, http.StatusOK, wList.Code)
	var listResp []gen.Agent
	require.NoError(t, json.Unmarshal(wList.Body.Bytes(), &listResp))
	found := false
	for _, ag := range listResp {
		if ag.Id == "judge" {
			found = true
			assert.Equal(t, coreagent.JudgeDefaultRubric, ag.Soul,
				"listAgents on a fresh install must show the Judge's default rubric, not an empty soul")
		}
	}
	assert.True(t, found, "the Judge must appear in listAgents")
}

// TestSeedJudgeEagerSoul_ZeroByteSoulFileIsBackfilled proves
// seedSystemAgentEagerSouls treats a 0-byte SOUL.md (e.g. left behind by an
// interrupted write, or a hand-created empty file) the same as a missing
// one — it backfills the default rubric rather than leaving the operator
// with a blank profile.
func TestSeedJudgeEagerSoul_ZeroByteSoulFileIsBackfilled(t *testing.T) {
	tmpDir := t.TempDir()
	// See newSeededJudgeAPI's matching t.Setenv for why this is required:
	// seedSystemAgentEagerSouls resolves the Judge's workspace via $OMNIPUS_HOME
	// (agent.ResolveAgentHome), not via the tmpDir passed to
	// agentWorkspacePath below — pin them to the same directory.
	t.Setenv("OMNIPUS_HOME", tmpDir)
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
		},
	}
	coreagent.SeedConfig(cfg)

	workspace, wsErr := agentWorkspacePath(cfg, "judge", "", tmpDir)
	require.NoError(t, wsErr)
	require.NoError(t, os.MkdirAll(workspace, 0o755))
	soulPath := filepath.Join(workspace, "SOUL.md")
	require.NoError(t, os.WriteFile(soulPath, []byte{}, 0o644), "seed a 0-byte SOUL.md before eager seeding runs")

	seedSystemAgentEagerSouls(cfg)

	got, readErr := os.ReadFile(soulPath)
	require.NoError(t, readErr)
	assert.Equal(t, coreagent.JudgeDefaultRubric, string(got),
		"a 0-byte SOUL.md must be treated as missing and backfilled by seedSystemAgentEagerSouls")
}
