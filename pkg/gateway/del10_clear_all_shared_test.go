package gateway

import (
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

// DEL-10 / A1: "clear all sessions" must clear the SHARED store, where every chat
// lives. It used to loop the per-agent stores only, so the data the user asked to
// delete survived.
func TestHandleClearSessions_ClearsSessionsOfTheSharedStore(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
			List:     []config.AgentConfig{{ID: "agent-a", Name: "Agent A", Home: tmpDir}},
		},
	}
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	shared := al.GetSessionStore()
	require.NotNil(t, shared)
	_, err := shared.NewSession(session.SessionTypeChat, "test", "agent-a")
	require.NoError(t, err)
	_, err = shared.NewSession(session.SessionTypeChat, "test", "agent-a")
	require.NoError(t, err)

	api := &restAPI{agentLoop: al, taskStore: task.New(tmpDir + "/tasks")}
	w := httptest.NewRecorder()
	api.HandleClearSessions(w, httptest.NewRequest(http.MethodDelete, "/api/v1/sessions/all", nil))
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	left, err := shared.ListSessions()
	require.NoError(t, err)
	assert.Empty(t, left, "every shared-store session is gone after clear-all")
}
