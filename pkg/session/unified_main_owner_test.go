package session

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// Owner-stamp RED pack. All oracles derive from the dispatch's R1-R7, not
// observed implementation output. The real store and filesystem are used;
// fresh stores read persistence before the writing store is closed.
//
// Cases: new owned/ownerless main (R1/R3); unchanged existing owned/ownerless
// main, including identical files (R2/R7); first adoption and same/different
// repeat adopters (R4); 16 distinct simultaneous adopters (R5); empty owners
// and every non-main type, with byte-for-byte metadata preservation (R6).
//
// CHECK, not RED, must prove these mutations die: omit the creation owner or
// its disk write; overwrite an existing owner; adopt during create-or-reuse;
// return true on repeat adoption; remove the main/empty-owner guard; split
// adoption's read/check/write outside its lock. GREEN and mutation proof are
// deferred to the independent CHECK instance. Invalid IDs and storage errors
// have no specified new error contract in this dispatch and are not invented.

// These exact-signature interfaces call the real UnifiedStore methods. They
// let the RED tests fail loudly with BLOCKED while the two specified methods
// are absent, rather than preventing all existing package tests from compiling.
type mainOwnerCreator interface {
	GetOrCreateMainSessionOwned(workspaceID, agentID, owner string) (*UnifiedMeta, error)
}

type mainOwnerAdopter interface {
	AdoptMainSessionOwner(sessionID, owner string) (adopted bool, err error)
}

func requireMainOwnerCreator(t *testing.T, store *UnifiedStore) mainOwnerCreator {
	t.Helper()
	creator, ok := any(store).(mainOwnerCreator)
	if !ok {
		t.Fatal("BLOCKED: UnifiedStore.GetOrCreateMainSessionOwned not implemented — required by owner-stamp R1/R2/R3/R7")
	}
	return creator
}

func requireMainOwnerAdopter(t *testing.T, store *UnifiedStore) mainOwnerAdopter {
	t.Helper()
	adopter, ok := any(store).(mainOwnerAdopter)
	if !ok {
		t.Fatal("BLOCKED: UnifiedStore.AdoptMainSessionOwner not implemented — required by owner-stamp R4/R5/R6")
	}
	return adopter
}

func mainOwnerReopenMeta(t *testing.T, store *UnifiedStore, sessionID string) *UnifiedMeta {
	t.Helper()
	fresh, err := NewUnifiedStore(store.BaseDir())
	require.NoError(t, err, "open an independent store over the persisted files")
	t.Cleanup(func() {
		require.NoError(t, fresh.Close(), "close the independent store")
	})
	meta, err := fresh.GetMeta(sessionID)
	require.NoError(t, err, "read persisted metadata without the writing store's cache")
	require.NotNil(t, meta, "persisted session must exist")
	return meta
}

// Snapshot names and bytes, not just the known metadata filename, so replacing
// an existing main or creating an extra identity cannot masquerade as reuse.
func mainOwnerFiles(t *testing.T, root string) map[string][]byte {
	t.Helper()
	files := make(map[string][]byte)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[rel] = raw
		return nil
	})
	require.NoError(t, err, "snapshot every persisted file")
	return files
}

func TestMainOwner_CreationPersistsGivenOwner(t *testing.T) {
	// R1: the supplied login account, not the agent ID, owns a new main.
	store := newTestStore(t)
	creator := requireMainOwnerCreator(t, store)
	const workspaceID, agentID, owner = "owner-workspace", "mia", "alice"

	meta, err := creator.GetOrCreateMainSessionOwned(workspaceID, agentID, owner)
	require.NoError(t, err, "create an owned main")
	require.NotNil(t, meta, "owned creation must return its main")
	require.Equal(t, owner, meta.Owner, "new main carries the supplied human username")
	require.Equal(t, SessionTypeMain, meta.Type, "owned creation still creates a main")
	require.Equal(t, workspaceID, meta.WorkspaceID, "owned creation preserves the workspace")
	require.Equal(t, agentID, meta.AgentID, "human ownership must not replace the agent identity")

	persisted := mainOwnerReopenMeta(t, store, meta.ID)
	require.Equal(t, meta.ID, persisted.ID, "fresh store reads the same main")
	require.Equal(t, owner, persisted.Owner, "owner is durable before closing the writing store")
	require.Equal(t, SessionTypeMain, persisted.Type, "persisted identity remains a main")
	require.Equal(t, workspaceID, persisted.WorkspaceID, "workspace survives reopening")
	require.Equal(t, agentID, persisted.AgentID, "agent identity survives reopening")
}

