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

// RequestScopedCancelForSession is the command surfaces' entry to the one
// Stop (stop_session.go::StopSession) with the explicitly requested scope.
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
	res, err := al.StopSession(ctx, StopRequest{
		SessionID: sessionID,
		By:        steer.Principal{Kind: steer.PrincipalKindHuman, ID: userID},
		Channel:   channel,
		Tree:      scope == "tree",
	})
	if err != nil {
		return false, false, err
	}
	var failures []error
	if res.RootErr != nil {
		failures = append(failures, res.RootErr)
	}
	for _, unreachable := range res.Report.Unreachable {
		failures = append(failures, fmt.Errorf("Stop for session %s failed: %s", unreachable.ID, unreachable.Reason))
	}
	if res.BackgroundFailed != 0 {
		failures = append(failures, fmt.Errorf("Stop could not terminate %d background sessions", res.BackgroundFailed))
	}
	if err := errors.Join(failures...); err != nil {
		return res.Fired, false, err
	}
	return res.Fired, res.Armed, nil
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
