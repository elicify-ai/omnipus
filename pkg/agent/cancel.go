// Package agent — cancel.go
//
// RequestCancel is the canonical cancel entry point for all four cancel
// surfaces: web SPA (WebSocket), Tier A /cancel command, Tier B text-parsing
// channels, and the CLI.
//
// All four surfaces call RequestCancel so that audit emission, transcript
// marking, abuse detection, approval auto-deny, and the 2-stage graceful→hard
// timer apply uniformly regardless of how the cancel arrived.
//
// Resolves architect review finding B2.

package agent

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync/atomic"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// CancelScope identifies what to cancel.
// Exactly one of SessionID or (Channel + ChatID) must be set.
//
//   - SessionID is preferred when known (web SPA, CLI, Tier A /cancel).
//   - Channel + ChatID is used by Tier B channels that carry no SessionID;
//     RequestCancel resolves the session internally by walking activeTurnStates.
type CancelScope struct {
	SessionID string // non-empty → cancel the session directly
	Channel   string // Tier B: factory ID, e.g. "telegram"
	ChatID    string // Tier B: platform chat identifier
}

// CancelCanceller is the identity of who issued the cancel. Used for audit
// attribution and abuse detection.
type CancelCanceller struct {
	UserID  string // e.g. "@alice", "user_abc123"
	Channel string // factory ID: "web" | "cli" | "telegram" | "slack" | ...
}

// CancelOutcome is returned to the caller after a cancel attempt.
type CancelOutcome struct {
	// Fired is true if ANY turn sharing the session was actually claimed
	// (ClaimCancel succeeded) and therefore targeted by the cascade below —
	// NOT specifically the root/parent turn. The primary resolution
	// (GetActiveTurnHookForSession) prefers the root when one is claimable,
	// but the claimAnyTurnForSession fallback (see that function's doc
	// comment) can claim a live, never-canceled descendant instead when the
	// root has already fired from an earlier, unrelated cancel or no longer
	// resolves at all. Either way Fired:true means the descendant-
	// cancellation cascade and the turn_canceled transcript/audit write did
	// run; Fired:false means no live, unclaimed turn existed for this
	// session at all (a genuine no-op, e.g. everything already finished).
	Fired       bool     // true if some turn was actually targeted (ClaimCancel succeeded)
	Descendants []string // turn IDs canceled (root/claimed turn + all its sub-turns)
	// TurnID is the ID of whichever turn was actually claimed — the root when
	// GetActiveTurnHookForSession resolved and claimed it, or a descendant's
	// ID when claimAnyTurnForSession's fallback had to claim one instead
	// (root already fired / not found). Empty when Fired is false.
	TurnID string

	// BackgroundSessionsKilled and BackgroundSessionsFailed report the
	// hooks.KillBackgroundSessions cascade's outcome (FR-B10/FR-B11/FR-B14).
	// Populated UNCONDITIONALLY — regardless of Fired — because the cascade
	// itself fires unconditionally too (see the call site's doc comment): a
	// `bash run_in_background=true` job's own turn ends immediately, so the
	// MOST COMMON way a user cancels it is with no active turn left to
	// claim (Fired stays false), and callers (the web WS handler, the
	// scheduled-run deadline watcher) need these counts to give the user/
	// operator feedback even on that no-active-turn path — see
	// pkg/gateway/websocket.go's handleCancel and pkg/gateway/schedules.go's
	// watchDeadline, both of which log/notify on BackgroundSessionsKilled >
	// 0 even when Fired is false.
	BackgroundSessionsKilled int
	BackgroundSessionsFailed int
}

