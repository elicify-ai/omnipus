//go:build goolm && stdjson

package agent

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

func TestCrashRecovery_RetiredRoomDerivedHelperNeverBootResumesIndependentHelperDoes(t *testing.T) {
	f := crashConsumedRoomContext(t, true)
	h := f.h
	h.al.GetConfig().Performance.MaxParallelAgents = 3
	root := h.record(t, f.id)
	scope := crashFields(t, root)["room_scope"]
	require.NotNil(t, scope, "the consumed request stamps a trusted causal scope before delegation")
	gate := make(chan struct{})
	held := &crashScript{gate: gate, entered: make(chan int, 8)}
	t.Cleanup(func() {
		select {
		case <-gate:
		default:
			close(gate)
		}
	})
	h.al.GetRegistry().primaryChatAgentForTest().Provider = held
	launcher := NewSteerLauncher(h.al)
	derived, err := launcher.Launch(context.Background(), steer.LaunchRequest{SteeringSessionID: f.id, TargetAgentID: testDefaultAgentID, Task: "request-derived continuation", Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "derived-G-helper"}})
	require.NoError(t, err)
	_, err = launcher.Dispatch(context.Background(), derived.SessionID, derived.Generation)
	require.NoError(t, err)
	crashAwait(t, held.entered)
	oldDerived := h.record(t, derived.SessionID)
	assert.Equal(t, scope, crashFields(t, oldDerived)["room_scope"])
	derivedOwner := h.al.executionDispositionFor(h.al.executionClaimFor(oldDerived))
	require.NotNil(t, derivedOwner)
	otherParent := newTestSteeringSession(t, h.al, testHarnessWorkspaceMembershipID)
	independent, err := launcher.Launch(context.Background(), steer.LaunchRequest{SteeringSessionID: otherParent, TargetAgentID: testDefaultAgentID, Task: "independent continuation", Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "independent-helper"}})
	require.NoError(t, err)
	_, err = launcher.Dispatch(context.Background(), independent.SessionID, independent.Generation)
	require.NoError(t, err)
	crashAwait(t, held.entered)
	oldIndependent := h.record(t, independent.SessionID)
	assert.Nil(t, crashFields(t, oldIndependent)["room_scope"], "independent work must not inherit G by agent identity")
	independentOwner := h.al.executionDispositionFor(h.al.executionClaimFor(oldIndependent))
	require.NotNil(t, independentOwner)
	snapshot := h.snapshot(t)
	f.release()
	require.NoError(t, crashAwait(t, f.done))
	close(gate)
	crashAwaitDone(t, derivedOwner.done)
	crashAwaitDone(t, independentOwner.done)
	release := make(chan struct{})
	quiet := &crashScript{gate: release, entered: make(chan int, 8)}
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	h.restore(t, snapshot, quiet)
	h.recover(t)
	crashAwait(t, quiet.entered)
	retired := h.record(t, derived.SessionID)
	assert.Equal(t, session.LifecycleStopped, retired.State, "G-derived continuation is retired before any helper Dispatch")
	assert.Equal(t, oldDerived.ExecutionID, retired.ExecutionID, "old derived execution is never replaced by a boot continuation")
	resumed := h.record(t, independent.SessionID)
	assert.Equal(t, session.LifecycleRunning, resumed.State)
	assert.NotEqual(t, oldIndependent.ExecutionID, resumed.ExecutionID, "positive control: genuine independent in-flight helper resumes")
	assert.Equal(t, 1, len(quiet.calls()), "only the independent helper reaches the provider")
	owner := h.al.executionDispositionFor(h.al.executionClaimFor(resumed))
	require.NotNil(t, owner)
	close(release)
	crashAwaitDone(t, owner.done)
	bytes, err := os.ReadFile(f.path)
	require.NoError(t, err)
	assert.Equal(t, "G1\n", string(bytes))
}
