// rest_plans_wire_coverage_test.go — the mechanical net over toWirePlan.
//
// THE DEFECT THIS EXISTS TO MAKE UNREPEATABLE
//
// `supervision`, `source_channel` and `source_chat_id` were added to
// contracts/components/schemas/Plan.yaml, generated into gen.Plan, and
// hand-populated in the contract fixtures — but toWirePlan was never touched,
// so no plan response ever carried them. Two pre-existing siblings,
// `owner_session_id` and `last_unmet_terminal_signature`, were missing the same
// way: 5 of 29 top-level contract fields unreachable through the only mapper
// all seven plan response sites use (list, create, get, put, approve, stop,
// restart).
//
// Nothing caught it. pkg/api/generated/contract_test.go checks schema
// VALIDITY, and every one of the five is optional — so their absence validates
// perfectly. FR-024's whole point is that an undelivered supervisor wake is
// recorded at `supervision.wake_error` so it is OBSERVABLE; an operator hitting
// GET /api/v1/plans/{id} saw no `supervision` key at all while the plan burned
// its attempts and terminated failed(supervision_unavailable), the recorded
// cause readable only by opening ~/.omnipus/plans/<id>.json by hand.
//
// WHY THIS FILE IS SHAPED THE WAY IT IS
//
// A hand-written "assert these five fields" test rots exactly the way the
// mapper did: the next field added to Plan.yaml is not in the list, so the list
// keeps passing while the contract grows past it. So the headline test does not
// name fields at all. It walks gen.Plan's own json tags by REFLECTION and
// asserts every one is present in a real HTTP response body for a plan whose
// every persisted field is populated. A new contract field is therefore
// unmapped-and-red by default, and can only go green by being mapped or by
// being added to planWireExclusions with a written reason.
//
// The sweep runs over the decoded HTTP body, not over the returned gen.Plan
// struct, because `omitempty` is applied at marshal time: a field can be
// non-nil in Go and still never reach a client. What a client can read is the
// only thing under test here.

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// planWireExclusions names every gen.Plan json path (dotted, e.g.
// "supervision.wake_at") that toWirePlan legitimately never populates, mapped
// to the REASON it is never populated. Membership here is a deliberate,
// reviewable statement — "the server does not send this, and here is why" —
// never a silent gap.
//
// IT IS EMPTY, AND THAT IS THE POINT: every field the plan contract declares is
// currently reachable through the mapper. Adding an entry is how you assert a
// field is server-unsendable; it is not a way to quiet a failing sweep. Before
// adding one, check that the field really has no plan.Plan (or computed)
// source — the five fields this file was written for all looked unsendable
// right up until someone read the struct.
//
// A stale entry is caught too: TestToWirePlan_EveryContractFieldReachable
// fails if a key here names a path that no longer exists on gen.Plan, so a
// renamed or deleted field cannot leave a dead excuse behind.
var planWireExclusions = map[string]string{}

// --- the fully-populated fixture ---------------------------------------------

// The five RFC 3339 stamps the fixture uses, each a DIFFERENT instant. A
// presence-only sweep cannot tell `started_at` mapped from StartedAt apart from
// `started_at` mapped from ApprovedAt — both are simply "present". Distinct
// values let TestToWirePlan_LifecycleTimestampsAreNotCrossWired pin the actual
// wiring, so the sweep cannot pass on a coincidence.
const (
	planWireApprovedAt  = "2026-07-01T10:00:00Z"
	planWireStartedAt   = "2026-07-02T11:00:00Z"
	planWireCompletedAt = "2026-07-03T12:00:00Z"
	planWireActivityAt  = "2026-07-04T13:00:00Z"
	planWireWakeAt      = "2026-07-05T14:00:00Z"
)

