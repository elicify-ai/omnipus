// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// external_run_delivery.go — FR-043's production steering delivery for an
// external-CLI (3P) child.
//
// The intended delivery for a live external CLI session is interrupt + native-
// conversation resume, not a mid-turn injection (claude -p / codex exec /
// opencode run have no mid-run stdin channel). This method lands the
// instruction S in the child's steering queue AND interrupts the child's live
// CLI run; the run ends, the post-turn drain
// (steer_turn_drain.go::continueSteeredTurn) consumes S and re-enters the
// external-CLI body with ExternalCLIResume set, which Resumes the SAME native
// conversation (external_dispatch.go + external_run_session.go) and delivers S
// to it.
//
// With no live conversation the instruction is NOT queued (a queue with no live
// consumer would strand it): the delivery refuses visibly, so the tool layer
// reports the refusal to the caller rather than a silent success (BDD-05.6).
//
// Binding (N2). The whole check-run → enqueue → cancel sequence runs while
// holding the holder's own lock, so a run can neither end nor be superseded
// between the liveness check and the cancel: the cancel func fired is the
// SELECTED run's own (sess.cancelRun), never "whatever turn the session id
// currently resolves to". A caller that pre-read liveness may pass its expected
// claim; a delivery whose expected claim no longer matches the in-flight run is
// refused as superseded (BDD-05.6 "no stale delivery").
//
// Task runs (FR-032). A task-origin external run is steered the same way: the
// delivery marks the holder (steerInterrupt) and interrupts the CLI run, and the
// task loop (processTaskDirectExternalCLI) — not the post-turn drain — consumes
// the queued instruction and resumes the SAME native conversation inside the one
// task turn, so the claim, attempts and result rules are untouched. A Stop does
// not set the mark and supersedes the queue, so it still ends the run.
//
// Starting CLI (#1899, founder decision 2026-11-18). Until the CLI has
// announced its own native conversation id (runner.CanResumeAfterInterrupt), an
// interrupt would kill the original work and leave nothing to resume. The
// instruction is then queued WITHOUT interrupting: the run continues to its
// natural end, and the same consumer (post-turn drain, or the task loop through
// the steerInterrupt mark) resumes the conversation the run announced and
// delivers the instruction to it.
//
// Two intakes (#1894). An agent's delegate(steer) records the D4 steer receipt
// (EnqueueSteeringMessageWithStatus) and its exact-text instruction row; a
// person's message was already saved by the gateway, so it is queued through
// the ordinary inbound intake with that saved row id and no receipt row.
package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// DeliverExternalCLIInstruction delivers a live steering instruction S to an
// external-CLI child session by interrupt + native-conversation resume
// (FR-043). It returns the resolved correlation id of the queued instruction on
// success, or a visible error when there is no live conversation to deliver to
// (or the selected execution was superseded).
//
// It lives on *AgentLoop (the production delegate steering sink embeds it) so
// pkg/tools can reach it through an optional-capability assertion — a test fake
// that cannot deliver simply does not implement the capability, and the tool's
// existing not_steerable refusal stays its fallback.
func (al *AgentLoop) DeliverExternalCLIInstruction(
	ctx context.Context,
	sessionID, agentID string,
	msg providers.Message,
	correlationID string,
) (string, error) {
	return al.deliverExternalCLIInstruction(ctx, sessionID, agentID, msg, correlationID, executionClaim{})
}

// DeliverExternalCLIInstructionReporting is DeliverExternalCLIInstruction that
// also reports how the instruction reached the run (A1): interrupted is true
// when the live run was interrupted so the same conversation continues with
// the instruction now, and false when the instruction was queued without an
// interrupt (the tool had not announced its conversation yet, or the run had
// already ended) and is delivered to the same conversation when the run ends.
func (al *AgentLoop) DeliverExternalCLIInstructionReporting(
	_ context.Context,
	sessionID, agentID string,
	msg providers.Message,
	correlationID string,
) (resolved string, interrupted bool, err error) {
	return al.deliverExternalCLIInput(sessionID, executionClaim{}, al.delegateSteerEnqueue(sessionID, agentID, msg, correlationID))
}

