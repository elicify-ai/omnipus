// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// approvals_consent.go: the consent audit trail of the approval path
// (channels security S3, tool-registry-redesign-spec FR-047/054/074/080).
//
// Every admitted approval writes tool.policy.ask.requested with its approval,
// call, session, turn and agent identity and the SHA-256 canonical-JSON digest
// of the exact arguments shown on the card; every terminal decision writes
// tool.policy.ask.granted or tool.policy.ask.denied with the same identity,
// the same digest and the deciding person (empty for a system decision). An
// approved always-ask call is also indexed by (session, tool call) so the
// set_channel_outbound writer can record its outcome against the approval.

package gateway

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// approvedCallTTL bounds how long an approved always-ask call waits for its
// writer outcome to claim it. A claim normally follows within seconds; the
// bound only stops refused-before-the-writer calls from accumulating.
const approvedCallTTL = time.Hour

type approvedCallKey struct{ sessionID, toolCallID string }

type approvedCall struct {
	approvalID string
	agentID    string
	turnID     string
	at         time.Time
}

// approvedCallIndex maps an approved always-ask call to its approval. The
// zero value is ready to use.
type approvedCallIndex struct {
	mu    sync.Mutex
	calls map[approvedCallKey]approvedCall
}

// record indexes e when it is an always-ask call (only those have a writer
// outcome record), pruning claims older than approvedCallTTL.
func (x *approvedCallIndex) record(e *approvalEntry) {
	if e == nil || !tools.IsAlwaysAsk(e.ToolName) || e.ToolCallID == "" {
		return
	}
	now := time.Now()
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.calls == nil {
		x.calls = make(map[approvedCallKey]approvedCall)
	}
	for key, call := range x.calls {
		if now.Sub(call.at) > approvedCallTTL {
			delete(x.calls, key)
		}
	}
	x.calls[approvedCallKey{e.SessionID, e.ToolCallID}] = approvedCall{
		approvalID: e.ApprovalID, agentID: e.AgentID, turnID: e.TurnID, at: now,
	}
}

// take returns and forgets the approval of (sessionID, toolCallID).
func (x *approvedCallIndex) take(sessionID, toolCallID string) (approvedCall, bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	key := approvedCallKey{sessionID, toolCallID}
	call, ok := x.calls[key]
	if ok {
		delete(x.calls, key)
	}
	return call, ok
}

// consentDecision builds the call-bound audit identity of e.
func consentDecision(e *approvalEntry, approver string, latency time.Duration) audit.AskDecision {
	return audit.AskDecision{
		ApprovalID: e.ApprovalID, ToolCallID: e.ToolCallID, ToolName: e.ToolName,
		AgentID: e.AgentID, SessionID: e.SessionID, TurnID: e.TurnID,
		ApproverUserID: approver, ArgsHash: consentArgsHash(e),
		LatencyMS: latency.Milliseconds(),
	}
}

// consentArgsHash is the FR-080 digest of the arguments the card showed. A
// hashing failure is logged loudly and recorded as an empty digest — the
// decision itself is still audited.
func consentArgsHash(e *approvalEntry) string {
	hash, err := audit.ArgsHash(e.Args)
	if err != nil {
		slog.Error("approval audit: could not hash the approved arguments; args_hash is empty",
			"approval_id", e.ApprovalID, "tool", e.ToolName, "error", err)
	}
	return hash
}

// auditAskRequested writes tool.policy.ask.requested for an admitted entry.
func auditAskRequested(ctx context.Context, logger *audit.Logger, e *approvalEntry) {
	if logger == nil {
		return
	}
	preview := e.Args
	if e.ToolName == systools.ChannelOutboundToolName {
		// Q2-B A2.1: the card shows the person the private lists; the
		// audit preview carries only their readable non-target facts.
		preview = systools.ChannelOutboundAskAuditArgs(e.Args)
	}
	audit.EmitToolPolicyAskRequestedWithPreview(ctx, logger, e.ApprovalID, e.ToolCallID, e.ToolName,
		e.AgentID, e.SessionID, e.TurnID, e.Args, preview)
}

// auditAskDecision writes the terminal record for outcome. An approval is
// "granted"; every refusal is "denied" with its FR-047 reason ("session
// canceled", the Stop path, is recorded as cancel).
func auditAskDecision(ctx context.Context, logger *audit.Logger, e *approvalEntry, outcome ApprovalOutcome, latency time.Duration) {
	if logger == nil {
		return
	}
	d := consentDecision(e, outcome.ApproverUserID, latency)
	if outcome.Approved {
		audit.EmitToolPolicyAskGrantedForCall(ctx, logger, d, !outcome.RecordGrant)
		return
	}
	reason := audit.AskDenyReason(outcome.Reason)
	if outcome.Reason == denialReasonSessionCanceled {
		reason = audit.AskDenyReasonCancel
	}
	audit.EmitToolPolicyAskDeniedForCall(ctx, logger, d, reason)
}
