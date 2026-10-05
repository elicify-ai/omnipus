// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-093 D4 — an open conversation keeps its delegation. The ordinary
// inbound-turn admission path (processMessage) admits the turn through a
// revival check: when the message's chat's own lifecycle record is terminal
// or durably stopped at its current generation, the record is revived (new
// generation via resumed_from, terminal history immutable) BEFORE any tool
// runs, so a delegate call in that very turn succeeds.
//
// What never passes through here: system wakes — processSystemMessage calls
// runAgentLoop directly and never reaches processMessage's tail — and every
// other runAgentLoop caller. The wrapper is hooked only into processMessage,
// the human-message path, which is what keeps MIN-004 ("system wakes never
// revive") true structurally: a delegate call in a system wake's turn hits
// D2's launch backstop instead.

package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// reviveInboundIsHumanTurn is MIN-004's human predicate: only an ordinary
// human message can revive. A system wake arrives with channel "system"
// and/or the steer-wake metadata key
// (loop_inbound.go::processSystemMessage's own dispatch key) and is excluded.
func reviveInboundIsHumanTurn(msg bus.InboundMessage) bool {
	if msg.Channel == "system" {
		return false
	}
	return msg.Metadata["steer_message_id"] == ""
}

// lifecycleInFlightStopFence reports whether rec carries a stop fence for
// its CURRENT generation while the record is still live — not terminal and
// not LifecycleStopped (i.e. queued, running, or needs_input). This is the
// dying-turn shape LifecycleRecord.Stopped() also reports true for: the stop
// has been stamped but the turn it targets has not exited yet, so admission
// still refuses any new work in the session's name (the delegate
// steer/respond closures refuse it, SteerLauncher.Dispatch refuses it).
// Reviving this shape would clear the fence, mark the record queued and
// clear its ExecutionID, then strand a helper nobody runs; running a fresh
// turn on it would start a SECOND turn on the dying session. A landed
// LifecycleStopped record and a terminal record are never this shape — both
// stay revivable.
func lifecycleInFlightStopFence(rec *session.LifecycleRecord) bool {
	if rec == nil || rec.Stop == nil {
		return false
	}
	if rec.Terminal() || rec.State == session.LifecycleStopped {
		return false
	}
	return rec.Stop.Generation == rec.Generation
}

// inboundStopFenceInFlight returns a visible error when msg's session's own
// lifecycle record carries an in-flight stop fence
// (lifecycleInFlightStopFence) — the one shape the ordinary inbound-turn
// admission must neither revive nor run a second turn on
// (runInboundTurnWithRevival). A blank id, a missing store, a gone record
// (ErrLifecycleNotFound) and every non-fence shape are nil: the revivable
// check and the plain turn keep their existing behavior. An unreadable
// record — a read failure other than not-found — is returned wrapped:
// runInboundTurnWithRevival refuses the message with it, a visible failure
// the sender can retry, instead of proceeding on a fence check that never
// ran.
func (al *AgentLoop) inboundStopFenceInFlight(sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	store := al.GetSessionLifecycleStore()
	if sessionID == "" || store == nil {
		return nil
	}
	rec, err := store.Load(sessionID)
	if err != nil {
		if errors.Is(err, session.ErrLifecycleNotFound) {
			return nil // no record: no fence can be in flight
		}
		return fmt.Errorf("steer: inbound stop-fence check: load session %q: %w", sessionID, err)
	}
	if !lifecycleInFlightStopFence(rec) {
		return nil
	}
	// curatedTurnError, not a plain error: the text is Omnipus-authored and
	// provider-free, so the session worker publishes it as written
	// (translate_error.go::userVisibleTurnError) instead of the generic
	// "can't tell why" sentence the classifier would give an untyped error.
	return &curatedTurnError{text: fmt.Sprintf("session %s is stopping (a stop is in flight for its current generation); retry once the stop has landed", sessionID)}
}

