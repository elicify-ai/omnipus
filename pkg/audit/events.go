// Package audit — Tool Registry Redesign (Wave A2) audit event types.
//
// This file declares the audit event-name constants, severity constants,
// reason enum, and lightweight emitter helpers required by the Central
// Tool Registry redesign spec (`docs/specs/tool-registry-redesign-spec.md`,
// revision 6). The events listed below are referenced by FRs:
//
//	FR-011, FR-038, FR-047, FR-049, FR-051, FR-054, FR-057, FR-060,
//	FR-063, FR-066, FR-074, FR-080, FR-083 — and the spec's
//	"Audit & Observability" table.
//
// Every emitter in this file is non-blocking and best-effort: emission
// failure is logged via slog but never bubbled up to the caller, except
// the boot-abort path (FR-063) which uses a stderr fallback before exit
// (see boot_abort.go).

package audit

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"
)

// ---------------------------------------------------------------------------
// Event-name constants (FR-038, FR-047, FR-051, FR-054, FR-066, FR-074, FR-083).
//
// These names are part of the audit wire contract: log shippers and SIEM
// rules grep on them. Renaming any constant here is a breaking change.
// ---------------------------------------------------------------------------

const (
	// EventToolPolicyDenyAttempted — WARN. LLM emitted a tool_call for a tool
	// whose effective policy is `deny`. Reachable only via stale model state
	// (e.g. mid-turn policy change). Spec table; FR-079.
	EventToolPolicyDenyAttempted = "tool.policy.deny.attempted"

	// EventToolPolicyAskRequested — INFO. Approval event emitted (loop paused
	// awaiting human-in-the-loop). FR-011, FR-074.
	EventToolPolicyAskRequested = "tool.policy.ask.requested"

	// EventToolPolicyAskGranted — INFO. User approved an ask call. FR-054, FR-074.
	EventToolPolicyAskGranted = "tool.policy.ask.granted"

	// EventToolPolicyAskDenied — INFO. User denied an ask call OR a system-deny
	// path fired (timeout, cancel, restart, saturated, batch_short_circuit).
	// FR-047, FR-054, FR-074, FR-065 (combined event for batch short-circuit).
	EventToolPolicyAskDenied = "tool.policy.ask.denied"

	// EventToolCollisionMCPRejected — WARN. Central registry refused an MCP
	// registration because of a name collision with a builtin or a
	// previously-registered MCP server. FR-034, FR-060.
	EventToolCollisionMCPRejected = "tool.collision.mcp_rejected"

	// EventAgentConfigCorrupt — HIGH. Boot-time validator could not parse or
	// read a particular agent.json. FR-023.
	EventAgentConfigCorrupt = "agent.config.corrupt"

	// EventAgentConfigInvalidPolicyValue — HIGH. Boot-time validator rejected
	// a default_policy / per-tool policy / GlobalDefaultPolicy value that
	// is outside `{"allow", "ask", "deny"}`. Includes empty strings (FR-085
	// supersedes the legacy `agent.config.empty_policy_value_coerced`).
	// FR-049, FR-085.
	EventAgentConfigInvalidPolicyValue = "agent.config.invalid_policy_value"

	// EventAgentConfigUnknownToolInPolicy — WARN. Boot-time validator found
	// a per-agent policy entry referring to an unregistered tool name. FR-057.
	EventAgentConfigUnknownToolInPolicy = "agent.config.unknown_tool_in_policy"

	// EventToolAssemblyDuplicateName — HIGH. The final dedup pass during
	// `tools[]` assembly observed two registry entries with the same name —
	// an invariant violation. FR-066.
	EventToolAssemblyDuplicateName = "tool.assembly.duplicate_name"

	// EventMCPServerRenamed — HIGH. A reload detected an MCP server config
	// rename (transport+endpoint identical, name changed). Old entries
	// evicted, new entries added. FR-051, FR-068, FR-083.
	EventMCPServerRenamed = "mcp.server.renamed"

	// EventGatewayStartupGuardDisabled — WARN. Operator booted with
	// `gateway.tool_approval_max_pending=0` (sentinel "unlimited"); DoS risk
	// flagged for visibility. FR-016.
	EventGatewayStartupGuardDisabled = "gateway.startup.guard_disabled"

	// EventGatewayConfigInvalidValue — HIGH. Operator booted with a negative
	// `gateway.tool_approval_max_pending` or another invalid gateway config
	// value. The gateway exits non-zero after emitting this event. FR-016.
	EventGatewayConfigInvalidValue = "gateway.config.invalid_value"

	// EventTurnAbortedSyntheticLoop — WARN. The loop aborted a turn after
	// observing N consecutive synthetic-deny tool results (default N=8;
	// `gateway.turn_synthetic_error_floor`). FR-084.
	EventTurnAbortedSyntheticLoop = "turn.aborted_synthetic_loop"

	// EventApproverFallback — HIGH. The agent loop hit `nopPolicyApprover`
	// in a default (production) build, meaning `SetToolApprover` was never
	// called and an `ask`-policy tool would be denied with reason
	// "no_approver_configured". This event signals that the approval gate
	// is mis-wired and ANY ask-policy tool — including admin-flagged ones —
	// is being failed-closed in production.
	//
	// Emitted at most once per process via sync.Once: the first hit is the
	// diagnostic signal, subsequent denies are repeated by definition and
	// would flood the audit log if a misconfigured deployment kept calling
	// ask-policy tools. Closes V2.B silent-failure-hunter BE CRIT-1.
	EventApproverFallback = "approver.fallback"

	// EventTurnCancelAttempt — INFO. A cancel request arrived for a session.
	// Emitted for every attempt including duplicates and no-op cancels (FR-10,
	// FR-11). The was_fired field indicates whether this attempt triggered an
	// actual interrupt or was a no-op (e.g. turn already finished).
	EventTurnCancelAttempt = "turn.cancel.attempt"

	// EventTurnCancelled — INFO. A canceled turn has fully exited and the
	// transcript has been marked. Contains the cancel_method ("graceful" or
	// "hard") and the list of descendant turn IDs that were also canceled
	// (FR-15, FR-17, FR-18).
	EventTurnCancelled = "turn.cancelled"

	// EventTurnCancelStuck — WARN. The turn goroutine did not exit within
	// 5 seconds after the hard-abort signal was sent. The turn has been
	// detached (abandoned=true) and the gateway will stop waiting for it
	// (FR-19, FR-20, FR-21).
	EventTurnCancelStuck = "turn.cancel.stuck"

	// EventCancelAbusePattern — WARN. A single canceller (user + channel)
	// sent >= 10 cancel requests within 60 seconds, suggesting runaway client
	// logic or intentional abuse (FR-25a).
	EventCancelAbusePattern = "cancel.abuse_pattern"
)

