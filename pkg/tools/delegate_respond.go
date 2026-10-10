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
// resumes its own native CLI conversation or refuses visibly (FR-043).

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

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
	sessionID     string
	correlationID string
	text          string
	rec           *session.LifecycleRecord
	by            steer.Principal
	instruction   string
	answers       delegateCorrelationStore
	reserved      bool
}

func (t *DelegateTool) executeRespond(ctx context.Context, args map[string]any, cb AsyncCallback) *ToolResult {
	dt := &delegateToolExecuteRespond{t: t, ctx: ctx, args: args}
	// The answer slot is reserved atomically inside validateAndLoad; every exit
	// that did not deliver must give it back so the question stays open for a retry.
	delivered := false
	defer func() {
		if dt.reserved && !delivered {
			dt.answers.ReleaseAnswer(dt.rec.SteeringSessionID(), dt.sessionID, dt.correlationID)
		}
	}()

	if r0, stop := dt.validateAndLoad(); stop {
		return r0
	}

	var result *ToolResult
	if r0, stop := dt.dispatchThirdParty(); stop {
		result = r0
	} else {
		result = dt.deliverNative()
	}
	if result.IsError {
		return result
	}
	// Delivered: from here the slot is never released, only recorded.
	delivered = true
	if err := dt.answers.RecordAnswer(dt.rec.SteeringSessionID(), dt.sessionID, dt.correlationID); err != nil {
		slog.Error("delegate: respond: the answer was delivered but could not be recorded as answered",
			"session_id", dt.sessionID, "correlation_id", dt.correlationID, "error", err)
		// Not an error result: the child HAS the answer, and an error would
		// invite a retry that sends it twice. The slot stays reserved in
		// memory, so a repeat respond in this process is refused too.
		return NewToolResult(result.ForLLM + fmt.Sprintf(
			"\nThe answer WAS delivered — do not send it again; use delegate(action=\"steer\", session_id=%q, text=...) for any follow-up. "+
				"It could not be recorded as answered, so after a restart this question may still show as open.",
			dt.sessionID))
	}
	return result
}

// delegateCorrelationStore is the part of the durable inbox respond needs to
// resolve a correlation_id against the child's questions and to record the
// answer (founder decision 2026-10-07, #1213: reverses ADR-20261004's
// "correlation_id is address metadata, never a check" for respond).
type delegateCorrelationStore interface {
	ReserveAnswer(ownerKey, childSessionID, correlationID string) (session.CorrelationLookup, error)
	ReleaseAnswer(ownerKey, childSessionID, correlationID string)
	RecordAnswer(ownerKey, childSessionID, correlationID string) error
}

// checkCorrelation refuses a correlation_id that is not an open question of
// this child, telling the caller how to correct the call.
func (dt *delegateToolExecuteRespond) checkCorrelation() *ToolResult {
	if dt.t.inbox == nil {
		return ErrorResult("delegate: respond: no message inbox configured to verify correlation_id")
	}
	store, ok := dt.t.inbox.(delegateCorrelationStore)
	if !ok {
		return ErrorResult("delegate: respond: the message inbox cannot verify correlation_id, so the answer was not delivered")
	}
	lookup, err := store.ReserveAnswer(dt.rec.SteeringSessionID(), dt.sessionID, dt.correlationID)
	if err != nil {
		return controlFailure("respond", "resolve correlation_id failed: the message inbox could not be read right now; retry shortly", err)
	}
	dt.answers = store
	switch lookup.State {
	case session.CorrelationOpen:
		dt.reserved = true
		return nil
	case session.CorrelationAnswering:
		return ErrorResult(fmt.Sprintf(
			"delegate: respond: already_answered: an answer to correlation_id %q of session %s is being delivered, or was just delivered. "+
				`Do not send it again; for a follow-up use delegate(action="steer", session_id=%q, text=...).`,
			dt.correlationID, dt.sessionID, dt.sessionID))
	case session.CorrelationAnswered:
		return ErrorResult(fmt.Sprintf(
			"delegate: respond: already_answered: correlation_id %q of session %s was already answered at %s. "+
				`To follow up, send an ordinary message with delegate(action="steer", session_id=%q, text=...).`,
			dt.correlationID, dt.sessionID, lookup.AnsweredAt.UTC().Format(time.RFC3339), dt.sessionID))
	default:
		open := "It has no open questions."
		if len(lookup.OpenIDs) > 0 {
			open = "Its open question correlation_ids: " + strings.Join(lookup.OpenIDs, ", ") + "."
		}
		return ErrorResult(fmt.Sprintf(
			"delegate: respond: unknown_correlation_id: %q is not an open question of session %s. %s "+
				`Answer one of those questions by its id, or send an ordinary message with delegate(action="steer", session_id=%q, text=...).`,
			dt.correlationID, dt.sessionID, open, dt.sessionID))
	}
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
		return lifecycleLoadFailure("respond", dt.sessionID, lerr), true
	}
	dt.rec = rec
	by, verr := dt.t.verifyCallerPrincipal(dt.ctx, rec)
	if verr != nil {
		return ErrorResult(fmt.Sprintf("delegate: respond: %v", verr)), true
	}
	dt.by = by
	if refusal := dt.checkCorrelation(); refusal != nil {
		return refusal, true
	}
	dt.instruction = respondAnswerInstruction(dt.correlationID, dt.text)
	if cerr := dt.t.checkSteerCaps(dt.sessionID, dt.text); cerr != nil {
		return ErrorResult(fmt.Sprintf("delegate: respond: %v", cerr)).WithError(cerr), true
	}
	return nil, false
}

