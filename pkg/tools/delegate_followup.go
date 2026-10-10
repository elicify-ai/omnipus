// delegate_followup.go: Send a follow-up message to a delegation — steer one still running, or follow up one already finished.

package tools

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// followupRefusal is a refusal sentence authored in this file (a race the
// caller can act on). It is the only error text from a lifecycle Mutate that
// may reach the calling agent as written; every other Mutate error is
// store-level and is replaced by a fixed sentence.
type followupRefusal struct{ text string }

func (e *followupRefusal) Error() string { return e.text }

// refusalTexter is implemented by an error whose text was authored for a
// person or model to read (the agent package's curated revival refusals), so
// the tool may show it as written. Any other revival error is shown as a fixed
// sentence.
type refusalTexter interface{ RefusalText() string }

// controlFailure is the curated refusal for a store or revival failure whose
// own text can carry filesystem paths or session-file detail: the calling agent
// reads a fixed sentence, the gateway log keeps the cause, and the cause stays
// reachable through errors.Is/As on the result's error.
func controlFailure(action, fixed string, cause error) *ToolResult {
	slog.Warn("delegate: "+action+" failed; the caller was given a fixed sentence", "error", cause)
	return ErrorResult("delegate: " + action + ": " + fixed).WithError(cause)
}

// displayableCause returns the text of err that may reach the calling agent: an
// authored refusal's own sentence (refusalTexter), or the fixed text of a plain
// context error. Anything else — a wrapped store error that can carry paths —
// is not displayable.
func displayableCause(err error) (string, bool) {
	var texter refusalTexter
	if errors.As(err, &texter) {
		return texter.RefusalText(), true
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return context.DeadlineExceeded.Error(), true
	case errors.Is(err, context.Canceled):
		return context.Canceled.Error(), true
	}
	return "", false
}

// lifecycleLoadFailure is controlFailure for a failed lifecycle Load. A genuinely
// absent record names the looked-up id (so a mistyped id is visible); anything
// else is a store fault shown as a fixed sentence.
func lifecycleLoadFailure(action, sessionID string, cause error) *ToolResult {
	if errors.Is(cause, session.ErrLifecycleNotFound) {
		return ErrorResult(fmt.Sprintf("delegate: %s: session %s was not found", action, sessionID)).WithError(cause)
	}
	return controlFailure(action, fmt.Sprintf("the record of session %s could not be read or updated right now; retry shortly", sessionID), cause)
}

// reviveFailure is controlFailure for a failed revival: a curated refusal is
// shown as written, anything else as a fixed sentence.
func reviveFailure(action, sessionID string, cause error) *ToolResult {
	var curated refusalTexter
	if errors.As(cause, &curated) {
		return ErrorResult("delegate: " + action + ": " + curated.RefusalText()).WithError(cause)
	}
	return controlFailure(action, fmt.Sprintf("session %s could not be revived right now; retry shortly", sessionID), cause)
}

