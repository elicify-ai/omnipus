// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

const recapSuccessBody = `{"recap":"The user gave a codename.","went_well":["a"],"needs_improvement":["b"],"worth_remembering":["The codename is ORCHID-7"]}`

// WU-21 re-aim (#1197): the recap summary is stored per (agent, workspace) at
// workspaces/<ws>/.omnipus/carry-forward/<agent>.md, so this fixture stamps a
// workspace onto the recap run (rr.workspaceID — what loadTranscript resolves
// from the session meta in production) and isolates $OMNIPUS_HOME, which the
// pair path is built from. Assertions are unchanged.
func newRecapPersistFixture(t *testing.T, sessionID string) (*agentLoopRunRecap, string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNIPUS_HOME", home)

	auditDir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: auditDir, RetentionDays: 90})
	require.NoError(t, err)
	t.Cleanup(func() { _ = logger.Close() })
	al := &AgentLoop{auditLogger: logger}

	agentHome := filepath.Join(home, "agents", "recap-persist-agent")
	ag := &AgentInstance{ID: "recap-persist-agent", Home: agentHome, ContextBuilder: NewContextBuilder(agentHome)}
	require.NotNil(t, ag.ContextBuilder.Memory())

	rr := &agentLoopRunRecap{
		al:          al,
		sessionID:   sessionID,
		trigger:     "idle",
		agentInst:   ag,
		workspaceID: wu21ReaimWorkspace,
		resp:        &providers.LLMResponse{Content: recapSuccessBody},
	}
	pairPath := filepath.Join(home, "workspaces", wu21ReaimWorkspace, ".omnipus", "carry-forward", ag.ID+".md")
	return rr, auditDir, pairPath
}

func lastRecapOutcome(t *testing.T, auditDir string) string {
	t.Helper()
	entry := readLastAuditEntry(t, auditDir)
	details, _ := entry["details"].(map[string]any)
	outcome, _ := details["outcome"].(string)
	return outcome
}

// A failed carry-forward write on the success path is a visible failure: the
// audit outcome says so, and no retro is filed, so the session is not marked
// recapped and a later pass can retry.
func TestPersistResponse_LastSessionWriteFails_AuditedAndRetroNotFiled(t *testing.T) {
	rr, auditDir, pairPath := newRecapPersistFixture(t, "sess_ls_fail")

	// A directory where the file belongs: the atomic rename onto it fails.
	require.NoError(t, os.MkdirAll(filepath.Join(pairPath, "child"), 0o700))

	rr.persistResponse()

	outcome := lastRecapOutcome(t, auditDir)
	require.Equal(t, "write_failed:last_session", outcome,
		"a failed carry-forward write must not be audited as success")
	require.NotContains(t, outcome, pairPath, "no filesystem path may reach the audit string")
	require.False(t, rr.al.agentSessionHasRetro(rr.agentInst, rr.sessionID),
		"no retro may be filed after a failed write: it would block the retry")
}

// Control: with a working store the same input is audited "success", files the
// retro, and the worth_remembering item reaches the workspace's carry-forward
// file.
func TestPersistResponse_WriteOK_AuditedSuccessAndRetroFiled(t *testing.T) {
	rr, auditDir, pairPath := newRecapPersistFixture(t, "sess_ls_ok")

	rr.persistResponse()

	require.Equal(t, "success", lastRecapOutcome(t, auditDir))
	require.True(t, rr.al.agentSessionHasRetro(rr.agentInst, rr.sessionID))
	got, err := os.ReadFile(pairPath)
	require.NoError(t, err)
	require.True(t, strings.Contains(string(got), "ORCHID-7"))
}

// A failed retro append on the success path is a visible failure too: the audit
// outcome names the failed step, never "success". The retro is the marker that
// blocks a later recap, and it was not written, so a later pass can retry; the
// recap itself did land in the workspace's carry-forward file.
func TestPersistResponse_RetroAppendFails_AuditedAndRetryable(t *testing.T) {
	rr, auditDir, pairPath := newRecapPersistFixture(t, "sess_retro_fail")

	// A regular file where the retros directory belongs: the append cannot create it.
	retrosRoot := filepath.Join(rr.agentInst.Home, ".omnipus", "retros")
	require.NoError(t, os.WriteFile(retrosRoot, []byte("x"), 0o600))

	rr.persistResponse()

	outcome := lastRecapOutcome(t, auditDir)
	require.Equal(t, "write_failed:retro", outcome, "a failed retro append must not be audited as success")
	require.NotContains(t, outcome, pairPath, "no filesystem path may reach the audit string")
	require.False(t, rr.al.agentSessionHasRetro(rr.agentInst, rr.sessionID),
		"no retro exists, so a later recap pass is not blocked")
	got, err := os.ReadFile(pairPath)
	require.NoError(t, err, "the recap itself did land")
	require.True(t, strings.Contains(string(got), "ORCHID-7"))
}
