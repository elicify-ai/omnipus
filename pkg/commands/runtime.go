package commands

import (
	"context"
	"errors"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// ErrNoActiveTurn is returned by CancelActiveTurn when the agent loop reports
// that no turn is currently running for the given session AND no
// pre-registration cancel latch was armed in its place (see ErrCancelArmed).
// Callers should reply with an informational "Nothing to cancel" message
// rather than treating it as a failure.
var ErrNoActiveTurn = errors.New("no active turn")

// ErrCancelArmed is returned by CancelActiveTurn when no turn was registered
// yet for the session, but the agent loop armed a pre-registration cancel
// latch (pkg/agent/cancel_prearm.go) in its place instead of silently
// no-op'ing: the next turn to register for this session will be canceled the
// instant it does (bounded by the loop's cancelPreArmTTL). This is NOT the
// same outcome as ErrNoActiveTurn — reporting it that way is precisely the
// bug this sentinel closes (a user told "nothing to cancel" moments before
// the turn they meant to cancel registers and runs anyway). Callers MUST
// treat it as a THIRD distinct outcome, separate from both a fired cancel
// (nil) and a genuine no-op (ErrNoActiveTurn): acknowledged-and-pending, not
// "canceled" and not "nothing to cancel".
var ErrCancelArmed = errors.New("cancel acknowledged, pending turn registration")

// ErrNotHelperSession is returned by StopSessionTurn's redirect sibling,
// RedirectSessionTurn, when the target session is not a steered helper —
// either the current chat is the workspace root (ADR-20260928 D9 row 3:
// refuse with guidance to target a helper; the root's own /stop still
// works) or helper identity was re-checked server-side and failed
// fail-closed. Callers MUST reply with the helper-targeting guidance
// instead of surfacing it as a transport failure.
var ErrNotHelperSession = errors.New("redirect targets a helper session, not this chat")

// ErrNothingToRedirect is returned by RedirectSessionTurn when the target
// helper has already reached a terminal outcome (done/failed): per D2's
// stop table there is nothing to stop or resume — "already finished — use
// RESUME". This is guidance, not a failure; callers MUST reply with the
// RESUME guidance as a normal (non-error) message.
var ErrNothingToRedirect = errors.New("session already finished — nothing to redirect")

// AgentLoopInterface is a minimal interface for the agent-loop methods needed by
// the commands runtime. Using an interface here avoids a hard import cycle
// between pkg/commands and pkg/agent.
//
// ADR-057 FR-041/D8 note: this interface used to also declare
// InterruptSession(sessionID, hint string) ([]string, error), mirroring the
// pre-collapse agent.AgentLoop.InterruptSession. It was dead surface —
// CancelActiveTurn below has only ever called RequestCancelForSession, and
// no other pkg/commands code referenced it (only pkg/commands/cmd_cancel_test.go's
// stub, for its own internal test-glue reuse). FR-041 collapses the four
// pre-existing agent-side entry points into agent.AgentLoop.Interrupt /
// InterruptSessionHard, both of which take a new agent.InterruptScope
// argument; adding a matching method here would force this package to
// import pkg/agent for that type, reintroducing exactly the import cycle
// this interface exists to avoid. Since the method was unused, the correct
// resolution is removal, not a primitive-typed stand-in for a capability
// nothing calls.
type AgentLoopInterface interface {
	// RequestCancelForSession runs the full cancel state machine (audit, transcript,
	// abuse-detection, approval auto-deny, 2-stage timer) for the given session.
	// All parameters are primitive types so this interface can be defined without
	// importing pkg/agent (which would create a circular dependency).
	//
	// Returns (fired, armed, err):
	//   - fired is true when an active turn was successfully claimed.
	//   - armed is true when fired is false because no turn was registered yet
	//     and a pre-registration cancel latch was recorded in its place instead
	//     (see agent.CancelOutcome.Armed's doc comment for the full contract).
	//     armed is never true when fired is true.
	//   - err is non-nil only for validation failures.
	//
	// CancelActiveTurn depends on armed being carried all the way through —
	// discarding it here (flattening back to a bare bool) reintroduces the
	// exact bug this signature was widened to fix: an armed cancel silently
	// reported as ErrNoActiveTurn.
	RequestCancelForSession(ctx context.Context, sessionID, userID, channel string) (fired bool, armed bool, err error)

	// StopSessionTurn stops ONLY the named session's current turn — ADR-20260928
	// D9 /stop (scope session, no cascade; D2 stop semantics: non-terminal,
	// never ends a goal, keeps an open question open). It must delegate to the
	// SAME StopTurns(subtree=false) chain the gateway's single Stop press uses
	// — one shared stop implementation, never a divergent second path.
	// Primitive types only, per this interface's no-pkg/agent-import constraint.
	//
	// Three-outcome contract identical to RequestCancelForSession:
	//   (fired=true, armed=false, nil)  — stop fired.
	//   (fired=false, armed=true, nil)  — pre-registration latch armed.
	//   (fired=false, armed=false, nil) — nothing to stop.
	//   err non-nil                     — real failure, must surface.
	StopSessionTurn(ctx context.Context, sessionID, userID, channel string) (fired, armed bool, err error)

	// RedirectSessionTurn performs the D2 redirect on the named session:
	// fence + stop_note cause redirect_pause under lock, interrupt the live
	// turn (skipped when already stopped), same-generation resume with
	// instruction as the newest message; its subtree keeps working. Returns
	// named sentinel errors for guidance outcomes — ErrNotHelperSession
	// (root-style refusal with guidance), ErrNothingToRedirect (already
	// finished → "use RESUME") — so handlers reply truthfully instead of one
	// opaque error (mirrors ErrNoActiveTurn/ErrCancelArmed). Primitive types
	// only, per this interface's no-pkg/agent-import constraint.
	RedirectSessionTurn(ctx context.Context, sessionID, instruction, userID, channel string) error
}

// Canceller identifies who/what issued a cancel — populated for audit attribution.
type Canceller struct {
	UserID  string // user-side identity, e.g., "@bob" or "user_abc123"
	Channel string // factory ID, e.g., "telegram" | "slack" | "web" | "cli"
}

// Runtime provides runtime dependencies to command handlers. It is constructed
// per-request by the agent loop so that per-request state (like session scope)
// can coexist with long-lived callbacks (like GetModelInfo).
type Runtime struct {
	Config             *config.Config
	GetModelInfo       func() (name, provider string)
	ListAgentIDs       func() []string
	ListDefinitions    func() []Definition
	ListSkillNames     func() []string
	GetEnabledChannels func() []string
	GetActiveTurn      func() any // Returning any to avoid circular dependency with agent package
	SwitchModel        func(value string) (oldModel string, err error)
	SwitchChannel      func(value string) error
	ClearHistory       func() error
	ReloadConfig       func() error
	// SessionID returns the session key for the current request context. Used by
	// handlers that need to address a specific session (e.g., /cancel).
	SessionID func() string
	// IsHelperSession reports whether the current chat targets a helper
	// (steered) session — ADR-20260928 D9's root-vs-helper gate for
	// /stop-redirect. Fail-closed (seam ruling §3.2): unresolvable identity —
	// no lifecycle record, unreadable store, blank session id — is FALSE, so
	// /stop-redirect gets the root-style refusal and nothing is redirected.
	// false ⇒ workspace root. Populated by the agent loop via
	// buildCommandsRuntime; nil behaves as false for the same fail-closed
	// reason.
	IsHelperSession func() bool
	// agentLoop is the agent loop implementation used by CancelActiveTurn.
	// Populated by the agent loop via buildCommandsRuntime.
	agentLoop AgentLoopInterface
}

// CancelActiveTurn runs the full cancel state machine (audit, transcript,
// abuse-detection, 2-stage timer) for the given session via the centralized
// RequestCancelForSession entry point. The canceller fields are used for audit
// attribution.
//
// Return values:
//   - nil             — cancel fired.
//   - ErrCancelArmed  — no turn was registered yet, but a pre-registration
//     cancel latch now stands in for this cancel and will fire the instant
//     one registers. NOT the same as "nothing to cancel" — see ErrCancelArmed.
//   - ErrNoActiveTurn — genuinely nothing to cancel: no running turn, and no
//     latch was armed either.
//   - other error     — a real failure that the caller must surface.
func (rt *Runtime) CancelActiveTurn(ctx context.Context, sessionID string, canceller Canceller) error {
	if rt == nil || rt.agentLoop == nil {
		// No agent loop wired — treat as "nothing to cancel".
		return ErrNoActiveTurn
	}
	fired, armed, err := rt.agentLoop.RequestCancelForSession(ctx, sessionID, canceller.UserID, canceller.Channel)
	if err != nil {
		return fmt.Errorf("cancel: %w", err)
	}
	if fired {
		return nil
	}
	if armed {
		// Do NOT collapse this into ErrNoActiveTurn: a latch now stands in for
		// this cancel and WILL fire against the next turn to register for
		// this session (bounded by the loop's cancelPreArmTTL) — reporting it
		// as "nothing to cancel" is the exact bug CancelOutcome.Armed's doc
		// comment warns every surfacing caller against.
		return ErrCancelArmed
	}
	return ErrNoActiveTurn
}

// WithAgentLoop returns a shallow copy of rt with agentLoop set. Used by the
// agent loop's buildCommandsRuntime to inject the loop reference without
// exporting the field directly.
func (rt *Runtime) WithAgentLoop(al AgentLoopInterface) *Runtime {
	clone := *rt
	clone.agentLoop = al
	return &clone
}
