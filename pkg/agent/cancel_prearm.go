// Package agent — cancel_prearm.go
//
// Closes the "cancel arrives before its turn registers" race: RequestCancel
// runs, finds no turnState in al.activeTurnStates for the resolved identity
// (nothing has registered yet), and — pre-fix — simply reported Fired:false
// and returned. The turn then registers moments later (after session-worker
// dequeue, workspace-dir resolution, model-switch bookkeeping, etc. — all of
// which run between a message's arrival and registerActiveTurn, see
// runTurn's own call site, pkg/agent/loop.go) and runs to completion, having
// never seen the cancel. Observed repro: two Stop clicks, a full completion
// delivered anyway, and the orphan watchdog's ClaimCancel succeeding 22s
// later on the same session — proof the session WAS cancellable, just not
// at the moment the user asked.
//
// Fix: RequestCancel, when it finds no active turn, ARMS a latch — the
// original scope/canceller/hooks, keyed by the narrowest identity the caller
// actually supplied (session id when known; (channel, chatID) only as a
// fallback when it is not — see preArmKeyForScope) — instead of silently
// no-op'ing. registerActiveTurn (turn.go) checks for a matching latch
// immediately after storing a new turnState and, if one is armed and not
// expired, consumes it EXACTLY ONCE (atomic map delete under cancelPreArm.mu)
// and re-invokes RequestCancel with the ORIGINAL scope/canceller/hooks — so
// the turn that registers next under that identity is canceled through the
// exact same, already-tested machinery a cancel arriving a moment later
// (after registration) would have used: same audit trail, same transcript
// entry, same stage frames, same 3s/5s escalation timers.
//
// A latch with no bound would be worse than the bug it fixes: it would sit
// armed forever and cancel some later, wholly unrelated turn under the same
// session/chat identity. Two protections close that off:
//   - Exactly-once consumption (map delete is atomic and unconditional on
//     first read) — a latch can cancel AT MOST one turn, never two.
//   - A TTL (cancelPreArmTTL) — a latch older than the TTL is treated as
//     stale on the FIRST turn that would otherwise consume it: it is deleted
//     and reported not-found, and that turn runs untouched. This is the
//     guard against the case exactly-once consumption alone cannot cover: a
//     cancel whose turn never registers at all (e.g. the inbound message was
//     dropped — session-worker inbox full, validation failure, etc.) leaves
//     an orphaned latch that would otherwise sit armed until some much later,
//     unrelated message for the same session/chat identity walks in and
//     gets spuriously canceled.
//
// A THIRD protection, added after the TTL/exactly-once pair above shipped:
// arming itself is now gated on turnImminentForIdentity (below), not merely
// on "no active turn is registered right now." The two conditions are NOT
// the same thing — a session that just finished its only turn also has no
// active turn registered, and the pre-gate code armed identically for that
// case, which TestWS_Cancel_OnlyInterruptsTargetSession
// (pkg/gateway/websocket_multisession_test.go) caught: canceling a finished
// session emitted a server frame it should never have produced, and — the
// consequence that actually hurts users — a stale latch armed against a
// finished session would sit there for cancelPreArmTTL and cancel the NEXT,
// wholly unrelated turn the user starts in that same session, exactly the
// hazard the TTL/exactly-once pair above exists to bound. See
// turnImminentForIdentity's doc comment for the discriminator this adds and
// the alternatives rejected in its favor.
package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// cancelPreArmTTL bounds how long an armed pre-registration cancel latch
// remains eligible for consumption. Declared as a var (not const), mirroring
// session_worker.go's workerIdleTimeout, so tests can shrink it instead of
// sleeping the full duration.
//
// Sizing rationale: the observed race window (session-worker enqueue/
// dequeue, workspace-dir resolution, model-switch bookkeeping — everything
// that runs between a message's arrival and registerActiveTurn, see
// runTurn, pkg/agent/loop.go) was measured at 1-3s under load in the bug's
// own repro. 5s gives headroom for scheduler/admission-controller
// contention on a busy instance while staying far shorter than any
// realistic "user gave up on Stop and sent an unrelated new message"
// timescale — the failure mode an unbounded latch would recreate (see this
// file's top doc comment).
var cancelPreArmTTL = 5 * time.Second

