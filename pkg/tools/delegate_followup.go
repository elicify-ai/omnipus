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

	// [Finding 1, ADR-091 fix lane 2 — Q17/D8] A session carrying a Stop
	// marker for its OWN current generation is checked FIRST, off the plain
	// Load above — terminal or not: the founder's decision is that only a
	// newer instruction revives a stopped session, as a new generation,
	// never the steering queue (which a stopped session has no live
	// consumer left to drain). Deliberately NOT folded into the
	// Mutate-based terminal-rejection closure below: a Stop marker, once
	// stamped for a generation, is retained forever as inert history (see
	// SteerCanceller.Revive's own doc comment) — the same "checked off a
	// naked Load is safe because the fact cannot become stale" reasoning
	// executeRespond's own resumeNative uses for its terminal predicate.
	// Reviver.ReviveStoppedSession re-validates atomically under Revive's
	// own record lock regardless, so no window is opened here. This ALSO
	// avoids a real bug the Mutate-closure version of this check had: once
	// a stamped-but-never-ran session is terminalised by its own Stop
	// (Finding 5, pkg/agent/steer_cancel.go::terminaliseNeverRanStop),
	// persisting an UNCHANGED copy of that now-terminal record through
	// Mutate (the "harmless no-op" pattern the terminal-rejection closure
	// below relies on) trips the store's own immutable-terminal invariant
	// (ErrLifecycleTerminalImmutable) — Mutate's no-op persist is only
	// harmless when the record is NOT terminal.
	if rec.Stop != nil && rec.Stop.Generation == rec.Generation {
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
			"Session %s was stopped; the steering message revived it as a new generation and it has been redispatched.", sessionID,
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
	// locking guarantee, PROVIDED the record is not terminal — which is
	// exactly why the Stop-marker (possibly-terminal) case above is handled
	// before ever reaching here, never inside this closure. Ownership is
	// deliberately NOT re-checked here — see the comment above for why a
	// stale ownership read cannot happen.
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

	// correlation_id is optional for action="steer" (the schema's own
	// description: "Required for action=\"respond\" (optional for
	// \"steer\")") — a caller who wants to match a later steering_receipt
	// (issue #870) to this exact instruction supplies one; when absent,
	// EnqueueSteeringMessage mints a server-assigned reference and hands it
	// back below regardless, so the receipt is always correlatable.
	requestedCorrelationID, _ := stringArg(args, "correlation_id")
	resolvedCorrelationID, serr := t.steering.EnqueueSteeringMessage(sessionID, rec.AgentID,
		providers.Message{Role: "user", Content: text}, requestedCorrelationID)
	if serr != nil {
		return ErrorResult(fmt.Sprintf("delegate: steer: %v", serr)).WithError(serr)
	}
	return NewToolResult(fmt.Sprintf(
		"Steering message queued for session %s (correlation_id=%s); it will apply at the child's next tool boundary.",
		sessionID, resolvedCorrelationID,
	))
}

// steerReviver is satisfied by *agent.AgentLoop (t.steering's concrete
// production type, wired via SetSteeringSink) beyond DelegateSteeringSink's
// own EnqueueSteeringMessage method. Declared here, not added to
// DelegateSteeringSink itself, so a narrower test fake standing in for the
// steering sink is not forced to also implement revival — mirrors
// appendFollowUpInstruction's own optional-capability type assertion on
// t.sessionStore below.
type steerReviver interface {
	ReviveStoppedSession(ctx context.Context, sessionID string, by steer.Principal, instruction string) (bool, error)
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
// generation — the terminal follow-up bumps the record's Generation); 3P mints
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
	// [Defect 4, ADR-091 fix lane RX-DELIVERY] REFUSE to dispatch when the
	// new instruction did not land. reconstructSteeredTurn would otherwise
	// rebuild the turn from the last `user` transcript entry — the PREVIOUS
	// instruction — and the session would confidently answer the old
	// question and report it upward as a real result. The record is landed
	// failed for the same reason the dispatch-failure path just below does
	// it: a new generation was already persisted, so leaving it `queued`
	// would strand it and block its parent for ever.
	if err := t.appendFollowUpInstruction(newSessionID, instructions); err != nil {
		t.transitionLifecycle(newSessionID, session.LifecycleFailed, err.Error())
		slog.Error("delegate: follow-up instruction did not land; dispatch refused",
			"session_id", newSessionID,
			"generation", newRec.Generation,
			"agent_id", rec.AgentID,
			"error", err)
		return ErrorResult(fmt.Sprintf("delegate: follow_up: %v", err)).WithError(err)
	}

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
	// rec.Title is the durable launch-time label (session.LifecycleRecord's
	// own doc comment) — the session_id-addressed replacement for the
	// pre-ADR-091 in-memory task-state label lookup this used to read
	// (t.tasks/t.sessionIndex, deleted with the last writer that populated
	// them; see delegate.go's package doc comment).
	if rec.Title != "" {
		message = fmt.Sprintf("Follow-up for %q dispatched for session %s at generation %d (state: %s)",
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
// error. The entry id is fresh per call: a follow-up is a new instruction,
// never a duplicate of the last one.
//
// The signature deliberately keeps its original two parameters: the OTHER
// call site (delegate_park.go::resumeNative) belongs to a different fix lane,
// and adding a parameter would break its compilation rather than let it adopt
// the new error return on its own schedule. The transcript entry's agent id
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
