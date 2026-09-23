// loop_policy.go: Tool policy and approval at exec time

package agent

import (
	"context"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/shellrule"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// ResolveApprovalToolPolicy returns the effective tool policy ("allow"/"ask"/
// "deny") the WS approval hook consults for one (agentID, toolName) at exec time.
//
// Unification (#438): this routes the gateway exec gate through the SAME
// authoritative primitive (tools.EffectiveToolPolicy) AND the SAME live policy
// snapshot (the agent instance's LoadToolPolicy) that the agent loop's
// FilterToolsByPolicy uses at defs-assembly time, so the two can never diverge.
// It encapsulates, in order: (1) the scope gate (fail-closed for unknown
// scopes), and (2) global×agent strictest-wins (deny > ask > allow, god-mode,
// wildcards). ToolSearch resolves through this same merge as every other
// static builtin tool — it is seeded "allow" as real, explicit data for every
// agent (pkg/coreagent/core.go), not a code-level force-allow (there used to
// be an unconditional infra fast-path here; it was a CLAUDE.md
// hard-constraint-6 violation and has been removed).
//
// BEHAVIOR CHANGE (intentional): this ALIGNS the exec gate to the agent loop's
// wildcard-aware verdict. The OLD gateway resolver matched policy keys by
// exact-name only (it ignored ".*"/"_*" wildcard keys on both the global floor
// and the agent policy); routing through tools.EffectiveToolPolicy now honors
// those wildcards exactly as FilterToolsByPolicy always did. It only narrows or
// matches the loop's verdict — it never widens past it.
//
// Inputs are sourced from the registry when the agent is known (the exact
// snapshot the loop uses); when the agent is not in the registry it falls back
// to building a ToolPolicyCfg from the global config (sandbox floor + the agent's
// builtin policy from cfg.Agents.List) so the gate still enforces correctly
// pre-registration. The tool's scope is resolved from the agent's registry when
// the tool is registered; otherwise ScopeGeneral is assumed (a tool that reached
// the exec gate was already surfaced to the model, so it is not an unknown-scope
// tool — ScopeGeneral imposes no extra restriction beyond the policy merge).
func (al *AgentLoop) ResolveApprovalToolPolicy(agentID, toolName string) string {
	// Preferred path: resolve through the agent instance's LIVE policy snapshot
	// (LoadToolPolicy — the same *ToolPolicyCfg, including any GodMode flag, that
	// FilterToolsByPolicy receives) and the tool's real scope, so this verdict
	// equals the loop's defs-filter verdict for this tool.
	if al.registry != nil {
		if inst, ok := al.registry.GetAgent(agentID); ok && inst != nil {
			scope := tools.ScopeGeneral
			if inst.Tools != nil {
				if t, found := inst.Tools.Get(toolName); found {
					scope = t.Scope()
				}
			}
			return tools.EffectiveToolPolicy(inst.LoadToolPolicy(), scope, inst.AgentType, toolName)
		}
	}

	// Fallback: build the policy inputs from the global config. Used when the
	// agent is not (yet) in the registry. This path is wildcard-aware (it routes
	// through tools.EffectiveToolPolicy) but is intentionally NOT god-mode-aware
	// (no GodMode flag set on polCfg) and assumes ScopeGeneral (we cannot resolve
	// the tool's real scope without the registry). Both omissions make this
	// fallback strictly MORE restrictive (fail-closed) than the live registry
	// path, never more permissive: under god mode it may "ask"/"deny" a tool the
	// live path would allow. The divergence is transient (only until the agent is
	// registered) and never widens, so it is safe.
	cfg := al.GetConfig()
	if cfg == nil {
		return "ask"
	}
	// No default-policy fallback (CLAUDE.md hard constraint 6): only explicit
	// global/agent entries are threaded through; a tool with no match on
	// either side fails closed to "deny" inside tools.EffectiveToolPolicy.
	polCfg, agentType := tools.BuildFallbackPolicyCfg(cfg, agentID)
	return tools.EffectiveToolPolicy(polCfg, tools.ScopeGeneral, agentType, toolName)
}

// ResolveRegisteredToolPolicy resolves one tool through the same live policy
// snapshot used to assemble and execute calls. It deliberately has no config
// fallback: callers of management inventory must prove a current actor.
func (al *AgentLoop) ResolveRegisteredToolPolicy(agentID, toolName string) (string, bool) {
	if al.registry == nil {
		return string(config.ToolPolicyDeny), false
	}
	inst, ok := al.registry.GetAgent(agentID)
	if !ok || inst == nil {
		return string(config.ToolPolicyDeny), false
	}
	scope := tools.ScopeGeneral
	if inst.Tools != nil {
		if tool, found := inst.Tools.Get(toolName); found {
			scope = tool.Scope()
		}
	}
	return tools.EffectiveToolPolicy(inst.LoadToolPolicy(), scope, inst.AgentType, toolName), true
}

// SetToolApprover injects the gateway's policy-level approval implementation into
// the loop (FR-011). Must be called before any turns start; safe from any goroutine.
// Passing nil clears the approver (ask-policy tools treated as allow — open gate).
func (al *AgentLoop) SetToolApprover(a PolicyApprover) {
	al.mu.Lock()
	al.toolApprover = a
	al.mu.Unlock()
}

// SetAllowGodMode sets the god-mode opt-in flag (latch 2). Must be called
// before WireTier13Deps so the coercion logic picks up the correct value. If
// called after WireTier13Deps, the change takes effect on the next hot-reload.
//
// allow is expected to already be the combined boot decision — the caller
// (pkg/gateway/gateway.go's resolveAllowGodMode) ORs the --allow-god-mode CLI
// flag with the config-persisted sandbox.god_mode_allowed grant before
// calling this, so there is exactly one source of truth for availability.
func (al *AgentLoop) SetAllowGodMode(allow bool) {
	al.mu.Lock()
	al.allowGodMode = allow
	al.mu.Unlock()
	// Publish god-mode AVAILABILITY (boot flag AND build support) to the
	// package-level gate so the resolution-time override engine
	// (agentToolsCfgToPolicy / godModeActive) can decide whether the runtime
	// sandbox.god_mode switch has any effect. Availability is fixed at boot;
	// the on/off STATE lives in cfg.Sandbox.GodMode and is re-read on every
	// agent rebuild (TriggerReload).
	setGodModeAvailable(allow && sandbox.GodModeAvailable)
}

// checkToolDedupInvariant verifies that the assembled tool list has no duplicate
// names (FR-066). Returns a non-nil error on the first duplicate found; also emits
// a HIGH audit event "tool.assembly.duplicate_name" and logs a structured error.
func (al *AgentLoop) checkToolDedupInvariant(ts *turnState, filtered []tools.Tool) error {
	seen := make(map[string]string, len(filtered)) // name → first source tag
	for _, t := range filtered {
		name := t.Name()
		var sourceTag string
		if cat := t.Category(); cat == tools.CategoryMCP {
			sourceTag = "mcp:unknown"
		} else {
			sourceTag = "builtin"
		}
		if firstSrc, exists := seen[name]; exists {
			// Duplicate detected — audit + fail.
			sources := []string{firstSrc, sourceTag}
			details := map[string]any{
				"tool_name": name,
				"sources":   sources,
				"kept":      firstSrc,
				"agent_id":  ts.agentID,
				"turn_id":   ts.turnID,
			}
			logger.ErrorCF("agent", "FR-066: tools[] dedup invariant violated",
				map[string]any{"tool_name": name, "sources": sources, "agent_id": ts.agentID})
			// CRIT-6 + typed-Decision/Event migration: route through audit.EmitEntry
			// so Log failure bumps the audit-skipped counter; use the typed
			// EventToolAssemblyDuplicateName and DecisionDeny constants.
			audit.EmitEntry(al.auditLogger, &audit.Entry{
				Event:     audit.EventToolAssemblyDuplicateName,
				Decision:  audit.DecisionDeny,
				AgentID:   ts.agentID,
				Tool:      name,
				SessionID: ts.sessionKey,
				User:      ts.auditUser(), // FR-017
				Details:   details,
			})
			return fmt.Errorf("tools[] dedup invariant violated: tool %q appears from sources %v", name, sources)
		}
		seen[name] = sourceTag
	}
	return nil
}

// resolveToolPolicyAtExec performs the FR-079 TOCTOU re-check: re-loads the
// per-agent policy pointer and re-resolves the effective policy for toolName
// immediately before Execute is called.
//
// Returns the live effective policy ("allow", "ask", "deny").
// If the live policy matches the filter-time snapshot (filterTimePolicyMap) the
// return value is the same and no audit is emitted; discrepancies are audited as
// "mid_turn_policy_change" by the caller.
//
// A tool absent from filterTimePolicyMap was not in the filter-time allow set
// (it was deny at filter time or not registered). If it is also deny at re-check
// time we return "deny"; if it has become allow/ask we still return "deny" to be
// conservative (the LLM was not given this tool's definition, so executing it is
// unsound).
func (al *AgentLoop) resolveToolPolicyAtExec(
	ts *turnState,
	toolName string,
	filterTimePolicyMap map[string]string,
) string {
	filterTimePolicy, wasInFilterMap := filterTimePolicyMap[toolName]
	if !wasInFilterMap {
		// Tool was not included at filter time — treat as deny regardless of live policy.
		return "deny"
	}

	// Re-run FilterToolsByPolicy with the freshly loaded pointer to get the live
	// effective policy for this one tool. We run on the full tool list but only
	// care about our tool.
	livePolicy := al.resolveSingleToolPolicy(ts, toolName)

	// Discovery infrastructure (ToolSearch) is intentionally non-deniable
	// (user clarification 2026-09-18). Once the filter-time snapshot offered
	// it, operator Deny/Ask cannot block execution. Live deny still means
	// the agent is gone or the tool is unregistered. Goal-forcing withholds
	// the door via the offered-set gate (toolNotOfferedRefusal), not here.
	if tools.ToolManifestTier(toolName) == tools.ManifestInfra {
		if livePolicy == "deny" {
			return "deny"
		}
		return "allow"
	}

	// If policy flipped to deny mid-turn, the caller will audit "mid_turn_policy_change".
	if livePolicy == "deny" && filterTimePolicy != "deny" {
		return "deny"
	}
	// If policy is now ask but was allow at filter time — treat as ask (conservative).
	if livePolicy == "ask" {
		return "ask"
	}
	// Use filter-time policy as the authoritative effective policy when live==allow
	// and filter-time was ask — preserves the ask gate from filter time.
	if filterTimePolicy == "ask" {
		return "ask"
	}
	return filterTimePolicy
}

// resolveSingleToolPolicy loads the current policy pointer and resolves the
// effective policy for toolName using FilterToolsByPolicy. Returns "deny" if
// the tool is not found in the agent's registered tools or has no policy
// entry on either side.
//
// Discovery infrastructure (ToolSearch) is intentionally non-deniable
// once the agent still exists and the tool is registered (user
// clarification 2026-09-18). Operator Deny/Ask cannot block it. A
// missing registry, deleted agent, or unregistered name still denies.
// Filter-time offered applicability lives in resolveToolPolicyAtExec
// (absent from the snapshot → deny). Target-tool permissions still
// resolve through FilterToolsByPolicy below, including mid-turn revoke.
func (al *AgentLoop) resolveSingleToolPolicy(ts *turnState, toolName string) string {
	registry := al.GetRegistry()
	if registry == nil {
		return "deny"
	}
	current, exists := registry.GetAgent(ts.agent.ID)
	if !exists {
		return "deny"
	}
	if tools.ToolManifestTier(toolName) == tools.ManifestInfra {
		if current.Tools == nil {
			return "deny"
		}
		if _, ok := current.Tools.Get(toolName); !ok {
			return "deny"
		}
		return "allow"
	}
	// A running turn retains its loaded definitions across fast publication.
	// Resolve their authority against the current instance, never the old snapshot.
	allTools := ts.agent.Tools.GetAll()
	_, pmap := tools.FilterToolsByPolicy(allTools, current.AgentType, current.LoadToolPolicy())
	p, ok := pmap[toolName]
	if !ok {
		return "deny"
	}
	return p
}

// loadToolApprover returns the wired PolicyApprover or, when none has been
// set, a fail-closed nopPolicyApprover that denies every ask with reason
// "no_approver_configured" and emits one `approver.fallback` audit row per
// process (V2.B; closes silent-failure-hunter BE CRIT-1). The nop carries
// al.auditLogger so the diagnostic emit lands in the operator's JSONL.
//
// The previous default returned `nopPolicyApprover{}` which auto-approved
// every ask call — including admin-flagged tools — with zero log and zero
// audit. Test code that needs the auto-approve behavior now installs
// `testAutoApproveApprover{}` explicitly via SetToolApprover (build tag
// `test`; see `tool_approver_testonly.go`).
func (al *AgentLoop) loadToolApprover() PolicyApprover {
	al.mu.RLock()
	a := al.toolApprover
	logger := al.auditLogger
	al.mu.RUnlock()
	if a == nil {
		return nopPolicyApprover{auditLogger: logger}
	}
	return a
}

// CheckGrantOrRequestApproval is the SOLE consultation point for tool-approval
// grants on the "ask" policy path (ADR-036 §3.4). It first checks the
// session-scoped "Always Allow" grant store (al.ApprovalGrants()); only when
// no grant is on file does it fall through to the interactive human-approval
// flow via the wired PolicyApprover (loadToolApprover -> RequestApproval).
//
// Before ADR-036 this consultation lived in the gateway's wsApprovalHook
// (pkg/gateway/ws_approval.go, deleted by this change) — a WebSocket-frame
// approval gate that ran BEFORE runTurn's TOCTOU "ask" branch below and
// unconditionally denied after a 90s timeout once its answering frontend UI
// (ExecApprovalBlock/ExecApprovalTool) was removed in the same ADR-036
// change — making the "ask" branch, and therefore this grant store,
// permanently unreachable for any WebSocket-connected chat session. Retiring
// that gate and relocating grant consultation HERE (the only path that was
// ever reachable in practice) is the fix.
//
// Only called when the effective tool policy has already resolved to "ask" —
// callers MUST resolve policy (allow/deny/ask) themselves before calling this;
// it does not re-check policy itself. In runTurn, "deny" short-circuits with
// `continue` and "allow" falls straight through to execution, so neither ever
// reaches this function — a grant can never widen a "deny" verdict, and an
// "allow" verdict never touches the grant store at all.
//
// Exported (rather than folded inline against the unexported *turnState) so it
// is directly unit-testable — including from pkg/gateway, which imports
// pkg/agent but cannot construct a *turnState — without spinning up a
// WebSocket connection. See pkg/gateway/ws_approval_grants_test.go.
//
// Identity (ADR-057 FR-031/FR-080, W10b — corrected from the pre-ADR-057
// description this comment used to carry): sessionID MUST be the caller's
// own ACTING session id — turnState.transcriptSessionID — NOT the
// session-store scope key (turnState.sessionKey) and NOT the ROUTING
// identity (turnState.routingSessionID, W4). This is a narrower requirement
// than the pre-ADR-057 invariant it replaces: before D1, a delegated child's
// transcriptSessionID was always threaded through unchanged from its parent
// (subturn.go's spawnSubTurn), so "the one identity shared across a
// delegation chain" and "the child's own identity" were the same value and
// this distinction did not exist. Under ADR-057 the child gets its OWN
// distinct, store-backed transcriptSessionID (FR-005/FR-007/FR-009), and
// ApprovalGrantStore.InheritFrom (pkg/security/approvalgrants.go, U17a)
// copies grants at spawn time INTO exactly that child key — {dstSessionID:
// childID, dstAgentID} — never into the routing/root id. A grant read here
// keyed on anything other than the calling turn's own transcriptSessionID
// (in particular, keying on routingSessionID, which for a grandchild equals
// the ROOT's session id) would silently miss every grant InheritFrom wrote
// for THIS turn and force a real human through the 300s interactive
// approval wait on every delegated call — the exact failure class FR-031's
// two-key InheritFrom redesign exists to prevent on the write side; this is
// its read-side half (see pkg/security/approvalgrants_adr057_test.go for the
// write side, TestCheckGrantOrRequestApproval_UsesActingSessionKey below for
// this one). ClearSession (session teardown, U17b) uses the same key for the
// same reason: it is the acting session's own bucket, not a shared one.
// ADR-092 D4/FR-024 extension: for the "bash" tool specifically, this
// consultation ALSO checks the prefix-scope grant kind
// (ApprovalGrantStore.IsPrefixAllowed) alongside the classic exact-
// fingerprint IsAllowed check above — closing the gap where a human's
// earlier "Allow" with scope=prefix (e.g. "npm run test") never suppressed
// the dialog for a later, textually-different invocation ("npm run test
// -v") on this SAME classic ask-policy path. tools.BashPrefixGrantCheck
// does the resolve-and-verify (D3's own look-alike defence) so this
// function never needs its own copy of that logic.
// recordGrant (third return, review finding #5) is true only when this call
// reached a FRESH human "allow" decision (not "allow_once", and not an
// already-existing grant this function's own IsAllowed/BashPrefixGrantCheck
// short-circuits above already satisfied — there is nothing NEW to record
// in either of those branches, so both return recordGrant=false). The
// classic ask-policy caller (loop_run_turn_tools.go) does not consume this
// value; the ADR-092 D7/D8 pre-flight escalation callers
// (ShellPermissionGate.RequestShellApproval, below) do.
func (al *AgentLoop) CheckGrantOrRequestApproval(
	ctx context.Context,
	sessionID, agentID, toolName, toolCallID, turnID string,
	args map[string]any,
) (approved bool, denialReason string, recordGrant bool) {
	if al.ApprovalGrants().IsAllowed(sessionID, agentID, toolName, args) {
		return true, "", false
	}
	if toolName == "bash" && tools.BashPrefixGrantCheck(al.ApprovalGrants(), sessionID, agentID, toolName, args) {
		return true, "", false
	}
	approver := al.loadToolApprover()
	return approver.RequestApproval(ctx, PolicyApprovalReq{
		ToolCallID: toolCallID,
		ToolName:   toolName,
		Args:       cloneStringAnyMap(args),
		AgentID:    agentID,
		SessionID:  sessionID,
		TurnID:     turnID,
	})
}

// emitPolicyDenyAudit writes a tool.policy.deny.attempted audit entry.
// context is a free-form note such as "mid_turn_policy_change" or the denial reason.
// extra, when non-nil, is merged into the Details map so callers can attach
// structured fields (e.g. schedule_job_id) without a separate emit.
//
// CRIT-6 + typed-Decision/Event migration: routes through audit.EmitEntry so
// Log failure bumps the audit-skipped counter (/health audit_degraded), and
// uses the typed EventToolPolicyDenyAttempted + DecisionDeny constants in
// place of raw string literals.
func (al *AgentLoop) emitPolicyDenyAudit(
	ts *turnState,
	toolName, resolvedPolicy, context string,
	extra ...map[string]any,
) {
	details := map[string]any{
		"turn_id":         ts.turnID,
		"resolved_policy": resolvedPolicy,
		"context":         context,
	}
	for _, m := range extra {
		for k, v := range m {
			details[k] = v
		}
	}
	audit.EmitEntry(al.auditLogger, &audit.Entry{
		Event:     audit.EventToolPolicyDenyAttempted,
		Decision:  audit.DecisionDeny,
		AgentID:   ts.agentID,
		Tool:      toolName,
		SessionID: ts.sessionKey,
		User:      ts.auditUser(), // FR-017
		Details:   details,
	})
}

// emitScheduledAutoDenyAudit writes a tool.policy.ask.denied audit entry for
// the headless scheduled-run auto-deny path (O-3 / F-13 / issue #342).
//
// It routes through audit.EmitToolPolicyAskDenied (the canonical helper) so:
//   - CRIT-6 is honored: write failures bump IncSkipped via the Emit path,
//     making /health audit_degraded accurate.
//   - Severity is SeverityInfo (documented contract for ask.denied events).
//   - The reason field carries AskDenyReasonScheduled so SIEM rules can filter
//     headless auto-denies without string-matching the context note.
//
// The schedule identity (job_id, job_name) is read from the run context via
// scheduledJobContextFrom — the cron fire path (gateway RunScheduled) injects
// it with WithScheduledJobContext before calling ProcessScheduled. When the
// job info is missing from the context (e.g. a caller that omits the wrapper),
// a slog.Warn is emitted so the lost attribution is loud, and the ask.denied
// entry is still written (with empty schedule fields) so the denial is always
// recorded.
//
// The companion emitPolicyDenyAudit call (tool.policy.deny.attempted) at the
// same call site carries the same schedule identity in its Details map, giving
// operators two correlated entries per auto-deny: one for the policy-deny
// audit trail and one for the structured ask.denied reason.
func (al *AgentLoop) emitScheduledAutoDenyAudit(
	ctx context.Context,
	ts *turnState,
	toolName, toolCallID string,
) {
	var jobID, jobName string
	if info, ok := scheduledJobContextFrom(ctx); ok && info.JobID != "" {
		jobID = info.JobID
		jobName = info.JobName
	} else {
		// MEDIUM: missing job identity means the audit entry can't name the
		// schedule that was responsible for the skip. This is loud so operators
		// notice mis-wired fire paths (e.g. ProcessScheduled called without
		// WithScheduledJobContext). The ask.denied entry is still emitted —
		// losing the denial record entirely is worse than a partially-attributed
		// one.
		logger.WarnCF("agent", "scheduled auto-deny audit: job identity missing from context",
			map[string]any{
				"note":        "ask.denied entry will lack schedule_job_id/name",
				"agent_id":    ts.agentID,
				"tool":        toolName,
				"session_key": ts.sessionKey,
			},
		)
	}

	// Emit the canonical tool.policy.ask.denied record. approvalID and
	// approverUserID are empty: in the headless path no approval was ever
	// requested, so there is no approval id to reference and no human actor.
	// argsHash and cancelledToolCallIDs are also empty / nil for the same reason.
	audit.EmitToolPolicyAskDenied(
		ctx,
		al.auditLogger,
		"", // approvalID — no approval request was made
		"", // approverUserID — no human actor
		toolName,
		ts.agentID,
		ts.sessionKey,
		ts.turnID,
		audit.AskDenyReasonScheduled,
		"",  // argsHash — not available at this call site
		nil, // cancelledToolCallIDs — not applicable
	)

	// The schedule identity (jobID, jobName) also appears in the companion
	// tool.policy.deny.attempted entry emitted by the emitPolicyDenyAudit call
	// at the auto-deny call site — the caller reads it from the context and
	// passes it as extra Details there. Log it here too so the structured log
	// line is self-contained even when audit writing is disabled.
	if jobID != "" {
		logger.InfoCF("agent", "scheduled auto-deny: ask-gated tool skipped in headless run",
			map[string]any{
				"schedule_job_id":   jobID,
				"schedule_job_name": jobName,
				"tool":              toolName,
				"agent_id":          ts.agentID,
			},
		)
	}
}

// ShellPermissionGate implements tools.ShellModeResolver and
// tools.ShellApprovalRequester: the ADR-092 adapter connecting the bash
// tool's enforcement (pkg/tools/shell_permission_mode.go — D3 ask rules, D7
// filesystem and D8 network pre-flights) to the AgentLoop state that decides
// them, without pkg/tools importing pkg/agent (that import would cycle).
//
// One instance per AgentLoop, built in NewAgentLoop and injected into every
// agent's bash tool by wireExecToolDepsOn as both ExecToolDeps.ShellMode and
// ExecToolDeps.ApprovalRequester. The per-chat Auto-approve modifier is read
// from Loop.SessionModes() through Loop.autoApproveActive.
type ShellPermissionGate struct {
	Loop *AgentLoop
}

// shellModeKey carries the bash mode the agent loop settled on for one tool
// call (see withPinnedShellMode).
type shellModeKey struct{}

// withPinnedShellMode records, on the tool call's context, the mode the
// agent loop used when it decided whether to prompt before dispatch
// (resolveAskPolicy). ResolveShellMode returns this pinned value instead of
// re-resolving, so the tool enforces exactly the decision the loop acted on
// (ADR-092 FR-006: a command is resolved against the mode in force at the
// pre-dispatch check). Without the pin, a chat toggled from Auto to off
// between the loop skipping the prompt and the tool running would leave the
// tool in Ask mode, which assumes the loop already prompted: the command
// would run with neither a prompt nor the Auto pre-flights.
func withPinnedShellMode(ctx context.Context, mode tools.ShellMode) context.Context {
	return context.WithValue(ctx, shellModeKey{}, mode)
}

func pinnedShellMode(ctx context.Context) (tools.ShellMode, bool) {
	if ctx == nil {
		return "", false
	}
	m, ok := ctx.Value(shellModeKey{}).(tools.ShellMode)
	return m, ok && m != ""
}

// ResolveShellMode implements tools.ShellModeResolver. A mode pinned on ctx
// by the agent loop wins; otherwise the live value is resolved (liveMode).
// A nil receiver or nil Loop fails closed to Ask.
func (g *ShellPermissionGate) ResolveShellMode(ctx context.Context, agentID, sessionID string) tools.ShellMode {
	if m, ok := pinnedShellMode(ctx); ok {
		return m
	}
	return g.liveMode(agentID, sessionID, false)
}

// liveMode resolves which bash enforcement mode applies to agentID's call in
// sessionID right now:
//
//	God Mode active                          -> God (no approvals, no sandbox;
//	                                            D3 deny rules still apply)
//	bash policy is not "ask"                 -> Ask. "allow" runs without the
//	                                            Auto machinery (the contract:
//	                                            Auto never touches an allow
//	                                            tool); "deny" never reaches
//	                                            execution at all.
//	"ask" + Auto not active                  -> Ask (the loop prompts first)
//	"ask" + Auto active                      -> Auto (no upfront prompt; the
//	                                            tool's pre-flights ask only
//	                                            for what the sandbox cannot
//	                                            confine)
//
// "Auto active" is AgentLoop.autoApproveActive — the one check every tool
// shares (auto_approve_gate.go): Auto-approve resolved on and a kernel
// sandbox enforcing (FR-008). Every missing dependency fails closed to Ask.
func (g *ShellPermissionGate) liveMode(agentID, sessionID string, delegated bool) tools.ShellMode {
	if g == nil || g.Loop == nil {
		return tools.ShellModeAsk
	}
	cfg := g.Loop.GetConfig()
	if cfg == nil {
		return tools.ShellModeAsk
	}
	if GodModeActive(cfg) {
		return tools.ShellModeGod
	}
	if g.Loop.ResolveApprovalToolPolicy(agentID, "bash") != string(config.ToolPolicyAsk) {
		return tools.ShellModeAsk
	}
	if !g.Loop.autoApproveActive(agentID, sessionID, delegated) {
		return tools.ShellModeAsk
	}
	return tools.ShellModeAuto
}

// RequestShellApproval implements tools.ShellApprovalRequester: the D3
// ask-rule and D7/D8 pre-flight escalation call sites in pkg/tools reach
// AgentLoop.CheckGrantOrRequestApproval — the same consultation function the
// classic "ask" tool-policy path uses — through here. pkg/tools checks its
// own prefix/path/network grant kinds first and calls this only to reach the
// interactive approval dialog.
//
// A nil receiver or nil Loop fails closed (approved=false).
func (g *ShellPermissionGate) RequestShellApproval(
	ctx context.Context,
	sessionID, agentID, toolName, toolCallID, turnID string,
	args map[string]any,
) (bool, string, bool) {
	if g == nil || g.Loop == nil {
		return false, "shell permission gate not wired", false
	}
	return g.Loop.CheckGrantOrRequestApproval(ctx, sessionID, agentID, toolName, toolCallID, turnID, args)
}

// bashShellModeFor returns the ADR-092 mode for a bash call in ts's turn, or
// "" for any other tool. resolveAskPolicy records it before deciding whether
// to prompt, and the same value is pinned on the bash tool's context, so the
// prompt decision and the tool's enforcement come from one resolution.
func (al *AgentLoop) bashShellModeFor(ts *turnState, toolName string) tools.ShellMode {
	if toolName != "bash" || ts == nil {
		return ""
	}
	return al.shellGate.liveMode(ts.agentID, ts.transcriptSessionID, ts.isDelegated())
}

// bashCommandArg reads args["command"] as a string, "" when absent or not a
// string — shared by the two audit helpers below so a malformed/missing
// command never panics the audit path.
func bashCommandArg(args map[string]any) string {
	command, _ := args["command"].(string)
	return command
}

// emitShellRuleSettledAudit writes the FR-032(d)/review finding #8(c)
// shell.approval_decision event for a prompt an operator D3 ALLOW rule
// fully settled (bashRuleVerdict.settlesPrompt) — before this fix, this decision
// point left no audit trail at all, indistinguishable in the log from an
// ordinary unprompted "allow"-ceiling execution. No grant is recorded by
// this call site (the D3 rule itself is the standing authorization, not a
// session grant), so the outcome is ShellApprovalAllowOnce, matching the
// same vocabulary pkg/tools' own D3 ask-rule branch already uses for an
// equivalent "approved, no new grant" case.
func (al *AgentLoop) emitShellRuleSettledAudit(ts *turnState, args map[string]any) {
	if ts == nil {
		return
	}
	audit.EmitShellApprovalDecision(context.Background(), al.auditLogger,
		audit.ShellApprovalAllowOnce, ts.agentID, ts.transcriptSessionID, "bash", bashCommandArg(args),
		"rule_fully_allowed", "operator command_rules ALLOW rule covers every segment of this command")
}

// emitShellClassicAskDecisionAudit writes the FR-032(d)/review finding
// #8(b) shell.approval_decision event for the classic (bash tool policy ==
// "ask", non-Auto) human-in-the-loop decision path. kind is "classic_ask",
// or "rule_ask" when that one prompt also settled an operator D3 ask rule
// (§5.7). Before this fix, only
// the NEW ADR-092 D3/D7/D8 call sites inside pkg/tools emitted this event —
// the original, pre-ADR-092 "ask" consultation this branch drives (the same
// CheckGrantOrRequestApproval call every other ask-policy tool uses) left
// no audit trail of its own for bash specifically.
func (al *AgentLoop) emitShellClassicAskDecisionAudit(ts *turnState, args map[string]any, kind string, approved bool, denialReason string) {
	if ts == nil {
		return
	}
	outcome := audit.ShellApprovalDeny
	if approved {
		outcome = audit.ShellApprovalAllowOnce
	}
	audit.EmitShellApprovalDecision(context.Background(), al.auditLogger,
		outcome, ts.agentID, ts.transcriptSessionID, "bash", bashCommandArg(args), kind, denialReason)
}

// inheritSessionPermissions copies a delegating parent's session-scoped
// permission state onto a delegate at spawn: its approval grants (ADR-057
// two-key InheritFrom) and its per-chat Auto-approve modifier (ADR-092
// FR-005), both keyed on the parent's own session id and the child's own.
//
// Review finding #6 (MEDIUM, 2026-09-23 security fix lane): a delegate must
// never end up loosened past its OWN AutoApproveDisabled=true, however it
// inherits — FR-005's "a delegate takes the tightest of (parent modifier,
// its own override)" makes the delegate's own off-switch a floor the
// inherited modifier cannot cross. sessionmode.go's per-chat scope is
// documented and tested (TestResolveAutoApprove) as the one scope allowed
// to loosen past an agent's off-switch for that CHAT'S OWN directly-
// attached agent — a human is present and made the choice for exactly that
// conversation. A delegate's session is not that: nobody reviewed THIS
// agent's off-switch when the PARENT's chat toggle was set. Skipping the
// SessionModes() copy here — rather than hardening ResolveAutoApprove
// itself — leaves that documented direct-chat behaviour intact and closes
// only the inheritance gap: with no per-chat modifier of its own, the
// child's later ResolveAutoApprove call falls through to its own agent-level
// AutoApproveDisabled check, which already resolves to Auto off correctly.
func (al *AgentLoop) inheritSessionPermissions(parentSessionID, parentAgentID, childSessionID, childAgentID string) {
	al.ApprovalGrants().InheritFrom(parentSessionID, parentAgentID, childSessionID, childAgentID)
	if al.agentAutoApproveDisabled(childAgentID) {
		return
	}
	al.SessionModes().InheritFrom(parentSessionID, childSessionID)
}

// agentAutoApproveDisabled reports whether agentID's own config carries
// AutoApproveDisabled=true. A nil config or an agent absent from the list
// reports false (not disabled).
func (al *AgentLoop) agentAutoApproveDisabled(agentID string) bool {
	return agentAutoApproveDisabledIn(al.GetConfig(), agentID)
}

// bashRuleVerdict is the ADR-092 D3 operator-rule verdict for one bash call,
// evaluated once in resolveAskPolicy and read by both the prompt decision
// and the one-dialog fix (§5.7). ok is false for any non-bash call, an empty
// command, or an agent without a registered bash tool.
type bashRuleVerdict struct {
	verdict shellrule.CommandVerdict
	ok      bool
}

// bashCommandRuleVerdict evaluates the call against the agent's own
// registered bash tool (tools.ExecTool.EvaluateCommandRules), so this
// decision and the tool's enforcement use one rule list and one evaluator.
func bashCommandRuleVerdict(ts *turnState, toolName string, args map[string]any) bashRuleVerdict {
	if toolName != "bash" || ts == nil || ts.agent == nil || ts.agent.Tools == nil {
		return bashRuleVerdict{}
	}
	command, _ := args["command"].(string)
	if command == "" {
		return bashRuleVerdict{}
	}
	t, ok := ts.agent.Tools.Get("bash")
	if !ok {
		return bashRuleVerdict{}
	}
	exec, ok := t.(*tools.ExecTool)
	if !ok {
		return bashRuleVerdict{}
	}
	return bashRuleVerdict{verdict: exec.EvaluateCommandRules(command), ok: true}
}

// settlesPrompt reports whether the operator rules already settle a bash
// call the "ask" policy would otherwise prompt for:
//
//   - every segment of the command matches an allow rule, with no deny or
//     ask rule on any segment (deny > ask > allow still holds) — the
//     allow rule is the retired exec allowlist's replacement, so the call
//     proceeds without the prompt;
//   - any segment matches a deny rule — the bash tool refuses the command
//     outright in every mode, so prompting a human first would only ask
//     them to approve a command that cannot run.
//
// An allow verdict does not bypass the D7/D8 pre-flights: under Auto the
// tool still escalates a write outside the sandbox or a network need. Any
// other verdict (no rule, a partial match, an ask rule, a blind spot)
// returns false and the normal prompt runs.
func (v bashRuleVerdict) settlesPrompt() bool {
	return v.ok && (v.verdict.Action == shellrule.ActionDeny || v.verdict.FullyAllowed())
}

// needsRuleAsk reports whether the upfront prompt must also settle an
// operator D3 {action: ask} rule (§5.7): the call is in Ask mode, no rule
// denies it, and at least one segment matched a genuine ask rule. The bash
// tool would otherwise prompt a second time for the same call.
func (v bashRuleVerdict) needsRuleAsk(mode tools.ShellMode) bool {
	return v.ok && mode == tools.ShellModeAsk &&
		v.verdict.Action != shellrule.ActionDeny &&
		tools.VerdictHasGenuineAskRuleMatch(v.verdict)
}

// ruleAskRequestArgs is the approval request for a call whose one upfront
// prompt also settles a D3 ask rule: the call's own arguments plus the
// adr092_kind "rule_ask" marker the bash tool's own rule prompt carries, and
// a note naming the matched rule so the dialog explains why it is asking.
func ruleAskRequestArgs(args map[string]any, verdict shellrule.CommandVerdict) map[string]any {
	out := make(map[string]any, len(args)+2)
	for k, v := range args {
		out[k] = v
	}
	out["adr092_kind"] = "rule_ask"
	for _, seg := range verdict.Segments {
		if seg.Action == shellrule.ActionAsk && seg.MatchedRule != nil {
			out["note"] = fmt.Sprintf("matches an operator rule that requires approval (binary=%q arg_prefix=%q)",
				seg.MatchedRule.Binary, seg.MatchedRule.ArgPrefix)
			break
		}
	}
	return out
}