// fullyPopulatedPlan builds a plan.Plan with EVERY wire-reachable persisted
// field set to a non-zero, mutually distinct value, and persists it through the
// real store so the sweep reads back exactly what a live install would serve.
//
// Every value here is deliberately non-default: a field that happens to match
// its zero value would be indistinguishable from "the mapper dropped it" once
// `omitempty` runs, which is the precise failure mode under test.
//
// State is `failed` because plan.Plan.normalize rejects a failed_reason on any
// other state — and failed_reason is itself a wire field the sweep must see.
func fullyPopulatedPlan(t *testing.T, api *restAPI, wsID string) *plan.Plan {
	t.Helper()

	idleDays, judgeRounds := 22, 11
	turnTimeout, maxAttempts := 333, 4
	minCount, maxCount := 2, 9

	p := &plan.Plan{
		WorkspaceID:  wsID,
		Title:        "Fully populated plan",
		Goal:         "every contract field reachable",
		Description:  "seeded by fullyPopulatedPlan",
		Rationale:    "pins the toWirePlan coverage sweep",
		OwnerAgentID: testPlansAgentID,
		Owner:        "operator",
		CreatedBy:    "operator",
		State:        plan.StateFailed,
		FailedReason: plan.FailedReasonSupervisionUnavailable,
		PlanPhase:    plan.PhaseAwaitingSupervision,

		// Two criteria, not one: a `check` criterion may not carry a behavior
		// payload and vice versa (task.validateCriterion), so the `dod` element
		// shape can only be covered in full by the union of both kinds. The
		// sweep unions the keys across array elements for exactly this reason.
		DoD: []task.AcceptanceCriterion{
			{
				ID:         "11111111-1111-4111-8111-111111111111",
				Kind:       task.KindCheck,
				Text:       "the build is green",
				Status:     task.CritPending,
				Author:     task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "operator"},
				Check:      &task.CriterionCheck{Command: "go build ./...", ExpectedExitCode: 3},
				Provenance: task.ProvenanceStated,
			},
			{
				ID:     "22222222-2222-4222-8222-222222222222",
				Kind:   task.KindBehavior,
				Text:   "the agent actually ran the tests",
				Status: task.CritPending,
				Author: task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: testPlansAgentID},
				Behavior: &task.CriterionBehavior{
					Tool:     "bash",
					MinCount: &minCount,
					MaxCount: &maxCount,
					Scope:    task.BehaviorScopeAttempt,
				},
			},
		},

		Bounds: &plan.PlanBounds{
			IdleExpiryDays:                &idleDays,
			PlanJudgeMaxRounds:            &judgeRounds,
			SupervisionTurnTimeoutSeconds: &turnTimeout,
			SupervisionMaxAttempts:        &maxAttempts,
		},

		JudgeRounds:    7,
		ActiveLoop:     true,
		PausedReason:   plan.PausedReasonOwnerDisabled,
		LastActivityAt: planWireActivityAt,
		ApprovedAt:     planWireApprovedAt,
		StartedAt:      planWireStartedAt,
		CompletedAt:    planWireCompletedAt,

		LastUnmetTerminalSignature: "sig-abc123",
		OwnerSessionID:             "01JOWNERSESSION0000000001",

		Supervision: &plan.Supervision{
			WakeAt:           planWireWakeAt,
			WakeError:        "publish to telegram failed: 502 bad gateway",
			Attempts:         2,
			CorrectionRounds: 5,
			SessionID:        "01JSUPERVISIONSESSION00001",
		},

		SourceChannel: "telegram",
		SourceChatID:  "-1001234567890",
	}

	require.NoError(t, api.planStore.Create(p), "seeding the fully-populated plan")
	return p
}

// getPlanBody issues the real GET /api/v1/plans/{id} and returns the decoded
// response body as a generic JSON object. Generic, not gen.Plan: decoding into
// the typed struct would silently re-materialise absent keys as nil pointers
// and hide the very omission under test.
func getPlanBody(t *testing.T, api *restAPI, id string) map[string]any {
	t.Helper()
	w := getPlan(t, api, id)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body), "body=%s", w.Body.String())
	return body
}

// --- the reflection sweep ------------------------------------------------------

// timeType is compared by identity so time.Time — a struct that marshals to a
// plain string — is never recursed into as if it were a wire object.
var timeType = reflect.TypeOf(time.Time{})

// jsonFieldName returns the wire name of a struct field, or "" when the field
// does not cross the wire at all (`json:"-"`, or unexported).
func jsonFieldName(f reflect.StructField) string {
	if f.PkgPath != "" { // unexported
		return ""
	}
	name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
	if name == "" || name == "-" {
		return ""
	}
	return name
}

