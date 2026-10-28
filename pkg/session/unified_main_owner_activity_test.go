package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/require"
)

// Follow-up RED pack. Oracles come from chief requirements R9-R11:
// adoption changes ONLY owner, never activity, list order, or other identity;
// creation retains normal initial timestamps; retries and competing owners
// remain compare-and-set operations. The store and filesystem are real.
//
// R9/R11 fixtures use fixed, separated historical times (not sleeps), both
// possible sources of composed UpdatedAt, a nonempty title, and a nonzero
// partially-seen attention mark. Fresh stores bypass every pre-adoption cache.
// Comparing parsed identity maps deliberately ignores JSON object-key ordering,
// but ignores NO identity field other than owner.
//
// R10 retains the original R5 16-caller test and adds a consecutive same-owner
// retry and two independently cached stores over one directory. No production
// seams or locks are mocked. CHECK must kill timestamp bumps, dropped identity
// fields/attention, repeat-adoption writes, and a compare/check outside the
// shared lock. GREEN and mutation proof are deferred to independent CHECK.

func mainOwnerActivityDocument(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err, "read persisted JSON fixture %s", path)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(raw, &doc), "parse persisted JSON fixture %s", path)
	require.NotNil(t, doc, "fixture must be a JSON object")
	return doc
}

func mainOwnerActivityWriteDocument(t *testing.T, path string, doc map[string]any) {
	t.Helper()
	raw, err := json.Marshal(doc)
	require.NoError(t, err, "encode the explicit fixture, not implementation-derived expectations")
	require.NoError(t, os.WriteFile(path, raw, 0o600), "persist fixture %s", path)
}

func mainOwnerActivityOpenStore(t *testing.T, baseDir string) *UnifiedStore {
	t.Helper()
	store, err := NewUnifiedStore(baseDir)
	require.NoError(t, err, "open an independent store over the same directory")
	t.Cleanup(func() {
		require.NoError(t, store.Close(), "close the independent store")
	})
	return store
}

// Only setup writes historical timestamps out of band, then callers MUST open
// another store before invoking the real adoption API. Pending stats are flushed
// first, so no background writer can race the fixture or recency comparison.
func mainOwnerActivitySeedTimes(t *testing.T, store *UnifiedStore, id string, created, identityUpdated, statsUpdated time.Time) {
	t.Helper()
	require.NoError(t, store.FlushSessionStats(id), "settle all pending fixture stats")
	identityPath := filepath.Join(store.BaseDir(), id, "meta.json")
	identity := mainOwnerActivityDocument(t, identityPath)
	identity["created_at"] = created.Format(time.RFC3339Nano)
	identity["updated_at"] = identityUpdated.Format(time.RFC3339Nano)
	mainOwnerActivityWriteDocument(t, identityPath, identity)
	// A minimal valid stats group is sufficient: R9 concerns its recency,
	// not usage accounting. Its timestamp is deliberately distinct from meta's.
	mainOwnerActivityWriteDocument(t, filepath.Join(store.BaseDir(), id, "stats.json"), map[string]any{
		"updated_at": statsUpdated.Format(time.RFC3339Nano),
	})
}

func mainOwnerActivityListIDs(t *testing.T, store *UnifiedStore) []string {
	t.Helper()
	metas, err := store.ListSessions()
	require.NoError(t, err, "read the actual recency-sorted session list")
	ids := make([]string, len(metas))
	for i, meta := range metas {
		ids[i] = meta.ID
	}
	return ids
}

