// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// rest_tasks_plan_membership_test.go — REST coverage for the draft-only
// plan-membership rule (operator directive: "adding a task to a running plan
// must not be possible in neither for the agent nor via the ui").
//
// The hole these tests close: the REST task surface used to reject only
// TERMINAL plans, so POST /api/v1/tasks {plan_id: P, write_set, blocked_by,
// is_join} — and equally PATCH /api/v1/tasks/{id} {plan_id: P} — attached a
// brand-new member to an `approved` or `running` plan. PlanEngine.
// promoteInboxMembers then promoted it to `next` and the engine dispatched
// it. That member reached NEITHER plan.Lint (which runs once, at approve) NOR
// plan.LintCorrection (which runs on plansupervisor corrections), so its
// write_set overlap and its join-point obligations were never checked against
// the members already in the plan. It was the third writer of plan members
// that no safety check covered.
//
// The gate now lives in exactly one place — tools.ValidateTaskPlanMembership
// (pkg/tools/plan.go) — which POST /tasks, PATCH /tasks/{id}, the create_task
// agent tool and the create_task_in_workspace System Agent tool all call.
// These tests drive the two REST verbs; pkg/tools/task_plan_membership_test.go
// and pkg/sysagent/tools/task_plan_linkage_test.go drive the two tool paths.
package gateway

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// seedPlanInState persists a plan directly in the store, in `state`, owned by
// testPlansAgentID. Writing the state straight into Create (rather than
// walking the lifecycle) is deliberate: these tests are about what the ATTACH
// path does when it meets a plan in a given state, not about how the plan got
// there.
func seedPlanInState(t *testing.T, api *restAPI, wsID string, state plan.State) *plan.Plan {
	t.Helper()
	p := &plan.Plan{
		Title:        "membership gate plan (" + string(state) + ")",
		WorkspaceID:  wsID,
		OwnerAgentID: testPlansAgentID,
		State:        state,
	}
	require.NoError(t, api.planStore.Create(p), "seed plan in state %s", state)
	return p
}

// createStandaloneTask creates a task with no plan_id and returns its wire
// representation.
func createStandaloneTask(t *testing.T, api *restAPI, wsID, title string) gen.Task {
	t.Helper()
	w := postTask(t, api, `{"workspace_id":"`+wsID+`","title":"`+title+`",`+minimalCriteriaDodJSON+`}`)
	require.Equal(t, http.StatusCreated, w.Code, "create standalone task; body=%s", w.Body.String())
	var created gen.Task
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	return created
}

// --- POST /api/v1/tasks ------------------------------------------------------

// TestTaskCreate_PlanMembership_NonDraftPlanRefused is the core case: creating
// a member on a plan that has left draft must be refused 400, with a message
// that names the plan and its state so the SPA can render something the
// operator can act on. Every non-draft state is covered, including the two
// terminal ones (which keep their own, distinct wording).
func TestTaskCreate_PlanMembership_NonDraftPlanRefused(t *testing.T) {
	cases := []struct {
		state       plan.State
		wantFragmnt string
	}{
		{plan.StateApproved, "can no longer accept new member tasks"},
		{plan.StateRunning, "can no longer accept new member tasks"},
		{plan.StateDone, "(terminal) and cannot accept new member tasks"},
		{plan.StateFailed, "(terminal) and cannot accept new member tasks"},
	}
	for _, tc := range cases {
		t.Run(string(tc.state), func(t *testing.T) {
			api := newTestRestAPIWithPlans(t)
			wsID := createTestWorkspace(t, api, "Membership WS "+string(tc.state))
			p := seedPlanInState(t, api, wsID, tc.state)

			body := `{"workspace_id":"` + wsID + `","title":"smuggled member","plan_id":"` + p.ID + `",` +
				`"write_set":["src/app.ts"],"is_join":true,` + minimalCriteriaDodJSON + `}`
			w := postTask(t, api, body)

			require.Equal(t, http.StatusBadRequest, w.Code,
				"POST /tasks with plan_id at a %s plan must be refused 400; body=%s", tc.state, w.Body.String())
			respBody := w.Body.String()
			assert.Contains(t, respBody, p.ID, "the 400 must name the plan so the SPA can render it")
			assert.Contains(t, respBody, string(tc.state), "the 400 must name the plan's state")
			assert.Contains(t, respBody, tc.wantFragmnt,
				"a locked (approved/running) plan and a dead (done/failed) plan are different operator "+
					"problems and must not collapse into one message")

			// Nothing may have been persisted: a refused attach must not leave
			// an orphan task behind.
			all, lerr := api.taskStore.List(task.Filter{WorkspaceID: wsID})
			require.NoError(t, lerr)
			assert.Empty(t, all, "a refused attach must persist no task at all")
		})
	}
}

