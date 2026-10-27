package agent

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// Issue #1435 Round 6 — founder Q148.
//
// Item 1 (silent-failure hunter R5-SF-1 HIGH, architect R5-ARCH-1, pr-test
// analyzer R5-T1): the session store publishes its media-release hook with a
// plain assignment (session.UnifiedStore.SetMediaReleaseFn) and reads it back
// without synchronisation (session.UnifiedStore.RetentionSweep). The
// production caller of the setter is NOT "once at boot": a configuration
// reload replaces the media store and calls AgentLoop.SetMediaStore again,
// which republishes the hook on the SAME shared session store while a
// retention sweep may be reading and invoking it.
//
// The overlap is a reachable Go data race. This case drives the real
// production pair — a store replacement (AgentLoop.SetMediaStore, which
// republishes the hook) overlapping a real retention sweep — and is
// expected to FAIL under `go test -race` on the current code.
//
// The second case pins the property the fix must keep: after a store
// replacement, the release goes to the CURRENT store, never to the replaced
// one.
//
// Item 2 (independent CHECK round-5 survivor M6b): the model-facing marker
// for a STORAGE FAULT must say the file was not removed, and must be
// distinguishable from the "attachment unavailable" marker. The contract is
// the developer's round-5 statement — a storage fault gets its own marker and
// NO media_unavailable notice, because "please re-attach it" is wrong advice
// for a temporary filesystem problem and the bytes may still be on disk.

// issue1435R6AgedConversation creates a conversation whose transcript is aged
// out of the retention window, so the real sweep removes it and really
// invokes the release hook. It returns the conversation id.
func issue1435R6AgedConversation(t *testing.T, store *session.UnifiedStore) string {
	t.Helper()
	owner, err := store.NewSession(session.SessionTypeChat, "web", "mia")
	require.NoError(t, err)
	require.NoError(t, store.AppendTranscriptStrict(owner.ID, session.TranscriptEntry{
		ID: "issue1435-r6-entry-" + owner.ID, Role: "user", Content: "keep it short",
		Timestamp: time.Now().UTC(),
	}))
	transcript := filepath.Join(store.BaseDir(), owner.ID, "transcript.jsonl")
	require.NoError(t, os.Chtimes(transcript, time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC), time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)))
	return owner.ID
}

// issue1435R6StoreMapping registers a real media mapping for a conversation
// in the given store and returns the stored path.
func issue1435R6StoreMapping(t *testing.T, store *media.FileMediaStore, scope, name string) string {
	t.Helper()
	dir := filepath.Join(media.TempDir(), name)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	path := filepath.Join(dir, name+".png")
	require.NoError(t, os.WriteFile(path, issue1435PNG(t, 8, 8), 0o600))
	_, err := store.Store(path, media.MediaMeta{
		Filename: name + ".png", ContentType: "image/png",
		Source: "upload:webchat", CleanupPolicy: media.CleanupPolicyForgetOnly,
	}, scope)
	require.NoError(t, err)
	return path
}

// Item 1 — the race. RED under -race on the current code.
func TestIssue1435R6_RetentionReleaseHookIsRaceFreeUnderStoreReplacement(t *testing.T) {
	h := wu22NewHarness(t, &issue1435R2Provider{})
	storeA := media.NewFileMediaStore()
	t.Cleanup(storeA.Stop)
	storeB := media.NewFileMediaStore()
	t.Cleanup(storeB.Stop)
	h.al.SetMediaStore(storeA)

	// Conversations aged out of the retention window, each with a mapping
	// in BOTH stores, so the release hook has something to release whichever
	// store happens to be current when the sweep runs.
	type aged struct {
		id    string
		pathA string
		pathB string
	}
	var conversations []aged
	for i := 0; i < 4; i++ {
		id := issue1435R6AgedConversation(t, h.store)
		conversations = append(conversations, aged{
			id:    id,
			pathA: issue1435R6StoreMapping(t, storeA, media.SessionUploadScopePrefix+id, "accepted-a-"+id),
			pathB: issue1435R6StoreMapping(t, storeB, media.SessionUploadScopePrefix+id, "accepted-b-"+id),
		})
	}
	// Positive control: both mappings are live before the overlap.
	for _, c := range conversations {
		_, indexed := storeA.RefByPath(c.pathA)
		require.True(t, indexed, "instrument: the old store must hold its mapping before the overlap")
		_, indexed = storeB.RefByPath(c.pathB)
		require.True(t, indexed, "instrument: the current store must hold its mapping before the overlap")
	}

	// The overlap: a configuration reload replaces the media store (which
	// republishes the release hook on the shared session store) while the
	// retention sweep reads and invokes that hook.
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 50; i++ {
			h.al.SetMediaStore(storeA)
			h.al.SetMediaStore(storeB)
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 50; i++ {
			if _, err := h.store.RetentionSweep(1); err != nil {
				t.Logf("sweep %d reported: %v", i, err)
			}
		}
	}()
	close(start)
	wg.Wait()

	// Positive control: the overlap really reached the release hook — every
	// removed conversation's mapping is gone from at least one of the two
	// stores (the release ran against whichever store was current).
	for _, c := range conversations {
		_, inA := storeA.RefByPath(c.pathA)
		_, inB := storeB.RefByPath(c.pathB)
		require.False(t, inA && inB,
			"#1435 R6 item 1: the release hook must have released the removed conversation's mapping from the store that was current")
	}
}

