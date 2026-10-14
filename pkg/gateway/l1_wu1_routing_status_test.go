package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// FR-121/129 / B2 K15 / BDD-13.1/13.12: dropped inbound traffic must be
// visible in the actual channel REST status and repair must clear it.
func TestL1WU1ChannelRoutingDrop_StatusDegradesThenValidBindingClears(t *testing.T) {
	api := buildExecutorTestAPI(t)
	writeTestWorkspaceJSON(t, api, "sales", "active", []string{"test-agent"}, false)
	seedChannelInstance(t, api, "telegram")
	inst, ok := api.agentLoop.GetRegistry().GetAgent("test-agent")
	require.True(t, ok)
	provider := testutil.NewScenario().WithText("bound recovery answer")
	inst.Provider = provider // only the external model edge is replaced
	channelEntry := func() gen.ChannelEntry {
		t.Helper()
		w := httptest.NewRecorder()
		api.HandleChannels(w, httptest.NewRequest(http.MethodGet, "/api/v1/channels", nil))
		require.Equal(t, http.StatusOK, w.Code)
		var rows []gen.ChannelEntry
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &rows))
		for _, row := range rows {
			if row.Id == "telegram" {
				return row
			}
		}
		t.Fatal("known configured telegram channel missing from the REST status instrument")
		return gen.ChannelEntry{}
	}
	assert.Nil(t, channelEntry().LastDroppedAt, "no fabricated drop time before any input")
	started := time.Now().UTC()
	_, err := api.agentLoop.ProcessDirectWithChannel(context.Background(), "unbound input", "l1-unbound-status", "telegram", "person-status")
	finished := time.Now().UTC()
	assert.Error(t, err, "FR-121: unbound message must be refused before any turn")
	recorded, ok := api.agentLoop.RoutingDropFor("telegram")
	require.True(t, ok, "the actual input must record its routing drop")
	assert.False(t, recorded.At.IsZero())
	assert.False(t, recorded.At.Before(started), "drop time cannot predate this input")
	assert.False(t, recorded.At.After(finished), "drop time cannot postdate the completed drop")
	assert.Empty(t, provider.AllRequests(), "unbound message must not reach an agent")
	dropped := channelEntry()
	require.NotNil(t, dropped.LastDroppedAt, "F09 / M17: REST must expose this actual drop's timestamp")
	assert.True(t, dropped.LastDroppedAt.Equal(recorded.At), "REST timestamp must equal the actual recorded input drop: got %s want %s", dropped.LastDroppedAt, recorded.At)
	if assert.NotNil(t, dropped.Degraded, "K15: routing degradation must be visible, not hidden as runtime-only state") {
		assert.True(t, *dropped.Degraded)
	}
	if assert.NotNil(t, dropped.DegradedReason) {
		assert.Equal(t, "bind this channel to an agent in a workspace", *dropped.DegradedReason)
	}
	w := setChannelRoutingReq(t, api, "telegram", `{"workspace_id":"sales","default_agent_id":"test-agent"}`)
	require.Equal(t, http.StatusOK, w.Code, "valid binding positive control: %s", w.Body.String())
	repaired := channelEntry()
	assert.Nil(t, repaired.LastDroppedAt, "valid binding repair removes the old drop timestamp")
	_, stillDropped := api.agentLoop.RoutingDropFor("telegram")
	assert.False(t, stillDropped, "repair clears the recorded drop, not just the displayed reason")
	if repaired.Degraded != nil {
		assert.False(t, *repaired.Degraded, "FR-129: saving a valid binding clears the routing error")
	}
	if repaired.DegradedReason != nil {
		assert.Empty(t, *repaired.DegradedReason)
	}
	recoveryProvider := testutil.NewScenario().WithText("bound recovery answer")
	active, ok := api.agentLoop.GetRegistry().GetAgent("test-agent")
	require.True(t, ok)
	active.Provider = recoveryProvider // a fresh model-edge control; never replay a consumed response
	answer, err := api.agentLoop.ProcessDirectWithChannel(context.Background(), "bound input", "l1-bound-status", "telegram", "person-status")
	require.NoError(t, err, "after repair the next message must actually run")
	assert.Equal(t, "bound recovery answer", answer)
	assert.Len(t, recoveryProvider.AllRequests(), 1, "post-binding positive control must reach the model exactly once")
}
