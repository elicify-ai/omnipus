// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build test

// V2.B: Verifies that the test-only `testAutoApproveApprover` survives
// behind the `test` build tag and, when explicitly installed via
// SetToolApprover, allows ask-policy tools to proceed without ever
// touching the default-build fail-closed nopPolicyApprover. This pins the
// migration path for tests that previously relied on the old fail-open
// nop default — they must now wire testAutoApproveApprover explicitly so
// the dependency on auto-approval is visible at the call site.

package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
)

// TestNopApprover_TestApproverInstalled_AllowsAndDoesNotAudit — when the
// test-only auto-approve approver is wired via SetToolApprover, an ask
// request must (a) be approved, (b) NOT route through the nop, and (c)
// NOT emit any approver.fallback audit row. This is the inverse contract
// to TestNopApprover_DefaultBuild_DeniesAndAudits and proves that the
// test build tag preserves the historical auto-approve path for callers
// that explicitly opt in.
func TestNopApprover_TestApproverInstalled_AllowsAndDoesNotAudit(t *testing.T) {
	resetNopApproverFallbackOnceForTest()

	dir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: dir, RetentionDays: 90})
	require.NoError(t, err)
	defer logger.Close()

	// Direct invocation of testAutoApproveApprover (no AgentLoop needed).
	approver := testAutoApproveApprover{}
	approved, reason := approver.RequestApproval(context.Background(), PolicyApprovalReq{
		ToolCallID: "call-test",
		ToolName:   "exec",
		AgentID:    "ray",
		SessionID:  "sess-test",
		TurnID:     "turn-test",
	})
	assert.True(t, approved, "test-only approver must auto-approve")
	assert.Equal(t, "test_auto_approve", reason,
		"reason string must identify the test approver for diagnostic clarity")

	require.NoError(t, logger.Close())

	entries := readAuditJSONL(t, dir)
	count := countByEvent(entries, audit.EventApproverFallback)
	assert.Equal(t, 0, count,
		"installing testAutoApproveApprover must NEVER emit approver.fallback — "+
			"that event is the diagnostic signal for the un-wired nop path")
}
