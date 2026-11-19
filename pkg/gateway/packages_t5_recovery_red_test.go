// packages_t5_recovery_red_test.go — RED tests for start-up recovery and manual
// recovery (epic #1094, T-5, spec revision 7). FR-062: RecoverImports runs
// before the first roster load. The RED stub returns "not implemented", so
// these fail on that error (or on the missing call in gateway.go), not on an
// undefined symbol.
package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/require"
)

const recoveryAgentID = "11111111-1111-4111-8111-111111111111"

// TestPackages_StartupRecovery_WiredBeforeRoster is FR-062's boot order.
// gateway.go must call recovery before seedAndPersistAgentRoster.
func TestPackages_StartupRecovery_WiredBeforeRoster(t *testing.T) {
	// Comments are stripped first. A commented-out bootRecoverImports satisfies
	// a raw substring search and is not a call (FR-062).
	TestPackages_StartupRecovery_BootCallIsLive(t)
}

// TestPackages_StartupRecovery is TDD row 26a (D7 rows 23a–23g and 24).
func TestPackages_StartupRecovery(t *testing.T) {
	t.Run("D7-23a crash after E1 rolls back", func(t *testing.T) {
		api := packageAPI(t)
		dir := filepath.Join(api.homePath, "backups", ".staging", "stg-23a")
		writeStagingJournal(t, api.homePath, "stg-23a", recoveryAgentID, "ws-1", omnpkg.JournalStarted)
		err := api.recoverImportsBeforeRoster(packageCtx(t))
		require.NoError(t, err, "FR-062")
		// Spec recovery row (not committed): staging folder deleted, audit written.
		t5RequireAbsent(t, dir)
		require.Contains(t, readAudit(t, api.homePath), omnpkg.RecoveryRolledBackMessage)
		requireNoRecoveryAgent(t, api.homePath)
	})

	t.Run("D7-23b crash after E4 rolls back", func(t *testing.T) {
		api := packageAPI(t)
		writeStagingJournal(t, api.homePath, "stg-23b", recoveryAgentID, "ws-1", omnpkg.JournalStarted)
		writePendingOnly(t, api.homePath, recoveryAgentID)
		err := api.recoverImportsBeforeRoster(packageCtx(t))
		require.NoError(t, err, "FR-062")
		t5RequireAbsent(t, filepath.Join(api.homePath, "backups", ".staging", "stg-23b"))
		requireNoRecoveryAgent(t, api.homePath)
	})

	t.Run("D7-23c crash after membership rolls W back", func(t *testing.T) {
		api := packageAPI(t)
		ws := createWorkspaceViaAPI(t, api, "Research", "notes")
		before := fileBytes(t, filepath.Join(api.homePath, "workspaces", ws+".json"))
		addTeamMember(t, api.homePath, ws, recoveryAgentID)
		writeStagingJournal(t, api.homePath, "stg-23c", recoveryAgentID, ws, omnpkg.JournalStarted)
		require.NoError(t, api.recoverImportsBeforeRoster(packageCtx(t)))
		require.Equal(t, before, fileBytes(t, filepath.Join(api.homePath, "workspaces", ws+".json")), "W equals before")
		t5RequireAbsent(t, filepath.Join(api.homePath, "backups", ".staging", "stg-23c"))
		requireNoRecoveryAgent(t, api.homePath)
	})

	t.Run("D7-23d crash after the self-row rolls back", func(t *testing.T) {
		api := packageAPI(t)
		ws := createWorkspaceViaAPI(t, api, "Research", "notes")
		writePendingOnly(t, api.homePath, recoveryAgentID)
		writeStagingJournal(t, api.homePath, "stg-23d", recoveryAgentID, ws, omnpkg.JournalStarted)
		require.NoError(t, api.recoverImportsBeforeRoster(packageCtx(t)))
		t5RequireAbsent(t, filepath.Join(api.homePath, "backups", ".staging", "stg-23d"))
		requireNoRecoveryAgent(t, api.homePath)
	})

	t.Run("D7-23e published record is finished with no agent_created", func(t *testing.T) {
		api := packageAPI(t)
		ws := createWorkspaceViaAPI(t, api, "Research", "notes")
		writePublished(t, api.homePath, recoveryAgentID, ws)
		writeStagingJournal(t, api.homePath, "stg-23e", recoveryAgentID, ws, omnpkg.JournalStarted)
		err := api.recoverImportsBeforeRoster(packageCtx(t))
		require.NoError(t, err, "FR-062")
		// A finished import deletes the staging folder. The journal is not left to re-read.
		t5RequireAbsent(t, filepath.Join(api.homePath, "backups", ".staging", "stg-23e"))
		_, err = os.Stat(filepath.Join(api.homePath, "entities", "agents", recoveryAgentID+".json"))
		require.NoError(t, err, "the published agent stays")
		audit := readAudit(t, api.homePath)
		require.Contains(t, audit, omnpkg.RecoveryFinishedMessage)
		require.NotContains(t, audit, "agent_created")
	})

	t.Run("D7-23f missing membership is re-joined", func(t *testing.T) {
		api := packageAPI(t)
		ws := createWorkspaceViaAPI(t, api, "Research", "notes")
		writePublished(t, api.homePath, recoveryAgentID, ws)
		removeTeamMember(t, api.homePath, ws, recoveryAgentID)
		writeStagingJournal(t, api.homePath, "stg-23f", recoveryAgentID, ws, omnpkg.JournalCommitted)
		require.NoError(t, api.recoverImportsBeforeRoster(packageCtx(t)))
		require.Contains(t, workspaceTeam(t, api.homePath, ws), recoveryAgentID, "recovery re-joins W")
		_, err := os.Stat(filepath.Join(api.homePath, "entities", "agents", recoveryAgentID+".json"))
		require.NoError(t, err)
		t5RequireAbsent(t, filepath.Join(api.homePath, "backups", ".staging", "stg-23f"))
	})

	t.Run("D7-23g a failed leave does not abort boot", func(t *testing.T) {
		api := packageAPI(t)
		usePackageSeams(t)
		packageT5.RecoveryLeave = func(string, string) error { return errAuditFailed }
		ws := createWorkspaceViaAPI(t, api, "Research", "notes")
		addTeamMember(t, api.homePath, ws, recoveryAgentID)
		writeStagingJournal(t, api.homePath, "stg-23g", recoveryAgentID, ws, omnpkg.JournalStarted)
		err := api.recoverImportsBeforeRoster(packageCtx(t))
		require.NoError(t, err, "FR-062: recovery failure must not abort boot")
		require.Contains(t, workspaceTeam(t, api.homePath, ws), recoveryAgentID, "W still lists X when leave failed")
		_, statErr := os.Stat(filepath.Join(api.homePath, "entities", "agents", recoveryAgentID+".json"))
		require.True(t, os.IsNotExist(statErr), "X stays invisible")
		// D7-23g: a failed leave keeps the staging for the next start.
		_, statErr = os.Stat(filepath.Join(api.homePath, "backups", ".staging", "stg-23g"))
		require.NoError(t, statErr, "needs-cleanup staging stays when recovery fails")
		packageT5.RecoveryLeave = nil
		err = api.recoverImportsBeforeRoster(packageCtx(t))
		require.NoError(t, err)
		require.NotContains(t, workspaceTeam(t, api.homePath, ws), recoveryAgentID)
		t5RequireAbsent(t, filepath.Join(api.homePath, "backups", ".staging", "stg-23g"))
	})

	t.Run("D7-24 orphan pending with no journal is removed", func(t *testing.T) {
		api := packageAPI(t)
		writePendingOnly(t, api.homePath, recoveryAgentID)
		err := api.recoverImportsBeforeRoster(packageCtx(t))
		require.NoError(t, err, "FR-062")
		requireNoRecoveryAgent(t, api.homePath)
	})

	t.Run("a second run changes nothing", func(t *testing.T) {
		api := packageAPI(t)
		writeStagingJournal(t, api.homePath, "stg-idem", recoveryAgentID, "ws-1", omnpkg.JournalRolledBack)
		require.NoError(t, api.recoverImportsBeforeRoster(packageCtx(t)))
		before := snapshotHome(t, api.homePath)
		require.NoError(t, api.recoverImportsBeforeRoster(packageCtx(t)), "recovery is idempotent")
		require.Equal(t, before, snapshotHome(t, api.homePath))
	})
}