// turnSettleGrace bounds how long turnImminentForIdentity's inTurn signal
// stays suppressed for an identity right after its own turn's
// clearActiveTurn (turn.go) has run. Declared as a var (not const) so tests
// can shrink it instead of sleeping the full duration.
//
// Why this exists: sessionWorker.inTurn (session_worker.go) is true for the
// ENTIRE span of processTurn — from the moment a message is dequeued all
// the way through its post-turn steering-drain check, typing-stop
// notification, and response-guard/panic-recover defers — not just the
// pre-registration sub-window this file's turnImminentForIdentity cares
// about. A turn that has ALREADY registered and cleared (clearActiveTurn
// already ran, well before the client-visible "done" frame — see runTurn's
// own defer-ordering comment, pkg/agent/loop.go) still leaves inTurn=true
// for that short in-memory tail. TestWS_Cancel_OnlyInterruptsTargetSession
// (pkg/gateway/websocket_multisession_test.go) caught this directly: its
// cancel frame, sent the instant the client sees "done", can reach
// armCancelOrFindActiveTurn while the SAME goroutine's processTurn tail is
// still unwinding server-side — inTurn reads true, and without this grace
// window the fix would arm on exactly the finished-session case it exists
// to stop arming on.
//
// Sizing rationale: the tail this suppresses is pure in-memory bookkeeping
// (a queue-depth check, a map lookup, a few deferred closures) with no I/O —
// microseconds in practice, not the 1-3s-under-load pre-registration window
// cancelPreArmTTL's own rationale measures (two to three orders of magnitude
// larger). 250ms is chosen to comfortably cover realistic scheduler
// contention on that tail while staying far short of a genuine next-turn
// registration, so it does not meaningfully re-open the race this whole
// mechanism exists to close.
//
// Residual risk this DOES accept, documented rather than hidden: a second,
// genuinely NEW message for the SAME identity that is dequeued and enters
// its OWN pre-registration window within this grace period of the FIRST
// turn's clear — an unusually fast back-to-back follow-up landing during an
// unusually slow registration — could have a cancel arriving in that narrow
// overlap wrongly suppressed. This is a narrow, low-probability coincidence
// (both an immediate follow-up AND a slow second registration have to occur
// together) rather than the deterministic, 100%-reproducible false positive
// this fix closes, and is called out in this fix's own report.
var turnSettleGrace = 250 * time.Millisecond

// cancelPreArmLatch is a single early-arriving cancel recorded because
// RequestCancel found no live turn to claim at the moment it ran.
type cancelPreArmLatch struct {
	scope     CancelScope
	canceller CancelCanceller
	hooks     CancelHooks
	armedAt   time.Time
	// ttl is cancelPreArmTTL's value CAPTURED ONCE at arm time (armLocked),
	// not read live from the mutable package var thereafter. This is a
	// correctness fix, not just style: notifyLatchExpired runs on its own
	// detached goroutine (this file's own doc comment on that function),
	// which can still be alive after the arming test function has returned
	// and a LATER test's t.Cleanup (or a concurrent test) mutates
	// cancelPreArmTTL — a live re-read there raced under -race
	// (WARNING: DATA RACE between notifyLatchExpired's log call and
	// TestPreArmedCancel_ExpiredLatch_DoesNotCancelSubsequentTurn's
	// t.Cleanup restoring the shrunk TTL). A latch's own arm-time TTL is
	// immutable, latch-owned data — reading it never races with anything.
	ttl time.Duration
}

// expired reports whether now is at or past the latch's TTL deadline.
func (l *cancelPreArmLatch) expired(now time.Time) bool {
	return now.Sub(l.armedAt) > l.ttl
}

// cancelPreArm is the AgentLoop-wide table of armed pre-registration cancel
// latches, keyed by the identity string preArmKeyForScope/preArmKeysForTurn
// produce. A single mutex (not a striped pool) guards the whole table:
// cancel attempts that find no active turn are rare events, not a
// per-request hot path, so the extra concurrency a striped lock would buy
// is not needed — mirrors cancelAbuseDetector's own single-mutex-guards-
// whole-map shape (cancel_abuse.go).
type cancelPreArm struct {
	mu      sync.Mutex
	latches map[string]*cancelPreArmLatch

	// settled records, per identity key (same key space as latches —
	// preArmKeyForScope/preArmKeysForTurn), the timestamp of the most recent
	// clearActiveTurn (turn.go) call for that identity. Read by
	// turnImminentForIdentity to suppress a sessionWorker.inTurn=true
	// reading that is merely that identity's own just-finished turn's
	// post-clear tail — see turnSettleGrace's doc comment for the full
	// rationale. Guarded by the same mu as latches (not a hot path).
	settled map[string]time.Time
}

