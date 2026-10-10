// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Round 5 (REVIEW-mainfix-r5): every client-visible failure text - an HTTP error
// body, an HTTP 200 diagnostic field, a WebSocket error frame - is fixed text.
// The underlying cause (absolute paths, file names, session ids, OS errors)
// goes to the server log only. An agent's own id or name may appear (founder
// ruling); a session id or a storage file name may not.

// r5Leaks are the fragments no client body may contain.
var r5OSFragments = []string{
	"permission denied", "no such file", "not a directory", "read-only file system",
	"operation not permitted", "is a directory", "no space left", "input/output error",
	"/Users/", "/var/", "/tmp", "/private/", "meta.json", ".jsonl", "manifest.json",
	"USER.md", "config.json", "agent.json", "goals/", "OMNIPUS_HOME",
}

func r5AssertClean(t *testing.T, body string, extra ...string) {
	t.Helper()
	lower := strings.ToLower(body)
	for _, f := range append(append([]string{}, r5OSFragments...), extra...) {
		if f == "" {
			continue
		}
		assert.NotContains(t, lower, strings.ToLower(f), "the client body must not carry %q: %s", f, body)
	}
}

// r5Block chmods path for the test and restores it on cleanup.
func r5Block(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err, "instrument check: %s must exist before it is blocked", path)
	orig := info.Mode().Perm()
	require.NoError(t, os.Chmod(path, mode))
	t.Cleanup(func() { _ = os.Chmod(path, orig) })
}

// hostileLimitStore fails MutateState with an error that carries an agent id,
// an absolute path and an OS cause - the shape the real agent store produces.
type hostileLimitStore struct {
	*agentstore.Store
	failOn map[string][]bool
	calls  map[string]int
	cause  error
}

func (f *hostileLimitStore) MutateState(id, rev string, mutate func(*config.AgentConfig) error, soul *string) (agentstore.MutationResult, error) {
	n := f.calls[id]
	f.calls[id] = n + 1
	if plan := f.failOn[id]; n < len(plan) && plan[n] {
		return agentstore.MutationResult{}, f.cause
	}
	return f.Store.MutateState(id, rev, mutate, soul)
}

// F2: a failed rollback keeps its signal (code, the unrestored agent, the
// recovery instruction, a details.cause field) but the cause is fixed text.
func TestR5_F2_RollbackIncompleteCauseIsFixedText(t *testing.T) {
	api := newLimitAPI(t, 300,
		config.AgentConfig{ID: "agent-a", Name: "A", MaxToolIterations: 250},
		config.AgentConfig{ID: "agent-b", Name: "B", MaxToolIterations: 280})
	hostile := errors.New("agent-b: replace /secret/home/agents/agent-b/agent.json: permission denied")
	api.limitAgentStore = &hostileLimitStore{Store: agentstore.New(api.homePath),
		failOn: map[string][]bool{"agent-b": {true}, "agent-a": {false, true}}, calls: map[string]int{}, cause: hostile}

	w := putPerformanceJSON(t, api, `{"max_tool_iterations":200,"confirmed_lowering":`+
		`[{"agent_id":"agent-a","old_value":250},{"agent_id":"agent-b","old_value":280}]}`, true)

	require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
	e := gateFixErr(t, w)
	require.NotNil(t, e.Code)
	assert.Equal(t, "max_tool_iterations_rollback_incomplete", *e.Code, "the rollback-failure signal stays")
	assert.Contains(t, e.Error, "could not restore A (now 200, was 250)", "the unrestored agent and the recovery stay")
	require.NotNil(t, e.Details)
	cause, ok := (*e.Details)["cause"].(string)
	require.True(t, ok, "details.cause stays present as text: %v", *e.Details)
	assert.NotEmpty(t, cause)
	r5AssertClean(t, w.Body.String(), "secret", "replace ")
	assert.NotContains(t, w.Body.String(), hostile.Error())
}

// F4: a failed config write names no file.
func TestR5_F4_PerformanceConfigWriteFailureNamesNoFile(t *testing.T) {
	api := newLimitAPI(t, 300)
	r5Block(t, filepath.Join(api.homePath, "config.json"), 0o000)

	w := putPerformanceJSON(t, api, `{"max_tool_iterations":400}`, true)

	require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
	r5AssertClean(t, w.Body.String())
}

