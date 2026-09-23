// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Functional proof for ADR-092's FR-032(c)/FR-046 shell.grant_recorded
// audit event: every ApprovalGrantStore method that persists a NEW grant
// (Record's "bash" exact case, RecordPrefixGrant, RecordPathGrant,
// RecordNetworkGrant) must emit exactly one audit.EventShellGrantRecorded
// row for it, colocated with the state mutation — and must NOT emit a
// second row for a duplicate/no-op record of the same grant.
//
// These tests delete emitGrantRecorded's call site test-by-test: comment
// one out and the corresponding test below goes red (verified manually
// during development — see lane AUDIT's final report for the exact
// mutation-kill evidence).

package security

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
)

// grantAuditRow is one decoded audit.jsonl line, reduced to the fields
// FR-032(c) names.
type grantAuditRow struct {
	Event     string         `json:"event"`
	Decision  string         `json:"decision"`
	AgentID   string         `json:"agent_id"`
	SessionID string         `json:"session_id"`
	Tool      string         `json:"tool"`
	Details   map[string]any `json:"details"`
}

func (r grantAuditRow) detail(key string) string {
	v, _ := r.Details[key].(string)
	return v
}

// readGrantAuditRows closes logger (forcing a flush) and decodes every line
// of audit.jsonl in dir.
func readGrantAuditRows(t *testing.T, logger *audit.Logger, dir string) []grantAuditRow {
	t.Helper()
	require.NoError(t, logger.Close())
	data, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	require.NoError(t, err)
	var rows []grantAuditRow
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var row grantAuditRow
		require.NoError(t, json.Unmarshal([]byte(line), &row))
		rows = append(rows, row)
	}
	return rows
}

func rowsForEvent(rows []grantAuditRow, event string) []grantAuditRow {
	var out []grantAuditRow
	for _, r := range rows {
		if r.Event == event {
			out = append(out, r)
		}
	}
	return out
}

func newAuditedStore(t *testing.T) (*ApprovalGrantStore, *audit.Logger, string) {
	t.Helper()
	dir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: dir, RetentionDays: 1})
	require.NoError(t, err)
	store := NewApprovalGrantStore()
	store.SetAuditLogger(logger)
	return store, logger, dir
}

// --- exact scope (Record), bash-only ---

func TestApprovalGrantStore_RecordEmitsGrantRecordedForBash(t *testing.T) {
	store, logger, dir := newAuditedStore(t)

	ok := store.Record("session-1", "agent-1", "bash", map[string]any{"command": "git push"})
	require.True(t, ok)

	rows := rowsForEvent(readGrantAuditRows(t, logger, dir), audit.EventShellGrantRecorded)
	require.Len(t, rows, 1, "a new exact-fingerprint bash grant must emit exactly one shell.grant_recorded row")
	assert.Equal(t, audit.DecisionAllow, rows[0].Decision)
	assert.Equal(t, "agent-1", rows[0].AgentID)
	assert.Equal(t, "session-1", rows[0].SessionID)
	assert.Equal(t, "bash", rows[0].Tool)
	assert.Equal(t, string(audit.ShellGrantScopeExact), rows[0].detail("scope"))
	assert.Equal(t, "session", rows[0].detail("lifetime"))
}

func TestApprovalGrantStore_RecordDoesNotEmitForNonBashTool(t *testing.T) {
	store, logger, dir := newAuditedStore(t)

	ok := store.Record("session-1", "agent-1", "write_file", map[string]any{"path": "/tmp/x"})
	require.True(t, ok)

	rows := rowsForEvent(readGrantAuditRows(t, logger, dir), audit.EventShellGrantRecorded)
	assert.Empty(t, rows, "shell.grant_recorded is ADR-092's own bash taxonomy — a non-bash tool's exact grant must not be mislabeled under it")
}

func TestApprovalGrantStore_RecordDuplicateDoesNotReEmit(t *testing.T) {
	store, logger, dir := newAuditedStore(t)

	args := map[string]any{"command": "git push"}
	require.True(t, store.Record("session-1", "agent-1", "bash", args))
	require.True(t, store.Record("session-1", "agent-1", "bash", args)) // same fingerprint again

	rows := rowsForEvent(readGrantAuditRows(t, logger, dir), audit.EventShellGrantRecorded)
	assert.Len(t, rows, 1, "re-recording the SAME fingerprint must not emit a second grant_recorded row")
}