func TestMainOwner_CreationKeepsExistingMainUnchanged(t *testing.T) {
	// R2/R7: reuse never doubles as adoption, for old owned OR ownerless mains.
	for _, tc := range []struct {
		name, existingOwner string
		createdOwned        bool
	}{
		{name: "owned_api_created", existingOwner: "alice", createdOwned: true},
		{name: "legacy_owned", existingOwner: "alice"},
		{name: "legacy_ownerless", existingOwner: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newTestStore(t)
			creator := requireMainOwnerCreator(t, store)
			const workspaceID, agentID = "owner-workspace", "mia"
			var legacy *UnifiedMeta
			var err error
			if tc.createdOwned {
				legacy, err = creator.GetOrCreateMainSessionOwned(workspaceID, agentID, tc.existingOwner)
				require.NoError(t, err, "seed through the new owned API")
				require.Equal(t, tc.existingOwner, legacy.Owner, "first owned call stamps its owner")
			} else {
				legacy, err = store.GetOrCreateMainSession(workspaceID, agentID)
				require.NoError(t, err, "seed through the original API")
				require.Equal(t, "", legacy.Owner, "legacy fixture starts without a human owner")
				if tc.existingOwner != "" {
					require.NoError(t, store.SetMeta(legacy.ID, MetaPatch{Owner: &tc.existingOwner}), "seed an already-owned legacy main")
				}
			}
			// Make preservation meaningful: this is an existing conversation,
			// not merely an empty main that a replacement could imitate.
			title := "Existing conversation to preserve"
			require.NoError(t, store.SetMeta(legacy.ID, MetaPatch{Title: &title}))
			require.NoError(t, store.AppendTranscript(legacy.ID, TranscriptEntry{
				ID: "owner-fix-existing-entry", Role: "user", AgentID: agentID,
				Content: "Keep this existing conversation.", Timestamp: legacy.CreatedAt,
			}), "seed a durable transcript that reuse must preserve")
			require.NoError(t, store.FlushSessionStats(legacy.ID), "settle background stats before the file snapshot")
			beforeMeta, err := store.GetMeta(legacy.ID)
			require.NoError(t, err, "capture the existing metadata")
			require.Equal(t, tc.existingOwner, beforeMeta.Owner, "fixture has its specified existing owner")
			beforeFiles := mainOwnerFiles(t, store.BaseDir())
			require.Contains(t, beforeFiles, filepath.Join(legacy.ID, "meta.json"), "snapshot must include the real identity file")

			reused, err := creator.GetOrCreateMainSessionOwned(workspaceID, agentID, "bob")
			require.NoError(t, err, "reuse the existing main despite a different supplied owner")
			require.NotNil(t, reused, "reuse returns the existing main")
			require.Equal(t, legacy.ID, reused.ID, "original API and owned API resolve the same session")
			require.Equal(t, beforeMeta, reused, "reuse leaves every returned metadata field unchanged")
			afterMeta, err := store.GetMeta(legacy.ID)
			require.NoError(t, err, "read metadata after reuse")
			require.Equal(t, beforeMeta, afterMeta, "reuse does not mutate cached metadata")
			require.Equal(t, beforeFiles, mainOwnerFiles(t, store.BaseDir()), "reuse preserves all file names and bytes, with no replacement or extra session")
			require.Equal(t, tc.existingOwner, mainOwnerReopenMeta(t, store, legacy.ID).Owner, "reuse must not change the persisted owner, including an empty owner")
		})
	}
}

