package gateway

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SF-5: the published 1 MiB ceiling applies to the whole create body, not
// just a valid prefix. Every rejection must leave both rosters unchanged.
func TestCreateAgent_BodyFailsClosed(t *testing.T) {
	const limit = 1 << 20
	cases := []struct {
		name       string
		body       func(string) string
		transport  bool
		wantStatus int
		wantError  string
	}{
		{"below limit", func(s string) string { return s + strings.Repeat(" ", limit-1-len(s)) }, false, http.StatusCreated, ""},
		{"at limit", func(s string) string { return s + strings.Repeat(" ", limit-len(s)) }, false, http.StatusCreated, ""},
		{"above limit", func(s string) string { return s + strings.Repeat(" ", limit+1-len(s)) }, false, http.StatusRequestEntityTooLarge, "request body too large"},
		{"malformed tail beyond limit", func(s string) string { return s + strings.Repeat(" ", limit-len(s)) + "[" }, false, http.StatusRequestEntityTooLarge, "request body too large"},
		{"incomplete JSON", func(s string) string { return strings.TrimSuffix(s, "}") }, false, http.StatusBadRequest, "invalid JSON body"},
		{"second document", func(s string) string { return s + "{}" }, false, http.StatusBadRequest, "invalid JSON body"},
		{"malformed tail below limit", func(s string) string { return s + "[" }, false, http.StatusBadRequest, "invalid JSON body"},
		{"transport truncation", func(s string) string { return s }, true, http.StatusBadRequest, "could not read request body"},
		{"transport truncation at limit", func(s string) string { return s + strings.Repeat(" ", limit-len(s)) }, true, http.StatusBadRequest, "could not read request body"},
	}
	for _, strict := range []bool{false, true} {
		for _, wrapped := range []bool{false, true} {
			for _, tc := range cases {
				t.Run(fmt.Sprintf("strict=%t/auth=%t/%s", strict, wrapped, tc.name), func(t *testing.T) {
					api := buildExecutorTestAPI(t)
					api.agentLoop.GetConfig().Gateway.ValidateInbound = strict
					before := createBoundaryRoster(t, api)
					liveBefore := append([]config.AgentConfig(nil), api.agentLoop.GetConfig().Agents.List...)
					body := tc.body(`{"name":"Create boundary","type":"Main","soul":"must not partially create"}`)
					req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
					if tc.transport {
						req.Body = io.NopCloser(incompleteAgentBody{Reader: strings.NewReader(body)})
					}
					// Streaming requests must not depend on a Content-Length shortcut.
					req.ContentLength = -1
					req.Header.Set("Content-Type", "application/json")
					t.Setenv("OMNIPUS_BEARER_TOKEN", "create-body-test-token")
					req.Header.Set("Authorization", "Bearer create-body-test-token")
					rec := httptest.NewRecorder()
					handler := http.HandlerFunc(api.HandleAgents)
					if wrapped {
						handler = api.withAuth(handler)
					}
					handler(rec, req)
					t.Logf("CREATE_BODY_CASE=%s strict=%t auth=%t bytes=%d status=%d", tc.name, strict, wrapped, len(body), rec.Code)
					assert.Equal(t, tc.wantStatus, rec.Code, "body: %s", rec.Body.String())
					if tc.wantStatus == http.StatusCreated {
						created := decodeAgentResp(t, rec.Body.Bytes())
						require.Len(t, createBoundaryRoster(t, api), len(before)+1)
						state, err := agentstore.New(api.homePath).ReadState(created.Id)
						require.NoError(t, err)
						assert.Equal(t, "Create boundary", state.Agent.Name)
						assert.Equal(t, "must not partially create", state.Soul)
						return
					}
					assert.Equal(t, map[string]any{"error": tc.wantError}, decodeObject(t, rec.Body.Bytes()))
					assert.Equal(t, before, createBoundaryRoster(t, api), "rejected body must create no durable agent")
					assert.Equal(t, liveBefore, api.agentLoop.GetConfig().Agents.List, "rejected body must create no live agent")
				})
			}
		}
	}
}

func createBoundaryRoster(t *testing.T, api *restAPI) []config.AgentConfig {
	t.Helper()
	agents, skipped, err := agentstore.New(api.homePath).List()
	require.NoError(t, err)
	require.Empty(t, skipped, "a skipped corrupt record is not evidence of zero writes")
	return agents
}

