package gateway

// The set_channel_outbound bridge (channels-spec-r8 FR-020, FR-030): the
// tool calls the one outbound writer, applyOutboundChange, and receives its
// refusals in the sysagent tool package's neutral types.

import (
	"context"
	"errors"

	"github.com/elicify-ai/omnipus/pkg/tools"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// applyOutboundChangeForTool runs the one writer for the tool and translates
// its errors (outboundToolError). A call running under its approval card's
// pin passes the Discord targets the card's preview confirmed, so with the
// revision unchanged there is no second look-up (founder Q1 B).
func (a *restAPI) applyOutboundChangeForTool(ctx context.Context, change systools.ChannelOutboundChange) (systools.ChannelOutboundSaved, error) {
	var preVerified []string
	if change.Pin != nil {
		preVerified = change.Pin.VerifiedTargets
	}
	resp, verified, err := a.applyOutboundChangeVerified(ctx, change.ID, gen.ChannelOutboundUpdateRequestPolicy(change.Policy),
		change.Targets, change.ExpectedRevision, change.Actor, preVerified)
	a.auditOutboundToolOutcome(ctx, change, resp, verified, err)
	if err != nil {
		return systools.ChannelOutboundSaved{}, outboundToolError(err)
	}
	saved := systools.ChannelOutboundSaved{
		Policy:   string(resp.Outbound.Policy),
		Targets:  append([]string{}, resp.Outbound.Targets...),
		Revision: resp.Revision,
	}
	if resp.Warning != nil {
		saved.Warning = *resp.Warning
	}
	return saved, nil
}

// auditOutboundToolOutcome writes the writer outcome of one agent
// set_channel_outbound call (channels security S3): saved with the resulting
// revision, refused with the writer's rejection code, or failed. It names the
// approval the call ran under (from the approval registry's approved-call
// index), its tool call and session, so approval, successful save and
// refusal after approval stay distinguishable and correlated. Targets are
// never recorded — only their count on a save and, per saved target in
// saved order, whether a look-up confirmed it (Q2-B amendment A1).
func (a *restAPI) auditOutboundToolOutcome(ctx context.Context, change systools.ChannelOutboundChange, resp gen.ChannelOutboundWriteResponse, verified []string, err error) {
	logger := a.agentLoop.AuditLogger()
	if logger == nil {
		return
	}
	w := audit.ChannelOutboundWrite{
		ToolCallID: tools.ToolCallID(ctx), SessionID: tools.ToolTranscriptSessionID(ctx),
		AgentID: change.Actor, TurnID: tools.ToolTurnID(ctx), ChannelID: change.ID,
	}
	if a.approvalReg != nil {
		if call, ok := a.approvalReg.approvedCalls.take(w.SessionID, w.ToolCallID); ok {
			w.ApprovalID = call.approvalID
		}
	}
	var rejection *outboundRejectionError
	switch {
	case err == nil:
		w.Outcome, w.Policy, w.TargetCount, w.Revision = "saved", string(resp.Outbound.Policy), len(resp.Outbound.Targets), resp.Revision
		w.TargetVerifications = outboundTargetVerifications(resp.Outbound.Targets, verified)
	case errors.As(err, &rejection):
		w.Outcome, w.Reason = "refused", string(rejection.Body.Code)
	case errors.Is(err, errOutboundNotConfigured):
		w.Outcome, w.Reason = "refused", "not_configured"
	default:
		w.Outcome, w.Reason = "failed", err.Error()
	}
	audit.EmitChannelOutboundWrite(context.WithoutCancel(ctx), logger, w)
}

// outboundTargetVerifications is one result per saved target, in saved
// order: verified_before_approval for a target a look-up confirmed,
// not_required for one that needed none (already stored, or not on
// Discord). Always a list, empty for an empty saved list.
func outboundTargetVerifications(saved, verified []string) []string {
	confirmed := make(map[string]bool, len(verified))
	for _, t := range verified {
		confirmed[t] = true
	}
	out := make([]string, 0, len(saved))
	for _, t := range saved {
		if confirmed[t] {
			out = append(out, audit.TargetVerifiedBeforeApproval)
		} else {
			out = append(out, audit.TargetVerificationNotRequired)
		}
	}
	return out
}
