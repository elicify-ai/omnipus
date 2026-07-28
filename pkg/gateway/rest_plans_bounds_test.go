// rest_plans_bounds_test.go — the per-plan PlanBounds wire surface.
//
// Two defects are pinned here, both of which lost operator data silently:
//
//  1. plan.PlanBounds.SupervisionTurnTimeoutSeconds / .SupervisionMaxAttempts
//     persisted and validated but could not cross the wire at all —
//     `bounds` carries `additionalProperties: false` in Plan.yaml /
//     PlanCreateRequest.yaml / PlanUpdateRequest.yaml, and rest_plans.go maps
//     the bounds fields one by one. A value set through any non-REST path was
//     invisible to every API client.
//
//  2. handlePlanPut rebuilt plan.Bounds WHOLESALE from the request, and the
//     store applies patch.Bounds wholesale too (updateLocked's
//     `p.Bounds = newBounds`). So any PUT carrying a partial `bounds` object
//     zeroed every field it did not mention. That is not hypothetical: the
//     shipped SPA plan-edit form (CreatePlanSlideOver.tsx, buildBounds) sends
//     `{plan_judge_max_rounds, idle_expiry_days}` on EVERY save, so editing a
//     plan's title destroyed its supervision overrides.
//
// These assert the operator-visible outcome — what survives a round trip —
// not which mapper ran.

package gateway

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// createPlanWithAllBounds creates a draft plan carrying an override in ALL
// FOUR bounds fields and returns its id. Every field is deliberately distinct
// from its global default so a value read back later cannot be a default
// masquerading as a survivor.
func createPlanWithAllBounds(t *testing.T, api *restAPI, wsID string) string {
	t.Helper()
	body := `{"workspace_id":"` + wsID + `","title":"Bounded plan","owner_agent_id":"` + testPlansAgentID + `",` +
		`"bounds":{"plan_judge_max_rounds":11,"idle_expiry_days":22,` +
		`"supervision_turn_timeout_seconds":333,"supervision_max_attempts":4}}`
	w := postPlan(t, api, wsID, body)
	require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())

	var created gen.Plan
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))

	// The create response must already echo all four back — that alone proves
	// both the PlanCreateRequest mapper and the toWirePlan mapper carry them.
	require.NotNil(t, created.Bounds, "create response dropped bounds entirely")
	requireBounds(t, created.Bounds, 11, 22, 333, 4)
	return created.Id
}

// requireBounds asserts all four bounds fields are present with the expected
// values. A nil pointer is a failure, never a skipped assertion — "the field
// vanished" is precisely the bug under test.
func requireBounds(t *testing.T, b *struct {
	IdleExpiryDays                *int `json:"idle_expiry_days,omitempty"`
	PlanJudgeMaxRounds            *int `json:"plan_judge_max_rounds,omitempty"`
	SupervisionMaxAttempts        *int `json:"supervision_max_attempts,omitempty"`
	SupervisionTurnTimeoutSeconds *int `json:"supervision_turn_timeout_seconds,omitempty"`
}, rounds, days, timeout, attempts int,
) {
	t.Helper()
	require.NotNil(t, b, "bounds missing from the wire payload")
	require.NotNil(t, b.PlanJudgeMaxRounds, "plan_judge_max_rounds was destroyed")
	require.NotNil(t, b.IdleExpiryDays, "idle_expiry_days was destroyed")
	require.NotNil(t, b.SupervisionTurnTimeoutSeconds, "supervision_turn_timeout_seconds was destroyed")
	require.NotNil(t, b.SupervisionMaxAttempts, "supervision_max_attempts was destroyed")
	assert.Equal(t, rounds, *b.PlanJudgeMaxRounds, "plan_judge_max_rounds")
	assert.Equal(t, days, *b.IdleExpiryDays, "idle_expiry_days")
	assert.Equal(t, timeout, *b.SupervisionTurnTimeoutSeconds, "supervision_turn_timeout_seconds")
	assert.Equal(t, attempts, *b.SupervisionMaxAttempts, "supervision_max_attempts")
}

