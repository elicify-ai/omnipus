// delegate_followup.go: Send a follow-up message to a delegation — steer one still running, or follow up one already finished.

package tools

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/google/uuid"
)

// checkSteerCaps enforces the steer/respond body-size cap (16 KiB default)
// and per-target-session rate cap (6/min default) — ADR-053 §Contract
// Surface "Caps". Returns a clear, typed error naming the exceeded cap;
// callers surface it as a tool error (never-silent-drop applies to
// parent->child delivery too — a rejected steer must be visible to the
// PARENT, not silently dropped).
func (t *DelegateTool) checkSteerCaps(sessionID, text string) error {
	bodyCap := t.steerBodyBytes
	if bodyCap <= 0 {
		bodyCap = session.DefaultSteerBodyBytes
	}
	if len(text) > bodyCap {
		return fmt.Errorf("steer/respond body (%d bytes) exceeds the %d byte cap", len(text), bodyCap)
	}

	limit := t.steerRatePerMin
	if limit <= 0 {
		limit = session.DefaultSteerRatePerMinute
	}
	now := t.now()
	cutoff := now.Add(-1 * time.Minute)

	t.steerRateMu.Lock()
	defer t.steerRateMu.Unlock()

	// LOW-4 (14-reviewer sign-off): opportunistic full-map eviction, mirroring
	// cancel_prearm.go::markPendingSpawn's own pattern. The per-session logic
	// below always ends by storing THIS session's own entry back non-empty —
	// either the rate-limited kept window (>= limit, so never empty) or
	// kept+now (always >= 1) — so a session's own key never self-deletes,
	// even long after that session is terminal and will never call
	// steer/respond again. Left unaddressed, every distinct session_id ever
	// steered/responded-to accumulates a permanent entry in this map for the
	// life of the process. Sweep every OTHER session's window for entries
	// older than the rate window on each call and drop any that are now
	// fully empty, bounding the map to roughly the sessions actively
	// steered within the last minute.
	for sid, ts := range t.steerRateWindows {
		if sid == sessionID {
			continue // handled by the per-session logic below
		}
		live := ts[:0]
		for _, t2 := range ts {
			if t2.After(cutoff) {
				live = append(live, t2)
			}
		}
		if len(live) == 0 {
			delete(t.steerRateWindows, sid)
		} else {
			t.steerRateWindows[sid] = live
		}
	}

	window := t.steerRateWindows[sessionID]
	kept := window[:0]
	for _, ts := range window {
		if ts.After(cutoff) {
			kept = append(kept, ts)
		}
	}
	if len(kept) >= limit {
		t.steerRateWindows[sessionID] = kept
		return fmt.Errorf("steer/respond rate exceeded (%d/min) for session %s", limit, sessionID)
	}
	t.steerRateWindows[sessionID] = append(kept, now)
	return nil
}

