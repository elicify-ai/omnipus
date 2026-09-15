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

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
