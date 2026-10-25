//go:build goolm && stdjson

package agent

import (
	"context"
	"fmt"
	"github.com/elicify-ai/omnipus/pkg/session"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

func TestCrashRecovery_RealTurnCeilingBoundaryHasExactStepCounts(t *testing.T) {
	for _, steps := range []int{0, 1, 2, 3} {
		t.Run(fmt.Sprintf("requested_steps_%d", steps), func(t *testing.T) {
			gate := make(chan struct{})
			p := &crashScript{gate: gate, entered: make(chan int, 8)}
			h := crashNew(t, p)
			t.Cleanup(func() {
				select {
				case <-gate:
				default:
					close(gate)
				}
			})
			work, err := workspace.EnsureWorkDir(h.home, testHarnessWorkspaceMembershipID)
			require.NoError(t, err)
			path := filepath.Join(work, "boundary.txt")
			require.NoError(t, os.WriteFile(path, []byte("unchanged\n"), 0600))
			p.next = func(i int, _ []providers.Message) (*providers.LLMResponse, error) {
				if i < steps {
					return &providers.LLMResponse{ToolCalls: []providers.ToolCall{crashCall(fmt.Sprintf("boundary-read-%d", i), "read_file", map[string]any{"path": path})}, FinishReason: "tool_calls"}, nil
				}
				return &providers.LLMResponse{Content: "ordinary complete answer", FinishReason: "stop"}, nil
			}
			rec, owner := crashChild(t, h, p)
			close(gate)
			crashAwaitDone(t, owner.done)
			wantSteps, wantCalls := steps, steps+1
			wantState := session.LifecycleCompleted
			if steps >= 2 {
				wantSteps, wantCalls = 2, 2
				wantState = session.LifecycleState("interrupted")
			}
			assert.Equal(t, wantCalls, len(p.calls()), "exact configured ceiling, not an elapsed-time proxy")
			var results int
			for _, message := range h.al.GetSessionStore().GetHistory(rec.SessionID) {
				if message.Role == "tool" {
					results++
				}
			}
			assert.Equal(t, wantSteps, results)
			ended := h.record(t, rec.SessionID)
			assert.Equal(t, wantState, ended.State)
			if steps >= 2 {
				assert.Equal(t, "tool_limit", crashFields(t, ended)["interruption_reason"])
			} else {
				_, present := crashFields(t, ended)["interruption_reason"]
				assert.False(t, present)
			}
			bytes, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, "unchanged\n", string(bytes))
		})
	}
}

func TestCrashRecovery_SecondSameBootCeilingPreservesBothFrozenRounds(t *testing.T) {
	crashRequireInterrupted(t)
	h, child := crashCeiling(t)
	first := h.record(t, child.SessionID)
	require.NotNil(t, first.FinalDelivery)
	gate := make(chan struct{})
	p := &crashScript{gate: gate, entered: make(chan int, 8)}
	t.Cleanup(func() {
		select {
		case <-gate:
		default:
			close(gate)
		}
	})
	work, err := workspace.EnsureWorkDir(h.home, testHarnessWorkspaceMembershipID)
	require.NoError(t, err)
	p.next = func(i int, _ []providers.Message) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{crashCall(fmt.Sprintf("second-ceiling-%d", i), "read_file", map[string]any{"path": filepath.Join(work, "read-only.txt")})}, FinishReason: "tool_calls"}, nil
	}
	h.al.GetRegistry().primaryChatAgentForTest().Provider = p
	gen, err := NewSteerCanceller(h.al.GetSessionLifecycleStore()).Revive(context.Background(), child.SessionID, steer.Principal{Kind: steer.PrincipalKindAgent, ID: child.SteeringSessionID()})
	require.NoError(t, err, "authorized parent continuation remains reachable")
	require.Equal(t, first.Generation+1, gen)
	_, err = NewSteerLauncher(h.al).Dispatch(context.Background(), child.SessionID, gen)
	require.NoError(t, err)
	crashAwait(t, p.entered)
	admitted := h.record(t, child.SessionID)
	owner := h.al.executionDispositionFor(h.al.executionClaimFor(admitted))
	require.NotNil(t, owner)
	close(gate)
	crashAwaitDone(t, owner.done)
	second := h.record(t, child.SessionID)
	assert.Equal(t, "interrupted", string(second.State))
	assert.Equal(t, "tool_limit", crashFields(t, second)["interruption_reason"])
	assert.Equal(t, gen, second.Generation)
	assert.NotEqual(t, first.ExecutionID.RunID, second.ExecutionID.RunID)
	assert.Equal(t, first.ExecutionID.BootSeq, second.ExecutionID.BootSeq, "second ceiling is a new round in the SAME boot")
	old, _, _, _, err := h.al.GetSessionLifecycleStore().CommittedFinalDelivery(child.SessionID, first.Generation, first.ExecutionID.RunID)
	require.NoError(t, err)
	assert.Equal(t, *first.FinalDelivery, old, "G+1 never rewrites G's outbox")
	messages, _, _, err := h.al.GetMessageInboxStore().Drain(child.SteeringSessionID(), child.SessionID, "", 10)
	require.NoError(t, err)
	require.Len(t, messages, 2)
	ids := map[string]bool{}
	for _, message := range messages {
		e, err := message.AsSessionMessageError()
		require.NoError(t, err)
		assert.False(t, e.Fatal)
		ids[messageIDOf(message)] = true
	}
	assert.Len(t, ids, 2, "each ended round has one independently deduplicated notice")
}

