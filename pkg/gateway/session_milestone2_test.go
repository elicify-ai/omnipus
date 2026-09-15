// Milestone 2 REST handler tests: PUT /api/v1/sessions/{id} (rename)
// and DELETE /api/v1/sessions/{id} (delete).
//
// BDD scenarios:
//   Scenario: Rename session — PUT with valid title returns 200 + updated meta
//   Scenario: Rename with empty title — PUT with empty title returns 400
//   Scenario: Rename non-existent session — PUT returns 404
//   Scenario: Rename persistence — renamed title is readable via GET
//   Scenario: Delete session — DELETE returns 200 + success:true
//   Scenario: Delete non-existent session — DELETE returns 404
//   Scenario: Deleted session gone — GET after DELETE returns 404
//
// Traces to: pkg/gateway/rest.go renameSession + deleteSession (Milestone 2)

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// createTestSession creates a session via POST /api/v1/sessions and returns its ID.
// The test fails fatally if the session cannot be created.
func createTestSession(t *testing.T, api *restAPI) string {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/sessions",
		strings.NewReader(`{"agent_id":"mia","type":"chat"}`))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/sessions"
	api.HandleSessions(w, r)
	require.Equal(t, http.StatusCreated, w.Code,
		"POST /api/v1/sessions must return 201 to set up test; got %d body=%s",
		w.Code, w.Body.String())

	var meta session.UnifiedMeta
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &meta),
		"POST /api/v1/sessions response must unmarshal into UnifiedMeta")
	require.NotEmpty(t, meta.ID, "created session must have a non-empty ID")
	return meta.ID
}
