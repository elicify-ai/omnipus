// Omnipus — FR-VA-035 / TDD row 84: unexpired pending paths block
// transfers from, into, or across the whole owning collection root.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLibraryTransfer_RefusesWhenSourceOrDestinationHasAnUnexpiredPendingMove(t *testing.T) {
	for _, scenario := range []string{"source", "destination", "root"} {
		t.Run(scenario, func(t *testing.T) {
			api, ws, vault := pendingRetryFixture(t, time.Now().UTC())
			root := workDir(api, ws)
			require.NoError(t, os.MkdirAll(filepath.Join(root, "scratch"), 0o755))
			from, to := "vault-a/Open.view", "vault-b/Open.view"
			switch scenario {
			case "destination":
				from, to = "scratch/new.view", "vault-a/Renamed.view"
				require.NoError(t, os.WriteFile(filepath.Join(root, from), []byte("independent file"), 0o600))
			case "root":
				from, to = "vault-a", "scratch/vault-a"
			}
			before := loadRecordedViews(t, api.homePath, vault)
			w := libPostJSON(t, api, "/api/v1/library/move", transferBody(ws, from, to))
			require.Equal(t, http.StatusConflict, w.Code,
				"FR-VA-035: %s transfer touches an unexpired pending path: %s", scenario, w.Body.String())
			require.Contains(t, w.Body.String(), redRetryID,
				"the visible conflict must identify the pending move to resolve first")
			require.Equal(t, before, loadRecordedViews(t, api.homePath, vault))
			require.FileExists(t, filepath.Join(vault, "Open.view"))
			switch scenario {
			case "source":
				require.NoFileExists(t, filepath.Join(root, "vault-b", "Open.view"))
			case "destination":
				require.FileExists(t, filepath.Join(root, from))
				require.NoFileExists(t, filepath.Join(vault, "Renamed.view"))
			case "root":
				require.DirExists(t, vault)
				require.NoDirExists(t, filepath.Join(root, "scratch", "vault-a"))
			}
		})
	}
}
