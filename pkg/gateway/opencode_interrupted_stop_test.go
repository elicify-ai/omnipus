package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #1895 / docs/agents.md: a Stop that prevents continuation must close the
// interrupted turn exactly once; withholding done must not strand either viewer.
func TestOpenCodeFix1895_InterruptedThenStoppedWithoutResumeClosesBothViewers(t *testing.T) {
	var messages *bus.MessageBus
	al, child, viewers, original := opencodeFix1895Launch(t, opencodeFix1895Program(t), "live_followup_pending", func(_ *config.Config, actual *bus.MessageBus) {
		messages = actual
	})
	closed := false
	ctx := opencode1895PendingPersonIntake(t, al, &closed)
	var observed [2]opencode1895LiveObservation
	for i, viewer := range viewers {
		o := &observed[i]
		o.readUntil(t, viewer, child.SessionID, "original readiness never reached viewer", func() bool { return o.ready })
	}
	opencode1895ProbePaused(t, original)
	launched, err := al.GetSessionLifecycleStore().Load(child.SessionID)
	require.NoError(t, err)
	require.NotNil(t, launched.SteeredBy)
	// Cancel hooks use the persisted cascade routing id, not the child's own
	// transcript id. This harness has only this one active child in that root.
	hook := al.GetActiveTurnHookForSession(launched.SteeredBy.RootSessionID)
	require.NotNil(t, hook)
	originalTurnID := hook.TurnID()
	stopped := make(chan error, 1)
	// Existing public completion seam: ClaimCancel enables the finish callback
	// but does not request Stop or cancel the process. The real Stop is issued
	// only AFTER the person's interruption has finished the original body, and
	// synchronously before its post-turn drain. No blocked callback or sleep.
	require.True(t, hook.ClaimCancel())
	hook.SetOnCancelFinish(func(string) {
		_, stopErr := al.StopSession(context.Background(), agent.StopRequest{
			SessionID: child.SessionID, Channel: "webchat",
			By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "person-1895"},
		})
		stopped <- stopErr
	})
	opencode1895PublishPerson(t, ctx, al, messages, child.SessionID, "person-no-resume-1895")
	select {
	case stopErr := <-stopped:
		require.NoError(t, stopErr, "the real Stop must land after interruption and before continuation")
	case <-time.After(10 * time.Second):
		t.Fatal("person's interruption never reached the deterministic pre-drain Stop")
	}
	require.NoError(t, original.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, readErr := bufio.NewReader(original).ReadString('\n')
	require.ErrorIs(t, readErr, io.EOF, "original process was interrupted, never naturally released")
	al.Close() // Join the actual drain and its completion publications before observing absence/duplicates.
	closed = true
	rec, err := al.GetSessionLifecycleStore().Load(child.SessionID)
	require.NoError(t, err)
	require.True(t, rec.Stopped(), "the Stop must prevent continuation")
	require.Nil(t, al.GetActiveTurnBySession(child.SessionID), "no producing turn remains after the joined Stop")
	for i, viewer := range viewers {
		observed[i].drain(t, viewer, child.SessionID)
		done := observed[i].doneForTurn(originalTurnID)
		require.Len(t, done, 1, "#1895: viewer %d needs exactly ONE done for the interrupted, non-resumed turn", i)
		assert.Len(t, observed[i].done, 1, "no resumed or duplicate done may appear")
		assert.Empty(t, observed[i].tokens, "no answer was released and no resumed run may answer")
		assert.Empty(t, observed[i].errors, "Stop is not shown as a failed answer")
		opencode1895AssertStoppedIdle(t, observed[i].raw, i)
	}
	assert.Equal(t, observed[0].raw, observed[1].raw, "both live subscribers see the same completion")
}

func opencode1895PublishPerson(t *testing.T, ctx context.Context, al *agent.AgentLoop, messages *bus.MessageBus, sessionID, rowID string) {
	t.Helper()
	require.NotNil(t, messages)
	require.NoError(t, al.ResolveSessionStore(sessionID).AppendTranscriptWithProvenance(sessionID,
		session.TranscriptEntry{ID: rowID, Role: "user", Content: opencode1895LiveFollowup, Timestamp: time.Now().UTC()}, "person-1895"))
	require.NoError(t, messages.PublishInbound(ctx, bus.InboundMessage{
		Channel: "webchat", ChatID: "viewer-1895-0", SessionID: sessionID,
		Sender: bus.SenderInfo{CanonicalID: "webchat_user"}, GatewayUserID: "person-1895",
		Content: opencode1895LiveFollowup, TranscriptEntryID: rowID,
		Metadata: map[string]string{"agent_id": opencodeFix1895Worker}, UserInitiated: true, OperatorPrompt: true,
	}))
}

func opencode1895AssertStoppedIdle(t *testing.T, frames [][]byte, viewer int) {
	t.Helper()
	working := true // Each viewer first received the paused original's ready tool event.
	for _, raw := range frames {
		var fields map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &fields))
		var kind string
		require.NoError(t, json.Unmarshal(fields["type"], &kind))
		switch kind {
		case "done":
			working = false
		case "session_state":
			var state gen.SessionStateFrame
			require.NoError(t, json.Unmarshal(raw, &state))
			working = state.ActiveTurn != nil
		}
	}
	assert.False(t, working, "#1895: viewer %d must finish idle after Stop, not stuck working", viewer)
}
