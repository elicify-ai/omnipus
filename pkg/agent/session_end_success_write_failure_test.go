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
	"github.com/elicify-ai/omnipus/pkg/memrooms"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

const recapSuccessBody = `{"recap":"The user gave a codename.","went_well":["a"],"needs_improvement":["b"],"worth_remembering":["The codename is ORCHID-7"]}`

func newRecapPersistFixture(t *testing.T, sessionID string) (*agentLoopRunRecap, string, string) {
	t.Helper()
	auditDir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: auditDir, RetentionDays: 90})
	require.NoError(t, err)
	t.Cleanup(func() { _ = logger.Close() })
	al := &AgentLoop{auditLogger: logger}

	agentHome := t.TempDir()
	ag := &AgentInstance{ID: "recap-persist-agent", Home: agentHome, ContextBuilder: NewContextBuilder(agentHome)}
	require.NotNil(t, ag.ContextBuilder.Memory())

	rr := &agentLoopRunRecap{
		al:        al,
		sessionID: sessionID,
		trigger:   "idle",
		agentInst: ag,
		resp:      &providers.LLMResponse{Content: recapSuccessBody},
	}
	return rr, auditDir, ag.ContextBuilder.Memory().privateRoom.Root
}

func lastRecapOutcome(t *testing.T, auditDir string) string {
	t.Helper()
	entry := readLastAuditEntry(t, auditDir)
	details, _ := entry["details"].(map[string]any)
	outcome, _ := details["outcome"].(string)
	return outcome
}

// A failed last-session.md write on the success path is a visible failure: the
// audit outcome says so, and no retro is filed, so the session is not marked
// recapped and a later pass can retry.
func TestPersistResponse_LastSessionWriteFails_AuditedAndRetroNotFiled(t *testing.T) {
	rr, auditDir, roomRoot := newRecapPersistFixture(t, "sess_ls_fail")

	// A directory where the file belongs: the atomic rename onto it fails.
	require.NoError(t, os.MkdirAll(filepath.Join(roomRoot, memrooms.LastSessionFile, "child"), 0o700))

	rr.persistResponse()

	outcome := lastRecapOutcome(t, auditDir)
	require.Equal(t, "write_failed:last_session", outcome,
		"a failed last-session write must not be audited as success")
	require.NotContains(t, outcome, roomRoot, "no filesystem path may reach the audit string")
	require.False(t, rr.al.agentSessionHasRetro(rr.agentInst, rr.sessionID),
		"no retro may be filed after a failed write: it would block the retry")
}

// Control: with a working store the same input is audited "success", files the
// retro, and the worth_remembering item reaches last-session.md.
func TestPersistResponse_WriteOK_AuditedSuccessAndRetroFiled(t *testing.T) {
	rr, auditDir, roomRoot := newRecapPersistFixture(t, "sess_ls_ok")

	rr.persistResponse()

	require.Equal(t, "success", lastRecapOutcome(t, auditDir))
	require.True(t, rr.al.agentSessionHasRetro(rr.agentInst, rr.sessionID))
	got, err := os.ReadFile(filepath.Join(roomRoot, memrooms.LastSessionFile))
	require.NoError(t, err)
	require.True(t, strings.Contains(string(got), "ORCHID-7"))
}

// A failed retro append on the success path is a visible failure too: the audit
// outcome names the failed step, never "success". The retro is the marker that
// blocks a later recap, and it was not written, so a later pass can retry; the
// recap itself did land in last-session.md.
func TestPersistResponse_RetroAppendFails_AuditedAndRetryable(t *testing.T) {
	rr, auditDir, roomRoot := newRecapPersistFixture(t, "sess_retro_fail")

	// A regular file where the retros directory belongs: the append cannot create it.
	require.NoError(t, os.WriteFile(filepath.Join(roomRoot, "retros"), []byte("x"), 0o600))

	rr.persistResponse()

	outcome := lastRecapOutcome(t, auditDir)
	require.Equal(t, "write_failed:retro", outcome, "a failed retro append must not be audited as success")
	require.NotContains(t, outcome, roomRoot, "no filesystem path may reach the audit string")
	require.False(t, rr.al.agentSessionHasRetro(rr.agentInst, rr.sessionID),
		"no retro exists, so a later recap pass is not blocked")
	got, err := os.ReadFile(filepath.Join(roomRoot, memrooms.LastSessionFile))
	require.NoError(t, err, "the recap itself did land")
	require.True(t, strings.Contains(string(got), "ORCHID-7"))
}
