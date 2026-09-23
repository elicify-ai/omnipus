// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-092 (shell permission modes) FR-032/FR-046 typed audit events.
//
// docs/internal/specs/adr-092-shell-permission-modes-spec.md FR-046 routes
// these four events through the EXISTING audit.Entry / Logger.Log path
// (ExecTool.emitAudit's own established shape — Entry.Details is already
// the documented home for event-specific fields), not a new logDecision
// signature: "The four FR-032 events are written via ExecTool.emitAudit's
// existing audit.Entry.Details map[string]any ... new Details keys per
// event type, not a logDecision signature change."
//
// What this file supplies is the missing half of that routing: four
// DEDICATED Event* constants (shell.mode_change, shell.preflight_escalation,
// shell.grant_recorded, shell.approval_decision) so an operator can filter
// these four ADR-092 decision points in the audit log. Shell-core lane's
// first pass instead reused audit.EventExec for the "grant recorded" case
// (pkg/tools' now-deleted emitGrantAudit helper) with a
// Details["adr092_kind"] discriminator — every ADR-092 grant record was
// therefore indistinguishable, by event name, from an ordinary bash
// execution record, and the other three FR-032 decision points (mode
// change, pre-flight escalation, per-call approval decision) were not
// audited as their own event at all.
//
// Each Emit* helper below builds an *audit.Entry and writes it via
// audit.EmitEntry (CRIT-6: bumps the audit-skipped counter on write
// failure, matching every other emitter in this package) — never
// logger.Log directly.
package audit

import "context"

// Event name constants — FR-032(a)-(d). Dotted "shell.*" family, matching
// the AuditEntry.yaml contract pattern `^[a-z_.]+$`
// (contracts/components/schemas/AuditEntry.yaml) — no contract change
// needed, the pattern was already widened to allow dots (issue #667).
const (
	// EventShellModeChange — FR-032(a). A shell-permission-mode write: the
	// global mode (Settings), a per-agent override, or a per-chat modifier
	// changed — or was ATTEMPTED and refused (see ShellModeActorAgent).
	EventShellModeChange = "shell.mode_change"

	// EventShellPreflightEscalation — FR-032(b), FR-009/FR-042. An ADR-092
	// D7 (filesystem) or D8 (network) Auto-mode pre-flight found a
	// reference the session's current grants/sandbox policy does not
	// already cover, and is about to escalate to an interactive approval.
	// Emitted at the moment the escalation is TRIGGERED, before the human's
	// answer is known — EventShellApprovalDecision records the outcome.
	EventShellPreflightEscalation = "shell.preflight_escalation"

	// EventShellGrantRecorded — FR-032(c). A new ADR-092 session grant was
	// persisted to the ApprovalGrantStore: exact (D3 classic "Always
	// Allow"), prefix (D4), path-widening (D7), or network-widening (D8).
	EventShellGrantRecorded = "shell.grant_recorded"

	// EventShellApprovalDecision — FR-032(d). The per-call outcome of an
	// ADR-092 approval consultation: allow-once (approved, no grant
	// persisted), allow-with-grant (approved AND a grant was recorded), or
	// deny.
	EventShellApprovalDecision = "shell.approval_decision"
)

// ShellModeActor is the FR-032(a) actor taxonomy for a mode-change event.
type ShellModeActor string

const (
	// ShellModeActorOperator — a human changed the mode via Settings (REST).
	ShellModeActorOperator ShellModeActor = "operator"
	// ShellModeActorAgent — a tool call (set_config / agent_apply_args)
	// attempted to change the mode. Per FR-003/FR-045, a write that would
	// LOOSEN the effective mode is always refused at write time regardless
	// of actor — but the ATTEMPT is still audited (S43).
	ShellModeActorAgent ShellModeActor = "agent"
	// ShellModeActorSystem — a boot-time config-load reconciliation changed
	// the resolved value (no human or agent action this boot).
	ShellModeActorSystem ShellModeActor = "system"
)

// ShellPreflightKind is the FR-032(b) escalation family: filesystem (D7) or
// network (D8).
type ShellPreflightKind string

const (
	ShellPreflightFilesystem ShellPreflightKind = "filesystem"
	ShellPreflightNetwork    ShellPreflightKind = "network"
)

// ShellGrantScope is the FR-032(c) grant scope taxonomy.
type ShellGrantScope string

const (
	ShellGrantScopeExact           ShellGrantScope = "exact"
	ShellGrantScopePrefix          ShellGrantScope = "prefix"
	ShellGrantScopePathWidening    ShellGrantScope = "path_widening"
	ShellGrantScopeNetworkWidening ShellGrantScope = "network_widening"
)

// ShellApprovalOutcome is the FR-032(d) per-call decision taxonomy.
type ShellApprovalOutcome string