// CancelHooks lets callers inject transport-specific side-effects. All fields
// are optional; nil hooks are silently skipped.
type CancelHooks struct {
	// SendStageFrame is called at each timer stage transition
	// (stage values: "graceful", "hard", "detached").
	SendStageFrame func(sessionID, stage string)

	// CancelPendingApprovals auto-denies pending approvals on the canceled
	// session (FR-7). Called once at graceful stage.
	CancelPendingApprovals func(sessionID, reason string)

	// SetSessionInterrupted updates the session meta.json Status to interrupted.
	// Called once at graceful stage.
	SetSessionInterrupted func(sessionID string)

	// KillBackgroundSessions cascades the cancel to every detached background
	// bash/exec session owned by sessionID (FR-B10/FR-B11, User Story 5:
	// "Canceling a session also stops any background bash work it started").
	//
	// Called ONCE, unconditionally, for any resolvable (non-empty) sessionID
	// — deliberately NOT gated on whether an active turn exists or
	// ClaimCancel succeeds (wasFired). A `bash run_in_background=true` call's
	// own turn ends immediately, so by the time a user cancels the
	// still-running background job there is typically no active turn left
	// to claim; gating this hook on wasFired would silently no-op the entire
	// cascade in exactly that (the most common) case. A session with no
	// background work sees no behavior change — this hook is a no-op in that
	// case, not an error.
	//
	// Returns (killed, failed): killed is the count of background sessions
	// actually killed, failed is the count that were RUNNING and eligible but
	// whose kill call itself failed (the underlying syscall failing, not a
	// benign lost-race — see tools.SessionManager.KillAllForSession's doc
	// comment for that distinction). RequestCancel uses both to (a) emit its
	// own turn.cancel.background_killed audit event — gated on killed>0 ||
	// failed>0 so a cancel that finds nothing to kill does not emit a no-op
	// audit row — carrying background_sessions_failed alongside
	// background_sessions_killed, and (b) thread both counts into the
	// turn_canceled audit event's fields map on the ClaimCancel-gated path.
	// Before the failed count was added, a kill failure was invisible outside
	// a slog.Warn deep in pkg/tools/session.go, uncorrelated with any audit
	// event a security reviewer would actually go looking at — contradicting
	// this very event's own doc comment below.
	KillBackgroundSessions func(sessionID string) (killed, failed int)
}

