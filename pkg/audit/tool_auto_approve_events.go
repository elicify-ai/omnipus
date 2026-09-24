// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-092 (Auto-approve for tools other than bash) §5.5 typed audit event.
//
// docs/internal/specs/adr-092-auto-for-other-tools-design.md §5.5: "New
// event `tool.auto_approved`: A typed constant in `pkg/audit`, added to the
// event set in `audit.go`. Details: `{tool, agent_id, session_id, class,
// reason, paths}`. Emitted once for each call Auto ran without a prompt,
// through `audit.EmitEntry`, so a failed write shows up in the degraded
// count. Calls that were prompted or denied keep their existing
// `tool.policy.ask.*` rows." Details gained a `kernel_sandbox` bool
// [2026-09-24, founder decision]: once Auto stopped requiring an enforcing
// kernel sandbox (ADR-092 D1/J13, revised), this is the only per-call record
// of whether the kernel was actually confining the process this call ran
// (or spawned) under.
//
// This file supplies the constant and the Emit* helper. The call sites
// (the Auto verdict path in pkg/agent's loop_policy.go /
// loop_run_turn_tools.go, per the ADR-092 lane plan's L3) are out of scope
// for this file — pkg/audit does not import pkg/agent or pkg/tools.
package audit

import "context"

// EventToolAutoApproved — ADR-092 §5.5. Written once for every tool call
// that the Auto-approve toggle ran WITHOUT prompting a human: no
// tool.policy.ask.* row exists for that call, so this is the only audit
// trail an operator has for what Auto let through. A call that Auto instead
// routed to a prompt (a genuine ask-rule match, a RUNS-IF condition that
// failed, a destructive/unlabelled MCP tool, ...) keeps its existing
// tool.policy.ask.* event family and does NOT also emit this one.
const EventToolAutoApproved = "tool.auto_approved"

// EmitToolAutoApproved writes the ADR-092 §5.5 tool.auto_approved event.
//
//   - tool is the tool name Auto ran (e.g. "read_file", "send_message").
//   - class is the caller's AutoApproveClass verdict for this call, as a
//     string (e.g. "runs", "runs_if_args") — pkg/audit intentionally has no
//     dependency on pkg/tools' AutoApproveClass type, so the caller passes
//     its already-stringified classifier result.
//   - reason is a short, human-readable explanation of why THIS call
//     qualified (SEC-17 explainability; also carried on Entry.PolicyRule so
//     it is searchable the same way every other policy_rule value is).
//     Optional — omitted from Details when empty.
//   - paths is the set of filesystem paths (if any) the RUNS-IF check
//     resolved and approved for this call (ADR-092 §5.2's J2 workspace
//     rule) — e.g. a resolved read_file/write_file target. Optional —
//     omitted from Details when empty. Never includes file CONTENTS or any
//     other argument value: this event records what was approved and why,
//     not the payload the tool then acted on.
//   - kernelSandbox records whether a kernel sandbox (Landlock/Seatbelt) was
//     enforcing for spawned children at the moment this call was
//     auto-approved [2026-09-24, founder decision]: Auto no longer requires
//     an enforcing kernel sandbox (ADR-092 D1/J13, revised), so this call may
//     have run unconfined by the kernel. Recording it here — regardless of
//     that outcome — is how an operator finds every auto-approval that ran
//     without kernel confinement, after the fact.
//
// Decision is always DecisionAllow: this event exists only for calls Auto
// let through. A denial or a prompted call is recorded by the existing
// tool.policy.ask.* / tool.policy.deny.attempted events instead.
func EmitToolAutoApproved(
	ctx context.Context,
	logger *Logger,
	agentID, sessionID, tool, class, reason string,
	paths []string,
	kernelSandbox bool,
) {
	_ = ctx
	details := map[string]any{
		"tool":           tool,
		"class":          class,
		"kernel_sandbox": kernelSandbox,
	}
	if reason != "" {
		details["reason"] = reason
	}
	if len(paths) > 0 {
		details["paths"] = paths
	}
	EmitEntry(logger, &Entry{
		Event:      EventToolAutoApproved,
		Decision:   DecisionAllow,
		AgentID:    agentID,
		SessionID:  sessionID,
		Tool:       tool,
		PolicyRule: reason,
		Details:    details,
	})
}
