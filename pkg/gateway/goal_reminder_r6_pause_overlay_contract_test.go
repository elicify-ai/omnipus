package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGoalReminderR6PauseOverlayThroughLiveHub drives a real fifth reminder
// through the keeper and the live hub. The durable goal stays active. The
// generated goal_status frame for that session and goal must say
// waiting_on_user. Founder R6, and contracts/components/schemas/GoalStatusFrame.yaml.
func TestGoalReminderR6PauseOverlayThroughLiveHub(t *testing.T) {
	f := r6NewPublicFixture(t)
	gate := r6SignalGapHoldFifth(t, f)
	r6SignalGapAssertNoPauseOverlay(t, f)
	r6SignalGapReleaseFifth(t, f, gate)
	r6SignalGapAssertPauseOverlay(t, f)
	r6SignalGapAssertDurablePause(t, f)
}

func r6SignalGapHoldFifth(t *testing.T, f *r6PublicFixture) chan struct{} {
	t.Helper()
	prior, err := f.al.GetSessionLifecycleStore().Load(f.root)
	require.NoError(t, err)
	f.provider.mu.Lock()
	f.provider.hold = true
	f.provider.mu.Unlock()
	f.age(t)
	f.engine.Tick(context.Background())
	var gate chan struct{}
	select {
	case gate = <-f.provider.requests:
	case <-time.After(10 * time.Second):
		t.Fatal("SETUP: the real keeper did not reach the provider for the fifth reminder")
	}
	current, err := f.al.GetSessionLifecycleStore().Load(f.root)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleRunning, current.State, "SETUP: the fifth reminder must be a real admitted running turn")
	require.NotNil(t, current.ExecutionID)
	require.NotEqual(t, prior.ExecutionID.RunID, current.ExecutionID.RunID)
	require.Equal(t, prior.Generation+1, current.Generation)
	_, err = goal.NewStore(config.OmnipusHomeDir()).Update(f.goalID, func(g *goal.Goal) error {
		g.ZeroOutputPushes = 4 // four completed outcomes; the fifth is still running
		return nil
	})
	require.NoError(t, err)
	return gate
}

func r6SignalGapReleaseFifth(t *testing.T, f *r6PublicFixture, gate chan struct{}) {
	t.Helper()
	before := f.count()
	require.Equal(t, 1, before, "SETUP: one real keeper notification was issued")
	close(gate)
	r6WarnAwaitProducingPause(t, f, before)
	assert.Equal(t, before, f.count(), "the fifth completed unproductive outcome must not publish a sixth reminder")
}

func r6SignalGapAssertNoPauseOverlay(t *testing.T, f *r6PublicFixture) {
	t.Helper()
	for _, ch := range []chan []byte{f.frames, f.other, f.unbound} {
		overlays, notices := r6SignalGapReadFrames(t, ch)
		assert.Empty(t, r6SignalGapPauseOverlays(overlays, f.root, f.goalID), "a still-running fifth reminder must not publish the pause overlay")
		for _, frame := range overlays {
			if frame.SessionId == f.root && frame.GoalId != nil && *frame.GoalId == f.goalID {
				assert.NotEqual(t, "waiting_on_user", frame.State, "running work must not show the waiting-on-user overlay")
			}
		}
		for _, notice := range notices {
			assert.NotEqual(t, r6GatewayPause, notice.Content, "a still-running fifth reminder must not publish the pause sentence")
		}
	}
}