// RequestCancel is the canonical cancel entry point. All four cancel surfaces
// (web SPA, Tier A /cancel command, Tier B text-parsing channels, CLI) call
// this method.
//
// It performs the entire cancel state machine:
//   - background bash/exec session kill cascade (via hooks.KillBackgroundSessions) —
//     fires UNCONDITIONALLY for any resolvable sessionID, deliberately NOT
//     gated on ClaimCancel/wasFired below (see the inline comment at the call
//     site for the root-cause this closes: a `bash run_in_background=true`
//     call's turn ends immediately, so by the time a user cancels the still-
//     running background job there is no active turn left to claim)
//   - abuse-detection record
//   - ClaimCancel atomic first-cancel-wins check
//   - turn_cancel_attempt audit emission (always, even for no-op cancels)
//   - graceful cascade via InterruptSession / providerCancel
//   - approval auto-deny (via hooks.CancelPendingApprovals)
//   - cancel_stage frame emission (via hooks.SendStageFrame)
//   - session status → interrupted (via hooks.SetSessionInterrupted)
//   - transcript MarkLastEntryTruncated + turn_canceled entry on Finish
//   - turn_canceled audit on Finish
//   - 3s timer → hard abort (InterruptSessionHard)
//   - 5s timer → detached / MarkAbandoned + turn_cancel_stuck audit
//
// Returns:
//   - CancelOutcome{Fired: true, Descendants, TurnID} on a successful claim
//   - CancelOutcome{Fired: false} when no active turn matches OR ClaimCancel
//     found cancelFired==true (double-cancel race)
//   - error only for parameter validation failures (empty scope)
func (al *AgentLoop) RequestCancel(
	ctx context.Context,
	scope CancelScope,
	canceller CancelCanceller,
	hooks CancelHooks,
) (CancelOutcome, error) {
	// --- Validate scope ---
	hasBySession := scope.SessionID != ""
	hasByChannel := scope.Channel != "" && scope.ChatID != ""
	if !hasBySession && !hasByChannel {
		return CancelOutcome{}, fmt.Errorf("RequestCancel: scope must set SessionID or (Channel + ChatID)")
	}

	at := time.Now()
	auditLogger := al.AuditLogger()
	hint := fmt.Sprintf("canceled by %s via %s", canceller.UserID, canceller.Channel)

	// --- Resolve session ID from (channel, chatID) when SessionID is not set (Tier B) ---
	sessionID := scope.SessionID
	if sessionID == "" {
		// Walk activeTurnStates to find the root turn matching (channel, chatID).
		var rootTS *turnState
		al.activeTurnStates.Range(func(_, value any) bool {
			ts := value.(*turnState)
			ts.mu.RLock()
			ch := ts.channel
			cid := ts.chatID
			sid := ts.transcriptSessionID
			depth := ts.depth
			parentID := ts.parentTurnID
			ts.mu.RUnlock()
			if ch == scope.Channel && cid == scope.ChatID && sid != "" {
				// Prefer the root turn (depth==0 / parentTurnID=="").
				if depth == 0 || parentID == "" {
					rootTS = ts
					return false // stop
				}
				if rootTS == nil {
					rootTS = ts
				}
			}
			return true
		})
		if rootTS != nil {
			sessionID = rootTS.transcriptSessionID
		}
	}

	// --- Background-session kill cascade (FR-B10/FR-B11, User Story 5) —
	// decoupled from the active-turn gate ---
	//
	// This MUST NOT be gated behind wasFired/ClaimCancel below. A `bash
	// run_in_background=true` call returns immediately, so its turn — and
	// its activeTurnStates entry — is gone within the same LLM round-trip,
	// well before a user later clicks Cancel to stop the still-running
	// background job. GetActiveTurnHookForSession then finds nothing,
	// wasFired is false, and the old code path early-returned BEFORE ever
	// reaching hooks.KillBackgroundSessions — a silent no-op that left the
	// background process (and its process group) running forever. Root
	// cause confirmed; fix: fire the kill cascade here, unconditionally,
	// whenever sessionID resolved to something non-empty, independent of
	// whether any active turn was found or claimed. Audited under its own
	// event (turn.cancel.background_killed) rather than folded into
	// turn_cancel_attempt/turn_canceled, since those only fire on the
	// ClaimCancel-gated path below and a background-only cancel (no active
	// turn at all) would otherwise leave no audit trail whatsoever.
	var backgroundSessionsKilled int64
	var backgroundSessionsFailed int64
	if sessionID != "" && hooks.KillBackgroundSessions != nil {
		killed, failed := hooks.KillBackgroundSessions(sessionID)
		atomic.StoreInt64(&backgroundSessionsKilled, int64(killed))
		atomic.StoreInt64(&backgroundSessionsFailed, int64(failed))
		// Gate emission on killed>0 || failed>0 (architect finding): a
		// duplicate/no-op cancel that finds no background work at all (the
		// common case — most cancels target a session with nothing running
		// in the background) must not emit a no-op audit row. The outcome
		// counts themselves are still populated on CancelOutcome
		// unconditionally below, regardless of this gate.
		if killed > 0 || failed > 0 {
			audit.Emit(ctx, auditLogger, audit.EventTurnCancelBackgroundKilled, audit.SeverityInfo, map[string]any{
				"session_id":                 sessionID,
				"canceller_user":             canceller.UserID,
				"canceller_channel":          canceller.Channel,
				"background_sessions_killed": killed,
				"background_sessions_failed": failed,
			})
		}
	}

	// --- Abuse detection (always, before ClaimCancel) ---
	if al.cancelAbuse != nil {
		al.cancelAbuse.recordAttempt(ctx, canceller.UserID, canceller.Channel, at, auditLogger)
	}

	// --- First-cancel-wins atomic claim ---
	var activeTurn TurnCancelHook
	if sessionID != "" {
		activeTurn = al.GetActiveTurnHookForSession(sessionID)
	}
	wasFired := activeTurn != nil && activeTurn.ClaimCancel()

	// --- Fallback: claim ANY other live, unclaimed turn sharing this session
	// (closes the same wasFired-gate bug class 78bddc82 fixed for
	// KillBackgroundSessions, applied to the native cascade) ---
	//
	// GetActiveTurnHookForSession resolves exactly ONE hook (root-preferring)
	// and wasFired above reflects ONLY that hook's own ClaimCancel result.
	// That undercounts whenever a DIFFERENT turnState sharing sessionID is
	// still alive and has never been claimed — e.g. the resolved root already
	// fired from an earlier, unrelated cancel while a background/Critical
	// async delegate (a separate turnState, same transcriptSessionID) is
	// still genuinely running and was never signaled. Without this fallback,
	// the entire descendant-cancellation cascade AND the turn_canceled
	// transcript/audit write below are skipped purely because the ONE
	// resolved hook couldn't be claimed, even though claimAnyTurnForSession's
	// own scan (same predicate collectDescendantTurnIDs/InterruptSession use)
	// would find a perfectly claimable descendant. See that function's doc
	// comment for the full root-cause writeup.
	if !wasFired && sessionID != "" {
		if fallback := al.claimAnyTurnForSession(sessionID); fallback != nil {
			activeTurn = fallback
			wasFired = true
		}
	}

	// --- Audit: attempt (always, even for duplicate or no-turn cancels) ---
	audit.Emit(ctx, auditLogger, audit.EventTurnCancelAttempt, audit.SeverityInfo, map[string]any{
		"session_id":        sessionID,
		"canceller_user":    canceller.UserID,
		"canceller_channel": canceller.Channel,
		"was_fired":         wasFired,
	})

	if !wasFired {
		killedCount := int(atomic.LoadInt64(&backgroundSessionsKilled))
		failedCount := int(atomic.LoadInt64(&backgroundSessionsFailed))
		slog.Debug("agent: RequestCancel — no active turn or already canceled",
			"session_id", sessionID,
			"channel", scope.Channel,
			"chat_id", scope.ChatID,
			"background_sessions_killed", killedCount,
			"background_sessions_failed", failedCount,
		)
		// BackgroundSessionsKilled/Failed are populated here even though Fired
		// is false: the kill cascade above ran unconditionally, independent of
		// wasFired (see that block's doc comment) — this is precisely the
		// "background job outlived its own turn" case the cascade exists to
		// handle, and callers (handleCancel, watchDeadline) need these counts
		// to give the user/operator feedback despite there being no turn to
		// report as canceled.
		return CancelOutcome{
			Fired:                    false,
			BackgroundSessionsKilled: killedCount,
			BackgroundSessionsFailed: failedCount,
		}, nil
	}

	// --- Compute descendants list BEFORE InterruptSession to close the race ---
	//
	// Race window: InterruptSession calls providerCancel + requestGracefulInterrupt
	// which wakes the agent goroutine. That goroutine may call Finish() before we
	// reach SetOnCancelFinish below. If that happens, Finish() sees cancelFired==true
	// but onCancelFinish==nil and returns without invoking the callback — the
	// transcript entry, audit event, and MarkLastEntryTruncated are permanently lost.
	//
	// Fix: collect the descendants list now (same predicate as InterruptSession),
	// build the callback closure with the pre-computed list, register it via
	// SetOnCancelFinish, and THEN call InterruptSession. The callback is always
	// registered before any goroutine can reach Finish().
	store := al.ResolveSessionStore(sessionID)
	turnID := activeTurn.TurnID()
	descendants := al.collectDescendantTurnIDs(sessionID)

	// backgroundSessionsKilled was already computed above (independent of
	// wasFired, before this function's ClaimCancel gate) and is read here via
	// atomic — the write happened earlier in this same goroutine, but the
	// read below occurs inside a closure that may run on a DIFFERENT
	// goroutine (via Finish() called from the turn-processing goroutine), so
	// atomic access is required for correct cross-goroutine visibility even
	// though there is no write/write or write-after-read race to resolve.
	activeTurn.SetOnCancelFinish(func(cancelMethod string) {
		// Mark the last transcript entry as truncated.
		if store != nil {
			if err := store.MarkLastEntryTruncated(sessionID, turnID); err != nil {
				slog.Warn("agent: RequestCancel: MarkLastEntryTruncated failed",
					"session_id", sessionID, "turn_id", turnID, "error", err)
			}
			// Append a turn_canceled entry to the transcript.
			appendErr := store.AppendTranscript(sessionID, session.TranscriptEntry{
				ID:                   sessionID + "_canceled",
				Type:                 session.EntryTypeTurnCancelled,
				TurnID:               turnID,
				CancelledByUser:      canceller.UserID,
				CancelledByChannel:   canceller.Channel,
				CancelMethod:         cancelMethod,
				DescendantsCancelled: descendants,
				Timestamp:            time.Now().UTC(),
			})
			if appendErr != nil {
				slog.Warn("agent: RequestCancel: could not append turn_canceled transcript entry",
					"session_id", sessionID, "error", appendErr)
			}
		}
		// Audit: turn_canceled (fired once when the turn exits).
		audit.Emit(ctx, auditLogger, audit.EventTurnCancelled, audit.SeverityInfo, map[string]any{
			"session_id":                 sessionID,
			"turn_id":                    turnID,
			"canceller_user":             canceller.UserID,
			"canceller_channel":          canceller.Channel,
			"cancel_method":              cancelMethod,
			"descendants_canceled":       descendants,
			"background_sessions_killed": atomic.LoadInt64(&backgroundSessionsKilled),
			"background_sessions_failed": atomic.LoadInt64(&backgroundSessionsFailed),
		})
	})

	// --- PHASE A: graceful cascade + approval auto-deny ---
	//
	// (The background-session kill cascade already fired above, independent
	// of wasFired — see the comment at that call site.)
	//
	// Now that the callback is registered, fire InterruptSession. The ordering
	// guarantee: SetOnCancelFinish (above) stores the callback under ts.mu before
	// any goroutine awakened by InterruptSession can reach Finish() and read it.
	interrupted, _ := al.InterruptSession(sessionID, hint)

	// Defensive consistency check: the pre-computed descendants list must match
	// what InterruptSession collected. A mismatch means a turn was added or removed
	// in the narrow window between collectDescendantTurnIDs and InterruptSession —
	// this should never happen in practice but is worth a WARN if it does.
	if len(interrupted) != len(descendants) {
		slog.Warn("agent: RequestCancel: descendants list mismatch — turn added/removed between collect and interrupt",
			"session_id", sessionID,
			"pre_collected", descendants,
			"interrupted", interrupted,
		)
	}

	if hooks.CancelPendingApprovals != nil {
		hooks.CancelPendingApprovals(sessionID, "session canceled")
	}
	if hooks.SendStageFrame != nil {
		hooks.SendStageFrame(sessionID, "graceful")
	}

	// --- Mark session as interrupted in meta (best-effort) ---
	if hooks.SetSessionInterrupted != nil {
		hooks.SetSessionInterrupted(sessionID)
	} else if store != nil {
		// Default implementation when no hook is supplied (CLI / Tier A / Tier B).
		status := session.StatusInterrupted
		if err := store.SetMeta(sessionID, session.MetaPatch{Status: &status}); err != nil {
			slog.Warn("agent: RequestCancel: could not mark session interrupted",
				"session_id", sessionID, "error", err)
		}
	}

	// --- PHASE B: 3s timer → hard abort if ANY turn in the session cascade
	// (root or a still-running descendant, e.g. an orphaned background
	// delegate) is still alive ---
	//
	// Gated on al.sessionTurnsStillAlive(sessionID) rather than
	// activeTurn.IsAlive() alone: activeTurn is the single hook resolved once
	// above (GetActiveTurnHookForSession prefers the root turn), so gating
	// purely on ITS liveness meant a background delegate sub-turn that
	// outlived its already-gracefully-finished parent was never escalated to
	// — see sessionTurnsStillAlive's doc comment (pkg/agent/steering.go) for
	// the full root-cause writeup of the delegation-cancel bug this closes.
	time.AfterFunc(3*time.Second, func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("agent: RequestCancel: timer panic",
					"stage", "hard",
					"session_id", sessionID,
					"panic", r,
					"stack", string(debug.Stack()),
				)
			}
		}()
		if len(al.sessionTurnsStillAlive(sessionID)) == 0 {
			return // already finished — no live descendants either
		}
		if _, err := al.InterruptSessionHard(sessionID, hint); err != nil {
			slog.Warn("agent: RequestCancel: hard abort failed",
				"session_id", sessionID, "error", err)
		}
		if hooks.SendStageFrame != nil {
			hooks.SendStageFrame(sessionID, "hard")
		}

		// --- PHASE C: 5s after hard → detach any turn in the cascade still
		// alive (same session-wide liveness check as PHASE B; see above) ---
		hardAt := time.Now()
		time.AfterFunc(5*time.Second, func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("agent: RequestCancel: timer panic",
						"stage", "detached",
						"session_id", sessionID,
						"panic", r,
						"stack", string(debug.Stack()),
					)
				}
			}()
			stillAlive := al.sessionTurnsStillAlive(sessionID)
			if len(stillAlive) == 0 {
				return // finished in the meantime
			}
			for _, ts := range stillAlive {
				ts.MarkAbandoned()
			}
			if hooks.SendStageFrame != nil {
				hooks.SendStageFrame(sessionID, "detached")
			}
			audit.Emit(ctx, auditLogger, audit.EventTurnCancelStuck, audit.SeverityWarn, map[string]any{
				"session_id":                      sessionID,
				"turn_id":                         turnID,
				"goroutine_age_after_hard_cancel": time.Since(hardAt).String(),
			})
		})
	})

	return CancelOutcome{
		Fired:                    true,
		Descendants:              descendants,
		TurnID:                   turnID,
		BackgroundSessionsKilled: int(atomic.LoadInt64(&backgroundSessionsKilled)),
		BackgroundSessionsFailed: int(atomic.LoadInt64(&backgroundSessionsFailed)),
	}, nil
}

