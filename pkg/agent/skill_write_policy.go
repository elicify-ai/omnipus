// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// Skills spec r15 §10.1: a file-tool write into a skill folder is decided as
// a skill change, in one composition step before approval. The file tool's
// policy and edit_skill's are combined strictest-wins (deny > ask > allow),
// so an edit_skill deny cannot be bypassed by a write_file allow, and an
// edit_skill ask shows the standard approval card for the write.

import (
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// skillEditDeniedReason is the denial reason of a skill write refused by the
// composed policy (§10.1 item 3).
const skillEditDeniedReason = "skill_edit_denied"

// composeSkillWritePolicy applies §10.1 items 2–3 to the current call. It
// returns the targeted skill key and true when the call is a file-tool write
// whose path resolves inside an installed skill folder; ex.toctouPolicy then
// holds the composed verdict. Any other call is left untouched. A path that
// cannot be resolved is left to the tool, which refuses it.
func (ex *agentLoopRunTurnToolsExecute) composeSkillWritePolicy(tc providers.ToolCall) (string, bool) {
	rt := ex.rx.rr.rq.ri.rf.rt
	if rt.ts == nil || rt.ts.agent == nil || rt.ts.agent.Tools == nil {
		return "", false
	}
	tool, ok := rt.ts.agent.Tools.Get(ex.toolName)
	if !ok {
		return "", false
	}
	targeter, ok := tool.(tools.SkillWriteTargeter)
	if !ok {
		return "", false
	}
	args := ex.toolArgs
	if args == nil {
		args = tc.Arguments
	}
	key, ok := targeter.SkillWriteTarget(rt.turnCtx, args)
	if !ok {
		return "", false
	}
	ex.toctouPolicy = stricterPolicy(ex.toctouPolicy, rt.al.editSkillPolicy(rt.ts))
	return key, true
}

// editSkillPolicy is the agent's effective edit_skill verdict. When edit_skill
// is registered for the agent it is the live per-tool verdict
// (resolveSingleToolPolicy); otherwise it is the agent's live global×agent
// policy merge for edit_skill — the same verdict ResolveTurnFSPolicy uses for
// SkillsReadAll — so a loop without the system-tool registration (no
// sysagent deps wired) still decides from the operator's policy rather than
// treating the missing registration as a deny. A missing agent is deny.
func (al *AgentLoop) editSkillPolicy(ts *turnState) string {
	if ts.agent != nil && ts.agent.Tools != nil {
		if _, ok := ts.agent.Tools.Get("edit_skill"); ok {
			return al.resolveSingleToolPolicy(ts, "edit_skill")
		}
	}
	registry := al.GetRegistry()
	if registry == nil || ts.agent == nil {
		return string(config.ToolPolicyDeny)
	}
	current, exists := registry.GetAgent(ts.agent.ID)
	if !exists {
		return string(config.ToolPolicyDeny)
	}
	return tools.ResolveEffectivePolicy(current.LoadToolPolicy(), "edit_skill")
}

// stricterPolicy returns the stricter of two verdicts: deny > ask > allow.
// Anything unrecognised counts as deny (fail closed).
func stricterPolicy(a, b string) string {
	rank := func(p string) int {
		switch p {
		case string(config.ToolPolicyAllow):
			return 0
		case string(config.ToolPolicyAsk):
			return 1
		default:
			return 2
		}
	}
	names := [...]string{string(config.ToolPolicyAllow), string(config.ToolPolicyAsk), string(config.ToolPolicyDeny)}
	return names[max(rank(a), rank(b))]
}

// denySkillWrite records a skill write refused by the composed policy: the
// existing deny path and audit with reason skill_edit_denied (§10.1 item 3).
// Like policy_denied it never quarantines, so a later policy change is seen.
func (ex *agentLoopRunTurnToolsExecute) denySkillWrite(tc providers.ToolCall) agentLoopRunTurnToolsExecuteFlow {
	rt := ex.rx.rr.rq.ri.rf.rt
	// The model reads the policy_denied text; the payload and audit carry the
	// specific reason. No new denial-table row: the table is the pinned
	// ADR-058 set.
	cls, _ := ClassifyDenial("policy_denied")
	cls.Reason = skillEditDeniedReason
	denyMsg := denialPayloadJSON(ex.toolName, skillEditDeniedReason, cls)
	rt.al.emitPolicyDenyAudit(rt.ts, ex.toolName, "deny", skillEditDeniedReason)
	if err := ex.admitAndCheckpoint(toolResultAdmission{
		Tool: tc.Name, ToolCallID: tc.ID, Content: denyMsg, IsError: true, ParallelN: len(ex.rx.rr.normalizedToolCalls),
	}); err != nil {
		return ex.contextWindowExit(err)
	}
	rt.al.emitEvent(
		EventKindToolExecSkipped,
		rt.ts.eventMeta("runTurn", "turn.tool.skipped"),
		ToolExecSkippedPayload{Tool: ex.toolName, Reason: "permission_denied (" + skillEditDeniedReason + ")"},
	)
	if used, exhausted := rt.ts.recordToolDenial(ex.ledgerToolName, skillEditDeniedReason, false, denyMsg); exhausted {
		ex.rx.rr.rq.ri.turnStatus = TurnEndStatusAborted
		ex.rx.ret0, ex.rx.ret1 = rt.al.abortTurnForToolDenialBudget(rt.ts, ex.ledgerToolName, skillEditDeniedReason, used)
		ex.ret0 = agentLoopRunTurnToolsReturn
		return agentLoopRunTurnToolsExecuteReturn
	}
	return agentLoopRunTurnToolsExecuteContinue
}
