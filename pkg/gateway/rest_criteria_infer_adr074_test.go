// Omnipus — ADR-074 D2 REST criteria-inference tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// rest_criteria_infer_adr074_test.go — judgment-first-criteria-spec test #7:
// REST create AND update infer an omitted criterion kind from the payload
// (the gateway passes the absent kind THROUGH; inference happens in the
// store's normalizeCriteria), including the pinned EC-8 case: a kind-omitted
// all-check criteria set on an agent-assigned task is ACCEPTED at the REST
// layer — the ADR-049 D2-rule-5 bash-policy gate exists only on the two gated
// tools, and extending it to REST is an explicitly tracked follow-up, not
// silently covered here.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// postTaskRaw posts a raw JSON body to /api/v1/tasks and returns the recorder.
func postTaskRaw(t *testing.T, api *restAPI, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/tasks"
	api.HandleTasks(w, r)
	return w
}

// taskCriteria decodes the response body's criteria array into loose maps.
func taskCriteria(t *testing.T, body []byte) []map[string]any {
	t.Helper()
	var resp struct {
		Id       string           `json:"id"`
		Criteria []map[string]any `json:"criteria"`
	}
	require.NoError(t, json.Unmarshal(body, &resp))
	return resp.Criteria
}

// criterionJSON builds one wire criterion carrying the Input-required
// author/status but NO kind.
func criterionJSON(text, payload string) string {
	if payload == "" {
		return fmt.Sprintf(`{"text":%q,"author":{"kind":"user","id":"dan"},"status":"pending"}`, text)
	}
	return fmt.Sprintf(`{"text":%q,%s,"author":{"kind":"user","id":"dan"},"status":"pending"}`, text, payload)
}

// TestRESTTaskCreate_KindInference covers create-side inference: no payload
// => prose; check payload agent-assigned => check AND accepted (EC-8 pinned
// ungated); dual payload => 400.
func TestRESTTaskCreate_KindInference(t *testing.T) {
	// newTestRestAPIWithAgent registers agent 01JXTESTAGENTSTARTTEST001 in the
	// loop's registry, so the EC-8 subtest's agent_id assignment can pass
	// validateTaskAgentID (registry + workspace TeamSet membership).
	api := newTestRestAPIWithAgent(t)
	wsID := ensureTestWorkspace(t, api)
	setWorkspaceCoreTeam(t, api, wsID, []string{"01JXTESTAGENTSTARTTEST001"})

	t.Run("no_payload_inferred_prose", func(t *testing.T) {
		body := fmt.Sprintf(`{"title":"prose task","action":"llm","workspace_id":%q,"criteria":[%s]}`,
			wsID, criterionJSON("the summary reads well", ""))
		w := postTaskRaw(t, api, body)
		require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())
		crit := taskCriteria(t, w.Body.Bytes())
		require.Len(t, crit, 1)
		assert.Equal(t, "prose", crit[0]["kind"], "omitted kind with no payload must infer prose")
	})

	t.Run("EC8_kind_omitted_all_check_agent_assigned_accepted_ungated", func(t *testing.T) {
		body := fmt.Sprintf(
			`{"title":"machine task","action":"llm","workspace_id":%q,`+
				`"agent_id":"01JXTESTAGENTSTARTTEST001","criteria":[%s]}`,
			wsID, criterionJSON("tests pass",
				`"check":{"command":"go test ./...","expected_exit_code":0}`))
		w := postTaskRaw(t, api, body)
		// EC-8 (pinned, tracked): ACCEPTED — no D2-rule-5 bash-policy gate at
		// the REST layer. If this ever starts failing with a 4xx naming the
		// gate, the tracked follow-up landed and this pin should be updated.
		require.Equal(t, http.StatusCreated, w.Code,
			"EC-8: REST create of a kind-omitted all-check agent-assigned task must be ACCEPTED "+
				"(gate exists only on the two gated tools); body=%s", w.Body.String())
		crit := taskCriteria(t, w.Body.Bytes())
		require.Len(t, crit, 1)
		assert.Equal(t, "check", crit[0]["kind"], "omitted kind with check payload must infer check")
	})

	t.Run("dual_payload_kind_omitted_400", func(t *testing.T) {
		body := fmt.Sprintf(`{"title":"ambiguous","action":"llm","workspace_id":%q,"criteria":[%s]}`,
			wsID, criterionJSON("ambiguous",
				`"check":{"command":"true","expected_exit_code":0},"behavior":{"tool":"bash"}`))
		w := postTaskRaw(t, api, body)
		require.Equal(t, http.StatusBadRequest, w.Code,
			"kind-omitted dual-payload criterion must 400; body=%s", w.Body.String())
	})
}

// TestRESTTaskUpdate_KindInference covers update-side inference (US-1 S6): a
// replacement criteria set with omitted kinds is inferred identically, with
// behavior pointer semantics (explicit 0) surviving the round trip to the
// wire response.
func TestRESTTaskUpdate_KindInference(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)
	tsk := createTaskViaAPI(t, api, "inference update target", wsID)

	patch := fmt.Sprintf(`{"criteria":[%s]}`, criterionJSON("never shells out",
		`"behavior":{"tool":"bash","min_count":0,"max_count":0}`))
	w := patchTask(t, api, tsk.Id, patch)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	crit := taskCriteria(t, w.Body.Bytes())
	require.Len(t, crit, 1)
	assert.Equal(t, "behavior", crit[0]["kind"], "omitted kind with behavior payload must infer behavior")
	beh, ok := crit[0]["behavior"].(map[string]any)
	require.True(t, ok, "behavior payload missing from response: %v", crit[0])
	assert.Equal(t, float64(0), beh["min_count"], "EXPLICIT min_count 0 must round-trip (never default to 1)")
	assert.Equal(t, float64(0), beh["max_count"], "EXPLICIT max_count 0 must round-trip")

	t.Run("update_dual_payload_400", func(t *testing.T) {
		patch := fmt.Sprintf(`{"criteria":[%s]}`, criterionJSON("ambiguous",
			`"check":{"command":"true","expected_exit_code":0},"behavior":{"tool":"bash"}`))
		w := patchTask(t, api, tsk.Id, patch)
		require.Equal(t, http.StatusBadRequest, w.Code,
			"kind-omitted dual-payload criterion must 400 on update too; body=%s", w.Body.String())
	})
}
