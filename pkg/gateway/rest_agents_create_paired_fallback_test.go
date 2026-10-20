package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Fix round 9 (issue #1340, founder rule 2026-10-18: the backend enforces
// provider+model pairs on every writer). POST /api/v1/agents refuses a
// fallback entry that lacks either half, naming the entry; no agent is
// created.
func TestCreateAgent_FallbackModels_RefusesUnpairedEntry(t *testing.T) {
	for _, tc := range []struct{ name, entry, names string }{
		{"provider_missing", `{"model":"gpt-4o-mini"}`, "gpt-4o-mini"},
		{"provider_empty", `{"model":"gpt-4o-mini","provider":""}`, "gpt-4o-mini"},
		{"model_empty", `{"model":"","provider":"openai"}`, "fallback_models[1]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := buildExecutorTestAPI(t)
			before := len(api.agentLoop.GetRegistry().ListAgentIDs())
			body := `{"name":"PairedCreate","type":"Main","soul":"soul content","fallback_models":[{"model":"claude-sonnet-4.6","provider":"anthropic"},` + tc.entry + `]}`
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			api.HandleAgents(w, r)

			require.Equal(t, http.StatusBadRequest, w.Code, "an unpaired fallback must be refused; body: %s", w.Body.String())
			assert.Contains(t, w.Body.String(), tc.names, "the refusal must name the offending entry")
			assert.Equal(t, before, len(api.agentLoop.GetRegistry().ListAgentIDs()), "a refused create must not create an agent")
		})
	}
}