func newCancelPreArm() *cancelPreArm {
	return &cancelPreArm{
		latches: make(map[string]*cancelPreArmLatch),
		settled: make(map[string]time.Time),
	}
}

// markSettled records that a turn just cleared for each of keys, opportunistically
// pruning any settled entries that have already aged out past turnSettleGrace
// (mirrors armLocked's own opportunistic-eviction shape, keeping this map from
// growing unbounded across a long-running process without a background sweep
// goroutine). Empty keys are skipped. Safe to call with a nil receiver (no-op)
// so clearActiveTurn does not need its own nil check beyond the existing
// al.cancelPreArm nil guard.
func (p *cancelPreArm) markSettled(now time.Time, keys ...string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for k, t := range p.settled {
		if now.Sub(t) > turnSettleGrace {
			delete(p.settled, k)
		}
	}
	for _, key := range keys {
		if key == "" {
			continue
		}
		p.settled[key] = now
	}
}

// recentlySettledLocked reports whether any of keys had a clearActiveTurn
// recorded (via markSettled) within the last turnSettleGrace, as of now.
// Callers MUST hold p.mu (or, for a nil p, nothing at all — see the nil
// check below). Deliberately NOT self-locking: turnImminentForIdentity is
// called ONLY from armCancelOrFindActiveTurn's own critical section, which
// ALREADY holds al.cancelPreArm.mu for its entire body (cancel_prearm.go) —
// a self-locking variant called from there would re-lock the same
// non-reentrant sync.Mutex the caller is still holding and deadlock every
// cancel attempt that reaches it. There is currently only one caller
// (turnImminentForIdentity), and it always holds the lock; a future
// external, not-already-locked caller would need its own thin
// self-locking wrapper (p.mu.Lock(); defer p.mu.Unlock(); return
// p.recentlySettledLocked(...)) rather than reusing this one directly.
//
// A nil receiver or an empty/absent key reports false (never suppress —
// the safe default, matching this file's "when in doubt, do not treat as
// settled, let turnImminentForIdentity fall through to its inTurn/inbox
// signal" posture).
func (p *cancelPreArm) recentlySettledLocked(now time.Time, keys ...string) bool {
	if p == nil {
		return false
	}
	for _, key := range keys {
		if key == "" {
			continue
		}
		if t, ok := p.settled[key]; ok && now.Sub(t) <= turnSettleGrace {
			return true
		}
	}
	return false
}

// armLocked records latch under key, opportunistically evicting any entries
// that have already expired (the table is expected to stay tiny and
// short-lived, so a full sweep on every arm is cheap and keeps a pathological
// case — e.g. a Tier B channel/chatID whose turn never registers at all —
// from growing the map unbounded). Callers MUST hold p.mu.
//
// Returns the latches this sweep evicted as expired (nil when none), so the
// caller can notify each one's ORIGINAL requester — this is the ONLY
// discovery point for a latch whose target turn never registers at all
// (e.g. a dropped Tier B message); consume (below) only ever sees a latch
// that a LATER turn's own registration attempt happens to check against.
// Without surfacing this list, that case expired in total silence — no log,
// no hook, nothing — even though a real Stop click was acknowledged
// (CancelOutcome.Armed) and then simply never took effect.
func (p *cancelPreArm) armLocked(key string, latch *cancelPreArmLatch) []*cancelPreArmLatch {
	var expired []*cancelPreArmLatch
	for k, l := range p.latches {
		if l.expired(latch.armedAt) {
			expired = append(expired, l)
			delete(p.latches, k)
		}
	}
	p.latches[key] = latch
	return expired
}

// consume atomically removes and returns the latch filed under the first of
// keys that has one armed, or (nil, false, nil) when none is. A
// found-but-expired latch is deleted and reported as not-found via the first
// two return values — this is what guarantees an expired latch never
// cancels the turn that finds it (half (b) of the fix's contract) — and is
// ALSO returned as the third value so the caller can notify its original
// requester that the cancel it armed never landed (see notifyLatchExpired).
func (p *cancelPreArm) consume(now time.Time, keys ...string) (latch *cancelPreArmLatch, ok bool, expiredLatch *cancelPreArmLatch) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, key := range keys {
		if key == "" {
			continue
		}
		l, exists := p.latches[key]
		if !exists {
			continue
		}
		delete(p.latches, key)
		if l.expired(now) {
			slog.Debug("agent: cancelPreArm: latch expired before a turn consumed it",
				"key", key, "armed_at", l.armedAt, "ttl", l.ttl)
			return nil, false, l
		}
		return l, true, nil
	}
	return nil, false, nil
}

