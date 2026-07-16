// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package agent — orphan_watch.go implements the orphaned-foreground-turn
// watchdog (ADR-045): a TURN-SCOPED timer that bounds a webchat session's
// ROOT turn once the last WebSocket connection watching it disconnects,
// without ever touching a Critical/background delegate sub-turn sharing the
// same session. Those delegates are deliberately independent context trees
// (context.Background()-rooted, pkg/agent/loop.go's spawnSubTurn Critical
// path) and must survive an abandoned tab indefinitely — this is the whole
// reason the mechanism is NOT built by reusing RequestCancel/
// InterruptSessionHard wholesale (see ADR-045 "Alternatives Considered").
//
// Mechanism (docs/internal/architecture/ADR-045-orphaned-foreground-turn-timeout.md):
//
//   - Arm: called by the gateway's WS teardown defer when the closing
//     connection was the LAST one watching its session (multi-tab safety
//     checked by the caller). Starts a single grace-period timer keyed by
//     sessionID, capturing the session's current root TurnID.
//
//   - Disarm: called by the gateway on reattach/reconnect (handleAttachSession,
//     the session-continuation branch of handleChatMessage) as soon as a live
//     connection is confirmed. Cancels the pending timer — or, if the grace
//     period already elapsed and the escalation is mid-flight, stops the rest
//     of the PHASE B/C sequence too — with zero effect on the turn either way.
//
//   - Fire (grace elapses, no reattach): re-resolves the session's current
//     root turn hook. A mismatch (turn already finished, or a different turn
//     now owns the session) is a harmless no-op — there is nothing left to
//     protect. Otherwise:
//     PHASE A — graceful, session-wide cascade, reused as-is
//     (al.InterruptSession): PROVEN safe for Critical/background
//     descendants by
//     TestRequestCancel_OrphanedBackgroundDelegate_HardAbortedAfterParentGracefullyFinishes
//     (pkg/agent/cancel_orphan_delegate_test.go) — the graceful nudge alone
//     never terminates a delegate that ignores it.
//     PHASE B (+3s) — TURN-SCOPED hard abort via the new
//     TurnCancelHook.RequestHardAbort, applied ONLY if the exact armed
//     TurnID is still alive. Never session-wide, so a live delegate sharing
//     the session is structurally unreachable.
//     PHASE C (+5s more) — TURN-SCOPED MarkAbandoned, defensive symmetry
//     with RequestCancel's own PHASE C.
//
// This path deliberately bypasses ClaimCancel/onCancelFinish (RequestCancel's
// dedup/callback machinery) entirely — it is a different state machine
// serving a different trigger (silent abandonment, not an explicit user
// action) — so it emits its own audit events (EventTurnOrphanTimeout,
// EventTurnOrphanHardAborted) and appends its own transcript annotation
// directly, rather than reusing RequestCancel's onCancelFinish callback.
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

// orphanWatchHardAbortDelay (PHASE B's escalation window) and
// orphanWatchDetachDelay (PHASE C's window after the turn-scoped hard abort)
// mirror RequestCancel's own 3s graceful->hard / 5s hard->detached timing
// (pkg/agent/cancel.go). Declared as vars (not consts) solely so tests can
// shrink them to keep the escalation-cascade test cases fast; production
// code never mutates them.
var (
	orphanWatchHardAbortDelay = 3 * time.Second
	orphanWatchDetachDelay    = 5 * time.Second
)