func r6SignalGapAssertPauseOverlay(t *testing.T, f *r6PublicFixture) {
	t.Helper()
	overlays, notices := r6SignalGapReadFrames(t, f.frames)
	pauses := r6SignalGapPauseOverlays(overlays, f.root, f.goalID)
	require.Len(t, pauses, 1, "the bound session must receive one live goal_status pause frame")
	frame := pauses[0]
	assert.Equal(t, "goal_status", frame.Type)
	assert.Equal(t, "waiting_on_user", frame.State, "the live overlay is waiting_on_user; active is the durable goal, not this frame")
	assert.Equal(t, f.root, frame.SessionId)
	require.NotNil(t, frame.GoalId)
	assert.Equal(t, f.goalID, *frame.GoalId)
	assert.Equal(t, r6GatewayPause, frame.LatestReason)
	assert.Equal(t, 0, frame.Round, "the idle pause must not consume a timer round")
	var live []generated.ReplayMessageFrame
	for _, notice := range notices {
		if notice.Content == r6GatewayPause {
			live = append(live, notice)
		}
	}
	require.Len(t, live, 1, "the pause sentence must also arrive once as a live system notice")
	assert.Equal(t, "replay_message", live[0].Type)
	assert.Equal(t, "system", live[0].Role)
	assert.Equal(t, f.root, live[0].SessionId)
	require.NotNil(t, live[0].GoalId)
	assert.Equal(t, f.goalID, *live[0].GoalId)
	for _, ch := range []chan []byte{f.other, f.unbound} {
		other, _ := r6SignalGapReadFrames(t, ch)
		got := r6SignalGapPauseOverlays(other, f.root, f.goalID)
		require.Len(t, got, 1, "every existing subscriber must receive the producer's goal_status frame")
		assert.Equal(t, "waiting_on_user", got[0].State, "the hub must forward the waiting-on-user state unchanged")
		assert.Equal(t, "goal_status", got[0].Type)
		assert.Equal(t, f.root, got[0].SessionId)
		require.NotNil(t, got[0].GoalId)
		assert.Equal(t, f.goalID, *got[0].GoalId)
	}
}

func r6SignalGapAssertDurablePause(t *testing.T, f *r6PublicFixture) {
	t.Helper()
	persisted, err := goal.NewStore(config.OmnipusHomeDir()).Get(f.goalID)
	require.NoError(t, err)
	assert.Equal(t, "active", string(persisted.State), "the pause leaves the durable goal active")
	assert.Equal(t, 5, persisted.ZeroOutputPushes, "five completed unproductive outcomes pause")
	assert.Equal(t, 0, persisted.Round)
	w := httptest.NewRecorder()
	f.api.HandleSessions(w, httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+f.root, nil))
	require.Equal(t, http.StatusOK, w.Code)
	var detail generated.SessionDetail
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &detail))
	n := 0
	for _, row := range r6PauseHistoryMessages(t, f, detail, w.Body.Bytes()) {
		if row.Content != nil && *row.Content == r6GatewayPause {
			n++
			fields := r6MessageFields(t, row)
			assert.JSONEq(t, `"system"`, string(fields["role"]))
			assert.JSONEq(t, `"system"`, string(fields["type"]))
			assert.JSONEq(t, `"`+f.goalID+`"`, string(fields["goal_id"]))
		}
	}
	assert.Equal(t, 1, n, "the exact pause sentence is one public system row")
}

func r6SignalGapReadFrames(t *testing.T, ch chan []byte) ([]generated.GoalStatusFrame, []generated.ReplayMessageFrame) {
	t.Helper()
	var overlays []generated.GoalStatusFrame
	var notices []generated.ReplayMessageFrame
	for {
		select {
		case data := <-ch:
			var fields map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(data, &fields))
			var frameType string
			if raw, ok := fields["type"]; ok {
				require.NoError(t, json.Unmarshal(raw, &frameType))
			}
			switch frameType {
			case "goal_status":
				var frame generated.GoalStatusFrame
				require.NoError(t, json.Unmarshal(data, &frame))
				overlays = append(overlays, frame)
			case "replay_message":
				var frame generated.ReplayMessageFrame
				require.NoError(t, json.Unmarshal(data, &frame))
				notices = append(notices, frame)
			}
		default:
			return overlays, notices
		}
	}
}

func r6SignalGapPauseOverlays(frames []generated.GoalStatusFrame, sessionID, goalID string) []generated.GoalStatusFrame {
	var out []generated.GoalStatusFrame
	for _, frame := range frames {
		if frame.SessionId == sessionID && frame.GoalId != nil && *frame.GoalId == goalID && frame.LatestReason == r6GatewayPause {
			out = append(out, frame)
		}
	}
	return out
}
