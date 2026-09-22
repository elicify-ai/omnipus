// delegate_park.go: Park and resume a delegation — answer a parked child's open question and resume its turn.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// delegateToolExecuteRespond carries the shared state of executeRespond across its stages.
type delegateToolExecuteRespond struct {
	t             *DelegateTool
	ctx           context.Context
	args          map[string]any
	cb            AsyncCallback
	sessionID     string
	correlationID string
	text          string
	rec           *session.LifecycleRecord
	nextState     session.LifecycleState
	failedReason  string
}

func (t *DelegateTool) executeRespond(ctx context.Context, args map[string]any, cb AsyncCallback) *ToolResult {
	dt := &delegateToolExecuteRespond{t: t, ctx: ctx, args: args, cb: cb}

	if r0, stop := dt.validateAndLoad(); stop {
		return r0
	}

	if r0, stop := dt.verifyQuestionAuthority(); stop {
		return r0
	}

	if r0, stop := dt.dispatchThirdParty(); stop {
		return r0
	}

	return dt.resumeNative()
}

// validateAndLoad validates the respond request and loads its parked lifecycle record.
func (dt *delegateToolExecuteRespond) validateAndLoad() (*ToolResult, bool) {
	if dt.t.lifecycle == nil {
		return ErrorResult("delegate: no lifecycle store configured"), true
	}
	// HIGH-1 (14-reviewer sign-off): a parked child's turn has already ENDED
	// (TurnEndStatusParked, via message_parent(wait=true)) — respond must
	// actually RESUME it, not merely unpark the lifecycle record and hope a
	// live consumer is still around to read the steering queue (it never is
	// — see the redispatch comment on the native path below). That resume
	// goes through the same SessionLauncher Dispatch machinery follow_up
	// uses, so a missing launcher must be rejected up front, before touching
	// anything, exactly like executeFollowUp's own posture (checked before
	// any argument parsing) — never as a failure discovered mid-flow after
	// some other state has already changed.
	if dt.t.launcher == nil {
		return ErrorResult("delegate: respond: no session launcher configured to resume the session"), true
	}
	var err error
	dt.sessionID, err = requiredStringArg(dt.args, "session_id")
	if err != nil {
		return ErrorResult(err.Error()), true
	}
	dt.correlationID, err = requiredStringArg(dt.args, "correlation_id")
	if err != nil {
		return ErrorResult(err.Error()), true
	}
	dt.text, err = requiredStringArg(dt.args, "text")
	if err != nil {
		return ErrorResult(err.Error()), true
	}

	// rec is loaded UNLOCKED here only for the fast-path pre-checks
	// (ownership, needs_input/correlation match, authority via inbox Drain).
	// The actual state transition is atomic — see the Mutate call below,
	// which re-verifies state + correlation UNDER the per-session striped
	// lock (Correctness-MAJOR-3) so a concurrent respond/cancel cannot
	// double-apply. Do NOT wrap this Load in a manual Lock(); the transition
	// uses Mutate, which takes the lock once internally.
	var lerr error
	dt.rec, lerr = dt.t.lifecycle.Load(dt.sessionID)
	if lerr != nil {
		return ErrorResult(fmt.Sprintf("delegate: respond: %v", lerr)), true
	}
	if verr := dt.t.verifyCallerOwnsSession(dt.ctx, dt.rec); verr != nil {
		return ErrorResult(fmt.Sprintf("delegate: respond: %v", verr)), true
	}

	if dt.rec.State != session.LifecycleNeedsInput || dt.rec.NeedsInput == nil || dt.rec.NeedsInput.CorrelationID != dt.correlationID {
		return ErrorResult(fmt.Sprintf(
			"delegate: respond: session %s is not parked on correlation_id %q", dt.sessionID, dt.correlationID,
		)), true
	}
	if cerr := dt.t.checkSteerCaps(dt.sessionID, dt.text); cerr != nil {
		return ErrorResult(fmt.Sprintf("delegate: respond: %v", cerr)).WithError(cerr), true
	}
	return nil, false
}

