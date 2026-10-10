// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sf1CaptureSlog routes the default logger into a buffer for the test.
func sf1CaptureSlog(t *testing.T) func() string {
	t.Helper()
	var mu sync.Mutex
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&sf1LockedWriter{mu: &mu, w: &buf}, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		return buf.String()
	}
}

type sf1LockedWriter struct {
	mu *sync.Mutex
	w  *bytes.Buffer
}

func (l *sf1LockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// sf1GetSessionBody is env.getSession plus the raw error body, for tests that
// assert the message a user would see.
func sf1GetSessionBody(env *u1Env, id string) (int, string) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+id, nil)
	r.URL.Path = "/api/v1/sessions/" + id
	env.api.HandleSessions(w, r)
	return w.Code, w.Body.String()
}

// SF-1: a main that could not be created at boot (here: the session store is
// unwritable) is REPORTED (returned error naming workspace and agent, logged at
// error level), startup stays nonfatal, and the on-demand open re-attempts the
// creation and returns the real, actionable cause instead of "session not
// found" - then heals once the storage works again.
func TestSF1_BootMainFailureIsReportedAndOnDemandOpenIsActionable(t *testing.T) {
	logs := sf1CaptureSlog(t)
	env := u1NewEnv(t, false)
	const ws = "01JSF1WS0000000000000000A"
	env.seedWorkspace(t, ws, false, "mia")
	base := env.store(t).BaseDir()

	require.NoError(t, os.Chmod(base, 0o500))
	restore := func() { _ = os.Chmod(base, 0o700) }
	defer restore()
	probe := filepath.Join(base, "probe-unwritable")
	if werr := os.Mkdir(probe, 0o700); werr == nil {
		_ = os.Remove(probe)
		t.Fatal("instrument check: the session store must really be unwritable (tests must not run as root)")
	}

	stored, err := readWorkspaceFile(env.home, ws)
	require.NoError(t, err)
	mainsErr := env.api.ensureMainsForTeam(stored)
	require.Error(t, mainsErr, "a failed main creation must be returned, not swallowed")
	assert.Contains(t, mainsErr.Error(), ws, "the error names the workspace")
	assert.Contains(t, mainsErr.Error(), "mia", "the error names the agent")

	// Boot stays nonfatal and logs at ERROR with workspace and agent.
	env.api.ensureBootMains()
	out := logs()
	assert.Contains(t, out, "level=ERROR", "boot logs the failure at error level")
	assert.Contains(t, out, ws)
	assert.Contains(t, out, "mia")

	code, body := sf1GetSessionBody(env, u1MainID(ws, "mia"))
	assert.NotEqual(t, http.StatusNotFound, code, "an entitled pair with a storage failure is not 'not found'")
	assert.NotContains(t, body, "session not found")
	assert.Contains(t, body, "mia", "the message names the agent: %s", body)
	assert.Contains(t, body, "retry", "the message says how to recover: %s", body)

	// Recovery: the next open re-attempts the creation and succeeds.
	restore()
	code, body = sf1GetSessionBody(env, u1MainID(ws, "mia"))
	assert.Equal(t, http.StatusOK, code, "once storage works the on-demand open creates the main: %s", body)
}

// A pair that is simply not entitled to a main keeps the plain 404 - the error
// path is for entitled pairs that could not be served.
func TestSF1_NonMemberLookupStaysNotFound(t *testing.T) {
	env := u1NewEnv(t, false)
	const ws = "01JSF1WS0000000000000000B"
	env.seedWorkspace(t, ws, false, "mia")
	code, body := sf1GetSessionBody(env, u1MainID(ws, "jim"))
	assert.Equal(t, http.StatusNotFound, code, body)
}

// I1: boot over a saved default and a non-default workspace, an existing main,
// and a corrupt main beside healthy pairs, twice. The full main set, identity
// reuse, the corrupt bytes, the healthy creations and an identifying error log
// are all asserted.
func TestBootMains_AcrossSavedWorkspacesWithExistingAndCorruptMains(t *testing.T) {
	logs := sf1CaptureSlog(t)
	env := u1NewEnv(t, false)
	const def, other = "01JSF1DEFAULT00000000000C", "01JSF1OTHERWS000000000000D"
	env.seedWorkspace(t, def, true, "mia", "jim")
	env.seedWorkspace(t, other, false, "mia")

	// An existing, healthy main for (other, mia): created before boot.
	existing, err := env.store(t).GetOrCreateMainSession(other, "mia")
	require.NoError(t, err)
	existingPath := filepath.Join(env.store(t).BaseDir(), existing.ID, "meta.json")
	existingBefore, err := os.ReadFile(existingPath)
	require.NoError(t, err)

	// A corrupt main for (def, jim) beside the healthy (def, mia).
	corruptID := u1MainID(def, "jim")
	corruptDir := filepath.Join(env.store(t).BaseDir(), corruptID)
	require.NoError(t, os.MkdirAll(corruptDir, 0o700))
	corruptBytes := []byte("{not json")
	corruptPath := filepath.Join(corruptDir, "meta.json")
	require.NoError(t, os.WriteFile(corruptPath, corruptBytes, 0o600))

	for boot := 1; boot <= 2; boot++ {
		env.api.ensureBootMains()

		// The harness seeds its own default workspace; only the two workspaces this
		// test saved are under assertion.
		var got []string
		for _, id := range u1Sorted(env.storedOfType(t, "main")) {
			if strings.HasPrefix(id, "main-session-"+def+"+") || strings.HasPrefix(id, "main-session-"+other+"+") {
				got = append(got, id)
			}
		}
		want := []string{u1MainID(def, "admin"), u1MainID(def, "mia"), u1MainID(other, "mia")}
		assert.Equal(t, want, got, "boot %d: exactly Admin's, the healthy default pair and the existing pair; no replacement for the corrupt one", boot)

		after, err := os.ReadFile(existingPath)
		require.NoError(t, err)
		assert.Equal(t, string(existingBefore), string(after), "boot %d: the existing main's identity is reused, not rewritten", boot)
		cb, err := os.ReadFile(corruptPath)
		require.NoError(t, err)
		assert.Equal(t, string(corruptBytes), string(cb), "boot %d: corrupt bytes are preserved, not repaired or replaced", boot)
	}

	out := logs()
	assert.True(t, strings.Contains(out, "level=ERROR") && strings.Contains(out, def) && strings.Contains(out, "jim"),
		"the corrupt pair is identified by workspace and agent in an error log: %s", out)
}

// I2: Admin's main address reaches the real workspace JSON response, only on
// the default workspace and only once the main resolves.
func TestWorkspaceJSON_AdminMainSessionIDThroughTheRealResponse(t *testing.T) {
	env := u1NewEnv(t, false)
	const def, other = "01JSF1DEFAULT00000000000E", "01JSF1OTHERWS000000000000F"
	env.seedWorkspace(t, def, true, "mia")
	env.seedWorkspace(t, other, false, "mia")

	_, present := env.getWorkspace(t, def)["admin_main_session_id"]
	assert.False(t, present, "before the main exists the field is omitted, never a guessed id")

	env.api.ensureBootMains()

	got, ok := env.getWorkspace(t, def)["admin_main_session_id"].(string)
	require.True(t, ok, "the default workspace JSON carries admin_main_session_id once Admin's main resolves")
	assert.Equal(t, u1MainID(def, "admin"), got)
	_, present = env.getWorkspace(t, other)["admin_main_session_id"]
	assert.False(t, present, "a non-default workspace never carries it")
	assert.Nil(t, u1MemberConfig(env.getWorkspace(t, def), "admin"), "no fake membership entry for Admin")
}
