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
	if json.Unmarshal(raw, &payload) != nil {
		return httptest.NewRequest(http.MethodPut, target, bytes.NewReader(raw))
	}
	if revision, supplied := payload["revision"].(string); !supplied || revision == "" {
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
