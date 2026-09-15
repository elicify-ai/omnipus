// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// rest_plans_empty_approve_test.go — HTTP-layer regression coverage for UAT
// defect A: POST /api/v1/plans/{id}/approve returned 200 and moved a plan with
// ZERO member tasks to `approved`, from which the engine promoted it to
// `running`.
//
// The engine-side consequence is what made this severe rather than cosmetic: a
// PlanSupervisor then populated the empty plan by correction, and corrections
// carried no lint of their own, so the write-set-overlap and join-point checks
// never saw those members. Approving empty was a complete, two-step bypass of
// plan-lint.
//
// Note the asymmetry this closes: the AGENT approve path (execute_plan,
// pkg/tools/plan.go) already refused a memberless plan explicitly ("plan %q has
// zero member tasks; nothing to execute"). Only the REST/UI path — the one the
// UAT used — lacked the precondition.

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// TestPlanApprove_EmptyPlanRejected is the defect-A regression test at the
// HTTP boundary. Against the pre-fix handler the approve call returns 200 and
// the plan reads back `approved`.
func TestPlanApprove_EmptyPlanRejected(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Empty Plan WS")

	wCreate := postPlan(t, api, wsID,
		`{"workspace_id":"`+wsID+`","title":"Empty plan","owner_agent_id":"`+testPlansAgentID+`"}`)
	require.Equal(t, http.StatusCreated, wCreate.Code, "body=%s", wCreate.Body.String())
	var p gen.Plan
	require.NoError(t, json.Unmarshal(wCreate.Body.Bytes(), &p))

	// No member tasks are created at all — this is the whole point.
	wApprove := postPlanAction(t, api, p.Id, "approve")
	require.Equal(t, http.StatusBadRequest, wApprove.Code,
		"a plan with zero member tasks must not approve; body=%s", wApprove.Body.String())

	var approveErr gen.PlanApproveError
	require.NoError(t, json.Unmarshal(wApprove.Body.Bytes(), &approveErr))
	require.NotNil(t, approveErr.Error, "expected an error message, got body=%s", wApprove.Body.String())
	assert.Contains(t, *approveErr.Error, "at least one member task",
		"the rejection must say what is required, not merely refuse")

	// Approval must never partially apply: the plan stays draft, so the
	// engine can never pick it up and promote it to running.
	wGet := getPlan(t, api, p.Id)
	var reloaded gen.Plan
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &reloaded))
	assert.Equal(t, gen.PlanStateDraft, reloaded.State,
		"an empty plan that failed approval must remain draft")
}

// TestPlanApprove_OneMemberIsEnough is the positive control. The new gate is an
// ARITY precondition — exactly one valid member must be sufficient — so this
// pins that the fix did not accidentally become a stricter rule (e.g. "needs a
// DoD too", or "needs more than one member") and that ordinary approval still
// works.
func TestPlanApprove_OneMemberIsEnough(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "One Member WS")

	wCreate := postPlan(t, api, wsID,
		`{"workspace_id":"`+wsID+`","title":"One member plan","owner_agent_id":"`+testPlansAgentID+`"}`)
	require.Equal(t, http.StatusCreated, wCreate.Code, "body=%s", wCreate.Body.String())
	var p gen.Plan
	require.NoError(t, json.Unmarshal(wCreate.Body.Bytes(), &p))

	mustCreateTask(t, api, wsID, "the only member", p.Id)

	wApprove := postPlanAction(t, api, p.Id, "approve")
	require.Equal(t, http.StatusOK, wApprove.Code, "body=%s", wApprove.Body.String())
	var approved gen.Plan
	require.NoError(t, json.Unmarshal(wApprove.Body.Bytes(), &approved))
	assert.Equal(t, gen.PlanStateApproved, approved.State)
}