// SF-6: null rejection must observe every decoder-recognised occurrence,
// including repeated parents; neither casing nor a later value can hide null.
func TestCreateAgent_RejectsFoldedNullMembers(t *testing.T) {
	cases := []struct{ name, fields, wantError string }{
		{"uppercase skills", `"SKILLS":null`, "skills must not be null"},
		{"mixed skills", `"sKiLlS":null`, "skills must not be null"},
		{"escaped skills", fmt.Sprintf(`"%cu0053KILLS":null`, 0x5c), "skills must not be null"},
		{"Unicode folded skills", `"ſKILLS":null`, "skills must not be null"},
		{"same skills duplicate null first", `"skills":null,"skills":[]`, "skills must not be null"},
		{"folded skills duplicate null first", `"SKILLS":null,"skills":[]`, "skills must not be null"},
		{"folded skills duplicate null last", `"skills":[],"SKILLS":null`, "skills must not be null"},
		{"uppercase servers", `"MCP_SERVERS":null`, "mcp_servers must not be null"},
		{"mixed servers", `"McP_sErVeRs":null`, "mcp_servers must not be null"},
		{"escaped servers", fmt.Sprintf(`"%cu004dCP_SERVERS":null`, 0x5c), "mcp_servers must not be null"},
		{"same servers duplicate null first", `"mcp_servers":null,"mcp_servers":[]`, "mcp_servers must not be null"},
		{"folded servers duplicate null first", `"MCP_SERVERS":null,"mcp_servers":[]`, "mcp_servers must not be null"},
		{"folded servers duplicate null last", `"mcp_servers":[],"MCP_SERVERS":null`, "mcp_servers must not be null"},
		{"uppercase policy changes", `"TOOL_POLICY_CHANGES":null`, "tool_policy_changes must not be null"},
		{"mixed policy changes", `"Tool_Policy_Changes":null`, "tool_policy_changes must not be null"},
		{"escaped policy changes", fmt.Sprintf(`"%cu0054OOL_POLICY_CHANGES":null`, 0x5c), "tool_policy_changes must not be null"},
		{"same changes duplicate null first", `"tool_policy_changes":null,"tool_policy_changes":{}`, "tool_policy_changes must not be null"},
		{"folded changes duplicate null first", `"TOOL_POLICY_CHANGES":null,"tool_policy_changes":{}`, "tool_policy_changes must not be null"},
		{"folded changes duplicate null last", `"tool_policy_changes":{},"TOOL_POLICY_CHANGES":null`, "tool_policy_changes must not be null"},
		{"uppercase nested set", `"tool_policy_changes":{"SET":null}`, "tool_policy_changes.set must not be null"},
		{"uppercase nested remove", `"tool_policy_changes":{"REMOVE":null}`, "tool_policy_changes.remove must not be null"},
		{"folded changes parent set", `"TOOL_POLICY_CHANGES":{"set":null}`, "tool_policy_changes.set must not be null"},
		{"folded changes parent remove", `"TOOL_POLICY_CHANGES":{"remove":null}`, "tool_policy_changes.remove must not be null"},
		{"escaped nested set", fmt.Sprintf(`"tool_policy_changes":{"%cu0053ET":null}`, 0x5c), "tool_policy_changes.set must not be null"},
		{"escaped nested remove", fmt.Sprintf(`"tool_policy_changes":{"%cu0052EMOVE":null}`, 0x5c), "tool_policy_changes.remove must not be null"},
		{"same set duplicate null first", `"tool_policy_changes":{"set":null,"set":{}}`, "tool_policy_changes.set must not be null"},
		{"folded set duplicate null first", `"tool_policy_changes":{"SET":null,"set":{}}`, "tool_policy_changes.set must not be null"},
		{"folded remove duplicate null first", `"tool_policy_changes":{"REMOVE":null,"remove":[]}`, "tool_policy_changes.remove must not be null"},
		{"folded set duplicate null last", `"tool_policy_changes":{"set":{},"SET":null}`, "tool_policy_changes.set must not be null"},
		{"folded remove duplicate null last", `"tool_policy_changes":{"remove":[],"REMOVE":null}`, "tool_policy_changes.remove must not be null"},
		{"same changes parent overwrites set null", `"tool_policy_changes":{"set":null},"tool_policy_changes":{}`, "tool_policy_changes.set must not be null"},
		{"folded changes parent overwrites remove null", `"TOOL_POLICY_CHANGES":{"remove":null},"tool_policy_changes":{}`, "tool_policy_changes.remove must not be null"},
		{"uppercase nested tools", `"mcp_servers":[{"id":"docs","TOOLS":null}]`, "mcp_servers[].tools must not be null"},
		{"folded servers parent tools", `"MCP_SERVERS":[{"id":"docs","tools":null}]`, "mcp_servers[].tools must not be null"},
		{"escaped nested tools", fmt.Sprintf(`"mcp_servers":[{"id":"docs","%cu0054OOLS":null}]`, 0x5c), "mcp_servers[].tools must not be null"},
		{"same tools duplicate null first", `"mcp_servers":[{"id":"docs","tools":null,"tools":[]}]`, "mcp_servers[].tools must not be null"},
		{"folded tools duplicate null first", `"mcp_servers":[{"id":"docs","TOOLS":null,"tools":[]}]`, "mcp_servers[].tools must not be null"},
		{"folded tools duplicate null last", `"mcp_servers":[{"id":"docs","tools":[],"TOOLS":null}]`, "mcp_servers[].tools must not be null"},
		{"same servers parent overwrites tools null", `"mcp_servers":[{"id":"docs","tools":null}],"mcp_servers":[]`, "mcp_servers[].tools must not be null"},
		{"folded servers parent overwrites tools null", `"MCP_SERVERS":[{"id":"docs","tools":null}],"mcp_servers":[]`, "mcp_servers[].tools must not be null"},
		{"second server tools null", `"mcp_servers":[{"id":"docs","tools":[]},{"id":"docs","TOOLS":null}]`, "mcp_servers[].tools must not be null"},
	}
	for _, strict := range []bool{false, true} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("strict=%t/%s", strict, tc.name), func(t *testing.T) {
				api := buildExecutorTestAPI(t)
				api.agentLoop.GetConfig().Gateway.ValidateInbound = strict
				api.agentLoop.GetConfig().Tools.MCP.Servers = map[string]config.MCPServerConfig{"docs": {Enabled: true, Command: "test"}}
				before := createBoundaryRoster(t, api)
				liveBefore := append([]config.AgentConfig(nil), api.agentLoop.GetConfig().Agents.List...)
				body := `{"name":"Must not create","type":"Main","soul":"persona",` + tc.fields + `}`
				rec := postAgent(t, api, body)
				t.Logf("CREATE_NULL_CASE=%s strict=%t status=%d", tc.name, strict, rec.Code)
				assert.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
				assert.Equal(t, map[string]any{"error": tc.wantError}, decodeObject(t, rec.Body.Bytes()))
				assert.Equal(t, before, createBoundaryRoster(t, api), "null must reject the whole create, even with valid siblings")
				assert.Equal(t, liveBefore, api.agentLoop.GetConfig().Agents.List, "no live agent may be published")
			})
		}
	}
}

