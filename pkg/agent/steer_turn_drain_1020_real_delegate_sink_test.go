package agent

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// Round-4 item 5: the real, registered delegate tool must report the
// finishing-window outcome returned by its production *AgentLoop sink.
// A fake returning literal int would miss the named EnqueueStatus signature.
func TestSteeredTurnDrain1020_RealDelegateSinkReportsFinishingChild(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	parentID := newTestSteeringSession(t, al, "ws-1")
	child := launchRunningChild(t, al, parentID, "round4-real-delegate-sink")

	var hookCalls atomic.Int32
	var resultText string
	var resultIsError bool
	var buffered bool
	completeStateWriteTestHook = func(sessionID string) {
		if hookCalls.Add(1) != 1 {
			return // The revived generation's completion must not inject again.
		}
		result := runDelegateSteer(t, al, parentID, sessionID, "ROUND4-REAL-SINK-LATE-STEER")
		resultText, resultIsError = result.ForLLM, result.IsError
		al.steering.mu.Lock()
		transition := al.steering.terminalizing[sessionID]
		buffered = transition != nil && len(transition.finishingItems) == 1 &&
			transition.finishingItems[0].message.Content == "ROUND4-REAL-SINK-LATE-STEER"
		al.steering.mu.Unlock()
	}
	t.Cleanup(func() { completeStateWriteTestHook = nil })

	if err := al.completeSteeredTurn(context.Background(), child,
		turnResult{finalContent: "answer before the late steer"}, nil); err != nil {
		t.Fatalf("completeSteeredTurn: %v", err)
	}
	al.drainSteeredTurns(10 * time.Second)
	if hookCalls.Load() < 1 || !buffered {
		t.Fatalf("SETUP: expected the delegate steer in the real finishing buffer; hook calls=%d buffered=%v", hookCalls.Load(), buffered)
	}
	if resultIsError {
		t.Fatalf("delegate(action=steer) refused a finishing child: %q", resultText)
	}
	const want = "queued; the child is finishing and will see it next"
	if !strings.Contains(resultText, want) {
		t.Fatalf("real delegate sink returned %q, want caller-facing %q (issue #1020 round-4 item 5)", resultText, want)
	}
	if strings.HasPrefix(resultText, "Steering message queued for session") {
		t.Fatalf("finishing child was misreported as an ordinary queued steer: %q", resultText)
	}
	rec, err := al.GetSessionLifecycleStore().Load(child.SessionID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	if rec.Generation != child.Generation+1 || rec.State != session.LifecycleCompleted {
		t.Fatalf("late steer not consumed in exactly one revived generation: generation=%d state=%s, want generation=%d state=completed", rec.Generation, rec.State, child.Generation+1)
	}
}
