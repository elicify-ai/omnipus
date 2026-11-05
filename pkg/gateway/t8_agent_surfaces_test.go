package gateway

// RED oracle: tool-parity r17 §3.11/§7.11 and 7.7, 9.16, 11.1–11.3.
// Real REST handlers, temp config and protected entity store.
import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRestAgentWrite_ExecutorUnknownFieldTexts(t *testing.T) {
	for _, tc := range []struct {
		field string
		value any
		want  string
	}{
		{"kind", "external-cli", "executor.kind is derived from the agent type and cannot be set."},
		{"cli", "codex", "executor.cli is locked after create; create a new agent to switch CLIs."},
		{"cli_path", "/opt/tools/codex", "cli_path cannot be set on an agent. The program for the chosen CLI is set by a person in Settings → Third-party agents."},
	} {
		t.Run(tc.field, func(t *testing.T) {
			api := buildExecutorTestAPI(t)
			s := agentstore.New(api.homePath)
			before, err := s.ReadState("test-agent")
			require.NoError(t, err)
			body, err := json.Marshal(map[string]any{"revision": before.Revision, "executor": map[string]any{tc.field: tc.value}})
			require.NoError(t, err)
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent", bytes.NewReader(body))
			api.updateAgent(w, r, "test-agent")
			require.Equal(t, http.StatusBadRequest, w.Code, "%s", w.Body.String())
			var out map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
			assert.Equal(t, "executor."+tc.field, out["field"])
			assert.Equal(t, tc.want, out["error"])
			after, err := s.ReadState("test-agent")
			require.NoError(t, err)
			assert.Equal(t, before.Revision, after.Revision, "refusal must write nothing")
		})
	}
}

func TestRestCreate_KeepsFallbackModels(t *testing.T) {
	api := buildExecutorTestAPI(t)
	fallbacks := []any{map[string]any{"provider": "openrouter", "model": "fallback-one"}, map[string]any{"provider": "openai", "model": "fallback-two"}}
	body, err := json.Marshal(map[string]any{"name": "T8 Fallbacks", "type": "Main", "soul": "write carefully", "fallback_models": fallbacks})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", bytes.NewReader(body))
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusCreated, w.Code, "7.7 valid paired fallbacks accepted: %s", w.Body.String())
	var created map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	id, ok := created["id"].(string)
	require.True(t, ok, "create response id: %v", created)
	s := agentstore.New(api.homePath)
	a, err := s.Get(id)
	require.NoError(t, err)
	require.Len(t, a.FallbackModels, 2, "7.7 both validated fallback models must persist")
	assert.Equal(t, "fallback-one", a.FallbackModels[0].Model)
	assert.Equal(t, "openrouter", a.FallbackModels[0].Provider)
	assert.Equal(t, "fallback-two", a.FallbackModels[1].Model)
	assert.Equal(t, "openai", a.FallbackModels[1].Provider)
}