func (t *DelegateTool) executeSteer(ctx context.Context, args map[string]any) *ToolResult {
	if t.steering == nil {
		return ErrorResult("delegate: no steering sink configured")
	}
	if t.lifecycle == nil {
		return ErrorResult("delegate: no lifecycle store configured")
	}
	sessionID, err := requiredStringArg(args, "session_id")
	if err != nil {
		return ErrorResult(err.Error())
	}
	text, err := requiredStringArg(args, "text")
	if err != nil {
		return ErrorResult(err.Error())
	}

	// ADR-057 W12: ownership is verified via a plain Load BEFORE the Mutate
	// below, deliberately OUTSIDE the atomic closure — unlike the terminal
	// check (see the TOCTOU comment below), ownership cannot race: a
	// session's SteeringSessionID is stamped once at mint time and carried
	// forward unchanged even across resume generations (see
	// spawnCorrectiveFollowUp's whole-struct-copy comment), so a Load taken
	// a moment before Mutate observes the exact same value Mutate itself
	// would. Verifying it here, rather than inside the closure below, is
	// not just style: the ownership walk (verifyCallerOwnsSession, FR-039)
	// climbs the SteeringSessionID chain via t.lifecycle.Load(ancestor) for
	// every hop beyond the direct parent, and pkg/session/lifecycle_lock.go's
	// striped lock is only 64-wide — an ancestor whose id happens to hash to
	// the SAME shard as sessionID would deadlock against Mutate's
	// already-held, non-reentrant per-shard sync.Mutex if the walk ran
	// inside that closure instead.
	rec, lerr := t.lifecycle.Load(sessionID)
	if lerr != nil {
		return ErrorResult(fmt.Sprintf("delegate: steer: %v", lerr))
	}
	by, verr := t.verifyCallerPrincipal(ctx, rec)
	if verr != nil {
		return ErrorResult(fmt.Sprintf("delegate: steer: %v", verr))
	}
	// Not available to external-CLI (3P) sessions: every steering-queue drain
	// site lives in the native turn engine (pkg/agent/loop.go,
	// pkg/agent/steering.go) — runExternalCLISubTurn
	// (pkg/agent/external_dispatch.go) never drains it, so a message queued
	// here for a 3P child is silently orphaned forever (no live consumer ever
	// reads it, and there is no "next tool boundary" concept for an external
	// CLI's own turn loop). Unlike respond/resume, steer has no corrective-
	// redispatch fallback to degrade to — injecting an instruction mid-turn is
	// meaningless for a session that isn't running on this engine's turn loop
	// at all. Mirrors message_parent's identical Is3P posture (D5).
	if rec.Is3P {
		return ErrorResult(fmt.Sprintf(
			"delegate: steer: not_steerable: external command-line session %s runs on an external CLI "+
				"(claude-code/codex/opencode) with no steering-queue drain in its dispatch path; use "+
				"action=\"respond\" (which redispatches a corrective session) or action=\"resume\" instead",
			sessionID,
		))
	}

	// [Finding 1, ADR-091 fix lane 2 — Q17/D8] A terminal record (ADR-093
	// D4) or a record whose stop has already LANDED (state LifecycleStopped;
	// TransitionSession cleared the current-generation fence the moment it
	// landed) is checked FIRST, off the plain Load above: the founder's
	// decision is that only a newer instruction revives a stopped session,
	// as a new generation, never the steering queue (which a stopped session
	// has no live consumer left to drain). BOTH shapes revive through
	// ReviveStoppedSession in the branch below. A stop fence still IN FLIGHT
	// (stamped for the current generation while the state is still running
	// or queued) is NEITHER shape — an in-flight fence is not a landed stop:
	// the dying turn is still registered, so a revive here would clear the
	// fence, re-queue the record and strand it queued with nobody running it
	// (admission then refuses because the dying turn is still registered).
	// It is refused visibly right below, before any queueing; the caller
	// retries once the stop has landed. Deliberately NOT folded into the
	// Mutate-based terminal-rejection closure below: a landed stop-state is
	// left only through Revive, which re-validates atomically under Revive's
	// own record lock, so the fact cannot become stale under this Load the
	// way a live fence can — a fence not yet landed can land, and
	// TransitionSession then clears it, at any moment; that moving fact is
	// exactly what the closure's own in-flight-fence check below re-reads
	// under the lock. This ALSO avoids a real bug the Mutate-closure version
	// of this check had: once a stamped-but-never-ran session is terminalised
	// by its own Stop (Finding 5, pkg/agent/steer_cancel.go::
	// terminaliseNeverRanStop), persisting an UNCHANGED copy of that
	// now-terminal record through Mutate (the "harmless no-op" pattern the
	// terminal-rejection closure below relies on) trips the store's own
	// immutable-terminal invariant (ErrLifecycleTerminalImmutable) —
	// Mutate's no-op persist is only harmless when the record is NOT
	// terminal.
	// ADR-093 D4: a terminal record, stopped or not, takes this same revive.
	if rec.Terminal() || rec.State == session.LifecycleStopped {
		if cerr := t.checkSteerCaps(sessionID, text); cerr != nil {
			return ErrorResult(fmt.Sprintf("delegate: steer: %v", cerr)).WithError(cerr)
		}
		reviver, ok := t.steering.(steerReviver)
		if !ok {
			return ErrorResult(fmt.Sprintf("delegate: steer: session %s is stopped and cannot be revived: no reviver configured", sessionID))
		}
		revived, rerr := reviver.ReviveStoppedSession(ctx, sessionID, by, text)
		if rerr != nil {
			return ErrorResult(fmt.Sprintf("delegate: steer: revive stopped session %s: %v", sessionID, rerr)).WithError(rerr)
		}
		if !revived {
			return ErrorResult(fmt.Sprintf("delegate: steer: session %s could not be revived", sessionID))
		}
		if rec.Terminal() {
			return NewToolResult(fmt.Sprintf(
				"Session %s had finished; the steering message started its next round.", sessionID,
			))
		}
		return NewToolResult(fmt.Sprintf(
			"Session %s was stopped; the steering message resumed it on the same conversation.", sessionID,
		))
	}

	// A current-generation fence that has NOT yet landed (state still
	// running or queued) is neither queueable nor revivable: the dying turn
	// is still registered, so reviving now would strand the record queued
	// with nobody running it, and queueing would put the message in a queue
	// the dying turn's unwinding may never drain. Refuse visibly — never
	// queue, never revive; the caller retries once the stop has landed.
	// (A fence for an EARLIER generation is inert history — Stopped() is
	// false for it — and takes the ordinary queueing path below.)
	if rec.Stop != nil && rec.Stop.Generation == rec.Generation {
		return ErrorResult(fmt.Sprintf("delegate: steer: session %s is stopping (a stop is in flight for its current generation); retry the steer once it has stopped", sessionID))
	}

	// TOCTOU race guard: a plain Load() followed by a branch on
	// rec.Terminal() was a check-then-act race against the concurrent
	// atomic terminal transition in pkg/agent/task_executor.go
	// (session.TransitionSession) — if that transition landed in the window
	// between this Load and EnqueueSteeringMessage below, a stale
	// non-terminal snapshot passed the check and the steering message got
	// queued for a session with no live consumer left to read it (orphaned
	// permanently — pkg/agent/steering.go's queue has no liveness check).
	// The sibling action executeRespond already avoids this by routing
	// through LifecycleStore.Mutate, the atomic read-modify-write primitive
	// that holds the per-session striped lock across the whole read+decide
	// (pkg/session/lifecycle.go's own docs: "a naked Load+Persist races a
	// concurrent transition on the same session_id"). Doing the same here —
	// the terminal check evaluated INSIDE the Mutate closure, under the SAME
	// lock the terminal-transition writer uses — closes the window; a
	// transition landing just before we take the lock is now guaranteed
	// visible, and one landing just after must wait for us to release it.
	// This performs no actual field mutation on the non-error path (steer
	// delivery is a separate side channel, not a lifecycle field) — Mutate
	// persisting an unchanged copy of the tail record is a harmless,
	// deliberate byproduct of reusing the RMW primitive purely for its
	// locking guarantee, PROVIDED the record is not terminal — which is
	// exactly why the stop cases above are handled before ever reaching
	// here (landed-stopped and terminal revive; an in-flight fence refuses),
	// never inside this closure. Ownership is
	// deliberately NOT re-checked here — see the comment above for why a
	// stale ownership read cannot happen.
	// The same race has a second shape (ADR-20260928, founder decision
	// 2026-10-04): the helper can LAND LifecycleStopped in the window between
	// the plain Load above and this lock-protected re-read — its stop
	// finished unwinding (TransitionSession cleared the fence) and no live
	// consumer will ever drain the steering queue. Queueing there would
	// strand the message, so the closure signals the landed-stopped shape out
	// via stoppedInRace and the revive below runs AFTER the store lock is
	// released — the same way the earlier stopped branch revives (the
	// striped lock is not reentrant, so ReviveStoppedSession, which takes the
	// record lock itself, must never run inside this closure; its own
	// re-read under that lock re-validates whatever changed since). A stop
	// fence still IN FLIGHT (stamped for the current generation, state not
	// yet LifecycleStopped) is neither queueable nor revivable: the old turn
	// is still registered, so an early revive would strand the record queued
	// with nobody running it — refused visibly, the caller retries once the
	// stop has landed.
	stoppedInRace := false
	if merr := t.lifecycle.Mutate(sessionID, func(cur *session.LifecycleRecord) error {
		if cur == nil {
			return session.ErrLifecycleNotFound
		}
		// A record already terminal never reaches this closure — the branch
		// above revived it — and one carrying a live in-flight fence is
		// refused by the branch above too. These checks exist ONLY for the
		// race where the record becomes terminal or Stop-stamped between the
		// plain Load and this lock-protected re-read; their strings are
		// refusals for those races alone.
		if cur.Terminal() {
			return fmt.Errorf("session %s is terminal (%s) and cannot be steered", sessionID, cur.State)
		}
		if cur.State == session.LifecycleStopped {
			stoppedInRace = true
			rec = cur
			return nil
		}
		if cur.Stop != nil && cur.Stop.Generation == cur.Generation {
			return fmt.Errorf("session %s is stopping (a stop is in flight for its current generation); retry the steer once it has stopped", sessionID)
		}
		rec = cur
		return nil
	}); merr != nil {
		return ErrorResult(fmt.Sprintf("delegate: steer: %v", merr))
	}

	if stoppedInRace {
		if cerr := t.checkSteerCaps(sessionID, text); cerr != nil {
			return ErrorResult(fmt.Sprintf("delegate: steer: %v", cerr)).WithError(cerr)
		}
		reviver, ok := t.steering.(steerReviver)
		if !ok {
			return ErrorResult(fmt.Sprintf("delegate: steer: session %s is stopped and cannot be revived: no reviver configured", sessionID))
		}
		revived, rerr := reviver.ReviveStoppedSession(ctx, sessionID, by, text)
		if rerr != nil {
			return ErrorResult(fmt.Sprintf("delegate: steer: revive stopped session %s: %v", sessionID, rerr)).WithError(rerr)
		}
		if !revived {
			return ErrorResult(fmt.Sprintf("delegate: steer: session %s could not be revived", sessionID))
		}
		return NewToolResult(fmt.Sprintf(
			"Session %s was stopped; the steering message resumed it on the same conversation.", sessionID,
		))
	}

	if cerr := t.checkSteerCaps(sessionID, text); cerr != nil {
		return ErrorResult(fmt.Sprintf("delegate: steer: %v", cerr)).WithError(cerr)
	}

	// correlation_id is optional for action="steer" (the schema's own
	// description: "Required for action=\"respond\" (optional for
	// \"steer\")") — a caller who wants to match a later steering_receipt
	// (issue #870) to this exact instruction supplies one; when absent,
	// EnqueueSteeringMessage mints a server-assigned reference and hands it
	// back below regardless, so the receipt is always correlatable.
	requestedCorrelationID, _ := stringArg(args, "correlation_id")
	resolvedCorrelationID, serr, postFinish := enqueueSteeringWithStatus(t.steering, sessionID, rec.AgentID,
		providers.Message{Role: "user", Content: text}, requestedCorrelationID)
	if serr != nil {
		return ErrorResult(fmt.Sprintf("delegate: steer: %v", serr)).WithError(serr)
	}
	if postFinish {
		// Round-4 correction: a steer that landed in a terminal-transition
		// finishing window (rather than the main queue) is reported with
		// the spec's exact wording — "queued; the child is finishing and
		// will see it next" — so the caller can tell apart the two
		// outcomes (an ordinary queued steer vs. one the closing hand-off
		// will revive the child to consume). correlation_id is still
		// returned so a receipt-based caller (issue #870) can correlate.
		return NewToolResult(fmt.Sprintf(
			"queued; the child is finishing and will see it next (correlation_id=%s, session_id=%s).",
			resolvedCorrelationID, sessionID,
		))
	}
	return NewToolResult(fmt.Sprintf(
		"Steering message queued for session %s (correlation_id=%s); it will apply at the child's next tool boundary.",
		sessionID, resolvedCorrelationID,
	))
}