func TestMainOwnerActivity_AdoptionPreservesRecencyAndIdentity(t *testing.T) {
	// R9: adoption is not activity. R11: the parsed identity changes only owner.
	for _, statsIsNewer := range []bool{false, true} {
		name := "identity_supplies_recency"
		if statsIsNewer {
			name = "stats_supply_recency"
		}
		t.Run(name, func(t *testing.T) {
			seed := newTestStore(t)
			main, err := seed.GetOrCreateMainSession("owner-workspace", "mia")
			require.NoError(t, err, "seed an ownerless main")
			anchor, err := seed.GetOrCreateMainSession("owner-list-anchor", "jim")
			require.NoError(t, err, "seed a separately newer list entry")
			title := "Keep the original main identity"
			require.NoError(t, seed.SetMeta(main.ID, MetaPatch{Title: &title}))

			// Arbitrary fixed fixture times, separated by hours to avoid ties.
			created := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
			firstOutcome := created.Add(time.Hour)
			lastOutcome := created.Add(2 * time.Hour)
			appendOutcome(t, seed, main.ID, "owner-first-outcome", generated.GoalOutcomeEndingMet, firstOutcome)
			appendOutcome(t, seed, main.ID, "owner-last-outcome", generated.GoalOutcomeEndingRoundsExhausted, lastOutcome)
			seen, err := seed.AckAttentionSeen(main.ID, firstOutcome.UnixMilli())
			require.NoError(t, err, "seed an attention mark with seen and unseen outcomes")
			require.Equal(t, firstOutcome.UnixMilli(), seen, "fixture acknowledges only the first outcome")
			identityUpdated, statsUpdated := lastOutcome, firstOutcome
			wantUpdated := identityUpdated
			if statsIsNewer {
				identityUpdated, statsUpdated = firstOutcome, lastOutcome
				wantUpdated = statsUpdated
			}
			mainOwnerActivitySeedTimes(t, seed, main.ID, created, identityUpdated, statsUpdated)
			anchorUpdated := created.Add(3 * time.Hour)
			mainOwnerActivitySeedTimes(t, seed, anchor.ID, created, anchorUpdated, created)

			store := mainOwnerActivityOpenStore(t, seed.BaseDir())
			before, err := store.GetMeta(main.ID)
			require.NoError(t, err, "read the historical fixture through a fresh store")
			require.Equal(t, "", before.Owner, "fixture is eligible for a successful adoption")
			require.Equal(t, "mia", before.AgentID, "fixture retains a nonempty agent identity")
			require.Equal(t, "owner-workspace", before.WorkspaceID, "fixture retains a nonempty workspace")
			require.Equal(t, title, before.Title, "fixture has a nonempty title to preserve")
			require.Equal(t, SessionTypeMain, before.Type, "fixture is a real main")
			require.Equal(t, created, before.CreatedAt, "fixture has its fixed original creation time")
			require.Equal(t, wantUpdated, before.UpdatedAt, "fixture exercises the intended recency source")
			require.Equal(t, AttentionMark{OutcomeOrder: lastOutcome.UnixMilli(), SeenOrder: firstOutcome.UnixMilli()}, before.Attention, "nonzero attention must be present before testing its preservation")
			wantOrder := []string{anchor.ID, main.ID}
			require.Equal(t, wantOrder, mainOwnerActivityListIDs(t, store), "newer anchor must precede the older adoption target")
			metaPath := filepath.Join(store.BaseDir(), main.ID, "meta.json")
			statsPath := filepath.Join(store.BaseDir(), main.ID, "stats.json")
			beforeIdentity := mainOwnerActivityDocument(t, metaPath)
			beforeStats := mainOwnerActivityDocument(t, statsPath)

			adopted, err := requireMainOwnerAdopter(t, store).AdoptMainSessionOwner(main.ID, "alice")
			require.NoError(t, err, "first owner adoption succeeds")
			require.Equal(t, true, adopted, "an owner must really be stamped, not a no-op")
			fresh := mainOwnerActivityOpenStore(t, store.BaseDir())
			after, err := fresh.GetMeta(main.ID)
			require.NoError(t, err, "read adopted identity without the writing cache")
			require.Equal(t, "alice", after.Owner, "adoption persists the supplied person")
			require.Equal(t, before.UpdatedAt, after.UpdatedAt, "successful adoption does not become session activity")
			require.Equal(t, wantOrder, mainOwnerActivityListIDs(t, fresh), "adoption cannot move an old main above a newer session")
			require.Equal(t, wantOrder, mainOwnerActivityListIDs(t, store), "writing-store list also remains activity-neutral")
			afterIdentity := mainOwnerActivityDocument(t, metaPath)
			require.Equal(t, "alice", afterIdentity["owner"], "the real identity file carries the new owner")
			delete(beforeIdentity, "owner")
			delete(afterIdentity, "owner")
			require.Equal(t, beforeIdentity, afterIdentity, "ALL parsed identity fields except owner must remain identical, including attention and both timestamps")
			require.Equal(t, beforeStats, mainOwnerActivityDocument(t, statsPath), "adoption must not rewrite a separate recency or stats group")
			wantMeta := *before
			wantMeta.Owner = "alice"
			require.Equal(t, &wantMeta, after, "the entire composed metadata changes only owner")
		})
	}
}

func TestMainOwnerActivity_CreationInitializesAndPersistsTimestamps(t *testing.T) {
	// R9 no-regression: creation initializes both clocks together during its
	// call and persists them, rather than inheriting adoption's neutral behavior.
	// The call interval is a timestamp oracle, NOT a performance deadline.
	for _, tc := range []struct {
		name, owner string
		legacy      bool
	}{
		{name: "legacy_creation_control", legacy: true},
		{name: "owned_creation", owner: "alice"},
		{name: "ownerless_owned_creation", owner: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newTestStore(t)
			var creator mainOwnerCreator
			if !tc.legacy {
				creator = requireMainOwnerCreator(t, store)
			}
			started := time.Now().UTC()
			var meta *UnifiedMeta
			var err error
			if tc.legacy {
				meta, err = store.GetOrCreateMainSession("owner-workspace", "mia")
			} else {
				meta, err = creator.GetOrCreateMainSessionOwned("owner-workspace", "mia", tc.owner)
			}
			finished := time.Now().UTC()
			require.NoError(t, err, "main creation succeeds")
			require.NotNil(t, meta, "creation returns its initialized identity")
			require.False(t, meta.CreatedAt.IsZero(), "new main must have a real creation time")
			require.False(t, meta.CreatedAt.Before(started), "creation time cannot predate its creation call")
			require.False(t, meta.CreatedAt.After(finished), "creation time cannot be later than its completed call")
			require.Equal(t, meta.CreatedAt, meta.UpdatedAt, "a new main starts with matching creation and update times")
			require.Equal(t, tc.owner, meta.Owner, "timestamp initialization does not lose the requested owner")
			persisted := mainOwnerReopenMeta(t, store, meta.ID)
			require.Equal(t, meta.CreatedAt, persisted.CreatedAt, "initial creation timestamp persists exactly")
			require.Equal(t, meta.UpdatedAt, persisted.UpdatedAt, "initial activity timestamp persists exactly")
		})
	}
}

