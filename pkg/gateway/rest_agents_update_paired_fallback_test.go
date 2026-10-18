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
	for _, tc := range []struct{ name, entry string }{
		{"provider_missing", `{"model":"gpt-4o-mini"}`},
		{"provider_empty", `{"model":"gpt-4o-mini","provider":""}`},
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
			assert.Contains(t, w.Body.String(), "gpt-4o-mini", "the refusal must name the offending entry")
			assert.Contains(t, w.Body.String(), "provider", "the refusal must say what is missing")

			after, err := agentstore.New(api.homePath).Get("test-agent")
			require.NoError(t, err)
			assert.Equal(t, before.FallbackModels, after.FallbackModels, "a refused PUT must not save any fallback")
		})
	}
}
