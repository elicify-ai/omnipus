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

func TestGoalReminderR6PauseIsPublicThroughRESTAndLiveHub(t *testing.T) {
	f := r6NewPublicFixture(t)
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
		t.Fatal("SETUP: actual public keeper/notifier/ordinary receiver did not reach its real provider")
	}
	current, err := f.al.GetSessionLifecycleStore().Load(f.root)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleRunning, current.State)
	require.NotNil(t, current.ExecutionID)
	require.NotEqual(t, prior.ExecutionID.RunID, current.ExecutionID.RunID)
	require.Equal(t, prior.Generation+1, current.Generation)
	// Boundary isolation complements the agent pack's full five-outcome replay.
	// Four prior assessed outcomes are durable; THIS actually executing fifth
	// reminder must finish before any pause decision can be made.
	_, err = goal.NewStore(config.OmnipusHomeDir()).Update(f.goalID, func(g *goal.Goal) error { g.ZeroOutputPushes = 4; return nil })
	require.NoError(t, err)
	beforePause := r6ReplayFrames(t, f.frames)
	require.NotEmpty(t, beforePause, "positive instrument: real admitted reminder reaches the real hub/send queue")
	for _, frame := range beforePause {
		assert.NotEqual(t, r6GatewayPause, frame.Content, "a still-running fifth outcome cannot pause")
	}
	close(gate)
	require.Eventually(t, func() bool {
		rec, e := f.al.GetSessionLifecycleStore().Load(f.root)
		return e == nil && rec.State == session.LifecycleCompleted && f.al.GetActiveTurnHookForSession(f.root) == nil
	}, 10*time.Second, 10*time.Millisecond, "the actual producing reminder must complete and leave the live-turn registry")
	beforeDispatch := f.count()
	require.Equal(t, 1, beforeDispatch, "one real keeper notification has actually been issued")
	f.age(t)
	f.engine.Tick(context.Background())
	assert.Equal(t, beforeDispatch, f.count(), "fifth completed unproductive outcome must publish no sixth notification")
	w := httptest.NewRecorder()
	f.api.HandleSessions(w, httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+f.root, nil))
	require.Equal(t, http.StatusOK, w.Code, "actual session REST view must remain readable")
	var detail generated.SessionDetail
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &detail))
	var rows []map[string]json.RawMessage
	for _, row := range detail.Messages {
		if row.Content != nil && *row.Content == r6GatewayPause {
			rows = append(rows, r6MessageFields(t, row))
		}
	}
	require.Len(t, rows, 1, "R6: exact pause must be one PUBLIC REST system row, not a hidden bookmark/user bubble")
	row := rows[0]
	assert.JSONEq(t, `"system"`, string(row["role"]))
	assert.JSONEq(t, `"system"`, string(row["type"]))
	assert.JSONEq(t, `"`+f.goalID+`"`, string(row["goal_id"]))
	var live []generated.ReplayMessageFrame
	for _, frame := range r6ReplayFrames(t, f.frames) {
		if frame.Content == r6GatewayPause {
			live = append(live, frame)
		}
	}
	require.Len(t, live, 1, "R6: the same pause must arrive LIVE through the real event bus/hub")
	assert.Equal(t, "replay_message", live[0].Type)
	assert.Equal(t, "system", live[0].Role)
	assert.Equal(t, f.root, live[0].SessionId)
	require.NotNil(t, live[0].Id)
	require.NotNil(t, live[0].GoalId)
	require.NotNil(t, live[0].Timestamp)
	assert.JSONEq(t, `"`+*live[0].Id+`"`, string(row["id"]), "REST/live use the same persisted ID")
	assert.JSONEq(t, `"`+*live[0].Timestamp+`"`, string(row["timestamp"]), "REST/live use the same persisted instant")
	assert.Equal(t, f.goalID, *live[0].GoalId)
	for _, other := range []chan []byte{f.other, f.unbound} {
		for _, frame := range r6ReplayFrames(t, other) {
			assert.NotEqual(t, r6GatewayPause, frame.Content, "the original session's pause must not leak to unrelated/unbound tabs")
		}
	}
	persisted, err := goal.NewStore(config.OmnipusHomeDir()).Get(f.goalID)
	require.NoError(t, err)
	assert.Equal(t, 5, persisted.ZeroOutputPushes)
	assert.Equal(t, "active", string(persisted.State))
	assert.Equal(t, 0, persisted.Round, "idle pause is not a timer adjudication")
}

func r6MessageFields(t *testing.T, message any) map[string]json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(message)
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &fields))
	return fields
}