// steerReviver is satisfied by the production delegate steering sink through
// its embedded *agent.AgentLoop beyond DelegateSteeringSink's own
// EnqueueSteeringMessage method. Declared here, not added to
// DelegateSteeringSink itself, so a narrower test fake standing in for the
// steering sink is not forced to also implement revival — mirrors
// appendFollowUpInstruction's own optional-capability type assertion on
// t.sessionStore below.
type steerReviver interface {
	ReviveStoppedSession(ctx context.Context, sessionID string, by steer.Principal, instruction string) (bool, error)
}

// steerSinkWithEnqueueStatus is satisfied by agent's delegateSteeringSink
// adapter beyond DelegateSteeringSink's EnqueueSteeringMessage. Declared here
// (not added to DelegateSteeringSink) for the same reason steerReviver
// stays narrow: the round-4 status (issue #1020 round-4 correction) is
// only meaningful to executeSteer's caller-facing text, and forcing
// every DelegateSteeringSink implementer to know about it would couple
// the interface to a notion that does not exist for non-steering
// callers (a session_worker enqueue, for example, never lands in a
// finishing-window buffer — every session_worker enqueue is for an
// idle session). The status field is an int so pkg/tools does not
// import pkg/agent, which already imports pkg/tools. The production adapter
// explicitly converts the named EnqueueStatus to int; 1 means PostFinish
// and any other value means Normal. Sinks without this capability fall back to
// EnqueueSteeringMessage and never report the post-finish outcome.
type steerSinkWithEnqueueStatus interface {
	EnqueueSteeringMessageWithStatus(scope, agentID string, msg providers.Message, correlationID string) (string, int, error)
}