// ---------------------------------------------------------------------------
// Severity constants. These are NOT slog levels — they are Omnipus audit
// severities, used as a top-level field on each emitted JSONL record so
// SIEM rules can route on `.severity` without inferring from the event name.
// ---------------------------------------------------------------------------

// Severity is the Omnipus audit severity classification.
type Severity string

const (
	// SeverityInfo — routine, expected events that operators want a record of.
	SeverityInfo Severity = "INFO"

	// SeverityWarn — unexpected but recoverable; operator attention recommended.
	SeverityWarn Severity = "WARN"

	// SeverityHigh — security-relevant or correctness-critical; operator
	// attention required. Includes any path that aborts boot.
	SeverityHigh Severity = "HIGH"
)

// ---------------------------------------------------------------------------
// Ask-deny reason enum (FR-047, FR-065). Exhaustive list — any caller that
// emits `tool.policy.ask.denied` MUST use one of these constants. The
// helper `IsValidAskDenyReason` is exposed for boundary validation.
// ---------------------------------------------------------------------------

// AskDenyReason is the discriminator on `tool.policy.ask.denied` events.
type AskDenyReason string

const (
	// AskDenyReasonUser — user clicked "deny" in the SPA approval modal.
	AskDenyReasonUser AskDenyReason = "user"

	// AskDenyReasonTimeout — `gateway.tool_approval_timeout` elapsed before
	// any approve/deny/cancel action arrived.
	AskDenyReasonTimeout AskDenyReason = "timeout"

	// AskDenyReasonCancel — user (or another client) clicked "cancel"
	// (third action on the approval endpoint, FR-017).
	AskDenyReasonCancel AskDenyReason = "cancel"

	// AskDenyReasonRestart — the gateway restarted while the approval was
	// pending (FR-013). Emitted from the next-boot recovery path AND the
	// graceful-shutdown path (FR-048, FR-069).
	AskDenyReasonRestart AskDenyReason = "restart"

	// AskDenyReasonSaturated — the pending-approval cap
	// (`gateway.tool_approval_max_pending`) was reached; the new ask was
	// synthetically denied without ever emitting a WS approval event.
	// FR-016.
	AskDenyReasonSaturated AskDenyReason = "saturated"

	// AskDenyReasonBatchShortCircuit — a prior call in the same sequential
	// ask batch was denied or canceled, so this and every subsequent
	// sibling call is auto-denied. FR-065.
	AskDenyReasonBatchShortCircuit AskDenyReason = "batch_short_circuit"
)

