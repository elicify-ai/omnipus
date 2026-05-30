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
	"time"

	"github.com/dapicom-ai/omnipus/pkg/audit"
	"github.com/dapicom-ai/omnipus/pkg/session"
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
}

// RequestCancel is the canonical cancel entry point. All four cancel surfaces
// (web SPA, Tier A /cancel command, Tier B text-parsing channels, CLI) call
// this method.
//
// It performs the entire cancel state machine:
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

	// --- Audit: attempt (always, even for duplicate or no-turn cancels) ---
	audit.Emit(ctx, auditLogger, audit.EventTurnCancelAttempt, audit.SeverityInfo, map[string]any{
		"session_id":        sessionID,
		"canceller_user":    canceller.UserID,
		"canceller_channel": canceller.Channel,
		"was_fired":         wasFired,
	})

	if !wasFired {
		slog.Debug("agent: RequestCancel — no active turn or already canceled",
			"session_id", sessionID,
			"channel", scope.Channel,
			"chat_id", scope.ChatID,
		)
		return CancelOutcome{Fired: false}, nil
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
			"session_id":           sessionID,
			"turn_id":              turnID,
			"canceller_user":       canceller.UserID,
			"canceller_channel":    canceller.Channel,
			"cancel_method":        cancelMethod,
			"descendants_canceled": descendants,
		})
	})

	// --- PHASE A: graceful cascade + approval auto-deny ---
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

	// --- PHASE B: 3s timer → hard abort if turn is still alive ---
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
		if !activeTurn.IsAlive() {
			return // already finished
		}
		if _, err := al.InterruptSessionHard(sessionID, hint); err != nil {
			slog.Warn("agent: RequestCancel: hard abort failed",
				"session_id", sessionID, "error", err)
		}
		if hooks.SendStageFrame != nil {
			hooks.SendStageFrame(sessionID, "hard")
		}

		// --- PHASE C: 5s after hard → detach if still alive ---
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
			if !activeTurn.IsAlive() {
				return // finished in the meantime
			}
			activeTurn.MarkAbandoned()
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
		Fired:       true,
		Descendants: descendants,
		TurnID:      turnID,
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
		CancelHooks{}, // no transport-specific side-effects for Tier A /cancel command
	)
	if err != nil {
		return false, err
	}
	return outcome.Fired, nil
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
		CancelHooks{}, // no transport-specific side-effects for Tier B channels
	)
	return err
}
