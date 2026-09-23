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
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// autoApproveActive reports whether Auto-approve is in force for agentID's
// calls in sessionID. All of the following must hold:
//
//   - a config is loaded and God Mode is off (God Mode has no sandbox, and
//     Auto needs one);
//   - a delegated turn's own agent does not carry AutoApproveDisabled — a
//     delegate's own off-switch always wins over anything inherited from the
//     parent's chat, including a per-chat modifier set on its session;
//   - ResolveAutoApprove is on: the global default, then the agent's own
//     off-switch, then the chat's per-session modifier;
//   - a kernel sandbox is enforcing (ruling J13: one rule for every tool).
func (al *AgentLoop) autoApproveActive(agentID, sessionID string, delegated bool) bool {
	if al == nil {
		return false
	}
	cfg := al.GetConfig()
	if cfg == nil || GodModeActive(cfg) {
		return false
	}
	if delegated && agentAutoApproveDisabledIn(cfg, agentID) {
		return false
	}
	if !al.SessionAutoApprove(agentID, sessionID) {
		return false
	}
	return sandbox.TurnPolicyBaseInstalled()
}

// autoApproveActiveFor is autoApproveActive for the calling turn: its own
// agent, its own acting session, and whether it is a delegated sub-turn.
func (al *AgentLoop) autoApproveActiveFor(ts *turnState) bool {
	if ts == nil {
		return false
	}
	return al.autoApproveActive(ts.agentID, ts.transcriptSessionID, ts.isDelegated())
}

// isDelegated reports whether ts is a sub-turn spawned by another turn.
func (ts *turnState) isDelegated() bool {
	return ts != nil && (ts.depth > 0 || ts.parentTurnID != "" || ts.parentTurnState != nil)
}

// agentAutoApproveDisabledIn reports whether agentID's own config entry in
// cfg carries AutoApproveDisabled=true.
func agentAutoApproveDisabledIn(cfg *config.Config, agentID string) bool {
	if cfg == nil {
		return false
	}
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == agentID {
			return cfg.Agents.List[i].AutoApproveDisabled
		}
	}
	return false
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
// call Auto is about to run without a prompt.
func (al *AgentLoop) emitToolAutoApprovedAudit(ctx context.Context, ts *turnState, toolName string, verdict tools.AutoVerdict) {
	if ts == nil {
		return
	}
	var paths []string
	for _, p := range verdict.Paths {
		paths = append(paths, p.Real)
	}
	audit.EmitToolAutoApproved(ctx, al.auditLogger, ts.agentID, ts.sessionKey, toolName, verdict.Class, verdict.Reason, paths)
}
