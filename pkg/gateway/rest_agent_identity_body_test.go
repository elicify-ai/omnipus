package gateway

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// F-BODY: a complete valid body at the 1 MiB cap is a passing control. Reading
// only its prefix must not turn an oversized or truncated body into a write.
func TestAgentIdentity_UpdateBodyFailsClosed(t *testing.T) {
	const limit = 1 << 20 // withAuth's published 1 MiB request-body ceiling.
	cases := []struct {
		name       string
		body       func(string) string
		wantStatus int
	}{
		{"below limit", func(base string) string { return base + strings.Repeat(" ", limit-1-len(base)) }, http.StatusOK},
		{"at limit", func(base string) string { return base + strings.Repeat(" ", limit-len(base)) }, http.StatusOK},
		{"above limit", func(base string) string { return base + strings.Repeat(" ", limit+1-len(base)) }, http.StatusRequestEntityTooLarge},
		{"malformed tail beyond limit", func(base string) string { return base + strings.Repeat(" ", limit-len(base)) + "[" }, http.StatusRequestEntityTooLarge},
		{"incomplete JSON", func(base string) string { return strings.TrimSuffix(base, "}") }, http.StatusBadRequest},
		{"second document", func(base string) string { return base + "{}" }, http.StatusBadRequest},
	}
	for _, strict := range []bool{false, true} {
		for _, wrapped := range []bool{false, true} {
			for _, tc := range cases {
				t.Run(fmt.Sprintf("strict=%t/auth=%t/%s", strict, wrapped, tc.name), func(t *testing.T) {
					api := buildExecutorTestAPI(t)
					api.agentLoop.GetConfig().Gateway.ValidateInbound = strict
					t.Setenv("OMNIPUS_BEARER_TOKEN", "body-limit-test-token")
					before := savedAgent(t, api, "test-agent")
					store := agentstore.New(api.homePath)
					stateBefore, err := store.ReadState("test-agent")
					require.NoError(t, err)
					base := fmt.Sprintf(`{"revision":%q,"color":"#22D3EE","description":"must not partially save"}`, stateBefore.Revision)
					body := tc.body(base)
					req := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent", strings.NewReader(body))
					// Unknown length also exercises streamed bodies, not just a
					// shortcut based on the Content-Length header.
					req.ContentLength = -1
					req.Header.Set("Authorization", "Bearer body-limit-test-token")
					req.Header.Set("Content-Type", "application/json")
					rec := httptest.NewRecorder()
					handler := http.HandlerFunc(api.HandleAgents)
					if wrapped {
						handler = api.withAuth(handler)
					}
					handler(rec, req)
					stateAfter, err := store.ReadState("test-agent")
					require.NoError(t, err)
					t.Logf("BODY_CASE=%s strict=%t auth=%t bytes=%d status=%d revision_before=%s revision_after=%s", tc.name, strict, wrapped, len(body), rec.Code, stateBefore.Revision, stateAfter.Revision)
					assert.Equal(t, tc.wantStatus, rec.Code, "body: %s", rec.Body.String())
					if tc.wantStatus == http.StatusOK {
						assert.Equal(t, "#22D3EE", savedAgent(t, api, "test-agent")["color"], "complete in-limit control must actually write")
						assert.NotEqual(t, stateBefore.Revision, stateAfter.Revision)
						return
					}
					assert.Equal(t, before, savedAgent(t, api, "test-agent"), "rejected body must preserve the complete stored record")
					assert.Equal(t, stateBefore, stateAfter, "rejected body must preserve state and revision")
					if tc.wantStatus == http.StatusRequestEntityTooLarge {
						assert.Equal(t, map[string]any{"error": "request body too large"}, decodeObject(t, rec.Body.Bytes()))
					}
				})
			}
		}
	}
}

type incompleteAgentBody struct {
	io.Reader
}

func (body incompleteAgentBody) Read(p []byte) (int, error) {
	n, err := body.Reader.Read(p)
	if err == io.EOF {
		return n, io.ErrUnexpectedEOF
	}
	return n, err
}

func TestAgentIdentity_UpdateTransportTruncationIsZeroWrite(t *testing.T) {
	api := buildExecutorTestAPI(t)
	before := savedAgent(t, api, "test-agent")
	store := agentstore.New(api.homePath)
	stateBefore, err := store.ReadState("test-agent")
	require.NoError(t, err)
	body := fmt.Sprintf(`{"revision":%q,"color":"#22D3EE"}`, stateBefore.Revision)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent", nil)
	req.Body = io.NopCloser(incompleteAgentBody{Reader: strings.NewReader(body)})
	rec := httptest.NewRecorder()
	api.HandleAgents(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, map[string]any{"error": "could not read request body"}, decodeObject(t, rec.Body.Bytes()))
	assert.Equal(t, before, savedAgent(t, api, "test-agent"))
	stateAfter, err := store.ReadState("test-agent")
	require.NoError(t, err)
	assert.Equal(t, stateBefore, stateAfter, "a complete-looking prefix plus transport error is not a complete body")
}
