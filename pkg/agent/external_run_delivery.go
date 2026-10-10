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
// Refusal (N4). A task-origin external run has no consumer that resumes the
// native conversation after an interrupt — the queued instruction would be
// orphaned and the task run would simply end cancelled. Such a delivery is
// refused BEFORE queueing or interrupting, with an actionable message. (The
// task-side consumer that would resume it is the U10b unit; until it lands this
// refusal is the honest outcome.)
package agent

import (
	"context"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// DeliverExternalCLIInstruction delivers a live steering instruction S to an
// external-CLI child session by interrupt + native-conversation resume
// (FR-043). It returns the resolved correlation id of the queued instruction on
// success, or a visible error when there is no live conversation to deliver to
// (or the target is a task run, or the selected execution was superseded).
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

// deliverExternalCLIInstruction is DeliverExternalCLIInstruction's body with an
// optional expected claim. A non-zero expected claim binds the delivery to a
// specific selected execution: if the in-flight run's claim no longer matches,
// the delivery is refused as superseded (N2). The tool-facing method passes the
// zero value (no pre-read expected claim); the lock alone closes the race for
// it, and this seam makes supersession deterministically testable.
func (al *AgentLoop) deliverExternalCLIInstruction(
	ctx context.Context,
	sessionID, agentID string,
	msg providers.Message,
	correlationID string,
	expected executionClaim,
) (string, error) {
	if sessionID == "" {
		return "", fmt.Errorf("external-cli steer: empty session id")
	}
	// N4: refuse a task-origin external run BEFORE queueing or interrupting.
	// Task execution does not consume the steering queue's continuation, so a
	// queued instruction would be orphaned and the task run would end cancelled.
	if rec, rerr := al.loadLifecycleRecord(sessionID); rerr == nil && rec != nil &&
		rec.Origin != nil && rec.Origin.Kind == session.OriginKindTask {
		return "", fmt.Errorf(
			"external-cli steer: session %s is running a task; a live instruction cannot be delivered to an external-CLI task run — Stop and Rerun the task, or wait for it to finish",
			sessionID)
	}

	sess := al.externalRunSessionIfPresent(sessionID)
	if sess == nil {
		return "", al.noLiveExternalConversationErr(sessionID)
	}
	// Hold the holder's lock across check-run → capture-claim → enqueue →
	// cancel-run (N2). beginExternalRun and finishExternalRun also take this
	// lock, so no run can end or start between the check and the cancel.
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if !sess.running || sess.driver == nil {
		return "", al.noLiveExternalConversationErr(sessionID)
	}
	if expected.Generation != 0 &&
		(expected.SessionID != sess.claim.SessionID || expected.Generation != sess.claim.Generation) {
		return "", fmt.Errorf(
			"external-cli steer: the selected execution for session %s was superseded; the instruction was not delivered",
			sessionID)
	}
	// Queue FIRST, so the instruction is present before the run ends: the
	// post-turn drain sees it and re-enters the external-CLI body with it. The
	// D4 steer receipt (enqueueDelegateSteer) records it as a real accepted
	// steer, so a later supersede/abandon is truthful rather than a silent drop.
	resolved, _, err := al.EnqueueSteeringMessageWithStatus(sessionID, agentID, msg, correlationID)
	if err != nil {
		return "", fmt.Errorf("external-cli steer: enqueue instruction for %s: %w", sessionID, err)
	}
	// Interrupt THIS run via its own cancel func (N2) — never
	// al.Interrupt(sessionID, …), which resolves whatever turn is current for
	// the session id and could cancel a newer run. A cancel of an already-ended
	// run is a harmless no-op; the drain still consumes the queued instruction.
	if sess.cancelRun != nil {
		sess.cancelRun()
	}
	return resolved, nil
}

// noLiveExternalConversationErr is the shared BDD-05.6 refusal for "there is no
// live external CLI conversation to deliver to".
func (al *AgentLoop) noLiveExternalConversationErr(sessionID string) error {
	return fmt.Errorf(
		"external-cli steer: session %s has no live external CLI conversation to deliver to (FR-043); the instruction was not delivered",
		sessionID)
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
