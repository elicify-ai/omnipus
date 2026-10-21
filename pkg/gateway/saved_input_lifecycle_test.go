package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/require"
)

// Executes real handlers/cascade operations and storage, not browser UAT or a
// model-provider workflow. Deleted recipients are refused, never resurrected.
func TestSavedInputStorageLifecycleGatewayMatrix(t *testing.T) {
	t.Run("single_chat_delete", func(t *testing.T) {
		api := lifecycleGatewayAPI(t)
		store, sid := lifecycleGatewaySeed(t, api, "mia")
		_, err := store.SaveChatInput(sid, "saved-input-before-delete", "mia", "held before delete")
		require.NoError(t, err)
		w := deleteSessionViaAPI(t, api, sid)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		lifecycleGatewayGone(t, store, sid)
		fresh, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
		require.NoError(t, err)
		lifecycleGatewayFresh(t, store, fresh.ID, "mia", "single-delete")
	})
	t.Run("bulk_delete_preserves_main", func(t *testing.T) {
		api := lifecycleGatewayAPI(t)
		store, sid := lifecycleGatewaySeed(t, api, "mia")
		main, err := store.GetOrCreateMainSession("lifecycle-bulk-workspace", "mia")
		require.NoError(t, err)
		_, err = store.SaveChatInput(main.ID, "saved-input-main-held", "mia", "main report kept with main")
		require.NoError(t, err)
		w := httptest.NewRecorder()
		api.HandleClearSessions(w, httptest.NewRequest(http.MethodDelete, "/api/v1/sessions/all", nil))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var result gen.ClearAllSessionsResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
		require.Equal(t, 1, result.Count)
		lifecycleGatewayGone(t, store, sid)
		lifecycleGatewayFresh(t, store, main.ID, "mia", "bulk-preserved-main")
		pending, err := store.PendingChatInputs(context.Background(), main.ID)
		require.NoError(t, err)
		require.Len(t, pending, 1)
		require.Equal(t, "main report kept with main", pending[0].Content)
	})
	t.Run("workspace_delete_main_cascade", func(t *testing.T) {
		api := lifecycleGatewayAPI(t)
		const wsID = "lifecycle-delete-workspace"
		hbWriteWorkspaceRecord(t, api, workspace.Workspace{
			ID: wsID, Name: "Lifecycle workspace", Status: "active", CoreTeam: []string{"mia"},
			CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
		})
		store := api.agentLoop.GetSessionStore()
		main, err := store.GetOrCreateMainSession(wsID, "mia")
		require.NoError(t, err)
		_, err = store.SaveChatInput(main.ID, "saved-input-workspace-held", "mia", "held before workspace deletion")
		require.NoError(t, err)
		w := deleteWorkspaceViaAPI(t, api, wsID)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		lifecycleGatewayGone(t, store, main.ID)
		fresh, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
		require.NoError(t, err)
		lifecycleGatewayFresh(t, store, fresh.ID, "mia", "workspace-delete-new-recipient")
	})
	t.Run("agent_delete_session_cascade", func(t *testing.T) {
		api := lifecycleGatewayAPI(t)
		store, sid := lifecycleGatewaySeed(t, api, "mia")
		revision, err := agentstore.New(api.homePath).PeekRevision("mia")
		require.NoError(t, err)
		result := systools.RunAgentDeleteCascade(api.homePath, "mia", revision, store, nil)
		require.NoError(t, result.DeletionErr)
		require.False(t, result.CleanupFailed)
		require.Empty(t, result.Warnings)
		require.Equal(t, 1, result.SessionsDeleted)
		lifecycleGatewayGone(t, store, sid)
		fresh, err := store.NewSession(session.SessionTypeChat, "webchat", "jim")
		require.NoError(t, err)
		lifecycleGatewayFresh(t, store, fresh.ID, "jim", "agent-delete-other-owner")
	})
	t.Run("backup_then_coherent_restore", lifecycleGatewayBackupRestore)
	t.Run("incoherent_restore_overlay_is_refused", lifecycleGatewayOverlayRefusal)
}

// Unlike the heartbeat fixture, bind the agent-home base UNDER the backup
// home: sessionsHomeFor deliberately takes its parent. The unchanged seed
// assertion below prevents an out-of-root store from producing false greens.
func lifecycleGatewayAPI(t *testing.T) *restAPI {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: filepath.Join(home, "agent-home"), DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
			List: []config.AgentConfig{
				{ID: "mia", Name: "Mia", Type: config.AgentTypeCustom},
				{ID: "jim", Name: "Jim", Type: config.AgentTypeCustom},
			},
		},
	}
	seedAgentEntities(t, home, cfg.Agents.List)
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	cs := cron.NewCronService(filepath.Join(home, "cron.json"))
	require.NoError(t, cs.Start())
	t.Cleanup(cs.Stop)
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: filepath.Join(home, "system"), RetentionDays: 90})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, logger.Close()) })
	api := &restAPI{agentLoop: al, homePath: home, auditor: logger}
	api.cronService.Store(cs)
	return api
}

