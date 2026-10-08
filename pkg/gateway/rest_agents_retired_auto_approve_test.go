package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
)

// The per-agent "Never auto-approve" switch (auto_approve_disabled) is
// retired from the wire (#1221). A caller still sending it must get a visible
// 400 that names the field — never a 200 that silently drops it and leaves
// the caller believing the agent is protected.

func TestUpdateAgent_RejectsRetiredAutoApproveDisabledField(t *testing.T) {
	api := buildExecutorTestAPI(t)
	id := createSubagent3p(t, api)

	before, err := agentstore.New(api.homePath).Get(id)
	require.NoError(t, err)

	for _, body := range []string{`{"auto_approve_disabled":true}`, `{"auto_approve_disabled":false}`} {
		w := httptest.NewRecorder()
		r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/"+id, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		api.HandleAgents(w, r)

		require.Equal(t, http.StatusBadRequest, w.Code, "body %s: %s", body, w.Body.String())
		assert.Contains(t, w.Body.String(), "auto_approve_disabled")
	}

	after, err := agentstore.New(api.homePath).Get(id)
	require.NoError(t, err)
	assert.Equal(t, before, after, "agent record must be unchanged after a rejected PUT")
}

func TestCreateAgent_RejectsRetiredAutoApproveDisabledField(t *testing.T) {
	for _, agentType := range []string{"Main", "Subagent"} {
		t.Run(agentType, func(t *testing.T) {
			api := buildExecutorTestAPI(t)
			body := `{"name":"Retired Switch","type":"` + agentType + `","description":"d","soul":"s","auto_approve_disabled":true}`
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			api.HandleAgents(w, r)

			require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
			assert.Contains(t, w.Body.String(), "auto_approve_disabled")
		})
	}
}
