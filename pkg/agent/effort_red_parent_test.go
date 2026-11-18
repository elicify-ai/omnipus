package agent

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// S-04 coverage repair: a genuine delegated child executes, finishes and publishes to
// the actual parent's durable inbox. A returned root-turn string is not used
// as a stand-in for parent delivery.
func TestT_C3_S04_RealDelegatedParentReceivesEmptyAnswerSentinel(t *testing.T) {
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	parent := newTestSteeringSession(t, al, "effort-parent-workspace")
	p := &effortScriptedProvider{reasoning: redMarker + " private child reasoning", finish: "stop"}
	ag, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	require.True(t, ok)
	ag.Provider = p
	child, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{SteeringSessionID: parent, TargetAgentID: testDefaultAgentID, Task: "produce delegated answer", Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "effort-parent-call-7731"}})
	require.NoError(t, err)
	admitted, err := NewSteerLauncher(al).Dispatch(context.Background(), child.SessionID, child.Generation)
	require.NoError(t, err)
	require.Equal(t, steer.DispatchRunning, admitted.State)
	// Issue #1761: a terminal record carrying FinalDelivery only proves the
	// outbox COMMIT (commitSteeredCompletion). The parent-inbox append is
	// the later publication step (publishCommittedFinal), whose durable
	// receipt is the InboxAppended fact. Wait for that receipt, not the
	// commit, before reading the parent inbox.
	waitForCond(t, 5*time.Second, "actual child final publication", func() bool {
		return committedFinalInboxAppended(al, child.SessionID)
	})
	msgs, _, more, err := al.GetMessageInboxStore().Drain(parent, child.SessionID, "", 10)
	require.NoError(t, err)
	require.False(t, more)
	require.Len(t, msgs, 1, "one final handback, not a fabricated root response")
	kind, err := msgs[0].Discriminator()
	require.NoError(t, err)
	assert.Equal(t, "handback", kind, "S-04 requires an ordinary handback, not an error notice")
	result, err := msgs[0].AsSessionMessageHandback()
	require.NoError(t, err)
	assert.Equal(t, redDefaultResponse, result.ResultSoFar)
	assert.NotContains(t, result.ResultSoFar, redMarker)
	assert.Equal(t, 2, p.callCount(), "empty non-truncated child retries exactly once")
	entries, err := al.GetSessionStore().ReadTranscript(child.SessionID)
	require.NoError(t, err)
	answer := ""
	for _, entry := range entries {
		if entry.Role == "assistant" {
			answer = entry.Content
		}
	}
	assert.Equal(t, redDefaultResponse, answer, "actual child transcript has the same sentinel")
}

// committedFinalInboxAppended reports whether the child's committed final
// carries the durable InboxAppended receipt. publishCommittedFinal records
// that fact only after the upward deliverer has appended the exact committed
// message to the parent inbox, so it is the real "handback is readable"
// signal; a terminal record with FinalDelivery set is only the commit.
func committedFinalInboxAppended(al *AgentLoop, childID string) bool {
	lifecycle := al.GetSessionLifecycleStore()
	rec, err := lifecycle.Load(childID)
	if err != nil || !rec.Terminal() || rec.FinalDelivery == nil {
		return false
	}
	progress, _, _, err := lifecycle.FinalDeliveryState(childID, rec.Generation, rec.FinalDelivery.CommitID)
	return err == nil && progress.InboxAppended
}

// gatedUpwardDeliverer holds every Deliver call until released, so a test
// can observe the state between the outbox commit and the parent-inbox
// append deterministically (no timing).
type gatedUpwardDeliverer struct {
	inner   steer.UpwardDeliverer
	entered chan struct{}
	release chan struct{}
}

func (g *gatedUpwardDeliverer) Deliver(ctx context.Context, event steer.UpwardEvent) (steer.Delivery, error) {
	select {
	case g.entered <- struct{}{}:
	default:
	}
	<-g.release
	return g.inner.Deliver(ctx, event)
}

// Issue #1761 regression: the delegated child's final is published through a
// transactional outbox — the terminal state and the protected FinalDelivery
// tuple commit first, the parent-inbox append follows. The window between the
// two is real and by design, so "terminal with FinalDelivery set" is NOT
// proof the handback is in the parent inbox (the old wait condition of
// TestT_C3_S04_RealDelegatedParentReceivesEmptyAnswerSentinel). This test
// pins the window open: inside it the old condition already holds while the
// inbox is empty and InboxAppended is false; after release the handback
// arrives exactly once and InboxAppended is recorded.
func TestT_C3_S04_HandbackFollowsOutboxCommit_1761(t *testing.T) {
	al, _ := newSteerAL(t)
	classifier := NewSteerRecordClassifier(al.GetSessionLifecycleStore(), al.GetSessionStore())
	// Install the real deliverer first so SetSteerAudienceDeps back-wires its
	// *AgentLoop, then put the gate in front of it.
	inner := NewSteerUpwardDeliverer()
	al.SetSteerAudienceDeps(NewSteerAudienceResolver(classifier), steer.NopBoundaryObserver{}, inner)
	gate := &gatedUpwardDeliverer{inner: inner, entered: make(chan struct{}, 1), release: make(chan struct{})}
	al.SetSteerAudienceDeps(NewSteerAudienceResolver(classifier), steer.NopBoundaryObserver{}, gate)
	parent := newTestSteeringSession(t, al, "effort-parent-workspace-1761")
	p := &effortScriptedProvider{reasoning: redMarker + " private child reasoning", finish: "stop"}
	ag, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	require.True(t, ok)
	ag.Provider = p
	child, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{SteeringSessionID: parent, TargetAgentID: testDefaultAgentID, Task: "produce delegated answer", Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "effort-parent-call-1761"}})
	require.NoError(t, err)
	released := false
	t.Cleanup(func() {
		if !released {
			close(gate.release)
		}
	})
	admitted, err := NewSteerLauncher(al).Dispatch(context.Background(), child.SessionID, child.Generation)
	require.NoError(t, err)
	require.Equal(t, steer.DispatchRunning, admitted.State)

	select {
	case <-gate.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the child's final never reached the upward deliverer")
	}
	// Inside the window: the outbox commit is durable...
	rec, err := al.GetSessionLifecycleStore().Load(child.SessionID)
	require.NoError(t, err)
	require.True(t, rec.Terminal(), "the terminal state commits before publication")
	require.NotNil(t, rec.FinalDelivery, "the protected outbox tuple commits before publication")
	// ...but nothing is in the parent inbox yet and no receipt is recorded.
	msgs, _, _, err := al.GetMessageInboxStore().Drain(parent, child.SessionID, "", 10)
	require.NoError(t, err)
	require.Empty(t, msgs, "the handback is appended only after the commit, by publication")
	require.False(t, committedFinalInboxAppended(al, child.SessionID), "no InboxAppended receipt before the append")

	close(gate.release)
	released = true
	waitForCond(t, 5*time.Second, "durable InboxAppended receipt", func() bool {
		return committedFinalInboxAppended(al, child.SessionID)
	})
	msgs, _, more, err := al.GetMessageInboxStore().Drain(parent, child.SessionID, "", 10)
	require.NoError(t, err)
	require.False(t, more)
	require.Len(t, msgs, 1, "exactly one handback once the receipt is recorded")
	kind, err := msgs[0].Discriminator()
	require.NoError(t, err)
	assert.Equal(t, "handback", kind)
}