// preArmKeyForScope returns the identity key a pre-registration cancel latch
// is filed under for scope, or "" when scope carries neither a resolvable
// session id nor a (channel, chatID) pair. sessionID is the caller's own
// already-resolved session id — non-empty either because the caller
// supplied CancelScope.SessionID directly (the primary case: web SPA, CLI,
// Tier A /cancel all know the session id up front) or because
// RequestCancel's Tier B resolution found an EXISTING turn for
// (channel, chatID) and read its transcriptSessionID. That resolution is
// exactly what fails — leaving sessionID empty — in the pre-registration
// race this mechanism exists to close, which is why the (channel, chatID)
// form below is the fallback key, never the primary one: it is the only
// identity a Tier B caller can supply before any turn (and therefore any
// session id) exists at all.
func preArmKeyForScope(sessionID string, scope CancelScope) string {
	if sessionID != "" {
		return "s:" + sessionID
	}
	if scope.Channel != "" && scope.ChatID != "" {
		return "c:" + scope.Channel + ":" + scope.ChatID
	}
	return ""
}

// preArmKeysForTurn returns the key(s) a newly-registered turn should be
// checked against, most-specific first: the session-id form when the turn
// has a transcript session id (matching the primary, web-SPA/CLI-style
// cancel path), then the (channel, chatID) form when the turn carries both
// (matching the Tier B fallback path). Checking both is cheap and correct
// even when only one could plausibly be armed for a given turn.
func preArmKeysForTurn(ts *turnState) []string {
	var keys []string
	if ts.transcriptSessionID != "" {
		keys = append(keys, "s:"+ts.transcriptSessionID)
	}
	if ts.channel != "" && ts.chatID != "" {
		keys = append(keys, "c:"+ts.channel+":"+ts.chatID)
	}
	return keys
}

// armCancelOrFindActiveTurn is called by RequestCancel exactly when its own
// initial (unlocked, best-effort) lookup found no active turn for scope. It
// re-resolves under al.cancelPreArm.mu — the SAME lock consumePreArmedCancel
// (below) takes around its own check — so "is there a live turn right now"
// and "arm a latch for one that is not here yet" become one atomic unit
// relative to a concurrently-registering turn. This is what actually closes
// the race, not just narrows it:
//
// registerActiveTurn always (1) stores the turnState into activeTurnStates,
// THEN (2) calls consumePreArmedCancel, which takes al.cancelPreArm.mu — in
// that order, on the turn's own goroutine. Because (2) takes the same lock
// this method holds for its whole critical section, (2) can only ever run
// entirely before this method's critical section starts or entirely after
// it ends — never during it (ordinary mutex mutual exclusion). Case split:
//
//   - If the turn's Store (1) happens-before this method's critical section
//     starts: the re-check below (which reads activeTurnStates the Store
//     wrote) finds the turn directly. No latch is armed; the caller falls
//     through to the ordinary ClaimCancel path.
//   - If this method's critical section runs to completion (re-check finds
//     nothing, latch armed, lock released) before the turn's Store even
//     happens: then the turn's own (2) necessarily runs after its (1),
//     which is necessarily after this method released the lock — so (2)
//     finds and consumes the latch this method armed.
//
// There is no interleaving in which both sides observe "nothing" — the lock
// makes the two checks mutually exclusive, and program order on the turn's
// own goroutine guarantees its Store always precedes its own latch check.
//
// Returns the found TurnCancelHook (nil if none was found), and whether a
// latch was armed in its place.
func (al *AgentLoop) armCancelOrFindActiveTurn(
	key string,
	sessionID string,
	scope CancelScope,
	canceller CancelCanceller,
	hooks CancelHooks,
) (TurnCancelHook, bool) {
	if al.cancelPreArm == nil {
		// Defensive: should not happen outside a bare turnState-only unit test
		// that never went through NewAgentLoop. Behave exactly like the
		// pre-fix code in that case rather than panicking.
		return nil, false
	}

	al.cancelPreArm.mu.Lock()
	defer al.cancelPreArm.mu.Unlock()

	var hook TurnCancelHook
	if sessionID != "" {
		hook = al.GetActiveTurnHookForSession(sessionID)
	} else if scope.Channel != "" && scope.ChatID != "" {
		if sid := al.resolveSessionIDByChannelChat(scope.Channel, scope.ChatID); sid != "" {
			hook = al.GetActiveTurnHookForSession(sid)
		}
	}
	if hook != nil {
		return hook, false
	}

	// Only arm when a turn is genuinely IMMINENT for this identity — not
	// merely because none is registered right now. "No active turn" is
	// equally true of a session about to start its first turn and one that
	// just finished its last turn with nothing coming; see
	// turnImminentForIdentity's doc comment for the discriminator and why an
	// unconditional arm here (the previous behavior) is itself the bug
	// TestWS_Cancel_OnlyInterruptsTargetSession caught. When nothing is
	// imminent this is a genuine no-op: Fired stays false, Armed is now
	// false too (not a latch standing in for it), and no stale latch is left
	// behind to poison whatever turn this identity runs next.
	if !al.turnImminentForIdentity(sessionID, scope) {
		return nil, false
	}

	expired := al.cancelPreArm.armLocked(key, &cancelPreArmLatch{
		scope:     scope,
		canceller: canceller,
		hooks:     hooks,
		armedAt:   time.Now(),
		ttl:       cancelPreArmTTL, // captured once, here — see cancelPreArmLatch.ttl's doc comment
	})
	if len(expired) > 0 {
		// Notify off this goroutine, and specifically BEFORE this method's own
		// deferred mu.Unlock() has necessarily run — safe regardless, because
		// notifyLatchExpired never touches al.cancelPreArm itself, only the
		// ORIGINAL caller's own hooks captured on each expired latch.
		go notifyLatchExpired(expired...)
	}
	return nil, true
}

