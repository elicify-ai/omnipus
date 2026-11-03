package gateway

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// R-S4 / security S4 / FR-037: both create request policy forms must use the
// same always-ask guard as update. The registered route and its auth/validation
// middleware remain real, as do entity persistence and live publication.
func TestQ7FollowupS4_RegisteredCreateRefusesAllowButAcceptsAskAndDeny(t *testing.T) {
	for _, form := range []string{"sparse", "complete"} {
		for _, policy := range []string{"allow", "ask", "deny"} {
			t.Run(form+"_"+policy, func(t *testing.T) {
				f := q7PolicyFixture(t)
				// Production gateway_boot registers the exact collection route in
				// addition to rest.go's slash subtree; retain both, without a redirect.
				f.mux.Handle("/api/v1/agents", f.api.withAuth(f.api.HandleAgents))
				store := agentstore.New(f.f1277.home)
				before, skipped, err := store.List()
				require.NoError(t, err)
				require.Empty(t, skipped, "instrument: agent inventory sees every existing entity")
				beforeConfig := q7ConfigBytes(t, f.wu6Fixture)
				beforeLive := append([]config.AgentConfig{}, f.al.GetConfig().Agents.List...)
				body := map[string]any{"type": "Main", "name": "Q7 create policy probe", "soul": "A test colleague."}
				if form == "sparse" {
					body["tool_policy_changes"] = map[string]any{"set": map[string]string{"set_channel_outbound": policy}}
				} else {
					full := fullBuiltinPolicyMap("deny") // Complete input scaffolding, not the expected verdict.
					full["set_channel_outbound"] = policy
					body["tools_cfg"] = map[string]any{"builtin": map[string]any{"policies": full}}
				}
				raw, err := json.Marshal(body)
				require.NoError(t, err)
				rec := t14Request(t, f.wu6Fixture, http.MethodPost, "/api/v1/agents", string(raw), true)
				if policy == "allow" {
					assert.Equal(t, http.StatusBadRequest, rec.Code, "S4: create must refuse the same unstorable Allow as update")
					assert.JSONEq(t, `{"error":"`+q7AlwaysAskText+`"}`, rec.Body.String(), "S4: exact FR-037 refusal, not unknown tool or invalid shape")
					after, skipped, err := store.List()
					require.NoError(t, err)
					require.Empty(t, skipped)
					assert.Equal(t, before, after, "S4: refused creation persists no agent")
					assert.Equal(t, beforeConfig, q7ConfigBytes(t, f.wu6Fixture), "S4: refused creation writes no config")
					assert.Equal(t, beforeLive, f.al.GetConfig().Agents.List, "S4: refused creation publishes no live agent")
					return
				}
				require.Equal(t, http.StatusCreated, rec.Code, "S4: valid %s create remains accepted: %s", policy, rec.Body.String())
				created := decodeAgentResp(t, rec.Body.Bytes())
				require.NotEmpty(t, created.Id)
				state, err := store.ReadState(created.Id)
				require.NoError(t, err)
				require.NotNil(t, state.Agent.Tools)
				require.Equal(t, config.ToolPolicy(policy), state.Agent.Tools.Builtin.Policies["set_channel_outbound"], "S4: accepted create stores the exact requested Ask/Deny")
			})
		}
	}
}