// --- prefix scope (D4) ---

func TestApprovalGrantStore_RecordPrefixGrantEmitsGrantRecorded(t *testing.T) {
	store, logger, dir := newAuditedStore(t)

	grant := ShellPrefixGrant{Binary: "/usr/bin/npm", ArgPrefix: "run test", RunInBackground: false}
	ok := store.RecordPrefixGrant("session-1", "agent-1", "bash", grant)
	require.True(t, ok)

	rows := rowsForEvent(readGrantAuditRows(t, logger, dir), audit.EventShellGrantRecorded)
	require.Len(t, rows, 1)
	assert.Equal(t, string(audit.ShellGrantScopePrefix), rows[0].detail("scope"))
	assert.Equal(t, "/usr/bin/npm", rows[0].detail("resolved_binary"))
	assert.Equal(t, "run test", rows[0].detail("arg_prefix"))
}

func TestApprovalGrantStore_RecordPrefixGrantDuplicateDoesNotReEmit(t *testing.T) {
	store, logger, dir := newAuditedStore(t)

	grant := ShellPrefixGrant{Binary: "/usr/bin/npm", ArgPrefix: "run test"}
	require.True(t, store.RecordPrefixGrant("session-1", "agent-1", "bash", grant))
	require.True(t, store.RecordPrefixGrant("session-1", "agent-1", "bash", grant))

	rows := rowsForEvent(readGrantAuditRows(t, logger, dir), audit.EventShellGrantRecorded)
	assert.Len(t, rows, 1, "an identical prefix grant recorded twice must not double-emit")
}

// --- path-widening scope (D7) ---

func TestApprovalGrantStore_RecordPathGrantEmitsGrantRecorded(t *testing.T) {
	store, logger, dir := newAuditedStore(t)

	ok := store.RecordPathGrant("session-1", "agent-1", fspolicy.PathGrant{Path: "/tmp/out.txt", Access: fspolicy.PathGrantAccessWrite})
	require.True(t, ok)

	rows := rowsForEvent(readGrantAuditRows(t, logger, dir), audit.EventShellGrantRecorded)
	require.Len(t, rows, 1)
	assert.Equal(t, string(audit.ShellGrantScopePathWidening), rows[0].detail("scope"))
	assert.Equal(t, "/tmp/out.txt", rows[0].detail("path"))
	assert.Equal(t, "write", rows[0].detail("access"))
	assert.Equal(t, "bash", rows[0].Tool)
}

// --- network-widening scope (D8) ---

func TestApprovalGrantStore_RecordNetworkGrantEmitsGrantRecordedOnceOnly(t *testing.T) {
	store, logger, dir := newAuditedStore(t)

	require.True(t, store.RecordNetworkGrant("session-1", "agent-1"))
	require.True(t, store.RecordNetworkGrant("session-1", "agent-1")) // idempotent re-grant

	rows := rowsForEvent(readGrantAuditRows(t, logger, dir), audit.EventShellGrantRecorded)
	require.Len(t, rows, 1, "RecordNetworkGrant is idempotent — a second call for an already-held grant must not re-emit")
	assert.Equal(t, string(audit.ShellGrantScopeNetworkWidening), rows[0].detail("scope"))
	assert.Equal(t, "bash", rows[0].Tool)
}

// --- no audit logger wired: pure store, no panic, no emission ---

func TestApprovalGrantStore_NoAuditLoggerWiredIsPureNoOp(t *testing.T) {
	store := NewApprovalGrantStore() // SetAuditLogger never called
	assert.True(t, store.RecordNetworkGrant("session-1", "agent-1"))
	assert.True(t, store.RecordPathGrant("session-1", "agent-1", fspolicy.PathGrant{Path: "/tmp/x", Access: fspolicy.PathGrantAccessRead}))
	assert.True(t, store.Record("session-1", "agent-1", "bash", map[string]any{"a": 1}))
	// No assertion beyond "did not panic" — there is no logger to read from.
}
