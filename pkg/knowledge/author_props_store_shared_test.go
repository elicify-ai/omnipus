// Regression tests: a direct index refresh reuses one properties-index handle
// per collection instead of opening SQLite per edit (Claude review cut-list
// perf, deferred by fix3/gateway-refresh, fixed on fix4/index-perf).
//
// Oracle: count calls through the propindexOpen seam across N consecutive
// refreshes. The companion test pins the safety half — a deleted
// properties.db (the documented rebuild request) must be reopened, not written
// into through a handle on the unlinked file.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/records/propindex"
)

func perfRequireSQLite(t *testing.T) {
	t.Helper()
	if err := records.RequirePropertyIndex(records.CapabilityOpenIndex); err != nil {
		t.Skipf("properties index unavailable on this build: %v", err)
	}
}

// perfCountOpens wraps the propindexOpen seam and restores it (and drops any
// cached handles) at cleanup.
func perfCountOpens(t *testing.T) *atomic.Int64 {
	t.Helper()
	var n atomic.Int64
	orig := propindexOpen
	closeIdlePropertiesStores()
	propindexOpen = func(ctx context.Context, path string, opts propindex.Options) (propindex.Store, error) {
		n.Add(1)
		return orig(ctx, path, opts)
	}
	t.Cleanup(func() {
		closeIdlePropertiesStores()
		propindexOpen = orig
	})
	return &n
}

func TestRefreshIndexesForNote_OpensPropertiesStoreOncePerCollection(t *testing.T) {
	perfRequireSQLite(t)
	home, _, root := a4Fixture(t, "kb")
	opens := perfCountOpens(t)

	const n = 6
	for i := 0; i < n; i++ {
		a4Note(t, root, "Rec.md", "---\nstatus: s"+strings.Repeat("x", i)+"\n---\n# Rec\n")
		require.Empty(t, refreshIndexesForNote(context.Background(), home, root, "Rec.md"))
	}
	require.Equal(t, int64(1), opens.Load(), "%d consecutive refreshes must open the properties store once", n)

	rows := ifxPropPaths(t, home, root)
	row, ok := rows["Rec.md"]
	require.True(t, ok)
	onDisk, err := os.ReadFile(filepath.Join(root, "Rec.md"))
	require.NoError(t, err)
	require.Equal(t, propindex.SourceHash(onDisk), row.SourceHash, "the last write must be what the index holds")
}

// Concurrent refreshes share the handle without closing it under each other.
func TestRefreshIndexesForNote_ConcurrentRefreshesShareHandle(t *testing.T) {
	perfRequireSQLite(t)
	home, _, root := a4Fixture(t, "kb")
	opens := perfCountOpens(t)

	const n = 8
	for i := 0; i < n; i++ {
		a4Note(t, root, "C"+strings.Repeat("c", i)+".md", "# C\n")
	}
	var wg sync.WaitGroup
	warnings := make([]string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			warnings[i] = refreshIndexesForNote(context.Background(), home, root, "C"+strings.Repeat("c", i)+".md")
		}(i)
	}
	wg.Wait()
	for i, w := range warnings {
		require.Empty(t, w, "refresh %d", i)
	}
	require.Equal(t, int64(1), opens.Load())
	rows := ifxPropPaths(t, home, root)
	for i := 0; i < n; i++ {
		_, ok := rows["C"+strings.Repeat("c", i)+".md"]
		require.True(t, ok, "row %d must be indexed", i)
	}
}

// Deleting properties.db is the documented way to ask for a rebuild. A cached
// handle must notice and reopen, rather than keep writing into an unlinked file.
func TestRefreshIndexesForNote_ReopensWhenPropertiesFileReplaced(t *testing.T) {
	perfRequireSQLite(t)
	home, _, root := a4Fixture(t, "kb")
	opens := perfCountOpens(t)

	a4Note(t, root, "A.md", "# A\n")
	require.Empty(t, refreshIndexesForNote(context.Background(), home, root, "A.md"))

	p, err := PropertiesIndexPath(home, root)
	require.NoError(t, err)
	for _, f := range []string{p, p + "-wal", p + "-shm", p + "-journal"} {
		_ = os.Remove(f)
	}

	a4Note(t, root, "B.md", "# B\n")
	require.Empty(t, refreshIndexesForNote(context.Background(), home, root, "B.md"))
	require.Equal(t, int64(2), opens.Load(), "a replaced database file must be reopened")

	rows := ifxPropPaths(t, home, root)
	_, ok := rows["B.md"]
	require.True(t, ok, "the write after the file was deleted must land in the new database")
}
