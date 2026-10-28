package session

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Oracle: chief's stale-cache requirement. B's successful adoption is final;
// A's losing adoption must refresh A's ordinary GetMeta view before A may make
// an unrelated metadata update. Both the cache and the persisted owner must
// remain alice. No production implementation or lock is mocked or mutated.
func TestMainOwnerCache_LosingAdopterRefreshesOwnerBeforeMetadataUpdate(t *testing.T) {
	storeA := newTestStore(t)
	main, err := storeA.GetOrCreateMainSession("owner-workspace", "mia")
	require.NoError(t, err, "seed one ownerless main in store A")
	storeB := mainOwnerActivityOpenStore(t, storeA.BaseDir())

	beforeA, err := storeA.GetMeta(main.ID)
	require.NoError(t, err, "load the ownerless main into A's normal cache")
	require.Equal(t, "", beforeA.Owner, "A starts with the ownerless main cached")
	beforeB, err := storeB.GetMeta(main.ID)
	require.NoError(t, err, "load the same main into B's independent cache")
	require.Equal(t, "", beforeB.Owner, "B also starts with the ownerless main cached")

	adoptedByB, err := requireMainOwnerAdopter(t, storeB).AdoptMainSessionOwner(main.ID, "alice")
	require.NoError(t, err, "B adopts first")
	require.Equal(t, true, adoptedByB, "B must really win ownership")
	require.Equal(t, "alice", mainOwnerReopenMeta(t, storeB, main.ID).Owner, "B's winning owner is already durable")

	adoptedByA, err := requireMainOwnerAdopter(t, storeA).AdoptMainSessionOwner(main.ID, "bob")
	require.NoError(t, err, "A's later adoption is a harmless loss")
	require.Equal(t, false, adoptedByA, "A cannot replace B's winning owner")
	cachedA, err := storeA.GetMeta(main.ID)
	require.NoError(t, err, "read A through its ordinary cached API, without eviction or reopening")
	// Non-fatal intentionally: also exercise the real title update and disk
	// preservation below, even when A's stale cached view is the first defect.
	assert.Equal(t, "alice", cachedA.Owner, "losing adoption must refresh A's cached owner to the actual winner")

	title := "Title edited through the formerly stale store"
	require.NoError(t, storeA.SetMeta(main.ID, MetaPatch{Title: &title}), "perform an ordinary metadata update through A")
	persisted := mainOwnerReopenMeta(t, storeA, main.ID)
	require.Equal(t, title, persisted.Title, "the ordinary title update actually reached disk")
	require.Equal(t, "alice", persisted.Owner, "A's unrelated title update must not erase B's durable owner")
}
