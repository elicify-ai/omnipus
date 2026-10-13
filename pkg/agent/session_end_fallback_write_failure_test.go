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
	"github.com/elicify-ai/omnipus/pkg/session"
)

// WU-21 re-aim (#1197): the fallback resolves its (agent, workspace)
// carry-forward scope from the session's own meta via the loop's shared
// session store, so the fixture builds one and stamps the workspace; the
// failure injection moved from the retired agent-wide last-session.md to the
// workspace's pair file, and $OMNIPUS_HOME is isolated because the pair path
// is built from it. The write-failure outcome no longer names "retro" next to
// "last_session": on a failed summary write the fallback files NO retro at all
// (amendment §3.2(3)), which the no-retro assertion below pins.
func newFallbackFixture(t *testing.T, sessionID string) (*AgentLoop, *AgentInstance, string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNIPUS_HOME", home)

	auditDir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: auditDir, RetentionDays: 90})
	require.NoError(t, err)
	t.Cleanup(func() { _ = logger.Close() })
	al := &AgentLoop{auditLogger: logger}

	store, err := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	require.NoError(t, err)
	t.Cleanup(func() {
		if closeErr := store.Close(); closeErr != nil {
			t.Errorf("close session store: %v", closeErr)
		}
	})
	al.sharedSessionStore = store

	agentHome := filepath.Join(home, "agents", "recap-fb-agent")
	ag := &AgentInstance{ID: "recap-fb-agent", Home: agentHome, ContextBuilder: NewContextBuilder(agentHome)}
	require.NotNil(t, ag.ContextBuilder.Memory())

	meta, err := store.NewSession(session.SessionTypeChat, "web", ag.ID)
	require.NoError(t, err)
	require.Equal(t, sessionID, meta.ID, "fixture session id must match the caller's")
	wu21ReaimStampWorkspace(t, store, sessionID)

	pairPath := filepath.Join(home, "workspaces", wu21ReaimWorkspace, ".omnipus", "carry-forward", ag.ID+".md")
	return al, ag, auditDir, pairPath
}

func fallbackAuditOutcome(t *testing.T, auditDir string) string {
	t.Helper()
	entry := readLastAuditEntry(t, auditDir)
	details, _ := entry["details"].(map[string]any)
	outcome, _ := details["outcome"].(string)
	return outcome
}

// The heuristic fallback must not claim success when it could not write: the
// failure is returned, the audit outcome says so instead of recording
// "fallback:<reason>", and no retro is filed (the marker must not claim a
// fallback that does not exist).
func TestWriteHeuristicFallbackRetro_WriteFailure_ReturnedAndAudited(t *testing.T) {
	al, ag, auditDir, pairPath := newFallbackFixture(t, "sess_fail")

	// A directory where the pair file belongs: the atomic rename onto it fails.
	require.NoError(t, os.MkdirAll(filepath.Join(pairPath, "child"), 0o700))

	writeErr := al.writeHeuristicFallbackRetroWithCount("sess_fail", "explicit", "llm_error", ag, 3, 1, "")
	require.Error(t, writeErr, "a fallback whose carry-forward write failed must return that error")

	outcome := fallbackAuditOutcome(t, auditDir)
	require.Equal(t, "fallback_write_failed:llm_error:last_session", outcome,
		"audit outcome must name the failed step")
	require.NotContains(t, outcome, pairPath, "no filesystem path may reach the audit string")
	require.False(t, al.agentSessionHasRetro(ag, "sess_fail"),
		"no retro may be filed after a failed carry-forward write: it would block the retry")
}

// Control: with a working store the fallback returns nil, records
// "fallback:<reason>", and lands the summary in the workspace's pair file.
func TestWriteHeuristicFallbackRetro_WriteOK_RecordsFallback(t *testing.T) {
	al, ag, auditDir, pairPath := newFallbackFixture(t, "sess_ok")

	require.NoError(t, al.writeHeuristicFallbackRetroWithCount("sess_ok", "explicit", "llm_error", ag, 3, 1, ""))

	entry := readLastAuditEntry(t, auditDir)
	details, _ := entry["details"].(map[string]any)
	require.Equal(t, "fallback:llm_error", details["outcome"])
	got, err := os.ReadFile(pairPath)
	require.NoError(t, err, "the fallback summary must land in the workspace's pair file")
	require.Contains(t, string(got), "Fallback reason: llm_error.")
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