// enqueueSteeringWithStatus is executeSteer's adapter: it forwards to the
// rich return shape when the steering sink exposes it (the production
// delegateSteeringSink does), and falls back to the plain DelegateSteeringSink
// contract for narrower test fakes that do not. postFinish is true when
// the rich sink reports the item landed in a terminal-transition
// finishingItems buffer rather than the main queue.
func enqueueSteeringWithStatus(
	sink any,
	scope, agentID string,
	msg providers.Message,
	correlationID string,
) (resolvedID string, err error, postFinish bool) {
	if rich, ok := sink.(steerSinkWithEnqueueStatus); ok {
		resolvedID, status, err := rich.EnqueueSteeringMessageWithStatus(scope, agentID, msg, correlationID)
		if err != nil {
			return "", err, false
		}
		return resolvedID, nil, status == 1 // EnqueueStatusPostFinish
	}
	if basic, ok := sink.(interface {
		EnqueueSteeringMessage(scope, agentID string, msg providers.Message, correlationID string) (string, error)
	}); ok {
		resolvedID, err := basic.EnqueueSteeringMessage(scope, agentID, msg, correlationID)
		if err != nil {
			return "", err, false
		}
		return resolvedID, nil, false
	}
	return "", fmt.Errorf("delegate: steer: no steering sink configured"), false
}

