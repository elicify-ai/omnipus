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

	"github.com/elicify-ai/omnipus/pkg/omnpkg"
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

func indexOf(events []string, want string) int {
	for i, e := range events {
		if e == want {
			return i
		}
	}
	return -1
}