// orphanWatch tracks a single armed (or firing) orphan-foreground-turn watch
// for one session. Only one watch is ever active per session — arming again
// cancels and replaces any previous entry (re-arm semantics).
type orphanWatch struct {
	// turnID is the root turn's TurnID captured at arm time. Every phase
	// re-checks the CURRENT session state against this exact value before
	// acting — a mismatch (turn finished, or a different turn now owns the
	// session) makes that phase a no-op.
	turnID string

	mu    sync.Mutex
	timer *time.Timer // the initial grace-period timer; nil once it has fired
	// canceled is set by Disarm (or AgentLoop.Close). Checked at the start of
	// every phase transition (fire, PHASE B, PHASE C) so a reconnect stops
	// the whole escalation sequence, not just a still-pending initial timer.
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
// multi-tab-safety check; this method itself only requires a sessionID and a
// resolved grace period.
//
// graceSeconds <= 0 disables the watchdog entirely (no-op) — matching the
// TimeoutSeconds: 0-disabled convention used elsewhere in this codebase. The
// gateway resolves the effective value from config.GatewayConfig via
// (*config.Config).EffectiveOrphanedTurnGraceSeconds before calling this
// method, so AgentLoop itself never reads config directly here.
//
// Also a no-op when no active turn is currently registered for sessionID —
// there is nothing to watch (e.g. the session's turn already finished before
// the last tab closed). Re-arming an already-armed session (a rapid
// disconnect/reconnect/disconnect sequence, or a fresh turn starting on the
// same session while a prior watch was still pending) cancels the PRIOR
// watch first so at most one timer is ever pending per session.
func (al *AgentLoop) ArmOrphanForegroundTurnWatch(sessionID string, graceSeconds int) {
	if sessionID == "" || graceSeconds <= 0 {
		return
	}

	hook := al.GetActiveTurnHookForSession(sessionID)
	if hook == nil {
		return // no active turn — nothing to watch
	}

	ow := &orphanWatch{turnID: hook.TurnID()}

	// Re-arm semantics: cancel and replace any prior watch for this session
	// so only one timer is ever pending.
	if prev, loaded := al.orphanWatches.Swap(sessionID, ow); loaded {
		if prevWatch, ok := prev.(*orphanWatch); ok {
			prevWatch.cancel()
		}
	}

	ow.timer = time.AfterFunc(time.Duration(graceSeconds)*time.Second, func() {
		al.fireOrphanForegroundTurnWatch(sessionID, ow)
	})
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

// DisarmOrphanForegroundTurnWatch cancels a pending (or in-flight) orphan
// watch for sessionID. Called by the gateway as soon as a live connection is
// confirmed on the session — handleAttachSession (reattach) and the
// session-continuation branch of handleChatMessage — covering the common
// browser-refresh/reconnect case with zero user-visible effect. A no-op when
// no watch is currently armed for sessionID.
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
// reattach. It re-resolves the session's current root turn and, only if it is
// STILL the exact turn armed (same TurnID, still alive), escalates through
// the graceful -> turn-scoped-hard -> turn-scoped-detach stages described in
// ADR-045. Any mismatch (turn already finished naturally, or a new turn now
// owns the session) is a harmless no-op.
func (al *AgentLoop) fireOrphanForegroundTurnWatch(sessionID string, ow *orphanWatch) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("agent: orphan watchdog: timer panic",
				"stage", "fire", "session_id", sessionID, "panic", r, "stack", string(debug.Stack()))
		}
	}()

	// The watch has fired — remove it from the map now (CompareAndDelete only
	// removes if it's still THIS watch instance; a re-arm that already
	// replaced it must not be clobbered).
	al.orphanWatches.CompareAndDelete(sessionID, ow)

	if ow.isCanceled() {
		return // disarmed (reconnect) between timer-fire and this goroutine running
	}

	turnID := ow.turnID
	hook := al.GetActiveTurnHookForSession(sessionID)
	if hook == nil || hook.TurnID() != turnID || !hook.IsAlive() {
		// Turn already finished naturally, or a different turn now owns the
		// session (e.g. a brand new turn started after reconnect) — nothing
		// to do. This is also what protects a Critical/background delegate:
		// once the root has finished and been cleared from activeTurnStates,
		// GetActiveTurnHookForSession can only resolve to a DIFFERENT
		// turnState (the delegate's own), whose TurnID never matches turnID.
		return
	}

	auditLogger := al.AuditLogger()
	hint := "orphaned foreground turn: no client has watched this session for the configured grace period"

	// --- PHASE A: graceful, session-wide cascade (reused as-is; PROVEN safe
	// for Critical/background descendants — see package doc comment). ---
	descendants, _ := al.InterruptSession(sessionID, hint)

	audit.Emit(context.Background(), auditLogger, audit.EventTurnOrphanTimeout, audit.SeverityInfo, map[string]any{
		"session_id":  sessionID,
		"turn_id":     turnID,
		"descendants": descendants,
		"canceller":   "system:orphan-watchdog",
	})

	if store := al.ResolveSessionStore(sessionID); store != nil {
		entry := session.TranscriptEntry{
			ID:                   sessionID + "_orphan_timeout_" + turnID,
			Type:                 session.EntryTypeTurnCancelled,
			TurnID:               turnID,
			CancelledByUser:      "system",
			CancelledByChannel:   "orphan-watchdog",
			CancelMethod:         "orphan_timeout",
			DescendantsCancelled: descendants,
			Timestamp:            time.Now().UTC(),
		}
		if err := store.AppendTranscript(sessionID, entry); err != nil {
			slog.Warn("agent: orphan watchdog: could not append transcript entry",
				"session_id", sessionID, "turn_id", turnID, "error", err)
		}
	}

	// --- PHASE B: +3s, TURN-SCOPED hard abort (new — never session-wide, so
	// a live Critical/background delegate sharing this session is
	// structurally unreachable). ---
	time.AfterFunc(orphanWatchHardAbortDelay, func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("agent: orphan watchdog: timer panic",
					"stage", "hard", "session_id", sessionID, "turn_id", turnID, "panic", r, "stack", string(debug.Stack()))
			}
		}()
		if ow.isCanceled() {
			return
		}
		hardHook := al.GetActiveTurnHookForSession(sessionID)
		if hardHook == nil || hardHook.TurnID() != turnID || !hardHook.IsAlive() {
			return // already finished gracefully — no escalation needed
		}
		hardHook.RequestHardAbort()

		audit.Emit(context.Background(), auditLogger, audit.EventTurnOrphanHardAborted, audit.SeverityWarn, map[string]any{
			"session_id": sessionID,
			"turn_id":    turnID,
			"canceller":  "system:orphan-watchdog",
		})

		// --- PHASE C: +5s more, TURN-SCOPED detach (defensive symmetry with
		// RequestCancel's own PHASE C). ---
		time.AfterFunc(orphanWatchDetachDelay, func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("agent: orphan watchdog: timer panic",
						"stage", "detached", "session_id", sessionID, "turn_id", turnID, "panic", r, "stack", string(debug.Stack()))
				}
			}()
			if ow.isCanceled() {
				return
			}
			detachHook := al.GetActiveTurnHookForSession(sessionID)
			if detachHook == nil || detachHook.TurnID() != turnID || !detachHook.IsAlive() {
				return
			}
			detachHook.MarkAbandoned()
			slog.Warn("agent: orphan watchdog: turn did not exit after turn-scoped hard abort — marking abandoned",
				"session_id", sessionID, "turn_id", turnID)
		})
	})
}