// consumePreArmedCancel is called by registerActiveTurn immediately after
// storing ts into activeTurnStates. If a non-expired latch was armed for
// ts's identity (session id, or (channel, chatID) fallback), it is consumed
// exactly once here and RequestCancel is re-invoked synchronously — now that
// ts IS registered and reachable via GetActiveTurnHookForSession — with the
// ORIGINAL caller's scope/canceller/hooks.
//
// Synchronous, not spawned in a goroutine: registerActiveTurn runs on the
// turn's own goroutine before any real turn work (message assembly, model
// switch, the LLM call itself) begins, so by the time this call returns,
// RequestCancel's InterruptSession has already fired providerCancel/
// requestGracefulInterrupt on ts — the turn cannot slip an LLM round-trip
// through before observing the cancellation.
func (al *AgentLoop) consumePreArmedCancel(ts *turnState) {
	if al.cancelPreArm == nil {
		return
	}
	latch, ok, expired := al.cancelPreArm.consume(time.Now(), preArmKeysForTurn(ts)...)
	if expired != nil {
		// Same off-goroutine notification as armCancelOrFindActiveTurn's own
		// sweep — this turn's registration is exactly the "later, unrelated
		// check" discovery point (Path 1) rather than armLocked's
		// opportunistic sweep (Path 2); both funnel into the same helper so
		// the original requester learns their Stop never landed either way.
		go notifyLatchExpired(expired)
	}
	if !ok {
		return
	}
	if _, err := al.RequestCancel(context.Background(), latch.scope, latch.canceller, latch.hooks); err != nil {
		slog.Warn("agent: consumePreArmedCancel: RequestCancel failed on latch consumption",
			"turn_id", ts.turnID, "session_key", ts.sessionKey, "error", err)
	}
}