func r5Upload(t *testing.T, api *restAPI, workspaceID string) *httptest.ResponseRecorder {
	t.Helper()
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	require.NoError(t, mw.WriteField("workspace_id", workspaceID))
	fw, err := mw.CreateFormFile("file", "doc.txt")
	require.NoError(t, err)
	_, _ = io.WriteString(fw, "hello")
	require.NoError(t, mw.Close())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/upload", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rr := httptest.NewRecorder()
	api.HandleUpload(rr, req)
	return rr
}

// F1: the three workspace-upload storage failures answer fixed text.
func TestR5_F1_WorkspaceUploadStorageFailuresAreFixedText(t *testing.T) {
	t.Run("library cannot load", func(t *testing.T) {
		api := newWorkspaceLibraryTestAPI(t)
		mediaDir := filepath.Join(api.homePath, "workspaces", "ws-r5", "media")
		require.NoError(t, os.MkdirAll(mediaDir, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(mediaDir, "manifest.json"), []byte("{not valid json"), 0o600))
		rr := r5Upload(t, api, "ws-r5")
		require.Equal(t, http.StatusInternalServerError, rr.Code, rr.Body.String())
		r5AssertClean(t, rr.Body.String(), "ws-r5")
	})
	t.Run("media store write fails", func(t *testing.T) {
		api := newWorkspaceLibraryTestAPI(t)
		mediaDir := filepath.Join(api.homePath, "workspaces", "ws-r5b", "media")
		require.NoError(t, os.MkdirAll(mediaDir, 0o700))
		r5Block(t, mediaDir, 0o500)
		rr := r5Upload(t, api, "ws-r5b")
		require.Equal(t, http.StatusInternalServerError, rr.Code, rr.Body.String())
		r5AssertClean(t, rr.Body.String(), "ws-r5b")
	})
	t.Run("staging the agent-visible copy fails", func(t *testing.T) {
		api := newWorkspaceLibraryTestAPI(t)
		wsDir := filepath.Join(api.homePath, "workspaces", "ws-r5c")
		workDir := filepath.Join(wsDir, "work")
		require.NoError(t, os.MkdirAll(workDir, 0o700))
		r5Block(t, workDir, 0o500)
		rr := r5Upload(t, api, "ws-r5c")
		require.Equal(t, http.StatusInternalServerError, rr.Code, rr.Body.String())
		r5AssertClean(t, rr.Body.String(), "ws-r5c")
	})
}

// F1: a default-model save that cannot write the config answers fixed text.
func TestR5_F1_DefaultModelSaveFailureIsFixedText(t *testing.T) {
	rows := []*config.ModelConfig{
		{Name: "before", Provider: "provider-a", Model: "model-before", APIBase: "https://a.example/v1", Protocol: "openai-compatible"},
		{Name: "after", Provider: "provider-b", Model: "model-after", APIBase: "https://b.example/v1", Protocol: "openai-compatible"},
	}
	api, tmpDir, _ := newDefaultModelAPI(t, config.DefaultModel{Provider: "provider-a", Model: "model-before"}, rows, &restMockProvider{})
	r5Block(t, filepath.Join(tmpDir, "config.json"), 0o000)
	w := doDefaultModel(api, http.MethodPut, `{"provider":"provider-b","model":"model-after"}`)
	require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
	r5AssertClean(t, w.Body.String(), tmpDir)
}

// r5NewWSHandler builds a WSHandler over a real agent loop.
func r5NewWSHandler(t *testing.T) *WSHandler {
	t.Helper()
	env := u1NewEnv(t, false)
	h := newWSHandler(bus.NewMessageBus(), env.api.agentLoop, "")
	t.Cleanup(h.Wait)
	return h
}

// drainConnFrames returns every queued server->client frame of wc as text.
func drainConnFrames(wc *wsConn) string {
	var sb strings.Builder
	for {
		select {
		case b := <-wc.sendCh:
			sb.Write(b)
			sb.WriteByte('\n')
		default:
			return sb.String()
		}
	}
}

