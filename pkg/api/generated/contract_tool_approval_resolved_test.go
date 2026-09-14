// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package generated

import "testing"

// ── ToolApprovalResolvedFrame ────────────────────────────────────────────────
// Traces to: contracts/components/schemas/ToolApprovalResolvedFrame.yaml
// The frame pkg/gateway/ws_tool_approval.go::broadcastToolApprovalResolved
// emits for every pending→terminal approval transition.

func TestContract_ToolApprovalResolvedFrame_Populated(t *testing.T) {
	sid := "session_01M2F6KYY245E622E6AWGN355S"
	mustPassComponent(t, "ToolApprovalResolvedFrame", ToolApprovalResolvedFrame{
		Type:       string(WsFrameTypeToolApprovalResolved),
		ApprovalId: "167596d2-8123-4616-b248-41daca09eb05",
		State:      "denied_cancel",
		SessionId:  &sid,
	})
}

func TestContract_ToolApprovalResolvedFrame_NoSessionID(t *testing.T) {
	// session_id is optional: an approval recorded without an acting session
	// id still resolves and still broadcasts.
	mustPassComponent(t, "ToolApprovalResolvedFrame", ToolApprovalResolvedFrame{
		Type:       string(WsFrameTypeToolApprovalResolved),
		ApprovalId: "167596d2-8123-4616-b248-41daca09eb05",
		State:      "denied_timeout",
	})
}

func TestContract_ToolApprovalResolvedFrame_RejectsUnknownState(t *testing.T) {
	mustFailComponent(t, "ToolApprovalResolvedFrame", ToolApprovalResolvedFrame{
		Type:       string(WsFrameTypeToolApprovalResolved),
		ApprovalId: "167596d2-8123-4616-b248-41daca09eb05",
		State:      "pending",
	}, "a resolution frame can never carry the pending state")
}

func TestContract_ToolApprovalResolvedFrame_ZeroValueRejected(t *testing.T) {
	mustFailComponent(t, "ToolApprovalResolvedFrame", ToolApprovalResolvedFrame{},
		"empty type/approval_id/state must not validate")
}
