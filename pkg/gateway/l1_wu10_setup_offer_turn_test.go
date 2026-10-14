package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/memrooms"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The runtime anchor is proved by a real remember(room=shared) effect in
// the default workspace's memory, never an internal context spy/mock.
func l1WU10AwaitTurn(t *testing.T, subscription agent.EventSubscription, done <-chan error, sessionID string) {
	t.Helper()
	timer := time.NewTimer(busDeliveryTimeout)
	defer timer.Stop()
	for {
		select {
		case event, open := <-subscription.C:
			require.True(t, open)
			if event.Kind == agent.EventKindTurnEnd {
				end, typed := event.Payload.(agent.TurnEndPayload)
				require.True(t, typed)
				if end.SessionID == sessionID {
					require.Equal(t, agent.TurnEndStatusCompleted, end.Status)
					return
				}
			}
		case runErr := <-done:
			t.Fatalf("real loop exited before the turn completed: %v", runErr)
		case <-timer.C:
			t.Fatal("no completed actual turn; not valid kickoff RED evidence")
		}
	}
}

func TestL1WU10RESTCreate_SetupTurnKeepsDefaultWorkspaceContext(t *testing.T) {
	h := newL1WU10SetupHarness(t)
	// Drive creation first even on the missing-producer baseline. A missing
	// named seam is a loud BLOCKED test, never an undefined-symbol compile.
	created := h.post(t, gen.WorkspaceCreateRequest{Name: "Foreign target, default anchor"})
	msg := h.awaitOffer(t, created.Id)
	h.awaitHandler(t)
	assert.Equal(t, h.defaultID, msg.Metadata["workspace_id"])
	assert.Equal(t, created.Id, msg.Metadata["setup_target_workspace_id"])
	omnipus, registered := h.loop.GetRegistry().GetAgent("omnipus")
	require.True(t, registered)
	_, present := omnipus.Tools.Get("remember")
	require.True(t, present)
	defaultRoom := memrooms.ResolveWorkspaceSharedRoom(h.api.homePath, h.defaultID)
	targetRoom := memrooms.ResolveWorkspaceSharedRoom(h.api.homePath, created.Id)
	_, ensureDefaultErr := memrooms.EnsureRoom(defaultRoom)
	require.NoError(t, ensureDefaultErr)
	_, ensureTargetErr := memrooms.EnsureRoom(targetRoom)
	require.NoError(t, ensureTargetErr)
	beforeDefault, defaultErr := memrooms.ListMemoryIDs(defaultRoom.MemoriesDir)
	require.NoError(t, defaultErr)
	beforeTarget, targetErr := memrooms.ListMemoryIDs(targetRoom.MemoriesDir)
	require.NoError(t, targetErr)
	h.provider.WithText("running-loop instrument")
	h.provider.WithToolCall("remember", `{"content":"setup anchor from real turn","category":"reference","room":"shared"}`).WithText("setup turn finished")
	subscription := h.loop.SubscribeEvents(128)
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.loop.Run(runCtx) }()
	t.Cleanup(func() {
		cancel()
		h.loop.Stop()
		select {
		case runErr := <-done:
			require.NoError(t, runErr)
		case <-time.After(busDeliveryTimeout):
			t.Error("actual setup loop did not stop")
		}
		h.loop.WaitForActiveRequests()
		h.loop.UnsubscribeEvents(subscription.ID)
	})
	// A real completed turn proves the consumer is running before replaying
	// the server-produced, captured wake unchanged into that consumer.
	require.NoError(t, h.bus.PublishInbound(context.Background(), bus.InboundMessage{
		Channel: "webchat", SessionID: h.miaMainID, ChatID: "wu10-instrument", Content: "real consumer instrument",
		UserInitiated: true, Metadata: map[string]string{"agent_id": "mia", "workspace_id": h.defaultID},
	}))
	l1WU10AwaitTurn(t, subscription, done, h.miaMainID)
	require.NoError(t, h.bus.PublishInbound(context.Background(), msg))
	l1WU10AwaitTurn(t, subscription, done, h.mainID)
	afterDefault, defaultReadErr := memrooms.ListMemoryIDs(defaultRoom.MemoriesDir)
	require.NoError(t, defaultReadErr)
	require.Len(t, afterDefault, len(beforeDefault)+1, "OSA-9.1: the actual shared-memory write belongs to the default turn workspace")
	known := make(map[string]bool, len(beforeDefault))
	for _, id := range beforeDefault {
		known[id] = true
	}
	newCount := 0
	for _, id := range afterDefault {
		if !known[id] {
			newCount++
			memory, readErr := memrooms.ReadMemoryFile(defaultRoom.MemoriesDir, id)
			require.NoError(t, readErr)
			assert.Equal(t, "setup anchor from real turn\n", memory.Body)
			assert.Equal(t, "omnipus", memory.Frontmatter.Author)
		}
	}
	assert.Equal(t, 1, newCount)
	afterTarget, targetReadErr := memrooms.ListMemoryIDs(targetRoom.MemoriesDir)
	require.NoError(t, targetReadErr)
	assert.Equal(t, beforeTarget, afterTarget, "the target workspace is data, not the turn's memory/workspace anchor")
	h.assertMainAnchor(t)
}