func TestMainOwner_EmptyCreationOwnerRemainsEmpty(t *testing.T) {
	// R3: no-account installations may still create an ownerless main.
	store := newTestStore(t)
	creator := requireMainOwnerCreator(t, store)

	meta, err := creator.GetOrCreateMainSessionOwned("owner-workspace", "mia", "")
	require.NoError(t, err, "an empty creation owner is allowed")
	require.NotNil(t, meta, "empty-owner creation still returns a main")
	require.Equal(t, SessionTypeMain, meta.Type, "empty owner does not change session type")
	require.Equal(t, "", meta.Owner, "do not fabricate a human owner when no account exists")
	persisted := mainOwnerReopenMeta(t, store, meta.ID)
	require.Equal(t, meta.ID, persisted.ID, "empty-owner main is persisted under the same identity")
	require.Equal(t, "", persisted.Owner, "empty owner survives reopening")
}

func TestMainOwner_AdoptionPersistsOwnerOnce(t *testing.T) {
	// R4: exactly the first nonempty human owner may be stamped.
	store := newTestStore(t)
	adopter := requireMainOwnerAdopter(t, store)
	main, err := store.GetOrCreateMainSession("owner-workspace", "mia")
	require.NoError(t, err, "seed an ownerless main through the original API")
	require.Equal(t, "", main.Owner, "adoption fixture must be ownerless")

	adopted, err := adopter.AdoptMainSessionOwner(main.ID, "alice")
	require.NoError(t, err, "first adoption succeeds")
	require.Equal(t, true, adopted, "first adoption reports that it stamped the owner")
	afterFirst, err := store.GetMeta(main.ID)
	require.NoError(t, err, "read the newly adopted main")
	require.Equal(t, "alice", afterFirst.Owner, "successful adoption updates the cached owner")
	require.Equal(t, "alice", mainOwnerReopenMeta(t, store, main.ID).Owner, "first adoption is persisted immediately")
	beforeFiles := mainOwnerFiles(t, store.BaseDir())
	require.Contains(t, beforeFiles, filepath.Join(main.ID, "meta.json"), "repeat-adoption snapshot includes metadata")

	for _, laterOwner := range []string{"bob", "alice"} {
		adopted, err := adopter.AdoptMainSessionOwner(main.ID, laterOwner)
		require.NoError(t, err, "repeat adoption by %s is a harmless no-op", laterOwner)
		require.Equal(t, false, adopted, "repeat adoption by %s must not report a new stamp", laterOwner)
		unchanged, err := store.GetMeta(main.ID)
		require.NoError(t, err, "read after repeat adoption by %s", laterOwner)
		require.Equal(t, afterFirst, unchanged, "repeat adoption by %s changes no metadata", laterOwner)
		require.Equal(t, beforeFiles, mainOwnerFiles(t, store.BaseDir()), "repeat adoption by %s changes no persisted file", laterOwner)
		require.Equal(t, "alice", mainOwnerReopenMeta(t, store, main.ID).Owner, "repeat adoption by %s cannot replace the first persisted owner", laterOwner)
	}
}

