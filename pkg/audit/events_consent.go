// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// events_consent.go — call-correlated consent records (channels security S3).
//
// The terminal ask events of the tool-registry spec (FR-047, FR-054, FR-074)
// name the approval but not the tool call. These emitters write the same
// event names and fields plus tool_call_id, so a terminal decision binds to
// exactly the call it answered, and add the writer outcome of an approved
// set_channel_outbound call.

package audit

import "context"

// AskDecision is one terminal approval decision, bound to its call.
type AskDecision struct {
	ApprovalID     string
	ToolCallID     string
	ToolName       string
	AgentID        string
	SessionID      string
	TurnID         string
	ApproverUserID string // "" for a system decision (timeout, restart, …)
	ArgsHash       string
	LatencyMS      int64
}

func (d AskDecision) fields() map[string]any {
	return map[string]any{
		"approval_id":      d.ApprovalID,
		"tool_call_id":     d.ToolCallID,
		"approver_user_id": d.ApproverUserID,
		"tool_name":        d.ToolName,
		"agent_id":         d.AgentID,
		"session_id":       d.SessionID,
		"turn_id":          d.TurnID,
		"args_hash":        d.ArgsHash,
		"latency_ms":       d.LatencyMS,
	}
}

// EmitToolPolicyAskGrantedForCall writes tool.policy.ask.granted for d.
// once is true for an approve-once decision (no standing grant recorded).
func EmitToolPolicyAskGrantedForCall(ctx context.Context, logger *Logger, d AskDecision, once bool) {
	fields := d.fields()
	fields["grant_recorded"] = !once
	Emit(ctx, logger, EventToolPolicyAskGranted, SeverityInfo, fields)
}

// EmitToolPolicyAskDeniedForCall writes tool.policy.ask.denied for d. An
// unknown reason is refused exactly like EmitToolPolicyAskDenied does.
func EmitToolPolicyAskDeniedForCall(ctx context.Context, logger *Logger, d AskDecision, reason AskDenyReason) {
	if !IsValidAskDenyReason(reason) {
		EmitToolPolicyAskDenied(ctx, logger, d.ApprovalID, d.ApproverUserID, d.ToolName, d.AgentID,
			d.SessionID, d.TurnID, reason, d.ArgsHash, nil)
		return
	}
	fields := d.fields()
	fields["reason"] = string(reason)
	Emit(ctx, logger, EventToolPolicyAskDenied, SeverityInfo, fields)
}

// ChannelOutboundWrite is the writer outcome of one set_channel_outbound
// call. Outcome is "saved", "refused" (a writer rejection after approval,
// before anything was written), "refused_before_approval" (Channels Q2-B: a
// refusal before any approval card; ApprovalID is empty) or "failed" (any
// other error, with nothing reported as saved). Revision is the channel's
// resulting FR-088 revision when saved; Reason the refusal code otherwise.
// TargetVerifications (Q2-B amendment A1, saved only) holds one result per
// saved target, in saved order: TargetVerifiedBeforeApproval or
// TargetVerificationNotRequired. No target value is ever recorded here.
type ChannelOutboundWrite struct {
	ApprovalID  string
	ToolCallID  string
	SessionID   string
	AgentID     string
	TurnID      string
	ChannelID   string
	Outcome     string
	Policy      string
	TargetCount int
	Revision    string
	Reason      string
	// TargetVerifications is recorded on a saved outcome only, always as a
	// list (empty when the saved list is empty).
	TargetVerifications []string
}

// Results of ChannelOutboundWrite.TargetVerifications (Q2-B amendment A1).
const (
	TargetVerifiedBeforeApproval  = "verified_before_approval"
	TargetVerificationNotRequired = "not_required"
)

// EmitChannelOutboundWrite writes channel.outbound.write for w.
func EmitChannelOutboundWrite(ctx context.Context, logger *Logger, w ChannelOutboundWrite) {
	fields := map[string]any{
		"approval_id":  w.ApprovalID,
		"tool_call_id": w.ToolCallID,
		"session_id":   w.SessionID,
		"agent_id":     w.AgentID,
		"turn_id":      w.TurnID,
		"channel_id":   w.ChannelID,
		"outcome":      w.Outcome,
	}
	if w.Outcome == "saved" {
		fields["policy"] = w.Policy
		fields["target_count"] = w.TargetCount
		fields["revision"] = w.Revision
		fields["target_verifications"] = append([]string{}, w.TargetVerifications...)
	} else {
		fields["reason"] = w.Reason
	}
	sev := SeverityInfo
	if w.Outcome == "failed" {
		sev = SeverityWarn
	}
	Emit(ctx, logger, EventChannelOutboundWrite, sev, fields)
}
