// auto_approve_gate.go: ADR-092 D9 — the agent loop's one Auto-approve check
// (docs/internal/specs/adr-092-auto-for-other-tools-design.md §2, §5.1, §5.4).
//
// autoApproveActive is the single answer to "is Auto on for this call?".
// bash's shell mode (ShellPermissionGate.liveMode) and every other tool's
// Auto verdict (autoApproveFor) both read it, so the two can never disagree
// about whether Auto applies. autoApproveFor then asks pkg/tools'
// classifier whether this particular call may run without a prompt.

package agent

import (
	"context"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// autoApproveActive reports whether Auto-approve is in force for calls in
// sessionID. Both of the following must hold:
//
//   - a config is loaded and God Mode is off (God Mode floors the ceiling at
//     allow and has no Auto machinery of its own);
//   - SessionAutoApprove is on: the chat's per-session modifier when set,
//     otherwise the global default. A delegated turn runs in its own session
//     that inherited its parent's modifier (inheritSessionPermissions), so a
//     helper of an auto-approve chat is auto-approved exactly as its parent.
//
// [2026-09-24, founder decision] Auto no longer requires an enforcing kernel
// sandbox (ADR-092 D1/J13, revised). It applies to every tool, bash
// included, whether or not Landlock/Seatbelt is enforcing — that covers
// Windows, a sandbox that failed to start, and permissive mode. Without a
// kernel sandbox, bash's D7/D8 pre-flights and the text-based guards are the
// only checks on what a command touches; see ADR-092's 2026-09-24 revision
// note for the accepted risk. Whether a kernel sandbox was enforcing at call
// time is still recorded on the tool.auto_approved audit row
// (emitToolAutoApprovedAudit below) so an operator can find every
// auto-approval that ran unconfined.
func (al *AgentLoop) autoApproveActive(sessionID string) bool {
	if al == nil {
		return false
	}
	cfg := al.GetConfig()
	if cfg == nil || GodModeActive(cfg) {
		return false
	}
	return al.SessionAutoApprove(sessionID)
}

// autoApproveActiveFor is autoApproveActive for the calling turn's own
// acting session.
func (al *AgentLoop) autoApproveActiveFor(ts *turnState) bool {
	if ts == nil {
		return false
	}
	return al.autoApproveActive(ts.transcriptSessionID)
}

// autoApproveFor returns the Auto verdict for one call whose effective
// policy has already resolved to "ask". It returns an "asks" verdict for
// bash (bash has its own ADR-092 shell mode) and whenever Auto is not
// active; otherwise it consults pkg/tools' classifier with the agent's own
// registered instance and the turn context the tool will execute under, so
// the classifier resolves paths exactly as the tool will.
func (ex *agentLoopRunTurnToolsExecute) autoApproveFor() tools.AutoVerdict {
	rt := ex.rx.rr.rq.ri.rf.rt
	ts := rt.ts
	if ex.toolName == "bash" {
		return tools.AutoVerdict{Class: tools.AutoVerdictClassAsks, Reason: "bash is decided by its own shell permission mode"}
	}
	if !rt.al.autoApproveActiveFor(ts) {
		return tools.AutoVerdict{Class: tools.AutoVerdictClassAsks, Reason: "Auto-approve is not active"}
	}
	var instance tools.Tool
	if ts.agent != nil && ts.agent.Tools != nil {
		if t, ok := ts.agent.Tools.Get(ex.toolName); ok {
			instance = t
		}
	}
	return tools.ClassifyAutoApprove(rt.turnCtx, ex.toolName, instance, ex.toolArgs)
}

// emitToolAutoApprovedAudit writes the §5.5 tool.auto_approved row for a
// call Auto is about to run without a prompt. kernel_sandbox on the row
// records sandbox.TurnPolicyBaseInstalled() at this exact moment
// [2026-09-24, founder decision]: since Auto no longer requires an
// enforcing kernel sandbox, this is the only place that records whether THIS
// particular auto-approval ran confined or not.
func (al *AgentLoop) emitToolAutoApprovedAudit(ctx context.Context, ts *turnState, toolName string, verdict tools.AutoVerdict) {
	if ts == nil {
		return
	}
	var paths []string
	for _, p := range verdict.Paths {
		paths = append(paths, p.Real)
	}
	audit.EmitToolAutoApproved(ctx, al.auditLogger, ts.agentID, ts.sessionKey, toolName, string(verdict.Class), verdict.Reason, paths,
		sandbox.TurnPolicyBaseInstalled())
}