// notifyLatchExpired informs each expired latch's ORIGINAL requester
// (CancelHooks.OnLatchExpired, if the calling surface wired it) that the
// cancel it armed — and that CancelOutcome.Armed already acknowledged as
// "standing in for this cancel" — never actually reached a turn to cancel:
// cancelPreArmTTL elapsed with nothing consuming it. Always logged at Warn
// regardless of whether a hook is wired (this has a real, user-facing
// consequence — a Stop click silently failing — unlike consume's own
// Debug-level trace at the discovery site), so an operator has SOME signal
// even when the calling surface never wires the hook.
//
// Deliberately not run under al.cancelPreArm.mu — both call sites
// (armCancelOrFindActiveTurn's opportunistic sweep, consumePreArmedCancel's
// own expiry branch) launch this on its own goroutine specifically so an
// arbitrary caller-supplied hook is never invoked while that lock is held,
// matching armCancelOrFindActiveTurn's existing "never run caller code
// inside this critical section" discipline. A hook panic is recovered and
// logged rather than crashing the process — the same defensiveness
// RequestCancel's own escalation timers apply to their hook calls
// (cancel.go).
//
// Deliberately does NOT widen cancelPreArmTTL or retry — the fix for
// "the Stop I clicked never took effect" is telling the truth about it, not
// making the window that produces it wider or quieter.
func notifyLatchExpired(expired ...*cancelPreArmLatch) {
	for _, latch := range expired {
		if latch == nil {
			continue
		}
		slog.Warn("agent: cancelPreArm: an armed cancel latch expired before any turn consumed it — "+
			"the Stop this latch stood in for never actually landed",
			"session_id", latch.scope.SessionID,
			"channel", latch.scope.Channel,
			"chat_id", latch.scope.ChatID,
			"canceller_user", latch.canceller.UserID,
			"canceller_channel", latch.canceller.Channel,
			"armed_at", latch.armedAt,
			"ttl", latch.ttl,
		)
		if latch.hooks.OnLatchExpired == nil {
			continue
		}
		func(l *cancelPreArmLatch) {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("agent: notifyLatchExpired: OnLatchExpired hook panicked",
						"panic", r, "session_id", l.scope.SessionID)
				}
			}()
			l.hooks.OnLatchExpired(l.scope, l.canceller)
		}(latch)
	}
}