func lifecycleGatewaySeed(t *testing.T, api *restAPI, agentID string) (*session.UnifiedStore, string) {
	t.Helper()
	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store)
	require.Equal(t, filepath.Join(api.homePath, "sessions"), store.BaseDir(), "backup must include the actual shared store")
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", agentID)
	require.NoError(t, err)
	_, _, err = store.AppendModelMessage(context.Background(), meta.ID, session.ModelAppend{
		Message:        providers.Message{Role: "user", Content: "retained conversation"},
		ViewMembership: session.ViewMembershipBoth, Source: session.EntrySource{Kind: "user", Principal: "daniel"}, AgentID: agentID,
	})
	require.NoError(t, err)
	return store, meta.ID
}

func lifecycleGatewayFresh(t *testing.T, store *session.UnifiedStore, sid, agentID, operation string) {
	t.Helper()
	id, body := "saved-input-new-after-"+operation, "genuinely new helper report after "+operation
	addr, err := store.SaveChatInput(sid, id, agentID, body)
	require.NoError(t, err)
	lookedUp, placed, err := store.SavedChatInput(sid, id)
	require.NoError(t, err)
	require.Equal(t, addr, lookedUp)
	require.False(t, placed)
	_, _, err = store.PlaceSavedInput(context.Background(), sid, addr)
	require.NoError(t, err)
	lookedUp, placed, err = store.SavedChatInput(sid, id)
	require.NoError(t, err)
	require.Equal(t, addr, lookedUp)
	require.True(t, placed)
	view, err := store.WindowView(context.Background(), sid)
	require.NoError(t, err)
	messages, _ := view.History()
	count := 0
	for _, msg := range messages {
		if msg.Content == body {
			count++
		}
	}
	require.Equal(t, 1, count)
	retried, err := store.SaveChatInput(sid, id, agentID, body)
	require.NoError(t, err)
	require.Equal(t, addr, retried)
}

func lifecycleGatewayGone(t *testing.T, store *session.UnifiedStore, sid string) {
	t.Helper()
	_, err := os.Stat(filepath.Join(store.BaseDir(), sid))
	require.ErrorIs(t, err, fs.ErrNotExist)
	_, err = store.SaveChatInput(sid, "saved-input-deleted-refused", "mia", "must not resurrect a deleted recipient")
	require.Error(t, err)
	_, _, err = store.SavedChatInput(sid, "saved-input-deleted-refused")
	require.Error(t, err)
	_, err = os.Stat(filepath.Join(store.BaseDir(), sid))
	require.ErrorIs(t, err, fs.ErrNotExist)
}

func lifecycleGatewayBackup(t *testing.T, api *restAPI) string {
	t.Helper()
	w := httptest.NewRecorder()
	api.HandleCreateBackup(w, httptest.NewRequest(http.MethodPost, "/api/v1/backup", nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var result gen.BackupCreateResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	_, err := os.Stat(result.Path)
	require.NoError(t, err)
	return filepath.Base(result.Path)
}

func lifecycleGatewayRestore(t *testing.T, api *restAPI, filename string) {
	t.Helper()
	body, err := json.Marshal(gen.RestoreBackupRequest{Filename: filename})
	require.NoError(t, err)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/restore", bytes.NewReader(body))
	r = withReAuthAdmin(t, api, r)
	w := httptest.NewRecorder()
	api.HandleRestore(w, r)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
}

func lifecycleGatewayBackupRestore(t *testing.T) {
	withEdition(t, config.EditionCore)
	api := lifecycleGatewayAPI(t)
	store, sid := lifecycleGatewaySeed(t, api, "mia")
	_, err := store.SaveChatInput(sid, "saved-input-backup-held", "mia", "held in coherent backup")
	require.NoError(t, err)
	filename := lifecycleGatewayBackup(t, api)
	lifecycleGatewayFresh(t, store, sid, "mia", "backup")
	lifecycleGatewayRestore(t, api, filename)
	pending, err := store.PendingChatInputs(context.Background(), sid)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Equal(t, "held in coherent backup", pending[0].Content)
	lifecycleGatewayFresh(t, store, sid, "mia", "coherent-restore")
}

func lifecycleGatewayOverlayRefusal(t *testing.T) {
	withEdition(t, config.EditionCore)
	api := lifecycleGatewayAPI(t)
	store, sid := lifecycleGatewaySeed(t, api, "mia")
	filename := lifecycleGatewayBackup(t, api) // No pending sidecar exists yet.
	_, err := store.SaveChatInput(sid, "saved-input-after-backup", "mia", "not present in old backup")
	require.NoError(t, err)
	lifecycleGatewayRestore(t, api, filename)
	_, err = store.PendingChatInputs(context.Background(), sid)
	require.Error(t, err, "overlay restores do not remove newer omitted files: this is real inconsistency")
	_, err = store.SaveChatInput(sid, "saved-input-overlay-must-refuse", "mia", "do not hide dangling restored storage")
	require.Error(t, err)
}
