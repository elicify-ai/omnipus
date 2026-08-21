// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package security_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/security"
)

// TestE2E_AgentToolDenied: REMOVED (#70). Exercised policy.Evaluator.EvaluateTool,
// which was never the live tool-policy authority — see pkg/policy/evaluator.go's
// prior SCOPE (#438) note. The real deny+audit path is exercised by
// pkg/policy/auditor_test.go's EvaluateExec tests and the agent loop's own audit
// wiring.
// Traces to: wave2-security-layer-spec.md line 813 (TestE2E_AgentToolDenied)

// TestE2E_RateLimitTriggered is an end-to-end test: agent hits rate limit →
// receives retry_after → audit entry written with decision: "deny".
// Traces to: wave2-security-layer-spec.md line 815 (TestE2E_RateLimitTriggered)
// BDD: Full rate limit rejection (spec line 689)
func TestE2E_RateLimitTriggered(t *testing.T) {
	dir := t.TempDir()
	auditLogger, err := audit.NewLogger(audit.LoggerConfig{
		Dir:           dir,
		RetentionDays: 90,
	})
	require.NoError(t, err)
	// Test cleanup: Close error is inconsequential — t.TempDir()
	// removes the backing directory regardless.
	defer func() {
		if err := auditLogger.Close(); err != nil {
			_ = err
		}
	}()

	sw := security.NewSlidingWindow(3, time.Minute, security.ScopeAgent, "researcher", "llm_calls")

	// Exhaust the limit
	for i := 0; i < 3; i++ {
		sw.Allow()
	}

	// 4th call should be rejected
	limitResult := sw.Allow()
	require.False(t, limitResult.Allowed)
	assert.Greater(t, limitResult.RetryAfterSeconds, 0.0)
	assert.NotEmpty(t, limitResult.PolicyRule)

	// Log the rate limit rejection
	entry := audit.Entry{
		Timestamp:  time.Now().UTC(),
		Event:      audit.EventRateLimit,
		Decision:   "deny",
		AgentID:    "researcher",
		SessionID:  "sess-e2e-003",
		Tool:       "llm_call",
		Parameters: map[string]any{},
		PolicyRule: limitResult.PolicyRule,
	}
	require.NoError(t, auditLogger.Log(&entry))

	// Validate audit entry has all required fields
	data, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	require.NoError(t, err)

	var parsed map[string]any
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, "deny", parsed["decision"])
	policyRule, _ := parsed["policy_rule"].(string)
	assert.Contains(t, policyRule, "rate_limit")
}

// TestE2E_SSRFBlocked is an end-to-end test: agent calls web_fetch to private IP →
// SSRF checker blocks it → audit entry written with decision: "deny".
// Traces to: wave2-security-layer-spec.md line 816 (TestE2E_SSRFBlocked)
// BDD: Full SSRF block (spec line 648)
func TestE2E_SSRFBlocked(t *testing.T) {
	dir := t.TempDir()
	auditLogger, err := audit.NewLogger(audit.LoggerConfig{
		Dir:           dir,
		RetentionDays: 90,
		RedactEnabled: true,
	})
	require.NoError(t, err)
	defer func() {
		if err := auditLogger.Close(); err != nil {
			_ = err
		}
	}()

	checker := security.NewSSRFChecker(nil)

	targetURL := "http://169.254.169.254/latest/meta-data/"
	ssrfErr := checker.CheckURL(context.Background(), targetURL)
	require.Error(t, ssrfErr, "cloud metadata endpoint must be blocked by SSRF checker")
	assert.Contains(t, ssrfErr.Error(), "SSRF")

	// Build audit entry for the denial
	entry := audit.Entry{
		Timestamp:  time.Now().UTC(),
		Event:      audit.EventSSRF,
		Decision:   "deny",
		AgentID:    "researcher",
		SessionID:  "sess-e2e-004",
		Tool:       "web_fetch",
		Parameters: map[string]any{"url": targetURL},
		PolicyRule: ssrfErr.Error(),
	}
	require.NoError(t, auditLogger.Log(&entry))

	// Validate audit entry
	data, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	require.NoError(t, err)

	var parsed map[string]any
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, "deny", parsed["decision"])
	assert.Equal(t, "web_fetch", parsed["tool"])
	policyRule, _ := parsed["policy_rule"].(string)
	assert.Contains(t, policyRule, "SSRF")
}
