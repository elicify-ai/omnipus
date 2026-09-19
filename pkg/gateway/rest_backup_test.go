// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// rest_backup_test.go — WP4 (ADR-0010): local backup/restore is restored
// behind the edition auth-mode switch (config.EditionAuthMode()). These
// tests pin the switch itself at the handler layer: HandleListBackups (and,
// by the same guard, HandleCreateBackup/HandleRestore) must answer 404 in
// platform mode and behave normally in local mode. This is defence in depth
// — the routes are also only registered in local mode, see rest.go — but a
// handler that forgets its own guard is reachable by any caller who already
// has a bearer token, so the guard needs its own test independent of the
// route table.
package gateway

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// withEdition (the config.Edition save/restore helper) lives in auth_mode_test.go.

func newBackupTestAPI(t *testing.T, homePath string) *restAPI {
	t.Helper()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: homePath, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	return &restAPI{
		agentLoop: al,
		homePath:  homePath,
	}
}

// TestHandleListBackups_PlatformMode_404 pins that HandleListBackups refuses
// with a 404 JSON error when the stamped edition derives platform auth mode
// (hosted/desktop) — even though the caller reached the handler directly,
// bypassing the route table's own local-mode-only registration.
func TestHandleListBackups_PlatformMode_404(t *testing.T) {
	withEdition(t, config.EditionHosted)
	home := t.TempDir()
	api := newBackupTestAPI(t, home)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/backups", nil)
	api.HandleListBackups(w, r)

	require.Equal(t, http.StatusNotFound, w.Code, "body=%s", w.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	_, hasError := body["error"]
	assert.True(t, hasError, "404 body must be a gen.ErrorResponse shape with an 'error' field")
}

// TestHandleListBackups_LocalMode_ListsArchives pins the inverse: in local
// mode (the open-source edition's default), the handler is reachable and
// lists the .tar.gz files under $OMNIPUS_HOME/backups.
func TestHandleListBackups_LocalMode_ListsArchives(t *testing.T) {
	withEdition(t, config.EditionCore)
	home := t.TempDir()
	backupsDir := filepath.Join(home, "backups")
	require.NoError(t, os.MkdirAll(backupsDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(backupsDir, "backup-20260101T000000Z.tar.gz"), []byte("x"), 0o600))

	api := newBackupTestAPI(t, home)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/backups", nil)
	api.HandleListBackups(w, r)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var entries []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &entries))
	require.Len(t, entries, 1)
	assert.Equal(t, "backup-20260101T000000Z.tar.gz", entries[0]["filename"])
}

// TestHandleCreateBackup_PlatformMode_404 and TestHandleRestore_PlatformMode_404
// pin the same guard on the other two backup handlers, so the 404 defence in
// depth is proven on all three restored endpoints, not just list.
func TestHandleCreateBackup_PlatformMode_404(t *testing.T) {
	withEdition(t, config.EditionDesktop)
	home := t.TempDir()
	api := newBackupTestAPI(t, home)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/backup", nil)
	api.HandleCreateBackup(w, r)

	require.Equal(t, http.StatusNotFound, w.Code, "body=%s", w.Body.String())
}

func TestHandleRestore_PlatformMode_404(t *testing.T) {
	withEdition(t, config.EditionHosted)
	home := t.TempDir()
	api := newBackupTestAPI(t, home)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/restore", nil)
	api.HandleRestore(w, r)

	require.Equal(t, http.StatusNotFound, w.Code, "body=%s", w.Body.String())
}

// TestHandleCreateBackup_LocalMode_CreatesArchive and
// TestHandleRestore_LocalMode_RestoresArchive prove the restored upstream
// data path still works end to end once the switch is on: create writes a
// real tar.gz under backups/, and restore extracts a known-good archive back
// over $OMNIPUS_HOME (skipping config.json).
func TestHandleCreateBackup_LocalMode_CreatesArchive(t *testing.T) {
	withEdition(t, config.EditionCore)
	home := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.json"), []byte(`{"version":1}`), 0o600))
	api := newBackupTestAPI(t, home)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/backup", nil)
	api.HandleCreateBackup(w, r)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	path, _ := resp["path"].(string)
	require.NotEmpty(t, path, "response must include the created archive's path")
	_, err := os.Stat(path)
	require.NoError(t, err, "the archive must actually exist on disk at the reported path")
}

func TestHandleRestore_LocalMode_RestoresArchive(t *testing.T) {
	withEdition(t, config.EditionCore)
	home := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.json"), []byte(`{"version":1,"original":true}`), 0o600))
	api := newBackupTestAPI(t, home)

	// Create a real backup first (round-trips through the restored createTarGz).
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/backup", nil)
	api.HandleCreateBackup(w, r)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var createResp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &createResp))
	fullPath, _ := createResp["path"].(string)
	filename := filepath.Base(fullPath)

	// Mutate a tracked file, then restore — it should come back.
	agentsDir := filepath.Join(home, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "marker.txt"), []byte("pre-restore"), 0o600))
	require.NoError(t, os.Remove(filepath.Join(agentsDir, "marker.txt")))

	body, err := json.Marshal(map[string]string{"filename": filename})
	require.NoError(t, err)
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodPost, "/api/v1/restore", bytes.NewReader(body))
	r2 = withReAuthAdmin(t, api, r2) // restore takes the step-up gate in local mode
	api.HandleRestore(w2, r2)

	require.Equal(t, http.StatusOK, w2.Code, "body=%s", w2.Body.String())
	// config.json must be preserved (extractTarGz skips it), not overwritten
	// by the archived copy.
	cfgBytes, err := os.ReadFile(filepath.Join(home, "config.json"))
	require.NoError(t, err)
	assert.Contains(t, string(cfgBytes), "original")
}

// TestHandleRestore_RequiresReAuth: a restore overwrites master.key, the
// credential store and every entity, so in local mode it takes the same
// step-up gate as the vault writes — a session alone is refused (403).
func TestHandleRestore_RequiresReAuth(t *testing.T) {
	withEdition(t, config.EditionCore)
	home := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.json"), []byte(`{"version":1}`), 0o600))
	api := newBackupTestAPI(t, home)

	body, err := json.Marshal(map[string]string{"filename": "backup-x.tar.gz"})
	require.NoError(t, err)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/restore", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdminNoToken(r)
	w := httptest.NewRecorder()
	api.HandleRestore(w, r)
	require.Equal(t, http.StatusForbidden, w.Code, "body=%s", w.Body.String())
}

// TestExtractTarGz_RefusesSymlink: an archive entry whose destination is a
// symlink (or sits under one) must not be written through — the lexical
// prefix check cannot see links, so a link left under the home would let a
// restore write outside it.
func TestExtractTarGz_RefusesSymlink(t *testing.T) {
	home := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, "workspaces"), 0o700))
	require.NoError(t, os.Symlink(outside, filepath.Join(home, "workspaces", "linked")))

	archive := filepath.Join(t.TempDir(), "evil.tar.gz")
	f, err := os.Create(archive)
	require.NoError(t, err)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	content := []byte("owned")
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "workspaces/linked/x.txt", Mode: 0o600, Size: int64(len(content)), Typeflag: tar.TypeReg}))
	_, err = tw.Write(content)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	require.NoError(t, f.Close())

	err = extractTarGz(archive, home)
	require.Error(t, err, "writing through a symlinked directory must be refused")
	_, statErr := os.Stat(filepath.Join(outside, "x.txt"))
	require.True(t, os.IsNotExist(statErr), "nothing may land outside the destination")
}