// executeResume implements action="resume" (ADR-20261004, locked decision
// 4; renamed from follow_up with no alias path). Continues a stopped helper
// on the same conversation and the same generation, or starts the next
// round when the helper is done or failed — one primitive for both, which
// is exactly what ReviveStoppedSession admits. A working helper has nothing
// to resume: a non-error "already running" result (the same reasoning as
// executeStopAll's non-error "already terminal" — an error here drives
// agents into retry loops). A 3P child keeps its D5 corrective re-dispatch
// (external CLIs have no warm-resume primitive); resume is what it uses in
// place of the renamed follow_up.
func (t *DelegateTool) executeResume(ctx context.Context, args map[string]any, cb AsyncCallback) *ToolResult {
	if t.launcher == nil {
		return ErrorResult("delegate: no session launcher configured")
	}
	if t.lifecycle == nil {
		return ErrorResult("delegate: no lifecycle store configured")
	}
	sessionID, err := requiredStringArg(args, "session_id")
	if err != nil {
		return ErrorResult(err.Error())
	}

	// text is optional: a bare resume continues the stopped helper with no
	// new instruction; when present it is the additional instruction for the
	// continued/next round.
	instruction, ierr := resumeInstructionArg(args)
	if ierr != nil {
		return ErrorResult(ierr.Error())
	}

	rec, lerr := t.lifecycle.Load(sessionID)
	if lerr != nil {
		return ErrorResult(fmt.Sprintf("delegate: resume: %v", lerr))
	}
	by, verr := t.verifyCallerPrincipal(ctx, rec)
	if verr != nil {
		return ErrorResult(fmt.Sprintf("delegate: resume: %v", verr))
	}

	// A working helper has nothing to resume — non-error, nothing started
	// (locked decision 4 names only stopped and done/failed recipients).
	if !rec.Terminal() && !rec.Stopped() {
		return NewToolResult(fmt.Sprintf(
			"Session %s is already running (state=%s) — nothing to resume. Use action=\"steer\" to inject an instruction into the current turn.",
			sessionID, rec.State,
		))
	}

	if rec.Is3P {
		// D5: a 3P child never warm-resumes. Its one continuation primitive
		// is the corrective re-dispatch (a NEW session carrying the prior
		// context), which serves both the stopped and the finished shape.
		return t.spawnCorrectiveFollowUp(ctx, sessionID, rec, instruction, cb)
	}

	// Native: stopped → same-generation resume; done/failed → next round.
	// Both shapes go through ReviveStoppedSession (SteerCanceller.Revive's
	// own branch), which appends the instruction BEFORE the generation moves
	// and refuses visibly when the append fails.
	reviver, ok := t.steering.(steerReviver)
	if !ok {
		return ErrorResult(fmt.Sprintf("delegate: resume: session %s cannot be resumed: no reviver configured", sessionID))
	}
	wasTerminal := rec.Terminal()
	revived, rerr := reviver.ReviveStoppedSession(ctx, sessionID, by, instruction)
	if rerr != nil {
		return ErrorResult(fmt.Sprintf("delegate: resume: %v", rerr)).WithError(rerr)
	}
	if !revived {
		return ErrorResult(fmt.Sprintf("delegate: resume: session %s could not be resumed", sessionID))
	}
	if wasTerminal {
		return NewToolResult(fmt.Sprintf(
			"Session %s was finished; a next round has been started on the same conversation.", sessionID,
		))
	}
	return NewToolResult(fmt.Sprintf(
		"Session %s was stopped; it has been resumed on the same conversation and generation.", sessionID,
	))
}