// turnImminentForIdentity reports whether a turn is genuinely about to exist
// for the identity armCancelOrFindActiveTurn is considering arming a latch
// for (sessionID when known, otherwise (scope.Channel, scope.ChatID)) — as
// opposed to merely "no turn is registered right now," which is equally true
// of a session about to start its FIRST turn and one that just finished its
// LAST turn with nothing coming.
//
// Candidates considered, and why they lost to this one:
//
//   - Arm unconditionally whenever nothing is registered (the pre-fix
//     behavior this replaces): cannot tell "about to start" from "just
//     finished" at all — proven wrong by
//     TestWS_Cancel_OnlyInterruptsTargetSession
//     (pkg/gateway/websocket_multisession_test.go), which cancels a session
//     immediately after its only turn's "done" frame and requires that
//     cancel to be a true no-op, not an acknowledged-but-armed one.
//   - A short time-based epsilon ("only arm if this identity's last turn
//     ended within the last N ms"): rejected as fragile and, worse, not
//     actually a discriminator — any finished session is trivially "recently
//     idle" for SOME N, and an N large enough to cover the real
//     scheduler/admission-controller contention this file's own TTL
//     rationale cites (1-3s under load) is large enough to misclassify a
//     truly finished session as "about to start" for that same window. It
//     just relocates the false positive instead of removing it.
//   - Session meta Status (pkg/session.SessionStatus): only has
//     Active/Archived/Interrupted values (pkg/session/daypartition.go) and
//     sits at StatusActive both WHILE a turn runs and immediately AFTER it
//     finishes — nothing flips it on ordinary turn completion, only
//     RequestCancel's own SetSessionInterrupted hook does, and only on a
//     real cancel. Rejected: it cannot distinguish the two states this
//     discriminator exists to tell apart.
//   - Tried, then refined: raw dispatcher state. Is there a message sitting
//     queued in, or already being processed (but not yet registered) by,
//     the per-scope sessionWorker (session_worker.go) that owns this
//     identity? A queued-but-undequeued message (sessionWorker.inbox,
//     buffered) is unambiguous physical evidence on its own. But
//     sessionWorker.inTurn taken RAW is not: it is true for processTurn's
//     ENTIRE span, including the brief post-clear tail after THIS
//     identity's own turn already registered AND cleared (the
//     steering-drain check, typing-stop notify, response-guard/
//     panic-recover defers) — a turn that finished and sent its client-
//     visible "done" frame moments ago still reads inTurn=true for that
//     tail. TestWS_Cancel_OnlyInterruptsTargetSession caught this directly
//     the first time this function shipped with raw inTurn: its cancel
//     frame, sent the instant the client sees "done", reached this
//     function while the SAME server goroutine's tail was still unwinding.
//   - Chosen: inbox depth unconditionally, PLUS inTurn gated on
//     turnSettleGrace (recentlySettledLocked, above) — inTurn only counts as
//     imminent when clearActiveTurn has NOT just run for this identity.
//     See turnSettleGrace's doc comment for the sizing rationale and the
//     narrow residual risk this trades in return (a genuinely new message
//     for the same identity, dequeued and mid-registration within that
//     grace window of the previous turn's clear).
//
// sessionWorkers is keyed by a routing-derived "scope" string
// (agentSessionKey / resolveSteeringTarget, pkg/agent/loop.go) this function
// cannot reconstruct exactly: CancelScope carries no agentID, and a Tier B
// (channel, chatID) pair may not even appear in the key under DMScopeMain
// (routing.BuildAgentPeerSessionKey collapses a direct-message peer to
// "agent:<id>:main" under that scope). Rather than duplicate routing/DMScope
// resolution here, this matches on the one part of the key format
// resolveSteeringTarget guarantees unconditionally: a literal ":"+sessionID
// suffix whenever msg.SessionID is non-empty (pkg/agent/loop.go,
// resolveSteeringTarget's final two lines) — that covers every SessionID-
// keyed caller (web SPA, Tier A /cancel, the orphan watchdog reap). For the
// (channel, chatID) fallback (Tier B, no session id resolved), matching is
// best-effort substring containment on the lower-cased scope string: sound
// for group/channel peers (which always key by peerID per
// routing.BuildAgentPeerSessionKey's "Group/channel peers always get
// per-peer sessions" rule) but blind to direct peers under DMScopeMain. That
// residual gap is real and is called out in this fix's own report rather
// than papered over — see this function's callers for the discussion.
func (al *AgentLoop) turnImminentForIdentity(sessionID string, scope CancelScope) bool {
	lowerChatID := strings.ToLower(scope.ChatID)
	lowerChannel := strings.ToLower(scope.Channel)
	settleKey := preArmKeyForScope(sessionID, scope)
	now := time.Now()

	imminent := false
	al.sessionWorkers.Range(func(key, value any) bool {
		wscope, _ := key.(string)
		w, ok := value.(*sessionWorker)
		if !ok || w == nil {
			return true // keep scanning
		}

		matched := false
		switch {
		case sessionID != "":
			matched = strings.HasSuffix(wscope, ":"+sessionID)
		case scope.Channel != "" && scope.ChatID != "":
			lw := strings.ToLower(wscope)
			matched = strings.Contains(lw, lowerChatID) && strings.Contains(lw, lowerChannel)
		}
		if !matched {
			return true // keep scanning
		}

		// A queued-but-undequeued message is unambiguous evidence on its
		// own: nothing has claimed it yet, so it can only mean "about to be
		// processed," never "leftover from an already-finished turn."
		if len(w.inbox) > 0 {
			imminent = true
			return false
		}
		// inTurn, by contrast, spans processTurn's ENTIRE call — including
		// the brief post-clear tail after THIS identity's own turn already
		// registered and cleared. Only count it as imminent when
		// clearActiveTurn has NOT just run for this identity — see
		// turnSettleGrace's doc comment for why (this is exactly what
		// TestWS_Cancel_OnlyInterruptsTargetSession caught: raw inTurn,
		// unsuppressed, false-positived on a finished session).
		// Locked variant: this whole function runs from within
		// armCancelOrFindActiveTurn's al.cancelPreArm.mu critical section —
		// see recentlySettledLocked's doc comment for why the self-locking
		// recentlySettled would deadlock here.
		if w.inTurn.Load() && !al.cancelPreArm.recentlySettledLocked(now, settleKey) {
			imminent = true
			return false
		}
		// Matched but idle (or settled): this is the "just finished" case,
		// not "about to start." Keep scanning in case a DIFFERENT worker
		// (unlikely, but Tier B's substring match is not exclusive) does
		// show imminent work.
		return true
	})
	return imminent
}

