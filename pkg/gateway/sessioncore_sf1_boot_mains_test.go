// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
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
	assert.Contains(t, body, "main chat could not be prepared", "the message says what failed: %s", body)
	assert.Contains(t, body, "retry", "the message says how to recover: %s", body)
	sf1AssertNoInternalDetail(t, env, body, ws, "mia")

	// Recovery: the next open re-attempts the creation and succeeds.
	restore()
	code, body = sf1GetSessionBody(env, u1MainID(ws, "mia"))
	assert.Equal(t, http.StatusOK, code, "once storage works the on-demand open creates the main: %s", body)
}

// sf1AssertNoInternalDetail asserts a client-visible body carries no
// filesystem path, session-file name, session or identity id, home-directory
// fragment, or stored owner/workspace value.
func sf1AssertNoInternalDetail(t *testing.T, env *u1Env, body string, forbidden ...string) {
	t.Helper()
	home, _ := os.UserHomeDir()
	frags := append([]string{
		env.home, env.store(t).BaseDir(), os.TempDir(), "/Users/", "/var/", "/tmp", "~",
		"meta.json", ".jsonl", "main-session-", "OMNIPUS_HOME",
	}, forbidden...)
	if home != "" {
		frags = append(frags, home)
	}
	for _, f := range frags {
		if f == "" {
			continue
		}
		assert.NotContains(t, body, f, "the client body must not leak %q: %s", f, body)
	}
}

// A stored main whose identity contradicts its pair is also answered with the
// fixed message only - the stored owner and workspace never reach the client.
func TestSF1_MismatchedStoredMainLeaksNoStoredIdentity(t *testing.T) {
	env := u1NewEnv(t, false)
	const ws = "01JSF1WS0000000000000000G"
	env.seedWorkspace(t, ws, false, "mia")
	u1SeedStoredMain(t, env, u1MainID(ws, "mia"), "jim", "some-other-workspace-xyz")

	code, body := sf1GetSessionBody(env, u1MainID(ws, "mia"))
	assert.Equal(t, http.StatusInternalServerError, code)
	assert.Contains(t, body, "main chat could not be prepared")
	sf1AssertNoInternalDetail(t, env, body, ws, "jim", "some-other-workspace-xyz", "mia", "type=")
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

// I1: boot over a saved default and a non-default workspace, twice. The default
// team lists a CORRUPT member BEFORE a healthy one, so the test proves creation
// continues past a failure; the non-default workspace has no main before boot,
// so boot itself must create it there; one healthy main already exists and must
// be reused byte-for-byte. The full main set, the preserved corrupt bytes and an
// identifying error log are asserted.
func TestBootMains_AcrossSavedWorkspacesWithExistingAndCorruptMains(t *testing.T) {
	logs := sf1CaptureSlog(t)
	env := u1NewEnv(t, false)
	const def, other = "01JSF1DEFAULT00000000000C", "01JSF1OTHERWS000000000000D"
	env.seedWorkspace(t, def, true, "jim", "mia") // jim (corrupt) is listed BEFORE mia (healthy)
	env.seedWorkspace(t, other, false, "mia", "jim")

	// An existing, healthy main for (other, jim): created before boot. Nothing
	// exists yet for (other, mia) or (def, mia) - boot must create those.
	existing, err := env.store(t).GetOrCreateMainSession(other, "jim")
	require.NoError(t, err)
	existingPath := filepath.Join(env.store(t).BaseDir(), existing.ID, "meta.json")
	existingBefore, err := os.ReadFile(existingPath)
	require.NoError(t, err)
	require.NoDirExists(t, filepath.Join(env.store(t).BaseDir(), u1MainID(other, "mia")),
		"precondition: the non-default workspace's main must not exist before boot")

	// A corrupt main for (def, jim).
	corruptDir := filepath.Join(env.store(t).BaseDir(), u1MainID(def, "jim"))
	require.NoError(t, os.MkdirAll(corruptDir, 0o700))
	corruptBytes := []byte("{not json")
	corruptPath := filepath.Join(corruptDir, "meta.json")
	require.NoError(t, os.WriteFile(corruptPath, corruptBytes, 0o600))

	for boot := 1; boot <= 2; boot++ {
		env.api.ensureBootMains()

		// The harness seeds its own default workspace; only the two workspaces
		// this test saved are under assertion.
		var got []string
		for _, id := range u1Sorted(env.storedOfType(t, "main")) {
			if strings.HasPrefix(id, "main-session-"+def+"+") || strings.HasPrefix(id, "main-session-"+other+"+") {
				got = append(got, id)
			}
		}
		want := []string{u1MainID(def, "admin"), u1MainID(def, "mia"), u1MainID(other, "jim"), u1MainID(other, "mia")}
		assert.Equal(t, want, got, "boot %d: Admin's, the healthy default member (created AFTER the corrupt one failed), the reused existing main and the main boot created in the non-default workspace; no replacement for the corrupt one", boot)

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

// I2: Admin's main address reaches the real workspace JSON response - validated
// against the generated Workspace schema - only on the default workspace and
// only once the main resolves.
func TestWorkspaceJSON_AdminMainSessionIDThroughTheRealResponse(t *testing.T) {
	env := u1NewEnv(t, false)
	const def, other = "01JSF1DEFAULT00000000000E", "01JSF1OTHERWS000000000000F"
	env.seedWorkspace(t, def, true, "mia")
	env.seedWorkspace(t, other, false, "mia")

	raw := func(id string) ([]byte, map[string]any) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+id, nil)
		r.URL.Path = "/api/v1/workspaces/" + id
		env.api.HandleWorkspaces(w, r)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var m map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &m))
		return w.Body.Bytes(), m
	}
	contractsDir := filepath.Join(filepath.Dir(gatewayTestCallerFile(t)), "..", "..", "contracts", "components", "schemas")
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(newYAMLSchemaLoader(t))
	schema, err := compiler.Compile("file://" + filepath.Join(contractsDir, "Workspace.yaml"))
	require.NoError(t, err, "must compile Workspace.yaml")
	validate := func(body []byte) {
		var doc any
		require.NoError(t, json.Unmarshal(body, &doc))
		assert.NoError(t, schema.Validate(doc), "the workspace response must validate against Workspace.yaml: %s", body)
	}

	before, m := raw(def)
	validate(before)
	_, present := m["admin_main_session_id"]
	assert.False(t, present, "before the main exists the field is omitted, never a guessed id")

	env.api.ensureBootMains()

	body, m := raw(def)
	validate(body)
	got, ok := m["admin_main_session_id"].(string)
	require.True(t, ok, "the default workspace JSON carries admin_main_session_id once Admin's main resolves")
	assert.Equal(t, u1MainID(def, "admin"), got)
	assert.LessOrEqual(t, len(got), 255, "the contract bounds the id at 255")
	assert.Nil(t, u1MemberConfig(m, "admin"), "no fake membership entry for Admin")

	body, m = raw(other)
	validate(body)
	_, present = m["admin_main_session_id"]
	assert.False(t, present, "a non-default workspace never carries it")
}
