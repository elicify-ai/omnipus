//go:build goolm && stdjson

package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

func crashCeiling(t *testing.T, beforeRelease ...func(*crashHarness, *session.LifecycleRecord)) (*crashHarness, *session.LifecycleRecord) {
	t.Helper()
	gate := make(chan struct{})
	p := &crashScript{entered: make(chan int, 8), gate: gate}
	h := crashNew(t, p)
	work, err := workspace.EnsureWorkDir(h.home, testHarnessWorkspaceMembershipID)
	require.NoError(t, err)
	path := filepath.Join(work, "read-only.txt")
	require.NoError(t, os.WriteFile(path, []byte("unchanged\n"), 0600))
	p.next = func(i int, _ []providers.Message) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{crashCall(fmt.Sprintf("read-%d", i), "read_file", map[string]any{"path": path})}, FinishReason: "tool_calls"}, nil
	}
	rec, owner := crashChild(t, h, p)
	for _, install := range beforeRelease {
		install(h, rec)
	}
	close(gate)
	crashAwaitDone(t, owner.done)
	require.Len(t, p.calls(), 2, "configured small ceiling must actually execute exactly two model steps")
	history := h.al.GetSessionStore().GetHistory(rec.SessionID)
	var calls, results int
	for _, m := range history {
		calls += len(m.ToolCalls)
		if m.Role == "tool" {
			results++
		}
	}
	require.Equal(t, 2, calls, "the native loop, not a forged turnResult, reached the ceiling")
	require.Equal(t, 2, results, "both real tool steps settled before the ceiling outcome")
	bytes, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "unchanged\n", string(bytes))
	return h, rec
}

func TestCrashRecovery_ToolCeilingPersistsInterrupted(t *testing.T) {
	h, before := crashCeiling(t)
	got := h.record(t, before.SessionID)
	assert.Equal(t, session.LifecycleState("interrupted"), got.State, "Q14: the real ceiling execution ended; it must not remain Running or become Failed")
	assert.True(t, got.Terminal(), "Interrupted freezes this generation, not the conversation")
	assert.Equal(t, "tool_limit", crashFields(t, got)["interruption_reason"], "persist a typed cause, not transcript-prose inference")
	assert.Equal(t, before.Generation, got.Generation, "completion does not revive its own generation")
	assert.Equal(t, before.ExecutionID, got.ExecutionID, "outcome belongs to the producing execution")
	assert.Empty(t, got.FailedReason, "a limit is not a generic failure")
	assert.Nil(t, got.NeedsInput, "a ceiling is not a parked question")
	if assert.NotNil(t, got.FinalDelivery, "ended ceiling must use the durable final/outbox boundary") {
		assert.Equal(t, before.ExecutionID.RunID, got.FinalDelivery.CommitID, "outbox uses the original producing run")
	}
	messages, _, more, err := h.al.GetMessageInboxStore().Drain(before.SteeringSessionID(), before.SessionID, "", 10)
	require.NoError(t, err)
	require.False(t, more)
	require.Len(t, messages, 1, "one nonfatal parent outcome")
	e, err := messages[0].AsSessionMessageError()
	require.NoError(t, err)
	assert.False(t, e.Fatal, "ceiling notice is ended but nonfatal")
}

func TestCrashRecovery_ToolCeilingExecutionDoesNotAutoResume(t *testing.T) {
	h, before := crashCeiling(t)
	ended := h.record(t, before.SessionID)
	for boot := 0; boot < 2; boot++ {
		gate := make(chan struct{})
		p := &crashScript{gate: gate, entered: make(chan int, 8)}
		// Cleanup opens a wrongly admitted baseline execution even on assertion failure.
		t.Cleanup(func() { close(gate) })
		h.reboot(t, p)
		got := h.record(t, before.SessionID)
		assert.Equal(t, session.LifecycleState("interrupted"), got.State, "Q14: boot %d must preserve the ended state instead of admitting a helper", boot+1)
		assert.Equal(t, ended.ExecutionID, got.ExecutionID, "no new execution may be stamped on a ceiling-ended helper")
		assert.Nil(t, got.StopNote, "boot must not manufacture Stopped(restart) for an already-ended execution")
		// Run returns only after all synchronous admission decisions. An execution
		// identity change detects even a goroutine not yet scheduled at the provider.
		assert.Equal(t, before.Generation, got.Generation)
		assert.Empty(t, p.calls(), "zero recovered model requests for the ended helper")
		close(gate)
		// Cleanup must not close the channel twice.
		gate = make(chan struct{})
		if got.ExecutionID != nil {
			if owner := h.al.executionDispositionFor(h.al.executionClaimFor(got)); owner != nil {
				crashAwaitDone(t, owner.done)
			}
		}
	}
}

func TestCrashRecovery_ExplicitCeilingContinuationUsesNextGeneration(t *testing.T) {
	h, before := crashCeiling(t)
	ended := h.record(t, before.SessionID)
	gate := make(chan struct{})
	p := &crashScript{gate: gate, entered: make(chan int, 8)}
	h.al.GetRegistry().primaryChatAgentForTest().Provider = p
	defer close(gate)
	gen, err := NewSteerCanceller(h.al.GetSessionLifecycleStore()).Revive(context.Background(), before.SessionID,
		steer.Principal{Kind: steer.PrincipalKindHuman, ID: "owner"})
	require.NoError(t, err, "explicit human continuation remains reachable")
	assert.Equal(t, before.Generation+1, gen, "Q14: explicit continuation starts G+1, never reuses the frozen ended round")
	launched, err := NewSteerLauncher(h.al).Dispatch(context.Background(), before.SessionID, gen)
	require.NoError(t, err)
	require.Equal(t, steer.DispatchRunning, launched.State)
	require.Equal(t, 0, crashAwait(t, p.entered))
	got := h.record(t, before.SessionID)
	assert.NotEqual(t, ended.ExecutionID, got.ExecutionID, "continuation has a fresh execution")
	assert.Equal(t, session.LifecycleRunning, got.State)
}

func TestCrashRecovery_GenuineInFlightHelperStillResumes(t *testing.T) {
	h := newPWBootRecoveryHarness(t)
	parent := h.rootSession(t)
	child := h.newSession(t, session.SessionTypeDelegate, parent)
	h.persist(t, h.steeredRecord(child, parent, session.LifecycleRunning))
	h.seedCompletedToolHistory(t, child)
	before, err := h.lifecycle.Load(child)
	require.NoError(t, err)
	require.NoError(t, h.recovery().Run(context.Background()))
	h.provider.await(t)
	after, err := h.lifecycle.Load(child)
	require.NoError(t, err)
	assert.Equal(t, session.LifecycleRunning, after.State, "positive control: a killed-in-flight helper is not an already-ended ceiling")
	assert.NotEqual(t, before.ExecutionID, after.ExecutionID)
	require.NoError(t, h.recovery().Run(context.Background()))
	assert.Len(t, h.provider.requests, 0, "a second sweep in this boot cannot dispatch the same helper twice")
}
