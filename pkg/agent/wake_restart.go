package agent

import (
	"context"
	"errors"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

type restartWakeRevivalKey struct{}

// reopenRestartWakeRecipient implements only PW-D14. The source is a fresh
// helper outcome correlated to the same writing boot, not an anonymous wake.
// It releases the restart stop before publication; the stopped-root receiver
// remains strict, and the interrupted parent instruction is never replayed.
func (al *AgentLoop) reopenRestartWakeRecipient(ctx context.Context, parent *session.LifecycleRecord) (*session.LifecycleRecord, error) {
	if parent.SteeredBy != nil || parent.State != session.LifecycleStopped || parent.StopNote == nil ||
		parent.StopNote.Cause != session.StopCauseRestart {
		return parent, nil
	}
	source, ok := ctx.Value(wakeSourceKey{}).(wakeSource)
	if !ok || source.childID == "" {
		return parent, nil
	}
	lifecycle := al.GetSessionLifecycleStore()
	child, err := lifecycle.Load(source.childID)
	if err != nil {
		return parent, err
	}
	if child.SteeringSessionID() != parent.SessionID {
		return parent, nil
	}
	boot := source.restartBootSeq
	if boot == 0 && child.ExecutionID != nil {
		boot = child.ExecutionID.BootSeq
	}
	if boot == 0 || boot != parent.StopNote.BootSeq {
		return parent, nil
	}
	transitions, err := lifecycle.ListStoppedTransitions(child.SessionID)
	if err != nil {
		return parent, err
	}
	matched := false
	for i := len(transitions) - 1; i >= 0; i-- {
		tr := transitions[i]
		if tr.Generation == child.Generation {
			matched = tr.Cause == session.StopCauseRestart && tr.ParentSessionID == parent.SessionID
			break
		}
	}
	if !matched {
		return parent, nil
	}
	guarded := context.WithValue(ctx, restartWakeRevivalKey{}, true)
	_, err = al.steerCanceller().reviveWithExecution(guarded, parent.SessionID,
		steer.Principal{Kind: steer.PrincipalKindAgent, ID: "helper-restart-outcome"}, parent, nil)
	if errors.Is(err, steer.ErrStaleGeneration) || errors.Is(err, session.ErrStopIntentSuperseded) {
		return lifecycle.Load(parent.SessionID)
	}
	if err != nil {
		return parent, err
	}
	return lifecycle.Load(parent.SessionID)
}

// Check under the revival's lifecycle lock. An accepted-but-unlanded Stop
// must win even when its current-generation fence has not been appended yet.
func restartWakeRevivalAllowed(ctx context.Context, lifecycle *session.LifecycleStore, current, selected *session.LifecycleRecord) error {
	guarded, _ := ctx.Value(restartWakeRevivalKey{}).(bool)
	if !guarded {
		return nil
	}
	if selected == nil || current.State != session.LifecycleStopped || current.StopNote == nil ||
		current.StopNote.Cause != session.StopCauseRestart || selected.StopNote == nil ||
		current.StopNote.Seq != selected.StopNote.Seq || current.StopNote.BootSeq != selected.StopNote.BootSeq {
		return steer.ErrStaleGeneration
	}
	intents, err := lifecycle.UnfinishedStopIntentsLocked(current.SessionID)
	if err != nil {
		return err
	}
	if acceptedStopSelectsExecution(current, intents) {
		return session.ErrStopIntentSuperseded
	}
	return nil
}
