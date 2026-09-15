// UAT re-test U-58, second half: "records stop indexing". Once a knowledge
// base's marker is removed through the Library, the gateway must stop holding
// that folder's index open — no search index handle, no filesystem watcher
// re-indexing its notes, no drift schedule re-indexing it every six hours.
//
// The plain delete removes the marker from disk, and every endpoint that
// resolves collections by scope stops naming the folder at once. But the
// knowledge lifecycle only ever releases a collection when a MOUNT is revoked
// or the gateway stops, so without this a demoted work-tree folder stayed
// attached, and its watcher kept indexing it, until the next restart.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/knowledge"
)

// attachedVaultLifecycle builds the linked-notes vault, publishes a knowledge
// lifecycle for the API's home (the registry every door reads), and attaches
// the vault the way the boot / runtime sweep does for a work-tree collection.
// The filesystem watcher is a fake that counts Start and Stop, so the test
// does not depend on OS watch support.
func attachedVaultLifecycle(t *testing.T) (api *restAPI, ws, vault, vaultReal string, kl *KnowledgeLifecycle, watchers func() []*fakeWatcher) {
	t.Helper()
	api, ws, vault = buildReservedVault(t)
	var err error
	vaultReal, err = filepath.EvalSymlinks(vault)
	require.NoError(t, err)

	var mu sync.Mutex
	var built []*fakeWatcher
	kl = kltLifecycle(t, KnowledgeLifecycleOptions{
		Home: api.homePath,
		NewWatcher: func(*knowledge.Index) knowledgeWatcher {
			w := newFakeWatcher(nil)
			mu.Lock()
			built = append(built, w)
			mu.Unlock()
			return w
		},
	})
	registerKnowledgeLifecycle(api.homePath, kl)
	t.Cleanup(func() { unregisterKnowledgeLifecycle(api.homePath) })

	require.NoError(t, kl.AttachCollection(context.Background(), ws, vault))
	require.Contains(t, kl.AttachedRoots(), vaultReal, "precondition: the vault is attached")
	watchers = func() []*fakeWatcher {
		mu.Lock()
		defer mu.Unlock()
		return append([]*fakeWatcher(nil), built...)
	}
	require.Len(t, watchers(), 1, "precondition: one watcher for the attached vault")
	return api, ws, vault, vaultReal, kl, watchers
}

// TestLibraryDoors_RemovingTheVaultMarkerReleasesTheKnowledgeIndex covers the
// three Library actions that take a marker away from its folder.
func TestLibraryDoors_RemovingTheVaultMarkerReleasesTheKnowledgeIndex(t *testing.T) {
	cases := []struct {
		name   string
		method string
		target string // WS is replaced by the workspace id
		body   string
		status int
	}{
		{"delete", http.MethodDelete, "/api/v1/library/WS/entries?path=vault/.omnipus-vault", "", http.StatusNoContent},
		{"rename away", http.MethodPost, "/api/v1/library/WS/rename",
			`{"from":"vault/.omnipus-vault","to":"vault/omnipus-vault-backup"}`, http.StatusOK},
		{"move away", http.MethodPost, "/api/v1/library/move",
			`{"from_workspace_id":"WS","from_path":"vault/.omnipus-vault","to_workspace_id":"WS","to_path":"vault/Projects/omnipus-vault-backup"}`,
			http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api, ws, vault, vaultReal, kl, watchers := attachedVaultLifecycle(t)
			// The Obsidian config is removed first, through the same door, so
			// the marker is the folder's LAST one and taking it away really does
			// demote the folder.
			w := libTree(t, api, http.MethodDelete, "/api/v1/library/"+ws+"/entries?path=vault/.obsidian", "")
			require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
			require.Contains(t, kl.AttachedRoots(), vaultReal,
				"removing .obsidian while .omnipus-vault remains leaves a knowledge base, so it stays attached")

			ix, ok := kl.IndexForRoot(vault)
			require.True(t, ok)

			w = libTree(t, api, tc.method, strings.ReplaceAll(tc.target, "WS", ws), strings.ReplaceAll(tc.body, "WS", ws))
			require.Equalf(t, tc.status, w.Code, "body: %s", w.Body.String())
			require.False(t, detectKnowledgeBase(t, api, ws, "vault").IsKnowledgeBase, "precondition: the folder is demoted")

			require.NotContains(t, kl.AttachedRoots(), vaultReal,
				"U-58: a folder that is no longer a knowledge base must not stay attached")
			require.Zero(t, kl.HoldersFor(vault))
			_, stops := watchers()[0].counts()
			require.Equal(t, 1, stops, "its filesystem watcher must be stopped, or it keeps indexing the notes")
			_, err := ix.DocCount()
			require.Error(t, err, "its index handle must be closed")

			// The runtime sweep agrees and does not bring it back.
			kl.AttachWorkspace(ws)
			kl.WaitForAttaches()
			require.NotContains(t, kl.AttachedRoots(), vaultReal)
		})
	}
}

// TestLibraryDelete_OrdinaryFolderKeepsTheKnowledgeIndexAttached is the
// control: deleting an ordinary folder, or a tool-state folder that is not
// the folder's last marker, must leave the knowledge base's index alone.
func TestLibraryDelete_OrdinaryFolderKeepsTheKnowledgeIndexAttached(t *testing.T) {
	api, ws, vault, vaultReal, kl, watchers := attachedVaultLifecycle(t)

	for _, rel := range []string{"vault/assets", "vault/.obsidian", "vault/.omnipus-vault/trash"} {
		w := libTree(t, api, http.MethodDelete, "/api/v1/library/"+ws+"/entries?path="+rel, "")
		require.Equalf(t, http.StatusNoContent, w.Code, "%s: %s", rel, w.Body.String())
		require.Containsf(t, kl.AttachedRoots(), vaultReal, "deleting %s must not release the knowledge base", rel)
	}
	require.Equal(t, 1, kl.HoldersFor(vault))
	_, stops := watchers()[0].counts()
	require.Zero(t, stops, "the watcher keeps running")
	ix, ok := kl.IndexForRoot(vault)
	require.True(t, ok)
	_, err := ix.DocCount()
	require.NoError(t, err, "the index is still open")
}
