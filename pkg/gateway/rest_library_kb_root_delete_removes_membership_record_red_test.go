// Omnipus — FR-VA-039 / TDD rows 88–89: permanent KB deletion and last
// marker demotion must remove the private record before the path can be reused.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/stretchr/testify/require"
)

func seedKBRecordForDeletion(t *testing.T, api *restAPI, vault string) string {
	t.Helper()
	plantTrackedView(t, api.homePath, vault, "Projects.base", "Open.view")
	stagePendingViewMove(t, api.homePath, vault, redRetryID, "Open.view", "Renamed.view", false,
		pendingViewMember("Projects.base", "Projects.base", "Open.view", "Renamed.view"), time.Now().UTC())
	plantTrackedView(t, api.homePath, vault, "Other.base", "Other.view")
	require.NoError(t, knowledge.WithViewMembership(api.homePath, vault, func(m *knowledge.ViewMembership) error {
		if m.CompletedMoves == nil {
			m.CompletedMoves = make(map[string]knowledge.CompletedViewMove)
		}
		m.CompletedMoves["previous"] = knowledge.CompletedViewMove{
			From: "Past.view", To: "Later.view", CompletedAt: time.Now().UTC(),
		}
		return knowledge.SaveViewMembership(m)
	}))
	path, err := knowledge.IndexDirFor(api.homePath, vault)
	require.NoError(t, err)
	record := filepath.Join(path, knowledge.ViewMembershipFileName)
	require.FileExists(t, record)
	return record
}

func assertRecreatedKBHasNoOldAuthority(t *testing.T, api *restAPI, vault string) {
	t.Helper()
	makeKnowledgeBase(t, vault, "Recreated KB")
	// Deliberately plant a marker-only copy, not plantTrackedView: an old
	// record must not silently authorize it at the reused absolute path.
	markerOnly := []byte("name: open\nkind: table\nsource: Projects.base\nderived_from: Projects.base\n")
	copyPath := filepath.Join(vault, "Open.view")
	require.NoError(t, os.WriteFile(copyPath, markerOnly, 0o600))
	m := loadRecordedViews(t, api.homePath, vault)
	require.Empty(t, m.Bases, "a reused path must not inherit prior write authority")
	require.Empty(t, m.PendingMoves, "an old Retry receipt must not become discoverable")
	require.Empty(t, m.CompletedMoves, "completed IDs must not leak into a new KB")
	written, err := m.RewriteManagedView("Projects.base", "open", markerOnly)
	require.Error(t, err, "an old base must not rewrite the newly planted copy")
	require.False(t, written)
	deleted, err := m.DeleteManagedView("Projects.base", "open")
	require.NoError(t, err)
	require.False(t, deleted, "an old base must not delete the newly planted copy")
	body, err := os.ReadFile(copyPath)
	require.NoError(t, err)
	require.Equal(t, markerOnly, body)
}

func TestKBRootDelete_RemovesMembershipRecordSoRecreatedKBHasNoAuthority(t *testing.T) {
	api, ws, vault, _ := twoKBWorkspace(t)
	record := seedKBRecordForDeletion(t, api, vault)
	w := libDelete(t, api, "/api/v1/library/"+ws+"/entries?path=vault-a")
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	require.NoFileExists(t, record, "permanent root deletion removes the outside-vault record")
	require.NoDirExists(t, vault)
	require.NoError(t, os.MkdirAll(vault, 0o755))
	assertRecreatedKBHasNoOldAuthority(t, api, vault)
}

func TestMarkerFolderDelete_RemovesMembershipRecordAndFailsVisiblyIfRemovalFails(t *testing.T) {
	t.Run("success_and_recreate", func(t *testing.T) {
		api, ws, vault, _ := twoKBWorkspace(t)
		record := seedKBRecordForDeletion(t, api, vault)
		w := libDelete(t, api, "/api/v1/library/"+ws+"/entries?path=vault-a/.omnipus-vault")
		require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
		require.NoFileExists(t, record)
		require.DirExists(t, vault, "demotion leaves the collection folder in place")
		require.NoDirExists(t, knowledge.MarkerDir(vault))
		assertRecreatedKBHasNoOldAuthority(t, api, vault)
	})
	t.Run("record_removal_failure_keeps_marker", func(t *testing.T) {
		api, ws, vault, _ := twoKBWorkspace(t)
		record := seedKBRecordForDeletion(t, api, vault)
		// An occupied directory in place of the record deterministically makes
		// os.Remove fail, even as root. It tests cleanup failure, not a corrupt
		// JSON fallback, and avoids chmod's platform/user-dependent behavior.
		require.NoError(t, os.Remove(record))
		require.NoError(t, os.Mkdir(record, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(record, "blocker"), []byte("x"), 0o600))
		w := libDelete(t, api, "/api/v1/library/"+ws+"/entries?path=vault-a/.omnipus-vault")
		require.NotEqual(t, http.StatusNoContent, w.Code,
			"failed record cleanup must be visible, never a successful demotion")
		require.True(t, w.Code >= 400 && w.Code < 600, "failure must be HTTP error: %d", w.Code)
		require.DirExists(t, knowledge.MarkerDir(vault), "failed cleanup must not delete the last marker")
	})
}
