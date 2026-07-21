package policy

import (
	"log/slog"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/logger"
)
// AuditEntry represents a policy decision to be logged.
// Mirrors audit.Entry fields relevant to policy evaluation.
type AuditEntry struct {
	Timestamp  time.Time
	Event      string
	Decision   string
	AgentID    string
	SessionID  string
	Tool       string
	Command    string
	PolicyRule string
}

// AuditLogger is the interface that PolicyAuditor uses to write audit entries.
// Implemented by audit.Logger.
type AuditLogger interface {
	LogPolicyDecision(entry *AuditEntry) error
}

// PolicyAuditor wraps an Evaluator and automatically logs every policy decision
// to an audit logger (W-3: auto-logging of policy decisions).
type PolicyAuditor struct {
	eval      *Evaluator
	logger    AuditLogger
	sessionID string
}

// NewPolicyAuditor creates a PolicyAuditor that wraps eval and logs decisions to logger.
// Panics if eval is nil (wiring-time error). Logger may be nil (audit logging silently skipped).
func NewPolicyAuditor(eval *Evaluator, logger AuditLogger, sessionID string) *PolicyAuditor {
	if eval == nil {
		panic("PolicyAuditor: nil Evaluator")
	}
	return &PolicyAuditor{eval: eval, logger: logger, sessionID: sessionID}
}

// EvaluateTool checks a tool invocation and logs the decision.
func (pa *PolicyAuditor) EvaluateTool(agentID, toolName string, agentPolicy ...*AgentPolicy) Decision {
	d := pa.eval.EvaluateTool(agentID, toolName, agentPolicy...)
	pa.logDecision("tool_call", agentID, toolName, "", d)
	return d
}

// EvaluateExec checks an exec command and logs the decision.
func (pa *PolicyAuditor) EvaluateExec(agentID, command string) Decision {
	d := pa.eval.EvaluateExec(agentID, command)
	pa.logDecision("exec", agentID, "", command, d)
	return d
}

func (pa *PolicyAuditor) logDecision(event, agentID, tool, command string, d Decision) {
	if pa.logger == nil {
		return
	}
	decision := "allow"
	if !d.Allowed {
		decision = "deny"
	}
	if err := pa.logger.LogPolicyDecision(&AuditEntry{
		Timestamp:  time.Now().UTC(),
		Event:      event,
		Decision:   decision,
		AgentID:    agentID,
		SessionID:  pa.sessionID,
		Tool:       tool,
		Command:    command,
		PolicyRule: d.PolicyRule,
	}); err != nil {
		slog.Warn("PolicyAuditor: failed to log policy decision", "event", event, "error", err)
	}
}
