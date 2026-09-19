// Omnipus - Plans REST authorship-origin regression tests (PR732)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// The SD-A7 tiered-DoD gate reads the plan's explicit creation-time
// authorship stamp (Plan.CreatedByKind, via plan.AuthoredByAgent) instead of
// re-deriving origin from the agent registry at approve time. The registry
// probe misrouted every plan created by a HUMAN whose username collides with
// a registered agent's ID — "admin" (the human administrator) vs "admin"
// (the ADR-090 Admin agent), "mia", ... — into the strict
// agent-authored tier: approve 400'd on an empty DoD a human never needed
// (found in PR732 CI, e2e exec shard; evidence: release-uat/ci-e2e.md
// observation 1).

package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/require"
)

// originTestOwnerAgentID is the plan OWNER (a real, addressable agent,
// required by validatePlanOwnerAgent) — deliberately distinct from the
// colliding author identities below.
const originTestOwnerAgentID = "01JXORIGINOWNER0000001"

// newOriginTestRestAPI mirrors newTestRestAPIWithPlans (rest_plans_test.go)
// but registers an EXPLICIT agent list so the collision cases can register
// agents whose IDs collide with human usernames.
func newOriginTestRestAPI(t *testing.T, agentIDs ...string) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	agents := make([]config.AgentConfig, 0, len(agentIDs))
	for _, id := range agentIDs {
		home := filepath.Join(tmpDir, "agents", id)
		require.NoError(t, os.MkdirAll(home, 0o700))
		agents = append(agents, config.AgentConfig{
			ID:      id,
			Name:    "Agent " + id,
			Type:    config.AgentTypeCustom,
			Home:    home,
			Default: id == agentIDs[0],
		})
	}
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			List: agents,
		},
	}
	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config.json"), minimalCfg, 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		homePath:      tmpDir,
		taskStore:     task.New(tmpDir + "/tasks"),
		taskLock:      task.TaskFileLock,
		planStore:     plan.New(tmpDir + "/plans"),
	}
	pe := agent.NewPlanEngine(al, api.planStore, api.taskStore, api.taskExecutor)
	api.agentLoop.SetPlanEngine(pe)
	t.Cleanup(func() {
		pe.Stop()
		api.agentLoop.WaitForActiveRequests()
	})
	return api
}

// postPlanAs issues POST /api/v1/workspaces/{wsID}/plans AS AN AUTHENTICATED
// HUMAN USER (callerIdentity reads UserContextKey), mirroring postPlan but
// injecting the caller identity the real auth middleware would.
func postPlanAs(t *testing.T, api *restAPI, wsID, username, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/"+wsID+"/plans", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/workspaces/" + wsID + "/plans"
	r = r.WithContext(context.WithValue(r.Context(), UserContextKey{}, &config.UserConfig{Username: username}))
	api.HandleWorkspaces(w, r)
	return w
}

// createPlanAs creates a plan as a human user and returns its wire form.
func createPlanAs(t *testing.T, api *restAPI, wsID, username, title string) gen.Plan {
	t.Helper()
	body := `{"workspace_id":"` + wsID + `","title":"` + title + `","owner_agent_id":"` + originTestOwnerAgentID + `"}`
	w := postPlanAs(t, api, wsID, username, body)
	require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())
	var p gen.Plan
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &p))
	require.Empty(t, p.Dod, "test precondition: the created plan carries no DoD")
	return p
}

// seedPlanWithKind seeds a plan directly on the store with an explicit
// CreatedByKind (empty = legacy kindless), plus one criteria-complete member
// task so the DoD gate is the only gate that can reject an approve.
func seedPlanWithKind(t *testing.T, api *restAPI, wsID, createdBy, kind string) *plan.Plan {
	t.Helper()
	p := &plan.Plan{
		Title:         "Seeded " + createdBy,
		WorkspaceID:   wsID,
		OwnerAgentID:  originTestOwnerAgentID,
		CreatedBy:     createdBy,
		CreatedByKind: kind,
	}
	require.NoError(t, api.planStore.Create(p))
	member := &task.Task{
		Title: "member", Prompt: "do it", Action: task.ActionLLM,
		AgentID: originTestOwnerAgentID, WorkspaceID: wsID, Status: task.StatusNext,
		PlanID: p.ID,
		Criteria: []task.AcceptanceCriterion{{
			Kind: task.KindProse, Text: "done",
			Author: task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: originTestOwnerAgentID},
		}},
	}
	require.NoError(t, api.taskStore.Create(member))
	return p
}

// approvePlan issues POST /plans/{id}/approve and unmarshals a 200 body.
func approvePlan(t *testing.T, api *restAPI, id string) (int, *gen.PlanApproveError) {
	t.Helper()
	w := postPlanAction(t, api, id, "approve")
	if w.Code == http.StatusOK {
		return w.Code, nil
	}
	var approveErr gen.PlanApproveError
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &approveErr))
	return w.Code, &approveErr
}

