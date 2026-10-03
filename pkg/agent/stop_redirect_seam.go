package agent

// stop_redirect_seam.go — the primitive-argument seam the D9 chat commands
// (/stop, /stop-redirect) and the channels redirect sibling hang on
// (ADR-20260928 sub-agent control plane, D9/D2; architect seam ruling
// §1.1/§1.3). Primitive types only: pkg/commands and pkg/channels consume
// these methods through their own local interfaces (commands.AgentLoopInterface,
// channels.CancelInterceptor) and must not import pkg/agent.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// ErrRedirectNotWired is the VISIBLE DEPENDENCY marker returned by
// RedirectSessionTurn until Backend-CP's D2 composition lands: the redirect
// sequence (ledger intent + current-generation Stop fence + stop_note cause
// redirect_pause under the record lock → interrupt the live turn →
// same-generation RESUME with the instruction) does not exist yet, and a
// silent no-op here would let /stop-redirect report a redirect that never
// happened. Every caller surfaces this error to the user as a real failure;
// nothing is fenced, interrupted or resumed on this path.
var ErrRedirectNotWired = errors.New("redirect is not available yet: the D2 redirect composition (stop fence + interrupt + same-generation resume) is not implemented in this build")

// stopTurnLatchExpiredHook is the OnLatchExpired hook shared by this file's
// RequestCancel legs: an armed stop that ages out unconsumed must be visible
// somewhere, exactly like RequestCancelForSession's own hook — a caller that
// honestly reported "acknowledged, pending" otherwise has nothing to fall
// back on when the latch expires before any turn consumes it.
func stopTurnLatchExpiredHook(sessionID string) func(CancelScope, CancelCanceller) {
	return func(scope CancelScope, canceller CancelCanceller) {
		slog.Warn("agent: StopSessionTurn: pre-registration cancel latch expired unconsumed — the stop this call acknowledged never actually took effect",
			"session_id", sessionID,
			"canceller_user", canceller.UserID,
			"canceller_channel", canceller.Channel,
		)
	}
}

// StopSessionTurn stops ONLY the named session's current turn — the
// commands.AgentLoopInterface seam behind D9 /stop (scope session, no
// cascade; non-terminal; never ends a goal; an open owner question stays
// open). It delegates to the SAME chain the gateway's single Stop press uses
// (websocket_stop_scope.go::requestScopedStop with stopAll=false): for a
// session with a durable lifecycle record, SteerCanceller.StopTurns with
// subtree=false; for an ordinary legacy chat with no record, the
// single-turn RequestCancel(TurnOnly) path — never a divergent second stop
// implementation (T21/T27 own the equivalence; this adapter changes no
// semantics, it only carries primitive arguments).
//
// Three-outcome contract (mirrors RequestCancelForSession):
//   - (fired=true, armed=false, nil)  — stop fired.
//   - (fired=false, armed=true, nil)  — pre-registration cancel latch armed.
//   - (fired=false, armed=false, nil) — nothing to stop (includes a
//     terminal done/failed target: nothing is running to stop).
//   - err non-nil — a real failure (unreadable lifecycle record, cancel
//     state-machine error, unreachable stop) that callers must surface.
func (al *AgentLoop) StopSessionTurn(ctx context.Context, sessionID, userID, channel string) (fired bool, armed bool, err error) {
	if sessionID == "" {
		return false, false, fmt.Errorf("StopSessionTurn: sessionID must not be empty")
	}

	// The stopTurn adapter mirrors the gateway's stopTurn closure in
	// requestScopedStop: one non-terminal RequestCancel(TurnOnly, Generation)
	// per stamped session — capturing the outcome of the NAMED session as
	// the root outcome, exactly as the gateway does — with the same never-ran
	// fallback to SteerGenerationCancel (queue-position cleanup + never-ran
	// landing). No wsConn-bound hooks here: the chat-command surface has no
	// websocket connection to publish stage frames to, exactly like
	// RequestCancelForSession. (TurnOnly also keeps
	// CancelHooks.CancelPendingApprovals out of the picture on both paths —
	// RequestCancel only invokes it when !TurnOnly.)
	var rootOutcome CancelOutcome
	var rootErr error
	var rootSeen bool
	stopTurn := func(ctx context.Context, id string, generation int) (GenerationCancelResult, error) {
		outcome, err := al.RequestCancel(ctx,
			CancelScope{SessionID: id, TurnOnly: true, Generation: generation},
			CancelCanceller{UserID: userID, Channel: channel},
			CancelHooks{
				KillBackgroundSessions: killBackgroundSessionsForCancelSurface,
				OnLatchExpired:         stopTurnLatchExpiredHook(id),
			},
		)
		if id == sessionID {
			rootOutcome, rootErr, rootSeen = outcome, err, true
		}
		result := GenerationCancelResult{
			Found:                  outcome.Fired || outcome.Armed,
			Cancelled:              outcome.Fired,
			SkippedNewerGeneration: outcome.SkippedNewerGeneration,
		}
		if err == nil && !result.Found {
			fallback, fbErr := al.SteerGenerationCancel(ctx, id, generation)
			if fbErr != nil {
				return result, fbErr
			}
			result = fallback
		}
		return result, err
	}

	store := al.GetSessionLifecycleStore()
	if store != nil {
		_, loadErr := store.Load(sessionID)
		switch {
		case loadErr == nil:
			// Session with a durable lifecycle record — the same
			// StopTurns(subtree=false) chain the gateway's single Stop
			// press runs for recorded sessions.
			if c := al.steerCanceller(); c != nil {
				report, stopErr := c.StopTurns(ctx, sessionID,
					steer.Principal{Kind: steer.PrincipalKindHuman, ID: userID},
					false, stopTurn)
				if rootErr != nil {
					return rootOutcome.Fired, rootOutcome.Armed, rootErr
				}
				if !rootSeen {
					// The root was never stamped — a terminal done/failed
					// target is skipped by the cascade (errCascadeTerminal):
					// the truthful "nothing to stop" outcome.
					return false, false, nil
				}
				if len(report.Unreachable) > 0 {
					return rootOutcome.Fired, rootOutcome.Armed,
						fmt.Errorf("stop did not fully take effect: %s", report.Unreachable[0].Reason)
				}
				return rootOutcome.Fired, rootOutcome.Armed, stopErr
			}
		case errors.Is(loadErr, session.ErrLifecycleNotFound):
			// Ordinary legacy chat (no lifecycle record) — the same
			// single-turn chain the gateway falls back to
			// (requestScopedStop's !durable branch → requestTurnStop).
		default:
			// Unreadable record: refuse visibly rather than stop blind.
			return false, false, fmt.Errorf("stop: lifecycle record unreadable: %w", loadErr)
		}
	}

	outcome, err := al.RequestCancel(ctx,
		CancelScope{SessionID: sessionID, TurnOnly: true},
		CancelCanceller{UserID: userID, Channel: channel},
		CancelHooks{
			KillBackgroundSessions: killBackgroundSessionsForCancelSurface,
			OnLatchExpired:         stopTurnLatchExpiredHook(sessionID),
		},
	)
	if err != nil {
		return false, false, err
	}
	return outcome.Fired, outcome.Armed, nil
}

