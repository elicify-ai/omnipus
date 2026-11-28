package gateway

// Oracle: cli-credentials-s1-s2-spec.md revision 8, S-45 / FR-009.
// These rows own their channel/team and do not depend on a GitHub connection.

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

func pgChannelSetting(t *testing.T, f *pgGateFixture) map[string]any {
	t.Helper()
	w := pgProgramsSessionCall(t, f, http.MethodGet, "/api/v1/channels/telegram/programs", "")
	if w.Code == http.StatusNotFound {
		routeMissing(t, w, "GET /channels/telegram/programs", "S-45 / FR-009")
	}
	require.Equal(t, http.StatusOK, w.Code, "%s", w.Body.String())
	var setting map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &setting))
	require.IsType(t, false, setting["allowed"], "ChannelProgramsSetting.allowed is boolean")
	require.Contains(t, setting, "revision", "use the actual revision, never invented revision 1")
	return setting
}

func pgEnableChannelPrograms(t *testing.T, f *pgGateFixture) {
	t.Helper()
	before := pgChannelSetting(t, f)
	body, err := json.Marshal(map[string]any{"allowed": true, "revision": before["revision"]})
	require.NoError(t, err)
	w := pgProgramsSessionCall(t, f, http.MethodPut, "/api/v1/channels/telegram/programs", string(body))
	require.Equal(t, http.StatusOK, w.Code, "switch-on accepted: %s", w.Body.String())
	require.Equal(t, true, pgChannelSetting(t, f)["allowed"], "positive: real setting is ON")
}

func pgBoundChannel(t *testing.T) *pgGateFixture {
	t.Helper()
	f := pgPersonAPI(t)
	addAgentsToAPI(t, f.api, []config.AgentConfig{{ID: "ray", Name: "Ray", Type: config.AgentTypeCustom}})
	writeTestWorkspaceJSON(t, f.api, "s45-workspace", "active", []string{"dev", "ray"}, false)
	seedChannelInstance(t, f.api, "telegram")
	w := pgProgramsSessionCall(t, f, http.MethodPut, "/api/v1/channels/telegram/routing",
		`{"workspace_id":"s45-workspace","default_agent_id":"dev"}`)
	require.Equal(t, http.StatusOK, w.Code, "initial real binding: %s", w.Body.String())
	g := pgProgramsSessionCall(t, f, http.MethodGet, "/api/v1/channels/telegram/routing", "")
	require.Equal(t, http.StatusOK, g.Code)
	var routing gen.ChannelRouting
	require.NoError(t, json.Unmarshal(g.Body.Bytes(), &routing))
	require.Equal(t, gen.ChannelRouting{Bound: true, WorkspaceId: "s45-workspace", DefaultAgentId: "dev"}, routing)
	return f
}

func TestProgramsChannel_OneWriter(t *testing.T) {
	t.Run("S45_channel_programs_setting_rest_face", func(t *testing.T) {
		f := pgBoundChannel(t)
		require.Equal(t, false, pgChannelSetting(t, f)["allowed"], "default off (FR-009)")
		pgEnableChannelPrograms(t, f)
	})
	t.Run("S45_channel_rebind_resets_the_switch", func(t *testing.T) {
		f := pgBoundChannel(t)
		pgEnableChannelPrograms(t, f)
		w := pgProgramsSessionCall(t, f, http.MethodPut, "/api/v1/channels/telegram/routing",
			`{"workspace_id":"s45-workspace","default_agent_id":"ray"}`)
		require.Equal(t, http.StatusOK, w.Code, "different main agent: %s", w.Body.String())
		require.Equal(t, false, pgChannelSetting(t, f)["allowed"], "same rebind request clears ON")
		// Persisted pair + switch are read immediately after that one request.
		raw, err := os.ReadFile(f.api.configPath())
		require.NoError(t, err)
		var cfg map[string]any
		require.NoError(t, json.Unmarshal(raw, &cfg))
		channels, channelsOK := cfg["channels"].(map[string]any)
		require.True(t, channelsOK, "instrument: persisted channels is a JSON object")
		channel, channelOK := channels["telegram"].(map[string]any)
		require.True(t, channelOK, "instrument: persisted Telegram channel is a JSON object")
		allowed, exists := channel["programs_allowed"]
		if exists {
			require.Equal(t, false, allowed)
		} // absent is explicitly false (FR-009)
		routing := channelRoutingRead(f.api.agentLoop.GetConfig(), "telegram")
		require.Equal(t, gen.ChannelRouting{Bound: true, WorkspaceId: "s45-workspace", DefaultAgentId: "ray"}, routing)
	})
}
