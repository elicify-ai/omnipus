// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// FR-017 (cli-minimization-spec): agent-turn audit entries must be
// attributable to the authenticated gateway principal that initiated the turn.
// A CLI run authenticated as the `cli` principal must yield audit entries with
// User=="cli"; channel-originated and unauthenticated (env-token / dev-bypass)
// turns must leave User empty.
//
// These tests drive the real plumbing path:
//
//	processOptions.UserID  →  newTurnState → turnState.userID → ts.auditUser()
//	                       →  audit.Entry.User (stamped at a ts-scoped emission
//	                          site: emitPolicyDenyAudit)
//
// They do NOT poke turnState.userID directly — they construct the turnState via
// the production constructor so a regression anywhere in the thread is caught.

package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/audit"
)

// newAuditUserFixture builds a minimal AgentLoop with a real audit logger
// writing to a temp dir, plus a turnState constructed from processOptions
// carrying the given userID. It returns the loop, the turn, and the temp dir
// so the caller can read back audit.jsonl. closeFn flushes + closes the logger.
func newAuditUserFixture(t *testing.T, userID string) (al *AgentLoop, ts *turnState, dir string, closeFn func()) {
	t.Helper()
	dir = t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: dir, RetentionDays: 90})
	require.NoError(t, err)

	al = &AgentLoop{auditLogger: logger}

	agentInst := &AgentInstance{ID: "jim"}
	opts := processOptions{
		SessionKey:  "sess-cli-1",
		Channel:     "webchat",
		UserMessage: "2+2?",
		// FR-017: the authenticated gateway principal threaded from the WS
		// connection. This is the field under test.
		UserID: userID,
	}
	scope := turnEventScope{agentID: "jim", sessionKey: "sess-cli-1", turnID: "jim-turn-1"}
	ts = newTurnState(agentInst, opts, scope)

	return al, ts, dir, func() { _ = logger.Close() }
}

// readLastAuditEntry reads audit.jsonl and returns the last decoded entry.
func readLastAuditEntry(t *testing.T, dir string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	require.NoError(t, err)

	var last map[string]any
	for _, line := range splitNonEmptyLines(string(data)) {
		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &entry), "audit line must be valid JSON")
		last = entry
	}
	require.NotNil(t, last, "expected at least one audit entry")
	return last
}

// splitNonEmptyLines splits JSONL into non-empty lines.
func splitNonEmptyLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			if line := s[start:i]; line != "" {
				out = append(out, line)
			}
			start = i + 1
		}
	}
	if tail := s[start:]; tail != "" {
		out = append(out, tail)
	}
	return out
}

// TestAuditEntry_AttributedToCliPrincipal is the FR-017 core proof: a turn whose
// processOptions.UserID is "cli" produces an audit.Entry with User=="cli".
//
// Traces to: cli-minimization-spec.md FR-017 / SC-004 / US-5 AC-4
// ("the run's audit entries carry User=\"cli\"").
func TestAuditEntry_AttributedToCliPrincipal(t *testing.T) {
	al, ts, dir, closeFn := newAuditUserFixture(t, "cli")

	// The threaded value must reach turnState via the production constructor.
	require.Equal(t, "cli", ts.userID, "processOptions.UserID must thread into turnState.userID")
	require.Equal(t, "cli", ts.auditUser(), "ts.auditUser() must return the threaded principal")

	// Drive a real ts-scoped audit emission site (FR-017 stamps User here).
	al.emitPolicyDenyAudit(ts, "workspace.shell", "deny", "exec_policy_denied")
	closeFn()

	entry := readLastAuditEntry(t, dir)
	assert.Equal(t, "cli", entry["user"], "audit entry must be attributable to the cli principal")
	// Sanity: the rest of the entry is populated with real values, not blanks.
	assert.Equal(t, string(audit.EventToolPolicyDenyAttempted), entry["event"])
	assert.Equal(t, string(audit.DecisionDeny), entry["decision"])
	assert.Equal(t, "jim", entry["agent_id"])
	assert.Equal(t, "workspace.shell", entry["tool"])
}

// TestAuditEntry_NoUser_WhenUnauthenticated pins the default/empty behavior:
// a turn with no authenticated principal (channel-originated, env-token,
// dev-bypass) leaves User empty — the field is omitted from the JSONL entirely
// (json:"user,omitempty"), never guessed.
//
// Traces to: cli-minimization-spec.md FR-017 ("when no authenticated user …
// User is empty — never crash, never guess").
func TestAuditEntry_NoUser_WhenUnauthenticated(t *testing.T) {
	al, ts, dir, closeFn := newAuditUserFixture(t, "")

	require.Empty(t, ts.userID, "an unauthenticated turn must carry no principal")
	require.Empty(t, ts.auditUser())

	al.emitPolicyDenyAudit(ts, "workspace.shell", "deny", "exec_policy_denied")
	closeFn()

	entry := readLastAuditEntry(t, dir)
	_, present := entry["user"]
	assert.False(t, present, "user key must be omitted when no principal is authenticated")
	// The emission itself must still happen with real values (fail-open empty,
	// not fail-to-emit).
	assert.Equal(t, string(audit.EventToolPolicyDenyAttempted), entry["event"])
	assert.Equal(t, "jim", entry["agent_id"])
}

// TestTurnState_AuditUser_NilSafe documents that the accessor never panics on a
// nil turnState (lifecycle audit sites pass a turn that may be absent).
func TestTurnState_AuditUser_NilSafe(t *testing.T) {
	var ts *turnState
	assert.Empty(t, ts.auditUser(), "auditUser() on a nil turnState must return empty, not panic")
}
