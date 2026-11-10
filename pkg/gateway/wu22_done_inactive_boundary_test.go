package gateway

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The hook is the SOCKET publication edge, not an engine/finalizer mock.
// sessionHub.publishMeta journals the actual done before enqueueing it here.
// Forward the original bytes to the real connection without altering outcome.
type wu22DoneBoundaryConn struct {
	*wsConn
	observe func(generated.DoneFrame)
}

func (c *wu22DoneBoundaryConn) enqueue(raw []byte) bool {
	var frame generated.DoneFrame
	if err := json.Unmarshal(raw, &frame); err != nil {
		panic(err) // malformed producer JSON is an instrument failure, never a pass
	}
	if frame.Type == "done" {
		c.observe(frame)
	}
	return c.wsConn.enqueue(raw)
}

type wu22DoneBoundaryObservation struct {
	done          generated.DoneFrame
	snapshot      generated.SessionStateFrame
	activeVisible bool
	cancelPresent bool
	cancelAlive   bool
	cancelTurnID  string
}

// This fixture has one ordinary root and no descendants/follow-ups. Its public
// root cancel reader exposes the SAME candidate/IsAlive predicate that
// activeTurnForCancel(TurnOnly=true) would admit, without issuing a mutating
// Stop (which would change the turn or re-enter a locked publication hub).
func wu22ObserveDoneBoundary(t *testing.T, f *wu22EngineFixture) *[]wu22DoneBoundaryObservation {
	t.Helper()
	observations := make([]wu22DoneBoundaryObservation, 0, 1)
	handler, sid := f.cut.frames.handler, f.cut.frames.sid
	tap := &wu22DoneBoundaryConn{wsConn: f.cut.frames.conn}
	tap.observe = func(done generated.DoneFrame) {
		assert.Equal(t, sid, done.SessionId)
		freshClient := makeTestConn() // no finished-turn memory/old attachment state
		var snapshot generated.SessionStateFrame
		require.NoError(t, json.Unmarshal(handler.sessionStateBytes(freshClient, sid), &snapshot))
		_, _, _, active := f.al.ActiveForegroundTurnInfo(sid)
		hook := f.al.GetActiveTurnHookForSession(sid)
		observation := wu22DoneBoundaryObservation{done: done, snapshot: snapshot, activeVisible: active, cancelPresent: hook != nil}
		if hook != nil {
			observation.cancelAlive = hook.IsAlive()
			observation.cancelTurnID = hook.TurnID()
		}
		observations = append(observations, observation)
	}
	hub := handler.hubs.lookup(sid)
	require.NotNil(t, hub, "SETUP: existing real session hub is bound")
	hub.mu.Lock()
	delete(hub.conns, f.cut.frames.conn)
	hub.conns[tap] = struct{}{}
	hub.mu.Unlock()
	return &observations
}

func wu22AssertDoneInactive(t *testing.T, f *wu22EngineFixture, observations []wu22DoneBoundaryObservation, failed bool) {
	t.Helper()
	require.Len(t, observations, 1, "instrument: observe exactly one REAL done publication, not post-return cleanup")
	observation := observations[0]
	require.NotNil(t, observation.snapshot.SessionId)
	assert.Equal(t, f.cut.frames.sid, *observation.snapshot.SessionId)
	assert.Nil(t, observation.snapshot.ActiveTurn, "done-publication snapshot must not advertise a running turn to a fresh client")
	assert.False(t, observation.activeVisible, "ActiveForegroundTurnInfo must be inactive at done publication")
	assert.False(t, observation.cancelPresent && observation.cancelAlive, "activeTurnForCancel must not admit the just-completed ordinary root after done")
	if observation.cancelAlive {
		require.NotNil(t, observation.done.TurnId)
		assert.Equal(t, *observation.done.TurnId, observation.cancelTurnID, "instrument: live cancellation target is this exact done turn")
	}
	require.NotNil(t, observation.done.Stats)
	if failed {
		require.NotNil(t, observation.done.Stats.TurnFailed)
		assert.True(t, *observation.done.Stats.TurnFailed, "ordering fix must preserve the failed completion")
	} else if observation.done.Stats.TurnFailed != nil {
		assert.False(t, *observation.done.Stats.TurnFailed)
	}
}

// Chief round-3 ordering oracle: completion means the same turn is no longer
// discoverable as running or selectable by cancellation. Current HEAD is RED
// even though its eventual healthy engine/lifecycle outcomes are correct.
func TestWU22DonePublicationHealthyTurnIsInactive(t *testing.T) {
	f := wu22EngineSettlementFixture(t, false)
	observations := wu22ObserveDoneBoundary(t, f)
	answer, err := f.al.ProcessScheduled(context.Background(), "mia", f.cut.frames.sid, wu22EnginePrompt, "webchat", "wu22-engine-chat")
	require.NoError(t, err)
	assert.Equal(t, wu22EngineAnswer, answer)
	assert.Equal(t, 1, f.provider.calls)
	assert.Equal(t, 1, f.cut.finalizations)
	wu22AssertDoneInactive(t, f, *observations, false)
}

func TestWU22DonePublicationLateFailureTurnIsInactive(t *testing.T) {
	f := wu22EngineSettlementFixture(t, true)
	observations := wu22ObserveDoneBoundary(t, f)
	answer, err := f.al.ProcessScheduled(context.Background(), "mia", f.cut.frames.sid, wu22EnginePrompt, "webchat", "wu22-engine-chat")
	require.Error(t, err, "ordering probe must preserve already-working error propagation")
	assert.Empty(t, answer)
	assert.Equal(t, 1, f.provider.calls)
	assert.Equal(t, 1, f.cut.finalizations)
	wu22AssertDoneInactive(t, f, *observations, true)
	live, _ := wu22GatewayFinalFrames(t, f.cut.frames)
	assert.Equal(t, 1, wu22GatewayNoticeCount(t, live), "completion ordering must not duplicate the save notice")
	f.cut.restore()
}

var _ hubConn = (*wu22DoneBoundaryConn)(nil)