// ---------------------------------------------------------------------------
// Cross-package test seam
//
// turnImminentForIdentity's only production evidence source is
// al.sessionWorkers — a real, in-flight dispatcher. Constructing that
// evidence from a bare unit test (no real message dispatch) requires
// touching the unexported *sessionWorker type, which pkg/agent's OWN test
// files do via primeImminentSessionWorker/primeImminentSessionWorkerTierB
// (cancel_prearm_test.go). Those helpers cannot be reused by a DIFFERENT
// package's tests (pkg/gateway's WS integration suite, which drives cancel
// requests through a real *AgentLoop returned by its own test harness) —
// Go does not export a package's _test.go symbols across package boundaries,
// and pkg/gateway cannot construct pkg/agent's unexported sessionWorker type
// directly.
//
// The two exported methods below are that seam: they install the exact same
// evidence (a real sessionWorkers entry with inTurn=true) that
// primeImminentSessionWorker/TierB install, so a cross-package caller
// exercises turnImminentForIdentity's REAL production Range/inTurn read —
// not a stand-in for its boolean result. A helper that instead forced
// CancelOutcome.Armed to true directly would make the caller's test vacuous
// (it would keep passing even if turnImminentForIdentity were deleted
// outright), which is exactly the failure mode this fix's own report calls
// out.
//
// Guarded by testing.Testing() (stdlib, Go 1.21+): it reports false in every
// production binary, so these methods return an error instead of touching
// al.sessionWorkers outside `go test` — there is no config flag, env var, or
// build tag that can make them fire in production. This mirrors the same
// guard this codebase already uses for a test-only fallback
// (pkg/audit/hmac.go's resolveChainKey, gated the same way for the same
// reason: dev/test convenience that must never leak into a shipped binary).
// ---------------------------------------------------------------------------

// newFakeImminentSessionWorker builds a *sessionWorker suitable for
// registering directly into al.sessionWorkers from a test, WITHOUT ever
// starting its runLoop goroutine. It still needs to survive
// AgentLoop.Close() -> stopSessionWorkers (loop.go), which unconditionally
// calls w.cancel() and waits on w.done for every worker found in the map —
// a bare &sessionWorker{} has both as nil (nil func panics on call; a nil
// channel blocks forever, up to stopSessionWorkers' 5s per-worker budget).
// cancel is wired to a real (harmless) context.CancelFunc and done is
// pre-closed, so cleanup treats this fake worker as already exited.
//
// Single source of truth for both this file's exported cross-package seam
// and cancel_prearm_test.go's in-package primeImminentSessionWorker/TierB
// helpers, which call PrimeSessionImminentForTest/PrimeChannelChatImminentForTest
// below rather than duplicating this construction.
func newFakeImminentSessionWorker() *sessionWorker {
	_, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	close(done)
	w := &sessionWorker{cancel: cancel, done: done}
	w.inTurn.Store(true)
	return w
}

// PrimeSessionImminentForTest installs a minimal fake sessionWorker under a
// session-id scope key so a subsequent RequestCancel (and the
// turnImminentForIdentity gate it consults) observes genuine dispatcher
// evidence that a turn is imminent for sessionID — see the package doc
// comment immediately above for the full rationale and the guard this
// relies on.
//
// Returns an error (touching nothing) when called outside `go test` or with
// an empty sessionID; both are test-authoring bugs, not runtime conditions.
func (al *AgentLoop) PrimeSessionImminentForTest(sessionID string) error {
	if !testing.Testing() {
		return fmt.Errorf("agent: PrimeSessionImminentForTest is test-only and must not be called outside go test")
	}
	if sessionID == "" {
		return fmt.Errorf("agent: PrimeSessionImminentForTest requires a non-empty sessionID")
	}
	al.sessionWorkers.Store("test-scope:session:"+sessionID, newFakeImminentSessionWorker())
	return nil
}

// PrimeChannelChatImminentForTest is PrimeSessionImminentForTest's Tier B
// (channel, chatID) counterpart — see turnImminentForIdentity's doc comment
// for why the scope key must contain both, lower-cased, for its substring
// match. Same test-only guard and rationale.
func (al *AgentLoop) PrimeChannelChatImminentForTest(channel, chatID string) error {
	if !testing.Testing() {
		return fmt.Errorf("agent: PrimeChannelChatImminentForTest is test-only and must not be called outside go test")
	}
	if channel == "" || chatID == "" {
		return fmt.Errorf("agent: PrimeChannelChatImminentForTest requires non-empty channel and chatID")
	}
	al.sessionWorkers.Store("test-scope:"+channel+":"+chatID, newFakeImminentSessionWorker())
	return nil
}
