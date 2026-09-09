// rest_agent_reserved_default_name_test.go — ADR-071 §5.1.3 part 2: agent
// create/update rejects a name of "default" (case-insensitive) with 400. This
// is the forward half of the "default" collision fix: switch_agent's
// target:"default" sentinel always resolves to the CONFIGURED default agent,
// never to an agent whose id or name happens to be that literal string.
// Rejecting the name at the create/update boundary makes the collision
// impossible going forward, complementing the boot-time WARN
// (pkg/agent/registry_reserved_default_id_test.go) that covers an agent that
// predates this check.

package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCreateAgent_RejectsReservedDefaultName verifies createAgent 400s on a
// name of "default", case-insensitively.
func TestCreateAgent_RejectsReservedDefaultName(t *testing.T) {
	for _, name := range []string{"default", "Default", "DEFAULT", "  default  "} {
		t.Run("name="+name, func(t *testing.T) {
			api := buildExecutorTestAPI(t)

			body := `{"name":"` + name + `","type":"Main","soul":"s"}`
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			api.HandleAgents(w, r)

			require.Equal(t, http.StatusBadRequest, w.Code,
				"create with reserved name %q must be rejected 400, got body: %s", name, w.Body.String())
			assert.Contains(t, w.Body.String(), "reserved",
				"error body must explain the name is reserved")
		})
	}
}

// TestCreateAgent_AllowsNonReservedNameContainingDefault verifies the check
// is an exact match (after trim/case-fold), not a substring match — a name
// like "Default Assistant" must NOT be rejected.
func TestCreateAgent_AllowsNonReservedNameContainingDefault(t *testing.T) {
	api := buildExecutorTestAPI(t)

	body := `{"name":"Default Assistant","type":"Main","soul":"s"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusCreated, w.Code,
		"a name merely containing \"default\" as a substring must be allowed, got body: %s", w.Body.String())
}

// TestUpdateAgent_RejectsReservedDefaultName verifies updateAgent 400s on a
// name of "default", case-insensitively, for an existing agent.
func TestUpdateAgent_RejectsReservedDefaultName(t *testing.T) {
	for _, name := range []string{"default", "Default", "DEFAULT"} {
		t.Run("name="+name, func(t *testing.T) {
			api := buildExecutorTestAPI(t)

			body := `{"name":"` + name + `"}`
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			api.updateAgent(w, r, "test-agent")

			require.Equal(t, http.StatusBadRequest, w.Code,
				"update to reserved name %q must be rejected 400, got body: %s", name, w.Body.String())
			assert.Contains(t, w.Body.String(), "reserved",
				"error body must explain the name is reserved")
		})
	}
}

// TestUpdateAgent_AllowsOrdinaryNameChange is the negative control — an
// ordinary name update must still succeed, proving the reserved-name check
// does not over-fire.
func TestUpdateAgent_AllowsOrdinaryNameChange(t *testing.T) {
	api := buildExecutorTestAPI(t)

	body := `{"name":"Renamed Agent"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.updateAgent(w, r, "test-agent")

	require.Equal(t, http.StatusOK, w.Code, "ordinary rename must succeed, got body: %s", w.Body.String())
	updated := decodeAgentResp(t, w.Body.Bytes())
	assert.Equal(t, "Renamed Agent", updated.Name)
}