func TestCrashRecovery_UnauthorizedContinuationCannotReviveInterrupted(t *testing.T) {
	for _, by := range []steer.Principal{{Kind: steer.PrincipalKindHuman, ID: ""}, {Kind: steer.PrincipalKindAgent, ID: "unrelated-agent"}, {Kind: steer.PrincipalKindAgent, ID: "unrelated-session"}} {
		t.Run(string(by.Kind)+"/"+by.ID, func(t *testing.T) {
			crashRequireInterrupted(t)
			h, child := crashCeiling(t)
			before := h.record(t, child.SessionID)
			_, err := NewSteerCanceller(h.al.GetSessionLifecycleStore()).Revive(context.Background(), child.SessionID, by)
			assert.Error(t, err, "only a new identified human or authorized ancestor action can revive the ended round")
			assert.Equal(t, before, h.record(t, child.SessionID), "refused continuation cannot rewrite the frozen outcome or mint a generation")
		})
	}
}

func TestCrashRecovery_ActualCeilingProducesTruthfulStateEndAndSavedMessage(t *testing.T) {
	crashRequireInterrupted(t)
	h, child := crashCeiling(t)
	entries, err := h.al.GetSessionStore().ReadTranscript(child.SteeringSessionID())
	require.NoError(t, err)
	span := SubagentSpanID(child.Origin.CallID, child.Generation)
	var states, ends int
	for _, entry := range entries {
		if entry.SubagentState != nil && entry.SubagentState.SpanId == span && entry.SubagentState.State == "interrupted" {
			states++
			assert.Equal(t, "tool_limit", crashFields(t, entry.SubagentState)["interruption_reason"])
		}
		if entry.SubagentEnd != nil && entry.SubagentEnd.SpanId == span {
			ends++
			assert.Equal(t, "interrupted", string(entry.SubagentEnd.Status))
			assert.Equal(t, "tool_limit", crashFields(t, entry.SubagentEnd)["reason"])
		}
	}
	assert.Equal(t, 1, states)
	assert.Equal(t, 1, ends, "one actual ended frame, not a synthetic Failed adapter")
	own, err := h.al.GetSessionStore().ReadTranscript(child.SessionID)
	require.NoError(t, err)
	var final int
	// Spec reconciliation: the release ceiling notice is system-role/chat-only;
	// DESIGN Decision 1 still requires Interrupted/tool_limit metadata. Select
	// the notice independently of that metadata so missing stamping fails here.
	for _, entry := range own {
		if entry.Role == "system" && entry.Content == toolLimitResponse {
			final++
			t.Logf("CEILING_METADATA surface=saved-helper role=%s status=%q interruption_reason=%q", entry.Role, entry.Status, entry.InterruptionReason)
			assert.Equal(t, "interrupted", entry.Status)
			assert.Equal(t, "tool_limit", crashFields(t, entry)["interruption_reason"])
		}
	}
	assert.Equal(t, 1, final, "saved child message carries the same reason used by REST/replay consumers")
	assert.Nil(t, h.al.getActiveTurnState(child.SessionID), "no running timer/slot survives the ending")
}