// verifyQuestionAuthority confirms the target inbox question permits a parent-authored answer.
func (dt *delegateToolExecuteRespond) verifyQuestionAuthority() (*ToolResult, bool) {
	// R§8.2/FR-132: reject a respond targeting an owner_required question.
	// PHASE-1 SCOPING: this reads the original question's CHILD-AUTHORED
	// authority tag directly from the inbox — the runtime content-based
	// upgrade heuristic (deriveQuestionAuthority, FR-139) is Group F,
	// Phase 2; this wave implements only the mandatory fail-closed DEFAULT
	// (an omitted tag already resolved to owner_required at
	// message_parent.go's Append time, FR-131), which IS enforced here.
	//
	// FAIL-CLOSED (MAJOR-2): the authority check is a POSITIVE confirmation
	// that the target question is safe to answer, not a negative scan that
	// silently passes when the question can't be inspected. Previously a
	// Drain error OR a question excluded from the Drain result (ACKED by an
	// earlier inbox_ack) let the check silently pass — letting a parent
	// answer an owner_required question. Now every condition that prevents
	// the target question from being positively inspected DENIES the
	// respond:
	//   1. inbox not configured → deny (can't verify authority)
	//   2. Drain errors → deny (can't verify authority)
	//   3. target question absent from the Drain (acked / never existed)
	//      → deny (can't verify authority — R§8.2's fail-closed default)
	// Only when the question IS found in the drain AND its authority is not
	// owner_required does the respond proceed. Resolving the question
	// independent of ack state would need a new inbox-store primitive
	// (outside this wave's write-set); fail-closed-on-absent is the safe
	// posture until that lands.
	if dt.t.inbox == nil {
		return ErrorResult("delegate: respond: no message inbox configured to verify question authority"), true
	}
	msgs, _, _, derr := dt.t.inbox.Drain(dt.rec.SteeringSessionID(), dt.sessionID, "", 0)
	if derr != nil {
		return ErrorResult(fmt.Sprintf("delegate: respond: %v", derr)).WithError(derr), true
	}
	questionVerified := false
	for _, m := range msgs {
		kind, kerr := m.Discriminator()
		if kerr != nil || kind != "question" {
			continue
		}
		q, qerr := m.AsSessionMessageQuestion()
		if qerr != nil || q.CorrelationId != dt.correlationID {
			continue
		}
		if q.Authority != nil && string(*q.Authority) == "owner_required" {
			return ErrorResult(fmt.Sprintf(
				"delegate: respond: question %q requires owner/human authority and cannot be "+
					"answered by a parent directly (R§8.2)", dt.correlationID,
			)), true
		}
		questionVerified = true
		break
	}
	if !questionVerified {
		return ErrorResult(fmt.Sprintf(
			"delegate: respond: question %q could not be verified in the inbox (it may be acked or absent) — "+
				"denying by default to enforce owner_required authority (R§8.2)", dt.correlationID,
		)), true
	}
	return nil, false
}

