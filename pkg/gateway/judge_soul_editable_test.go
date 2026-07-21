//go:build !cgo

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

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: tmpDir, ModelName: "test-model", MaxTokens: 4096},
		},
	}
	coreagent.SeedConfig(cfg)
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
