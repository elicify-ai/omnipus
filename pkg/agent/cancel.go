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

// cancelHardAbortDelay is PHASE B's escalation delay: how long after a
// graceful cancel (PHASE A) RequestCancel waits before hard-aborting whatever
// is still alive in sessionID's tree. Declared as a var, not a const, so
// tests can shrink it — mirrors cancel_prearm.go's cancelPreArmTTL/
// turnSettleGrace, which exist for the identical reason (deterministic,
// fast tests instead of sleeping the real production duration).
var cancelHardAbortDelay = 3 * time.Second

// cancelDetachDelay is PHASE C's escalation delay, measured from PHASE B's
// own hard-abort firing (not from the original Stop). Declared as a var for
// the same test-shrinking reason as cancelHardAbortDelay.
var cancelDetachDelay = 5 * time.Second

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

	// --- Pending AskUserQuestion cancel (askuserquestion-tool-spec v3,
	// US-6 S2): a Stop on a session with a PARKED question set has no active
	// turn to claim below (the park already ended the turn), so — like the
	// background-kill cascade above and for the same reason — this must NOT
	// be gated on wasFired/ClaimCancel. Fires whenever sessionID resolved,
	// unconditionally; a session with no pending set is a cheap no-op. ---
	al.cancelPendingAskForScope(sessionID)

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
	// Fallback (release/v0.1.1 cancel-cascade fix, restored in the merge
	// review — the resolution had kept this function and its CancelOutcome
	// documentation but dropped the one production call site): when the
	// PRIMARY resolved turn could not be claimed — most commonly because it
	// already fired from an earlier, unrelated cancel — a DIFFERENT live,
	// never-canceled turn sharing the session (a background/Critical async
	// delegate is the common case) may still be claimable. Without this,
	// the whole descendant cascade and the turn_canceled transcript/audit
	// write sit behind wasFired computed from that ONE turn alone, so the
	// claimable descendant is silently skipped. See
	// claimAnyTurnForSession's doc comment (turn.go) and
	// cancel_descendant_fallback_test.go.
	// sessionID != "" restores the base-release guard the merge restoration
	// dropped: turns with an empty routingSessionID legally exist (system
	// messages with no async transcript id, channel messages with no
	// msg.SessionID), and a Tier B cancel in an idle chat resolves
	// sessionID to "" — without the guard, claimAnyTurnForSession("")
	// claims whichever unrelated empty-routing-id turn it scans first,
	// consuming its first-cancel-wins latch and reporting fired=true for a
	// cancel that reached nothing.
	if !wasFired && !armed && sessionID != "" {
		if fallback := al.claimAnyTurnForSession(sessionID); fallback != nil {
			activeTurn = fallback
			wasFired = true
		}
	}

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
	// [SUPERSEDED, 2026-08-04] ADR-057 FR-024 used to mandate computing this
	// list EXACTLY ONCE here and threading it verbatim through PHASE B/C
	// (al.liveTurnStatesAmong(descendants)) rather than re-deriving the
	// subtree afresh. That rule is exactly what let a sub-turn that
	// registers AFTER this point — most commonly a `delegate async=true`
	// spawn, whose parent turn frequently finishes gracefully within
	// milliseconds while the backgrounded spawnSubTurn goroutine keeps
	// running and registers its child moments later — escape PHASE B/C
	// entirely: the frozen snapshot below never named the late child, so
	// al.liveTurnStatesAmong(descendants) filtered it out of existence no
	// matter how long it kept running. The operator's own diagnosis:
	// "either the list needs to be updated when something new starts, or it
	// must be done like a chain reaction: each parent cancels first his
	// children before its cancellation concludes" — chosen as the stronger
	// of the two, because it closes the race BY CONSTRUCTION rather than by
	// hoping a periodic re-scan lands often enough. PHASE B/C (below) now
	// re-derive the live subtree FRESH at each checkpoint
	// (al.collectDescendantTurnIDs(sessionID) again, not the snapshot this
	// variable holds) and additionally arm a chain-reaction latch
	// (armChainReactionCancelLatch) whenever a delegate spawn is still
	// in-flight but not yet registered (hasPendingDescendantSpawn) — so a
	// child that registers even after every checkpoint here has already
	// fired is still caught, through the exact same
	// consumePreArmedCancel -> RequestCancel machinery a cancel arriving
	// before ANY turn ever registered already uses (cancel_prearm.go). That
	// recursive re-invocation gives the newly-caught turn its OWN fresh
	// PHASE A/B/C cycle — which applies this SAME re-derive-and-arm
	// discipline to ITS OWN children — so the induction holds at arbitrary
	// delegation depth, not just one level. See docs/internal/specs/
	// adr-057-session-unification-spec.md's FR-024 entry for the full
	// updated contract.
	//
	// This variable is KEPT (not deleted) for three uses that are still
	// correct as a PHASE-A-time snapshot: the CancelOutcome.Descendants
	// return value below (documented as "what PHASE A found," not a promise
	// about the whole cascade's eventual reach), the defensive
	// pre-collected-vs-Interrupt consistency check immediately below, and as
	// the SetOnCancelFinish callback's fallback ordering guarantee — the
	// callback itself now recomputes fresh at the moment it actually fires
	// (see that closure below) rather than closing over this variable, so
	// the audit/transcript record reflects reality at Finish() time, not
	// PHASE-A time.
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
		// [Chain-reaction supersession of ADR-057 FR-024 — NOT a fresh
		// recompute here, deliberately] This callback reports the PHASE-A
		// snapshot (`descendants`), captured once, above, at the moment this
		// cancel activated — NOT a fresh al.collectDescendantTurnIDs(sessionID)
		// call made right here. That was tried and reverted: runTurn's own
		// cleanup order is `defer ts.Finish(...)` registered BEFORE
		// `defer al.clearActiveTurn(ts)` in the SAME function (loop.go), and
		// Go defers run LIFO — so clearActiveTurn ALWAYS runs BEFORE Finish()
		// for the very turn whose Finish() is invoking this callback right
		// now. By the time this closure runs, THIS turn's own entry (and, in
		// the common single-descendant case, therefore the ENTIRE tree) has
		// already been removed from al.activeTurnStates — a fresh re-scan at
		// this exact point finds fewer entries than PHASE A did not because
		// less was reached, but because cleanup already ran. Using it here
		// would UNDER-report, not correct, the audit trail (confirmed by
		// TestRepro_AsyncDelegateCancel_ArmsBeforeChildRegisters going from
		// green to red the moment this was tried). The dynamic, "reflects
		// reality" reporting FR-030/requirement-3 calls for is instead
		// produced by the chain-reaction mechanism itself: each
		// recursively-caught late descendant (armChainReactionCancelLatch ->
		// consumePreArmedCancel -> a FRESH RequestCancel call) gets its OWN
		// independent PHASE A and therefore its OWN accurate
		// turn_canceled audit event, computed at THE MOMENT it was caught —
		// not by mutating this single event after the fact.
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
	// diverged, in which direction. descendantSetsMatch is ALSO consulted
	// (not just the missing/extra diff) so a duplicate-count-only divergence
	// — which stringSliceSetDiff's pure-set comparison cannot see, since
	// deduping both sides hides it — still fires this WARN; neither collector
	// should ever emit a duplicate turn id, so any count disagreement is
	// itself a genuine inconsistency worth surfacing.
	missingFromInterrupted, extraInInterrupted := stringSliceSetDiff(descendants, interrupted)
	if len(missingFromInterrupted) > 0 || len(extraInInterrupted) > 0 || !descendantSetsMatch(descendants, interrupted) {
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

	// --- PHASE B: hard-abort timer → hard abort if ANY turn CURRENTLY live
	// in sessionID's tree is still alive, OR arm a chain-reaction latch if a
	// delegate spawn for this identity is still in flight ---
	//
	// [Chain-reaction supersession of ADR-057 FR-024, see the doc comment at
	// this function's PHASE-A `descendants` collection above for the full
	// rationale.] Gated on a FRESH re-scan (al.collectDescendantTurnIDs
	// (sessionID), called again right here — NOT the PHASE-A snapshot this
	// file used to thread through), because activeTurn.IsAlive() alone is
	// insufficient (a background delegate sub-turn can outlive its
	// already-gracefully-finished parent) AND the frozen PHASE-A list is
	// insufficient too (a delegate spawned moments before the Stop can
	// register a brand-new child at any point in this window — the late
	// registration is, structurally, indistinguishable from "was already
	// there," since routingSessionID is inherited verbatim by every member
	// of this chat's tree regardless of when it registers). Re-scanning here
	// is a flat Range match on routingSessionID (see collectDescendantTurnIDs,
	// steering.go) — never a graph walk — so there is no cycle or
	// unbounded-recursion hazard in calling it again at each checkpoint.
	// Snapshot BOTH escalation delays synchronously, on THIS goroutine, before
	// scheduling PHASE B's timer — never re-read the package-level vars from
	// inside the async callback below. cancelHardAbortDelay was already safe
	// (time.AfterFunc evaluates its first argument immediately, in the
	// caller's goroutine), but cancelDetachDelay was read directly at PHASE
	// C's own time.AfterFunc call INSIDE the PHASE-B callback — i.e. ~150ms
	// (test-shrunk) or ~3s (production) after RequestCancel returned. Tests
	// that shrink these vars restore them via t.Cleanup the moment they
	// observe hardAbortRequested() flip true (PHASE B's FIRST externally
	// visible side effect, set a few lines above the old cancelDetachDelay
	// read) — but the SAME callback goroutine keeps running past that point
	// to reach the old read, with no happens-before edge to the test's
	// Cleanup. That let one test's teardown race a PRIOR (or this test's own,
	// still in-flight) PHASE-B callback's read of the global — a genuine
	// data race (WARNING: DATA RACE, cancel.go:697 vs cancel_chain_reaction_
	// test.go:98/101), not a flake. Capturing both delays up front removes
	// the later read entirely; production behavior is unchanged since these
	// vars are never mutated outside tests.
	hardAbortDelay := cancelHardAbortDelay
	detachDelay := cancelDetachDelay
	time.AfterFunc(hardAbortDelay, func() {
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
		liveNow := al.liveTurnStatesAmong(al.collectDescendantTurnIDs(sessionID))
		pendingSpawn := al.hasPendingDescendantSpawn(sessionID, scope)
		if len(liveNow) == 0 {
			if pendingSpawn {
				// Nothing is alive right now, but a delegate spawn for this
				// identity is already dispatched and has not registered yet
				// (MarkPendingDelegateSpawn fired, spawnSubTurn hasn't
				// reached registerActiveTurn). Arm a chain-reaction latch so
				// the INSTANT it registers, consumePreArmedCancel (turn.go's
				// registerActiveTurn) catches it and re-invokes RequestCancel
				// for it — the same mechanism a cancel arriving before any
				// turn ever registered already uses (cancel_prearm.go) — this
				// closes the race for a child that registers even AFTER this
				// checkpoint (and PHASE C below) have already run.
				al.armChainReactionCancelLatch(sessionID, scope, canceller, hooks)
			}
			return // nothing alive right now, and PHASE C below only makes
			// sense once InterruptSessionHard has actually fired against
			// something — the armed latch (if any) is this branch's own
			// complete protection for whatever is still pending.
		}
		if _, err := al.InterruptSessionHard(sessionID, ScopeSubtree, hint); err != nil {
			slog.Warn("agent: RequestCancel: hard abort failed",
				"session_id", sessionID, "error", err)
		}
		if hooks.SendStageFrame != nil {
			hooks.SendStageFrame(sessionID, "hard")
		}
		if pendingSpawn {
			// A DIFFERENT delegate spawn may still be in flight even though
			// something else in the tree was alive and just got hard-aborted
			// above — arm defensively so that spawn is not lost either.
			al.armChainReactionCancelLatch(sessionID, scope, canceller, hooks)
		}

		// --- PHASE C: detach timer, measured from PHASE B's own hard abort →
		// detach any turn CURRENTLY live in sessionID's tree (same fresh
		// re-scan discipline as PHASE B; see above) ---
		hardAt := time.Now()
		time.AfterFunc(detachDelay, func() {
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
			stillAlive := al.liveTurnStatesAmong(al.collectDescendantTurnIDs(sessionID))
			pendingSpawnAtC := al.hasPendingDescendantSpawn(sessionID, scope)
			if len(stillAlive) == 0 {
				if pendingSpawnAtC {
					// This is the LAST scheduled checkpoint — no further
					// timer re-scans this identity after this point. Arming
					// here is the decisive close for a spawn that is still
					// slow to register: however much later it eventually
					// reaches registerActiveTurn, consumePreArmedCancel finds
					// this latch and gives it its own full cancel cascade.
					al.armChainReactionCancelLatch(sessionID, scope, canceller, hooks)
				}
				return // finished in the meantime
			}
			for _, ts := range stillAlive {
				ts.MarkAbandoned()
			}
			if pendingSpawnAtC {
				al.armChainReactionCancelLatch(sessionID, scope, canceller, hooks)
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

// descendantSetsMatch reports whether a and b contain exactly the same
// elements with exactly the same MULTIPLICITIES (duplicate counts),
// independent of order — a stricter check than stringSliceSetDiff's pure-set
// comparison, which dedupes both sides and so cannot see a duplicate-count-
// only divergence (e.g. a=[x,x,y], b=[x,y,y]: both reduce to the SAME set
// {x,y}, so stringSliceSetDiff reports no mismatch, even though the two
// lists disagree on how many times x/y each appear). collectDescendantTurnIDs
// and InterruptSession should never emit a duplicate turn id in the first
// place, so any duplicate-count disagreement between the two collection
// passes is itself a genuine inconsistency worth a WARN — see this
// function's use in RequestCancel's PHASE-A/Interrupt consistency check
// below, alongside stringSliceSetDiff's richer missing/extra reporting.
func descendantSetsMatch(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	counts := make(map[string]int, len(a))
	for _, v := range a {
		counts[v]++
	}
	for _, v := range b {
		counts[v]--
	}
	for _, c := range counts {
		if c != 0 {
			return false
		}
	}
	return true
}

// stringSliceSetDiff compares two string slices as SETS (not as ordered/
// counted sequences) and returns (onlyInA, onlyInB): the elements present in
// a but absent from b, and vice versa. Both returns are nil when the two
// slices contain exactly the same set of elements, regardless of order or
// duplicate count. Used by RequestCancel's PHASE-A/Interrupt consistency
// check (FIX-5, Defect 3b) to catch a same-SIZE-but-different-MEMBERSHIP
// mismatch that a bare len(a) != len(b) comparison cannot see. Paired with
// descendantSetsMatch (above) to also catch a duplicate-count-only
// divergence this pure-set diff cannot see on its own.
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
// the role-B predicates plus WS-payload stamping plus the pre-arm reads
// (exact census: routing_session_id_consumer_set_adr057_test.go) — so this
// walk is unaffected by that restriction.
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
// without adding an illegitimate read site of its own (the closed census
// lives in routing_session_id_consumer_set_adr057_test.go).
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
// (turnState.IsAlive()).
//
// [Chain-reaction supersession of ADR-057 FR-024] This function's OWN
// filtering behavior is unchanged — it still just narrows a given id list
// down to the still-live subset. What changed is what RequestCancel's PHASE
// B/C escalation timers now pass as ids: a FRESH al.collectDescendantTurnIDs
// (sessionID) call made again at each checkpoint, not the single list PHASE A
// computed once and threaded through verbatim. Widening the escalation's
// reach to a turn that registered AFTER PHASE A's snapshot is now the
// INTENDED behavior, not a hazard to guard against — see the doc comment on
// RequestCancel's PHASE-A `descendants` collection (cancel.go) for the full
// rationale for why the old "never widen beyond the snapshot" rule was itself
// the bug (a late-registering sub-turn escaped the cascade entirely).
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

// hasPendingDescendantSpawn reports whether a delegate sub-turn spawn is
// currently in flight for the SAME identity this RequestCancel call is
// scoped to (sessionID, or (scope.Channel, scope.ChatID) when sessionID is
// empty) — i.e. pkg/tools/delegate.go's executeAsync has already called
// MarkPendingDelegateSpawn (via the DelegateSpawnMarker seam) but the spawned
// goroutine has not yet reached registerActiveTurn. See cancel_prearm.go's
// pendingSpawns field for the full mark/clear/TTL contract.
//
// This is the second half of the chain-reaction fix (the first half is the
// fresh re-scan liveTurnStatesAmong's callers now perform): a pending spawn
// is evidence of a child that WILL exist soon but does not exist in
// al.activeTurnStates YET, so no re-scan — however fresh — can find it. Nil
// receiver-safe like every other cancelPreArm lookup (bare turnState-only
// unit tests that never went through NewAgentLoop leave al.cancelPreArm nil).
func (al *AgentLoop) hasPendingDescendantSpawn(sessionID string, scope CancelScope) bool {
	if al.cancelPreArm == nil {
		return false
	}
	return al.cancelPreArm.hasPendingSpawn(time.Now(), pendingSpawnKeys(sessionID, scope.Channel, scope.ChatID)...)
}

// armChainReactionCancelLatch arms a pre-registration cancel latch under the
// SAME identity key (preArmKeyForScope) this RequestCancel call is already
// scoped to, using the ORIGINAL scope/canceller/hooks — so that whichever
// delegate spawn hasPendingDescendantSpawn detected as in-flight is caught
// the INSTANT it registers, via the EXACT SAME
// consumePreArmedCancel -> RequestCancel machinery cancel_prearm.go already
// uses for "a cancel arrives before any turn has registered at all".
//
// This closes a gap that mechanism's own top-level entry point
// (armCancelOrFindActiveTurn) cannot reach: it only runs when RequestCancel's
// OWN initial lookup finds NO active turn at all (the `if activeTurn == nil`
// branch, above in this file) — never when a turn WAS found and claimed,
// which is exactly PHASE B/C's situation (the root, or an already-known
// descendant, was found and is being escalated while a NEW, not-yet-
// registered sibling spawn is also in flight).
//
// The recursive RequestCancel call this latch's eventual consumption
// triggers gives the newly-caught turn its OWN full PHASE A/B/C cycle — which
// re-applies this SAME hasPendingDescendantSpawn/armChainReactionCancelLatch
// pair for ITS OWN children — so induction closes the race at arbitrary
// delegation depth: there is no bespoke per-depth bookkeeping here, only the
// SAME two functions being invoked again by the recursive call.
//
// No infinite-recursion or unbounded-depth hazard: unlike
// CollectDescendantSessionIDs's durable ParentDurableKey BFS (which needs its
// own `visited` set because a corrupt/cyclic persisted graph is a real
// possibility), this recursion is driven entirely by REAL, freshly-created
// turnState registrations — registerActiveTurn is called at most once per
// actual spawned turn, and a latch is consumed AT MOST once
// (cancelPreArm.consume's exactly-once delete), so the recursion depth is
// bounded by the real, already depth/concurrency-limited delegation tree, not
// by anything this mechanism could loop on by itself.
//
// Idempotent (armLocked simply overwrites the same key, so calling this more
// than once for the same still-pending spawn is harmless) and nil-safe (a
// nil al.cancelPreArm or an unresolvable key — CancelScope with neither a
// session id nor a (channel, chatID) pair — is a silent no-op, mirroring
// every other method on cancelPreArm).
func (al *AgentLoop) armChainReactionCancelLatch(sessionID string, scope CancelScope, canceller CancelCanceller, hooks CancelHooks) {
	if al.cancelPreArm == nil {
		return
	}
	key := preArmKeyForScope(sessionID, scope)
	if key == "" {
		return
	}
	expired := al.cancelPreArm.arm(key, &cancelPreArmLatch{
		scope:     scope,
		canceller: canceller,
		hooks:     hooks,
		armedAt:   time.Now(),
		ttl:       cancelPreArmTTL, // captured once, here — see cancelPreArmLatch.ttl's doc comment (cancel_prearm.go)
	})
	if len(expired) > 0 {
		go notifyLatchExpired(expired...)
	}
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