// IsValidAskDenyReason reports whether `r` is one of the six enum values
// defined by FR-047 + FR-065. Useful at API boundaries before logging.
func IsValidAskDenyReason(r AskDenyReason) bool {
	switch r {
	case AskDenyReasonUser,
		AskDenyReasonTimeout,
		AskDenyReasonCancel,
		AskDenyReasonRestart,
		AskDenyReasonSaturated,
		AskDenyReasonBatchShortCircuit:
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// `conflict_with` enum for `tool.collision.mcp_rejected` (FR-034, FR-060).
// ---------------------------------------------------------------------------

const (
	// ConflictWithBuiltin — incoming MCP tool collided with an existing
	// builtin name; builtin wins (FR-034).
	ConflictWithBuiltin = "builtin"

	// ConflictWithReservedPrefix — incoming MCP tool name begins with
	// `system.`; the central registry reserves that prefix exclusively
	// for builtins (FR-015, FR-060).
	ConflictWithReservedPrefix = "reserved_prefix"
	// ConflictWithMCPPrefix is the prefix for the discriminator value when
	// the conflict is with another MCP server. The full value is
	// `mcp:<server_id>` (e.g. `mcp:srv-A`). The prefix exists so SIEM rules
	// can match all MCP-vs-MCP collisions with one regex.
	ConflictWithMCPPrefix = "mcp:"
)

// ---------------------------------------------------------------------------
// Generic emitter for the new structured-record events.
//
// Every event type here writes a flat JSONL record to the audit logger
// using the same wire shape:
//
//	{
//	  "timestamp": "<RFC3339Nano UTC>",
//	  "event":     "<EventXxx>",
//	  "severity":  "<Severity>",
//	  "fields":    { ... event-specific ... }
//	}
//
// We do NOT reuse the `Entry` struct because the Tool Registry events have
// substantially different field sets from the existing tool_call/exec
// schema, and forcing them through `Parameters`/`Details` would discard
// type information that downstream consumers need (e.g. `args_hash`,
// `canceled_tool_call_ids`, `latency_ms`).
//
// Best-effort contract: emission failure logs to slog and returns nil.
// The audit subsystem MUST NOT block tool execution. Boot-abort paths
// (FR-063) use the dedicated stderr fallback in boot_abort.go.
// ---------------------------------------------------------------------------

// Record is the canonical wire shape for Tool Registry redesign audit
// events. `Fields` carries the per-event-type payload defined by the spec.
type Record struct {
	Timestamp string         `json:"timestamp"`
	Event     string         `json:"event"`
	Severity  Severity       `json:"severity"`
	Fields    map[string]any `json:"fields,omitempty"`
}

// Emit writes one Record to the audit logger. `fields` is shallow-copied
// into the record so subsequent caller mutation cannot corrupt the
// already-flushed JSON. logger == nil is a no-op (audit disabled).
//
// `ctx` is reserved for future actor-extraction; today the function does
// not consult it (the events emitted here either have a synthetic / system
// actor, or carry the user-id explicitly in fields).
func Emit(ctx context.Context, logger *Logger, event string, sev Severity, fields map[string]any) {
	_ = ctx
	if logger == nil {
		return
	}
	if !IsValidSeverity(sev) {
		slog.Warn("audit: invalid severity, defaulting to WARN", "event", event, "severity", string(sev))
		sev = SeverityWarn
	}

	rec := Record{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Event:     event,
		Severity:  sev,
		Fields:    cloneFields(fields),
	}
	data, err := json.Marshal(rec)
	if err != nil {
		slog.Error("audit: marshal record failed", "error", err, "event", event)
		return
	}
	// CRIT-5: structured Record emissions inherit the same fsync gate as
	// Logger.Log — High-severity events and the policy-deny event names go
	// to disk synchronously so they survive a crash. INFO/WARN allow rows
	// batch through bufio.
	fsyncRequired := sev == SeverityHigh ||
		event == EventToolPolicyDenyAttempted ||
		event == EventToolPolicyAskDenied ||
		event == EventBootAbort
	if writeErr := logger.writeLine(data, fsyncRequired); writeErr != nil {
		slog.Error("audit: write record failed", "error", writeErr, "event", event)
	}
}

// IsValidSeverity reports whether `s` is one of the three declared severities.
func IsValidSeverity(s Severity) bool {
	switch s {
	case SeverityInfo, SeverityWarn, SeverityHigh:
		return true
	}
	return false
}

// cloneFields returns a shallow copy of m so the caller cannot mutate the
// emitted record after Emit returns. Returns nil for nil input (omits the
// `fields` JSON key in that case).
func cloneFields(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// ---------------------------------------------------------------------------
// Typed convenience emitters. Each of the 13 events listed above has a
// dedicated function that enforces the field contract from the spec
// at compile time. Call sites SHOULD prefer these over raw Emit() so that
// renaming a field is a single-edit operation.
// ---------------------------------------------------------------------------

// EmitToolPolicyDenyAttempted — WARN. FR-079; spec table.
//
// `note` is optional; the spec mandates the literal string "mid_turn_policy_change"
// when the re-check (FR-079) observes a flip from the filter snapshot.
func EmitToolPolicyDenyAttempted(
	ctx context.Context,
	logger *Logger,
	agentID, toolName, source, sessionID, turnID, toolCallID, note string,
) {
	fields := map[string]any{
		"agent_id":     agentID,
		"tool_name":    toolName,
		"source":       source, // "global" | "agent"
		"session_id":   sessionID,
		"turn_id":      turnID,
		"tool_call_id": toolCallID,
	}
	if note != "" {
		fields["note"] = note
	}
	Emit(ctx, logger, EventToolPolicyDenyAttempted, SeverityWarn, fields)
}

// EmitToolPolicyAskRequested — INFO. FR-011, FR-074, FR-080.
//
// `args` is hashed via ArgsHash and previewed via ArgsPreview before
// emission so callers do not have to remember to redact.
func EmitToolPolicyAskRequested(
	ctx context.Context,
	logger *Logger,
	approvalID, toolCallID, toolName, agentID, sessionID, turnID string,
	args map[string]any,
) {
	hash, _ := ArgsHash(args)
	fields := map[string]any{
		"approval_id":  approvalID,
		"tool_call_id": toolCallID,
		"tool_name":    toolName,
		"agent_id":     agentID,
		"session_id":   sessionID,
		"turn_id":      turnID,
		"args_hash":    hash,
		"args_preview": ArgsPreview(args),
	}
	Emit(ctx, logger, EventToolPolicyAskRequested, SeverityInfo, fields)
}

// EmitToolPolicyAskGranted — INFO. FR-054, FR-074.
func EmitToolPolicyAskGranted(
	ctx context.Context,
	logger *Logger,
	approvalID, approverUserID, toolName, agentID, sessionID, turnID string,
	latencyMS int64,
	argsHash string,
) {
	fields := map[string]any{
		"approval_id":      approvalID,
		"approver_user_id": approverUserID,
		"tool_name":        toolName,
		"agent_id":         agentID,
		"session_id":       sessionID,
		"turn_id":          turnID,
		"latency_ms":       latencyMS,
		"args_hash":        argsHash,
	}
	Emit(ctx, logger, EventToolPolicyAskGranted, SeverityInfo, fields)
}

// EmitToolPolicyAskDenied — INFO. FR-047, FR-054, FR-074, FR-065.
//
// For batch short-circuit denies (FR-065) the caller passes the list of
// canceled tool_call_ids in `cancelledToolCallIDs`; pass nil for non-batch
// denies. `approverUserID` is "" for system-deny paths (timeout, restart,
// saturated, batch_short_circuit).
func EmitToolPolicyAskDenied(
	ctx context.Context,
	logger *Logger,
	approvalID, approverUserID, toolName, agentID, sessionID, turnID string,
	reason AskDenyReason,
	argsHash string,
	cancelledToolCallIDs []string,
) {
	if !IsValidAskDenyReason(reason) {
		slog.Error("audit: invalid AskDenyReason, refusing to emit",
			"approval_id", approvalID, "reason", string(reason))
		return
	}
	fields := map[string]any{
		"approval_id":      approvalID,
		"approver_user_id": approverUserID,
		"tool_name":        toolName,
		"agent_id":         agentID,
		"session_id":       sessionID,
		"turn_id":          turnID,
		"reason":           string(reason),
		"args_hash":        argsHash,
	}
	if len(cancelledToolCallIDs) > 0 {
		fields["canceled_tool_call_ids"] = cancelledToolCallIDs
	}
	Emit(ctx, logger, EventToolPolicyAskDenied, SeverityInfo, fields)
}

// EmitToolCollisionMCPRejected — WARN. FR-034, FR-060.
//
// `conflictWith` is one of the ConflictWith* constants above, OR
// `ConflictWithMCPPrefix + "<server_id>"` for MCP-vs-MCP collisions.
func EmitToolCollisionMCPRejected(ctx context.Context, logger *Logger, mcpServerID, toolName, conflictWith string) {
	Emit(ctx, logger, EventToolCollisionMCPRejected, SeverityWarn, map[string]any{
		"mcp_server_id": mcpServerID,
		"tool_name":     toolName,
		"conflict_with": conflictWith,
	})
}

// EmitAgentConfigCorrupt — HIGH. FR-023.
//
// `agentType` is one of {"core", "custom"}. The boot validator decides
// the skip-or-abort disposition by consulting the constructor-seed
// disposition map (FR-062), not by `agentType` alone.
func EmitAgentConfigCorrupt(ctx context.Context, logger *Logger, agentID, agentType, path string, err error) {
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}
	Emit(ctx, logger, EventAgentConfigCorrupt, SeverityHigh, map[string]any{
		"agent_id":   agentID,
		"agent_type": agentType,
		"path":       path,
		"error":      errMsg,
	})
}

// EmitAgentConfigInvalidPolicyValue — HIGH. FR-049, FR-085.
//
// `entries` lists each invalid entry (e.g. `default_policy="banana"`);
// the slice form is preserved so multiple invalid values in one config
// emit one event, not N.
func EmitAgentConfigInvalidPolicyValue(
	ctx context.Context,
	logger *Logger,
	agentID, agentType, path string,
	entries []InvalidPolicyEntry,
) {
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		out = append(out, map[string]any{
			"field":  e.Field,
			"value":  e.Value,
			"reason": e.Reason,
		})
	}
	Emit(ctx, logger, EventAgentConfigInvalidPolicyValue, SeverityHigh, map[string]any{
		"agent_id":   agentID,
		"agent_type": agentType,
		"path":       path,
		"entries":    out,
	})
}

// InvalidPolicyEntry describes one bad value found by the boot validator.
// `Field` is a JSON-pointer-ish path (e.g. `default_policy`,
// `policies["system.config.set"]`); `Value` is the raw string the operator
// wrote; `Reason` is a short human explanation.
type InvalidPolicyEntry struct {
	Field  string
	Value  string
	Reason string
}

// EmitAgentConfigUnknownToolInPolicy — WARN. FR-057.
func EmitAgentConfigUnknownToolInPolicy(ctx context.Context, logger *Logger, agentID, path string, toolNames []string) {
	Emit(ctx, logger, EventAgentConfigUnknownToolInPolicy, SeverityWarn, map[string]any{
		"agent_id":   agentID,
		"path":       path,
		"tool_names": toolNames,
	})
}

// EmitToolAssemblyDuplicateName — HIGH. FR-066.
//
// `sources` is the ordered list of sources observed for the colliding name,
// e.g. ["builtin", "mcp:srv-A"]. `kept` is the source whose entry survived
// the dedup pass per FR-034 precedence (builtin > first-MCP).
func EmitToolAssemblyDuplicateName(
	ctx context.Context,
	logger *Logger,
	toolName string,
	sources []string,
	kept string,
) {
	Emit(ctx, logger, EventToolAssemblyDuplicateName, SeverityHigh, map[string]any{
		"tool_name": toolName,
		"sources":   sources,
		"kept":      kept,
	})
}

// EmitMCPServerRenamed — HIGH. FR-051, FR-068, FR-083.
func EmitMCPServerRenamed(ctx context.Context, logger *Logger, oldName, newName, transportType, endpointURL string) {
	Emit(ctx, logger, EventMCPServerRenamed, SeverityHigh, map[string]any{
		"old_name":       oldName,
		"new_name":       newName,
		"transport_type": transportType,
		"endpoint_url":   endpointURL,
	})
}

// EmitGatewayStartupGuardDisabled — WARN. FR-016.
//
// Emitted exactly once at boot when `gateway.tool_approval_max_pending == 0`
// (sentinel "unlimited").
func EmitGatewayStartupGuardDisabled(ctx context.Context, logger *Logger, configKey string) {
	Emit(ctx, logger, EventGatewayStartupGuardDisabled, SeverityWarn, map[string]any{
		"config_key": configKey,
		"message":    "approval saturation guard disabled — DoS risk",
	})
}

// EmitGatewayConfigInvalidValue — HIGH. FR-016.
//
// Emitted when boot validation observes a negative cap or another invalid
// gateway config value. The gateway exits non-zero immediately after.
func EmitGatewayConfigInvalidValue(ctx context.Context, logger *Logger, configKey, value, reason string) {
	Emit(ctx, logger, EventGatewayConfigInvalidValue, SeverityHigh, map[string]any{
		"config_key": configKey,
		"value":      value,
		"reason":     reason,
	})
}

// EmitTurnAbortedSyntheticLoop — WARN. FR-084.
func EmitTurnAbortedSyntheticLoop(
	ctx context.Context,
	logger *Logger,
	agentID, sessionID, turnID string,
	syntheticErrorCount int,
) {
	Emit(ctx, logger, EventTurnAbortedSyntheticLoop, SeverityWarn, map[string]any{
		"agent_id":              agentID,
		"session_id":            sessionID,
		"turn_id":               turnID,
		"synthetic_error_count": syntheticErrorCount,
	})
}