const (
	ShellApprovalAllowOnce      ShellApprovalOutcome = "allow_once"
	ShellApprovalAllowWithGrant ShellApprovalOutcome = "allow_with_grant"
	ShellApprovalDeny           ShellApprovalOutcome = "deny"
)

// EmitShellModeChange writes the FR-032(a) mode-change event. decision is
// DecisionAllow for a write that took effect and DecisionDeny for a write
// that was refused (e.g. S43/S44) — the refusal is still audited, per
// FR-032(a)'s actor-taxonomy note.
//
// Reachability note: every FR-003 writer (the global-mode Settings REST
// handler, a per-agent config write, the per-chat SessionModeStore.Set, the
// sysagent set_config/agent_apply_args validator, and boot-time config-load
// reconciliation) lives in pkg/agent and pkg/gateway. This function is
// exported so those writers can call it; wiring the call sites is outside
// pkg/audit, pkg/tools/shell_permission_mode*.go and
// pkg/security/approvalgrants*.go, the three locations this change is
// scoped to.
func EmitShellModeChange(
	ctx context.Context,
	logger *Logger,
	decision Decision,
	newMode, level string,
	actor ShellModeActor,
	agentID, sessionID, policyRule string,
) {
	_ = ctx
	EmitEntry(logger, &Entry{
		Event:      EventShellModeChange,
		Decision:   string(decision),
		AgentID:    agentID,
		SessionID:  sessionID,
		PolicyRule: policyRule,
		Details: map[string]any{
			"new_mode": newMode,
			"level":    level,
			"actor":    string(actor),
		},
	})
}

// EmitShellPreflightEscalation writes the FR-032(b) pre-flight-escalation
// event: an Auto-mode D7/D8 pre-flight found a reference the session's
// current grants do not cover and is about to escalate. matched is the
// rule/path/classifier match that tripped (SEC-17 explainability — also
// carried on Entry.PolicyRule); requested is the operation ("read"/"write")
// or the network access being asked for. command is the full command text
// (FR-046's redaction posture: Entry.Command already logs the full command
// text today, so this carries it at the same fidelity — no new redaction
// requirement).
func EmitShellPreflightEscalation(
	ctx context.Context,
	logger *Logger,
	kind ShellPreflightKind,
	agentID, sessionID, tool, command, matched, requested string,
) {
	_ = ctx
	EmitEntry(logger, &Entry{
		Event:      EventShellPreflightEscalation,
		AgentID:    agentID,
		SessionID:  sessionID,
		Tool:       tool,
		Command:    command,
		PolicyRule: matched,
		Details: map[string]any{
			"kind":      string(kind),
			"matched":   matched,
			"requested": requested,
		},
	})
}

// EmitShellGrantRecorded writes the FR-032(c) grant-recorded event. extra
// carries scope-specific fields (e.g. {"path":..., "access":...} for
// path_widening; {"arg_prefix":..., "run_in_background":...} for prefix) —
// merged into Details alongside the common scope/lifetime/resolved_binary
// triple. lifetime is always "session" (ADR-092 D4/D7/D8/FR-028: every
// grant kind dies with the session — there is no longer-lived grant scope).
func EmitShellGrantRecorded(
	ctx context.Context,
	logger *Logger,
	scope ShellGrantScope,
	resolvedBinary, agentID, sessionID, tool string,
	extra map[string]any,
) {
	_ = ctx
	details := map[string]any{
		"scope":           string(scope),
		"lifetime":        "session",
		"resolved_binary": resolvedBinary,
	}
	for k, v := range extra {
		details[k] = v
	}
	EmitEntry(logger, &Entry{
		Event:     EventShellGrantRecorded,
		Decision:  DecisionAllow,
		AgentID:   agentID,
		SessionID: sessionID,
		Tool:      tool,
		Details:   details,
	})
}

// EmitShellApprovalDecision writes the FR-032(d) per-call approval-decision
// event. kind identifies which ADR-092 decision point produced this call
// (e.g. "rule_ask", "fs_preflight", "fs_preflight_blind",
// "network_preflight"); reason is the denial reason (empty unless outcome
// is ShellApprovalDeny).
func EmitShellApprovalDecision(
	ctx context.Context,
	logger *Logger,
	outcome ShellApprovalOutcome,
	agentID, sessionID, tool, command, kind, reason string,
) {
	_ = ctx
	decision := DecisionAllow
	if outcome == ShellApprovalDeny {
		decision = DecisionDeny
	}
	details := map[string]any{
		"outcome": string(outcome),
		"kind":    kind,
	}
	if reason != "" {
		details["reason"] = reason
	}
	EmitEntry(logger, &Entry{
		Event:     EventShellApprovalDecision,
		Decision:  decision,
		AgentID:   agentID,
		SessionID: sessionID,
		Tool:      tool,
		Command:   command,
		Details:   details,
	})
}
