package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type opencodeGateRevivalDriver struct {
	*opencodeFixDriver
	resumed      chan struct{}
	resumeEvents chan runner.RunEvent
	finishOnce   sync.Once
}

func (d *opencodeGateRevivalDriver) ResumeWith(_ context.Context, opts runner.RunOptions) (<-chan runner.RunEvent, error) {
	d.mu.Lock()
	d.resumeOpts = append(d.resumeOpts, opts)
	d.resumeIDs = append(d.resumeIDs, d.nativeID)
	first := len(d.resumeOpts) == 1
	d.mu.Unlock()
	if first {
		close(d.resumed)
		return d.resumeEvents, nil
	}
	// Revival can retain the old queued instruction for a subsequent drain.
	// Only the first replacement run is held; later valid continuations finish.
	events := make(chan runner.RunEvent, 2)
	events <- runner.RunEvent{Kind: runner.EventKindOutput, Output: &runner.OutputEvent{Text: "retained queued instruction applied"}}
	events <- runner.RunEvent{Kind: runner.EventKindEnd}
	close(events)
	return events, nil
}

func (d *opencodeGateRevivalDriver) finishResume() {
	d.finishOnce.Do(func() {
		d.resumeEvents <- runner.RunEvent{Kind: runner.EventKindOutput, Output: &runner.OutputEvent{Text: "new execution's own answer"}}
		d.resumeEvents <- runner.RunEvent{Kind: runner.EventKindEnd}
		close(d.resumeEvents)
	})
}

// ARCHITECT A4: unit boundary = drain/held-output settlement. The held-tail
// input is reconstructed from a REAL admitted producer's immutable identity;
// its pending-done flag is set only by the real deferral function. It is NOT a
// second registered producer. Actual Stop, revival and replacement Dispatch
// establish the stale-claim condition before invoking the old drain. Only the
// external process and outgoing stream transport are doubles. This forces the
// conditional stale-tail input; it does not claim a browser race was observed.
func TestOpenCodeGateA4_StaleHeldDoneCannotCloseNewExecution(t *testing.T) {
	for _, supersede := range []bool{false, true} {
		name := "same_execution_settles_once"
		if supersede {
			name = "stopped_and_restarted_suppresses_old_done"
		}
		t.Run(name, func(t *testing.T) {
			al := newDelegateDispatchLoop(t, &delegateDispatchProvider{})
			wireSteerCompletionDeps(t, al)
			worker, ok := al.registry.GetAgent(delegateExtCLIAgentID)
			require.True(t, ok)
			worker.Subagents = &config.SubagentsConfig{Executor: &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: "opencode"}}
			driver := &opencodeGateRevivalDriver{
				opencodeFixDriver: &opencodeFixDriver{started: make(chan struct{}), firstEvents: make(chan runner.RunEvent, 2), readyAtStart: true},
				resumed:           make(chan struct{}), resumeEvents: make(chan runner.RunEvent, 2),
			}
			previous := newExternalDriver
			newExternalDriver = func(string, runner.ConsentHandler) (runner.ExternalAgentRunner, error) { return driver, nil }
			t.Cleanup(func() {
				driver.finishFirst()
				driver.finishResume()
				opencodeFixJoin(t, al)
				newExternalDriver = previous
			})
			child := launchDelegateTo(t, al, delegateExtCLIAgentID, "original held-output execution")
			_, err := NewSteerLauncher(al).Dispatch(context.Background(), child.SessionID, child.Generation)
			require.NoError(t, err)
			select {
			case <-driver.started:
			case <-time.After(5 * time.Second):
				t.Fatal("original real admission did not reach its CLI process")
			}
			old := al.getActiveTurnState(child.SessionID)
			require.NotNil(t, old)
			snapshot, err := al.GetSessionLifecycleStore().Load(child.SessionID)
			require.NoError(t, err)
			claim := al.tsExecutionClaim(old, "")
			require.True(t, claim.matches(snapshot), "positive control: never substitute a fabricated admission stamp")
			require.True(t, al.steerAdmission().hasExecutionReservation(claim))
			old.mu.RLock()
			held := newTurnState(old.agent, old.opts, al.newTurnEventScope(old.agentID, old.sessionKey))
			held.generation, held.executionRunID, held.executionBootSeq = old.generation, old.executionRunID, old.executionBootSeq
			held.turnID = old.turnID
			old.mu.RUnlock()
			require.Equal(t, claim, al.tsExecutionClaim(held, ""), "held output belongs to the genuine prior execution, not the replacement")
			transport := &mockStreamer{}
			held.setLastStreamer(transport)
			_, err = al.EnqueueSteeringMessage(child.SessionID, delegateExtCLIAgentID,
				providers.Message{Role: "user", Content: "continue after the current run"}, "gate-a4")
			require.NoError(t, err)
			require.True(t, al.deferExternalLiveDoneForResume(held, child.SessionID, context.Canceled))
			require.True(t, held.externalLiveDoneDeferred.Load(), "real deferral, not a manually assigned pending flag")
			require.Zero(t, transport.finalizeCalled)
			if !supersede {
				require.NoError(t, al.settleDeferredExternalLiveDone(context.Background(), held))
				assert.Equal(t, 1, transport.finalizeCalled, "positive control: an eligible held done really reaches the transport")
				require.NoError(t, al.settleDeferredExternalLiveDone(context.Background(), held))
				assert.Equal(t, 1, transport.finalizeCalled, "a held done settles at most once")
				return
			}
			canceller := NewSteerCanceller(al.GetSessionLifecycleStore(), al.SteerGenerationCancel)
			report, err := canceller.CancelSubtree(context.Background(), child.SessionID, steer.Principal{Kind: steer.PrincipalKindHuman, ID: "gate-a4-person"})
			require.NoError(t, err)
			require.Equal(t, []string{child.SessionID}, report.Reached)
			driver.finishFirst()
			opencodeFixJoin(t, al)
			stopped, err := al.GetSessionLifecycleStore().Load(child.SessionID)
			require.NoError(t, err)
			require.Equal(t, session.LifecycleStopped, stopped.State)
			generation, err := canceller.Revive(context.Background(), child.SessionID, steer.Principal{Kind: steer.PrincipalKindHuman, ID: "gate-a4-person"})
			require.NoError(t, err)
			_, err = NewSteerLauncher(al).Dispatch(context.Background(), child.SessionID, generation)
			require.NoError(t, err)
			select {
			case <-driver.resumed:
			case <-time.After(5 * time.Second):
				t.Fatal("replacement real admission did not start the retained CLI conversation")
			}
			current := al.getActiveTurnState(child.SessionID)
			require.NotNil(t, current)
			require.NotSame(t, old, current)
			replacement, err := al.GetSessionLifecycleStore().Load(child.SessionID)
			require.NoError(t, err)
			require.False(t, claim.matches(replacement), "the old claim is actually superseded, even when generation is unchanged")
			require.True(t, al.tsExecutionClaim(current, "").matches(replacement))
			require.True(t, current.IsAlive(), "new process is held before its own completion")
			_, _, err = al.drainSteeredTurn(context.Background(), snapshot, held, turnResult{}, nil)
			require.NoError(t, err)
			assert.Zero(t, transport.finalizeCalled, "A4: stale held done must not reach the session-wide transport while the NEW live turn is working")
			assert.Same(t, current, al.getActiveTurnState(child.SessionID), "the replacement remains the active turn")
			assert.True(t, current.IsAlive(), "only the replacement's own completion may end its live turn")
			driver.finishResume()
			opencodeFixJoin(t, al)
		})
	}
}
