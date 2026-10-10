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
	"github.com/elicify-ai/omnipus/pkg/config"
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

var _ = json.Marshal