// TestPlanApprove_HumanAdmin_CollidesWithAgentAdmin_SoftTier is THE
// regression: the human administrator (username "admin") creates a plan
// through REST while the ADR-090 Admin agent (ID "admin") is registered.
// The plan carries no DoD — a human's plan never needs one — and approval
// must succeed. Before the CreatedByKind stamp, isAgentID("admin") resolved
// to the agent and approve 400'd with "plan requires a Definition of Done
// before approval (agent-authored plan)".
func TestPlanApprove_HumanAdmin_CollidesWithAgentAdmin_SoftTier(t *testing.T) {
	api := newOriginTestRestAPI(t, originTestOwnerAgentID, "admin")
	wsID := createTestWorkspace(t, api, "Origin WS")

	p := createPlanAs(t, api, wsID, "admin", "Human admin plan")
	// One criteria-complete member: plan-lint arity precondition.
	mustCreateTask(t, api, wsID, "member", p.Id)

	code, approveErr := approvePlan(t, api, p.Id)
	require.Equal(t, http.StatusOK, code, "human-admin plan must approve on the soft tier, body=%+v", approveErr)
}

// TestPlanApprove_AgentAdminAuthored_StrictTierPreserved proves the repair
// did NOT weaken the agent-authored gate: an explicitly agent-stamped plan
// (the create_plan path) with no DoD is still rejected — even though its
// CreatedBy ("admin") is ALSO a valid human username.
func TestPlanApprove_AgentAdminAuthored_StrictTierPreserved(t *testing.T) {
	api := newOriginTestRestAPI(t, originTestOwnerAgentID, "admin")
	wsID := createTestWorkspace(t, api, "Origin WS")

	p := seedPlanWithKind(t, api, wsID, "admin", plan.CreatedByKindAgent)
	code, approveErr := approvePlan(t, api, p.ID)
	require.Equal(t, http.StatusBadRequest, code, "agent-stamped plan without DoD must stay on the strict tier")
	require.NotNil(t, approveErr.Error)
	require.Contains(t, *approveErr.Error, "agent-authored plan")
}

// TestPlanApprove_HumanMia_CollidesWithAgentMia_SoftTier proves the repair
// generalizes beyond "admin": ANY human username colliding with ANY
// registered agent ID takes the soft tier.
func TestPlanApprove_HumanMia_CollidesWithAgentMia_SoftTier(t *testing.T) {
	api := newOriginTestRestAPI(t, originTestOwnerAgentID, "mia")
	wsID := createTestWorkspace(t, api, "Origin WS")

	p := createPlanAs(t, api, wsID, "mia", "Human mia plan")
	mustCreateTask(t, api, wsID, "member", p.Id)

	code, approveErr := approvePlan(t, api, p.Id)
	require.Equal(t, http.StatusOK, code, "body=%+v", approveErr)
}

// TestPlanApprove_LegacyKindlessPlan_FallsBackToRegistryHeuristic pins the
// explicit missing-origin derivation: a plan persisted BEFORE the stamp
// (kindless) falls back to the old registry probe — preserving the
// pre-fix semantics, INCLUDING its documented limit (a kindless human plan
// whose username collides stays misrouted; the stored bytes cannot
// distinguish the two and no migration backfills the stamp).
func TestPlanApprove_LegacyKindlessPlan_FallsBackToRegistryHeuristic(t *testing.T) {
	api := newOriginTestRestAPI(t, originTestOwnerAgentID, "admin")
	wsID := createTestWorkspace(t, api, "Origin WS")

	// Kindless + CreatedBy resolves to the registered "admin" agent ->
	// legacy heuristic says agent-authored -> strict tier (pre-fix
	// behavior preserved for legacy data).
	pLegacyAgent := seedPlanWithKind(t, api, wsID, "admin", "")
	code, approveErr := approvePlan(t, api, pLegacyAgent.ID)
	require.Equal(t, http.StatusBadRequest, code, "legacy kindless plan resolving to an agent must keep the pre-fix strict tier")
	require.NotNil(t, approveErr.Error)
	require.Contains(t, *approveErr.Error, "agent-authored plan")

	// Kindless + CreatedBy resolves to NO agent -> soft tier.
	pLegacyHuman := seedPlanWithKind(t, api, wsID, "daniel", "")
	code, approveErr = approvePlan(t, api, pLegacyHuman.ID)
	require.Equal(t, http.StatusOK, code, "legacy kindless plan with a non-agent author must approve, body=%+v", approveErr)
}

// TestPlanWire_DoesNotLeakCreatedByKind guards the internal-only scope of
// the stamp: the wire Plan schema was NOT regenerated (no contract change),
// so the persisted created_by_kind must never appear in a REST response.
func TestPlanWire_DoesNotLeakCreatedByKind(t *testing.T) {
	api := newOriginTestRestAPI(t, originTestOwnerAgentID, "admin")
	wsID := createTestWorkspace(t, api, "Origin WS")

	p := createPlanAs(t, api, wsID, "admin", "No leak")
	// Plan-lint arity: a plan needs >=1 member task to be approvable at all;
	// this test is about wire shape, so the member is scaffolding.
	mustCreateTask(t, api, wsID, "member", p.Id)
	w := postPlanAction(t, api, p.Id, "approve")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	require.NotContains(t, w.Body.String(), "created_by_kind",
		"CreatedByKind is internal-only; a wire appearance means the contract was changed without regeneration")
}