// resumeInstructionArg resolves the optional additional instruction for
// action="resume": "text" is the documented field. Absent or blank means a
// bare continuation — no error, no placeholder substitution.
func resumeInstructionArg(args map[string]any) (string, error) {
	rawText, present := args["text"]
	if !present || rawText == nil {
		return "", nil
	}
	s, ok := rawText.(string)
	if !ok {
		return "", fmt.Errorf("text must be a string")
	}
	return strings.TrimSpace(s), nil
}

// spawnCorrectiveFollowUp is the shared mechanics behind `resume` (native
// and 3P) and a 3P `respond` (D5 — a 3P child never warm-resumes; every
// continuation is a NEW corrective session carrying the prior context).
// Native resume reuses sessionID verbatim (warm resume, same session, new
// generation — the terminal resume bumps the record's Generation); 3P mints
// a NEW session_id (cold respawn), linked back via ResumedFrom.
func (t *DelegateTool) spawnCorrectiveFollowUp(
	ctx context.Context,
	sessionID string,
	rec *session.LifecycleRecord,
	instructions string,
	_ AsyncCallback,
) *ToolResult {
	newSessionID := sessionID
	if rec.Is3P {
		newSessionID = uuid.NewString()
	}

	// The whole-struct copy is load-bearing for FR-034: ParentAgentID (and
	// SteeringSessionID/OriginChannel/OriginChatID with it) MUST be carried
	// forward onto every generation mint. It is deliberately CARRIED FORWARD
	// from the prior generation rather than re-sourced from ToolAgentID(ctx)
	// — the resume caller is not necessarily the agent that originally
	// spawned the session, and re-sourcing would silently re-parent it. Do
	// not replace this copy with field-by-field construction.
	// Origin.CallID stays the original run's call id on every generation.
	// The follow-up's own span is not a new call id: pkg/agent.SubagentSpanID
	// appends _g<N> for generation N >= 2, and replay rebuilds that same
	// string. Minting a new CallID here would break parent_call_id.
	newRec := *rec
	newRec.SessionID = newSessionID
	newRec.Generation = rec.Generation + 1
	newRec.ResumedFrom = sessionID
	newRec.State = session.LifecycleQueued
	newRec.FailedReason = ""
	newRec.NeedsInput = nil
	// ADR-20260928 D2: an explicit resume of a committed done/failed G creating
	// G+1 neither copies G's committed final outbox into G+1 nor hides it —
	// G's entry stays discoverable for delivery retry through
	// LifecycleStore.ListPendingFinalDeliveries. The whole-struct copy would
	// otherwise carry a tuple whose generation no longer matches (persistLocked
	// would reject it).
	newRec.FinalDelivery = nil
	if rec.Is3P {
		if err := t.cloneCorrectiveSessionIdentity(sessionID, newSessionID, rec); err != nil {
			return ErrorResult(fmt.Sprintf("delegate: resume: failed to create corrective session: %v", err)).WithError(err)
		}
	}
	if err := t.lifecycle.Persist(&newRec); err != nil {
		return ErrorResult(fmt.Sprintf("delegate: resume: failed to persist new generation: %v", err)).WithError(err)
	}
	// [Defect 4, ADR-091 fix lane RX-DELIVERY] REFUSE to dispatch when the
	// new instruction did not land. reconstructSteeredTurn would otherwise
	// rebuild the turn from the last `user` transcript entry — the PREVIOUS
	// instruction — and the session would confidently answer the old
	// question and report it upward as a real result. The record is landed
	// failed for the same reason the dispatch-failure path just below does
	// it: a new generation was already persisted, so leaving it `queued`
	// would strand it and block its parent for ever.
	if err := t.appendFollowUpInstruction(newSessionID, instructions); err != nil {
		t.transitionLifecycle(newSessionID, session.LifecycleFailed, err.Error(), nil)
		slog.Error("delegate: follow-up instruction did not land; dispatch refused",
			"session_id", newSessionID,
			"generation", newRec.Generation,
			"agent_id", rec.AgentID,
			"error", err)
		return ErrorResult(fmt.Sprintf("delegate: resume: %v", err)).WithError(err)
	}

	dispatch, err := t.launcher.Dispatch(ctx, newSessionID, newRec.Generation)
	if err != nil {
		t.transitionLifecycle(newSessionID, session.LifecycleFailed, err.Error(), nil)
		slog.Error("delegate: follow-up dispatch failed",
			"session_id", newSessionID,
			"generation", newRec.Generation,
			"agent_id", rec.AgentID,
			"error", err)
		return ErrorResult(fmt.Sprintf("delegate: resume: dispatch failed: %v", err)).WithError(err)
	}

	message := fmt.Sprintf("Resume dispatched for session %s at generation %d (state: %s)",
		newSessionID, dispatch.Generation, dispatch.State)
	if dispatch.State == steer.DispatchQueued {
		message += fmt.Sprintf(", queue position %d", dispatch.QueuePosition)
	}
	// rec.Title is the durable launch-time label (session.LifecycleRecord's
	// own doc comment) — the session_id-addressed replacement for the
	// pre-ADR-091 in-memory task-state label lookup this used to read
	// (t.tasks/t.sessionIndex, deleted with the last writer that populated
	// them; see delegate.go's package doc comment).
	if rec.Title != "" {
		message = fmt.Sprintf("Resume for %q dispatched for session %s at generation %d (state: %s)",
			rec.Title, newSessionID, dispatch.Generation, dispatch.State)
	}
	return NewToolResult(message)
}

