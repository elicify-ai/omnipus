package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Fix round 8 (issue #1340, founder rule 2026-10-18: "provider and model come
// always in pairs, never alone ... the backend must also enforce that"). A PUT
// whose fallback_models carries an entry without a provider is refused with a
// message naming that entry, and nothing is saved.
func TestUpdateAgent_FallbackModels_RefusesUnpairedEntry(t *testing.T) {
	for _, tc := range []struct{ name, entry, names, missing string }{
		{"provider_missing", `{"model":"gpt-4o-mini"}`, "gpt-4o-mini", "provider"},
		{"provider_empty", `{"model":"gpt-4o-mini","provider":""}`, "gpt-4o-mini", "provider"},
		{"model_empty", `{"model":"","provider":"openai"}`, "fallback_models[1]", "model"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := buildExecutorTestAPI(t)
			before, err := agentstore.New(api.homePath).Get("test-agent")
			require.NoError(t, err)

			body := `{"fallback_models":[{"model":"claude-sonnet-4.6","provider":"anthropic"},` + tc.entry + `]}`
			w := httptest.NewRecorder()
			r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/test-agent", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			api.HandleAgents(w, r)

			require.Equal(t, http.StatusBadRequest, w.Code, "an unpaired fallback must be refused; body: %s", w.Body.String())
			assert.Contains(t, w.Body.String(), tc.names, "the refusal must name the offending entry")
			assert.Contains(t, w.Body.String(), tc.missing, "the refusal must say what is missing")

			after, err := agentstore.New(api.homePath).Get("test-agent")
			require.NoError(t, err)
			assert.Equal(t, before.FallbackModels, after.FallbackModels, "a refused PUT must not save any fallback")
		})
	}
}
