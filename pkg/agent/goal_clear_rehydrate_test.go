package agent

// goal_clear_rehydrate_test.go pins the founder-reported goal-pill scenario
// at the backend: after `/goal clear` (the only user clear path — no REST or
// WS clear surface exists), the durable record must be TERMINAL so the
// attach-time rehydrate (EmitGoalStatusRehydrate, called by the gateway's
// attach path) never re-shows an active pill on a reload — including for a
// reader that reopens the goal store cold (the restart case).

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/stretchr/testify/require"
)

func TestGoalClear_CommandClearsRecordAndRehydrateIsSilent(t *testing.T) {
	al, agentInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	store, sid := newGoalTestSession(t, al, agentInst.ID)

	// A real, registered, ACTIVE goal record bound to this session — the
	// state whose pill the founder saw rehydrate on every reload.
	setActiveGoalRecordless(t, store, sid, "goal-pill-founder", "keep the founder's Jarvis chat tidy")
	require.True(t, al.EmitGoalStatusRehydrate(sid), "precondition: the active goal rehydrates before the clear")

	// The user's clear, through the real `/goal clear` command entry.
	opts := &processOptions{UserInitiated: true, TranscriptStore: store, TranscriptSessionID: sid}
	matched, handled, reply := al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal clear", UserInitiated: true}, agentInst, opts)
	require.True(t, matched, "/goal clear must match")
	require.True(t, handled, "/goal clear must answer synchronously")
	require.True(t, strings.Contains(reply, "cleared"), "clear reply = %q", reply)

	// The durable record must be terminal NOW: a fresh, cold goal store —
	// what a restarted gateway reopens — lists no active record for the
	// session, so a reload cannot rehydrate a pill.
	fresh := goal.NewStore(config.OmnipusHomeDir())
	active, err := fresh.ListActive()
	require.NoError(t, err)
	for i := range active {
		require.NotEqual(t, sid, active[i].ActiveSessionID,
			"a record bound to the cleared session must not stay active after /goal clear")
	}

	// The attach-time rehydrate must be silent for the cleared session.
	collector, cleanup := newEventCollector(t, al)
	defer cleanup()
	require.False(t, al.EmitGoalStatusRehydrate(sid), "no pill may rehydrate after a landed clear")
	deadline := time.Now().Add(500 * time.Millisecond)
	var payloads []GoalStatusChangedPayload
	for time.Now().Before(deadline) {
		payloads = goalStatusPayloadsFor(collector, sid)
		if len(payloads) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.Empty(t, payloads, "the rehydrate must emit no goal_status frame for a cleared goal")
}
