// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

// authoring_audit_bridge.go — how a knowledge mutation reaches the operator's
// real audit log (FR-090, ADR-067 D19, US-15).
//
// # The wiring defect this closes
//
// AuthoringDeps.Audit is an interface, and nil means "record nothing". The
// tools still work and still refuse when it is nil — they simply leave no
// trace. That is the shape of a control that can be lost by omission: whoever
// constructs the tools is one forgotten struct field away from an entirely
// unaudited write path into the operator's own notes, with nothing at compile
// time, boot time or test time to say so. And for the whole of ADR-067's
// stage 3 there WAS no construction site at all, so the field was never
// filled in anywhere.
//
// Two things fix that, and both live here:
//
//  1. A REAL SINK. NewAuthorAuditLogger adapts an *audit.Logger — the same
//     logger every other audited surface in the process writes to — onto
//     AuthorAudit, so the record lands in $OMNIPUS_HOME/audit/ alongside
//     tool_call, exec and the library.* mutations rather than in a
//     package-local shape nobody reads.
//
//  2. A PROPAGATION PATH THAT CANNOT BE FORGOTTEN. Each authoring tool
//     implements SetAuditLogger, which is pkg/tools' auditLoggerAware
//     contract. The agent loop already walks every registered tool and calls
//     it (AgentLoop.wireMemoryAuditLoggerOn → ToolRegistry.SetAuditLogger), so
//     registering an authoring tool is by itself enough to audit it — there is
//     no second step for a caller to miss.
//
// # Why this does NOT route through Auditor.Record
//
// Auditor.Record (audit.go) validates that a record names a collection AND at
// least one path, and refuses one that does not. That is right for the
// lower-layer records it was written for: author.go and rename.go only ever
// audit after a collection has been opened and a path resolved.
//
// The TOOL layer's most important records are exactly the ones that fail that
// validation. A call refused because the collection is not in the caller's
// workspace has no collection root and no paths — that is WHY it was refused —
// and it is precisely the event an operator needs to see (an agent reaching
// for a knowledge base it may not address). Passing it through Auditor.Record
// would drop it, leaving the audit log complete-looking and silent about the
// one class of refusal that matters most. So this bridge writes the entry
// itself, carrying whatever is known and simply omitting what is not. It
// reuses audit.go's own sanitising helpers so the leak bounds are identical.

import (
	"log/slog"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/audit"
)

// authorAuditLogger adapts an *audit.Logger onto AuthorAudit.
type authorAuditLogger struct {
	logger *audit.Logger
}

// NewAuthorAuditLogger returns an AuthorAudit that writes to logger, falling
// back to the structured log when there is no audit logger at all.
//
// # Why nil does not mean "no sink" here
//
// `sandbox.audit_log` is FALSE on a default install — nothing in
// pkg/config/defaults.go seeds it true — so pkg/agent's al.auditLogger is nil
// on a fresh gateway and ToolRegistry.SetAuditLogger is never called. If this
// returned nil there, every authoring tool would hold a nil Audit, and
// AuthoringDeps.begin refuses outright on a nil Audit (correctly: FR-090 says
// a mutation with no record must not happen). The result would be seven tools
// that are registered, catalogued, seeded "allow"/"ask" — and that refuse
// every call on the default configuration. That is the same class of defect as
// the unregistered tools this wiring exists to fix, just one layer along.
//
// ADR-067 D19 settles which way to resolve it: knowledge-base mutations "must
// not be exempt" from the audit record, and the record is required by FR-090
// regardless of whether the OPTIONAL security audit log is switched on. So a
// sink always exists. With an audit logger it is the real one, writing into
// $OMNIPUS_HOME/system/ alongside every other audited event; without one it is
// the process's structured log, which is weaker but is a record.
//
// The nil-Audit refusal in begin still stands and still means what it says: it
// catches a host that constructs AuthoringDeps by hand and forgets the field.
// It can no longer be reached by the ordinary registration path, which is the
// point — a wiring defect should be impossible, not merely reported.
func NewAuthorAuditLogger(logger *audit.Logger) AuthorAudit {
	if logger == nil {
		return authorAuditSlog{}
	}
	return authorAuditLogger{logger: logger}
}

// authorAuditSlog is the fallback record when no audit.Logger exists.
//
// It writes at INFO for an applied mutation and WARN for a refusal, because a
// refusal is the event an operator is looking for after the fact (US-15) and
// must not be filtered out with routine chatter. It carries the same fields as
// the real entry and, like it, never carries note content.
type authorAuditSlog struct{}

