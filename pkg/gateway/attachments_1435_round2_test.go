package gateway

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

func issue1435R2UploadAPI(t *testing.T) (*restAPI, *media.FileMediaStore) {
	t.Helper()
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	api, cleanup := newTestRestAPI(t)
	t.Cleanup(cleanup)
	// Align HTTP uploads and the actual owning session store's home.
	api.homePath = filepath.Dir(api.agentLoop.GetSessionStore().BaseDir())
	t.Setenv("OMNIPUS_HOME", api.homePath)
	store := media.NewFileMediaStoreWithCleanup(media.MediaCleanerConfig{MaxAge: time.Hour})
	t.Cleanup(store.Stop)
	api.agentLoop.SetMediaStore(store)
	api.mediaStore = store
	return api, store
}

func issue1435R2Upload(t *testing.T, api *restAPI, owner string) *httptest.ResponseRecorder {
	t.Helper()
	body, contentType := buildMultipart(t, owner, map[string]string{"accepted.txt": "accepted owner-scoped attachment"})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/upload", body)
	request.Header.Set("Content-Type", contentType)
	recorder := httptest.NewRecorder()
	api.HandleUpload(recorder, request)
	return recorder
}

func issue1435R2AcceptedRef(t *testing.T, recorder *httptest.ResponseRecorder, store media.MediaStore) (string, string) {
	t.Helper()
	require.Equal(t, http.StatusCreated, recorder.Code, "upload setup must genuinely succeed: %s", recorder.Body.String())
	var result gen.UploadFilesResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &result))
	require.Len(t, result.Files, 1)
	require.NotNil(t, result.Files[0].Ref)
	ref := *result.Files[0].Ref
	path, err := store.Resolve(ref)
	require.NoError(t, err, "positive control: the endpoint's accepted ref really resolves")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "accepted owner-scoped attachment", string(data))
	return ref, path
}

// Age only the filesystem clock fact in the real persisted registry, then
// reopen it before owner removal. This gives deterministic cleanup input
// without sleeps, private-field access, or reloading AFTER deletion (which
// would hide stale in-memory mappings by dropping missing files on boot).
func issue1435R2AgedStore(t *testing.T, api *restAPI, original *media.FileMediaStore, ref string) *media.FileMediaStore {
	t.Helper()
	original.Stop()
	registry := filepath.Join(api.homePath, "media", "registry.json")
	data, err := os.ReadFile(registry)
	require.NoError(t, err)
	var document map[string]any
	require.NoError(t, json.Unmarshal(data, &document))
	entries, ok := document["entries"].(map[string]any)
	require.True(t, ok)
	entry, ok := entries[ref].(map[string]any)
	require.True(t, ok)
	entry["stored_at"] = "2000-01-01T00:00:00Z" // Fixture clock, unambiguously older than the one-hour cleaner age.
	data, err = json.Marshal(document)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(registry, data, 0o600))
	store := media.NewFileMediaStoreWithCleanup(media.MediaCleanerConfig{MaxAge: time.Hour})
	t.Cleanup(store.Stop)
	require.NoError(t, store.LoadRegistry())
	_, err = store.Resolve(ref)
	require.NoError(t, err, "aged accepted ref must be loaded before exercising actual lifecycle cleanup")
	api.agentLoop.SetMediaStore(store)
	api.mediaStore = store
	return store
}

func TestIssue1435R2_OwnerRemovalReleasesPinnedUpload(t *testing.T) {
	for _, removal := range []string{"chat_delete_control", "retention"} {
		t.Run(removal, func(t *testing.T) {
			api, original := issue1435R2UploadAPI(t)
			sessions := api.agentLoop.GetSessionStore()
			owner, err := sessions.NewSession(session.SessionTypeChat, "web", "mia")
			require.NoError(t, err)
			ref, path := issue1435R2AcceptedRef(t, issue1435R2Upload(t, api, owner.ID), original)
			store := issue1435R2AgedStore(t, api, original, ref)
			if removal == "chat_delete_control" {
				require.NoError(t, api.removeStoppedSession(sessions, owner.ID))
			} else {
				// A real idle chat with an aged retained transcript triggers the
				// production retention sweep, not manual ReleaseAll in the test.
				transcript := filepath.Join(sessions.BaseDir(), owner.ID, "transcript.jsonl")
				require.NoError(t, os.WriteFile(transcript, []byte("{}\n"), 0o600))
				old := time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)
				require.NoError(t, os.Chtimes(transcript, old, old))
				removed, err := sessions.RetentionSweep(1)
				require.NoError(t, err)
				require.Positive(t, removed, "instrument: actual retention must remove an expired transcript")
			}
			_, err = sessions.GetMeta(owner.ID)
			require.Error(t, err, "instrument: the owning conversation must actually be gone")
			_, err = os.Stat(path)
			require.ErrorIs(t, err, fs.ErrNotExist, "the owning conversation's upload bytes must be removed")
			store.CleanExpired()
			_, err = store.Resolve(ref)
			require.ErrorIs(t, err, media.ErrNotFound,
				"#1435 R5: %s removed the owning chat but left its upload mapping pinned in the running media store", removal)
		})
	}
}

func TestIssue1435R2_NonexistentOwnerCannotLeavePinnedUpload(t *testing.T) {
	api, original := issue1435R2UploadAPI(t)
	const owner = "syntactically-valid-nonexistent-chat"
	_, err := api.agentLoop.GetSessionStore().GetMeta(owner)
	require.Error(t, err, "fixture must name no existing conversation")
	recorder := issue1435R2Upload(t, api, owner)
	if recorder.Code != http.StatusCreated {
		// Both explicit refusal and expiring temporary acceptance satisfy
		// the chief's lifecycle requirement; no new HTTP status is invented.
		require.Contains(t, []int{http.StatusBadRequest, http.StatusNotFound}, recorder.Code, recorder.Body.String())
		return
	}
	ref, _ := issue1435R2AcceptedRef(t, recorder, original)
	store := issue1435R2AgedStore(t, api, original, ref)
	store.CleanExpired()
	_, err = store.Resolve(ref)
	require.ErrorIs(t, err, media.ErrNotFound,
		"#1435 R5: a nonexistent conversation cannot own a permanently pinned upload; temporary accepted uploads must expire")
}