// TestPackages_ManualRecovery is TDD row 21d (D15 rows 1–4, FR-037a, FR-044).
func TestPackages_ManualRecovery(t *testing.T) {
	t.Run("D15-1 restart removes an incomplete C2", func(t *testing.T) {
		api := packageAPI(t)
		writeStagingJournal(t, api.homePath, "stg-d15", recoveryAgentID, "ws-1", omnpkg.JournalRollbackIncomplete)
		writePendingOnly(t, api.homePath, recoveryAgentID)
		require.NoError(t, api.recoverImportsBeforeRoster(packageCtx(t)))
		requireNoRecoveryAgent(t, api.homePath)
		t5RequireAbsent(t, filepath.Join(api.homePath, "backups", ".staging", "stg-d15"))
		got := callPackageImport(t, api, validMainPackage(t))
		require.Equal(t, http.StatusCreated, got.Code, "the slot is free; body=%s", got.Body)
	})

	t.Run("D15-2 restart clears a failed C1", func(t *testing.T) {
		api := packageAPI(t)
		ws := createWorkspaceViaAPI(t, api, "Research", "notes")
		before := fileBytes(t, filepath.Join(api.homePath, "workspaces", ws+".json"))
		addTeamMember(t, api.homePath, ws, recoveryAgentID)
		writeStagingJournal(t, api.homePath, "stg-c1", recoveryAgentID, ws, omnpkg.JournalRollbackIncomplete)
		require.NoError(t, api.recoverImportsBeforeRoster(packageCtx(t)))
		require.Equal(t, before, fileBytes(t, filepath.Join(api.homePath, "workspaces", ws+".json")), "W equals before")
		t5RequireAbsent(t, filepath.Join(api.homePath, "backups", ".staging", "stg-c1"))
		got := callPackageImport(t, api, validMainPackage(t))
		require.Equal(t, http.StatusCreated, got.Code, "body=%s", got.Body)
	})

	t.Run("D15-3 manual steps when recovery keeps failing", func(t *testing.T) {
		api := packageAPI(t)
		usePackageSeams(t)
		packageT5.RecoveryLeave = func(string, string) error { return errAuditFailed }
		ws := createWorkspaceViaAPI(t, api, "Research", "notes")
		addTeamMember(t, api.homePath, ws, recoveryAgentID)
		writeStagingJournal(t, api.homePath, "stg-manual", recoveryAgentID, ws, omnpkg.JournalRollbackIncomplete)
		writePendingOnly(t, api.homePath, recoveryAgentID)
		require.NoError(t, api.recoverImportsBeforeRoster(packageCtx(t)), "boot continues")
		// Manual step 2, gateway stopped: delete the leftovers the owner is told to delete.
		require.NoError(t, os.RemoveAll(filepath.Join(api.homePath, "entities", "agents", recoveryAgentID+".pending")))
		require.NoError(t, os.RemoveAll(filepath.Join(api.homePath, "agents", recoveryAgentID)))
		require.NoError(t, os.RemoveAll(filepath.Join(api.homePath, "backups", ".staging", "stg-manual")))
		// Manual step 3 (D15-3): the public revision-checked workspace PUT, not a
		// direct rewrite of the workspace file.
		state, readErr := workspace.ReadState(api.homePath, ws)
		require.NoError(t, readErr)
		body := `{"revision":` + packageJSONString(state.Revision) + `,"core_team":[],"delegation":[]}`
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/"+ws, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.URL.Path = "/api/v1/workspaces/" + ws
		api.HandleWorkspaces(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, "D15-3: PUT with the current revision removes the unknown member; body=%s", rec.Body)
		require.NotContains(t, workspaceTeam(t, api.homePath, ws), recoveryAgentID)
		packageT5.RecoveryLeave = nil
		got := callPackageImport(t, api, validMainPackage(t))
		require.Equal(t, http.StatusCreated, got.Code, "body=%s", got.Body)
		require.NotContains(t, previewWarningCodes(decodePreview(t, got.Body)), "unfinished_import_found")
	})

	t.Run("D15-4 five retained stagings release on restart", func(t *testing.T) {
		api := packageAPI(t)
		// One unfinished journal per slot, each retaining a different agent id
		// (SC-010). Reusing one id does not show that five recoveries freed five slots.
		ids := []string{
			"11111111-1111-4111-8111-111111111111",
			"22222222-2222-4222-8222-222222222222",
			"33333333-3333-4333-8333-333333333333",
			"44444444-4444-4444-8444-444444444444",
			"55555555-5555-4555-8555-555555555555",
		}
		require.Equal(t, omnpkg.MaxStagedImports, len(ids))
		for i, id := range ids {
			writeStagingJournal(t, api.homePath, "stg-full-"+string(rune('a'+i)), id, "ws-1", omnpkg.JournalRollbackIncomplete)
		}
		require.NoError(t, api.recoverImportsBeforeRoster(packageCtx(t)))
		for i := range ids {
			t5RequireAbsent(t, filepath.Join(api.homePath, "backups", ".staging", "stg-full-"+string(rune('a'+i))))
		}
		for i := range ids {
			got := callPackageImport(t, api, validMainPackage(t))
			require.Equal(t, http.StatusCreated, got.Code, "slot %d of %d is free after recovery; body=%s", i+1, len(ids), got.Body)
		}
		sixth := callPackageImport(t, api, validMainPackage(t))
		require.Equal(t, http.StatusConflict, sixth.Code, "the five-slot cap still holds; body=%s", sixth.Body)
		require.Contains(t, string(sixth.Body), omnpkg.CodeTooManyStagedImports)
	})
}

