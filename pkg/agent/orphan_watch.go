// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package agent — orphan_watch.go implements the orphaned-foreground-turn
// watchdog (ADR-045): a single grace-period timer that, once elapsed with no
// reconnect, hands the abandoned session's ROOT turn to the EXISTING,
// battle-tested RequestCancel state machine (pkg/agent/cancel.go) — the same
// path every other cancel surface (web SPA Stop button, Tier A /cancel,
// Tier B text-parsing channels, CLI) already uses.
//
// This is a deliberate redesign (2026-07, post-review) away from an earlier
// version that REIMPLEMENTED cancellation from scratch — its own turn-scoped
// PHASE A/graceful, PHASE B/hard-abort, PHASE C/detach escalation, its own
// exported TurnCancelHook.RequestHardAbort, and its own audit/transcript
// writes. That bespoke machinery duplicated RequestCancel's logic while
// silently DROPPING the side effects RequestCancel performs for every other
// cancel surface — approval auto-deny (FR-7), background bash/exec session
// kill (FR-B10/FR-B11), session-status-interrupted, and MarkLastEntryTruncated
// — and a 7-reviewer gate found three sev-9 correctness/safety bugs in it
// (see docs/internal/architecture/ADR-045-orphaned-foreground-turn-timeout.md
// for the full writeup). Reusing RequestCancel directly dissolves that whole
// class of bug by construction: there is only one cancellation state machine
// in this codebase now, not two similar-but-subtly-different ones.
//
// The one property this redesign must never lose — the reason a naive
// "just call RequestCancel unconditionally" was rejected in the ORIGINAL
// ADR-045 decision — is that RequestCancel's PHASE B/C escalation
// (InterruptSessionHard, pkg/agent/cancel.go, gated on liveness of the
// PHASE-A-computed descendant set — ADR-057 FR-024) is SESSION-WIDE in
// reach: it hard-aborts every turnState sharing a routingSessionID that was
// still alive when the Stop/reap fired, Critical/background delegate
// sub-turns included, once they're still alive past the 3s mark. That is
// correct and desired for an explicit user Stop-click, but would be wrong
// for an automatic, WS-close-triggered timeout, which must NEVER touch a
// legitimate Critical background delegate no matter how long the user stays
// away (`delegate async=true` always sets Critical:true — see
// pkg/tools/delegate.go's executeAsync — so "Critical" and "background/
// async" name the identical turnState.critical flag).
//
// The fix is NOT a parallel escalation timer family (the old design) — it is
// simply: defer reaping entirely, for good, for this fire, whenever a live
// Critical/background delegate is found on the session. See
// fireOrphanForegroundTurnWatch's doc comment for the exact three-condition
// gate.
//
// Mechanism (docs/internal/architecture/ADR-045-orphaned-foreground-turn-timeout.md):
//
//   - Arm: called by the gateway's WS teardown defer when the closing
//     connection was the LAST one watching its session (multi-tab safety
//     checked by the caller), and only for a foreground CHAT session (MA-2;
//     Task/Scheduled/Channel/Heartbeat sessions are never watched — see
//     ArmOrphanForegroundTurnWatch). Starts a SINGLE grace-period timer keyed
//     by sessionID. The gateway supplies two callbacks: reap (invokes
//     RequestCancel with the orphan-timeout reason) and stillOrphaned (checks
//     whether any live connection has since reattached).
//
//   - Disarm: called by the gateway on reattach/reconnect (handleAttachSession,
//     the session-continuation branch of handleChatMessage) as soon as a live
//     connection is confirmed. Stops the pending timer. Calling Disarm AFTER
//     the timer has already fired-and-reaped is a harmless no-op on the turn
//     — the watch entry is already gone (fireOrphanForegroundTurnWatch
//     removes it from the map the instant it runs) — and a reconnect landing
//     during RequestCancel's OWN subsequent escalation window behaves
//     identically to a user clicking Stop and then reconnecting: there is no
//     "abort the cancel" mechanism for that case today, for ANY cancel
//     surface, so the orphan-reap path does not invent one either.
//
//   - Fire (grace elapses, no reattach): reap ONLY IF ALL of the following
//     hold (see fireOrphanForegroundTurnWatch for the implementation):
//     1. A genuine LIVE ROOT turn exists for the session (MA-1) — resolved
//     via the root-ONLY getActiveRootTurnStateForSession, never
//     GetActiveTurnHookForSession's non-root anyMatch fallback.
//     2. No surviving Critical/background/async delegate is alive on the
//     session (hasLiveCriticalDelegate).
//     3. No client has reconnected since arming (stillOrphaned()).
//     If all three hold, a dedicated turn.orphan_timeout audit event is
//     emitted (distinguishing this from a real user cancel — attributed to
//     "system:orphan-watchdog") and reap("orphan_timeout") is called exactly
//     once. reap in turn calls al.RequestCancel with the full CancelHooks a
//     web SPA cancel gets — approval auto-deny, background-session kill,
//     session-status-interrupted, and RequestCancel's own audit/transcript
//     writes and graceful->hard->detached escalation all apply uniformly.
//     Because condition 2 already guarantees no live Critical delegate
//     remains, RequestCancel's session-wide escalation cannot cascade into
//     anything this mechanism needs to protect.
package agent

