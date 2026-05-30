// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// V2.B fail-closed approver tests. Closes silent-failure-hunter BE CRIT-1
// from the 14-reviewer audit: nopPolicyApprover.RequestApproval previously
// returned (true, "") in the default build, auto-approving every ask-policy
// tool — including admin-flagged tools — with zero log and zero audit when
// SetToolApprover hadn't been called. The tests below pin the new
// fail-closed behavior:
//
//  1. Default-build nop denies and emits one approver.fallback audit row.
//  2. Repeated nop hits emit at most one audit row per process (sync.Once).
//
// The third sibling test — testAutoApproveApprover preserves auto-approve
// when the test build tag is on — lives in tool_approver_testonly_test.go
// (build tag `test`) so the production binary never compiles or runs it.

package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/audit"
)

// resetNopApproverFallbackOnceForTest re-arms the package-level sync.Once
// that gates the V2.B `approver.fallback` audit emit. Each test that
// exercises the once-per-process guarantee MUST call this before invoking
// nopPolicyApprover, otherwise the order in which Go runs tests in the
// package would leak state across cases. Test-only — only callable from
// within the agent package.
func resetNopApproverFallbackOnceForTest() {
	nopApproverFallbackOnce = sync.Once{}
}

// readAuditJSONL parses every JSON line in the audit.jsonl under dir and
// returns the decoded entries. Used by the tests below to assert which
// events were emitted (or NOT emitted, for the deny-without-audit case).
func readAuditJSONL(t *testing.T, dir string) []map[string]any {
	t.Helper()
	path := filepath.Join(dir, "audit.jsonl")
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	defer f.Close()

	var entries []map[string]any
	scanner := bufio.NewScanner(f)
	// Audit entries can be wider than the default 64KiB token; bump the
	// buffer so a long redacted-args entry doesn't trip "token too long".
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &entry), "audit line must be valid JSON")
		entries = append(entries, entry)
	}
	require.NoError(t, scanner.Err())
	return entries
}

// countByEvent returns the number of entries whose `event` field equals
// the given string. The event constant is supplied by the caller so a
// rename of audit.EventApproverFallback is caught by the test compile,
// not by silent miscount.
func countByEvent(entries []map[string]any, event string) int {
	n := 0
	for _, e := range entries {
		if got, _ := e["event"].(string); got == event {
			n++
		}
	}
	return n
}

// TestNopApprover_DefaultBuild_DeniesAndAudits — V2.B CRIT-1 contract:
// without SetToolApprover, an approval request must (a) return
// approved=false with reason "no_approver_configured", and (b) emit one
// `approver.fallback` audit row classified as deny. This is the
// fail-closed signal that replaces the old fail-open auto-approve.
func TestNopApprover_DefaultBuild_DeniesAndAudits(t *testing.T) {
	resetNopApproverFallbackOnceForTest()

	dir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: dir, RetentionDays: 90})
	require.NoError(t, err)
	defer logger.Close()

	approver := nopPolicyApprover{auditLogger: logger}
	approved, reason := approver.RequestApproval(context.Background(), PolicyApprovalReq{
		ToolCallID:    "call-1",
		ToolName:      "exec",
		AgentID:       "ray",
		SessionID:     "sess-1",
		TurnID:        "turn-1",
		RequiresAdmin: true,
	})
	assert.False(t, approved, "default-build nop must deny — fail-closed")
	assert.Equal(t, "no_approver_configured", reason,
		"denial reason must be the V2.B canonical string so callers / SPA / SIEM can pattern-match")

	// Force the audit logger to flush so we read the JSONL we just wrote.
	require.NoError(t, logger.Close())

	entries := readAuditJSONL(t, dir)
	require.NotEmpty(t, entries, "approver.fallback row must be on disk after one nop hit")

	count := countByEvent(entries, audit.EventApproverFallback)
	assert.Equal(t, 1, count, "exactly one approver.fallback row")

	// Locate the row and verify the load-bearing fields.
	var fallback map[string]any
	for _, e := range entries {
		if got, _ := e["event"].(string); got == audit.EventApproverFallback {
			fallback = e
			break
		}
	}
	require.NotNil(t, fallback, "must find the approver.fallback entry")

	assert.Equal(t, audit.DecisionDeny, fallback["decision"],
		"approver.fallback must be classified as deny")
	assert.Equal(t, "ray", fallback["agent_id"])
	assert.Equal(t, "sess-1", fallback["session_id"])
	assert.Equal(t, "exec", fallback["tool"])

	details, ok := fallback["details"].(map[string]any)
	require.True(t, ok, "details map must be present")
	assert.Equal(t, "no_approver_configured", details["reason"])
	assert.Equal(t, "default", details["build"])
	assert.Equal(t, "turn-1", details["turn_id"])
	assert.Equal(t, "call-1", details["tool_call_id"])
	assert.Equal(t, true, details["requires_admin"],
		"requires_admin must round-trip so an operator can see the admin-flagged tool got denied")
}

// TestNopApprover_RepeatedHits_OnlyAuditOnce — V2.B sync.Once gate. Five
// consecutive approval requests must all deny but emit at most ONE
// approver.fallback audit row. Without the once-gate, a misconfigured
// deployment running an LLM in a loop would flood audit.jsonl with one
// row per ask call.
func TestNopApprover_RepeatedHits_OnlyAuditOnce(t *testing.T) {
	resetNopApproverFallbackOnceForTest()

	dir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: dir, RetentionDays: 90})
	require.NoError(t, err)
	defer logger.Close()

	approver := nopPolicyApprover{auditLogger: logger}
	for i := 0; i < 5; i++ {
		approved, reason := approver.RequestApproval(context.Background(), PolicyApprovalReq{
			ToolCallID: "call-rep",
			ToolName:   "exec",
			AgentID:    "ray",
			SessionID:  "sess-rep",
			TurnID:     "turn-rep",
		})
		assert.False(t, approved, "every repeated nop hit must deny")
		assert.Equal(t, "no_approver_configured", reason)
	}

	require.NoError(t, logger.Close())

	entries := readAuditJSONL(t, dir)
	count := countByEvent(entries, audit.EventApproverFallback)
	assert.Equal(t, 1, count,
		"5 nop hits must emit exactly 1 approver.fallback row (sync.Once gate)")
}

// TestNopApprover_NilAuditLogger_StillDenies — defensive: when audit is
// disabled by the operator (auditLogger == nil), the nop must still
// fail-closed. The audit emit becomes a no-op via audit.EmitEntry's
// nil-logger contract, but the deny return value is independent of audit
// and must be unchanged.
func TestNopApprover_NilAuditLogger_StillDenies(t *testing.T) {
	resetNopApproverFallbackOnceForTest()

	approver := nopPolicyApprover{auditLogger: nil}
	approved, reason := approver.RequestApproval(context.Background(), PolicyApprovalReq{
		ToolName: "exec",
		AgentID:  "ray",
	})
	assert.False(t, approved, "nil audit logger must NOT regress the fail-closed deny")
	assert.Equal(t, "no_approver_configured", reason)
}