// RecordKnowledgeWrite implements AuthorAudit.
func (authorAuditSlog) RecordKnowledgeWrite(rec AuthorAuditRecord) {
	attrs := []any{
		"event", string(rec.Operation),
		"outcome", string(rec.Outcome),
		"agent_id", strings.TrimSpace(rec.AgentID),
		"workspace_id", strings.TrimSpace(rec.WorkspaceID),
		"collection", strings.TrimSpace(rec.Root),
		"paths", normalizeAuditPaths(rec.Paths),
	}
	if reason := truncateAuditValue(strings.TrimSpace(rec.Reason), auditReasonMax); reason != "" {
		attrs = append(attrs, "reason", reason)
	}
	if rec.Outcome == AuthorOutcomeApplied {
		slog.Info("knowledge: mutation", attrs...)
		return
	}
	slog.Warn("knowledge: mutation refused", attrs...)
}

// decision maps an authoring outcome onto pkg/audit's Decision vocabulary, so
// an existing audit query filtering on decision keeps working over these rows.
func (o AuthorOutcome) decision() string {
	if o == AuthorOutcomeApplied {
		return audit.DecisionAllow
	}
	return audit.DecisionDeny
}

// RecordKnowledgeWrite implements AuthorAudit.
//
// It never returns an error and never blocks the mutation: the write has
// already happened or already been refused by the time this is called, and
// failing the operation because its record could not be written would turn a
// logging fault into data loss. A sink failure is surfaced by audit's own
// error path.
func (a authorAuditLogger) RecordKnowledgeWrite(rec AuthorAuditRecord) {
	details := map[string]any{
		detailKeyOutcome: string(rec.Outcome),
	}
	if root := strings.TrimSpace(rec.Root); root != "" {
		details[detailKeyCollection] = root
	}
	if name := strings.TrimSpace(rec.Collection); name != "" {
		details["collection_name"] = truncateAuditValue(name, auditDetailValueMax)
	}
	if paths := normalizeAuditPaths(rec.Paths); len(paths) > 0 {
		details[detailKeyPaths] = paths
		details[detailKeyPathCount] = len(paths)
	}
	if reason := truncateAuditValue(strings.TrimSpace(rec.Reason), auditReasonMax); reason != "" {
		details[detailKeyReason] = reason
	}
	if ws := strings.TrimSpace(rec.WorkspaceID); ws != "" {
		details[detailKeyWorkspace] = ws
	}
	if actor := strings.TrimSpace(rec.AgentID); actor != "" {
		details[detailKeyActor] = actor
	}

	entry := &audit.Entry{
		Timestamp: rec.At,
		Event:     string(rec.Operation),
		Decision:  rec.Outcome.decision(),
		AgentID:   strings.TrimSpace(rec.AgentID),
		Details:   details,
	}
	// audit.Logger.Log is nil-receiver safe and returns nil when disabled.
	_ = a.logger.Log(entry)
}

// --- auditLoggerAware, for every authoring tool ------------------------------
//
// pkg/tools' ToolRegistry.SetAuditLogger calls these on every registered tool
// that implements them. Implementing it on each tool rather than asking the
// registration site to build a sink is what makes the audit path impossible to
// forget: a tool that is registered is a tool that is audited.

// SetAuditLogger implements pkg/tools' auditLoggerAware.
func (t *CreateTool) SetAuditLogger(l *audit.Logger) { t.deps.Audit = NewAuthorAuditLogger(l) }

// SetAuditLogger implements pkg/tools' auditLoggerAware.
func (t *LinkTool) SetAuditLogger(l *audit.Logger) { t.deps.Audit = NewAuthorAuditLogger(l) }

// SetAuditLogger implements pkg/tools' auditLoggerAware.
func (t *SetPropertyTool) SetAuditLogger(l *audit.Logger) { t.deps.Audit = NewAuthorAuditLogger(l) }

// SetAuditLogger implements pkg/tools' auditLoggerAware.
func (t *AppendSectionTool) SetAuditLogger(l *audit.Logger) { t.deps.Audit = NewAuthorAuditLogger(l) }

// SetAuditLogger implements pkg/tools' auditLoggerAware.
func (t *TasksTool) SetAuditLogger(l *audit.Logger) { t.deps.Audit = NewAuthorAuditLogger(l) }

// SetAuditLogger implements pkg/tools' auditLoggerAware.
func (t *MoveTool) SetAuditLogger(l *audit.Logger) { t.deps.Audit = NewAuthorAuditLogger(l) }

// SetAuditLogger implements pkg/tools' auditLoggerAware.
func (t *RenameTool) SetAuditLogger(l *audit.Logger) { t.deps.Audit = NewAuthorAuditLogger(l) }
