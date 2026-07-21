// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"github.com/dapicom-ai/omnipus/pkg/logger"
	"github.com/dapicom-ai/omnipus/pkg/audit"
	"github.com/dapicom-ai/omnipus/pkg/policy"
)

// auditBridge adapts an *audit.Logger to satisfy the policy.AuditLogger interface.
// This bridge exists because the audit package cannot import the policy package
// (to avoid circular dependencies). The agent package imports both and provides
// this thin adapter at the wiring point.
type auditBridge struct {
	logger *audit.Logger
}

// newAuditBridge creates an auditBridge. Panics if logger is nil — this is a
// wiring-time error that should be caught immediately, not a runtime nil-pointer
// panic deep in LogPolicyDecision.
func newAuditBridge(logger *audit.Logger) *auditBridge {
	if logger == nil {
		panic("audit_bridge: nil audit.Logger")
	}
	return &auditBridge{logger: logger}
}

// Compile-time check: auditBridge implements policy.AuditLogger.
var _ policy.AuditLogger = (*auditBridge)(nil)

// LogPolicyDecision converts a policy.AuditEntry into an audit.Entry and writes it
// to the audit log. This bridges SEC-15 (structured audit logging) with SEC-17
// (explainable policy decisions) so every policy evaluation produces an audit record.
func (b *auditBridge) LogPolicyDecision(entry *policy.AuditEntry) error {
	return b.logger.Log(&audit.Entry{
		Timestamp:  entry.Timestamp,
		Event:      entry.Event,
		Decision:   entry.Decision,
		AgentID:    entry.AgentID,
		SessionID:  entry.SessionID,
		Tool:       entry.Tool,
		Command:    entry.Command,
		PolicyRule: entry.PolicyRule,
	})
}