import (
	"context"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// orphanWatch tracks a single armed (or firing) orphan-foreground-turn watch
// for one session. Only one watch is ever active per session — arming again
// cancels and replaces any previous entry (re-arm semantics).
type orphanWatch struct {
	mu    sync.Mutex
	timer *time.Timer // the grace-period timer; nil only in the brief window before Arm finishes constructing it
	// canceled is set by Disarm (or AgentLoop.Close). Checked at the top of
	// fireOrphanForegroundTurnWatch so a reconnect (or shutdown) that raced
	// the timer stops the fire from doing anything.
	canceled bool
}

// cancel marks the watch canceled and stops the pending grace timer if it has
// not fired yet. Safe to call multiple times (idempotent).
func (ow *orphanWatch) cancel() {
	ow.mu.Lock()
	defer ow.mu.Unlock()
	ow.canceled = true
	if ow.timer != nil {
		ow.timer.Stop()
	}
}

// isCanceled reports whether Disarm (or Close) has already canceled this
// watch.
func (ow *orphanWatch) isCanceled() bool {
	ow.mu.Lock()
	defer ow.mu.Unlock()
	return ow.canceled
}

// ArmOrphanForegroundTurnWatch starts (or restarts) the orphan-foreground-turn
// grace timer for sessionID (ADR-045). Called by the gateway's WS ServeHTTP
// teardown defer when the closing connection was the LAST one watching this
// session — the caller (pkg/gateway/websocket.go) is responsible for that
// multi-tab-safety check; this method itself only requires a sessionID, a
// resolved grace period, and the two callbacks below.
//
// reap is called exactly once, at most, if the grace period elapses and every
// safety condition in fireOrphanForegroundTurnWatch holds. It must perform
// the actual cancellation (the gateway wires this to a closure that calls
// al.RequestCancel with the same CancelHooks a web SPA Stop-click gets).
//
// stillOrphaned is called at fire time to give the caller one last chance to
// report "actually, someone reconnected" — closing the race between the
// grace timer firing and a reattach that arrived just before Disarm could
// run. The gateway wires this to a check of its live chatID->sessionID
// mappings.
//
// graceSeconds <= 0, or either callback being nil, disables the watchdog
// entirely (no-op) — matching the TimeoutSeconds: 0-disabled convention used
// elsewhere in this codebase. The gateway resolves the effective grace value
// from config.GatewayConfig via (*config.Config).EffectiveOrphanedTurnGraceSeconds
// before calling this method, so AgentLoop itself never reads config directly
// here.
//
// MA-2: only ever arms for a foreground CHAT session (session.SessionTypeChat).
// Task/Scheduled/Channel/Heartbeat sessions have no comparable "user walked
// away from a browser tab" signal to bound — their owning turn is meant to
// run to completion regardless of whether a browser happens to be watching —
// so arming for one and later reaping it would cut off legitimate, wanted
// work for the wrong reason. A session whose type cannot be determined at all
// (store/meta lookup failure) is treated as watchable: this is an explicit
// opt-out for KNOWN non-chat types, not a fail-closed gate.
//
// Also a no-op when no active turn is currently registered for sessionID —
// there is nothing to watch (e.g. the session's turn already finished before
// the last tab closed). Re-arming an already-armed session (a rapid
// disconnect/reconnect/disconnect sequence, or a fresh turn starting on the
// same session while a prior watch was still pending) cancels the PRIOR
// watch first so at most one timer is ever pending per session.
func (al *AgentLoop) ArmOrphanForegroundTurnWatch(
	sessionID string,
	graceSeconds int,
	reap func(reason string),
	stillOrphaned func() bool,
) {
	if sessionID == "" || graceSeconds <= 0 || reap == nil || stillOrphaned == nil {
		return
	}

	if store := al.ResolveSessionStore(sessionID); store != nil {
		meta, err := store.GetMeta(sessionID)
		switch {
		case err != nil:
			// MA-2 fail-open: we could not confirm the session type, so we default
			// to watchable rather than skip a genuine abandoned chat turn. Log it so
			// a wrongly-armed non-chat session (task/scheduled) is traceable to THIS
			// decision point, not confused with the unrelated boot-time cache error.
			slog.Warn("agent: orphan watch: could not resolve session type, defaulting to watchable",
				"session_id", sessionID, "error", err)
		case meta != nil && meta.Type != "" && meta.Type != session.SessionTypeChat:
			return // MA-2: never watch a non-chat (task/scheduled/...) session
		}
	}

	if al.GetActiveTurnHookForSession(sessionID) == nil {
		return // no active turn — nothing to watch
	}

	ow := &orphanWatch{}
	timer := time.AfterFunc(time.Duration(graceSeconds)*time.Second, func() {
		al.fireOrphanForegroundTurnWatch(sessionID, ow, reap, stillOrphaned)
	})
	// MA-6 fix: fully initialize ow — including its timer field, under its own
	// mutex — BEFORE publishing it to orphanWatches via Swap below. The prior
	// version published ow via Swap FIRST and only afterward assigned
	// `ow.timer = ...` with no lock at all: an unsynchronized write racing
	// DisarmOrphanForegroundTurnWatch's (or a concurrent Arm's) locked read of
	// the very same field on another goroutine the instant the watch became
	// visible via Swap. Setting ow.timer under ow.mu, before Swap publishes
	// the pointer, means any goroutine that later loads ow from the map only
	// ever observes a fully-constructed watch.
	ow.mu.Lock()
	ow.timer = timer
	ow.mu.Unlock()

	// Re-arm semantics: cancel and replace any prior watch for this session
	// so only one timer is ever pending.
	if prev, loaded := al.orphanWatches.Swap(sessionID, ow); loaded {
		if prevWatch, ok := prev.(*orphanWatch); ok {
			prevWatch.cancel()
		}
	}
}

// HasOrphanWatchArmed reports whether an orphan-foreground-turn watch is
// currently pending for sessionID (ADR-045). Exported for observability and
// for tests to verify the gateway's Arm/Disarm wiring without reaching into
// unexported state — mirrors the existing GetActiveTurn/GetActiveAgentIDs
// read-only accessor convention.
func (al *AgentLoop) HasOrphanWatchArmed(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	_, ok := al.orphanWatches.Load(sessionID)
	return ok
}

// DisarmOrphanForegroundTurnWatch cancels a pending orphan watch for
// sessionID. Called by the gateway as soon as a live connection is confirmed
// on the session — handleAttachSession (reattach) and the
// session-continuation branch of handleChatMessage — covering the common
// browser-refresh/reconnect case with zero user-visible effect. A no-op when
// no watch is currently armed for sessionID, including when the watch has
// already fired and reaped (the map entry is removed the instant
// fireOrphanForegroundTurnWatch runs — see that function's doc comment on
// why a reconnect after that point is intentionally NOT specially handled).
func (al *AgentLoop) DisarmOrphanForegroundTurnWatch(sessionID string) {
	if sessionID == "" {
		return
	}
	if v, ok := al.orphanWatches.LoadAndDelete(sessionID); ok {
		if ow, ok := v.(*orphanWatch); ok {
			ow.cancel()
		}
	}
}

// fireOrphanForegroundTurnWatch runs when ow's grace timer elapses with no
// reattach. It reaps the session's root turn — by calling the caller-supplied
// reap callback, which performs the actual RequestCancel call — ONLY IF ALL
// THREE of the following hold; otherwise it is a harmless, silent no-op and
// the watch is spent (no auto-retry: only a fresh WS teardown re-arms it):
//
//  1. A genuine LIVE ROOT turn exists for the session (MA-1). Resolved via
//     getActiveRootTurnStateForSession — NEVER GetActiveTurnHookForSession's
//     non-root anyMatch fallback. If the root has already finished and been
//     cleared from activeTurnStates (clearActiveTurn, loop.go) — e.g. it
//     wrapped up gracefully almost immediately, the common case when a
//     Critical async delegate was spawned just before — the ONLY resolvable
//     match left may be that live delegate. Treating a resolved delegate as
//     "the turn to reap" and handing it to RequestCancel would trigger
//     RequestCancel's session-wide escalation against the exact turn this
//     mechanism exists to protect. "No root" and "only a delegate resolved"
//     must be indistinguishable from this function's perspective: both mean
//     do not reap.
//
//  2. No surviving Critical/background/async delegate is alive on the
//     session (hasLiveCriticalDelegate) — checked even when a live root DOES
//     exist, covering the concurrent case (root still running, e.g. still
//     processing other work, while an async delegate it spawned runs
//     alongside it). RequestCancel's PHASE B/C escalation
//     (InterruptSessionHard, gated on the PHASE-A-computed descendant set —
//     ADR-057 FR-024) is session-wide by construction and cannot be scoped
//     to "the root only" from the outside — so rather than reap anyway and
//     rely on PHASE A's mere graceful nudge (which a delegate ignoring it,
//     by design, would not obey), this mechanism defers reaping ENTIRELY
//     while the delegate survives. The root's own turn keeps running
//     unwatched a while longer; that is always an acceptable trade against
//     ever cutting off wanted background work.
//
//  3. No client has reconnected since arming (stillOrphaned()) — MA-5. Closes
//     the race between the timer firing and a reattach that arrived just
//     before Disarm could run.
//
// On success, a dedicated turn.orphan_timeout audit event is emitted — INFO,
// attributed to "system:orphan-watchdog" — BEFORE calling reap, so the audit
// trail always records WHY a cancel was triggered even if reap itself
// encounters an error. reap is responsible for the actual RequestCancel call
// (and therefore for RequestCancel's own turn_cancel_attempt/turn_canceled
// audit events, transcript writes, and graceful->hard->detached escalation).
func (al *AgentLoop) fireOrphanForegroundTurnWatch(
	sessionID string,
	ow *orphanWatch,
	reap func(reason string),
	stillOrphaned func() bool,
) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("agent: orphan watchdog: timer panic",
				"session_id", sessionID, "panic", r, "stack", string(debug.Stack()))
		}
	}()

	// The watch has fired — remove it from the map now (CompareAndDelete only
	// removes if it's still THIS watch instance; a re-arm that already
	// replaced it, or a concurrent Disarm, must not be clobbered/resurrected).
	al.orphanWatches.CompareAndDelete(sessionID, ow)

	if ow.isCanceled() {
		return // disarmed (reconnect, or AgentLoop.Close) between fire and this goroutine running
	}

	// Condition 1 (MA-1): a genuine live ROOT turn must exist. Regression
	// coverage: TestOrphanWatch_RootAlreadyClearedFromActiveTurnStates_CriticalDelegateNeverReaped
	// (orphan_watch_test.go) — the on-point test explicitly mirrors
	// production's clearActiveTurn removing the root's map entry, unlike its
	// pre-redesign predecessor which left a stale (finished-but-uncleared)
	// root entry in place and so never actually exercised this fallback path.
	rootTS := al.getActiveRootTurnStateForSession(sessionID)
	if rootTS == nil || !rootTS.IsAlive() {
		return
	}

	// Condition 2: no surviving Critical/background/async delegate.
	// Regression coverage: TestOrphanWatch_LiveRootWithLiveCriticalDelegate_ReapDeferred
	// (orphan_watch_test.go) covers the concurrent case where the root is
	// ALSO still alive.
	if al.hasLiveCriticalDelegate(sessionID) {
		return
	}

	// Condition 3 (MA-5): no reconnect raced the timer.
	if !stillOrphaned() {
		return
	}

	audit.Emit(context.Background(), al.AuditLogger(), audit.EventTurnOrphanTimeout, audit.SeverityInfo, map[string]any{
		"session_id": sessionID,
		"turn_id":    rootTS.turnID,
		"canceller":  "system:orphan-watchdog",
	})

	reap("orphan_timeout")
}
