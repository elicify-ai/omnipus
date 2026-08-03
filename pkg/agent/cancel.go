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

	// DescendantWalkIncomplete is true when the durable descendant-set walk
	// this cancel relied on (CollectDescendantSessionIDs, via
	// resolveBackgroundKillSessionIDs) hit a lifecycleStore.List failure
	// partway through and had to abandon that branch of the tree (FIX-5,
	// Defect 2). When true, BackgroundSessionsKilled/BackgroundSessionsFailed
	// and the turn.cancel.background_killed audit event reflect only the
	// PARTIAL subtree actually reached — a caller MUST NOT read
	// "BackgroundSessionsKilled: N, BackgroundSessionsFailed: 0" as "the
	// whole subtree was swept clean" when this is true. Populated
	// UNCONDITIONALLY, like BackgroundSessionsKilled/Failed above, since the
	// walk that can fail runs before the ClaimCancel gate.
	DescendantWalkIncomplete bool
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
	// bash/exec session owned by ONE session id (FR-B10/FR-B11, User Story 5:
	// "Canceling a session also stops any background bash work it started").
	//
	// Signature is deliberately unchanged (single id, not a set) even though
	// ADR-057 FR-027 requires the cascade to reach sessionID's FULL
	// descendant set, not sessionID alone: a delegated child now owns its OWN
	// distinct session id (see pkg/tools/session.go's
	// ProcessSession.OwnerSessionID doc comment), so a single exact match
	// against the root id no longer reaches a child's background shells.
	// RequestCancel closes that gap on ITS side instead of widening this
	// hook's contract: it resolves the descendant set once
	// (resolveBackgroundKillSessionIDs, this file) and invokes THIS hook
	// once per id in that set, accumulating each call's (killed, failed) into
	// the totals below — so every existing implementation of this hook
	// (pkg/gateway/websocket.go's buildCancelHooks, pkg/gateway/schedules.go's
	// watchDeadline) cascades correctly over the full descendant set with NO
	// changes of their own, because each of their calls only ever has to
	// reason about the ONE id it was actually given.
	//
	// Called ONCE PER RESOLVED ID, unconditionally, for any resolvable
	// (non-empty) sessionID — deliberately NOT gated on whether an active
	// turn exists or ClaimCancel succeeds (wasFired). A `bash
	// run_in_background=true` call's own turn ends immediately, so by the
	// time a user cancels the still-running background job there is
	// typically no active turn left to claim; gating this hook on wasFired
	// would silently no-op the entire cascade in exactly that (the most
	// common) case. A session with no background work sees no behavior
	// change — this hook is a no-op in that case, not an error.
	//
	// Returns (killed, failed) for the ONE id it was called with: killed is
	// the count of background sessions actually killed, failed is the count
	// that were RUNNING and eligible but whose kill call itself failed (the
	// underlying syscall failing, not a benign lost-race — see
	// tools.SessionManager.KillAllForSessions's doc comment for that
	// distinction). RequestCancel sums both across every id in the resolved
	// set to (a) emit its own turn.cancel.background_killed audit event —
	// gated on killed>0 || failed>0 so a cancel that finds nothing to kill
	// does not emit a no-op audit row — carrying background_sessions_failed
	// alongside background_sessions_killed, and (b) thread both totals into
	// the turn_canceled audit event's fields map on the ClaimCancel-gated
	// path. Before the failed count was added, a kill failure was invisible
	// outside a slog.Warn deep in pkg/tools/session.go, uncorrelated with any
	// audit event a security reviewer would actually go looking at —
	// contradicting this very event's own doc comment below.
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
//     running background job there is no active turn left to claim), and
//     cascades over sessionID's FULL descendant set (ADR-057 FR-027 — see
//     resolveBackgroundKillSessionIDs)
//   - abuse-detection record
//   - ClaimCancel atomic first-cancel-wins check
//   - turn_cancel_attempt audit emission (always, even for no-op cancels)
//   - graceful cascade via Interrupt(sessionID, ScopeSubtree, hint) / providerCancel
//   - durable descendant lifecycle-record walk, own goroutine, off the
//     escalation path (ADR-057 FR-025/FR-026 — see
//     cancelDurableDescendantLifecycleRecords)
//   - approval auto-deny (via hooks.CancelPendingApprovals)
//   - cancel_stage frame emission (via hooks.SendStageFrame)
//   - session status → interrupted (via hooks.SetSessionInterrupted)
//   - transcript MarkLastEntryTruncated + turn_canceled entry on Finish
//   - turn_canceled audit on Finish
//   - 3s timer → hard abort (InterruptSessionHard(sessionID, ScopeSubtree, hint))
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
	// [FIX-5, Defect 2] descendantWalkIncomplete is true when
	// resolveBackgroundKillSessionIDs' underlying durable walk hit a
	// lifecycleStore.List error partway through — the ids loop below then
	// ran over a PARTIAL (truncated) view of the true descendant set, so
	// killed/failed below must never be read as "the whole subtree was
	// swept clean". This is threaded into both the background-kill audit
	// event below AND the CancelOutcome this function returns, at every
	// return site.
	var descendantWalkIncomplete bool
	if sessionID != "" && hooks.KillBackgroundSessions != nil {
		// ADR-057 FR-027: cascade over sessionID's FULL descendant set, not
		// sessionID alone — a delegated child now owns its OWN distinct
		// session id, so its background shells are invisible to a hook call
		// scoped to only the root id. hooks.KillBackgroundSessions' own
		// signature stays single-id (see its doc comment for why); this loop
		// is what actually reaches every descendant, summing each call's
		// result into the totals below.
		ids, walkErr := al.resolveBackgroundKillSessionIDs(sessionID)
		if walkErr != nil {
			descendantWalkIncomplete = true
			slog.Warn("agent: RequestCancel: descendant walk failed partway through — the background-kill cascade below is INCOMPLETE; some descendants' background bash/exec work may be left running undetected",
				"session_id", sessionID, "error", walkErr)
		}
		for _, id := range ids {
			killed, failed := hooks.KillBackgroundSessions(id)
			atomic.AddInt64(&backgroundSessionsKilled, int64(killed))
			atomic.AddInt64(&backgroundSessionsFailed, int64(failed))
		}
		killed := int(atomic.LoadInt64(&backgroundSessionsKilled))
		failed := int(atomic.LoadInt64(&backgroundSessionsFailed))
		// Gate emission on killed>0 || failed>0 || descendantWalkIncomplete
		// (architect finding, widened by FIX-5/Defect 2): a duplicate/no-op
		// cancel that finds no background work at all (the common case —
		// most cancels target a session with nothing running in the
		// background) must not emit a no-op audit row — UNLESS the walk
		// itself failed, in which case "killed:0, failed:0" is not actually
		// known to be a clean no-op; it might just be everything the walk
		// could see before it broke. The outcome counts themselves are
		// still populated on CancelOutcome unconditionally below,
		// regardless of this gate.
		if killed > 0 || failed > 0 || descendantWalkIncomplete {
			audit.Emit(ctx, auditLogger, audit.EventTurnCancelBackgroundKilled, audit.SeverityInfo, map[string]any{
				"session_id":                 sessionID,
				"canceller_user":             canceller.UserID,
				"canceller_channel":          canceller.Channel,
				"background_sessions_killed": killed,
				"background_sessions_failed": failed,
				"descendant_walk_incomplete": descendantWalkIncomplete,
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
			DescendantWalkIncomplete: descendantWalkIncomplete,
		}, nil
	}

	// --- Compute descendants list BEFORE Interrupt to close the race ---
	//
	// Race window: Interrupt calls providerCancel + requestGracefulInterrupt
	// which wakes the agent goroutine. That goroutine may call Finish() before we
	// reach SetOnCancelFinish below. If that happens, Finish() sees cancelFired==true
	// but onCancelFinish==nil and returns without invoking the callback — the
	// transcript entry, audit event, and MarkLastEntryTruncated are permanently lost.
	//
	// Fix: collect the descendants list now (same predicate as Interrupt),
	// build the callback closure with the pre-computed list, register it via
	// SetOnCancelFinish, and THEN call Interrupt. The callback is always
	// registered before any goroutine can reach Finish().
	//
	// ADR-057 FR-024: this list is computed EXACTLY ONCE, here, in PHASE A —
	// PHASE B/C below thread it through (al.liveTurnStatesAmong(descendants))
	// rather than re-deriving the subtree from sessionID afresh each time an
	// escalation timer fires.
	store := al.ResolveSessionStore(sessionID)
	turnID := activeTurn.TurnID()
	descendants := al.collectDescendantTurnIDs(sessionID)

	// --- ADR-057 FR-025/FR-026: durable descendant lifecycle-record walk ---
	// Runs on its OWN goroutine, off the 3s/5s escalation path below, so a
	// subtree with many persisted lifecycle records never delays
	// RequestCancel's return or the graceful/hard cascade timers. Reaches
	// every descendant with a DURABLE lifecycle record — including one whose
	// own intermediate parent turn has already finished and is no longer in
	// al.activeTurnStates, a gap the in-memory descendants list above cannot
	// close (see collectLiveDescendantTurnStates's KNOWN LIMITATION doc
	// comment, steering.go) — and transitions each one's persisted record to
	// cancelled. See cancelDurableDescendantLifecycleRecords's own doc
	// comment for the full mechanism.
	go al.cancelDurableDescendantLifecycleRecords(sessionID)

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
	// Now that the callback is registered, fire Interrupt. The ordering
	// guarantee: SetOnCancelFinish (above) stores the callback under ts.mu before
	// any goroutine awakened by Interrupt can reach Finish() and read it.
	//
	// ScopeSubtree: a Stop reaching RequestCancel always names a CHAT/session
	// root (web SPA Stop button, Tier A /cancel, Tier B channels, CLI, and
	// the ADR-045 orphan-reap path all resolve sessionID this way — see this
	// file's own top-of-file doc comment) and users expect a chat-level Stop
	// to sweep the WHOLE delegation tree, not just the turn directly
	// registered under sessionID. This is also byte-identical to the
	// pre-collapse InterruptSession's own reach (steering.go's
	// resolveInterruptAnchors Range fallback already finds every descendant
	// sharing sessionID's routingSessionID directly, so the descendant walk
	// on top is redundant-but-harmless for a chat root) — ScopeSelfOnly would
	// be a silent behavior regression here, not a neutral choice.
	interrupted, _ := al.Interrupt(sessionID, ScopeSubtree, hint)

	// Defensive consistency check: the pre-computed descendants list must match
	// what Interrupt collected. A mismatch means a turn was added or removed
	// in the narrow window between collectDescendantTurnIDs and Interrupt —
	// this should never happen in practice but is worth a WARN if it does.
	//
	// [FIX-5, Defect 3b, 2026-08-03] This USED to compare len(interrupted) !=
	// len(descendants) only — a same-SIZE-but-different-MEMBERSHIP mismatch
	// (e.g. turn X finished and was replaced by a new turn Y in the same
	// instant, so both lists have length 1 but name different turn ids) was
	// silent: the length-only check passed, no WARN fired, and the
	// turn_canceled audit event's descendants_canceled field (set to
	// `descendants`, the PRE-collected list, in the SetOnCancelFinish closure
	// below) then named turn X as "cancelled" even though Interrupt actually
	// reached only turn Y — an audit trail asserting a cancellation that
	// never happened. Comparing actual set membership (not just count) below
	// catches this class of mismatch and reports exactly which turn ids
	// diverged, in which direction.
	if missingFromInterrupted, extraInInterrupted := stringSliceSetDiff(descendants, interrupted); len(missingFromInterrupted) > 0 || len(extraInInterrupted) > 0 {
		slog.Warn("agent: RequestCancel: descendants list mismatch — turn added/removed between collect and interrupt; "+
			"turn_canceled's descendants_canceled field may name a turn Interrupt did not actually reach, or omit one it did",
			"session_id", sessionID,
			"pre_collected", descendants,
			"interrupted", interrupted,
			"missing_from_interrupted", missingFromInterrupted, // audited as cancelled but NOT actually reached by Interrupt
			"extra_in_interrupted", extraInInterrupted, // reached by Interrupt but NOT named in the audit's descendants_canceled
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

	// --- PHASE B: 3s timer → hard abort if ANY turn in the PHASE-A-computed
	// descendant set (root or a still-running descendant, e.g. an orphaned
	// background delegate) is still alive ---
	//
	// Gated on al.liveTurnStatesAmong(descendants) — the SAME descendants
	// list PHASE A computed once above — rather than activeTurn.IsAlive()
	// alone: activeTurn is the single hook resolved once above
	// (GetActiveTurnHookForSession prefers the root turn), so gating purely
	// on ITS liveness meant a background delegate sub-turn that outlived its
	// already-gracefully-finished parent was never escalated to. ADR-057
	// FR-024 requires threading PHASE A's once-computed subtree through here
	// rather than re-deriving it fresh via al.sessionTurnsStillAlive
	// (steering.go) each time this timer fires — a fresh re-derivation could
	// silently widen the escalation to a turn that started AFTER
	// turn_canceled's own descendants_canceled field was already decided.
	// See sessionTurnsStillAlive's own doc comment (steering.go) for the full
	// root-cause writeup of the underlying delegation-cancel bug this
	// mechanism closes.
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
		if len(al.liveTurnStatesAmong(descendants)) == 0 {
			return // already finished — no live descendants either
		}
		if _, err := al.InterruptSessionHard(sessionID, ScopeSubtree, hint); err != nil {
			slog.Warn("agent: RequestCancel: hard abort failed",
				"session_id", sessionID, "error", err)
		}
		if hooks.SendStageFrame != nil {
			hooks.SendStageFrame(sessionID, "hard")
		}

		// --- PHASE C: 5s after hard → detach any turn in the PHASE-A-computed
		// descendant set still alive (same threaded liveness check as
		// PHASE B; see above, ADR-057 FR-024) ---
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
			stillAlive := al.liveTurnStatesAmong(descendants)
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
		DescendantWalkIncomplete: descendantWalkIncomplete,
	}, nil
}

// stringSliceSetDiff compares two string slices as SETS (not as ordered/
// counted sequences) and returns (onlyInA, onlyInB): the elements present in
// a but absent from b, and vice versa. Both returns are nil when the two
// slices contain exactly the same set of elements, regardless of order or
// duplicate count. Used by RequestCancel's PHASE-A/Interrupt consistency
// check (FIX-5, Defect 3b) to catch a same-SIZE-but-different-MEMBERSHIP
// mismatch that a bare len(a) != len(b) comparison cannot see.
func stringSliceSetDiff(a, b []string) (onlyInA, onlyInB []string) {
	setA := make(map[string]struct{}, len(a))
	for _, v := range a {
		setA[v] = struct{}{}
	}
	setB := make(map[string]struct{}, len(b))
	for _, v := range b {
		setB[v] = struct{}{}
	}
	for v := range setA {
		if _, ok := setB[v]; !ok {
			onlyInA = append(onlyInA, v)
		}
	}
	for v := range setB {
		if _, ok := setA[v]; !ok {
			onlyInB = append(onlyInB, v)
		}
	}
	return onlyInA, onlyInB
}

// ============================================================================
// ADR-057 U15 — descendant-set helpers (W8, FR-024…FR-027)
// ============================================================================

// CollectDescendantSessionIDs performs a breadth-first walk of the durable
// ParentDurableKey edge (pkg/session/lifecycle.go, U13's FR-019/FR-020
// index) starting at rootSessionID and returns every reachable descendant's
// OWN session id (rootSessionID itself is never included).
//
// [FIX-5, Defect 4, 2026-08-03] Exported and HOISTED: this used to be
// duplicated byte-for-byte as pkg/gateway/websocket.go's unexported
// u11CollectDescendantSessionIDs, kept as a separate copy only because a
// parallel-implementation ownership rule forbade that unit from editing
// pkg/agent (or this unit from editing pkg/gateway). That rule has expired.
// websocket.go's buildCancelHooks now calls this function directly;
// u11CollectDescendantSessionIDs itself survives only as a signature-compat
// shim (its exact pre-existing name/signature is called directly by
// pkg/gateway/rest.go's deleteSession handler and by
// pkg/gateway/websocket_adr057_test.go's U11 unit tests — both outside this
// fix's file ownership, so the shim is kept rather than requiring edits
// there).
//
// This is the same primitive the cancel/approval-cascade paths in both
// packages share, because a background bash/exec session (FR-027, see
// resolveBackgroundKillSessionIDs below) and a persisted lifecycle record
// (FR-025/FR-026, see cancelDurableDescendantLifecycleRecords below) can
// each OUTLIVE the in-memory turnState that spawned them: a `delegate
// async=true` child's own turn frequently finishes almost immediately while
// its spawned background bash job keeps running for minutes. Deriving this
// set from al.activeTurnStates (which only knows about turns still
// registered right now) would silently miss exactly the descendants
// FR-027/FR-025 exist to reach. Reads no turnState field at all — in
// particular never ts.routingSessionID, whose reader set FR-014 closes to
// the seven role-B predicates plus WS-payload stamping plus the three
// pre-arm reads — so this walk is unaffected by that restriction.
//
// Returns (nil, nil) when lifecycleStore is nil or rootSessionID is empty,
// which degrades resolveBackgroundKillSessionIDs to exactly today's
// single-id behavior (root only) rather than failing the cascade outright —
// this is NOT an error case, it is the documented degrade-gracefully path
// for an install that never wired a lifecycle store at all.
//
// [FIX-5, Defect 2, 2026-08-03] Returns a non-nil error when ANY
// lifecycleStore.List call in the walk fails. Before this fix, a single
// corrupt/unreadable record made the query for that one node fail, and the
// walk silently `continue`d past it — treating "the query itself errored"
// identically to "this node legitimately has zero children". Every
// descendant beneath the failure point then vanished from the returned
// slice with NO signal to the caller: resolveBackgroundKillSessionIDs would
// report a clean-looking background-kill cascade that actually missed half
// the tree, and the approval-cancel cascade (websocket.go's
// buildCancelHooks) would leave a dropped grandchild's pending
// RequestApproval hanging until its own multi-minute timeout while the UI
// reported the cancel as complete. The returned descendants slice is still
// the PARTIAL set successfully discovered before the failure — callers MUST
// treat a non-nil error as "this is a truncated view of the true descendant
// set", never as "clean success with fewer descendants than expected".
func CollectDescendantSessionIDs(lifecycleStore *session.LifecycleStore, rootSessionID string) ([]string, error) {
	if lifecycleStore == nil || rootSessionID == "" {
		return nil, nil
	}
	visited := map[string]struct{}{rootSessionID: {}}
	queue := []string{rootSessionID}
	var descendants []string
	var walkErrs []error
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		children, err := lifecycleStore.List(session.LifecycleFilter{ParentDurableKey: id})
		if err != nil {
			// This branch of the tree is now UNREACHABLE for this walk — every
			// descendant beneath `id`, however many levels deep, is silently
			// dropped from the returned slice. Recorded (not just logged) so
			// the caller can distinguish this from "id has no children".
			walkErrs = append(walkErrs, fmt.Errorf("list children of %q: %w", id, err))
			continue
		}
		for _, rec := range children {
			if _, seen := visited[rec.SessionID]; seen {
				continue
			}
			visited[rec.SessionID] = struct{}{}
			descendants = append(descendants, rec.SessionID)
			queue = append(queue, rec.SessionID)
		}
	}
	if len(walkErrs) > 0 {
		return descendants, fmt.Errorf("descendant walk incomplete for root %q: %d branch(es) failed to list children: %w",
			rootSessionID, len(walkErrs), errors.Join(walkErrs...))
	}
	return descendants, nil
}

// collectDescendantSessionIDs is al's own lifecycle-store-bound wrapper
// around the hoisted CollectDescendantSessionIDs (see that function's doc
// comment for the full walk semantics and the FIX-5/Defect-2 error
// contract).
func (al *AgentLoop) collectDescendantSessionIDs(rootSessionID string) ([]string, error) {
	return CollectDescendantSessionIDs(al.GetSessionLifecycleStore(), rootSessionID)
}

// resolveBackgroundKillSessionIDs returns the SET of real, store-backed
// session ids whose background bash/exec work a cancel of sessionID must
// reach (ADR-057 FR-027): sessionID itself, plus every durable descendant
// collectDescendantSessionIDs finds. A delegated child's background shell is
// owned by the CHILD's OWN session id (see pkg/tools/session.go's
// ProcessSession.OwnerSessionID doc comment), never by the shared
// routing/interrupt key sessionID names, so killing only sessionID's own
// background shells silently orphans every descendant's as detached
// processes — U16's KillAllForSessions red/green proved exactly this
// failure mode against the single-id KillAllForSession call, which still
// compiles and so gives no build-time warning that it is now wrong.
//
// [FIX-5, Defect 2] Returns the descendant-walk error alongside the (still
// usable, but possibly PARTIAL) id set — sessionID itself is always present
// even on error, since it is prepended unconditionally and never depends on
// the walk succeeding. The caller (RequestCancel) decides how to surface a
// non-nil error; this function never silently drops it.
func (al *AgentLoop) resolveBackgroundKillSessionIDs(sessionID string) ([]string, error) {
	if sessionID == "" {
		return nil, nil
	}
	descendants, err := al.collectDescendantSessionIDs(sessionID)
	return append([]string{sessionID}, descendants...), err
}

// cancelDurableDescendantLifecycleRecords walks the durable ParentDurableKey
// edge transitively from rootSessionID (via collectDescendantSessionIDs) and
// transitions EVERY reachable descendant's persisted LifecycleRecord to
// cancelled (FR-026), independent of whether that descendant still has a
// live turnState in al.activeTurnStates. This is the DURABLE counterpart to
// the in-memory turn cascade (Interrupt/InterruptSessionHard) RequestCancel
// fires above: a descendant whose own intermediate parent turn has already
// finished and been cleared from activeTurnStates is UNREACHABLE via the
// in-memory parentTurnID chain (steering.go's collectLiveDescendantTurnStates
// documents this limitation on itself) but remains reachable here, because
// this walk is keyed on the durable ParentDurableKey edge persisted to disk,
// never on any in-memory turn registration.
//
// FR-025: RequestCancel launches this via `go
// al.cancelDurableDescendantLifecycleRecords(...)` — once per Stop, on its
// OWN goroutine, off the 3s/5s escalation path — so a subtree with many
// persisted lifecycle records never delays RequestCancel's return or the
// graceful/hard cascade timers.
//
// rootSessionID's OWN lifecycle record is transitioned by RequestCancel's
// existing session.TransitionSession call (PHASE A, above this function in
// the file) — this walk starts its BFS AT rootSessionID (to enumerate its
// direct children) but never re-writes rootSessionID's own record.
// ErrLifecycleNotFound (no record for this descendant — e.g. it is itself a
// plain, non-delegate session with no lifecycle record of its own) and
// ErrLifecycleTerminalImmutable (the descendant already reached a terminal
// state on its own, e.g. it completed naturally moments before the Stop)
// are both expected, benign outcomes and are silenced rather than logged.
func (al *AgentLoop) cancelDurableDescendantLifecycleRecords(rootSessionID string) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("agent: cancelDurableDescendantLifecycleRecords: panic",
				"root_session_id", rootSessionID, "panic", r, "stack", string(debug.Stack()))
		}
	}()
	lifecycleStore := al.GetSessionLifecycleStore()
	if lifecycleStore == nil {
		return
	}
	ids, walkErr := al.collectDescendantSessionIDs(rootSessionID)
	if walkErr != nil {
		// [FIX-5, Defect 2] The walk itself failed partway through — ids is a
		// PARTIAL view of the true descendant set. Every descendant beneath
		// the failure point will NOT be transitioned to cancelled by the loop
		// below and will sit on disk as running/queued until a future boot
		// sweep catches it (the same class of staleness a kill -9 crash
		// leaves, but produced here by normal cancel operation). This is a
		// best-effort, fire-and-forget goroutine (FR-025) with no return
		// value to propagate the failure through, so a clearly-worded WARN
		// naming BOTH the root and the partial id count is the load-bearing
		// signal for this path.
		slog.Warn("agent: cancelDurableDescendantLifecycleRecords: descendant walk failed partway through — this cascade is INCOMPLETE, some durable lifecycle records beneath the failure point will NOT be transitioned to cancelled",
			"root_session_id", rootSessionID, "partial_descendants_found", len(ids), "error", walkErr)
	}
	for _, id := range ids {
		childStore := al.ResolveSessionStore(id)
		err := session.TransitionSession(lifecycleStore, childStore, id, session.LifecycleCancelled, "")
		if err != nil && !errors.Is(err, session.ErrLifecycleNotFound) && !errors.Is(err, session.ErrLifecycleTerminalImmutable) {
			slog.Warn("agent: cancelDurableDescendantLifecycleRecords: could not transition descendant to cancelled",
				"session_id", id, "root_session_id", rootSessionID, "error", err)
		}
	}
}