// Item 1 — the property the fix must keep: after a store replacement the
// release goes to the CURRENT store.
func TestIssue1435R6_ReplacementReleasesToTheCurrentStore(t *testing.T) {
	h := wu22NewHarness(t, &issue1435R2Provider{})
	storeA := media.NewFileMediaStore()
	t.Cleanup(storeA.Stop)
	storeB := media.NewFileMediaStore()
	t.Cleanup(storeB.Stop)
	h.al.SetMediaStore(storeA)

	id := issue1435R6AgedConversation(t, h.store)
	pathA := issue1435R6StoreMapping(t, storeA, media.SessionUploadScopePrefix+id, "accepted-old-"+id)
	// The replacement: the loop's media store is swapped for a new one.
	h.al.SetMediaStore(storeB)
	pathB := issue1435R6StoreMapping(t, storeB, media.SessionUploadScopePrefix+id, "accepted-current-"+id)
	_, indexed := storeB.RefByPath(pathB)
	require.True(t, indexed, "instrument: the current store must hold its mapping before the sweep")

	removed, err := h.store.RetentionSweep(1)
	require.NoError(t, err)
	require.Positive(t, removed, "instrument: the real retention sweep must remove the expired conversation")

	_, indexed = storeB.RefByPath(pathB)
	require.False(t, indexed,
		"#1435 R6 item 1: after a store replacement the release must reach the CURRENT store")
	_, indexed = storeA.RefByPath(pathA)
	require.True(t, indexed,
		"#1435 R6 item 1: the release must not have gone to the replaced store")
}

// Item 2 — the storage-fault marker wording. It must say the file was NOT
// removed, and it must be distinguishable from the unavailable marker.
func TestIssue1435R6_StorageFaultMarkerIsHonestAndDistinct(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	parent := filepath.Join(home, "uploads", "issue1435-r6-session")
	require.NoError(t, os.MkdirAll(parent, 0o700))
	path := filepath.Join(parent, "accepted.png")
	require.NoError(t, os.WriteFile(path, issue1435PNG(t, 8, 8), 0o600))
	store := media.NewFileMediaStore()
	t.Cleanup(store.Stop)
	ref, err := store.Store(path, media.MediaMeta{
		Filename: "accepted.png", ContentType: "image/png",
		Source: "upload:webchat", CleanupPolicy: media.CleanupPolicyForgetOnly,
	}, "upload:issue1435-r6-session")
	require.NoError(t, err)

	// The real storage fault: the stored file's parent directory is replaced
	// by a regular file, so the resolver cannot verify the file. This is NOT
	// a confirmed absence.
	backup := parent + "-issue1435-r6-offline"
	require.NoError(t, os.Rename(parent, backup))
	require.NoError(t, os.WriteFile(parent, []byte("temporary filesystem obstruction"), 0o600))
	t.Cleanup(func() {
		_ = os.Remove(parent)
		_ = os.Rename(backup, parent)
	})

	h := wu22NewHarness(t, &issue1435R2Provider{})
	h.al.SetMediaStore(store)
	h.ts.media, h.ts.opts.Media = []string{ref}, []string{ref}
	h.ts.userMessage = "Inspect this attachment and continue with the text."

	rr := h.responseStage()
	rr.rq.ri.turnMediaStore = store
	rr.rq.ri.wsDir = t.TempDir()
	rp := &agentLoopRunTurnPrepare{rc: &agentLoopRunTurnConductor{rx: &agentLoopRunTurnTools{rr: rr}}}
	_, stopped := rp.assembleInitialContext()
	require.False(t, stopped, "instrument: initial assembly must complete")

	// The marker the MODEL sees. The contract: a storage fault is not a lost
	// attachment — the bytes may still be on disk, so the marker must say the
	// file was not removed, and it must not read like the unavailable marker.
	const expectedMarker = "[attachment temporarily unreadable: "
	var marked string
	for _, message := range rr.rq.ri.messages {
		if strings.Contains(message.Content, "attachment") {
			marked = message.Content
		}
	}
	require.Contains(t, marked, expectedMarker+ref+" (storage error; the file was not removed)]",
		"#1435 R6 item 2: a storage fault must produce the honest marker that says the file was not removed")
	require.NotContains(t, marked, "[attachment unavailable:",
		"#1435 R6 item 2: a storage fault must NOT be reported with the unavailable marker")
	require.Equal(t, 0, issue1435R5CountNotices(h),
		"#1435 R6 item 2: a storage fault must not produce the unavailable notice — the bytes may still be on disk")
}
