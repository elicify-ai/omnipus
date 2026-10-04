// delegate_respond.go: the parent's reply-to-message action — deliver the
// answer downward as an ordinary steering message (ADR-20261004, locked
// decisions 5–7).
//
// ADR-20260928 D1's person-question machinery was withdrawn by
// ADR-20260928's amendment ("Steering commands: no person question",
// 2026-10-04): there is no owner_required park, no owner-answer acceptor
// (the former verifyQuestionAuthority Who/When/Which/Once checks here),
// and no parked-record resume path. A respond is now an ordinary steering
// message — Correction C1's message/state table applies to the recipient:
// working mid-turn → delivered into the current turn (never stopping it);
// stopped → same-generation resume; done/failed → next round. A 3P child
// keeps its D5 corrective re-dispatch (no warm-resume primitive).

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
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
	by            steer.Principal
	instruction   string
}

func (t *DelegateTool) executeRespond(ctx context.Context, args map[string]any, cb AsyncCallback) *ToolResult {
	dt := &delegateToolExecuteRespond{t: t, ctx: ctx, args: args, cb: cb}

	if r0, stop := dt.validateAndLoad(); stop {
		return r0
	}

	if r0, stop := dt.dispatchThirdParty(); stop {
		return r0
	}

	return dt.deliverNative()
}

