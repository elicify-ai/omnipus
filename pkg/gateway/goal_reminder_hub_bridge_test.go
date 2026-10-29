package gateway

import (
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Bridge-only R2 unit boundary. The synthetic event here complements, and
// never replaces, agent.TestGoalReminderLiveNoticeContract's actual keeper
// producer path. All expected fields below are independent dispatch literals.
func TestGoalReminderTurnStartHubBridge(t *testing.T) {
	t.Run("PublishesOnlyToEveryTabBoundToActualNoticeSession", func(t *testing.T) {
		h := makeMinimalHandler()
		first, firstFrames := makeForwarderTestConn(16)
		second, secondFrames := makeForwarderTestConn(16)
		other, otherFrames := makeForwarderTestConn(16)
		unbound, unboundFrames := makeForwarderTestConn(16)
		bindTestConnToSession(h, "bridge-first-tab", "r2-original-session", first)
		bindTestConnToSession(h, "bridge-second-tab", "r2-original-session", second)
		bindTestConnToSession(h, "bridge-other-tab", "r2-other-session", other)
		// A connected tab with no session binding must not receive the notice.
		h.sessions["bridge-unbound-tab"] = unbound
		bus := agent.NewEventBus()
		t.Cleanup(bus.Close)
		bus.SetSyncTap(h.hubSyncTap)

		id, goal, stamp, workingAgent := "r2-persisted-notice-1", "r2-original-goal", "2026-10-21T12:00:01.123456789Z", "mia"
		bus.Emit(agent.Event{
			Kind: agent.EventKindTurnStart,
			// Deliberately name another tab in the legacy routing fields: the
			// notice's actual session, not ChatID/Meta, owns hub delivery.
			Meta: agent.EventMeta{SessionKey: "r2-other-session", TurnID: "r2-real-turn"},
			Payload: agent.TurnStartPayload{Channel: "webchat", ChatID: "bridge-other-tab", IsRoot: true,
				ChatNotice: &generated.ReplayMessageFrame{
					Type: "replay_message", SessionId: "r2-original-session", Role: "system",
					Content: "Goal reminder: continuing work toward the goal",
					Id:      &id, GoalId: &goal, Timestamp: &stamp, AgentId: &workingAgent,
				}},
		})

		// The real sync tap/hub publishes synchronously. Count actual output
		// bytes, never a mock publisher invocation or an elapsed-time guess.
		require.Len(t, firstFrames, 1, "first bound tab must receive one actual replay frame")
		require.Len(t, secondFrames, 1, "every tab bound to the same session must receive the notice")
		assert.Empty(t, otherFrames, "the unrelated session must receive no notice")
		assert.Empty(t, unboundFrames, "an unbound connected tab must receive no notice")
		firstRaw, secondRaw := <-firstFrames, <-secondFrames
		assert.Equal(t, firstRaw, secondRaw, "both original-session tabs must get identical numbered notice bytes")
		for _, raw := range [][]byte{firstRaw, secondRaw} {
			var frame generated.ReplayMessageFrame
			require.NoError(t, json.Unmarshal(raw, &frame))
			assert.Equal(t, "replay_message", frame.Type)
			assert.Equal(t, "r2-original-session", frame.SessionId)
			assert.Equal(t, "system", frame.Role, "bridge must not manufacture a user bubble")
			assert.Equal(t, "Goal reminder: continuing work toward the goal", frame.Content)
			require.NotNil(t, frame.Id)
			assert.Equal(t, "r2-persisted-notice-1", *frame.Id)
			require.NotNil(t, frame.GoalId)
			assert.Equal(t, "r2-original-goal", *frame.GoalId)
			require.NotNil(t, frame.Timestamp)
			assert.Equal(t, "2026-10-21T12:00:01.123456789Z", *frame.Timestamp)
			require.NotNil(t, frame.AgentId)
			assert.Equal(t, "mia", *frame.AgentId)
			_, sequenced := frameSeq(t, raw)
			assert.True(t, sequenced, "use the real session hub, not an unsequenced direct send")
		}
	})

	t.Run("NilChatNoticePublishesNoFabricatedReplay", func(t *testing.T) {
		h := makeMinimalHandler()
		first, firstFrames := makeForwarderTestConn(16)
		second, secondFrames := makeForwarderTestConn(16)
		other, otherFrames := makeForwarderTestConn(16)
		unbound, unboundFrames := makeForwarderTestConn(16)
		bindTestConnToSession(h, "nil-first-tab", "r2-original-session", first)
		bindTestConnToSession(h, "nil-second-tab", "r2-original-session", second)
		bindTestConnToSession(h, "nil-other-tab", "r2-other-session", other)
		h.sessions["nil-unbound-tab"] = unbound
		bus := agent.NewEventBus()
		t.Cleanup(bus.Close)
		bus.SetSyncTap(h.hubSyncTap)
		bus.Emit(agent.Event{Kind: agent.EventKindTurnStart,
			Meta:    agent.EventMeta{SessionKey: "r2-original-session", TurnID: "r2-no-notice-turn"},
			Payload: agent.TurnStartPayload{Channel: "webchat", ChatID: "nil-first-tab", IsRoot: true, ChatNotice: nil},
		})
		assert.Empty(t, firstFrames, "nil carrier must not create a replay message for the first tab")
		assert.Empty(t, secondFrames, "nil carrier must not create a replay message for the second tab")
		assert.Empty(t, otherFrames, "nil carrier must not reach another session")
		assert.Empty(t, unboundFrames, "nil carrier must not be broadcast to an unbound tab")
	})
}
