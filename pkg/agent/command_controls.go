// command_controls.go keeps D9 chat controls on the existing durable Stop and
// redirect paths. Transport adapters supply an authenticated human identity;
// none of these controls ends a goal or changes an ancestor or sibling.
package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/commands"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

var _ commands.AgentLoopInterface = (*AgentLoop)(nil)
var _ commands.ScopedCancelLoop = (*AgentLoop)(nil)

// StopSessionTurn is the primitive D9 /stop seam: this session, no cascade.
func (al *AgentLoop) StopSessionTurn(ctx context.Context, sessionID, userID, channel string) (bool, bool, error) {
	return al.RequestScopedCancelForSession(ctx, sessionID, userID, channel, "session")
}

// RequestScopedCancelForSession runs StopTurns with the explicitly requested
// scope, using RequestCancel's TurnOnly path for each stamped live effect.
func (al *AgentLoop) RequestScopedCancelForSession(ctx context.Context, sessionID, userID, channel, scope string) (bool, bool, error) {
	if al == nil || strings.TrimSpace(sessionID) == "" {
		return false, false, fmt.Errorf("Stop requires a session")
	}
	if strings.TrimSpace(userID) == "" {
		return false, false, fmt.Errorf("Stop requires an authenticated sender")
	}
	if scope != "session" && scope != "tree" {
		return false, false, fmt.Errorf("Stop scope must be session or tree")
	}
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		if scope == "tree" {
			return false, false, fmt.Errorf("tree-scoped Stop all is unavailable; nothing was stopped")
		}
		outcome, err := al.requestCommandTurnStop(ctx, sessionID, 0, userID, channel)
		return outcome.Fired, outcome.Armed, err
	}
	if _, err := lifecycle.Load(sessionID); err != nil {
		if !errors.Is(err, session.ErrLifecycleNotFound) {
			return false, false, fmt.Errorf("read Stop target: %w", err)
		}
		// An ordinary chat may have no lifecycle record. Prove its tree is
		// empty before treating its own turn as the entire tree; read failures
		// or orphaned children are never hidden by a session-only fallback.
		if scope == "tree" {
			children, walkErr := CollectDescendantSessionIDs(lifecycle, sessionID)
			if walkErr != nil {
				return false, false, fmt.Errorf("resolve Stop-all tree: %w", walkErr)
			}
			if len(children) != 0 {
				return false, false, fmt.Errorf("stop-all target has no lifecycle record; its helpers were not stopped")
			}
		}
		outcome, stopErr := al.requestCommandTurnStop(ctx, sessionID, 0, userID, channel)
		return outcome.Fired, outcome.Armed, stopErr
	}

	var fired, armed bool
	stopTurn := func(ctx context.Context, id string, generation int) (GenerationCancelResult, error) {
		outcome, err := al.requestCommandTurnStop(ctx, id, generation, userID, channel)
		fired = fired || outcome.Fired
		armed = armed || (id == sessionID && outcome.Armed)
		result := GenerationCancelResult{
			Found: outcome.Fired || outcome.Armed, Cancelled: outcome.Fired,
			SkippedNewerGeneration: outcome.SkippedNewerGeneration,
		}
		if err == nil && !result.Found && !result.SkippedNewerGeneration {
			// The same never-ran landing the gateway's scoped Stop uses.
			return al.SteerGenerationCancel(ctx, id, generation)
		}
		return result, err
	}
	report, err := al.steerCanceller().StopTurns(ctx, sessionID,
		steer.Principal{Kind: steer.PrincipalKindHuman, ID: userID}, scope == "tree", stopTurn)
	if err != nil {
		return fired, false, err
	}
	var failures []error
	for _, unreachable := range report.Unreachable {
		failures = append(failures, fmt.Errorf("Stop for session %s failed: %s", unreachable.ID, unreachable.Reason))
	}
	if err := errors.Join(failures...); err != nil {
		return fired, false, err
	}
	// A queued-only stop still did work: its durable stop landed even without
	// an active turn. Repeated stops have no Reached entries and are no-ops.
	if !armed {
		fired = fired || len(report.Reached) != 0
	}
	return fired, !fired && armed, nil
}

func (al *AgentLoop) requestCommandTurnStop(ctx context.Context, sessionID string, generation int, userID, channel string) (CancelOutcome, error) {
	outcome, err := al.RequestCancel(ctx,
		CancelScope{SessionID: sessionID, TurnOnly: true, Generation: generation},
		CancelCanceller{UserID: userID, Channel: channel},
		CancelHooks{KillBackgroundSessions: killBackgroundSessionsForCancelSurface})
	if err != nil {
		return outcome, err
	}
	if outcome.BackgroundSessionsFailed != 0 {
		return outcome, fmt.Errorf("Stop could not terminate %d background sessions", outcome.BackgroundSessionsFailed)
	}
	return outcome, nil
}

// helperSessionRecord resolves only a real durable edge. Genuine absence gets
// helper guidance; corruption and other read failures are visible errors.
func (al *AgentLoop) helperSessionRecord(sessionID string) (*session.LifecycleRecord, error) {
	if al == nil || strings.TrimSpace(sessionID) == "" {
		return nil, commands.ErrNotHelperSession
	}
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return nil, fmt.Errorf("redirect identity could not be read: lifecycle store is unavailable")
	}
	rec, err := lifecycle.Load(sessionID)
	if errors.Is(err, session.ErrLifecycleNotFound) {
		return nil, commands.ErrNotHelperSession
	}
	if err != nil {
		return nil, fmt.Errorf("read redirect identity: %w", err)
	}
	if rec == nil || rec.SteeredBy == nil || strings.TrimSpace(rec.SteeredBy.SteeringSessionID) == "" {
		return nil, commands.ErrNotHelperSession
	}
	return rec, nil
}

func (al *AgentLoop) resolveHelperSession(sessionID string) (bool, error) {
	_, err := al.helperSessionRecord(sessionID)
	if errors.Is(err, commands.ErrNotHelperSession) {
		return false, nil
	}
	return err == nil, err
}

// RedirectSessionTurn is D9's helper-only primitive. It preserves the existing
// refusal of in-flight fences and the existing same-generation stopped resume.
func (al *AgentLoop) RedirectSessionTurn(ctx context.Context, sessionID, instruction, userID, channel string) error {
	rec, err := al.helperSessionRecord(sessionID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(userID) == "" {
		return fmt.Errorf("redirect requires an authenticated sender")
	}
	instruction = strings.TrimSpace(instruction)
	if instruction == "" {
		return fmt.Errorf("redirect requires an instruction")
	}
	if rec.Terminal() {
		return commands.ErrNothingToRedirect
	}
	if rec.Is3P {
		return fmt.Errorf("not_steerable: this external helper cannot redirect its live turn; use Stop all or RESUME")
	}
	by := steer.Principal{Kind: steer.PrincipalKindHuman, ID: userID}
	if rec.Stopped() {
		// ReviveStoppedSession refuses the in-flight fence before writing any
		// instruction; only a landed stopped session may resume here.
		revived, err := al.ReviveStoppedSession(ctx, sessionID, by, instruction)
		if err != nil {
			return err
		}
		if !revived {
			return fmt.Errorf("redirect instruction was not applied; the helper is already running again")
		}
		return nil
	}
	return al.RedirectSteeredSession(ctx, sessionID, by, instruction)
}
