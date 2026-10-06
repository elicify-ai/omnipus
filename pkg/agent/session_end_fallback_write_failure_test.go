// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
)

// The heuristic fallback must not claim success when it could not write: the
// failure is returned, and the audit outcome says so instead of recording
// "fallback:<reason>".
func TestWriteHeuristicFallbackRetro_WriteFailure_ReturnedAndAudited(t *testing.T) {
	auditDir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: auditDir, RetentionDays: 90})
	require.NoError(t, err)
	defer func() { _ = logger.Close() }()
	al := &AgentLoop{auditLogger: logger}

	agentHome := t.TempDir()
	ag := &AgentInstance{ID: "recap-fail-agent", Home: agentHome, ContextBuilder: NewContextBuilder(agentHome)}
	memory := ag.ContextBuilder.Memory()
	require.NotNil(t, memory)

	// A regular file where the private room's directory belongs: every write fails.
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o600))
	memory.privateRoom.Root = filepath.Join(blocker, "room")

	writeErr := al.writeHeuristicFallbackRetroWithCount("sess_fail", "explicit", "llm_error", ag, 3, 1, "")
	require.Error(t, writeErr, "a fallback whose writes failed must return that error")

	entry := readLastAuditEntry(t, auditDir)
	details, _ := entry["details"].(map[string]any)
	outcome, _ := details["outcome"].(string)
	require.Equal(t, "fallback_write_failed:llm_error:last_session,retro", outcome,
		"audit outcome must name the failed steps")
	require.NotContains(t, outcome, blocker, "no filesystem path may reach the audit string")
}

// Control: with a working store the fallback returns nil and records "fallback:<reason>".
func TestWriteHeuristicFallbackRetro_WriteOK_RecordsFallback(t *testing.T) {
	auditDir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: auditDir, RetentionDays: 90})
	require.NoError(t, err)
	defer func() { _ = logger.Close() }()
	al := &AgentLoop{auditLogger: logger}

	agentHome := t.TempDir()
	ag := &AgentInstance{ID: "recap-ok-agent", Home: agentHome, ContextBuilder: NewContextBuilder(agentHome)}

	require.NoError(t, al.writeHeuristicFallbackRetroWithCount("sess_ok", "explicit", "llm_error", ag, 3, 1, ""))

	entry := readLastAuditEntry(t, auditDir)
	details, _ := entry["details"].(map[string]any)
	require.Equal(t, "fallback:llm_error", details["outcome"])
}

// With no agent, or an agent without a memory store, nothing is written: the
// fallback says so (sentinel error, "fallback_skipped" audit) instead of
// recording a fallback that does not exist.
func TestWriteHeuristicFallbackRetro_NothingToWriteTo_SkippedNotRecorded(t *testing.T) {
	cases := map[string]*AgentInstance{
		"no agent":        nil,
		"no memory store": {ID: "no-memory-agent", ContextBuilder: &ContextBuilder{}},
	}
	for name, ag := range cases {
		t.Run(name, func(t *testing.T) {
			auditDir := t.TempDir()
			logger, err := audit.NewLogger(audit.LoggerConfig{Dir: auditDir, RetentionDays: 90})
			require.NoError(t, err)
			defer func() { _ = logger.Close() }()
			al := &AgentLoop{auditLogger: logger}

			writeErr := al.writeHeuristicFallbackRetroWithCount("sess_skip", "explicit", "llm_error", ag, 3, 1, "")
			require.ErrorIs(t, writeErr, ErrFallbackRecapSkipped)

			entry := readLastAuditEntry(t, auditDir)
			details, _ := entry["details"].(map[string]any)
			require.Equal(t, "fallback_skipped:llm_error", details["outcome"])
		})
	}
}