// deref unwraps pointer indirection so a *T and a T are swept identically.
func derefType(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

// mergeArrayElements unions the keys of every object in a JSON array into one
// synthetic object, first non-nil value winning.
//
// This exists for `dod`, whose element shape can only be covered by more than
// one element: a `check` criterion must not carry `behavior` and a `behavior`
// criterion must not carry `check` (task.validateCriterion), so no single
// element can ever exhibit every field the contract declares. Unioning asserts
// the MAPPER can emit each field, which is the property under test — not that
// any one criterion carries all of them, which would be invalid data.
func mergeArrayElements(t *testing.T, node any, path string) map[string]any {
	t.Helper()
	elems, ok := node.([]any)
	require.Truef(t, ok, "%s: expected a JSON array, got %T", path, node)
	require.NotEmptyf(t, elems, "%s: array is empty — the fixture must populate it or its element fields cannot be swept", path)

	merged := map[string]any{}
	for i, e := range elems {
		obj, ok := e.(map[string]any)
		require.Truef(t, ok, "%s[%d]: expected a JSON object, got %T", path, i, e)
		for k, v := range obj {
			if cur, seen := merged[k]; !seen || cur == nil {
				merged[k] = v
			}
		}
	}
	return merged
}

// sweepWireFields walks typ's json tags against the decoded payload at the same
// level, recording every path it visits into visited, and failing for any field
// the payload does not carry.
//
// Recursion is what catches a whole sub-object going missing — which is exactly
// how `supervision` (five fields, one key) was lost. Checking only the 29
// top-level names would still have caught `supervision`, but would not catch a
// SIXTH supervision member being added later and never mapped.
func sweepWireFields(t *testing.T, typ reflect.Type, node any, path string, visited map[string]bool) {
	t.Helper()
	typ = derefType(typ)
	require.Equalf(t, reflect.Struct, typ.Kind(), "%s: sweep target is not a struct", path)

	obj, ok := node.(map[string]any)
	require.Truef(t, ok, "%s: expected a JSON object, got %T", path, node)

	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		name := jsonFieldName(f)
		if name == "" {
			continue
		}
		full := name
		if path != "" {
			full = path + "." + name
		}
		visited[full] = true

		if reason, excluded := planWireExclusions[full]; excluded {
			require.NotEmptyf(t, reason, "%s: exclusion must state a reason", full)
			t.Logf("sweep: skipping %s — documented exclusion: %s", full, reason)
			continue
		}

		v, present := obj[name]
		require.Truef(t, present,
			"CONTRACT FIELD NOT SENT: %q is declared on gen.Plan but toWirePlan never populates it, "+
				"so no API client can ever read it. Map it in toWirePlan (pkg/gateway/rest_plans.go), "+
				"or — only if the server genuinely never sends it — add %q to planWireExclusions with a written reason.",
			full, full)
		require.NotNilf(t, v, "CONTRACT FIELD NULL: %q is present but null in the response body", full)

		ft := derefType(f.Type)
		switch {
		case ft == timeType:
			// Marshals to an RFC 3339 string, not an object — nothing to recurse into.
		case ft.Kind() == reflect.Slice:
			if et := derefType(ft.Elem()); et.Kind() == reflect.Struct && et != timeType {
				sweepWireFields(t, et, mergeArrayElements(t, v, full), full, visited)
			}
		case ft.Kind() == reflect.Struct:
			sweepWireFields(t, ft, v, full, visited)
		}
	}
}

// TestToWirePlan_EveryContractFieldReachable is the headline regression net.
//
// For a plan whose every persisted field is populated, EVERY json field
// gen.Plan declares — top level and nested — must be present in the real
// GET /api/v1/plans/{id} response body. It names no field of its own, so it
// cannot drift out of date with the contract: the next field added to
// Plan.yaml is red here until it is mapped.
func TestToWirePlan_EveryContractFieldReachable(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Plans WS")
	p := fullyPopulatedPlan(t, api, wsID)

	visited := map[string]bool{}
	sweepWireFields(t, reflect.TypeOf(gen.Plan{}), getPlanBody(t, api, p.ID), "", visited)

	// A stale exclusion is a lie left behind by a rename or a deletion. Fail on
	// any entry naming a path the sweep never reached.
	for path, reason := range planWireExclusions {
		assert.Truef(t, visited[path],
			"STALE EXCLUSION: planWireExclusions names %q (%q) but no such field exists on gen.Plan — remove it",
			path, reason)
	}

	// Guard the guard: if the sweep silently walked nothing (a refactor that
	// broke tag parsing, say), every assertion above is vacuous. gen.Plan has
	// 29 top-level fields today; assert the top-level count directly so a sweep
	// that quietly stops walking is itself a failure.
	topLevel := 0
	for path := range visited {
		if !strings.Contains(path, ".") {
			topLevel++
		}
	}
	assert.Equalf(t, reflect.TypeOf(gen.Plan{}).NumField(), topLevel,
		"sweep visited %d top-level fields but gen.Plan declares %d — the walker skipped something",
		topLevel, reflect.TypeOf(gen.Plan{}).NumField())
	assert.Greaterf(t, len(visited), topLevel, "sweep never recursed into any nested object")
}

