// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package policy_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/policy"
)

// mockAuditLogger records policy decisions for test verification.
type mockAuditLogger struct {
	entries []*policy.AuditEntry
}

func (m *mockAuditLogger) LogPolicyDecision(entry *policy.AuditEntry) error {
	m.entries = append(m.entries, entry)
	return nil
}

// TestPolicyAuditor_EvaluateExec_LogsDecision verifies that exec policy
// evaluation is logged with the command field populated (SEC-05, SEC-17).
func TestPolicyAuditor_EvaluateExec_LogsDecision(t *testing.T) {
	cfg := &policy.SecurityConfig{
		DefaultPolicy: policy.PolicyDeny,
		Policy: policy.PolicySection{
			Exec: policy.ExecPolicy{
				AllowedBinaries: []string{"git *"},
			},
		},
	}
	evaluator := policy.NewEvaluator(cfg)
	logger := &mockAuditLogger{}
	auditor := policy.NewPolicyAuditor(evaluator, logger, "sess-789")

	t.Run("allowed exec is logged", func(t *testing.T) {
		decision := auditor.EvaluateExec("researcher", "git status")
		assert.True(t, decision.Allowed)

		require.Len(t, logger.entries, 1)
		entry := logger.entries[0]
		assert.Equal(t, "exec", entry.Event)
		assert.Equal(t, "allow", entry.Decision)
		assert.Equal(t, "git status", entry.Command)
		assert.NotEmpty(t, entry.PolicyRule)
	})

	t.Run("denied exec is logged", func(t *testing.T) {
		logger.entries = nil // reset
		decision := auditor.EvaluateExec("researcher", "rm -rf /")
		assert.False(t, decision.Allowed)

		require.Len(t, logger.entries, 1)
		entry := logger.entries[0]
		assert.Equal(t, "exec", entry.Event)
		assert.Equal(t, "deny", entry.Decision)
		assert.Equal(t, "rm -rf /", entry.Command)
		assert.NotEmpty(t, entry.PolicyRule)
	})
}

// TestPolicyAuditor_NilLogger_NoPanic verifies that a PolicyAuditor with a nil
// logger does not panic and still returns correct decisions.
func TestPolicyAuditor_NilLogger_NoPanic(t *testing.T) {
	evaluator := policy.NewEvaluator(nil) // deny-by-default
	auditor := policy.NewPolicyAuditor(evaluator, nil, "")

	decision := auditor.EvaluateExec("agent", "cmd")
	assert.False(t, decision.Allowed, "should deny exec with nil evaluator config")
}