// dispatchThirdParty handles a LIVE 3P (external-CLI) recipient: the answer
// reaches the SAME native CLI conversation by interrupt + resume (FR-043), the
// way executeSteer delivers a live instruction. It NEVER mints a new corrective
// session: that bypassed the creation-edge authorization and started a fresh
// CLI conversation (DEL-31). A sink that cannot deliver, or a session with no
// live conversation, refuses visibly. A stopped or finished 3P recipient is not
// handled here: it falls through to deliverNative, whose ReviveStoppedSession
// either continues the retained conversation or refuses.
func (dt *delegateToolExecuteRespond) dispatchThirdParty() (*ToolResult, bool) {
	if !dt.rec.Is3P || dt.rec.Terminal() || dt.rec.Stopped() {
		return nil, false
	}
	deliverer, ok := dt.t.steering.(steerExternalCLIDeliverer)
	if !ok {
		return ErrorResult(fmt.Sprintf(
			"delegate: respond: not_steerable: external command-line session %s cannot receive an answer from this "+
				"steering sink; start a new delegation instead", dt.sessionID)), true
	}
	if _, derr := deliverer.DeliverExternalCLIInstruction(dt.ctx, dt.sessionID, dt.rec.AgentID,
		providers.Message{Role: "user", Content: dt.text}, dt.correlationID); derr != nil {
		if text, shown := displayableCause(derr); shown {
			return ErrorResult("delegate: respond: not_steerable: " + text).WithError(derr), true
		}
		return controlFailure("respond", fmt.Sprintf("not_steerable: the answer for session %s could not be delivered right now; retry shortly", dt.sessionID), derr), true
	}
	return dt.acknowledgedRespond("Answer delivered to the external CLI conversation by interrupt + resume."), true
}