// --- outcome tests: the five fields, asserted through the wire ----------------

// TestPlanGet_SupervisionWakeErrorIsReadableFromHTTPResponse is FR-024 stated as
// the operator outcome it exists for: a durably-recorded undelivered supervisor
// wake must be readable from the HTTP response, not merely present in the
// struct or on disk. Asserted against the raw decoded body so `omitempty` is
// exercised for real.
func TestPlanGet_SupervisionWakeErrorIsReadableFromHTTPResponse(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Plans WS")
	p := fullyPopulatedPlan(t, api, wsID)

	body := getPlanBody(t, api, p.ID)
	sup, ok := body["supervision"].(map[string]any)
	require.True(t, ok, "supervision missing from the GET body: %v", body)
	assert.Equal(t, "publish to telegram failed: 502 bad gateway", sup["wake_error"],
		"FR-024: the recorded wake failure must be observable over the API")
	assert.Equal(t, planWireWakeAt, sup["wake_at"])
	assert.Equal(t, "01JSUPERVISIONSESSION00001", sup["session_id"])
	assert.EqualValues(t, 2, sup["attempts"])
	assert.EqualValues(t, 5, sup["correction_rounds"])
}

// TestPlanGet_SupervisionCountersPresentAtZero pins the one place a zero is
// load-bearing rather than empty. FailedReason's own contract text instructs a
// reader to tell the two `judge_rounds_exhausted` causes apart via
// `supervision.correction_rounds == 0` — an instruction no client can follow if
// the server omits the field exactly when it is zero. Both counters are
// therefore emitted unconditionally once the supervision object exists.
func TestPlanGet_SupervisionCountersPresentAtZero(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Plans WS")

	p := &plan.Plan{
		WorkspaceID:  wsID,
		Title:        "Round ceiling, no correction ever applied",
		OwnerAgentID: testPlansAgentID,
		State:        plan.StateFailed,
		FailedReason: plan.FailedReasonJudgeRoundsExhausted,
		Supervision:  &plan.Supervision{Attempts: 0, CorrectionRounds: 0},
	}
	require.NoError(t, api.planStore.Create(p))

	sup, ok := getPlanBody(t, api, p.ID)["supervision"].(map[string]any)
	require.True(t, ok, "supervision object dropped when both counters are zero")

	v, present := sup["correction_rounds"]
	require.True(t, present,
		"correction_rounds omitted at zero — FR-035's `== 0` disambiguation becomes unreadable")
	assert.EqualValues(t, 0, v)

	v, present = sup["attempts"]
	require.True(t, present, "attempts omitted at zero")
	assert.EqualValues(t, 0, v)

	// The three genuinely-optional members keep normal omitempty semantics:
	// each has a real "not set yet" state (never woken, last wake succeeded, no
	// session minted) that must NOT be reported as an empty string.
	assert.NotContains(t, sup, "wake_at")
	assert.NotContains(t, sup, "wake_error")
	assert.NotContains(t, sup, "session_id")
}

// TestPlanGet_ChatOriginAndSessionLinkageAreReadable covers the remaining four
// previously-unreachable fields end to end.
func TestPlanGet_ChatOriginAndSessionLinkageAreReadable(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Plans WS")
	p := fullyPopulatedPlan(t, api, wsID)

	body := getPlanBody(t, api, p.ID)
	assert.Equal(t, "telegram", body["source_channel"])
	assert.Equal(t, "-1001234567890", body["source_chat_id"])
	assert.Equal(t, "01JOWNERSESSION0000000001", body["owner_session_id"])
	assert.Equal(t, "sig-abc123", body["last_unmet_terminal_signature"])
}