// followUpInstructionWriter is the write capability
// appendFollowUpInstruction needs from the session store. AddMessage extends
// the model's history (context.jsonl); AppendTranscriptStrict writes the
// transcript entry the rebuilt turn actually READS BACK, and is the only one
// of the two that can report a failure. The concrete production
// *session.UnifiedStore satisfies both; a read-only status fake does not, and
// is refused rather than silently skipped.
type followUpInstructionWriter interface {
	AddMessage(sessionKey, role, content string)
	AppendTranscriptStrict(sessionID string, entry session.TranscriptEntry) error
}

// appendFollowUpInstruction adds the new user instruction to the session's
// durable history AND to its transcript, before Dispatch reconstructs the
// turn, and reports whether it actually landed.
//
// [Defect 4, ADR-091 fix lane RX-DELIVERY, HIGH] It used to be
// fire-and-forget in two independent ways, either of which alone makes a
// followed-up session re-run its PREVIOUS instruction and report that answer
// upward as a real result:
//
//   - It wrote only AddMessage, which has NO return value (UnifiedStore logs
//     internally and tells the caller nothing), and a nil store or a failed
//     type assertion was a silent no-op. Callers dispatched regardless.
//   - AddMessage writes context.jsonl only. The turn Dispatch reconstructs
//     takes its UserMessage from the TRANSCRIPT —
//     agent/steer_reconstruct.go::reconstructSteeredTurn scans
//     transcript.jsonl backwards for the last non-blank `user` entry when
//     wake == nil — which only agent/steer_launcher.go's launch path ever
//     wrote. So the new instruction never reached the place that turn reads.
//
// Both halves are fixed here by mirroring the launch path's own write pair
// (AddMessage AND AppendTranscriptStrict) and returning the strict append's
// error. The entry id is fresh per call: a resume instruction is new input,
// never a duplicate of the last one.
//
// The transcript entry's agent id
// is therefore looked up here, best-effort — it is display metadata, never a
// reason to refuse an instruction that is otherwise durable.
func (t *DelegateTool) appendFollowUpInstruction(sessionID, instruction string) error {
	instruction = strings.TrimSpace(instruction)
	if instruction == "" {
		return nil
	}
	if t.sessionStore == nil {
		// Degraded boot: loop_wire.go skips SetSessionStore entirely when
		// al.GetSessionStore() is nil, the same state
		// cloneCorrectiveSessionIdentity above already treats as "nothing to
		// write". Refusing here would add nothing: with no session store,
		// agent/steer_reconstruct.go::reconstructSteeredTurn fails outright
		// ("session store is not wired") long before it could re-run a stale
		// instruction, so the dispatch cannot succeed with the wrong
		// instruction either way. Loud, not silent.
		slog.Warn("delegate: follow-up instruction not recorded: no session store is wired (degraded boot)",
			"session_id", sessionID)
		return nil
	}
	writer, ok := t.sessionStore.(followUpInstructionWriter)
	if !ok {
		return fmt.Errorf("session store cannot record the new instruction for %q "+
			"(no write capability); the session would re-run its previous instruction", sessionID)
	}
	writer.AddMessage(sessionID, "user", instruction)
	agentID := ""
	if t.lifecycle != nil {
		if rec, lerr := t.lifecycle.Load(sessionID); lerr == nil && rec != nil {
			agentID = rec.AgentID
		}
	}
	if err := writer.AppendTranscriptStrict(sessionID, session.TranscriptEntry{
		ID:      sessionID + "-instruction-" + uuid.NewString(),
		Role:    "user",
		AgentID: agentID,
		Content: instruction,
	}); err != nil {
		return fmt.Errorf("record the new instruction for %q in the transcript the rebuilt turn reads: %w", sessionID, err)
	}
	return nil
}

