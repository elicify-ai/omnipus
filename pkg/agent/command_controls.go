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
	if scope == "tree" && al.GetSessionLifecycleStore() == nil {
		// A person asked for Stop all; without the durable tree the helpers
		// cannot be reached, so say so instead of stopping only this turn.
		return false, false, fmt.Errorf("tree-scoped Stop all is unavailable; nothing was stopped")
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

// errNotHelperSession marks a session with no helper edge (an ordinary chat
// or a chat with no lifecycle record): /stop-redirect redirects it as a chat.
var errNotHelperSession = errors.New("not a helper session")

// helperSessionRecord resolves only a real durable edge. Genuine absence is
// errNotHelperSession; corruption and other read failures are visible errors.
func (al *AgentLoop) helperSessionRecord(sessionID string) (*session.LifecycleRecord, error) {
	if al == nil || strings.TrimSpace(sessionID) == "" {
		return nil, errNotHelperSession
	}
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return nil, fmt.Errorf("redirect identity could not be read: lifecycle store is unavailable")
	}
	rec, err := lifecycle.Load(sessionID)
	if errors.Is(err, session.ErrLifecycleNotFound) {
		return nil, errNotHelperSession
	}
	if err != nil {
		return nil, fmt.Errorf("read redirect identity: %w", err)
	}
	if rec == nil || rec.SteeredBy == nil || strings.TrimSpace(rec.SteeredBy.SteeringSessionID) == "" {
		return nil, errNotHelperSession
	}
	return rec, nil
}

// RedirectSessionTurn is /stop-redirect on any session the caller is in
// (founder decision 2026-10-06). A helper keeps D9's redirect: the existing
// refusal of in-flight fences and the same-generation stopped resume. An
// ordinary chat is stopped and continued with the instruction as its next
// user message (redirectOrdinarySession).
func (al *AgentLoop) RedirectSessionTurn(ctx context.Context, sessionID, instruction, userID, channel string) error {
	rec, err := al.helperSessionRecord(sessionID)
	if errors.Is(err, errNotHelperSession) {
		return al.redirectOrdinarySession(ctx, sessionID, instruction, userID, channel, "")
	}
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
		revived, err := al.reviveStoppedSession(ctx, sessionID, by, instruction, true)
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
