package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #1075: schema validation on and off must both enforce the same name limit.
// The generated wire types supply request/response shapes, not hand-written DTOs.
func TestWU3RESTAgentNameBoundaryMatchesToolContract(t *testing.T) {
	for _, validate := range []bool{false, true} {
		for _, operation := range []string{"create", "update"} {
			for _, name := range []string{strings.Repeat("a", 99), strings.Repeat("a", 100), strings.Repeat("a", 101), strings.Repeat("界", 100), strings.Repeat("界", 101)} {
				t.Run(fmt.Sprintf("%s_runes%d_bytes%d_validation%t", operation, len([]rune(name)), len(name), validate), func(t *testing.T) {
					api := wu3WiredAPI(t, nil)
					api.agentLoop.GetConfig().Gateway.ValidateInbound = validate
					path := "/api/v1/agents"
					method := http.MethodPost
					var payload any = gen.AgentCreateRequestMain{Name: name, Soul: "A valid bounded soul.", Type: gen.AgentCreateRequestMainType("Main")}
					var before *agentstore.State
					if operation == "update" {
						store := agentstore.New(api.homePath)
						// Existing fixture is custom/unlocked; select by identity, not list order.
						var err error
						before, err = store.ReadState("coverage-main")
						require.NoError(t, err)
						payload = gen.AgentUpdateRequest{Name: &name, Revision: before.Revision}
						path += "/coverage-main"
						method = http.MethodPut
					}
					data, err := json.Marshal(payload)
					require.NoError(t, err)
					w := httptest.NewRecorder()
					api.HandleAgents(w, httptest.NewRequest(method, path, bytes.NewReader(data)))
					oversized := len([]rune(name)) > 100
					if oversized {
						assert.Equal(t, http.StatusBadRequest, w.Code, "over-100 name must never land: %s", w.Body.String())
						assert.Contains(t, w.Body.String(), "100", "clear refusal states the limit")
						if before != nil {
							after, err := agentstore.New(api.homePath).ReadState(before.Agent.ID)
							require.NoError(t, err)
							assert.Equal(t, before.Revision, after.Revision)
							assert.Equal(t, before.Agent.Name, after.Agent.Name)
						}
					} else {
						want := http.StatusCreated
						if operation == "update" {
							want = http.StatusOK
						}
						require.Equal(t, want, w.Code, "valid boundary must not be refused: %s", w.Body.String())
						var response gen.Agent
						require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
						assert.Equal(t, name, response.Name)
					}
				})
			}
		}
	}
}
