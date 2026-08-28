// rest_settings_test.go — 7-reviewer-gate finding: HandleClearSessions
// hand-assembled its response as a bare map[string]any instead of the
// generated gen.ClearAllSessionsResponse contract type, so a future field
// rename in contracts/components/schemas/ClearAllSessionsResponse.yaml could
// silently desync the handler from the schema it claims to implement,
// undetected by any test. These tests pin the wire shape and the underlying
// data path (sessions actually removed, count accurate, warnings only
// present when non-empty).

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// TestHandleClearSessions_NoSessions_ReturnsContractShapeNoWarnings verifies
// DELETE /api/v1/sessions/all returns the gen.ClearAllSessionsResponse shape
// ({"status":"cleared","count":0}) with no "warnings" key when there is
// nothing to clear and nothing went wrong.
func TestHandleClearSessions_NoSessions_ReturnsContractShapeNoWarnings(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{
		agentLoop: al,
		taskStore: task.New(tmpDir + "/tasks"),
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/sessions/all", nil)
	api.HandleClearSessions(w, r)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	status, _ := resp["status"].(string)
	assert.Equal(t, "cleared", status, "status must match the gen.ClearAllSessionsResponseStatusCleared enum value")

	count, hasCount := resp["count"]
	require.True(t, hasCount, "count key must always be present")
	assert.Equal(t, float64(0), count)

	_, hasWarnings := resp["warnings"]
	assert.False(t, hasWarnings, "warnings key must be absent (omitempty) when there were no warnings")
}

// TestHandleClearSessions_RemovesSessionsAndReportsAccurateCount proves the
// full data path: creates real sessions for a registered agent, clears them
// via the handler, and confirms (a) the response count matches what was
// actually removed and (b) the sessions are actually gone from the store
// afterward (round-trip proof, not just a trusted count).
func TestHandleClearSessions_RemovesSessionsAndReportsAccurateCount(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
			List: []config.AgentConfig{
				{ID: "agent-a", Name: "Agent A", Home: tmpDir},
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})

	store := al.GetAgentStore("agent-a")
	require.NotNil(t, store, "agent-a must have a registered session store")

	_, err := store.NewSession(session.SessionTypeChat, "test", "agent-a")
	require.NoError(t, err, "NewSession #1")
	_, err = store.NewSession(session.SessionTypeChat, "test", "agent-a")
	require.NoError(t, err, "NewSession #2")

	before, err := store.ListSessions()
	require.NoError(t, err)
	require.Len(t, before, 2, "sanity: two sessions must exist before clearing")

	api := &restAPI{
		agentLoop: al,
		taskStore: task.New(tmpDir + "/tasks"),
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/sessions/all", nil)
	api.HandleClearSessions(w, r)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "cleared", resp["status"])
	assert.Equal(t, float64(2), resp["count"], "count must reflect the two sessions actually removed")
	_, hasWarnings := resp["warnings"]
	assert.False(t, hasWarnings, "warnings key must be absent when nothing failed")

	after, err := store.ListSessions()
	require.NoError(t, err)
	assert.Empty(t, after, "sessions must actually be gone from the store after clearing (round-trip proof)")
}

// TestHandleClearSessions_WrongMethod_Returns405 verifies the method guard.
func TestHandleClearSessions_WrongMethod_Returns405(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{
		agentLoop: al,
		taskStore: task.New(tmpDir + "/tasks"),
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/all", nil)
	api.HandleClearSessions(w, r)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}
