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

// applyOutboundChangeForTool runs applyOutboundChange for the tool and
// translates its errors: *outboundRejectionError → *ChannelOutboundRefusalError
// (the writer's code and safe text, per-target detail kept);
// errOutboundNotConfigured → ErrChannelOutboundNotConfigured; any other error
// is returned unchanged (a save failure before the file write).
func (a *restAPI) applyOutboundChangeForTool(ctx context.Context, change systools.ChannelOutboundChange) (systools.ChannelOutboundSaved, error) {
	resp, err := a.applyOutboundChange(ctx, change.ID, gen.ChannelOutboundUpdateRequestPolicy(change.Policy),
		change.Targets, change.ExpectedRevision, change.Actor)
	a.auditOutboundToolOutcome(ctx, change, resp, err)
	var rejection *outboundRejectionError
	switch {
	case err == nil:
		saved := systools.ChannelOutboundSaved{
			Policy:   string(resp.Outbound.Policy),
			Targets:  append([]string{}, resp.Outbound.Targets...),
			Revision: resp.Revision,
		}
		if resp.Warning != nil {
			saved.Warning = *resp.Warning
		}
		return saved, nil
	case errors.As(err, &rejection):
		refusal := &systools.ChannelOutboundRefusalError{Reason: string(rejection.Body.Code), Message: rejection.Body.Error}
		if rejection.Body.TargetErrors != nil {
			refusal.TargetErrors = *rejection.Body.TargetErrors
		}
		if rejection.Body.OmittedCount != nil {
			refusal.OmittedCount = *rejection.Body.OmittedCount
		}
		return systools.ChannelOutboundSaved{}, refusal
	case errors.Is(err, errOutboundNotConfigured):
		return systools.ChannelOutboundSaved{}, systools.ErrChannelOutboundNotConfigured
	default:
		return systools.ChannelOutboundSaved{}, err
	}
}

// auditOutboundToolOutcome writes the writer outcome of one agent
// set_channel_outbound call (channels security S3): saved with the resulting
// revision, refused with the writer's rejection code, or failed. It names the
// approval the call ran under (from the approval registry's approved-call
// index), its tool call and session, so approval, successful save and
// refusal after approval stay distinguishable and correlated. Targets are
// never recorded — only their count on a save.
func (a *restAPI) auditOutboundToolOutcome(ctx context.Context, change systools.ChannelOutboundChange, resp gen.ChannelOutboundWriteResponse, err error) {
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
	case errors.As(err, &rejection):
		w.Outcome, w.Reason = "refused", string(rejection.Body.Code)
	case errors.Is(err, errOutboundNotConfigured):
		w.Outcome, w.Reason = "refused", "not_configured"
	default:
		w.Outcome, w.Reason = "failed", err.Error()
	}
	audit.EmitChannelOutboundWrite(context.WithoutCancel(ctx), logger, w)
}
