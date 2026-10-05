package agent

import (
	"strings"
	"testing"
)

// Round-4 item 5: the real, registered delegate tool must report the
// finishing-window outcome returned by its production *AgentLoop sink.
// A fake returning literal int would miss the named EnqueueStatus signature.
func TestSteeredTurnDrain1020_RealDelegateSinkReportsFinishingChild(t *testing.T) {
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	parentID := newTestSteeringSession(t, al, "ws-1")
	child, provider := r1AdmitChild(t, al, parentID, "round4-real-delegate-sink", "answer before the late steer", "Mock response")
	var resultText string
	var resultIsError, buffered bool
	r1InjectBeforeCommit(t, al, child, func() {
		result := runDelegateSteer(t, al, parentID, child.SessionID, "ROUND4-REAL-SINK-LATE-STEER")
		resultText, resultIsError = result.ForLLM, result.IsError
		al.steering.mu.Lock()
		transition := al.steering.terminalizing[child.SessionID]
		buffered = transition != nil && len(transition.finishingItems) == 1 &&
			transition.finishingItems[0].message.Content == "ROUND4-REAL-SINK-LATE-STEER"
		al.steering.mu.Unlock()
	})
	provider.open(0)
	r1AwaitProvider(t, provider, 1)
	if !buffered {
		t.Fatal("delegate steer did not enter the real finishing buffer")
	}
	if resultIsError {
		t.Fatalf("delegate(action=steer) refused a finishing child: %q", resultText)
	}
	const want = "queued; the child is finishing and will see it next"
	if !strings.Contains(resultText, want) {
		t.Fatalf("real delegate sink returned %q, want caller-facing %q", resultText, want)
	}
	if strings.HasPrefix(resultText, "Steering message queued for session") {
		t.Fatalf("finishing child was misreported as ordinary queued steer: %q", resultText)
	}
	r1AssertNoFinal(t, al, child)
	assertSteerRevivalInput1020(t, provider.Requests()[1:], []string{"ROUND4-REAL-SINK-LATE-STEER"})
	provider.open(1)
	joinGoalFixtureRuns(t, al)
	r1RequireSameGenerationFinal(t, al, child, "Mock response")
}
