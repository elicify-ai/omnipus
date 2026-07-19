//go:build !cgo

// rest_judge_verdict_coldload_test.go — review r2 RV2: getSessionMessages
// (the cold-load path GET /sessions/{id}/messages) must parse a persisted
// EntryTypeJudgeVerdict entry's raw-JSON Content into the wire
// Message.verdict field (same shape handleTaskVerdicts / toWireJudgeVerdict
// produce, rest_tasks.go), not leave it absent/broken.

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// TestGetSessionMessages_JudgeVerdictEntry_PopulatesWireVerdict proves the
// RV2 fix: a judge_verdict transcript entry's Content (raw
// json.Marshal(task.JudgeVerdict)) is parsed and attached as the wire
// Message.verdict object, and the response validates against Message.yaml
// (which requires verdict to conform to JudgeVerdict.yaml when present).
func TestGetSessionMessages_JudgeVerdictEntry_PopulatesWireVerdict(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	sessionID := createTestSession(t, api)
	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store, "shared session store must be available")

	verdict := task.JudgeVerdict{
		ID:           "verdict-coldload-1",
		Scope:        "task",
		TaskID:       "task-xyz",
		Round:        1,
		Met:          false,
		Model:        "test-judge-model",
		JudgedAt:     "2026-07-19T12:05:00Z",
		JudgeAgentID: "judge",
		PerCriterion: []task.CriterionVerdict{
			{CriterionID: "c1", Met: false, Reason: "missing evidence"},
		},
	}
	payload, err := json.Marshal(verdict)
	require.NoError(t, err)

	require.NoError(t, store.AppendTranscript(sessionID, session.TranscriptEntry{
		ID:        "verdict-entry-1",
		Type:      session.EntryTypeJudgeVerdict,
		Role:      "system",
		Content:   string(payload),
		AgentID:   "judge",
		Timestamp: time.Date(2026, 7, 19, 12, 5, 0, 0, time.UTC),
	}), "seeding a judge_verdict entry must succeed")

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sessionID+"/messages", nil)
	r.URL.Path = "/api/v1/sessions/" + sessionID + "/messages"
	api.HandleSessions(w, r)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var entries []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &entries))

	var found map[string]any
	for _, e := range entries {
		if e["type"] == "judge_verdict" {
			found = e
			break
		}
	}
	require.NotNil(t, found, "must find the judge_verdict entry in the response")

	v, ok := found["verdict"].(map[string]any)
	require.True(t, ok, "judge_verdict entry must carry a populated 'verdict' object, got: %v", found)
	assert.Equal(t, "verdict-coldload-1", v["id"])
	assert.Equal(t, "task", v["scope"])
	assert.Equal(t, "task-xyz", v["task_id"])
	assert.Equal(t, false, v["met"])
	assert.Equal(t, "judge", v["judge_agent_id"])
	assert.Equal(t, float64(1), v["round"])

	// Bonus: the whole entry must validate against Message.yaml (verdict
	// conforms to JudgeVerdict.yaml, additionalProperties:false honored).
	schema := loadMessageSchema(t)
	assert.NoError(t, schema.Validate(any(found)), "judge_verdict entry with populated verdict must validate against Message.yaml")
}

// TestGetSessionMessages_JudgeVerdictEntry_MalformedContent_OmitsVerdict
// proves a judge_verdict entry with unparseable Content degrades gracefully:
// the entry still round-trips (id/type/content preserved) with no "verdict"
// key, rather than the whole response failing.
func TestGetSessionMessages_JudgeVerdictEntry_MalformedContent_OmitsVerdict(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	sessionID := createTestSession(t, api)
	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store)

	require.NoError(t, store.AppendTranscript(sessionID, session.TranscriptEntry{
		ID:        "bad-verdict-entry",
		Type:      session.EntryTypeJudgeVerdict,
		Role:      "system",
		Content:   "not valid json",
		AgentID:   "judge",
		Timestamp: time.Now().UTC(),
	}), "seeding a malformed judge_verdict entry must still succeed at the store layer")

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sessionID+"/messages", nil)
	r.URL.Path = "/api/v1/sessions/" + sessionID + "/messages"
	api.HandleSessions(w, r)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var entries []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &entries))

	var found map[string]any
	for _, e := range entries {
		if e["id"] == "bad-verdict-entry" {
			found = e
			break
		}
	}
	require.NotNil(t, found, "the entry must still round-trip even with malformed content")
	_, hasVerdict := found["verdict"]
	assert.False(t, hasVerdict, "verdict must be absent (not a broken/partial object) when Content fails to parse")
}
