// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"log/slog"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// bashApprovalSegments builds ToolApprovalRequiredFrame.segments for a bash
// approval (ADR-092 D3/D4): one entry per chained-command segment, split and
// resolved by the requesting agent's own bash tool (EvaluateCommandRules —
// splitShellSegments, shellCommandHeadDetailed and the child-PATH binary
// resolution the tool enforces with), so the dialog shows exactly the parts
// the tool will run.
//
// prefix_available / suggested_prefix come from tools.BashPrefixGrantFor, the
// same derivation HandleToolApprovals records a scope=prefix grant with, so
// the dialog offers "Allow commands starting with …" only when the server
// would really record that prefix. Every case BashPrefixGrantFor declines
// (chained command, bare program, wrapper, unresolvable head, Windows) reports
// prefix_available=false on every segment.
//
// Returns nil (segments omitted) for any other tool, for a bash call without a
// command string, and when the agent's bash tool cannot be found — the last is
// logged, because the dialog then falls back to the plain command view.
func (h *WSHandler) bashApprovalSegments(entry *approvalEntry) []generated.CommandSegmentInfo {
	if entry == nil || entry.ToolName != "bash" {
		return nil
	}
	command, _ := entry.Args["command"].(string)
	if strings.TrimSpace(command) == "" {
		return nil
	}
	execTool := h.agentBashTool(entry.AgentID)
	if execTool == nil {
		slog.Warn("ws: bash approval without a resolvable bash tool — segments omitted from tool_approval_required",
			"approval_id", entry.ApprovalID, "agent_id", entry.AgentID)
		return nil
	}
	verdict := execTool.EvaluateCommandRules(command)
	grant, prefixOK := tools.BashPrefixGrantFor(entry.Args)
	// BashPrefixGrantFor already refuses a chained command; the length check
	// states the invariant locally so a multi-segment frame can never carry
	// a prefix offer.
	prefixOK = prefixOK && len(verdict.Segments) == 1

	out := make([]generated.CommandSegmentInfo, 0, len(verdict.Segments))
	for i, seg := range verdict.Segments {
		text := strings.TrimSpace(seg.Segment)
		if text == "" {
			continue // command_text has minLength 1; an empty split part carries nothing to approve
		}
		info := generated.CommandSegmentInfo{SegmentIndex: i, CommandText: text}
		if !seg.Blind && seg.ResolvedPath != "" {
			resolved := seg.ResolvedPath
			info.ResolvedBinary = &resolved
		}
		available := prefixOK && !seg.Blind && seg.Head != ""
		info.PrefixAvailable = &available
		if available {
			prefix := seg.Head
			if grant.ArgPrefix != "" {
				prefix += " " + grant.ArgPrefix
			}
			info.SuggestedPrefix = &prefix
		}
		out = append(out, info)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// agentBashTool returns agentID's registered bash tool, or nil when the agent
// or its bash tool is not registered.
func (h *WSHandler) agentBashTool(agentID string) *tools.ExecTool {
	if h == nil || h.agentLoop == nil || agentID == "" {
		return nil
	}
	registry := h.agentLoop.GetRegistry()
	if registry == nil {
		return nil
	}
	inst, ok := registry.GetAgent(agentID)
	if !ok || inst == nil || inst.Tools == nil {
		return nil
	}
	t, ok := inst.Tools.Get("bash")
	if !ok {
		return nil
	}
	execTool, _ := t.(*tools.ExecTool)
	return execTool
}
