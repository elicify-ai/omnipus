package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/stretchr/testify/require"
)

// FR-002 rejects unsupported input before writes even when optional schema
// validation is disabled. A valid changed field must not hide an invalid one.
func TestADR090UpdateAgentUnknownFieldsAreZeroWrite(t *testing.T) {
	api := buildExecutorTestAPI(t)
	store := agentstore.New(api.homePath)
	for _, tc := range []struct {
		field string
		value any
	}{
		{"made_up", true},
		{"timeout_seconds", 120},
		{"rate_limits", map[string]any{"calls": 5}},
		{"heartbeat_enabled", true},
		{"heartbeat_interval", 60},
		{"model_params", map[string]any{"made_up": 1}},
	} {
		t.Run(tc.field, func(t *testing.T) {
			before, err := store.ReadState("test-agent")
			require.NoError(t, err)
			raw, err := json.Marshal(map[string]any{
				"revision": before.Revision, "description": "must not persist", tc.field: tc.value,
			})
			require.NoError(t, err)
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent", bytes.NewReader(raw))
			api.updateAgent(w, r, "test-agent")
			require.Equal(t, http.StatusBadRequest, w.Code, "%s", w.Body.String())
			after, err := store.ReadState("test-agent")
			require.NoError(t, err)
			require.Equal(t, before.Revision, after.Revision)
		})
	}
}

func TestADR090UpdateAgentFieldNamesInsideTextAreAllowed(t *testing.T) {
	api := buildExecutorTestAPI(t)
	store := agentstore.New(api.homePath)
	before, err := store.ReadState("test-agent")
	require.NoError(t, err)
	const description = `Explain "updated_at", "made_up" and "timeout_seconds" to the user.`
	raw, err := json.Marshal(map[string]any{"revision": before.Revision, "description": description})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent", bytes.NewReader(raw))
	api.updateAgent(w, r, "test-agent")
	require.Equal(t, http.StatusOK, w.Code, "%s", w.Body.String())
	after, err := store.ReadState("test-agent")
	require.NoError(t, err)
	require.Equal(t, description, after.Agent.Description)
}

func TestADR090UpdateAgentRejectsTrailingJSON(t *testing.T) {
	api := buildExecutorTestAPI(t)
	store := agentstore.New(api.homePath)
	before, err := store.ReadState("test-agent")
	require.NoError(t, err)
	raw, err := json.Marshal(map[string]any{"revision": before.Revision, "description": "must not persist"})
	require.NoError(t, err)
	raw = append(raw, []byte(` {"unexpected":"second object"}`)...)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent", bytes.NewReader(raw))
	api.updateAgent(w, r, "test-agent")
	require.Equal(t, http.StatusBadRequest, w.Code, "%s", w.Body.String())
	after, err := store.ReadState("test-agent")
	require.NoError(t, err)
	require.Equal(t, before.Revision, after.Revision)
}