func TestMainOwnerActivity_SameOwnerRetryDoesNotWrite(t *testing.T) {
	// R10: two CONSECUTIVE calls by the same owner return true, then false.
	store := newTestStore(t)
	adopter := requireMainOwnerAdopter(t, store)
	main, err := store.GetOrCreateMainSession("owner-workspace", "mia")
	require.NoError(t, err, "seed an ownerless main")
	require.Equal(t, "", main.Owner, "first call must be an actual adoption")
	adopted, err := adopter.AdoptMainSessionOwner(main.ID, "alice")
	require.NoError(t, err, "first adoption succeeds")
	require.Equal(t, true, adopted, "first same-owner call wins")
	first := mainOwnerReopenMeta(t, store, main.ID)
	require.Equal(t, "alice", first.Owner, "first owner is durably stamped")
	beforeFiles := mainOwnerFiles(t, store.BaseDir())
	require.Contains(t, beforeFiles, filepath.Join(main.ID, "meta.json"), "idempotence snapshot includes the real identity file")

	adopted, err = adopter.AdoptMainSessionOwner(main.ID, "alice")
	require.NoError(t, err, "second same-owner call is not an error")
	require.Equal(t, false, adopted, "already being the owner does not count as a second adoption")
	require.Equal(t, beforeFiles, mainOwnerFiles(t, store.BaseDir()), "second same-owner call leaves all persisted file bytes unchanged")
	require.Equal(t, first, mainOwnerReopenMeta(t, store, main.ID), "second call leaves every persisted metadata field unchanged")
}

func TestMainOwnerActivity_TwoStoreRaceHasOnePersistedWinner(t *testing.T) {
	// R10 optional cross-instance case is cheap: the existing real-store helper
	// opens another UnifiedStore over the very same base directory. Both caches
	// see the unowned identity BEFORE either different-owner caller may start.
	first := newTestStore(t)
	main, err := first.GetOrCreateMainSession("owner-workspace", "mia")
	require.NoError(t, err, "seed the one shared main")
	require.Equal(t, "", main.Owner, "first store initially sees no owner")
	second := mainOwnerActivityOpenStore(t, first.BaseDir())
	secondMeta, err := second.GetMeta(main.ID)
	require.NoError(t, err, "second store has independently loaded the same main")
	require.Equal(t, "", secondMeta.Owner, "second cache also starts ownerless")
	firstAdopter := requireMainOwnerAdopter(t, first)
	secondAdopter := requireMainOwnerAdopter(t, second)
	contenders := []struct {
		adopter mainOwnerAdopter
		owner   string
	}{
		{adopter: firstAdopter, owner: "alice"},
		{adopter: secondAdopter, owner: "bob"},
	}
	type result struct {
		owner   string
		adopted bool
		err     error
	}
	results := make(chan result, len(contenders))
	start := make(chan struct{})
	var ready, done sync.WaitGroup
	ready.Add(len(contenders))
	done.Add(len(contenders))
	for _, contender := range contenders {
		go func(adopter mainOwnerAdopter, owner string) {
			defer done.Done()
			ready.Done()
			<-start
			adopted, err := adopter.AdoptMainSessionOwner(main.ID, owner)
			results <- result{owner: owner, adopted: adopted, err: err}
		}(contender.adopter, contender.owner)
	}
	ready.Wait()
	close(start)
	done.Wait()
	close(results)
	completed, winners := 0, 0
	winner := ""
	for r := range results {
		completed++
		require.NoError(t, r.err, "cross-store adoption by %s", r.owner)
		if r.adopted {
			winners++
			winner = r.owner
		}
	}
	require.Equal(t, len(contenders), completed, "both independent stores must return their result")
	require.Equal(t, 1, winners, "different store-local locks may not permit two owners to win")
	require.Equal(t, winner, mainOwnerReopenMeta(t, first, main.ID).Owner, "fresh persisted owner equals the one caller that won, not a later writer")
}