// F1: a WebSocket Stop whose lifecycle journal cannot be opened answers the
// ErrorFrame with fixed text - no session id, no path, no OS error.
func TestR5_F1_WebSocketStopFailureIsFixedText(t *testing.T) {
	h := r5NewWSHandler(t)
	lc := session.NewLifecycleStore(t.TempDir())
	h.agentLoop.SetSessionMessagingStores(session.NewMessageInboxStore(t.TempDir()), lc)
	const sid = "session_r5stopleak0001"
	require.NoError(t, lc.Persist(&session.LifecycleRecord{
		SessionID: sid, Generation: 1, State: session.LifecycleRunning, OwnerScopeKind: session.OwnerScopeHuman,
		WorkspaceID: "ws-1", AgentID: "ann",
	}))
	r5Block(t, filepath.Join(lc.Dir(), sid+".jsonl"), 0o000)
	wc := makeTestConn()

	h.handleCancelWithScope(wc, sid, false)

	frames := drainConnFrames(wc)
	require.Contains(t, frames, `"type":"error"`, "the Stop failure must still be reported: %s", frames)
	r5AssertClean(t, frames, sid, lc.Dir())
}

// F4: the settled-Stop failure notice names no session id.
func TestR5_F4_StopSettledFailureNoticeNamesNoSession(t *testing.T) {
	h := r5NewWSHandler(t)
	wc := makeTestConn()
	hooks := h.buildCancelHooks(wc)
	require.NotNil(t, hooks.OnStopSettled)
	hooks.OnStopSettled("session_r5settled0001", errors.New("boom"))
	frames := drainConnFrames(wc)
	require.Contains(t, frames, `"type":"error"`, frames)
	assert.NotContains(t, frames, `"message":"Stop for session session_r5settled0001`)
	// The frame's own session_id correlation field is the documented contract
	// carrier and stays; the prose must not repeat it.
	var f struct {
		Message string `json:"message"`
	}
	line := strings.SplitN(frames, "\n", 2)[0]
	require.NoError(t, json.Unmarshal([]byte(line), &f))
	assert.NotContains(t, f.Message, "session_r5settled0001")
	assert.Contains(t, strings.ToLower(f.Message), "stop")
}

// F1: the knowledge-base detection diagnostic travels inside an HTTP 200; its
// message is fixed text too.
func TestR5_F1_KnowledgeDetectionDiagnosticIsFixedText(t *testing.T) {
	api, ws := buildLibraryTestAPI(t)
	vault := filepath.Join(workDir(api, ws), "vault")
	makeKnowledgeBase(t, vault, "Vault")
	r5Block(t, vault, 0o000)

	w := knowledgeGet(t, api, "/api/v1/library/"+ws+"/knowledge?path=vault")

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "detection_error", "the diagnostic itself stays: %s", w.Body.String())
	r5AssertClean(t, w.Body.String(), vault, api.homePath)
}

// F1: the MCP connection test is an HTTP 200 with a message; the message does
// not carry the OS error or the configured command path.
func TestR5_F1_MCPConnectionTestMessageIsFixedText(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	cmd := "/nonexistent-binary-for-r5-leak"
	bodyBytes, err := json.Marshal(gen.McpServerCreate{Name: "r5-srv", Transport: gen.McpServerCreateTransportStdio, Command: &cmd})
	require.NoError(t, err)
	wPost := httptest.NewRecorder()
	rPost := httptest.NewRequest(http.MethodPost, "/api/v1/mcp-servers", bytes.NewReader(bodyBytes))
	rPost.Header.Set("Content-Type", "application/json")
	api.HandleMCPServers(wPost, rPost)
	require.Equal(t, http.StatusCreated, wPost.Code)

	w := httptest.NewRecorder()
	api.HandleMCPServers(w, httptest.NewRequest(http.MethodPost, "/api/v1/mcp-servers/r5-srv/test", nil))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp gen.McpServerTestResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp.Success)
	assert.NotEmpty(t, resp.Message, "the failure is still reported")
	r5AssertClean(t, w.Body.String(), "nonexistent-binary", api.homePath)
}

