package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestL1WU1ChannelBindingSave_SiblingConfigReadbackAndInboundStayIsolated(t *testing.T) {
	// F06 / M30; K14 / FR-127/128: retire wildcard/unbind inputs, not
	// per-instance independence. Both siblings have real persisted bindings.
	api := buildExecutorTestAPI(t)
	addAgentsToAPI(t, api, []config.AgentConfig{{ID: "jim", Name: "Jim", Type: config.AgentTypeCore, Model: &config.AgentModelConfig{Primary: "test-model"}}})
	// Entity reload populates the handler roster; explicitly activate this
	// fixture through the real fast-upsert seam before exercising a turn.
	_, err := api.agentLoop.UpsertAgentFast(api.agentLoop.GetConfig(), "jim")
	require.NoError(t, err)
	writeTestWorkspaceJSON(t, api, "sales", "active", []string{"test-agent"}, false)
	writeTestWorkspaceJSON(t, api, "support", "active", []string{"test-agent", "jim"}, false)
	seedChannelInstance(t, api, "telegram.eu")
	seedChannelInstance(t, api, "telegram.us")
	for _, binding := range []struct{ instance, body string }{
		{"telegram.eu", `{"workspace_id":"support","default_agent_id":"test-agent"}`},
		{"telegram.us", `{"workspace_id":"support","default_agent_id":"jim"}`},
	} {
		w := setChannelRoutingReq(t, api, binding.instance, binding.body)
		require.Equal(t, http.StatusOK, w.Code, "initial real bound control: %s", w.Body.String())
	}
	readPair := func(id string) gen.ChannelRouting {
		t.Helper()
		w := getChannelRoutingReq(t, api, id)
		require.Equal(t, http.StatusOK, w.Code)
		var pair gen.ChannelRouting
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &pair))
		return pair
	}
	diskInstance := func(id string) json.RawMessage {
		t.Helper()
		raw, readErr := os.ReadFile(api.configPath())
		require.NoError(t, readErr)
		var cfg map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &cfg))
		var instances map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(cfg["channels"], &instances))
		require.Contains(t, instances, id, "instrument must see actual sibling config")
		return instances[id]
	}
	wantSibling := gen.ChannelRouting{WorkspaceId: "support", DefaultAgentId: "jim", Bound: true}
	require.Equal(t, wantSibling, readPair("telegram.us"))
	beforeDisk := diskInstance("telegram.us")
	beforeLive := api.agentLoop.GetConfig().Channels["telegram.us"]
	w := setChannelRoutingReq(t, api, "telegram.eu", `{"workspace_id":"sales","default_agent_id":"test-agent"}`)
	require.Equal(t, http.StatusOK, w.Code, "target save genuinely succeeds: %s", w.Body.String())
	assert.Equal(t, gen.ChannelRouting{WorkspaceId: "sales", DefaultAgentId: "test-agent", Bound: true}, readPair("telegram.eu"))
	assert.JSONEq(t, string(beforeDisk), string(diskInstance("telegram.us")), "saving EU changes no sibling setting on disk")
	assert.Equal(t, beforeLive, api.agentLoop.GetConfig().Channels["telegram.us"], "saving EU changes no live sibling setting")
	assert.Equal(t, wantSibling, readPair("telegram.us"), "sibling GET reports its own unchanged pair")
	jim, ok := api.agentLoop.GetRegistry().GetAgent("jim")
	require.True(t, ok)
	provider := testutil.NewScenario().WithText("US sibling still answers as Jim")
	jim.Provider = provider // external model edge only; routing and stores stay real
	answer, err := api.agentLoop.ProcessDirectWithChannel(context.Background(), "sibling input", "sibling-check", "telegram.us", "same-person")
	require.NoError(t, err, "next sibling inbound must remain routable to its own team")
	assert.Equal(t, "US sibling still answers as Jim", answer)
	assert.Len(t, provider.AllRequests(), 1, "sibling input reaches its canonical Jim provider exactly once")
}