// deliverNative delivers the answer to a native child as an ordinary
// steering message. A stopped or terminal record revives through
// ReviveStoppedSession — stopped: same-generation resume; done/failed:
// next round (Correction C1's message/state table, steering-message row) —
// exactly what the delegate tool's own steer and a chat message do. A live
// child takes the ordinary steering queue: delivered into the current turn
// at its next tool boundary, never stopping it (locked decision 5).
func (dt *delegateToolExecuteRespond) deliverNative() *ToolResult {
	// A terminal record (ADR-093 D4) or a record whose stop has already
	// LANDED (state LifecycleStopped; TransitionSession cleared the fence
	// when it landed) is checked off the plain Load above, mirroring
	// executeSteer: both shapes revive through ReviveStoppedSession below,
	// which re-validates atomically under its own record lock — a landed
	// stop-state is left only through Revive, so the fact cannot become
	// stale under this Load the way a live fence can: a fence not yet
	// landed can land at any moment, and that moving fact is exactly what
	// the closure's own in-flight-fence check below re-reads under the
	// lock.
	if dt.rec.Terminal() || dt.rec.State == session.LifecycleStopped {
		reviver, ok := dt.t.steering.(steerReviver)
		if !ok {
			return ErrorResult(fmt.Sprintf("delegate: respond: session %s is stopped and cannot be resumed: no reviver configured", dt.sessionID))
		}
		revived, rerr := reviver.ReviveStoppedSession(dt.ctx, dt.sessionID, dt.by, dt.instruction)
		if rerr != nil {
			return reviveFailure("respond", dt.sessionID, rerr)
		}
		if !revived {
			return ErrorResult(fmt.Sprintf("delegate: respond: session %s could not be resumed", dt.sessionID))
		}
		return dt.acknowledgedRespond("Answer delivered; session resumed.")
	}

	// A stop fence still IN FLIGHT (stamped for the current generation
	// while the state is still running or queued) is neither queueable nor
	// revivable — an in-flight fence is not a landed stop: the dying turn
	// is still registered, so an early revive would strand the record
	// queued with nobody running it, and queueing would put the answer in a
	// queue the dying turn's unwinding may never drain. Refuse visibly —
	// never queue, never revive; the caller retries once the stop has
	// landed. (A fence for an EARLIER generation is inert history —
	// Stopped() is false for it — and takes the ordinary queueing path
	// below.)
	if dt.rec.Stop != nil && dt.rec.Stop.Generation == dt.rec.Generation {
		return ErrorResult(fmt.Sprintf("delegate: respond: session %s is stopping (a stop is in flight for its current generation); retry the respond once it has stopped", dt.sessionID))
	}

	// TOCTOU race guard: the terminal check evaluated INSIDE the Mutate
	// closure, under the SAME lock the terminal-transition writer uses —
	// the same closure executeSteer uses (see its full rationale there).
	// No field is mutated on the non-error path; the unchanged persist is
	// harmless because the record is not terminal (the stopped/terminal
	// case above never reaches this closure).
	//
	// The same race has the second shape executeSteer's closure signals out
	// (ADR-20260928, founder decision 2026-10-04): the child can LAND
	// LifecycleStopped between the plain Load and this lock-protected
	// re-read — queueing then strands the answer in a steering queue no live
	// consumer will ever drain. stoppedInRace sends the caller through the
	// SAME revive the stopped branch above runs, AFTER the store lock is
	// released (the striped lock is not reentrant; ReviveStoppedSession
	// re-validates under its own). A stop fence still IN FLIGHT is neither
	// queueable nor revivable — the old turn is still registered, so an
	// early revive would strand the record queued with nobody running it;
	// refused visibly, the caller retries once the stop has landed.
	stoppedInRace := false
	if merr := dt.t.lifecycle.Mutate(dt.sessionID, func(cur *session.LifecycleRecord) error {
		if cur == nil {
			return session.ErrLifecycleNotFound
		}
		if cur.Terminal() {
			return &followupRefusalError{fmt.Sprintf("session %s is terminal (%s) and cannot be responded to", dt.sessionID, cur.State)}
		}
		if cur.State == session.LifecycleStopped {
			stoppedInRace = true
			dt.rec = cur
			return nil
		}
		if cur.Stop != nil && cur.Stop.Generation == cur.Generation {
			return &followupRefusalError{fmt.Sprintf("session %s is stopping (a stop is in flight for its current generation); retry the respond once it has stopped", dt.sessionID)}
		}
		dt.rec = cur
		return nil
	}); merr != nil {
		var refusal *followupRefusalError
		if errors.As(merr, &refusal) {
			return ErrorResult("delegate: respond: " + refusal.text).WithError(merr)
		}
		return lifecycleLoadFailure("respond", dt.sessionID, merr)
	}

	if stoppedInRace {
		reviver, ok := dt.t.steering.(steerReviver)
		if !ok {
			return ErrorResult(fmt.Sprintf("delegate: respond: session %s is stopped and cannot be resumed: no reviver configured", dt.sessionID))
		}
		revived, rerr := reviver.ReviveStoppedSession(dt.ctx, dt.sessionID, dt.by, dt.instruction)
		if rerr != nil {
			return reviveFailure("respond", dt.sessionID, rerr)
		}
		if !revived {
			return ErrorResult(fmt.Sprintf("delegate: respond: session %s could not be resumed", dt.sessionID))
		}
		return dt.acknowledgedRespond("Answer delivered; session resumed.")
	}

	if dt.t.steering == nil {
		return ErrorResult("delegate: no steering sink configured")
	}
	_, serr, postFinish := enqueueSteeringWithStatus(dt.t.steering, dt.sessionID, dt.rec.AgentID,
		providers.Message{Role: "user", Content: dt.text}, dt.correlationID)
	if serr != nil {
		if text, shown := displayableCause(serr); shown {
			return ErrorResult("delegate: respond: " + text).WithError(serr)
		}
		return controlFailure("respond", fmt.Sprintf("the answer for session %s could not be queued right now; retry shortly", dt.sessionID), serr)
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