// deliverExternalCLIInstruction is DeliverExternalCLIInstruction's body with an
// optional expected claim. A non-zero expected claim binds the delivery to a
// specific selected execution: if the in-flight run's claim no longer matches,
// the delivery is refused as superseded (N2). The tool-facing method passes the
// zero value (no pre-read expected claim); the lock alone closes the race for
// it, and this seam makes supersession deterministically testable.
func (al *AgentLoop) deliverExternalCLIInstruction(
	_ context.Context,
	sessionID, agentID string,
	msg providers.Message,
	correlationID string,
	expected executionClaim,
) (string, error) {
	resolved, _, err := al.deliverExternalCLIInput(sessionID, expected, al.delegateSteerEnqueue(sessionID, agentID, msg, correlationID))
	return resolved, err
}

// delegateSteerEnqueue queues a delegate steer as a real accepted steer: the
// D4 steer receipt (enqueueDelegateSteer) makes a later supersede/abandon
// truthful rather than a silent drop.
func (al *AgentLoop) delegateSteerEnqueue(sessionID, agentID string, msg providers.Message, correlationID string) func() (string, error) {
	return func() (string, error) {
		resolved, _, err := al.EnqueueSteeringMessageWithStatus(sessionID, agentID, msg, correlationID)
		return resolved, err
	}
}

// deliverHumanExternalCLIInput delivers a person's message to the live
// external-CLI run of msg.SessionID (a steered helper or an external task run)
// with the same check-run, binding and interrupt rules as
// deliverExternalCLIInstruction. The gateway has already saved the person's
// row, so the message is queued through the ordinary inbound intake with that
// row id (msg.TranscriptEntryID) and the person's origin: no delegate steer
// receipt, no second transcript row (#1894).
func (al *AgentLoop) deliverHumanExternalCLIInput(msg bus.InboundMessage, agentID string) error {
	_, _, err := al.deliverExternalCLIInput(msg.SessionID, executionClaim{}, func() (string, error) {
		resolved, _, err := al.enqueueInboundSteeringMessage(msg.SessionID, agentID, providers.Message{
			Role: "user", Content: msg.Content, Media: append([]string(nil), msg.Media...),
		}, msg.TranscriptEntryID, inboundInputOrigin(msg))
		return resolved, err
	})
	return err
}

