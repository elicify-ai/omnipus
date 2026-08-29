package catalog

// T067-04 — the persisted last-known-good store (spec §7 TDD Plan):
//
//	T6b  TestBoot_PersistedNewerThanEmbedded             — E6: persisted wins at boot, served as pulled
//	T15  TestStore_InvalidPersisted_Ignored_LegacyInvisible — US-3.AC7 (F-18): one WARN naming
//	     providers_catalog.json; zero log lines for capabilities_catalog.json
//	     TestBoot_CorruptEmbedded_NoCatalog              — E7: no catalog, one ERROR, everything degrades
//	     TestStore_NoLegacyFilenameInSource              — no code path names capabilities_catalog.json

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writePersisted(t *testing.T, dir string, data []byte) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, PersistedFileName), data, 0o600))
}

// ── T6b — E6 ────────────────────────────────────────────────────────────────

func TestBoot_PersistedNewerThanEmbedded(t *testing.T) {
	t.Run("persisted newer wins, served as pulled", func(t *testing.T) {
		dir := t.TempDir()
		writePersisted(t, dir, fixtureWithVersion(t, "v2026.8.23"))
		log := &captureLogger{}

		c := Boot(context.Background(), loadFixture(t), nil, NewFileStore(dir), log)

		assert.Equal(t, "v2026.8.23", c.Version().String())
		served, ok := c.Served()
		require.True(t, ok)
		assert.Equal(t, ServedPulled, served.From, "E6: a persisted last-known-good is pulled provenance")
		assert.Contains(t, string(served.Body), `"served_from":"pulled"`)
		assert.Empty(t, log.byLevel("WARN"))
	})

	t.Run("persisted older: embedded wins", func(t *testing.T) {
		dir := t.TempDir()
		writePersisted(t, dir, fixtureWithVersion(t, "v2026.8.21"))

		c := Boot(context.Background(), loadFixture(t), nil, NewFileStore(dir), &captureLogger{})

		assert.Equal(t, "v2026.8.22", c.Version().String())
		served, ok := c.Served()
		require.True(t, ok)
		assert.Equal(t, ServedEmbedded, served.From)
	})

	t.Run("no persisted file: embedded serves, no WARN", func(t *testing.T) {
		log := &captureLogger{}
		c := Boot(context.Background(), loadFixture(t), nil, NewFileStore(t.TempDir()), log)

		assert.Equal(t, "v2026.8.22", c.Version().String())
		served, ok := c.Served()
		require.True(t, ok)
		assert.Equal(t, ServedEmbedded, served.From)
		assert.Empty(t, log.byLevel("WARN"), "a fresh install is not a warning")
	})
}

// ── T15 — US-3.AC7, A-4, F-18 ───────────────────────────────────────────────

func TestStore_InvalidPersisted_Ignored_LegacyInvisible(t *testing.T) {
	t.Run("schema 1.0.0 persisted → one WARN reason=schema_version; legacy file invisible", func(t *testing.T) {
		dir := t.TempDir()
		writePersisted(t, dir, fixtureWithSchemaVersion(t, "1.0.0"))
		// The legacy capabilities-era file exists on disk; nothing may read
		// or even mention it (F-18).
		require.NoError(t, os.WriteFile(filepath.Join(dir, "capabilities_catalog.json"), []byte(`{"legacy":true}`), 0o600))
		log := &captureLogger{}

		c := Boot(context.Background(), loadFixture(t), nil, NewFileStore(dir), log)

		assert.Equal(t, "v2026.8.22", c.Version().String(), "the embedded snapshot serves")
		requireOneWarnWithReason(t, log, "schema_version")
		assert.GreaterOrEqual(t, log.mentions(PersistedFileName), 1, "the WARN names providers_catalog.json")
		assert.Zero(t, log.mentions("capabilities_catalog.json"), "zero log lines mention the legacy file")
	})

	t.Run("unreadable envelope → one WARN reason=invalid", func(t *testing.T) {
		dir := t.TempDir()
		writePersisted(t, dir, []byte(`{"schema_version": "2.0.0", truncated`))
		log := &captureLogger{}

		c := Boot(context.Background(), loadFixture(t), nil, NewFileStore(dir), log)

		assert.Equal(t, "v2026.8.22", c.Version().String())
		requireOneWarnWithReason(t, log, "invalid")
	})

	t.Run("persisted failing FR-033 → one WARN reason=invalid", func(t *testing.T) {
		dir := t.TempDir()
		m := fixtureMap(t)
		provider(t, m, providerIndex(t, m, "zai"))["api"] = "http://api.z.ai/api/paas/v4"
		writePersisted(t, dir, encode(t, m))
		log := &captureLogger{}

		c := Boot(context.Background(), loadFixture(t), nil, NewFileStore(dir), log)

		assert.Equal(t, "v2026.8.22", c.Version().String())
		requireOneWarnWithReason(t, log, "invalid")
	})
}

// ── E7 — corrupt embedded snapshot ──────────────────────────────────────────

func TestBoot_CorruptEmbedded_NoCatalog(t *testing.T) {
	t.Run("no persisted fallback: boots with no catalog, one ERROR", func(t *testing.T) {
		log := &captureLogger{}
		c := Boot(context.Background(), []byte(`{"not": "a catalog"`), nil, nil, log)

		assert.Nil(t, c.Document())
		_, ok := c.Served()
		assert.False(t, ok, "E7: GET /providers/catalog has nothing to serve (503 at the handler)")
		degraded, derr := c.Degraded()
		assert.True(t, degraded)
		require.Error(t, derr)
		assert.Len(t, log.byLevel("ERROR"), 1, "one ERROR at boot")

		h := c.Resolve("zai", "glm-5.2")
		assert.Zero(t, h.Window(), "every catalog consumer degrades to miss semantics")
	})

	t.Run("valid persisted last-known-good still serves", func(t *testing.T) {
		dir := t.TempDir()
		writePersisted(t, dir, fixtureWithVersion(t, "v2026.8.23"))
		log := &captureLogger{}

		c := Boot(context.Background(), []byte(`{`), nil, NewFileStore(dir), log)

		assert.Equal(t, "v2026.8.23", c.Version().String())
		served, ok := c.Served()
		require.True(t, ok)
		assert.Equal(t, ServedPulled, served.From)
	})
}

// ── DoD: no code path names capabilities_catalog.json ───────────────────────

func TestStore_NoLegacyFilenameInSource(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		src, err := os.ReadFile(e.Name())
		require.NoError(t, err)
		assert.NotContains(t, string(src), "capabilities_catalog", "%s: no code path may name the legacy file (A-4, F-18)", e.Name())
	}
}