// TestPlanGet_NoChatOrigin_OmitsSourceFields is the other half of the
// source_channel/source_chat_id contract, and the reason the mapping is
// conditional rather than unconditional. ABSENT IS A LEGITIMATE, EXPECTED
// STATE: a plan created over REST from the Plans UI has no chat origin at all.
// The mapper must not mint a synthetic origin, must not fall back to anything,
// and must not report an empty string — which a client would have to treat as a
// real channel named "".
func TestPlanGet_NoChatOrigin_OmitsSourceFields(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Plans WS")

	body := `{"workspace_id":"` + wsID + `","title":"UI-created plan","owner_agent_id":"` + testPlansAgentID + `"}`
	w := postPlan(t, api, wsID, body)
	require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())
	var created gen.Plan
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))

	got := getPlanBody(t, api, created.Id)
	assert.NotContains(t, got, "source_channel", "a UI-created plan has no chat origin; none must be invented")
	assert.NotContains(t, got, "source_chat_id")
	assert.NotContains(t, got, "supervision", "supervision is absent until the plan first parks")
	assert.NotContains(t, got, "owner_session_id")
	assert.NotContains(t, got, "last_unmet_terminal_signature")
}

// TestToWirePlan_LifecycleTimestampsAreNotCrossWired stops the presence sweep
// from passing on a coincidence. Presence alone cannot tell `started_at` mapped
// from StartedAt apart from `started_at` mapped from ApprovedAt; the fixture
// uses four distinct stamps so the values themselves pin the wiring.
func TestToWirePlan_LifecycleTimestampsAreNotCrossWired(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Plans WS")
	p := fullyPopulatedPlan(t, api, wsID)

	body := getPlanBody(t, api, p.ID)
	assert.Equal(t, planWireApprovedAt, body["approved_at"])
	assert.Equal(t, planWireStartedAt, body["started_at"])
	assert.Equal(t, planWireCompletedAt, body["completed_at"])
	assert.Equal(t, planWireActivityAt, body["last_activity_at"])
}

// TestToWirePlan_EveryResponseSiteUsesTheSameMapper documents why fixing
// toWirePlan alone was sufficient: all seven plan response sites route through
// it, so a field missing from the mapper is missing everywhere, and a field
// added to it appears everywhere. This asserts the two sites with a distinct
// wire path — the LIST response, which reaches the client through
// planListResponse's JSON round-trip into a structurally-identical but
// nominally-distinct generated element type, and the PUT response.
func TestToWirePlan_EveryResponseSiteUsesTheSameMapper(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Plans WS")
	p := fullyPopulatedPlan(t, api, wsID)

	// LIST — the round-trip through planListResponse must not drop the fields.
	visited := map[string]bool{}
	listed := listPlanBodies(t, api, wsID)
	require.Len(t, listed, 1)
	sweepWireFields(t, reflect.TypeOf(gen.Plan{}), listed[0], "", visited)

	// PUT — a title-only edit re-serialises the whole plan through the same
	// mapper; every field must survive the write path too.
	wPut := putPlan(t, api, p.ID, `{"title":"Renamed"}`)
	require.Equal(t, http.StatusOK, wPut.Code, "body=%s", wPut.Body.String())
	var putBody map[string]any
	require.NoError(t, json.Unmarshal(wPut.Body.Bytes(), &putBody))
	assert.Equal(t, "Renamed", putBody["title"])
	sweepWireFields(t, reflect.TypeOf(gen.Plan{}), putBody, "", map[string]bool{})
}

// listPlanBodies issues GET /api/v1/workspaces/{id}/plans through the real
// HandleWorkspaces dispatch and returns each plan as a generic JSON object,
// sorted by id for determinism. Generic, not gen.PlanListResponse, for the same
// reason as getPlanBody: a typed decode would re-materialise absent keys.
func listPlanBodies(t *testing.T, api *restAPI, wsID string) []map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+wsID+"/plans", nil)
	r.URL.Path = "/api/v1/workspaces/" + wsID + "/plans"
	api.HandleWorkspaces(w, r)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var resp struct {
		Plans []map[string]any `json:"plans"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp), "body=%s", w.Body.String())
	sort.Slice(resp.Plans, func(i, j int) bool {
		return toStr(resp.Plans[i]["id"]) < toStr(resp.Plans[j]["id"])
	})
	return resp.Plans
}

func toStr(v any) string {
	s, _ := v.(string)
	return s
}