// dispatchThirdParty dispatches a corrective successor for a parked third-party session.
func (dt *delegateToolExecuteRespond) dispatchThirdParty() (*ToolResult, bool) {
	// HIGH-1 ordering fix (14-reviewer sign-off): the un-park lifecycle
	// transition used to commit BEFORE delivery was even attempted — an
	// enqueue failure then left the record flipped away from needs_input
	// (NeedsInput cleared) with NO way to retry respond(), since the
	// correlation_id match it re-checks was already erased. Delivery is now
	// attempted FIRST, while the session is still safely parked; the
	// lifecycle is flipped only once delivery has genuinely succeeded, so a
	// failure at any point below leaves the session exactly as parked as it
	// was, ready for the caller to retry.
	//
	// Correctness-MAJOR-2 (3P respond state): a 3P respond spawns a NEW
	// corrective session (spawnCorrectiveFollowUp) and never warm-resumes the
	// original. Flipping the ORIGINAL to `running` would leave a record with
	// no live runtime turn, which status would falsely report as `running`
	// and the Phase-2 boot sweep would re-classify `failed(interrupted)`,
	// corrupting the terminal record. Instead the original is marked terminal
	// `cancelled` — recording via FailedReason that it was superseded by the
	// corrective re-dispatch. (FailedReason is the record's free-text "why
	// this ended" field; using it for a cancelled-via-supersession is more
	// informative than leaving the cancellation unexplained, and adding a
	// dedicated `superseded` state would be a wire-type change outside this
	// wave's scope.) The native path keeps the original `running` transition.
	dt.nextState = session.LifecycleRunning

	if dt.rec.Is3P {
		dt.nextState = session.LifecycleCancelled
		dt.failedReason = "superseded by corrective re-dispatch (3P respond)"
	}

	if dt.rec.Is3P {
		// D5: 3P respond spawns a NEW corrective session — never an
		// in-place warm resume (external CLIs have no such primitive).
		// spawnCorrectiveFollowUp Persists the new generation under a
		// DIFFERENT session_id and dispatches it; it never touches THIS
		// (the original) record. Dispatch it FIRST — a failure here (e.g. a
		// Persist I/O error, or the inner executeAsync call) never marks the
		// original as superseded, so it stays parked and retryable.
		dispatch := dt.t.spawnCorrectiveFollowUp(dt.ctx, dt.sessionID, dt.rec,
			fmt.Sprintf("Answer to your question (correlation_id=%s): %s", dt.correlationID, dt.text), dt.cb)
		if dispatch.IsError {
			return dispatch, true
		}
		// The corrective successor is confirmed dispatched — only now mark
		// the ORIGINAL terminal (superseded by the successor).
		if merr := dt.t.lifecycle.Mutate(dt.sessionID, func(cur *session.LifecycleRecord) error {
			if cur == nil {
				return session.ErrLifecycleNotFound
			}
			if cur.State != session.LifecycleNeedsInput || cur.NeedsInput == nil || cur.NeedsInput.CorrelationID != dt.correlationID {
				return fmt.Errorf("session %s is not parked on correlation_id %q", dt.sessionID, dt.correlationID)
			}
			cur.State = dt.nextState
			cur.NeedsInput = nil
			cur.FailedReason = dt.failedReason
			return nil
		}); merr != nil {
			// The corrective successor is already running by this point —
			// returning an error here would misleadingly tell the caller
			// "respond failed" when the answer was in fact delivered. Log it
			// instead; the original record is a display/bookkeeping nicety at
			// this stage, not the source of truth for whether the answer landed.
			slog.Warn("delegate: respond: 3P corrective successor dispatched but the original could not be marked superseded",
				"session_id", dt.sessionID, "error", merr)
		}
		return dispatch, true
	}
	return nil, false
}

// resumeNative delivers the answer and resumes a native parked session.
func (dt *delegateToolExecuteRespond) resumeNative() *ToolResult {
	// Atomic claim: re-verify state + correlation UNDER the lock
	// (Correctness-MAJOR-3) so a concurrent respond/cancel on this same
	// session cannot double-apply. This MUST run BEFORE the redispatch below,
	// not after: executeAsync's own internal transitionLifecycle call
	// unconditionally (no correlation re-check) forces the record to
	// `running` the instant dispatch begins, so a Mutate placed after that
	// call would always observe a record no longer `needs_input` — even on
	// the single-caller happy path — and spuriously reject it. Placing the
	// claim here instead, immediately before a redispatch that cannot fail
	// synchronously (t.spawner == nil was already rejected at the top of this
	// function, before any side effect), delivers the same net effect the
	// ordering fix requires: the record is only ever flipped once delivery
	// (the enqueue above) has already succeeded, and never in a way an
	// enqueue failure could leave half-applied.
	if merr := dt.t.lifecycle.Mutate(dt.sessionID, func(cur *session.LifecycleRecord) error {
		if cur == nil {
			return session.ErrLifecycleNotFound
		}
		if cur.State != session.LifecycleNeedsInput || cur.NeedsInput == nil || cur.NeedsInput.CorrelationID != dt.correlationID {
			return fmt.Errorf("session %s is not parked on correlation_id %q", dt.sessionID, dt.correlationID)
		}
		cur.State = dt.nextState
		cur.NeedsInput = nil
		cur.FailedReason = dt.failedReason
		return nil
	}); merr != nil {
		return ErrorResult(fmt.Sprintf("delegate: respond: failed to resume session: %v", merr)).WithError(merr)
	}

	instruction := fmt.Sprintf("Answer to your question (correlation_id=%s): %s", dt.correlationID, dt.text)
	dt.t.appendFollowUpInstruction(dt.sessionID, instruction)
	if _, err := dt.t.launcher.Dispatch(dt.ctx, dt.sessionID, dt.rec.Generation); err != nil {
		return ErrorResult(fmt.Sprintf("delegate: respond: dispatch: %v", err)).WithError(err)
	}

	resp := generated.DelegateRespondResponse{Acknowledged: true}
	payload, merr := json.Marshal(resp)
	if merr != nil {
		return NewToolResult("Answer delivered; session resumed.")
	}
	return NewToolResult(string(payload))
}