// getPlanBounds re-reads the plan through the real GET handler and returns its
// wire bounds. Going back through GET (rather than the store) is deliberate:
// it proves the value both PERSISTED and is VISIBLE to an API client, which
// are two separate failures this file cares about.
func getPlanBounds(t *testing.T, api *restAPI, id string) *struct {
	IdleExpiryDays                *int `json:"idle_expiry_days,omitempty"`
	PlanJudgeMaxRounds            *int `json:"plan_judge_max_rounds,omitempty"`
	SupervisionMaxAttempts        *int `json:"supervision_max_attempts,omitempty"`
	SupervisionTurnTimeoutSeconds *int `json:"supervision_turn_timeout_seconds,omitempty"`
} {
	t.Helper()
	w := getPlan(t, api, id)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var got gen.Plan
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	return got.Bounds
}

// TestPlanPut_TitleOnlyEdit_PreservesEveryBound is the headline property: a
// PUT that mentions ONLY the title must leave every bounds override exactly as
// it was. This is the operator-visible statement of the bug — "I renamed my
// plan and my supervision budget reset" — expressed against the real handler
// rather than the mapper.
func TestPlanPut_TitleOnlyEdit_PreservesEveryBound(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Plans WS")
	id := createPlanWithAllBounds(t, api, wsID)

	wPut := putPlan(t, api, id, `{"title":"Renamed plan"}`)
	require.Equal(t, http.StatusOK, wPut.Code, "body=%s", wPut.Body.String())

	var updated gen.Plan
	require.NoError(t, json.Unmarshal(wPut.Body.Bytes(), &updated))
	assert.Equal(t, "Renamed plan", updated.Title)
	requireBounds(t, updated.Bounds, 11, 22, 333, 4)
	requireBounds(t, getPlanBounds(t, api, id), 11, 22, 333, 4)
}

// TestPlanPut_ShippedSPAEditPayload_PreservesSupervisionBounds reproduces the
// exact body the shipped SPA plan-edit form sends on save
// (CreatePlanSlideOver.tsx: title/goal/description/owner_agent_id/dod/bounds,
// where buildBounds emits plan_judge_max_rounds and idle_expiry_days ONLY).
//
// Under the old replace semantics this silently zeroed both supervision
// overrides on every single save, with no concurrency and no unusual client
// involved — the primary UI did it every time an operator touched a plan.
func TestPlanPut_ShippedSPAEditPayload_PreservesSupervisionBounds(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Plans WS")
	id := createPlanWithAllBounds(t, api, wsID)

	spaSave := `{"title":"Bounded plan","owner_agent_id":"` + testPlansAgentID + `",` +
		`"bounds":{"plan_judge_max_rounds":11,"idle_expiry_days":22}}`
	wPut := putPlan(t, api, id, spaSave)
	require.Equal(t, http.StatusOK, wPut.Code, "body=%s", wPut.Body.String())

	// The two fields the form knows about keep their submitted values; the two
	// it has no inputs for survive untouched.
	requireBounds(t, getPlanBounds(t, api, id), 11, 22, 333, 4)
}

// TestPlanPut_PartialBounds_MergesInsteadOfReplacing generalises the above to
// the minimal partial object: a PUT naming ONE bounds field changes that field
// and leaves the other three alone.
func TestPlanPut_PartialBounds_MergesInsteadOfReplacing(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Plans WS")
	id := createPlanWithAllBounds(t, api, wsID)

	wPut := putPlan(t, api, id, `{"bounds":{"idle_expiry_days":99}}`)
	require.Equal(t, http.StatusOK, wPut.Code, "body=%s", wPut.Body.String())

	requireBounds(t, getPlanBounds(t, api, id), 11, 99, 333, 4)
}

