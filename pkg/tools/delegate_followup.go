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
	// forward unchanged even across follow_up generations (see
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
	if verr := t.verifyCallerOwnsSession(ctx, rec); verr != nil {
		return ErrorResult(fmt.Sprintf("delegate: steer: %v", verr))
	}
	// Not available to external-CLI (3P) sessions: every steering-queue drain
	// site lives in the native turn engine (pkg/agent/loop.go,
	// pkg/agent/steering.go) — runExternalCLISubTurn
	// (pkg/agent/external_dispatch.go) never drains it, so a message queued
	// here for a 3P child is silently orphaned forever (no live consumer ever
	// reads it, and there is no "next tool boundary" concept for an external
	// CLI's own turn loop). Unlike respond/follow_up, steer has no corrective-
	// redispatch fallback to degrade to — injecting an instruction mid-turn is
	// meaningless for a session that isn't running on this engine's turn loop
	// at all. Mirrors message_parent's identical Is3P posture (D5).
	if rec.Is3P {
		return ErrorResult(fmt.Sprintf(
			"delegate: steer: not_steerable: external command-line session %s runs on an external CLI "+
				"(claude-code/codex/opencode) with no steering-queue drain in its dispatch path; use "+
				"action=\"respond\" (which redispatches a corrective session) or action=\"follow_up\" instead",
			sessionID,
		))
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
	// locking guarantee. Ownership is deliberately NOT re-checked here — see
	// the comment above for why a stale ownership read cannot happen.
	if merr := t.lifecycle.Mutate(sessionID, func(cur *session.LifecycleRecord) error {
		if cur == nil {
			return session.ErrLifecycleNotFound
		}
		if cur.Terminal() {
			return fmt.Errorf("session %s is terminal (%s) and cannot be steered", sessionID, cur.State)
		}
		rec = cur
		return nil
	}); merr != nil {
		return ErrorResult(fmt.Sprintf("delegate: steer: %v", merr))
	}

	if cerr := t.checkSteerCaps(sessionID, text); cerr != nil {
		return ErrorResult(fmt.Sprintf("delegate: steer: %v", cerr)).WithError(cerr)
	}

	if serr := t.steering.EnqueueSteeringMessage(sessionID, rec.AgentID, providers.Message{Role: "user", Content: text}); serr != nil {
		return ErrorResult(fmt.Sprintf("delegate: steer: %v", serr)).WithError(serr)
	}
	return NewToolResult(fmt.Sprintf(
		"Steering message queued for session %s; it will apply at the child's next tool boundary.", sessionID,
	))
}

func (t *DelegateTool) executeFollowUp(ctx context.Context, args map[string]any, cb AsyncCallback) *ToolResult {
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

	// follow_up's own schema documents "text" as the instruction field
	// (grouped with steer/respond everywhere the schema/description mentions
	// them together — e.g. session_id's own description lists "steer/
	// respond/cancel/follow_up/peek" as one family), but this action used to
	// read ONLY args["task"], silently falling back to a generic "Continue
	// the previous task." placeholder whenever a caller passed "text" per
	// the documented sibling-action convention — dropping the caller's real
	// instruction with no error at all. "task" survives as a deprecated
	// back-compat alias (text wins when both are present); an instruction
	// that is blank after trimming both is now a validation error, never a
	// silent placeholder substitution.
	instruction, ierr := followUpInstructionArg(args)
	if ierr != nil {
		return ErrorResult(ierr.Error())
	}

	rec, lerr := t.lifecycle.Load(sessionID)
	if lerr != nil {
		return ErrorResult(fmt.Sprintf("delegate: follow_up: %v", lerr))
	}
	if verr := t.verifyCallerOwnsSession(ctx, rec); verr != nil {
		return ErrorResult(fmt.Sprintf("delegate: follow_up: %v", verr))
	}
	// NOTE: this is a naked Load+Terminal check, NOT the LifecycleStore.Mutate
	// RMW that executeSteer/executeRespond use to close their check-then-act
	// TOCTOU window. The polarity is inverted here: follow_up RESUMES a
	// terminal session (spawnCorrectiveFollowUp re-queues it as a new generation), so
	// the gate is `!rec.Terminal() -> reject`, and the immutable-terminal
	// invariant (L-3) means a session that is terminal at this Load STAYS
	// terminal — there is no concurrent transition that can flip it back to
	// non-terminal under us and race the branch. spawnCorrectiveFollowUp
	// below performs its OWN Persist under the lifecycle store's per-session
	// striped lock to mint the new generation; this read only decides whether
	// to enter that path. Do not "fix" this toward Mutate to match steer —
	// it would be a no-op lock acquisition on an immutable predicate.
	if !rec.Terminal() {
		return ErrorResult(fmt.Sprintf(
			"delegate: follow_up: session %s is not terminal (state=%s) — follow_up only resumes a finished session",
			sessionID, rec.State,
		))
	}

	return t.spawnCorrectiveFollowUp(ctx, sessionID, rec, instruction, cb)
}