// TestTaskCreate_PlanMembership_DraftPlanAccepted is the other half of the
// gate: authoring a member on a DRAFT plan — the only supported way to build a
// plan's member set — must still work exactly as before.
func TestTaskCreate_PlanMembership_DraftPlanAccepted(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Membership WS draft")
	p := seedPlanInState(t, api, wsID, plan.StateDraft)

	body := `{"workspace_id":"` + wsID + `","title":"legitimate member","plan_id":"` + p.ID + `",` +
		`"write_set":["src/app.ts"],` + minimalCriteriaDodJSON + `}`
	w := postTask(t, api, body)

	require.Equal(t, http.StatusCreated, w.Code,
		"POST /tasks with plan_id at a DRAFT plan must still succeed; body=%s", w.Body.String())
	var created gen.Task
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	require.NotNil(t, created.PlanId)
	assert.Equal(t, p.ID, *created.PlanId, "the member must actually be attached")

	stored, gerr := api.taskStore.Get(created.Id)
	require.NoError(t, gerr)
	assert.Equal(t, p.ID, stored.PlanID, "the linkage must survive to disk, not just to the response")
}

// TestTaskCreate_PlanMembership_CrossWorkspaceStaysDistinct proves the two
// preconditions did not collapse: a DRAFT plan in another workspace is still
// refused, and with the same-workspace wording — not the membership-frozen
// wording. They are different operator problems with different fixes.
func TestTaskCreate_PlanMembership_CrossWorkspaceStaysDistinct(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsA := createTestWorkspace(t, api, "Membership WS A")
	wsB := createTestWorkspace(t, api, "Membership WS B")
	p := seedPlanInState(t, api, wsB, plan.StateDraft)

	w := postTask(t, api,
		`{"workspace_id":"`+wsA+`","title":"cross-ws member","plan_id":"`+p.ID+`",`+minimalCriteriaDodJSON+`}`)

	require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
	body := w.Body.String()
	assert.Contains(t, body, "different workspace", "cross-workspace must keep its own diagnosis")
	assert.NotContains(t, body, "membership is frozen",
		"a draft plan in the wrong workspace is a workspace problem, not a lifecycle one")
}

// --- PATCH /api/v1/tasks/{id} ------------------------------------------------

// TestTaskPatch_PlanMembership_AttachToNonDraftPlanRefused covers the second
// REST verb. PATCH {plan_id: P} is exactly the same hole with a different verb
// — it is what the task detail panel's Plan dropdown sends — so the directive
// covers it identically.
func TestTaskPatch_PlanMembership_AttachToNonDraftPlanRefused(t *testing.T) {
	for _, state := range []plan.State{plan.StateApproved, plan.StateRunning, plan.StateDone, plan.StateFailed} {
		t.Run(string(state), func(t *testing.T) {
			api := newTestRestAPIWithPlans(t)
			wsID := createTestWorkspace(t, api, "Patch WS "+string(state))
			p := seedPlanInState(t, api, wsID, state)
			tsk := createStandaloneTask(t, api, wsID, "standalone")

			w := patchTask(t, api, tsk.Id, `{"plan_id":"`+p.ID+`"}`)

			require.Equal(t, http.StatusBadRequest, w.Code,
				"PATCH plan_id onto a %s plan must be refused 400; body=%s", state, w.Body.String())
			assert.Contains(t, w.Body.String(), string(state), "the 400 must name the plan's state")

			stored, gerr := api.taskStore.Get(tsk.Id)
			require.NoError(t, gerr)
			assert.Empty(t, stored.PlanID, "a refused PATCH must leave the task standalone")
		})
	}
}

// TestTaskPatch_PlanMembership_ReparentToRunningPlanRefused is the re-parent
// shape of the same hole: the member is already legitimately attached to a
// draft plan, and the PATCH moves it to a plan that is already running. The
// destination is what matters, not the origin.
func TestTaskPatch_PlanMembership_ReparentToRunningPlanRefused(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Reparent WS")
	draft := seedPlanInState(t, api, wsID, plan.StateDraft)
	running := seedPlanInState(t, api, wsID, plan.StateRunning)

	wCreate := postTask(t, api,
		`{"workspace_id":"`+wsID+`","title":"member of draft","plan_id":"`+draft.ID+`",`+minimalCriteriaDodJSON+`}`)
	require.Equal(t, http.StatusCreated, wCreate.Code, "body=%s", wCreate.Body.String())
	var created gen.Task
	require.NoError(t, json.Unmarshal(wCreate.Body.Bytes(), &created))

	w := patchTask(t, api, created.Id, `{"plan_id":"`+running.ID+`"}`)
	require.Equal(t, http.StatusBadRequest, w.Code,
		"re-parenting into a RUNNING plan must be refused 400; body=%s", w.Body.String())

	stored, gerr := api.taskStore.Get(created.Id)
	require.NoError(t, gerr)
	assert.Equal(t, draft.ID, stored.PlanID, "a refused re-parent must leave the original linkage intact")
}

