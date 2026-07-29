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
	"errors"
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
	Fired       bool     // true if a turn was actually targeted (ClaimCancel succeeded)
	Descendants []string // turn IDs canceled (parent + sub-turns)
	TurnID      string   // root turn ID; empty when Fired is false

	// Armed is true when Fired is false because no turn was registered yet
	// for the resolved identity AND a pre-registration cancel latch
	// (cancel_prearm.go) was recorded in its place. It is NOT a synonym for
	// "did nothing": the next turn to register under the same identity
	// (session id, or (channel, chatID) when no session id resolved) will be
	// canceled the instant it registers, through the same audit/transcript/
	// stage-frame machinery a cancel arriving after registration uses — see
	// registerActiveTurn (turn.go) and armCancelOrFindActiveTurn
	// (cancel_prearm.go). Bounded by cancelPreArmTTL: a latch a turn does not
	// consume within that window is discarded rather than reaching an
	// unrelated later turn. Callers surfacing Fired to a user MUST also
	// check Armed before reporting a cancel as a no-op.
	Armed bool

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

	// OnLatchExpired is called when a pre-registration cancel latch
	// (cancel_prearm.go) armed BY THIS SPECIFIC RequestCancel call ages out
	// (cancelPreArmTTL, 5s) before any turn ever consumes it — i.e., this
	// cancel was acknowledged via CancelOutcome.Armed as "standing in for a
	// turn that hasn't registered yet", and no turn ever showed up in time to
	// actually apply it. Receives the same scope/canceller this call itself
	// passed to RequestCancel, so a caller that already told the user "stop
	// requested" (Armed:true) can follow up HONESTLY — "the cancel you
	// requested did not take effect" — rather than leaving the user believing
	// Stop worked while the turn it targeted runs (or already ran) to
	// completion uncanceled. This is NOT a second success signal: it fires
	// only on the failure path.
	//
	// Optional; nil is a silent no-op like every other hook here, but the
	// expiry itself is UNCONDITIONALLY logged at Warn (notifyLatchExpired,
	// cancel_prearm.go) regardless of whether this is wired, so the failure
	// is never fully silent even for a caller that hasn't wired it yet.
	//
	// Called on its own goroutine, asynchronously, well after this
	// RequestCancel call has returned — expiry is discovered lazily, either
	// by a LATER turn's own registration attempt finding its latch already
	// stale (consumePreArmedCancel, turn.go) or by a completely unrelated
	// cancel's opportunistic sweep evicting it because no turn ever
	// registered for it at all (armLocked, cancel_prearm.go — the only
	// discovery point for that case, e.g. a dropped Tier B message). Never
	// invoked while AgentLoop.cancelPreArm's mutex is held.
	//
	// Do not "fix" a caller that ignores this by widening cancelPreArmTTL —
	// see cancel_prearm.go's own doc comment for why a longer window is a
	// worse bug than the one it would be hiding.
	OnLatchExpired func(scope CancelScope, canceller CancelCanceller)
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
		sessionID = al.resolveSessionIDByChannelChat(scope.Channel, scope.ChatID)
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

	// --- Pre-registration latch (cancel_prearm.go) ---
	//
	// Reached only when the lookup above found nothing: no turn is currently
	// registered for this identity. Rather than silently reporting Fired:false
	// and forgetting the request ever happened (the pre-fix behavior — the
	// exact bug this closes: a turn that registers moments later runs to
	// completion having never seen the cancel), arm a latch keyed on the
	// narrowest identity the caller supplied so the NEXT turn to register
	// under it is canceled the instant it does. armCancelOrFindActiveTurn
	// re-resolves under its own lock first (a turn may have registered in the
	// interval between the unlocked lookup above and this call), so `armed`
	// is only ever true when nothing was found on either check.
	var armed bool
	if activeTurn == nil {
		if key := preArmKeyForScope(sessionID, scope); key != "" {
			activeTurn, armed = al.armCancelOrFindActiveTurn(key, sessionID, scope, canceller, hooks)
		}
	}

	wasFired := activeTurn != nil && activeTurn.ClaimCancel()

	// --- Audit: attempt (always, even for duplicate, no-turn, or armed-latch cancels) ---
	audit.Emit(ctx, auditLogger, audit.EventTurnCancelAttempt, audit.SeverityInfo, map[string]any{
		"session_id":        sessionID,
		"canceller_user":    canceller.UserID,
		"canceller_channel": canceller.Channel,
		"was_fired":         wasFired,
		"armed":             armed,
	})

	if !wasFired {
		killedCount := int(atomic.LoadInt64(&backgroundSessionsKilled))
		failedCount := int(atomic.LoadInt64(&backgroundSessionsFailed))
		slog.Debug("agent: RequestCancel — no active turn or already canceled",
			"session_id", sessionID,
			"channel", scope.Channel,
			"chat_id", scope.ChatID,
			"armed", armed,
			"background_sessions_killed", killedCount,
			"background_sessions_failed", failedCount,
		)
		// BackgroundSessionsKilled/Failed are populated here even though Fired
		// is false: the kill cascade above ran unconditionally, independent of
		// wasFired (see that block's doc comment) — this is precisely the
		// "background job outlived its own turn" case the cascade exists to
		// handle, and callers (handleCancel, watchDeadline) need these counts
		// to give the user/operator feedback despite there being no turn to
		// report as canceled. Armed is true when a latch now stands in for
		// this cancel (see the block above) — callers must not read
		// Fired:false as "nothing will happen" when Armed is true.
		return CancelOutcome{
			Fired:                    false,
			Armed:                    armed,
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

	// --- Transition BOTH stores via the single mediator (Defect #28 fix) ---
	//
	// The cancel must transition the durable LifecycleRecord to
	// LifecycleCancelled AND mirror onto UnifiedMeta (interrupted) — the same
	// paired transition every task/delegate terminal write performs. Before
	// this fix, this block wrote ONLY UnifiedMeta (via the hook or the default
	// branch), orphaning the parent session's LifecycleRecord: it stayed
	// running/queued on disk until a future boot sweep caught it. That is the
	// SAME defect as a kill -9 crash, produced by NORMAL cancel operation, not
	// just crashes. The mediator is now the single authority for the
	// lifecycle-state → unified-status mapping (cancelled → interrupted).
	//
	// ErrLifecycleNotFound is expected and silenced here: a normal web CHAT
	// session may have no LifecycleRecord at all (only task/delegate/plan
	// sessions mint one), so the lifecycle half is a no-op for those and the
	// UnifiedMeta mirror still proceeds inside the mediator.
	lifecycleStore := al.GetSessionLifecycleStore()
	if err := session.TransitionSession(lifecycleStore, store, sessionID, session.LifecycleCancelled, ""); err != nil && !errors.Is(err, session.ErrLifecycleNotFound) {
		slog.Warn("agent: RequestCancel: could not transition session to cancelled",
			"session_id", sessionID, "error", err)
	}
	// The hook (if supplied) fires for any additional transport-specific
	// side-effects the WS layer needs beyond the UnifiedMeta mirror the
	// mediator already performed (the WS hook itself also writes UnifiedMeta
	// to interrupted — redundant with the mediator, but harmless: same value).
	if hooks.SetSessionInterrupted != nil {
		hooks.SetSessionInterrupted(sessionID)
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
// used by the commands.AgentLoopInterface (and, directly, by goal_loop.go's
// `/goal clear` verifier-cancel and plan_engine.go's Stop session-cancel
// fan-out). It avoids importing pkg/agent types in pkg/commands (which would
// create a circular dependency) by accepting and returning only primitive
// types.
//
// sessionID must be non-empty. Returns (fired, armed, nil) on success:
//   - fired is true when an active turn was claimed.
//   - armed is true when fired is false BECAUSE no turn was registered yet
//     for sessionID and a pre-registration cancel latch (cancel_prearm.go)
//     was recorded in its place — see CancelOutcome.Armed's doc comment for
//     the full contract. armed is NEVER true when fired is true.
//
// Every caller that surfaces fired to a user or operator MUST also check
// armed before reporting a cancel as a no-op: fired=false, armed=true means
// the cancel WILL still fire — against the next turn to register for this
// session, within cancelPreArmTTL — not that nothing happened.
//
// HISTORY: prior to this widening, this adapter flattened CancelOutcome down
// to a bare (bool, error), unconditionally discarding Armed at this exact
// boundary — the structural gap CancelOutcome.Armed's own doc comment warns
// every RequestCancel caller against, and the one this widening closes at
// the source rather than in each of the (several) call sites that go through
// it. See pkg/commands/runtime.go's CancelActiveTurn for the Tier A /cancel
// consumer this was fixed for; pkg/agent/plan_engine.go's cancelSessions
// (Stop fan-out) is a further consumer of this same adapter — it now has its
// own dedicated `armed` bucket on sessionCancelReport (plan_engine.go), kept
// apart from `failed`/`notFired` for exactly the same reason this doc
// comment gives, so the "still needs its own bucket" gap noted here at the
// time of this widening has since been closed, not merely tracked.
func (al *AgentLoop) RequestCancelForSession(ctx context.Context, sessionID, userID, channel string) (fired bool, armed bool, err error) {
	if sessionID == "" {
		return false, false, fmt.Errorf("RequestCancelForSession: sessionID must not be empty")
	}
	outcome, err := al.RequestCancel(ctx,
		CancelScope{SessionID: sessionID},
		CancelCanceller{UserID: userID, Channel: channel},
		CancelHooks{
			// Tier A /cancel command carries no other transport-specific side
			// effects, but must still cascade to any background bash/exec
			// sessions this chat session started (FR-B10/FR-B11).
			KillBackgroundSessions: killBackgroundSessionsForCancelSurface,
			// Mirror the WS handleCancel / cron watchDeadline callers (the two
			// "fixed by hand" consumers of RequestCancel's own Armed field):
			// give every caller of THIS adapter the same operator-visible
			// signal when a latch it armed ages out (cancelPreArmTTL) before
			// any turn consumes it — otherwise a caller that now honestly
			// reports "acknowledged, pending" (via the armed return above)
			// has nothing to fall back on if that promise silently expires.
			// Generic and caller-agnostic by necessity: this primitive
			// adapter has no per-call hook injection point of its own, so
			// this fires identically for every caller that reaches it
			// (Tier A /cancel, goal_loop.go's `/goal clear`, and
			// plan_engine.go's Stop session-cancel fan-out alike).
			OnLatchExpired: func(scope CancelScope, canceller CancelCanceller) {
				slog.Warn("agent: RequestCancelForSession: pre-registration cancel latch expired unconsumed — the cancel this call acknowledged never actually took effect",
					"session_id", sessionID,
					"canceller_user", canceller.UserID,
					"canceller_channel", canceller.Channel,
				)
			},
		},
	)
	if err != nil {
		return false, false, err
	}
	return outcome.Fired, outcome.Armed, nil
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
// Returns (fired, armed, err):
//   - fired is true when an active turn was claimed and the cancel cascade ran.
//   - armed is true when fired is false BECAUSE no turn was registered yet for
//     the resolved identity and a pre-registration cancel latch
//     (cancel_prearm.go) was recorded in its place — see CancelOutcome.Armed's
//     doc comment. Callers surfacing feedback to a user MUST distinguish this
//     from a genuine no-op (neither fired nor armed).
//   - err is non-nil only when channelName or chatID is empty.
//
// This widening mirrors RequestCancelForSession's own (fired, armed, err)
// return at the same boundary — Defect #29: the prior bare-error return
// discarded the entire CancelOutcome, so DispatchCancelIfRecognized could not
// distinguish a real cancel from an armed latch from a genuine no-op, and
// replied "Canceling..." unconditionally.
func (al *AgentLoop) RequestCancelByChannelChat(ctx context.Context, channelName, chatID, userID string) (fired bool, armed bool, err error) {
	if channelName == "" || chatID == "" {
		return false, false, fmt.Errorf("RequestCancelByChannelChat: channel and chatID must not be empty")
	}
	outcome, err := al.RequestCancel(ctx,
		CancelScope{Channel: channelName, ChatID: chatID},
		CancelCanceller{UserID: userID, Channel: channelName},
		CancelHooks{
			// Tier B channels carry no other transport-specific side effects,
			// but must still cascade to any background bash/exec sessions
			// this chat session started (FR-B10/FR-B11).
			KillBackgroundSessions: killBackgroundSessionsForCancelSurface,
		},
	)
	if err != nil {
		return false, false, err
	}
	return outcome.Fired, outcome.Armed, nil
}
