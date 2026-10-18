package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// F03 / K5 ruling: these are pre-publication failures of THIS attempt, not
// proof of historical absence. The actual authenticated mux/handler/service
// executes before its operation-declared response schema validates the body.
func TestWU4Selection_K5UnavailableResponsesConformToOperationContract(t *testing.T) {
	for _, fault := range []string{"missing_registry", "initialization", "reconciliation"} {
		for _, method := range []string{http.MethodPost, http.MethodGet} {
			t.Run(fault+"/"+method, func(t *testing.T) {
				f := newCardMatrixHTTP(t, secretrequest.Input{}, nil)
				authSnapshot := f.al.GetConfig()
				selectionK5AssertAuthentication(t, f, authSnapshot)
				restore := selectionK5AvailabilityCut(t, f, fault)
				defer restore()
				response := selectionK5Call(t, f, authSnapshot, method,
					f.valuesBody(t, map[string]string{"api_key": cardMatrixHTTPValue}), true)
				selectionK5AssertUnavailableBody(t, response)
				f.requireNoCommitOrResume(t)
				selectionK5ValidateActualResponse(t, method, response)
			})
		}
	}
}

func TestWU4Selection_K5UnavailableNeverClaimsEarlierSubmissionAbsent(t *testing.T) {
	f := newCardMatrixHTTP(t, secretrequest.Input{}, nil)
	authSnapshot := f.al.GetConfig()
	first := f.call(t, http.MethodPost, f.valuesBody(t, map[string]string{"api_key": cardMatrixHTTPValue}))
	state := requireCardMatrixState(t, first, http.StatusOK, f.rec.Card.RequestId, gen.SecretRequestStateStateSubmitted)
	selectionK5ValidateActualResponse(t, http.MethodPost, first)
	f.takeResume(t)
	beforeNames, err := f.store.List()
	require.NoError(t, err)
	require.Len(t, beforeNames, 1)
	beforeReceipt, exists, err := f.store.CardReceipt(f.rec.Card.RequestId)
	require.NoError(t, err)
	require.True(t, exists)
	require.NotNil(t, state.ReferenceNames)
	require.Equal(t, *state.ReferenceNames, beforeReceipt.References)
	beforeBytes, err := os.ReadFile(filepath.Join(f.api.homePath, "credentials.json"))
	require.NoError(t, err)

	restore := selectionK5AvailabilityCut(t, f, "initialization")
	defer restore()
	// State reads contain no value-bearing request and cannot replay the earlier
	// POST. An explicit same-ID duplicate during the outage is also unavailable;
	// neither outcome establishes that the first publication did not happen.
	for _, method := range []string{http.MethodGet, http.MethodGet, http.MethodPost} {
		body := ""
		if method == http.MethodPost {
			body = f.valuesBody(t, map[string]string{"api_key": "FAKE-MUST-NOT-BECOME-V2"})
		}
		response := selectionK5Call(t, f, authSnapshot, method, body, true)
		selectionK5AssertUnavailableBody(t, response)
		selectionK5ValidateActualResponse(t, method, response)
	}
	afterNames, err := f.store.List()
	require.NoError(t, err)
	assert.Equal(t, beforeNames, afterNames, "availability failure of this attempt cannot add another version")
	afterReceipt, exists, err := f.store.CardReceipt(f.rec.Card.RequestId)
	require.NoError(t, err)
	require.True(t, exists, "unavailable is not authoritative request absence")
	assert.Equal(t, beforeReceipt, afterReceipt)
	afterBytes, err := os.ReadFile(filepath.Join(f.api.homePath, "credentials.json"))
	require.NoError(t, err)
	assert.Equal(t, beforeBytes, afterBytes, "earlier committed values/bindings/receipt remain exactly unchanged")
	stored, err := f.store.Get(beforeReceipt.References[0])
	require.NoError(t, err)
	assert.Equal(t, cardMatrixHTTPValue, stored)
	select {
	case <-f.bus.InboundChan():
		t.Fatal("unavailable reads/duplicate unexpectedly dispatched another continuation")
	default:
	}
}

func selectionK5AvailabilityCut(t *testing.T, f *cardMatrixHTTP, fault string) func() {
	t.Helper()
	switch fault {
	case "missing_registry":
		original := f.api.agentLoop
		f.api.agentLoop = &agent.AgentLoop{} // real nil-session-store condition of SecretRequests(), not a fake registry
		require.Nil(t, f.api.agentLoop.SecretRequests())
		return func() { f.api.agentLoop = original }
	case "initialization":
		path := filepath.Join(f.al.GetSessionStore().BaseDir(), f.rec.Card.SessionId, "secret_requests.json")
		original, err := os.ReadFile(path)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, []byte(`{"unreadable":`), 0o600))
		require.Error(t, f.al.SecretRequests().Recover(), "real recovery must observe the malformed saved-state fault")
		return func() { require.NoError(t, os.WriteFile(path, original, 0o600)) }
	case "reconciliation":
		original := f.al.GetConfig()
		f.al.SwapConfig(nil) // real reconcilePendingLocked unavailable-config branch
		return func() { f.al.SwapConfig(original) }
	default:
		t.Fatalf("unknown availability cut %q", fault)
		return func() {}
	}
}

func selectionK5Call(t *testing.T, f *cardMatrixHTTP, authSnapshot *config.Config, method, body string, authenticated bool) *httptest.ResponseRecorder {
	t.Helper()
	path := "/api/v1/secret-requests/" + f.rec.Card.RequestId
	if method == http.MethodPost {
		path += "/submit"
	} else {
		body = "" // reconciliation GET never receives or replays plaintext
	}
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if authenticated {
		req.Header.Set("Authorization", "Bearer "+cardMatrixHTTPBearer)
	}
	req.RemoteAddr = uniqueTestSourceIP()
	req = req.WithContext(context.WithValue(req.Context(), ctxkey.ConfigContextKey{}, authSnapshot))
	response := httptest.NewRecorder()
	f.mux.ServeHTTP(response, req)
	secretRouteBlocked(t, response, method+" K5", "F03 / binding K5 shape ruling")
	return response
}

func selectionK5AssertAuthentication(t *testing.T, f *cardMatrixHTTP, cfg *config.Config) {
	t.Helper()
	response := selectionK5Call(t, f, cfg, http.MethodPost, f.valuesBody(t, map[string]string{"api_key": cardMatrixHTTPValue}), false)
	require.Equal(t, http.StatusUnauthorized, response.Code, "instrument must traverse real authentication, not a handler called directly")
	f.requireNoCommitOrResume(t)
}

func selectionK5AssertUnavailableBody(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	require.Equal(t, http.StatusServiceUnavailable, response.Code)
	var body gen.ErrorResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.NotEmpty(t, body.Error, "existing ErrorResponse carries the safe availability error")
	var fields map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &fields))
	require.Equal(t, map[string]any{"error": body.Error}, fields, "unavailable returns no invented authoritative state, references or timestamp")
	assert.NotContains(t, response.Body.String(), cardMatrixHTTPValue)
	assert.NotContains(t, response.Body.String(), "FAKE-MUST-NOT-BECOME-V2")
}
