// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// steer_delegated_injection_test.go pins the mechanism steered-session-
// reachability.spec.ts (e2e) relies on but cannot itself observe reliably:
// a steering message enqueued against a DELEGATED CHILD's own session id
// (steer_reconstruct.go's SessionKey: rec.SessionID) is injected into that
// child's next round (loop_run_turn.go's pendingMessages drain, gated on
// pendingSteeringCountForScope(target.SessionKey) in session_worker.go).
//
// This does not merely prove the queue accepted the message — a scope no
// one ever drains would still accept an Enqueue call. It proves (1) the
// EventKindSteeringInjected event fires for the CHILD's session key, and
// (2) the message text is actually present in the provider request the
// child's next round sends.
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 \
//        -run '^TestSteerDelegatedChild_EnqueuedAgainstChildSessionInjectsIntoChildsNextRound$' \
//        ./pkg/agent/
package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

const steerDelegatedChildText = "STEER-DELEGATED-CHILD-PAYLOAD"

// waitForSteeringInjectedEvent drains sub until an EventKindSteeringInjected
// event scoped to sessionKey arrives, or timeout elapses. Events for OTHER
// session keys (e.g. the parent/steerer's own turn) are skipped rather than
// matched — this is what makes the mutation below (enqueue against the
// parent's session id) fail loudly instead of by accident.
func waitForSteeringInjectedEvent(t *testing.T, c <-chan Event, sessionKey string, timeout time.Duration) SteeringInjectedPayload {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case evt := <-c:
			if evt.Kind != EventKindSteeringInjected || evt.Meta.SessionKey != sessionKey {
				continue
			}
			payload, ok := evt.Payload.(SteeringInjectedPayload)
			require.True(t, ok, "EventKindSteeringInjected payload must be SteeringInjectedPayload, got %T", evt.Payload)
			return payload
		case <-deadline:
			t.Fatalf("no EventKindSteeringInjected event for session %q within %s — the child's next round never drained the queued steering message", sessionKey, timeout)
			return SteeringInjectedPayload{}
		}
	}
}

func TestSteerDelegatedChild_EnqueuedAgainstChildSessionInjectsIntoChildsNextRound(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()

	agentInst, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	require.True(t, ok, "SETUP: test agent must be registered")
	tool := registerTruncationEchoTool(al, agentInst)

	// targetScope is set to the CHILD's own session id once Launch returns,
	// below — steer_reconstruct.go's whole point is that a steered child's
	// turn runs with SessionKey: rec.SessionID, the child's OWN id, never
	// the parent/steerer's.
	var targetScope string
	provider := &truncationScriptedProvider{steps: []truncationScriptStep{
		// Round 1: a tool call forces a round 2 to exist — INV-3 says the
		// steer applies "at the child's next tool boundary", so there must
		// be a next round for it to land in. onCall runs synchronously
		// inside this round, mid-turn, mirroring
		// TestTruncationD6_ChainRebuildKeepsSteeringMessage.
		{
			toolCalls:    []providers.ToolCall{truncationEchoCall("call-1", "go")},
			finishReason: "stop",
			onCall: func() {
				require.NoError(t, al.EnqueueSteeringMessage(targetScope, testDefaultAgentID,
					providers.Message{Role: "user", Content: steerDelegatedChildText}))
			},
		},
		// Round 2: the child's next round — where the steering message must
		// appear.
		{content: "child continuing", finishReason: "stop"},
	}}
	agentInst.Provider = provider

	launcher := NewSteerLauncher(al)
	steerer := newTestSteeringSession(t, al, "ws-1")
	launched, err := launcher.Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: steerer,
		TargetAgentID:     testDefaultAgentID,
		Task:              "delegated child for steering-injection pin",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate},
	})
	require.NoError(t, err, "Launch")
	require.NotEqual(t, steerer, launched.SessionID, "SETUP: the child must have its OWN session id, distinct from the steerer/parent")
	targetScope = launched.SessionID

	sub := al.SubscribeEvents(16)
	defer al.UnsubscribeEvents(sub.ID)

	dr, err := launcher.Dispatch(context.Background(), launched.SessionID, launched.Generation)
	require.NoError(t, err, "Dispatch")
	require.Equal(t, steer.DispatchRunning, dr.State)

	ts := al.getActiveTurnState(launched.SessionID)
	require.NotNil(t, ts, "child turn must be registered")
	select {
	case <-ts.Finished():
	case <-time.After(10 * time.Second):
		t.Fatal("child turn did not finish within 10s")
	}

	require.Equal(t, int32(1), tool.calls.Load(), "SETUP: round 1's scripted tool call must have run")

	// 1. The child's own next round must actually have drained and injected
	// the message — an EventKindSteeringInjected fired for the CHILD's
	// session key, not merely a queue that accepted an Enqueue call.
	payload := waitForSteeringInjectedEvent(t, sub.C, launched.SessionID, 3*time.Second)
	assert.Equal(t, 1, payload.Count, "exactly one steering message should have been injected")

	// 2. The message text must actually reach the provider's request for
	// the child's next round — proving delivery, not just bookkeeping.
	requests := provider.Requests()
	require.Len(t, requests, 2, "two rounds must have been dispatched to the child")
	assert.Equal(t, 1, countMessagesContaining(requests[1], steerDelegatedChildText),
		"the child's next-round provider request must carry the steering message enqueued against its own session id")
}