// TestTaskPatch_PlanMembership_DetachFromRunningPlanStillAllowed proves the
// gate is not over-broad in the one direction it must never block: LEAVING a
// plan. The guard is on the destination (a non-empty plan_id), so
// {"plan_id":""} — the detach the Plan dropdown's "No plan" option sends — is
// untouched, running plan or not.
func TestTaskPatch_PlanMembership_DetachFromRunningPlanStillAllowed(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Detach WS")
	p := seedPlanInState(t, api, wsID, plan.StateDraft)

	wCreate := postTask(t, api,
		`{"workspace_id":"`+wsID+`","title":"leaving member","plan_id":"`+p.ID+`",`+minimalCriteriaDodJSON+`}`)
	require.Equal(t, http.StatusCreated, wCreate.Code, "body=%s", wCreate.Body.String())
	var created gen.Task
	require.NoError(t, json.Unmarshal(wCreate.Body.Bytes(), &created))

	// Drive the plan to running AFTER the member is legitimately attached.
	drivePlanToRunning(t, api, p.ID)

	w := patchTask(t, api, created.Id, `{"plan_id":""}`)
	require.Equal(t, http.StatusOK, w.Code,
		"detaching from a running plan must still be allowed; body=%s", w.Body.String())

	stored, gerr := api.taskStore.Get(created.Id)
	require.NoError(t, gerr)
	assert.Empty(t, stored.PlanID, "the detach must have landed")
}

// TestTaskPatch_PlanMembership_UnrelatedPatchOnRunningMemberUnaffected proves
// the gate binds to the plan_id FIELD, not to plan membership in general: a
// PATCH that does not mention plan_id must not be refused just because the
// task happens to belong to a running plan. Editing a running member's title
// is a normal, legitimate operation.
func TestTaskPatch_PlanMembership_UnrelatedPatchOnRunningMemberUnaffected(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Unrelated WS")
	p := seedPlanInState(t, api, wsID, plan.StateDraft)

	wCreate := postTask(t, api,
		`{"workspace_id":"`+wsID+`","title":"running member","plan_id":"`+p.ID+`",`+minimalCriteriaDodJSON+`}`)
	require.Equal(t, http.StatusCreated, wCreate.Code, "body=%s", wCreate.Body.String())
	var created gen.Task
	require.NoError(t, json.Unmarshal(wCreate.Body.Bytes(), &created))
	drivePlanToRunning(t, api, p.ID)

	w := patchTask(t, api, created.Id, `{"title":"renamed mid-flight"}`)
	require.Equal(t, http.StatusOK, w.Code,
		"a PATCH that never mentions plan_id must be unaffected; body=%s", w.Body.String())

	stored, gerr := api.taskStore.Get(created.Id)
	require.NoError(t, gerr)
	assert.Equal(t, "renamed mid-flight", stored.Title)
	assert.Equal(t, p.ID, stored.PlanID, "the member must still belong to its plan")
}

// TestTaskCreate_PlanMembership_PathTraversalPlanIDRefused proves the guard
// did not lose the path-traversal check when the REST-local wrapper (which
// called validateEntityID) was deleted: plan.Store.Get runs pkg/plan's own
// validateID on every lookup, with the identical rejection set, and it still
// surfaces as a 400.
func TestTaskCreate_PlanMembership_PathTraversalPlanIDRefused(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Traversal WS")

	for _, bad := range []string{"../../etc/passwd", "sub/dir", `back\slash`} {
		w := postTask(t, api,
			`{"workspace_id":"`+wsID+`","title":"traversal","plan_id":`+
				mustJSONString(t, bad)+`,`+minimalCriteriaDodJSON+`}`)
		assert.Equal(t, http.StatusBadRequest, w.Code,
			"plan_id %q must be refused 400, never used as a path; body=%s", bad, w.Body.String())
		assert.True(t, strings.Contains(w.Body.String(), "invalid id"),
			"plan_id %q must be rejected as an invalid id; body=%s", bad, w.Body.String())
	}
}