func writePendingOnly(t *testing.T, home, id string) {
	t.Helper()
	dir := filepath.Join(home, "entities", "agents")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, id+".pending"), []byte(`{"id":"`+id+`","name":"Writer","type":"custom"}`), 0o600))
	agent := filepath.Join(home, "agents", id)
	require.NoError(t, os.MkdirAll(agent, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(agent, "SOUL.md"), []byte("Answer in rhyme.\n"), 0o600))
}

func writePublished(t *testing.T, home, id, ws string) {
	t.Helper()
	writePendingOnly(t, home, id)
	pending := filepath.Join(home, "entities", "agents", id+".pending")
	require.NoError(t, os.Rename(pending, filepath.Join(home, "entities", "agents", id+".json")))
	addTeamMember(t, home, ws, id)
}

func requireNoRecoveryAgent(t *testing.T, home string) {
	t.Helper()
	for _, rel := range []string{
		filepath.Join("entities", "agents", recoveryAgentID+".json"),
		filepath.Join("entities", "agents", recoveryAgentID+".pending"),
		filepath.Join("agents", recoveryAgentID),
	} {
		_, err := os.Stat(filepath.Join(home, rel))
		require.True(t, os.IsNotExist(err), "leftover %s", rel)
	}
}

func addTeamMember(t *testing.T, home, ws, agent string) {
	t.Helper()
	m := readWorkspaceMap(t, home, ws)
	team := teamFrom(m)
	for _, id := range team {
		if id == agent {
			return
		}
	}
	m["core_team"] = append(team, agent)
	writeWorkspaceMap(t, home, ws, m)
}

func removeTeamMember(t *testing.T, home, ws, agent string) {
	t.Helper()
	m := readWorkspaceMap(t, home, ws)
	var team []string
	for _, id := range teamFrom(m) {
		if id != agent {
			team = append(team, id)
		}
	}
	m["core_team"] = team
	writeWorkspaceMap(t, home, ws, m)
}

func readWorkspaceMap(t *testing.T, home, ws string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(home, "workspaces", ws+".json"))
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	return m
}

func writeWorkspaceMap(t *testing.T, home, ws string, m map[string]any) {
	t.Helper()
	raw, err := json.Marshal(m)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(home, "workspaces", ws+".json"), raw, 0o600))
}

func teamFrom(m map[string]any) []string {
	raw, _ := m["core_team"].([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