// TestPlanPut_ExplicitBoundsValuesWin guards the opposite failure mode: merge
// must not decay into "ignore". Every field named in the request — including
// the two new supervision ones — must overwrite its stored value.
func TestPlanPut_ExplicitBoundsValuesWin(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Plans WS")
	id := createPlanWithAllBounds(t, api, wsID)

	wPut := putPlan(t, api, id, `{"bounds":{"plan_judge_max_rounds":1,"idle_expiry_days":2,`+
		`"supervision_turn_timeout_seconds":3,"supervision_max_attempts":5}}`)
	require.Equal(t, http.StatusOK, wPut.Code, "body=%s", wPut.Body.String())

	requireBounds(t, getPlanBounds(t, api, id), 1, 2, 3, 5)
}

// TestPlanPut_SupervisionBoundsSettableOnPlanWithNoBounds covers the
// merge-from-nil path: a plan created with no bounds at all can still have a
// supervision override installed by PUT, and only that field is set.
func TestPlanPut_SupervisionBoundsSettableOnPlanWithNoBounds(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Plans WS")

	w := postPlan(t, api, wsID,
		`{"workspace_id":"`+wsID+`","title":"Unbounded","owner_agent_id":"`+testPlansAgentID+`"}`)
	require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())
	var created gen.Plan
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	require.Nil(t, created.Bounds, "a plan created without bounds must not invent them")

	wPut := putPlan(t, api, created.Id, `{"bounds":{"supervision_max_attempts":7}}`)
	require.Equal(t, http.StatusOK, wPut.Code, "body=%s", wPut.Body.String())

	got := getPlanBounds(t, api, created.Id)
	require.NotNil(t, got)
	require.NotNil(t, got.SupervisionMaxAttempts)
	assert.Equal(t, 7, *got.SupervisionMaxAttempts)
	assert.Nil(t, got.PlanJudgeMaxRounds, "merge must not fabricate the fields nobody set")
	assert.Nil(t, got.IdleExpiryDays)
	assert.Nil(t, got.SupervisionTurnTimeoutSeconds)
}

// TestPlanPut_BoundsOnMissingPlan_Returns404 guards the handler restructure
// the merge required. The bounds merge needs the stored plan, so handlePlanPut
// now loads it whenever `bounds` is present — a read that previously only
// happened for dod/owner_agent_id. A bounds-only PUT to an unknown id must
// still 404 (it used to reach plan.Store.Update and 404 from ErrNotFound); the
// new early read must produce the same status, not a 500.
func TestPlanPut_BoundsOnMissingPlan_Returns404(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	createTestWorkspace(t, api, "Plans WS")

	w := putPlan(t, api, "01JXNOSUCHPLANIDAAAAAAAAAA", `{"bounds":{"idle_expiry_days":5}}`)
	assert.Equal(t, http.StatusNotFound, w.Code, "body=%s", w.Body.String())
}

// TestPlanBounds_SupervisionValidationStillBites proves widening the wire did
// not widen what is ACCEPTED: plan.validatePlanBounds rejects < 1 on both new
// fields, and that rejection must surface as a 400 on create and on update
// rather than being swallowed or persisted.
//
// The zero case is checked on the store's own validation rather than the
// schema's `minimum: 1` because an explicit 0 is the value most likely to
// arrive from a client that serialises unset ints as 0.
func TestPlanBounds_SupervisionValidationStillBites(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Plans WS")

	for _, tc := range []struct {
		name  string
		field string
	}{
		{"turn timeout", "supervision_turn_timeout_seconds"},
		{"max attempts", "supervision_max_attempts"},
	} {
		t.Run(tc.name+" rejected at create", func(t *testing.T) {
			w := postPlan(t, api, wsID, `{"workspace_id":"`+wsID+`","title":"Bad",`+
				`"owner_agent_id":"`+testPlansAgentID+`","bounds":{"`+tc.field+`":0}}`)
			assert.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
		})

		t.Run(tc.name+" rejected at update", func(t *testing.T) {
			id := createPlanWithAllBounds(t, api, wsID)
			w := putPlan(t, api, id, `{"bounds":{"`+tc.field+`":0}}`)
			assert.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
			// A rejected update must not have half-applied: the stored
			// overrides are exactly as they were.
			requireBounds(t, getPlanBounds(t, api, id), 11, 22, 333, 4)
		})
	}
}