// RequestCancelForSession is a primitive-argument adapter for RequestCancel
// used by the commands.AgentLoopInterface. It avoids importing pkg/agent types
// in pkg/commands (which would create a circular dependency) by accepting only
// primitive string arguments.
//
// sessionID must be non-empty. Returns (fired, nil) on success; fired is true
// when an active turn was claimed.
func (al *AgentLoop) RequestCancelForSession(ctx context.Context, sessionID, userID, channel string) (bool, error) {
	if sessionID == "" {
		return false, fmt.Errorf("RequestCancelForSession: sessionID must not be empty")
	}
	outcome, err := al.RequestCancel(ctx,
		CancelScope{SessionID: sessionID},
		CancelCanceller{UserID: userID, Channel: channel},
		CancelHooks{
			// Tier A /cancel command carries no other transport-specific side
			// effects, but must still cascade to any background bash/exec
			// sessions this chat session started (FR-B10/FR-B11).
			KillBackgroundSessions: killBackgroundSessionsForCancelSurface,
		},
	)
	if err != nil {
		return false, err
	}
	return outcome.Fired, nil
}

// killBackgroundSessionsForCancelSurface is the CancelHooks.KillBackgroundSessions
// implementation shared by the primitive-argument adapters below (Tier A
// /cancel command and Tier B text-parsing channels), neither of which carries
// any other transport-specific cancel side effect. It reaches the single
// process-wide pkg/tools SessionManager via the exported GetSharedSessionManager
// accessor (getSessionManager itself is unexported/package-private to
// pkg/tools), kills every background session owned by sessionID, and returns
// (killed, failed) so RequestCancel can thread both counts into the
// turn_canceled audit event and its own background_killed audit event.
func killBackgroundSessionsForCancelSurface(sessionID string) (killed, failed int) {
	return tools.GetSharedSessionManager().KillAllForSession(sessionID)
}

// RequestCancelByChannelChat is a primitive-argument adapter for RequestCancel
// used by the channels.CancelInterceptor interface. It resolves the session by
// (channel, chatID) so Tier B text-parsing channels can fire the full cancel
// state machine without knowing the session ID.
//
// Returns nil when no matching turn exists (no-op). Returns a non-nil error
// only when channel or chatID is empty.
func (al *AgentLoop) RequestCancelByChannelChat(ctx context.Context, channelName, chatID, userID string) error {
	if channelName == "" || chatID == "" {
		return fmt.Errorf("RequestCancelByChannelChat: channel and chatID must not be empty")
	}
	_, err := al.RequestCancel(ctx,
		CancelScope{Channel: channelName, ChatID: chatID},
		CancelCanceller{UserID: userID, Channel: channelName},
		CancelHooks{
			// Tier B channels carry no other transport-specific side effects,
			// but must still cascade to any background bash/exec sessions
			// this chat session started (FR-B10/FR-B11).
			KillBackgroundSessions: killBackgroundSessionsForCancelSurface,
		},
	)
	return err
}
