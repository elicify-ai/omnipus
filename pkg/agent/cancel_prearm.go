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
package agent

import (
	"context"
	"log/slog"
	"sync"
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

// cancelPreArmLatch is a single early-arriving cancel recorded because
// RequestCancel found no live turn to claim at the moment it ran.
type cancelPreArmLatch struct {
	scope     CancelScope
	canceller CancelCanceller
	hooks     CancelHooks
	armedAt   time.Time
}

// expired reports whether now is at or past the latch's TTL deadline.
func (l *cancelPreArmLatch) expired(now time.Time) bool {
	return now.Sub(l.armedAt) > cancelPreArmTTL
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
}

func newCancelPreArm() *cancelPreArm {
	return &cancelPreArm{latches: make(map[string]*cancelPreArmLatch)}
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
				"key", key, "armed_at", l.armedAt, "ttl", cancelPreArmTTL)
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

	expired := al.cancelPreArm.armLocked(key, &cancelPreArmLatch{
		scope:     scope,
		canceller: canceller,
		hooks:     hooks,
		armedAt:   time.Now(),
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
			"ttl", cancelPreArmTTL,
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