// validateAndLoad validates the respond request and loads the target record.
func (dt *delegateToolExecuteRespond) validateAndLoad() (*ToolResult, bool) {
	// A stopped or finished recipient is resumed through the same
	// SessionLauncher Dispatch machinery resume uses, so a missing launcher
	// must be rejected up front, before touching anything, exactly like
	// executeResume's own posture (checked before any argument parsing).
	if dt.t.launcher == nil {
		return ErrorResult("delegate: respond: no session launcher configured to resume the session"), true
	}
	if dt.t.lifecycle == nil {
		return ErrorResult("delegate: no lifecycle store configured"), true
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

	rec, lerr := dt.t.lifecycle.Load(dt.sessionID)
	if lerr != nil {
		return ErrorResult(fmt.Sprintf("delegate: respond: %v", lerr)), true
	}
	dt.rec = rec
	by, verr := dt.t.verifyCallerPrincipal(dt.ctx, rec)
	if verr != nil {
		return ErrorResult(fmt.Sprintf("delegate: respond: %v", verr)), true
	}
	dt.by = by
	dt.instruction = respondAnswerInstruction(dt.correlationID, dt.text)
	if cerr := dt.t.checkSteerCaps(dt.sessionID, dt.text); cerr != nil {
		return ErrorResult(fmt.Sprintf("delegate: respond: %v", cerr)).WithError(cerr), true
	}
	return nil, false
}

// dispatchThirdParty handles a 3P recipient: D5 — a 3P child never
// warm-resumes; the answer spawns a NEW corrective session carrying the
// prior context, and the original is marked stopped as superseded by that
// successor. Dispatch it FIRST — a failure here (e.g. a Persist I/O error,
// or the inner executeAsync call) never marks the original as superseded,
// so it stays retryable.
func (dt *delegateToolExecuteRespond) dispatchThirdParty() (*ToolResult, bool) {
	if !dt.rec.Is3P {
		return nil, false
	}
	dispatch := dt.t.spawnCorrectiveFollowUp(dt.ctx, dt.sessionID, dt.rec, dt.instruction, dt.cb)
	if dispatch.IsError {
		return dispatch, true
	}
	// The corrective successor is confirmed dispatched — only now mark
	// the ORIGINAL stopped (superseded by the successor). A terminal
	// original is left alone (L-3 immutable-terminal invariant): the
	// corrective spawn already carried its context, and there is nothing
	// left to supersede.
	if merr := dt.t.lifecycle.Mutate(dt.sessionID, func(cur *session.LifecycleRecord) error {
		if cur == nil {
			return session.ErrLifecycleNotFound
		}
		if cur.Terminal() || cur.State == session.LifecycleStopped {
			return nil
		}
		cur.State = session.LifecycleStopped
		cur.FailedReason = "superseded by corrective re-dispatch (3P respond)"
		// No cascade/stamp precedes this write — a 3P respond that
		// supersedes its own original with a freshly dispatched corrective
		// session (D5) is closest to redirect_pause (a new instruction
		// superseding the current generation), never a Stop/cascade.
		if cur.Stop != nil && cur.Stop.Generation == cur.Generation {
			cur.Stop = nil
		}
		// Correction C3, the same discipline every other fence-less stop
		// obeys (boot_sweep.go::failInterrupted): the transition is ledgered
		// IN THIS SAME lock hold — the ledger, not the resumable stop note,
		// is the stop's durable history and the direct-parent notice's
		// discovery source. The note's Seq is the allocated ledger sequence,
		// never the generation. A ledger failure refuses the whole mutation:
		// the stop does not land note-only, and the original stays live.
		at := dt.t.now().UTC()
		actor := session.StopActorAgent(ToolAgentID(dt.ctx))
		landed := session.LandedStop{
			ParentSessionID: cur.SteeringSessionID(),
			Generation:      cur.Generation,
			Cause:           session.StopCauseRedirectPause,
			Actor:           actor,
			At:              at,
		}
		if cur.ExecutionID != nil {
			landed.RunID = cur.ExecutionID.RunID
			landed.BootSeq = cur.ExecutionID.BootSeq
		}
		seq, ledgerErr := dt.t.lifecycle.RecordFencelessLandedStopLocked(cur.SessionID, landed)
		if ledgerErr != nil {
			return fmt.Errorf("steer: respond: supersede stop for %q not landed: its transition could not be ledgered: %w",
				cur.SessionID, ledgerErr)
		}
		cur.StopNote = &session.StopNote{
			At:    at,
			By:    actor,
			Seq:   uint64(seq),
			Cause: session.StopCauseRedirectPause,
		}
		return nil
	}); merr != nil {
		// The corrective successor is already running by this point —
		// returning an error here would misleadingly tell the caller
		// "respond failed" when the answer was in fact delivered. But this
		// failure is not a display nicety: with the transition refused, the
		// supersede STOP DID NOT LAND — the original keeps its pre-respond
		// state and is not superseded until a retry lands the ledgered stop.
		// The Warn below is the visible record of that.
		slog.Warn("delegate: respond: 3P corrective successor dispatched but the supersede stop DID NOT LAND — the original session remains live and unsuperseded",
			"session_id", dt.sessionID, "error", merr)
	}
	return dispatch, true
}

// deliverNative delivers the answer to a native child as an ordinary
// steering message. A stopped or terminal record revives through
// ReviveStoppedSession — stopped: same-generation resume; done/failed:
// next round (Correction C1's message/state table, steering-message row) —
// exactly what the delegate tool's own steer and a chat message do. A live
// child takes the ordinary steering queue: delivered into the current turn
// at its next tool boundary, never stopping it (locked decision 5).
func (dt *delegateToolExecuteRespond) deliverNative() *ToolResult {
	// A session carrying a Stop marker for its OWN current generation — OR a
	// terminal record (ADR-093 D4) — is checked off the plain Load above,
	// mirroring executeSteer: the check-then-act reasoning is identical (a
	// stamped Stop marker and a terminal record are facts that cannot become
	// stale), and ReviveStoppedSession re-validates atomically under its own
	// record lock.
	if dt.rec.Terminal() || dt.rec.Stopped() {
		reviver, ok := dt.t.steering.(steerReviver)
		if !ok {
			return ErrorResult(fmt.Sprintf("delegate: respond: session %s is stopped and cannot be resumed: no reviver configured", dt.sessionID))
		}
		revived, rerr := reviver.ReviveStoppedSession(dt.ctx, dt.sessionID, dt.by, dt.instruction)
		if rerr != nil {
			return ErrorResult(fmt.Sprintf("delegate: respond: resume session %s: %v", dt.sessionID, rerr)).WithError(rerr)
		}
		if !revived {
			return ErrorResult(fmt.Sprintf("delegate: respond: session %s could not be resumed", dt.sessionID))
		}
		return dt.acknowledgedRespond("Answer delivered; session resumed.")
	}

	// TOCTOU race guard: the terminal check evaluated INSIDE the Mutate
	// closure, under the SAME lock the terminal-transition writer uses —
	// the same closure executeSteer uses (see its full rationale there).
	// No field is mutated on the non-error path; the unchanged persist is
	// harmless because the record is not terminal (the stopped/terminal
	// case above never reaches this closure).
	if merr := dt.t.lifecycle.Mutate(dt.sessionID, func(cur *session.LifecycleRecord) error {
		if cur == nil {
			return session.ErrLifecycleNotFound
		}
		if cur.Terminal() {
			return fmt.Errorf("session %s is terminal (%s) and cannot be responded to", dt.sessionID, cur.State)
		}
		dt.rec = cur
		return nil
	}); merr != nil {
		return ErrorResult(fmt.Sprintf("delegate: respond: %v", merr))
	}

	if dt.t.steering == nil {
		return ErrorResult("delegate: no steering sink configured")
	}
	_, serr, postFinish := enqueueSteeringWithStatus(dt.t.steering, dt.sessionID, dt.rec.AgentID,
		providers.Message{Role: "user", Content: dt.text}, dt.correlationID)
	if serr != nil {
		return ErrorResult(fmt.Sprintf("delegate: respond: %v", serr)).WithError(serr)
	}
	if postFinish {
		// The answer landed in the closing hand-off's transition buffer: the
		// child is finishing and will consume it on the revival that buffer
		// drives (Correction C1: a done recipient starts its next round).
		return dt.acknowledgedRespond("Answer delivered; the child is finishing and will see it next.")
	}
	return dt.acknowledgedRespond("Answer delivered into the child's steering queue; it will apply at the child's next tool boundary.")
}

// acknowledgedRespond renders the DelegateRespondResponse wire ack.
func (dt *delegateToolExecuteRespond) acknowledgedRespond(note string) *ToolResult {
	resp := generated.DelegateRespondResponse{Acknowledged: true}
	payload, merr := json.Marshal(resp)
	if merr != nil {
		return NewToolResult(note)
	}
	return NewToolResult(string(payload))
}

// respondAnswerInstruction frames the answer text with the correlation id
// it answers, so the child can tie the instruction to its own open message.
func respondAnswerInstruction(correlationID, text string) string {
	return fmt.Sprintf("Answer to your question (correlation_id=%s): %s", correlationID, text)
}
