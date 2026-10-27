package gateway

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Issue #1435 Round 5 item 1 — silent-failure hunter R4-SF-1 (HIGH),
// pr-test-analyzer R4-T1 and architect F5 (PARTIAL).
//
// With the media cleaner disabled, retention removes a conversation and its
// upload files. The conversation's MEDIA MAPPINGS must be released by that
// removal — not lazily by a later lookup.
//
// The round-4 regression proved the wrong thing: it called store.Resolve
// after the sweep, and that call itself performs the release, so the test
// passed while retention still left the mapping live. This case observes the
// store's index with a NON-MUTATING read (store.RefByPath) BEFORE any
// Resolve, so the observation cannot be repaired by the very cleanup it
// inspects. It covers both session scopes the gateway releases on an
// explicit session delete (media.SessionUploadScopePrefix for accepted
// uploads, media.SessionInlineScopePrefix for tool-generated inline media),
// and it runs the retention cycle TWICE to show that dead mappings do not
// accumulate and that SaveRegistry does not re-serialize them.

// issue1435R5RegistryEntries reads the media store's persisted registry.
func issue1435R5RegistryEntries(t *testing.T, home string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, "media", "registry.json"))
	require.NoError(t, err, "instrument: the media store must have persisted its registry")
	var document struct {
		Entries map[string]any `json:"entries"`
	}
	require.NoError(t, json.Unmarshal(data, &document))
	require.NotNil(t, document.Entries, "instrument: the registry must carry an entries map")
	return document.Entries
}

// issue1435R5DeadPathEntries counts the persisted registry entries that
// point at one of the given dead paths.
func issue1435R5DeadPathEntries(entries map[string]any, deadPaths ...string) int {
	dead := 0
	for _, record := range entries {
		entry, ok := record.(map[string]any)
		if !ok {
			continue
		}
		path, _ := entry["path"].(string)
		for _, candidate := range deadPaths {
			if path == candidate {
				dead++
				break
			}
		}
	}
	return dead
}

