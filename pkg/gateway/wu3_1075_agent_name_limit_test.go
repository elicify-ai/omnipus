// wu3_1075_agent_name_limit_test.go: #1075 — the REST create path refuses an
// agent name over the contract's 100 characters, with inbound validation on
// or off, and stores nothing; exactly 100 is accepted.

package gateway

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
)

func TestWU3_1075_RESTCreateRefusesAgentNameOver100(t *testing.T) {
	for _, validate := range []bool{false, true} {
		api := newMTIAPI(t, "200")
		cfg := api.agentLoop.GetConfig()
		cfg.Gateway.ValidateInbound = validate

		long := strings.Repeat("n", 101)
		w := mtiPostAgent(t, api, `{"name":"`+long+`","type":"Main","description":"d","soul":"s"}`)
		require.Equal(t, http.StatusBadRequest, w.Code, "validate=%v body: %s", validate, w.Body.String())
		agents, _, err := agentstore.New(api.homePath).List()
		require.NoError(t, err)
		for _, a := range agents {
			assert.NotEqual(t, long, a.Name, "a refused create must leave no agent record")
		}

		// Control: exactly 100 characters (multi-byte included) is accepted.
		ok := strings.Repeat("é", 100)
		w = mtiPostAgent(t, api, `{"name":"`+ok+`","type":"Main","description":"d","soul":"s"}`)
		require.Equal(t, http.StatusCreated, w.Code, "validate=%v body: %s", validate, w.Body.String())
	}
}
