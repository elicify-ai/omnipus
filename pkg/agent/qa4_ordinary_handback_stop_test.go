package agent

// F2 oracle: QA4 dispatch and ADR-20260928::Amended 2026-10-06 D-A/D-B/D-C.
// Every live turn, including an ordinary root's actual helper-final wake, owns
// its execution. Plain Stop reaches that execution and its owner lands stopped.
// The O3 fixture supplies real human admission, registered delegation, committed
// helper final, inbox and bus wake. Only the external parent provider is held.
// GREEN-after-fix and mutation proof are deferred to independent CHECK.

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type qa4HandbackProvider struct {
	entered chan context.Context
	release chan struct{}
	calls   atomic.Int32
	once    sync.Once
}

func (p *qa4HandbackProvider) Chat(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	if p.calls.Add(1) == 1 {
		p.entered <- ctx
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.release:
		return &providers.LLMResponse{Content: uatO3ParentAnswer}, nil
	}
}

func (*qa4HandbackProvider) GetDefaultModel() string { return "qa4-held-handback" }
func (p *qa4HandbackProvider) open()                 { p.once.Do(func() { close(p.release) }) }

func TestQA4Stop_CompletedOrdinaryRootHandbackOwnsAndStopsItsRunningExecution(t *testing.T) {
	f := newUATO3QueuedHandback(t)
	f.release()
	f.joinRoot() // Joins the original human turn AND its actual owning tail.
	require.NoError(t, f.rootResult.err)
	completed := rootReopenedRecord(t, f.al, f.rootID)
	require.Equal(t, session.LifecycleCompleted, completed.State, "SETUP: the original human root really finished")
	require.Nil(t, completed.SteeredBy, "SETUP: this must exercise an ordinary root, not the steered-only branch")
	require.NotNil(t, completed.ExecutionID, "SETUP: original execution identity must be production-minted")
	helperBefore := rootReopenedRecord(t, f.al, f.childID)
	require.Equal(t, session.LifecycleCompleted, helperBefore.State)

	// Change only the external network/provider edge, after both old producers
	// have joined. Do not install turn hooks, seed identity, or rewrite records.
	parentAgent, ok := f.al.GetRegistry().GetAgent(completed.AgentID)
	require.True(t, ok)
	p := &qa4HandbackProvider{entered: make(chan context.Context, 1), release: make(chan struct{})}
	parentAgent.Provider = p
	wake := f.queuedWake(t)
	wakeDone := make(chan struct{})
	var wakeErr error
	go func() {
		defer close(wakeDone)
		_, _, wakeErr = f.al.processMessage(context.Background(), wake)
	}()
	joinWake := func() {
		select {
		case <-wakeDone: // Includes the hand-back's real disposition tail.
		case <-time.After(5 * time.Second):
			t.Fatal("hand-back provider was released but its actual owning tail did not join")
		}
	}
	t.Cleanup(func() { p.open(); joinWake() })
	var providerCtx context.Context
	select {
	case providerCtx = <-p.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("SETUP: actual helper-final bus wake never reached the held parent provider")
	}
	require.NoError(t, providerCtx.Err(), "SETUP: provider context must be live before Stop")
	selected := f.al.getActiveTurnState(f.rootID)
	require.NotNil(t, selected, "SETUP: actual hand-back must have registered a live turn")
	require.True(t, selected.IsAlive(), "SETUP: Stop must be invoked while that provider call is running")
	live := rootReopenedRecord(t, f.al, f.rootID)
	// Use nonfatal assertions here so a missing owner cannot hide the actual
	// Stop/no-cancellation/incorrect-settlement failure farther down the test.
	assert.Equal(t, session.LifecycleRunning, live.State, "F2: a running hand-back must not leave the old completed record visible to Stop")
	assert.NotEqual(t, completed.ExecutionID, live.ExecutionID, "F2: hand-back admission must mint a fresh execution, not borrow the completed human turn")
	assert.NotNil(t, selected.opts.executionDisposition, "F2: the ordinary hand-back's actual owner must carry its settlement disposition")
	claim := f.al.tsExecutionClaim(selected, f.rootID)
	assert.NotEmpty(t, claim.RunID, "F2: the live hand-back must carry its own execution identity")
	assert.Equal(t, f.al.executionClaimFor(live), claim, "F2: the durable running owner and actual provider turn must agree")

	res, err := f.al.StopSession(context.Background(), StopRequest{
		SessionID: f.rootID, By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "uat-o3-owner"}, Channel: "webchat",
	})
	require.NoError(t, err, "plain Stop must start against the genuinely running hand-back")
	require.NoError(t, res.RootErr)
	assert.True(t, res.Root.Fired, "F2: Stop must target the running provider, not treat its old completed record as a no-op")
	stopSelection, found := res.Selected[f.rootID]
	assert.True(t, found, "F2: Stop must select this root's new running execution")
	if found {
		assert.Equal(t, claim.RunID, stopSelection.Effect.Target.RunID, "Stop selected a different execution")
		assert.Equal(t, claim.BootSeq, stopSelection.Effect.Target.BootSeq)
		assert.Equal(t, claim.Generation, stopSelection.Effect.Target.Generation)
	}
	select {
	case <-providerCtx.Done():
		assert.ErrorIs(t, providerCtx.Err(), context.Canceled, "Stop must cancel the actual held provider call")
	case <-time.After(5 * time.Second): // Join/event deadline, not a timing oracle.
		t.Error("F2: plain Stop never reached the running hand-back provider; the completed record incorrectly made Stop a no-op")
	}
	p.open() // Also releases the faulty no-op case, so RED never leaks a producer.
	joinWake()
	assert.ErrorIs(t, wakeErr, context.Canceled, "the stopped hand-back must preserve its actual cancellation outcome")
	landed := rootReopenedRecord(t, f.al, f.rootID)
	assert.Equal(t, session.LifecycleStopped, landed.State, "F2: the selected hand-back owner must land stopped, not completed")
	assert.Nil(t, landed.Stop, "the owning tail must clear its landed Stop fence")
	if assert.NotNil(t, landed.StopNote, "the landed Stop must retain its human attribution") {
		assert.Equal(t, session.StopCauseStop, landed.StopNote.Cause)
		assert.Equal(t, "human:uat-o3-owner", landed.StopNote.By)
	}
	assert.False(t, landed.Terminal(), "stopped is resumable, not completed/terminal")
	assert.Equal(t, int32(1), p.calls.Load(), "one genuine helper final must start exactly one parent provider call")
	assert.Equal(t, helperBefore, rootReopenedRecord(t, f.al, f.childID), "plain Stop must not cascade into or rewrite the completed helper")
}