// correctiveSessionStore is the write-capable production UnifiedStore shape
// needed when an external worker gets a fresh corrective session identity.
// DelegateSessionStore remains read-only for status-only fakes.
type correctiveSessionStore interface {
	DelegateSessionStore
	AddMessage(sessionKey, role, content string)
	GetMeta(sessionID string) (*session.UnifiedMeta, error)
	CreateSessionWithID(childID, parentID string, sessionType session.UnifiedSessionType, channel, creatingAgentID string) (*session.UnifiedMeta, error)
	SetMeta(sessionID string, patch session.MetaPatch) error
}

func (t *DelegateTool) cloneCorrectiveSessionIdentity(sourceID, newID string, rec *session.LifecycleRecord) error {
	if t.sessionStore == nil {
		return nil
	}
	store, ok := t.sessionStore.(correctiveSessionStore)
	if !ok {
		return fmt.Errorf("session store cannot create corrective identities")
	}
	source, err := store.GetMeta(sourceID)
	if err != nil {
		return err
	}
	parentID := source.ParentSessionID
	if rec != nil && rec.SteeredBy != nil && rec.SteeredBy.SteeringSessionID != "" {
		parentID = rec.SteeredBy.SteeringSessionID
	}
	if _, err := store.CreateSessionWithID(newID, parentID, source.Type, source.Channel, source.ActiveAgentID); err != nil {
		return err
	}
	title, owner, workspace, instanceID, taskID := source.Title, source.Owner, source.WorkspaceID, source.InstanceID, source.TaskID
	return store.SetMeta(newID, session.MetaPatch{
		Title: &title, Owner: &owner, WorkspaceID: &workspace, InstanceID: &instanceID, TaskID: &taskID,
		ParentSessionID: &parentID,
	})
}