func TestCreateAgent_NonNullMembersAndIdentityDefaultsRemainAccepted(t *testing.T) {
	for _, strict := range []bool{false, true} {
		for _, identity := range []string{`"figure":null,"role":null,"color":null`, `"figure":"","role":"","color":""`} {
			t.Run(fmt.Sprintf("strict=%t/%s", strict, identity), func(t *testing.T) {
				api := buildExecutorTestAPI(t)
				api.agentLoop.GetConfig().Gateway.ValidateInbound = strict
				body := `{"name":"Default identity","type":"Main","soul":"persona","skills":[],"mcp_servers":[],"tool_policy_changes":{"set":{},"remove":[]},` + identity + `}`
				rec := postAgent(t, api, body)
				require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
				created := decodeAgentResp(t, rec.Body.Bytes())
				assert.Equal(t, "Omnipus", string(created.Figure))
				assert.Equal(t, "general", string(created.Role))
				require.NotNil(t, created.Color)
				assert.Equal(t, "#9CA3AF", string(*created.Color))
				state, err := agentstore.New(api.homePath).ReadState(created.Id)
				require.NoError(t, err)
				assert.Equal(t, "Omnipus", state.Agent.Figure)
				assert.Equal(t, "general", state.Agent.Role)
				assert.Equal(t, "#9CA3AF", state.Agent.Color)
			})
		}
	}
	t.Run("folded non-null control on non-strict decoder", func(t *testing.T) {
		api := buildExecutorTestAPI(t)
		api.agentLoop.GetConfig().Tools.MCP.Servers = map[string]config.MCPServerConfig{"docs": {Enabled: true, Command: "test"}}
		body := `{"name":"Folded members","type":"Main","soul":"persona","SKILLS":[],"MCP_SERVERS":[{"id":"docs","TOOLS":[]}],"TOOL_POLICY_CHANGES":{"SET":{"bash":"deny"},"REMOVE":[]}}`
		rec := postAgent(t, api, body)
		require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
		created := decodeAgentResp(t, rec.Body.Bytes())
		state, err := agentstore.New(api.homePath).ReadState(created.Id)
		require.NoError(t, err)
		assert.Equal(t, config.ToolPolicyDeny, state.Agent.Tools.Builtin.Policies["bash"])
		require.Len(t, state.Agent.Tools.MCP.Servers, 1)
		assert.Equal(t, "docs", state.Agent.Tools.MCP.Servers[0].ID)
		assert.True(t, state.Agent.Tools.MCP.Servers[0].ToolsSpecified)
		assert.Empty(t, state.Agent.Tools.MCP.Servers[0].Tools)
	})
}
