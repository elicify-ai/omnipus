package catalog

// T067-04 — the pre-serialised bytes + ETag pair (T34c's package-level half;
// the REST half — headers, 304, 503 — lands in T067-10):
//
//	TestServed_QuotedStrongETagMatchesBody      — FR-017: ETag is the quoted SHA-256 of the served bytes
//	TestServed_EnvelopeShape                    — the ProvidersCatalog.yaml envelope, locality included; E9 unicode
//	TestServed_StaleComputedAtApply             — FR-037: stale true at 15 days, false at 13
//	TestServed_AtomicPairUnderConcurrentApply   — bytes and ETag swap as ONE pair (-race)

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func etagOf(body []byte) string {
	sum := sha256.Sum256(body)
	return `"` + hex.EncodeToString(sum[:]) + `"`
}

func TestServed_QuotedStrongETagMatchesBody(t *testing.T) {
	c := mustCatalog(t, loadFixture(t))
	s, ok := c.Served()
	require.True(t, ok)

	assert.Equal(t, etagOf(s.Body), s.ETag)
	assert.True(t, strings.HasPrefix(s.ETag, `"`) && strings.HasSuffix(s.ETag, `"`), "quoted")
	assert.False(t, strings.HasPrefix(s.ETag, `W/`), "strong, never weak")
	assert.Len(t, s.ETag, 66, "sha256 hex + 2 quotes")
}

func TestServed_EnvelopeShape(t *testing.T) {
	// E9: unicode survives byte-for-byte through load and serve.
	m := fixtureMap(t)
	provider(t, m, providerIndex(t, m, "zai"))["name"] = "智谱 AI"
	c := mustCatalog(t, encode(t, m))

	s, ok := c.Served()
	require.True(t, ok)

	var env map[string]any
	require.NoError(t, json.Unmarshal(s.Body, &env))

	for _, key := range []string{
		"schema_version", "version", "updated_at", "source",
		"default_resize_limits", "providers", "served_from", "stale",
	} {
		assert.Contains(t, env, key, "ProvidersCatalog.yaml requires %q", key)
	}
	assert.Equal(t, "2.0.0", env["schema_version"])
	assert.Equal(t, "v2026.8.22", env["version"])
	assert.Equal(t, "embedded", env["served_from"])
	assert.Equal(t, false, env["stale"])

	provs, ok := env["providers"].([]any)
	require.True(t, ok, "envelope 'providers' is not a list: %T", env["providers"])
	require.NotEmpty(t, provs)
	for _, p := range provs {
		row, ok := p.(map[string]any)
		require.True(t, ok, "provider entry is not a map: %T", p)
		for _, key := range []string{"id", "name", "company", "api", "tier", "auth_methods", "aliases", "locality", "models"} {
			assert.Contains(t, row, key, "CatalogProvider.yaml requires %q on %v", key, row["id"])
		}
		// Required arrays must be arrays, never null.
		assert.NotNil(t, row["aliases"], "aliases must be [] not null on %v", row["id"])
		assert.NotNil(t, row["models"], "models must be [] not null on %v", row["id"])
		modelRows, ok := row["models"].([]any)
		require.True(t, ok, "provider %v 'models' is not a list: %T", row["id"], row["models"])
		for _, mm := range modelRows {
			mrow, ok := mm.(map[string]any)
			require.True(t, ok, "model entry is not a map: %T", mm)
			for _, key := range []string{"id", "name", "context_window", "max_output_tokens", "input_modalities", "tool_call", "status"} {
				assert.Contains(t, mrow, key, "CatalogModel.yaml requires %q", key)
			}
		}
		if row["id"] == "zai" {
			assert.Equal(t, "智谱 AI", row["name"], "E9: unicode preserved")
			assert.Equal(t, "cloud", row["locality"], "FR-039: derived locality is served")
		}
	}
}

func TestServed_StaleComputedAtApply(t *testing.T) {
	updated, err := time.Parse(time.RFC3339, "2026-08-22T06:00:00Z")
	require.NoError(t, err)

	t.Run("15 days after updated_at → stale true", func(t *testing.T) {
		c := New()
		c.nowFn = func() time.Time { return updated.Add(15 * 24 * time.Hour) }
		require.NoError(t, c.Apply(loadFixture(t)))

		s, ok := c.Served()
		require.True(t, ok)
		assert.True(t, s.Stale)
		assert.Contains(t, string(s.Body), `"stale":true`)

		degraded, derr := c.Degraded()
		assert.True(t, degraded, "FR-037: a stale served document degrades /health")
		require.Error(t, derr)
	})

	t.Run("13 days after updated_at → stale false", func(t *testing.T) {
		c := New()
		c.nowFn = func() time.Time { return updated.Add(13 * 24 * time.Hour) }
		require.NoError(t, c.Apply(loadFixture(t)))

		s, ok := c.Served()
		require.True(t, ok)
		assert.False(t, s.Stale)
		assert.Contains(t, string(s.Body), `"stale":false`)

		degraded, _ := c.Degraded()
		assert.False(t, degraded)
	})
}

// T34c (package half): under concurrent Apply, a reader can never observe
// bytes from one apply paired with the ETag of another.
func TestServed_AtomicPairUnderConcurrentApply(t *testing.T) {
	docA := loadFixture(t)
	docB := fixtureWithVersion(t, "v2026.8.23")
	c := mustCatalog(t, docA)

	stop := make(chan struct{})
	var readers sync.WaitGroup
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				s, ok := c.Served()
				if !ok {
					t.Error("catalog lost its document mid-apply")
					return
				}
				if got := etagOf(s.Body); got != s.ETag {
					t.Errorf("torn pair: body hashes to %s but ETag is %s", got, s.ETag)
					return
				}
			}
		}()
	}

	for i := range 200 {
		if i%2 == 0 {
			require.NoError(t, c.Apply(docB))
		} else {
			require.NoError(t, c.Apply(docA))
		}
	}
	close(stop)
	readers.Wait()
}
