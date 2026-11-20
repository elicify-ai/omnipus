package gateway

// TestPackages_PrePublishDurability covers S1a security finding F2 (#1094):
// before the commit rename, the imported agent's prompt folder
// (agents/<id>/, holding SOUL.md and AGENT.md) and its parent entry
// (agents/) are synced, so a power cut after the rename cannot leave a
// published agent without its prompt files. A sync that fails BEFORE the
// rename is an import failure: rolled back, nothing published. A sync that
// fails AFTER the rename stays committed with the publish_not_durable warning;
// that half is pinned at the store by
// pkg/agentstore/pending_record_red_test.go::TestPublishPending_IsTheCommitRename
// ("a directory sync failure after the real rename stays committed"), and the
// gateway branch that turns it into the warning is unchanged.
//
// T-5 seams are process-wide: no t.Parallel.

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

func TestPackages_PrePublishDurability(t *testing.T) {
	t.Run("the prompt folder and its parent are synced before the commit rename", func(t *testing.T) {
		api := packageAPI(t)
		usePackageSeams(t)
		packageT5.Reload = func() (bool, error) { return true, nil }
		ws := createWorkspaceViaAPI(t, api, "Research", "notes")
		agentsDir := filepath.Join(api.homePath, "agents")
		var events []string
		packageT5.SyncDir = func(dir string) error {
			events = append(events, "sync "+dir)
			return nil
		}
		var id string
		packageT5.PublishRename = func(from, _ string) error {
			id = strings.TrimSuffix(filepath.Base(from), ".pending")
			events = append(events, "rename")
			return nil
		}
		prev := stageUpload(t, api, validMainPackage(t))
		got := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
		require.Equal(t, http.StatusOK, got.Code, "body=%s", got.Body)
		require.NotEmpty(t, id)

		renameAt := indexOf(events, "rename")
		require.GreaterOrEqual(t, renameAt, 0, "events=%v", events)
		for _, want := range []string{"sync " + filepath.Join(agentsDir, id), "sync " + agentsDir} {
			at := indexOf(events, want)
			require.GreaterOrEqual(t, at, 0, "%q must be synced; events=%v", want, events)
			require.Less(t, at, renameAt, "%q must be synced before the commit rename; events=%v", want, events)
		}
	})

	t.Run("a failed sync of the prompt folder before the rename rolls the import back", func(t *testing.T) {
		api := packageAPI(t)
		usePackageSeams(t)
		ws := createWorkspaceViaAPI(t, api, "Research", "notes")
		agentsDir := filepath.Join(api.homePath, "agents")
		syncErr := errors.New("sync failed: disk did not flush")
		var id string
		packageT5.SyncDir = func(dir string) error {
			if filepath.Dir(dir) == agentsDir {
				id = filepath.Base(dir)
				return syncErr
			}
			return nil
		}
		renamed := false
		packageT5.PublishRename = func(string, string) error { renamed = true; return nil }
		prev := stageUpload(t, api, validMainPackage(t))
		got := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
		requirePackageStatus(t, got, http.StatusInternalServerError, omnpkg.CodeImportRolledBack)
		require.NotEmpty(t, id, "the prompt folder sync must run")
		require.False(t, renamed, "a pre-commit sync failure must stop before the commit rename")
		requireAgentInvisible(t, api, id)
		_, err := os.Stat(filepath.Join(agentsDir, id))
		require.ErrorIs(t, err, os.ErrNotExist, "the rollback removes the agent folder")
	})
}

// TestPackages_JournalDurabilityStopsApply covers review finding R2 (#1094
// S1a): the import journal goes through the strict writer, so a failed
// directory sync of the first journal write (E1) stops the apply before any
// pending-agent or membership write. The fault sits in that writer's
// durability step; the old warn-only writer never reaches it, so with the old
// writer the import goes through and this test fails.
func TestPackages_JournalDurabilityStopsApply(t *testing.T) {
	api := packageAPI(t)
	usePackageSeams(t)
	ws := createWorkspaceViaAPI(t, api, "Research", "notes")
	prev := stageUpload(t, api, validMainPackage(t))
	stagingDir := filepath.Join(api.packageStagingBase(), prev.StagingId)
	faulted := 0
	t.Cleanup(fileutil.SetDirSyncFaultForTest(func(dir string) error {
		if dir == stagingDir {
			faulted++
			return errors.New("sync failed: disk did not flush")
		}
		return nil
	}))
	var writes []string
	packageT5.WriteWorkspace = func(_, wsID string, _ workspace.Workspace) error {
		writes = append(writes, "workspace "+wsID)
		return nil
	}
	packageT5.SaveDelegation = func(_, wsID string, _ []workspace.DelegationEdge) error {
		writes = append(writes, "delegation "+wsID)
		return nil
	}
	recordsBefore := agentEntityNames(t, api.homePath)
	foldersBefore := dirEntryNames(t, filepath.Join(api.homePath, "agents"))

	got := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))

	require.Equal(t, http.StatusInternalServerError, got.Code, "body=%s", got.Body)
	require.Positive(t, faulted, "the journal write's directory sync must be the faulted step")
	require.Empty(t, writes, "no membership write after a journal durability failure")
	require.Equal(t, recordsBefore, agentEntityNames(t, api.homePath), "no pending or final agent record")
	require.Equal(t, foldersBefore, dirEntryNames(t, filepath.Join(api.homePath, "agents")), "no agent folder")
	// Stopped at E1, before the pending write: no journal is left (a later
	// failure leaves a rolled_back journal) and the staging is ready again.
	_, err := os.Stat(filepath.Join(stagingDir, omnpkg.JournalName))
	require.ErrorIs(t, err, os.ErrNotExist, "an E1 refusal removes the journal")
	state, err := api.packageRegistry().State(prev.StagingId)
	require.NoError(t, err)
	require.Equal(t, omnpkg.StagingReady, state, "an E1 refusal leaves the staging ready")
}

// dirEntryNames lists dir's entry names; a missing dir is empty.
func dirEntryNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func indexOf(events []string, want string) int {
	for i, e := range events {
		if e == want {
			return i
		}
	}
	return -1
}
