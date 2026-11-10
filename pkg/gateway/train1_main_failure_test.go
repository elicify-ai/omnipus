package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Oracles: Train 1 UAT item 1, FR-030 / §6.3, and the architect's
// 2026-10-20 main_session_error ruling. A saved team is not proof of a usable
// main. Only a safe classified cause may cross the member response boundary.
func TestTrain1WorkspaceMainFailureReasonAndRetry(t *testing.T) {
	env := u1NewEnv(t, false)
	const ws = "train1-main-fault"
	const wantReason = "The main chat could not be prepared because session storage has a file where a folder is required. Ask the server operator to fix it, then retry."
	env.seedWorkspace(t, ws, false)
	badID := u1MainID(ws, "mia")
	blocker := filepath.Join(env.store(t).BaseDir(), badID)
	const sentinel = "preserve the unreadable stored identity"
	require.NoError(t, os.WriteFile(blocker, []byte(sentinel), 0o600))
	_, err := os.ReadFile(filepath.Join(blocker, "meta.json"))
	require.Error(t, err, "instrument: the stored identity must genuinely be unreadable")
	require.NotErrorIs(t, err, os.ErrNotExist, "this is not confirmed absence")

	put := env.putTeam(t, ws, "mia", "jim", "worker1")
	require.Equal(t, http.StatusOK, put.Code, put.Body.String())
	var saved map[string]any
	require.NoError(t, json.Unmarshal(put.Body.Bytes(), &saved))
	assert.Equal(t, []any{"mia", "jim", "worker1"}, saved["core_team"], "membership saved despite main failure")

	for _, response := range []map[string]any{saved, env.getWorkspace(t, ws)} {
		failed := u1MemberConfig(response, "mia")
		require.NotNil(t, failed, "an error-only member must be projected without saved member settings")
		assert.NotContains(t, failed, "main_session_id", "the failed identity is not advertised")
		assert.Equal(t, wantReason, failed["main_session_error"])
		reason, ok := failed["main_session_error"].(string)
		require.True(t, ok)
		sf1AssertNoInternalDetail(t, env, reason, ws, "mia")
		healthy := u1MemberConfig(response, "jim")
		require.NotNil(t, healthy)
		assert.Equal(t, u1MainID(ws, "jim"), healthy["main_session_id"])
		assert.NotContains(t, healthy, "main_session_error", "a healthy sibling never inherits the failure")
		worker := u1MemberConfig(response, "worker1")
		assert.NotContains(t, worker, "main_session_id")
		assert.NotContains(t, worker, "main_session_error", "ineligibility is not a preparation failure")
	}
	bytes, err := os.ReadFile(blocker)
	require.NoError(t, err)
	assert.Equal(t, sentinel, string(bytes), "read/retry must not repair or replace the blocking identity")

	// Client-supplied read-only fields cannot hide the real failure or survive
	// into the durable workspace. No existing assertions are changed.
	spoof := env.put(t, ws, map[string]any{"member_configs": map[string]any{
		"mia": map[string]any{"main_session_error": "pretend ready", "main_session_id": "fake-main"},
	}})
	require.Equal(t, http.StatusOK, spoof.Code, spoof.Body.String())
	assert.Equal(t, wantReason, u1MemberConfig(env.getWorkspace(t, ws), "mia")["main_session_error"])
	record, err := os.ReadFile(filepath.Join(env.home, "workspaces", ws+".json"))
	require.NoError(t, err)
	assert.NotContains(t, string(record), "main_session_error", "the error is a live projection, never persisted")
	assert.NotContains(t, string(record), "fake-main")

	// Operator repair is fixture setup, never a server repair. The exact same
	// detail GET used by Retry must now establish the genuinely absent main.
	require.NoError(t, os.Remove(blocker))
	recovered := env.getWorkspace(t, ws)
	mia := u1MemberConfig(recovered, "mia")
	require.NotNil(t, mia)
	assert.Equal(t, badID, mia["main_session_id"])
	assert.NotContains(t, mia, "main_session_error")
	before, err := os.ReadFile(filepath.Join(blocker, "meta.json"))
	require.NoError(t, err)
	assert.Equal(t, badID, u1MemberConfig(env.getWorkspace(t, ws), "mia")["main_session_id"])
	after, err := os.ReadFile(filepath.Join(blocker, "meta.json"))
	require.NoError(t, err)
	assert.Equal(t, before, after, "repeated detail reads reuse the same saved identity")
}

func TestTrain1WorkspaceMainPreparationIsDetailOnly(t *testing.T) {
	env := u1NewEnv(t, false)
	const ws = "train1-main-detail"
	env.seedWorkspace(t, ws, false, "mia", "worker1")
	mainDir := filepath.Join(env.store(t).BaseDir(), u1MainID(ws, "mia"))
	require.NoDirExists(t, mainDir)
	list := httptest.NewRecorder()
	env.api.HandleWorkspaces(list, httptest.NewRequest(http.MethodGet, "/api/v1/workspaces", nil))
	require.Equal(t, http.StatusOK, list.Code, list.Body.String())
	require.NoDirExists(t, mainDir, "listing workspaces must not prepare sessions")
	stored, err := readWorkspaceFile(env.home, ws)
	require.NoError(t, err)
	_ = workspaceToWire(env.api, stored, 0)
	require.NoDirExists(t, mainDir, "the shared serializer must remain read-only")

	opened := env.getWorkspace(t, ws)
	require.DirExists(t, mainDir, "the workspace-detail Retry path must actually prepare an absent main")
	assert.Equal(t, u1MainID(ws, "mia"), u1MemberConfig(opened, "mia")["main_session_id"])
	assert.NotContains(t, u1MemberConfig(opened, "mia"), "main_session_error")
	assert.NoDirExists(t, filepath.Join(env.store(t).BaseDir(), u1MainID(ws, "worker1")), "no main is minted for an ineligible worker")
}

func TestTrain1WorkspaceMainFailuresKeepSafeFallback(t *testing.T) {
	for _, kind := range []string{"corrupt", "foreign identity"} {
		t.Run(kind, func(t *testing.T) {
			env := u1NewEnv(t, false)
			const ws = "train1-main-safe"
			env.seedWorkspace(t, ws, false, "mia")
			id := u1MainID(ws, "mia")
			metaPath := filepath.Join(env.store(t).BaseDir(), id, "meta.json")
			if kind == "corrupt" {
				require.NoError(t, os.MkdirAll(filepath.Dir(metaPath), 0o700))
				require.NoError(t, os.WriteFile(metaPath, []byte("{broken secret-marker"), 0o600))
			} else {
				u1SeedStoredMain(t, env, id, "foreign-owner-marker", "foreign-workspace-marker")
			}
			before, err := os.ReadFile(metaPath)
			require.NoError(t, err)
			member := u1MemberConfig(env.getWorkspace(t, ws), "mia")
			require.NotNil(t, member)
			assert.NotContains(t, member, "main_session_id")
			const wantFallback = "This agent's main chat could not be prepared because session storage failed. Check disk space and permissions, then retry. Details are in the server log."
			require.Equal(t, wantFallback, member["main_session_error"])
			reason, ok := member["main_session_error"].(string)
			require.True(t, ok)
			sf1AssertNoInternalDetail(t, env, reason, ws, "mia", "secret-marker", "foreign-owner-marker", "foreign-workspace-marker")
			after, err := os.ReadFile(metaPath)
			require.NoError(t, err)
			assert.Equal(t, before, after, "corrupt or foreign identities stay untouched")
		})
	}
}