// checkSteerCaps enforces the steer/respond body-size cap (65,536 bytes by
// default, session.DefaultSteerBodyBytes) and the per-target-session rate cap
// (60 per minute by default, session.DefaultSteerRatePerMinute) — ADR-053
// §Contract Surface "Caps", defaults per session-core C-LIMIT. The rate window
// is keyed by target session_id on THIS tool instance, and each agent has its
// own DelegateTool (pkg/agent/loop_wire.go::registerDelegationTools), so the
// effective limit is per sending agent per target. Returns a clear, typed error naming the exceeded cap;
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
		return lifecycleLoadFailure("steer", sessionID, lerr)
	}
	by, verr := t.verifyCallerPrincipal(ctx, rec)
	if verr != nil {
		return ErrorResult(fmt.Sprintf("delegate: steer: %v", verr))
	}
	// External-CLI (3P) sessions deliver a live instruction by interrupt + real
	// native-conversation resume (FR-043), not by the native steering queue:
	// the production steering sink exposes DeliverExternalCLIInstruction, which
	// lands the instruction in the child's queue AND interrupts its live CLI run
	// so the post-turn drain re-enters the external-CLI body with resume set —
	// the new instruction S reaches the SAME native conversation. When the sink
	// cannot deliver (a fake, or a session with genuinely NO live conversation)
	// the named not_steerable refusal is kept, so the instruction is never
	// silently dropped and the "no no-op success" rule (BDD-05.6) holds.
	// Mirrors message_parent's identical Is3P posture (D5) for the fallback.
	if rec.Is3P {
		if deliverer, ok := t.steering.(steerExternalCLIDeliverer); ok {
			if cerr := t.checkSteerCaps(sessionID, text); cerr != nil {
				return ErrorResult(fmt.Sprintf("delegate: steer: %v", cerr)).WithError(cerr)
			}
			requestedCorrelationID, _ := stringArg(args, "correlation_id")
			resolved, derr := deliverer.DeliverExternalCLIInstruction(
				ctx, sessionID, rec.AgentID,
				providers.Message{Role: "user", Content: text}, requestedCorrelationID)
			if derr != nil {
				// Truthful visible refusal: no live conversation, or the
				// interrupt/enqueue failed. Never a silent success. Authored
				// refusals and plain context errors are shown; a store fault is
				// a fixed sentence with its cause kept for logs and errors.Is.
				if text, ok := displayableCause(derr); ok {
					return ErrorResult("delegate: steer: not_steerable: " + text).WithError(derr)
				}
				return controlFailure("steer", fmt.Sprintf("not_steerable: the live instruction for session %s could not be delivered right now; retry shortly", sessionID), derr)
			}
			return NewToolResult(fmt.Sprintf(
				"Steering message delivered to external CLI session %s by interrupt + native-conversation resume (correlation_id=%s); it will reach the same conversation.",
				sessionID, resolved,
			))
		}
		return ErrorResult(fmt.Sprintf(
			"delegate: steer: not_steerable: external command-line session %s runs on an external CLI "+
				"(claude-code/codex/opencode) and this steering sink cannot deliver a live instruction to it; "+
				"action=\"resume\" continues a stopped or finished one whose CLI conversation this gateway still "+
				"retains, or runs one that never started its CLI for the first time (otherwise start a new delegation)",
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
		return t.steerReviveStopped(ctx, sessionID, by, text, rec.Terminal())
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
			return &followupRefusal{fmt.Sprintf("session %s is terminal (%s) and cannot be steered", sessionID, cur.State)}
		}
		if cur.State == session.LifecycleStopped {
			stoppedInRace = true
			rec = cur
			return nil
		}
		if cur.Stop != nil && cur.Stop.Generation == cur.Generation {
			return &followupRefusal{fmt.Sprintf("session %s is stopping (a stop is in flight for its current generation); retry the steer once it has stopped", sessionID)}
		}
		rec = cur
		return nil
	}); merr != nil {
		var refusal *followupRefusal
		if errors.As(merr, &refusal) {
			return ErrorResult("delegate: steer: " + refusal.text).WithError(merr)
		}
		return lifecycleLoadFailure("steer", sessionID, merr)
	}

	if stoppedInRace {
		return t.steerReviveStopped(ctx, sessionID, by, text, false)
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
		if text, ok := displayableCause(serr); ok {
			return ErrorResult("delegate: steer: " + text).WithError(serr)
		}
		return controlFailure("steer", fmt.Sprintf("the steering message for session %s could not be queued right now; retry shortly", sessionID), serr)
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

// steerReviveStopped is the shared mechanics of executeSteer's two
// landed-stopped revive shapes — the plain-Load branch that observed
// Terminal() or LifecycleStopped, and the stoppedInRace shape the Mutate
// closure signals out — which were line-for-line identical apart from the
// final message. It runs the steer caps check first (a rejected steer must
// be visible to the parent, never silently dropped), then revives the
// session through ReviveStoppedSession with the steering message as the new
// instruction, and reports the outcome. nextRound is true only for a record
// that was already terminal (the message starts its next round); the
// stoppedInRace shape is always a stopped resume. Behaviour and result
// texts are exactly the two branches' own.
func (t *DelegateTool) steerReviveStopped(ctx context.Context, sessionID string, by steer.Principal, text string, nextRound bool) *ToolResult {
	if cerr := t.checkSteerCaps(sessionID, text); cerr != nil {
		return ErrorResult(fmt.Sprintf("delegate: steer: %v", cerr)).WithError(cerr)
	}
	reviver, ok := t.steering.(steerReviver)
	if !ok {
		return ErrorResult(fmt.Sprintf("delegate: steer: session %s is stopped and cannot be revived: no reviver configured", sessionID))
	}
	revived, rerr := reviver.ReviveStoppedSession(ctx, sessionID, by, text)
	if rerr != nil {
		return reviveFailure("steer", sessionID, rerr)
	}
	if !revived {
		return ErrorResult(fmt.Sprintf("delegate: steer: session %s could not be revived", sessionID))
	}
	if nextRound {
		return NewToolResult(fmt.Sprintf(
			"Session %s had finished; the steering message started its next round.", sessionID,
		))
	}
	return NewToolResult(fmt.Sprintf(
		"Session %s was stopped; the steering message resumed it on the same conversation.", sessionID,
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

// steerExternalCLIDeliverer is the optional capability that lets a steering
// sink deliver an instruction to a LIVE external-CLI (3P) child by interrupt +
// native-conversation resume (FR-043) — the production *agent.AgentLoop
// implements it; a test fake that cannot deliver simply does not, and keeps
// executeSteer's named not_steerable refusal. Declared here (not added to
// DelegateSteeringSink) for the same reason steerReviver stays narrow: only the
// 3P steer path needs it, and forcing every sink implementer to know about
// external-CLI resume would couple the interface to a notion that does not
// exist for the native path.
type steerExternalCLIDeliverer interface {
	DeliverExternalCLIInstruction(ctx context.Context, sessionID, agentID string, msg providers.Message, correlationID string) (string, error)
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
// agents into retry loops). A 3P (external-CLI) child resumes through the
// same ReviveStoppedSession primitive, which either continues its retained
// native CLI conversation or refuses visibly; it is NEVER re-dispatched as a
// new corrective session (that bypassed the creation-edge authorization,
// DEL-31 / FR-043).
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
		// A mistyped id is named; a store fault is a fixed sentence.
		return lifecycleLoadFailure("resume", sessionID, lerr)
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

	// Stopped → same-generation resume; done/failed → next round. An external-CLI
	// (3P) child takes this SAME path: the steering sink either resumes its
	// retained native CLI conversation or refuses visibly — never a new session
	// (FR-043, DEL-31).
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
		return reviveFailure("resume", sessionID, rerr)
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
