// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// steer_receipt_injection_test.go pins issue #870: SubagentStateFrame.yaml
// defines steering_receipt (required correlation_id, applied_at) and
// documents it as "the Go side stamps this" — until this delivery, nothing
// did. This test proves the emitter steer_frames.go::
// deliverSteeringReceiptsForInjection now wires up, reusing
// steer_delegated_injection_test.go's own harness pattern (a real delegated
// child, steered mid-round-1, asserted on in round 2):
//
//  1. A steer sent with a caller-supplied correlation_id gets that SAME id
//     echoed back by EnqueueSteeringMessage — never silently replaced.
//
//  2. The child's own next round (where INV-3 says a steer applies, "at the
//     child's next tool boundary") emits, to the PARENT's side, exactly one
//     subagent_state frame carrying a steering_receipt whose correlation_id
//     matches the steer sent and whose applied_at is a real, non-zero
//     RFC3339 timestamp — never a null, never absent, never duplicated.
//
//     Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 \
//     -run '^TestSteerDelegatedChild_InjectedSteerEmitsSteeringReceipt$' \
//     ./pkg/agent/
package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

const (
	steerReceiptChildText      = "STEER-RECEIPT-CHILD-PAYLOAD"
	steerReceiptCorrelationID  = "corr_test_receipt_001"
	steerReceiptFirstEventWait = 3 * time.Second
	steerReceiptQuietWindow    = 500 * time.Millisecond
)

// collectSteeringReceiptFrames drains sub until AT LEAST ONE subagent_state
// frame carrying a steering_receipt for parentSessionID has arrived (failing
// the test if none shows up within firstTimeout), then keeps draining for a
// further quiet window to catch any EXTRA receipt the injection might have
// double-emitted — the "exactly one receipt per applied steer" requirement
// only means something if a second, wrongly-emitted frame would actually be
// caught here rather than merely not being waited for.
func collectSteeringReceiptFrames(t *testing.T, c <-chan Event, parentSessionID string, firstTimeout, quiet time.Duration) []generated.SubagentStateFrame {
	t.Helper()
	var frames []generated.SubagentStateFrame
	timeout := firstTimeout
	for {
		select {
		case evt := <-c:
			if evt.Kind != EventKindSubagentState {
				continue
			}
			payload, ok := evt.Payload.(SubagentStatePayload)
			require.True(t, ok, "EventKindSubagentState payload must be SubagentStatePayload, got %T", evt.Payload)
			if payload.SessionID != parentSessionID || payload.Frame.SteeringReceipt == nil {
				continue
			}
			frames = append(frames, payload.Frame)
			timeout = quiet // first hit found; only wait the short quiet window for more
		case <-time.After(timeout):
			if len(frames) == 0 {
				t.Fatalf("no subagent_state frame carrying a steering_receipt for parent session %q within %s — the injected steer never got a receipt", parentSessionID, firstTimeout)
			}
			return frames
		}
	}
}

func TestSteerDelegatedChild_InjectedSteerEmitsSteeringReceipt(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()

	agentInst, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	require.True(t, ok, "SETUP: test agent must be registered")
	tool := registerTruncationEchoTool(al, agentInst)

	// targetScope becomes the CHILD's own session id once Launch returns,
	// below — exactly steer_delegated_injection_test.go's own setup.
	var targetScope string
	var resolvedCorrelationID string
	provider := &truncationScriptedProvider{steps: []truncationScriptStep{
		// Round 1: a tool call forces a round 2 to exist — the steer must
		// have a next round to land in.
		{
			toolCalls:    []providers.ToolCall{truncationEchoCall("call-1", "go")},
			finishReason: "stop",
			onCall: func() {
				var enqErr error
				resolvedCorrelationID, enqErr = al.EnqueueSteeringMessage(targetScope, testDefaultAgentID,
					providers.Message{Role: "user", Content: steerReceiptChildText}, steerReceiptCorrelationID)
				require.NoError(t, enqErr)
			},
		},
		// Round 2: the child's next round — where the steer applies and the
		// receipt must be stamped.
		{content: "child continuing", finishReason: "stop"},
	}}
	agentInst.Provider = provider

	launcher := NewSteerLauncher(al)
	steerer := newTestSteeringSession(t, al, "ws-1")
	launched, err := launcher.Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: steerer,
		TargetAgentID:     testDefaultAgentID,
		Task:              "delegated child for steering-receipt pin",
		// CallID is required for deliverSubagentState (and therefore for
		// the steering_receipt this test pins) — its own guard refuses to
		// emit anything when Origin.CallID is blank, exactly like the
		// bracketing subagent_start frame it shares a span id with
		// (subagentSpanID). steer_delegated_injection_test.go's sibling
		// harness never sets this because it only asserts on
		// EventKindSteeringInjected, which has no such requirement.
		Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-steer-receipt-launch"},
	})
	require.NoError(t, err, "Launch")
	require.NotEqual(t, steerer, launched.SessionID, "SETUP: the child must have its OWN session id, distinct from the steerer/parent")
	targetScope = launched.SessionID

	sub := al.SubscribeEvents(32)
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
	require.Equal(t, steerReceiptCorrelationID, resolvedCorrelationID,
		"a caller-supplied correlation_id must be echoed back unchanged, never replaced by a server-assigned one")

	// The child's own next round must actually have injected the steer —
	// same pin as TestSteerDelegatedChild_EnqueuedAgainstChildSessionInjectsIntoChildsNextRound.
	injectPayload := waitForSteeringInjectedEvent(t, sub.C, launched.SessionID, steerReceiptFirstEventWait)
	assert.Equal(t, 1, injectPayload.Count, "exactly one steering message should have been injected")

	// Exactly one steering_receipt, reported to the PARENT (steerer), for
	// the one applied steer — never zero (the field would be lying by
	// omission about an applied steer) and never more than one (the field
	// would be fabricating extra applications that never happened).
	frames := collectSteeringReceiptFrames(t, sub.C, steerer, steerReceiptFirstEventWait, steerReceiptQuietWindow)
	require.Len(t, frames, 1, "exactly one steering_receipt should have been emitted for the one applied steer")

	frame := frames[0]
	require.NotNil(t, frame.SteeringReceipt, "SETUP: collectSteeringReceiptFrames only collects frames with a non-nil receipt")
	assert.Equal(t, steerReceiptCorrelationID, frame.SteeringReceipt.CorrelationId,
		"the receipt's correlation_id must match the steer that was actually applied")

	appliedAt, perr := time.Parse(time.RFC3339, frame.SteeringReceipt.AppliedAt)
	require.NoError(t, perr, "applied_at must be a valid RFC3339 timestamp")
	assert.False(t, appliedAt.IsZero(), "applied_at must be non-zero — the steer was genuinely applied")

	require.NotNil(t, frame.ChildSessionId, "the receipt frame must report which child applied the steer")
	assert.Equal(t, launched.SessionID, *frame.ChildSessionId)
	assert.Equal(t, string(session.LifecycleRunning), frame.State)
	assert.Equal(t, steerer, frame.SessionId, "the receipt frame must be addressed to the PARENT's own session")
}