// inboundRevivable reports whether sessionID's lifecycle record is terminal
// or has LANDED LifecycleStopped — the two states ADR-093 D4 lets an
// ordinary human message revive. An in-flight stop fence (Stopped() is also
// true for that shape: a Stop stamped for the current generation while the
// state is still live) is NOT revivable here — the dying turn is still
// registered, and inboundStopFenceInFlight refuses it upstream with a
// visible error. No record (ErrLifecycleNotFound), a blank id or a missing
// store is not revivable: nothing to revive, nothing to guess about. Any
// OTHER Load error is a real read failure: it is logged at error level with
// the session id and still reads as "not revivable" (the turn must run),
// but the log keeps a broken record from being indistinguishable from a
// healthy one afterwards (gate SFH#2).
func (al *AgentLoop) inboundRevivable(sessionID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	store := al.GetSessionLifecycleStore()
	if sessionID == "" || store == nil {
		return false
	}
	rec, err := store.Load(sessionID)
	if err != nil {
		if !errors.Is(err, session.ErrLifecycleNotFound) {
			logger.ErrorCF("agent", "adr093: inbound revival check could not read the lifecycle record",
				map[string]any{"session_id": sessionID, "error": err.Error()})
		}
		return false
	}
	// Terminal OR landed LifecycleStopped only — deliberately not Stopped():
	// Stopped() is also true for an in-flight fence, and that shape must
	// never read as revivable (see lifecycleInFlightStopFence).
	return rec.Terminal() || rec.State == session.LifecycleStopped
}

// reviveRecordForHumanTurn revives a terminal or durably-stopped record to a
// new generation — SteerCanceller.Revive's existing shape, unchanged
// (generation+1, resumed_from = the session's own id, running, failed reason
// cleared, the older Stop kept as inert history) — and resets the session's
// UnifiedMeta.Status to active in the same revival (ADR-093 D4 / MIN-002:
// SteerCanceller.Revive has no UnifiedStore, so the AgentLoop wrapper does
// it). No dispatch lives here: whether the revived session runs an ordinary
// inbound turn (processMessage) or is redispatched as steered
// (ReviveStoppedSession) is the caller's routing decision.
func (al *AgentLoop) reviveRecordForHumanTurn(ctx context.Context, sessionID string, by steer.Principal) error {
	if _, err := al.steerCanceller().Revive(ctx, sessionID, by); err != nil {
		al.markRevivalFailure(sessionID, err)
		return err
	}
	// The record is live on its new generation from here — drop any stale
	// revival-failure memory so a later, genuine stop-refusal is not
	// mislabelled as a revival failure (gate SFH#6).
	al.clearRevivalFailure(sessionID)
	al.resetUnifiedMetaStatusActive(sessionID)
	return nil
}

// resetUnifiedMetaStatusActive is the shared post-revive session-list status
// reset (ADR-093 MIN-002), used by the human-message path
// (reviveRecordForHumanTurn) and the child-revive path (steering.go::
// ReviveStoppedSession). It never reports success falsely (gate SFH#4): a
// failed SetMeta or a missing store is error-logged with the session id — but
// not returned as an error, because every caller has already completed the
// actual revive by the time this runs; returning the failure here would make
// runInboundTurnWithRevival log the FALSE claim that the revive failed and
// the turn runs unrevived when the record is in fact live on its new
// generation. reconcileUnifiedMetaStatus (boot_sweep.go) repairs a
// still-stale list status on the next boot.
func (al *AgentLoop) resetUnifiedMetaStatusActive(sessionID string) {
	if store := al.ResolveSessionStore(sessionID); store != nil {
		active := session.StatusActive
		if err := store.SetMeta(sessionID, session.MetaPatch{Status: &active}); err != nil {
			logger.ErrorCF("agent", "adr093: record revived, but resetting the session-list status to active failed",
				map[string]any{"session_id": sessionID, "error": err.Error()})
		}
		return
	}
	logger.ErrorCF("agent", "adr093: record revived, but no session store owns this session — the session-list status was not reset to active",
		map[string]any{"session_id": sessionID})
}

// runInboundTurnWithRevival prepares a genuine lifecycle-backed human
// admission before constructing its ordinary turn. Stopped resumes retain the
// generation; terminal follow-ups use Revive's next generation. Preparation
// errors are visible and never fall through to an unstamped execution.
// A session worker retains ownership through its full drain/output tail;
// a direct caller completes its own disposition after runAgentLoop returns.
func (al *AgentLoop) runInboundTurnWithRevival(
	ctx context.Context,
	agent *AgentInstance,
	msg bus.InboundMessage,
	opts processOptions,
) (resp string, usedAgent *AgentInstance, runErr error) {
	entry := ordinaryExecutionFromContext(ctx)
	directOwner := entry == nil
	if directOwner {
		ctx, entry = ordinaryExecutionContext(ctx)
	}
	preparation, prepErr := al.prepareOrdinaryExecution(ctx, msg, opts)
	if prepErr != nil {
		return "", agent, prepErr
	}
	d := preparation.execution
	entry.disposition = d
	opts.executionDisposition = d
	if directOwner {
		defer func() { runErr = errors.Join(runErr, al.finishExecutionDisposition(d)) }()
	}
	resp, runErr = al.runAgentLoop(ctx, agent, opts)
	return resp, agent, runErr
}
