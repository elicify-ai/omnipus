package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// resumeRestartHelper admits only a native helper whose lasting state is a
// restart stop. It uses the normal revival and launcher, not a running stamp.
func (r *SteerBootRecovery) resumeRestartHelper(ctx context.Context, stopped *session.LifecycleRecord, notice func(string, string)) bool {
	if stopped.State != session.LifecycleStopped || stopped.StopNote == nil ||
		stopped.StopNote.Cause != session.StopCauseRestart || stopped.Is3P {
		return false
	}
	if stopped.Origin != nil && stopped.Origin.Kind == session.OriginKindTask {
		// A create_task run has its own restart rule: boot reconciliation
		// marks the task itself interrupted/failed (rest_tasks.go
		// reconcileStuckTasks), and its session stays stopped(restart). Resuming
		// the session would split the task (failed) from its session (working).
		// PW-D6 resumes delegated native helpers only.
		return false
	}
	al, err := r.noticeLoop()
	if err == nil {
		var pending bool
		pending, err = r.acceptedStopPending(stopped)
		if pending {
			return false
		}
	}
	if err == nil {
		if _, ok := al.GetRegistry().GetAgent(stopped.AgentID); !ok {
			err = fmt.Errorf("restart resume: agent %q is not available", stopped.AgentID)
		}
	}
	if err == nil {
		err = restartResumeWorkspaceRefusal(stopped.AgentID, stopped.WorkspaceID)
	}
	var generation int
	if err == nil {
		generation, err = al.steerCanceller().reviveWithExecution(context.WithValue(ctx, restartWakeRevivalKey{}, true), stopped.SessionID,
			steer.Principal{Kind: steer.PrincipalKindAgent, ID: "restart-recovery"}, stopped, nil)
	}
	if err == nil {
		launcher := al.getSteerSessionLauncher()
		if launcher == nil {
			// The gateway wires exactly NewSteerLauncher(agentLoop), which is
			// stateless; the loop's own instance is the same launch primitive,
			// not a substitute worker, so resume never depends on wiring order.
			launcher = NewSteerLauncher(al)
		}
		_, err = launcher.Dispatch(ctx, stopped.SessionID, generation)
	}
	if err == nil {
		return true
	}
	// A failed admission must not leave the helper looking queued/running.
	// A newer Stop or execution still owns its state and is never rolled back.
	if generation != 0 {
		rollbackErr := r.Lifecycle.Mutate(stopped.SessionID, func(current *session.LifecycleRecord) error {
			if current.Generation != stopped.Generation || current.State == session.LifecycleStopped || lifecycleInFlightStopFence(current) {
				return errRestartStopUnchanged
			}
			pending, checkErr := r.Lifecycle.UnfinishedStopIntentsLocked(current.SessionID)
			if checkErr != nil {
				return checkErr
			}
			if acceptedStopSelectsExecution(current, pending) {
				return errRestartStopUnchanged
			}
			current.State, current.Stop, current.StopNote = session.LifecycleStopped, nil, stopped.StopNote
			current.ExecutionID, current.StopEffect = stopped.ExecutionID, stopped.StopEffect
			return nil
		})
		if rollbackErr != nil && !errors.Is(rollbackErr, errRestartStopUnchanged) {
			err = errors.Join(err, rollbackErr)
		}
	}
	notice("resume-refused:"+stopped.SessionID, fmt.Sprintf("helper %s resume did not start: %v", stopped.SessionID, err))
	if writeErr := r.recordResumeRefused(stopped); writeErr != nil {
		notice("resume-refused-row:"+stopped.SessionID, fmt.Sprintf("helper %s resume refusal row could not be saved: %v", stopped.SessionID, writeErr))
	}
	return false
}

func (r *SteerBootRecovery) recordResumeRefused(rec *session.LifecycleRecord) error {
	id := stoppedChildNoticeID(rec.SteeringSessionID(), rec.SessionID, rec.Generation, rec.StopNote.Seq)
	event := generated.HelperEvent{HelperSessionId: rec.SessionID, Kind: generated.HelperEventKindResumeRefused,
		Text: "Resume did not start.", CreatedAt: rec.StopNote.At.UTC()}
	var errs []error
	for _, destination := range []string{rec.SessionID, rec.SteeringSessionID()} {
		if destination == "" {
			continue
		}
		if al, err := r.noticeLoop(); err == nil {
			if err := al.retainHelperEvent(rec.SessionID, destination, "resume-refused:"+id, event); err != nil {
				errs = append(errs, err)
			}
		} else {
			// A missing runtime can still save the static refusal through the
			// real boot stores. No alternate worker or fake admission is made.
			_, writeErr := r.Sessions.AppendTranscriptOnce(destination, session.TranscriptEntry{
				ID: helperEventMessageID(destination, "resume-refused:"+id, event.Kind), Type: session.EntryTypeSystem,
				Role: "system", Content: event.Text, AgentID: rec.AgentID, Timestamp: event.CreatedAt,
				SystemSubtype: "helper_event", HelperEvent: &event,
			})
			if writeErr != nil {
				errs = append(errs, writeErr)
			}
		}
	}
	return errors.Join(errs...)
}

// restartHistoryResume distinguishes a continuation from first launch without
// a second resume format: the landed history already preserves the transition.
func (al *AgentLoop) restartHistoryResume(rec *session.LifecycleRecord) (bool, error) {
	if rec.SteeredBy == nil {
		return false, nil
	}
	transitions, err := al.GetSessionLifecycleStore().ListStoppedTransitions(rec.SessionID)
	if err != nil {
		return false, err
	}
	for i := len(transitions) - 1; i >= 0; i-- {
		tr := transitions[i]
		if tr.Generation == rec.Generation {
			return tr.Cause == session.StopCauseRestart, nil
		}
	}
	return false, nil
}

// restartResumeWorkspaceRefusal (PW-SEC-01) refuses an automatic resume whose
// saved work would no longer execute in its own recorded workspace. Turn
// rooting falls back to another membership when the preferred one is gone,
// so a helper removed from workspace A but still in B would continue A's
// saved work in B's files. No person asked for that; the refusal is visible
// (resume_refused row) and the parent is still told about the restart.
func restartResumeWorkspaceRefusal(agentID, workspaceID string) error {
	if strings.TrimSpace(workspaceID) == "" {
		return nil
	}
	home := omnipusHome()
	if !workspace.Exists(home, workspaceID) {
		// A deleted workspace holds no authority to continue in: refuse rather
		// than let turn rooting fall back to another membership.
		return fmt.Errorf("restart resume: workspace %q no longer exists; refusing to continue its saved work elsewhere", workspaceID)
	}
	resolved, ok := workspace.FindForAgentPreferring(home, agentID, workspaceID)
	if ok && resolved == workspaceID {
		return nil
	}
	return fmt.Errorf("restart resume: agent %q no longer runs in workspace %q (would run in %q); refusing to continue its saved work elsewhere",
		agentID, workspaceID, resolved)
}
