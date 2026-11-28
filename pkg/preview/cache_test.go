package preview

import (
	"crypto/sha256"
	"testing"

	"github.com/stretchr/testify/require"
)

// AD-6: 64 MiB by decoded PNG bytes, not an entry-count approximation.
func TestPreviewCache_64MiBBudgetEvictsLeastRecentlyUsed(t *testing.T) {
	require.Equal(t, 64<<20, pageCacheBytes)
	cache := newPageCache(pageCacheBytes)
	page := Page{PNG: make([]byte, 4<<20)} // AD-2 maximum frame-sized fixture.
	for i := 1; i <= 16; i++ {
		cache.put(cacheKey{page: i}, page)
	}
	require.Equal(t, 64<<20, cache.used)
	_, ok := cache.get(cacheKey{page: 1})
	require.True(t, ok)
	cache.put(cacheKey{page: 17}, page)
	_, ok = cache.get(cacheKey{page: 2})
	require.False(t, ok, "page 2, not recently used page 1, must be evicted")
	_, ok = cache.get(cacheKey{page: 1})
	require.True(t, ok)
	_, ok = cache.get(cacheKey{page: 17})
	require.True(t, ok)
	require.Equal(t, 64<<20, cache.used)
	require.Len(t, cache.items, 16)
}

func TestPreviewCache_ReplacementAndOversizedEntryAccounting(t *testing.T) {
	cache := newPageCache(8)
	a, b, c := cacheKey{page: 1}, cacheKey{page: 2}, cacheKey{page: 3}
	cache.put(a, Page{PNG: []byte("1234")})
	cache.put(b, Page{PNG: []byte("5678")})
	cache.put(a, Page{PNG: []byte("12")})
	require.Equal(t, 6, cache.used, "replacing a four-byte entry with two bytes returns two bytes")
	cache.put(c, Page{PNG: []byte("xyz")})
	_, ok := cache.get(b)
	require.False(t, ok, "replacement refreshes a's recency")
	got, ok := cache.get(a)
	require.True(t, ok)
	require.Equal(t, []byte("12"), got.PNG)
	require.Equal(t, 5, cache.used)
	cache.put(cacheKey{page: 4}, Page{PNG: []byte("123456789")})
	require.Equal(t, 5, cache.used, "one oversized entry cannot flush valid cached pages")
	require.Len(t, cache.items, 2)
	cache.put(a, Page{PNG: []byte("123456789")})
	got, ok = cache.get(a)
	require.True(t, ok)
	require.Equal(t, []byte("12"), got.PNG)
}

// AD-9: all four key dimensions must participate; shared page numbers alone
// do not permit content, rendering settings or build/protocol collisions.
func TestPreviewCache_KeySeparatesSourcePageSettingsAndRenderer(t *testing.T) {
	cache := newPageCache(64)
	base := cacheKey{source: sha256.Sum256([]byte("PDF A")), page: 1, settings: "2x/2000/forms", renderer: "build-A/p1"}
	cache.put(base, Page{Number: 1, PNG: []byte("a")})
	keys := []cacheKey{base, base, base, base, base}
	keys[0].source = sha256.Sum256([]byte("PDF B"))
	keys[1].page = 2
	keys[2].settings = "1x/1000/forms"
	keys[3].renderer = "build-B/p1"
	keys[4].renderer = "build-A/p2"
	for _, key := range keys {
		_, ok := cache.get(key)
		require.False(t, ok, "different key dimension must miss: %+v", key)
	}
	got, ok := cache.get(base)
	require.True(t, ok)
	require.Equal(t, []byte("a"), got.PNG)
}
