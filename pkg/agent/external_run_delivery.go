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
package agent

import (
	"context"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// DeliverExternalCLIInstruction delivers a live steering instruction S to an
// external-CLI child session by interrupt + native-conversation resume
// (FR-043). It returns the resolved correlation id of the queued instruction on
// success, or a visible error when there is no live conversation to deliver to.
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
	if sessionID == "" {
		return "", fmt.Errorf("external-cli steer: empty session id")
	}
	if !al.externalRunLive(sessionID) {
		return "", fmt.Errorf(
			"external-cli steer: session %s has no live external CLI conversation to deliver to (FR-043); the instruction was not delivered",
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
	// Interrupt the live CLI run (ScopeSelfOnly = this child's own turn, never
	// its subtree). Interrupt fires the turn's providerCancel, which for an
	// external-CLI turn is the run-context cancel that terminates the CLI
	// process — the "interrupt" half of FR-043. A no-op (no resolved target) is
	// not an error here: the run may have ended naturally between the liveness
	// check above and this call, in which case the drain still consumes the
	// queued instruction.
	if _, ierr := al.Interrupt(sessionID, ScopeSelfOnly, "external CLI steer: interrupt to deliver follow-up"); ierr != nil {
		return resolved, fmt.Errorf("external-cli steer: interrupt live run for %s: %w", sessionID, ierr)
	}
	return resolved, nil
}
