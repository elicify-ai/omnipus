package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/stretchr/testify/require"
)

// revisionedAgentMutationRequest makes an otherwise-valid fixture satisfy
// FR-007 so the test reaches the behavior it was written to exercise.
// Malformed JSON remains untouched for request-decoding negative tests.
func revisionedAgentMutationRequest(t *testing.T, api *restAPI, target string, body io.Reader) *http.Request {
	t.Helper()
	raw, err := io.ReadAll(body)
	require.NoError(t, err)

	var payload map[string]any
	if json.Unmarshal(raw, &payload) != nil || payload == nil {
		return httptest.NewRequest(http.MethodPut, target, bytes.NewReader(raw))
	}
	if _, supplied := payload["revision"]; !supplied {
		id := strings.TrimPrefix(target, "/api/v1/agents/")
		id = strings.TrimSuffix(id, "/tools")
		state, readErr := agentstore.New(api.homePath).ReadState(id)
		require.NoError(t, readErr, "revision fixture target %q must be persisted", id)
		payload["revision"] = state.Revision
		raw, err = json.Marshal(payload)
		require.NoError(t, err)
	}
	return httptest.NewRequest(http.MethodPut, target, bytes.NewReader(raw))
}

func TestRevisionedAgentMutationRequestPreservesExplicitInvalidRevisionValues(t *testing.T) {
	api := &restAPI{homePath: t.TempDir()}
	for _, body := range []string{
		`{"revision":"","name":"empty"}`,
		`{"revision":null,"name":"null"}`,
		`{"revision":42,"name":"wrong type"}`,
		`null`,
	} {
		req := revisionedAgentMutationRequest(t, api, "/api/v1/agents/not-persisted", strings.NewReader(body))
		got, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		require.JSONEq(t, body, string(got))
	}
}