// RedirectSessionTurn is the commands.AgentLoopInterface seam behind D9
// /stop-redirect (D2 redirect on the named helper only; its subtree keeps
// working). The D2 composition itself — ledger intent + current-generation
// Stop fence + stop_note cause redirect_pause under the record lock →
// interrupt the live turn → same-generation RESUME with the instruction —
// is Backend-CP's (W2) unit and does NOT exist yet. Until it lands, this
// method returns the visible ErrRedirectNotWired dependency: /stop-redirect
// replies as a real failure and NOTHING is fenced, interrupted or resumed.
// It deliberately does not fake a successful redirect — a no-op success here
// is the exact defect the seam ruling forbids.
//
// Guidance sentinels (commands.ErrNotHelperSession, commands.ErrNothingToRedirect)
// are part of this method's contract for the wired implementation; the
// not-wired dependency error is returned before any of them could apply.
func (al *AgentLoop) RedirectSessionTurn(ctx context.Context, sessionID, instruction, userID, channel string) error {
	if sessionID == "" {
		return fmt.Errorf("RedirectSessionTurn: sessionID must not be empty")
	}
	if strings.TrimSpace(instruction) == "" {
		return fmt.Errorf("RedirectSessionTurn: instruction must not be blank")
	}
	_ = userID
	_ = channel
	return ErrRedirectNotWired
}

// RequestRedirectByChannelChat is the channels.CancelInterceptor sibling of
// RequestCancelByChannelChat (architect seam ruling §3.1 channel row): it
// resolves the session for (channelName, chatID) the same way RequestCancel
// does internally, then runs the same RedirectSessionTurn primitive — one
// redirect implementation, never a divergent second path. Until the D2
// composition lands, callers receive the visible ErrRedirectNotWired
// dependency through RedirectSessionTurn.
func (al *AgentLoop) RequestRedirectByChannelChat(ctx context.Context, channelName, chatID, userID, instruction string) error {
	if channelName == "" || chatID == "" {
		return fmt.Errorf("RequestRedirectByChannelChat: channel and chatID must not be empty")
	}
	sessionID := al.resolveSessionIDByChannelChat(channelName, chatID)
	if sessionID == "" {
		return fmt.Errorf("RequestRedirectByChannelChat: no active session for %s/%s", channelName, chatID)
	}
	return al.RedirectSessionTurn(ctx, sessionID, instruction, userID, channelName)
}

// sessionIsHelper reports whether sessionID targets a helper (steered)
// session: a readable lifecycle record whose SteeredBy edge is set
// (pkg/session/lifecycle_edge.go::SteeredBy is nil on non-steered sessions).
//
// FAIL-CLOSED (architect seam ruling §3.2): unresolvable identity — blank
// session id, no lifecycle store, no lifecycle record (an ordinary root
// chat), or an unreadable store — returns FALSE, so /stop-redirect gets the
// root-style refusal and nothing is redirected. Only a positively verified
// steering edge counts as helper.
func (al *AgentLoop) sessionIsHelper(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	store := al.GetSessionLifecycleStore()
	if store == nil {
		return false
	}
	rec, err := store.Load(sessionID)
	if err != nil {
		return false
	}
	return rec != nil && rec.SteeredBy != nil
}