func TestMainOwner_ConcurrentAdoptionHasOnePersistedWinner(t *testing.T) {
	// R5 fixes the fan-out at 16 distinct owners. The winner is intentionally
	// nondeterministic; exactly one success and owner==winner are exact oracles.
	store := newTestStore(t)
	adopter := requireMainOwnerAdopter(t, store)
	main, err := store.GetOrCreateMainSession("owner-workspace", "mia")
	require.NoError(t, err, "seed one shared ownerless main")
	require.Equal(t, "", main.Owner, "all contenders start from an ownerless main")

	const callers = 16 // R5: 16 goroutines with 16 distinct human owners.
	type result struct {
		owner   string
		adopted bool
		err     error
	}
	results := make(chan result, callers)
	start := make(chan struct{})
	var ready, done sync.WaitGroup
	ready.Add(callers)
	done.Add(callers)
	for i := 0; i < callers; i++ {
		owner := fmt.Sprintf("person-%02d", i)
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			adopted, err := adopter.AdoptMainSessionOwner(main.ID, owner)
			results <- result{owner: owner, adopted: adopted, err: err}
		}()
	}
	ready.Wait()
	close(start)
	done.Wait() // Join every caller before assertions, disk reads, or cleanup.
	close(results)

	completed, winners := 0, 0
	winner := ""
	for r := range results {
		completed++
		require.NoError(t, r.err, "concurrent adoption by %s", r.owner)
		if r.adopted {
			winners++
			winner = r.owner
		}
	}
	require.Equal(t, callers, completed, "every contender must return a result")
	require.Equal(t, 1, winners, "exactly one of the 16 different owners wins")
	cached, err := store.GetMeta(main.ID)
	require.NoError(t, err, "read the winning owner from the writing store")
	require.Equal(t, winner, cached.Owner, "cached owner must equal the caller that reported success")
	require.Equal(t, winner, mainOwnerReopenMeta(t, store, main.ID).Owner, "persisted owner must equal that same successful caller")
}

func TestMainOwner_AdoptionNoOpsWithoutOwnerOrMain(t *testing.T) {
	// R6: empty-owner and non-main inputs return false/nil without any write.
	for _, tc := range []struct {
		name  string
		typ   UnifiedSessionType
		owner string
	}{
		{name: "empty_owner_on_main", typ: SessionTypeMain, owner: ""},
		{name: "empty_owner_on_chat", typ: SessionTypeChat, owner: ""},
		{name: "non_main_chat", typ: SessionTypeChat, owner: "alice"},
		{name: "non_main_task", typ: SessionTypeTask, owner: "alice"},
		{name: "non_main_channel", typ: SessionTypeChannel, owner: "alice"},
		{name: "non_main_scheduled", typ: SessionTypeScheduled, owner: "alice"},
		{name: "non_main_heartbeat", typ: SessionTypeHeartbeat, owner: "alice"},
		{name: "non_main_verifier", typ: SessionTypeVerifier, owner: "alice"},
		{name: "non_main_delegate", typ: SessionTypeDelegate, owner: "alice"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newTestStore(t)
			adopter := requireMainOwnerAdopter(t, store)
			var meta *UnifiedMeta
			var err error
			if tc.typ == SessionTypeMain {
				meta, err = store.GetOrCreateMainSession("owner-workspace", "mia")
			} else {
				meta, err = store.NewSession(tc.typ, "webchat", "mia")
			}
			require.NoError(t, err, "seed the specified session type")
			require.Equal(t, tc.typ, meta.Type, "fixture exercises the intended type guard")
			require.Equal(t, "", meta.Owner, "fixture is ownerless so only the main/empty-owner guard can refuse")
			metaPath := filepath.Join(store.BaseDir(), meta.ID, "meta.json")
			beforeBytes, err := os.ReadFile(metaPath)
			require.NoError(t, err, "read the real metadata file before the no-op")

			adopted, err := adopter.AdoptMainSessionOwner(meta.ID, tc.owner)
			require.NoError(t, err, "ineligible adoption is not an error")
			require.Equal(t, false, adopted, "ineligible adoption must not claim a stamp")
			afterBytes, err := os.ReadFile(metaPath)
			require.NoError(t, err, "metadata must still exist after the no-op")
			require.Equal(t, beforeBytes, afterBytes, "no-op leaves meta.json byte-for-byte unchanged")
			afterMeta, err := store.GetMeta(meta.ID)
			require.NoError(t, err, "read metadata after the no-op")
			require.Equal(t, meta, afterMeta, "no-op also leaves cached metadata unchanged")
			require.Equal(t, "", mainOwnerReopenMeta(t, store, meta.ID).Owner, "no-op must leave the persisted owner empty")
		})
	}
}
