// rest_agents_max_tool_iterations_field_test.go — #904 gate round 2, item 4:
// every per-agent tool-iteration refusal (bound 1..1000 and D10 "above the
// global") on REST create and update carries ErrorResponse.field
// "max_tool_iterations" (contracts/components/schemas/ErrorResponse.yaml),
// so the SPA attributes the error without matching message text. Both the
// fast-path check and the deciding check under configMu are covered.

package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

func mtiErrorField(t *testing.T, w *httptest.ResponseRecorder) any {
	t.Helper()
	return mtiDecode(t, w.Body.Bytes())["field"]
}

func TestAgentMaxToolIterationsRefusals_CarryField(t *testing.T) {
	t.Run("update above global", func(t *testing.T) {
		api := newMTIAPI(t, "200", mtiAgent{id: "agent-a"})
		w := putAgentJSON(t, api, "agent-a", `{"max_tool_iterations":300}`)
		require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
		assert.Equal(t, "max_tool_iterations", mtiErrorField(t, w))
	})
	t.Run("update out of bounds", func(t *testing.T) {
		api := newMTIAPI(t, "1000", mtiAgent{id: "agent-a"})
		w := putAgentJSON(t, api, "agent-a", `{"max_tool_iterations":1001}`)
		require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
		assert.Equal(t, "max_tool_iterations", mtiErrorField(t, w))
	})
	t.Run("create above global", func(t *testing.T) {
		api := newMTIAPI(t, "200")
		w := mtiPostAgent(t, api, `{"name":"TooHigh","type":"Main","soul":"s","max_tool_iterations":500}`)
		require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
		assert.Equal(t, "max_tool_iterations", mtiErrorField(t, w))
	})
	t.Run("create out of bounds", func(t *testing.T) {
		api := newMTIAPI(t, "1000")
		w := mtiPostAgent(t, api, `{"name":"Bad","type":"Main","soul":"s","max_tool_iterations":0}`)
		require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
		assert.Equal(t, "max_tool_iterations", mtiErrorField(t, w))
	})
}

// The deciding check under configMu (update): the fast path passed against
// an older global; persistAgent re-checks against the global in force and
// its refusal must carry the field through withToolPolicyCoverageGuard.
func TestAgentMaxToolIterations_DecidingRefusalCarriesField(t *testing.T) {
	api := newMTIAPI(t, "200", mtiAgent{id: "agent-a"})
	v := 300
	ru := &restAPIUpdateAgent{a: api, id: "agent-a", req: gen.AgentUpdateRequest{MaxToolIterations: &v}}
	w := httptest.NewRecorder()
	ok := api.withToolPolicyCoverageGuard(w, nil, nil, ru.persistAgent, "test persist")
	require.False(t, ok)
	require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Equal(t, mtiAboveGlobalMsg(300, 200), mtiErrorText(t, w))
	assert.Equal(t, "max_tool_iterations", mtiErrorField(t, w))
}