// followUpInstructionArg resolves the new instruction for action="follow_up":
// "text" is the documented field, "task" is a deprecated back-compat alias
// consulted only when "text" is absent/blank. Returns an error naming the
// missing field when neither yields a non-blank string — no silent
// placeholder substitution.
func followUpInstructionArg(args map[string]any) (string, error) {
	if rawText, present := args["text"]; present && rawText != nil {
		s, ok := rawText.(string)
		if !ok {
			return "", fmt.Errorf("text must be a string")
		}
		if trimmed := strings.TrimSpace(s); trimmed != "" {
			return trimmed, nil
		}
	}
	if rawTask, present := args["task"]; present && rawTask != nil {
		s, ok := rawTask.(string)
		if !ok {
			return "", fmt.Errorf("task must be a string")
		}
		if trimmed := strings.TrimSpace(s); trimmed != "" {
			return trimmed, nil
		}
	}
	return "", fmt.Errorf(
		`text is required and must be a non-empty string for action="follow_up" (the new instruction to resume the session with; "task" is accepted as a deprecated alias)`,
	)
}

// spawnCorrectiveFollowUp is the shared mechanics behind `follow_up` (native
// and 3P) and a 3P `respond` (D5 — a 3P child never warm-resumes; every
// continuation is a NEW corrective session carrying the prior context).
// Native follow_up reuses sessionID verbatim (warm resume, same session, new
// generation — see agent.spawnSubTurn's childID-reuse mechanism); 3P mints a
// NEW session_id (cold respawn), linked back via ResumedFrom.
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

	label := ""
	t.mu.Lock()
	if taskID, ok := t.sessionIndex[sessionID]; ok {
		if st, ok := t.tasks[taskID]; ok {
			label = st.Label
			instructions = fmt.Sprintf("Original task: %s\n\n%s", st.Task, instructions)
		}
	}
	t.mu.Unlock()

	// The whole-struct copy is load-bearing for FR-034: ParentAgentID (and
	// SteeringSessionID/OriginChannel/OriginChatID with it) MUST be carried
	// forward onto every generation mint. It is deliberately CARRIED FORWARD
	// from the prior generation rather than re-sourced from ToolAgentID(ctx)
	// — the follow_up caller is not necessarily the agent that originally
	// spawned the session, and re-sourcing would silently re-parent it. Do
	// not replace this copy with field-by-field construction.
	newRec := *rec
	newRec.SessionID = newSessionID
	newRec.Generation = rec.Generation + 1
	newRec.ResumedFrom = sessionID
	newRec.State = session.LifecycleQueued
	newRec.FailedReason = ""
	newRec.NeedsInput = nil
	if rec.Is3P {
		if err := t.cloneCorrectiveSessionIdentity(sessionID, newSessionID, rec); err != nil {
			return ErrorResult(fmt.Sprintf("delegate: follow_up: failed to create corrective session: %v", err)).WithError(err)
		}
	}
	if err := t.lifecycle.Persist(&newRec); err != nil {
		return ErrorResult(fmt.Sprintf("delegate: follow_up: failed to persist new generation: %v", err)).WithError(err)
	}
	t.appendFollowUpInstruction(newSessionID, instructions)

	dispatch, err := t.launcher.Dispatch(ctx, newSessionID, newRec.Generation)
	if err != nil {
		t.transitionLifecycle(newSessionID, session.LifecycleFailed, err.Error())
		slog.Error("delegate: follow-up dispatch failed",
			"session_id", newSessionID,
			"generation", newRec.Generation,
			"agent_id", rec.AgentID,
			"error", err)
		return ErrorResult(fmt.Sprintf("delegate: follow_up: dispatch failed: %v", err)).WithError(err)
	}

	message := fmt.Sprintf("Follow-up dispatched for session %s at generation %d (state: %s)",
		newSessionID, dispatch.Generation, dispatch.State)
	if dispatch.State == steer.DispatchQueued {
		message += fmt.Sprintf(", queue position %d", dispatch.QueuePosition)
	}
	if label != "" {
		message = fmt.Sprintf("Follow-up for %q dispatched for session %s at generation %d (state: %s)",
			label, newSessionID, dispatch.Generation, dispatch.State)
	}
	return NewToolResult(message)
}

// appendFollowUpInstruction adds the new user instruction to the session's
// durable model history before Dispatch reconstructs the turn. The concrete
// production store implements this optional write capability; narrow status
// fakes used by older tests remain read-only.
func (t *DelegateTool) appendFollowUpInstruction(sessionID, instruction string) {
	writer, ok := t.sessionStore.(interface {
		AddMessage(sessionKey, role, content string)
	})
	if ok && strings.TrimSpace(instruction) != "" {
		writer.AddMessage(sessionID, "user", instruction)
	}
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