// turnStatesByTurnID resolves the live turnState pointers registered under
// any sessionKey in al.activeTurnStates whose OWN turnID appears in ids, via
// a single Range pass. activeTurnStates is keyed by sessionKey, not turnID,
// so there is no O(1) point lookup for a bare turn id — this mirrors the
// same cost steering.go's collectLiveDescendantTurnStates already pays for
// its own parentTurnID-chain walk.
//
// Deliberately reads only ts.turnID off each value — NEVER
// ts.routingSessionID, whose reader set FR-014 closes to exactly the seven
// role-B predicates plus WS-payload stamping plus the three pre-arm reads
// (see "Three reads, five sites" in the ADR-057 spec). This function lets
// RequestCancel's PHASE B/C escalation gate (FR-024, below via
// liveTurnStatesAmong) consult the EXACT descendant set a legitimate role-B
// predicate call (collectDescendantTurnIDs, steering.go) already produced,
// without adding an eighth, illegitimate read site of its own.
func (al *AgentLoop) turnStatesByTurnID(ids []string) []*turnState {
	if len(ids) == 0 {
		return nil
	}
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	var found []*turnState
	al.activeTurnStates.Range(func(_, value any) bool {
		ts, ok := value.(*turnState)
		if ok && ts != nil && want[ts.turnID] {
			found = append(found, ts)
		}
		return true
	})
	return found
}

// liveTurnStatesAmong filters turnStatesByTurnID(ids) to those still alive
// (turnState.IsAlive()). ADR-057 FR-024: RequestCancel's PHASE B/C
// escalation timers call this with the SAME descendant-id list PHASE A
// computed once, rather than re-deriving the subtree afresh via
// sessionTurnsStillAlive each time a timer fires — a re-derivation could
// silently pick up a turn that started AFTER PHASE A's snapshot, widening
// the escalation's reach beyond what turn_canceled's own
// descendants_canceled field named.
func (al *AgentLoop) liveTurnStatesAmong(ids []string) []*turnState {
	states := al.turnStatesByTurnID(ids)
	if len(states) == 0 {
		return nil
	}
	live := make([]*turnState, 0, len(states))
	for _, ts := range states {
		if ts.IsAlive() {
			live = append(live, ts)
		}
	}
	return live
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