// deliverExternalCLIInput is the shared delivery body: check the live run under
// the holder's lock, queue the input through enqueue, then interrupt the
// selected run when its CLI conversation can be resumed (#1899). interrupted
// reports whether that interrupt fired; false means the input waits in the
// queue for the run to end (A1).
func (al *AgentLoop) deliverExternalCLIInput(
	sessionID string,
	expected executionClaim,
	enqueue func() (string, error),
) (resolved string, interrupted bool, err error) {
	if sessionID == "" {
		return "", false, fmt.Errorf("external-cli steer: empty session id")
	}
	// A task-origin external run is steerable too (FR-032): the task loop
	// (task_executor_run.go::processTaskDirectExternalCLI) consumes the queued
	// instruction after the interrupt and resumes the same native conversation.
	// Only a run the task loop can resume is marked, so the mark can never
	// outlive a consumer. A lifecycle record that cannot be READ is not "not a
	// task run": the delivery refuses before anything is queued or cancelled,
	// rather than interrupting a run whose resume path it cannot determine.
	taskOrigin, originErr := al.externalRunIsTaskOrigin(sessionID)
	if originErr != nil {
		return "", false, &authoredRefusalError{text: fmt.Sprintf(
			"external-cli steer: could not read the lifecycle record of session %s; the instruction was not delivered",
			sessionID)}
	}

	sess := al.externalRunSessionIfPresent(sessionID)
	if sess == nil {
		return "", false, al.noLiveExternalConversationErr(sessionID)
	}
	// Check-run → capture-claim under the holder's lock (N2): beginExternalRun
	// and finishExternalRun take this lock too, so the run cannot end or be
	// superseded between the check and the capture. The lock is released BEFORE
	// the durable enqueue: the enqueue reads the lifecycle record and appends the
	// steer receipt, and the lifecycle lock is never taken under the holder lock
	// (beginExternalRun's own ordering rule); a slow disk must not stall a turn
	// end or a Stop.
	sess.mu.Lock()
	if !sess.running || sess.driver == nil {
		sess.mu.Unlock()
		return "", false, al.noLiveExternalConversationErr(sessionID)
	}
	if expected.Generation != 0 &&
		(expected.SessionID != sess.claim.SessionID || expected.Generation != sess.claim.Generation) {
		sess.mu.Unlock()
		return "", false, &authoredRefusalError{text: fmt.Sprintf(
			"external-cli steer: the selected execution for session %s was superseded; the instruction was not delivered",
			sessionID)}
	}
	selected := sess.claim
	sess.mu.Unlock()

	// Queue FIRST, so the instruction is present before the run ends: the
	// post-turn drain sees it and re-enters the external-CLI body with it.
	resolved, err = enqueue()
	if err != nil {
		return "", false, fmt.Errorf("external-cli steer: enqueue instruction for %s: %w", sessionID, err)
	}

	// Interrupt THIS run via its own cancel func (N2) - never
	// al.Interrupt(sessionID, ...), which resolves whatever turn is current for
	// the session id and could cancel a newer run. Re-take the lock and fire the
	// cancel only if the SELECTED run is still the one in flight; if it ended or
	// was superseded while the instruction was being queued, the queued
	// instruction is left for the post-turn drain / next entry to consume (the
	// same outcome as cancelling an already-ended run, which was always a no-op).
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.running && sess.claim == selected {
		// The task loop resumes only a run marked here, interrupted or not, so
		// the queued instruction always has its consumer.
		if taskOrigin {
			sess.steerInterrupt = true
		}
		// #1899: a CLI that has not announced its native conversation id yet
		// cannot be resumed after an interrupt. Leave it running; the queued
		// instruction is delivered to the conversation it announces, after the
		// run ends. Readiness is read here, at the decision point, so an id
		// announced while the instruction was being queued is honoured.
		if sess.cancelRun != nil && sess.driver != nil && runner.CanResumeAfterInterrupt(sess.driver) {
			sess.cancelRun()
			interrupted = true
		}
	}
	return resolved, interrupted, nil
}

// externalRunIsTaskOrigin reports whether sessionID's durable lifecycle record
// says it is a task run. A missing record or an unwired store means "not a task
// run"; any other read failure is returned, never swallowed.
func (al *AgentLoop) externalRunIsTaskOrigin(sessionID string) (bool, error) {
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return false, nil
	}
	rec, err := lifecycle.Load(sessionID)
	if errors.Is(err, session.ErrLifecycleNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return rec != nil && rec.Origin != nil && rec.Origin.Kind == session.OriginKindTask, nil
}

// noLiveExternalConversationErr is the shared BDD-05.6 refusal for "there is no
// live external CLI conversation to deliver to".
func (al *AgentLoop) noLiveExternalConversationErr(sessionID string) error {
	return &noLiveConversationError{authoredRefusalError{text: fmt.Sprintf(
		"external-cli steer: session %s has no live external CLI conversation to deliver to (FR-043); the instruction was not delivered",
		sessionID)}}
}

// loadLifecycleRecord loads sessionID's durable lifecycle record, or (nil, err)
// when the store is unwired or the record is absent — the delivery's N4 check
// treats "no record" as "not a task run" (an un-classified session is handled
// by the liveness check below).
func (al *AgentLoop) loadLifecycleRecord(sessionID string) (*session.LifecycleRecord, error) {
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return nil, fmt.Errorf("external-cli steer: no lifecycle store wired")
	}
	return lifecycle.Load(sessionID)
}
