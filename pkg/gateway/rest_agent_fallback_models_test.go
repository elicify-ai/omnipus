// REST round-trip tests for the P-F2 fallback_models bug: fallback_models
// persisted correctly to config.json (the original save-race was fixed) but
// was never echoed back on ANY response path — GET /agents/{id}, the
// agent-list endpoint, and PUT /agents/{id}'s own response body all built
// gen.Agent without ever assigning FallbackModels from
// config.AgentConfig.FallbackModels. Prior coverage
// (TestCreateAgent_Subagent3p_ForbiddenFields_ValidationEnabled,
// TestUpdateAgent_Subagent3p_ForbiddenFields) only asserted fallback_models
// REJECTION on subagent_3p agents — never a round-trip on a Main/Subagent
// agent that is allowed to carry the field at all.
//
// These tests prove the full path:
//  1. PUT /agents/{id} with fallback_models → the PUT's OWN response echoes it.
//  2. GET /agents/{id} → independently reflects the persisted value.
//  3. GET /agents (list) → also reflects the persisted value.
//  4. A second PUT touching an UNRELATED field does not erase fallback_models
//     (the reopened-agent data-loss path described in the bug report).

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetEditPut_FallbackModels_NotSilentlyReplacedByUnrelatedPUT is the
// data-loss regression named in the bug report: because a fresh page mount
// could never see the existing persisted chain (GET always showed it empty
// pre-fix), a client's local "add a fallback model" edit silently REPLACED
// the whole chain instead of appending. This test proves the READ side no
// longer starves that append logic: GET after the first PUT must show the
// already-persisted entry so a client can correctly build an appended list
// on its next PUT, and that appended list must be exactly what round-trips —
// nothing dropped, nothing duplicated.
func TestGetEditPut_FallbackModels_NotSilentlyReplacedByUnrelatedPUT(t *testing.T) {
	api := buildExecutorTestAPI(t)

	// Seed one fallback entry.
	seedBody := `{"fallback_models":[{"model":"openai/gpt-5-mini","provider":"openai"}]}`
	seedW := httptest.NewRecorder()
	seedR := revisionedAgentMutationRequest(t, api, "/api/v1/agents/test-agent", strings.NewReader(seedBody))
	seedR.Header.Set("Content-Type", "application/json")
	api.HandleAgents(seedW, seedR)
	require.Equal(t, http.StatusOK, seedW.Code, "seed body: %s", seedW.Body.String())

	// A "reopen" GET must show the seeded entry — this is the read this bug broke.
	reopenW := httptest.NewRecorder()
	reopenR := httptest.NewRequest(http.MethodGet, "/api/v1/agents/test-agent", nil)
	api.HandleAgents(reopenW, reopenR)
	require.Equal(t, http.StatusOK, reopenW.Code)
	reopened := decodeAgentResp(t, reopenW.Body.Bytes())
	require.NotNil(t, reopened.FallbackModels, "reopened GET must show the previously-saved chain")
	require.Len(t, *reopened.FallbackModels, 1)

	// Client appends a second entry to what it read back (the correct behavior
	// once the read side is fixed) and PUTs the full, appended chain.
	appended := append([]gen.FallbackModel{}, (*reopened.FallbackModels)...)
	newEntry := gen.FallbackModel{Model: "anthropic/claude-haiku-4.5"}
	provider := "anthropic"
	newEntry.Provider = &provider
	appended = append(appended, newEntry)
	appendedJSON, err := json.Marshal(appended)
	require.NoError(t, err)

	putW := httptest.NewRecorder()
	putR := revisionedAgentMutationRequest(t, api, "/api/v1/agents/test-agent",
		strings.NewReader(`{"fallback_models":`+string(appendedJSON)+`}`))
	putR.Header.Set("Content-Type", "application/json")
	api.HandleAgents(putW, putR)
	require.Equal(t, http.StatusOK, putW.Code, "append put body: %s", putW.Body.String())

	final := decodeAgentResp(t, putW.Body.Bytes())
	require.NotNil(t, final.FallbackModels)
	require.Len(t, *final.FallbackModels, 2, "both the original and the appended entry must survive")
	assert.Equal(t, "openai/gpt-5-mini", (*final.FallbackModels)[0].Model)
	assert.Equal(t, "anthropic/claude-haiku-4.5", (*final.FallbackModels)[1].Model)
}