// F1: the executor smoke test answers a result with ok=false and an error
// field inside an HTTP 200; the early preparation failures carry fixed text.
func TestR5_F1_ExecutorSmokeTestPreparationFailureIsFixedText(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	r5Block(t, home, 0o500)
	stub := writeScript(t, "#!/bin/sh\nexit 0\n")

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents/executor-smoke-test",
		strings.NewReader(`{"cli":"claude-code","cli_path":`+strconvQuote(stub)+`}`))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = t.Name() + ":0"
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp gen.ExecutorSmokeTestResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.False(t, resp.Ok)
	require.NotNil(t, resp.Error, "the failure is still reported")
	r5AssertClean(t, w.Body.String(), home)
}

// F1: a task whose Definition of Done cannot be stored answers fixed text and
// keeps saying what was lost.
func TestR5_F1_TaskGoalRecordFailureIsFixedText(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)
	id := createTaskWithGoal(t, api, wsID, "r5 goal write fails")
	duplicateGoalRecordForOwner(t, api, id)
	newCriteria := `[{"text":"an edited criterion","author":{"kind":"user","id":"tester"},"status":"pending"}]`

	w := patchTaskJSON(t, api, id, `{"criteria":`+newCriteria+`,"dod":`+validDoDJSON+`}`)

	require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "Definition of Done could not be persisted")
	r5AssertClean(t, w.Body.String(), "goal", api.homePath)
}

// F1: a channel toggle whose reload fails says so without the reload cause.
func TestR5_F1_ChannelToggleReloadFailureIsFixedText(t *testing.T) {
	api := newChannelTestAPI(t,
		`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{"telegram":{"enabled":true}}}`)
	api.agentLoop.SetReloadFunc(func() error {
		return errors.New("reload: open /secret/home/channels/telegram.json: permission denied")
	})
	w := httptest.NewRecorder()
	api.setChannelEnabled(w, "telegram", false)
	require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "saved")
	r5AssertClean(t, w.Body.String(), "secret", "reload:")
}

// F4: the user profile errors do not name the file.
func TestR5_F4_UserProfileFailureNamesNoFile(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	r5Block(t, home, 0o500)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/user-context", strings.NewReader(`{"content":"hello"}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleUserContext(w, withAdminRole(r))
	require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
	r5AssertClean(t, w.Body.String(), home)
}

// F3: a mount target inside the server's data directory is refused with a fixed
// reason - the resolved server home and its stored file names are not echoed.
func TestR5_F3_MountRefusalDoesNotEchoServerHome(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	t.Setenv("OMNIPUS_HOME", api.homePath)
	id := createTestWorkspace(t, api, "r5 mounts")
	inside := filepath.Join(api.homePath, "workspaces")
	require.NoError(t, os.MkdirAll(inside, 0o700))
	body := `{"name":"inside","host_path":` + strconvQuote(inside) + `}`
	r := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/"+id+"/mounts", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.handleWorkspaceMountCreate(w, r, id)
	require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
	r5AssertClean(t, w.Body.String(), api.homePath, "master.key", "Omnipus data directory (\"")
}

// F3: a mount target that cannot be inspected is answered with fixed text, not
// the wrapped OS error.
func TestR5_F3_MountTargetStatFailureDoesNotEchoTheOSError(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	id := createTestWorkspace(t, api, "r5 mounts stat")
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	body := `{"name":"gone","host_path":` + strconvQuote(missing) + `}`
	r := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/"+id+"/mounts", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.handleWorkspaceMountCreate(w, r, id)
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	r5AssertClean(t, w.Body.String(), missing)
}

// F3: a stored config whose agents section no longer decodes is a server-state
// failure (not the caller's 400) and does not name the file.
func TestR5_F3_StoredConfigDecodeFailureIsAServerFailure(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	require.NoError(t, os.WriteFile(filepath.Join(api.homePath, "config.json"),
		[]byte(`{"version":1,"agents":"not-an-object"}`), 0o600))
	r := httptest.NewRequest(http.MethodPut, "/api/v1/config", strings.NewReader(`{"gateway":{"log_level":"info"}}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.updateConfig(w, r)
	assert.Equal(t, http.StatusInternalServerError, w.Code, "a stored-state failure is not a 400: %s", w.Body.String())
	r5AssertClean(t, w.Body.String())
}