func TestIssue1435R5_RetentionReleasesSessionMediaMappings(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	t.Cleanup(cleanup)
	// Align the upload endpoint and the owning session store's home.
	api.homePath = filepath.Dir(api.agentLoop.GetSessionStore().BaseDir())
	t.Setenv("OMNIPUS_HOME", api.homePath)
	// A bare store: no cleaner configuration, so CleanExpired can never run
	// for it. The media cleaner is DISABLED for this scenario by
	// construction — nothing may release these mappings but retention.
	store := media.NewFileMediaStore()
	t.Cleanup(store.Stop)
	api.agentLoop.SetMediaStore(store)
	api.mediaStore = store

	sessions := api.agentLoop.GetSessionStore()
	old := time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)

	// type cycle is one retention round: a conversation with an accepted
	// upload and a tool-generated inline attachment, aged out and swept.
	type cycle struct {
		uploadPath string
		inlinePath string
		uploadRef  string
		inlineRef  string
	}
	var cycles []cycle

	for round := 1; round <= 2; round++ {
		owner, err := sessions.NewSession(session.SessionTypeChat, "web", "mia")
		require.NoError(t, err)

		// The accepted upload: a real HTTP upload through the production
		// endpoint, registered in the media store under the session scope.
		recorder := issue1435R2Upload(t, api, owner.ID)
		require.Equal(t, http.StatusCreated, recorder.Code, "upload setup must genuinely succeed: %s", recorder.Body.String())
		var uploaded gen.UploadFilesResponse
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &uploaded))
		require.Len(t, uploaded.Files, 1)
		require.NotNil(t, uploaded.Files[0].Ref)
		uploadRef := *uploaded.Files[0].Ref
		uploadPath := filepath.Join(api.homePath, "uploads", owner.ID, uploaded.Files[0].Name)
		require.FileExists(t, uploadPath, "instrument: the accepted upload must be on disk under the session's uploads dir")

		// The tool-generated inline attachment: the second session scope the
		// gateway releases on an explicit session delete.
		require.NoError(t, os.MkdirAll(media.TempDir(), 0o700))
		inlinePath := filepath.Join(media.TempDir(), "issue1435-r5-inline-"+owner.ID+".png")
		require.NoError(t, os.WriteFile(inlinePath, []byte("inline tool result bytes"), 0o600))
		inlineRef, err := store.Store(inlinePath, media.MediaMeta{
			Filename:      "chart.png",
			ContentType:   "image/png",
			Source:        "tool:inline",
			CleanupPolicy: media.CleanupPolicyForgetOnly,
		}, media.SessionInlineScopePrefix+owner.ID)
		require.NoError(t, err)

		// Both mappings exist before the sweep (positive control).
		_, indexed := store.RefByPath(uploadPath)
		require.True(t, indexed, "instrument: the accepted upload must be indexed before retention")
		_, indexed = store.RefByPath(inlinePath)
		require.True(t, indexed, "instrument: the inline attachment must be indexed before retention")

		// A real transcript, aged out of the retention window.
		require.NoError(t, sessions.AppendTranscriptStrict(owner.ID, session.TranscriptEntry{
			ID: "issue1435-r5-entry-" + owner.ID, Role: "user", Content: "keep it short",
			Timestamp: time.Now().UTC(),
		}))
		transcriptPath := filepath.Join(sessions.BaseDir(), owner.ID, "transcript.jsonl")
		require.NoError(t, os.Chtimes(transcriptPath, old, old))

		removed, err := sessions.RetentionSweep(1)
		require.NoError(t, err)
		require.Positive(t, removed, "instrument: the real retention sweep must remove the expired transcript")

		// Positive control: the conversation and its upload bytes are gone.
		_, statErr := os.Stat(uploadPath)
		require.ErrorIs(t, statErr, os.ErrNotExist, "instrument: retention must really have deleted the upload bytes")

		// THE ASSERTION — observed with a NON-MUTATING read, before any
		// Resolve: retention itself must have released the mappings. Both
		// scopes are asserted non-fatally so one run reports every scope
		// that is still live.
		_, stillIndexed := store.RefByPath(uploadPath)
		assert.False(t, stillIndexed,
			"#1435 R5 item 1: retention removed the conversation and its upload bytes, but the upload mapping is still live in the running store")
		_, stillIndexed = store.RefByPath(inlinePath)
		assert.False(t, stillIndexed,
			"#1435 R5 item 1: retention removed the conversation, but the inline media mapping is still live in the running store")

		// SaveRegistry must not re-serialize the dead entries.
		store.SaveRegistry()
		entries := issue1435R5RegistryEntries(t, api.homePath)
		assert.NotContains(t, entries, uploadRef,
			"#1435 R5 item 1: the dead upload mapping must not be re-serialized into the registry")
		assert.NotContains(t, entries, inlineRef,
			"#1435 R5 item 1: the dead inline mapping must not be re-serialized into the registry")
		assert.Equal(t, 0, issue1435R5DeadPathEntries(entries, uploadPath, inlinePath),
			"#1435 R5 item 1: no registry entry may point at a removed conversation's media")

		cycles = append(cycles, cycle{uploadPath: uploadPath, inlinePath: inlinePath, uploadRef: uploadRef, inlineRef: inlineRef})
	}

	// No accumulation across repeated retention cycles.
	store.SaveRegistry()
	entries := issue1435R5RegistryEntries(t, api.homePath)
	deadPaths := make([]string, 0, 2*len(cycles))
	for _, c := range cycles {
		deadPaths = append(deadPaths, c.uploadPath, c.inlinePath)
		assert.NotContains(t, entries, c.uploadRef,
			"#1435 R5 item 1: repeated retention cycles must not re-serialize a dead upload mapping")
		assert.NotContains(t, entries, c.inlineRef,
			"#1435 R5 item 1: repeated retention cycles must not re-serialize a dead inline mapping")
	}
	assert.Equal(t, 0, issue1435R5DeadPathEntries(entries, deadPaths...),
		"#1435 R5 item 1: repeated retention cycles must not accumulate dead mappings")
}
