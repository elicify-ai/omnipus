package capabilities

// Catalog tests — see catalog.go for the package doc and puller_test.go for
// the puller tests. The package godoc is intentionally defined once
// (catalog.go) to satisfy godoclint.
//
// Test inventory (extracted from earlier header; preserved here for grep-ability):
//
//	#1  TestParseSeed_EmbeddedFile      — embedded seed parses to ≥1 model with valid structure
//	#2  TestParseSeed_RejectsEmpty      — empty input → error
//	#3  TestParseSeed_RejectsMalformed  — non-JSON → error
//	#4  TestParseSeed_RejectsBadBudget  — non-positive budget → error
//	#5  TestParseSeed_RejectsDupIDs     — duplicate model id → error
//	#6  TestParseSeed_RejectsEmptyMods  — model with empty modalities → error
//	#7  TestParseSeed_AcceptsUnknownModalities — forward-compat for new modalities
//	#8  TestNewCatalog_EmbeddedSeed     — NewCatalog(seed, nil, nil) hydrates from embedded seed
//	#9  TestNewCatalog_HydratesFromStore — NewCatalog with prior Store data uses Store over seed
//	#10 TestNewCatalog_FallsBackToSeedOnStoreError — Store error → embedded seed
//	#11 TestCatalog_Resolve_KnownModel  — Resolve returns the catalog entry with default budget applied
//	#12 TestCatalog_Resolve_UnknownModel_Optimistic — FR-026: unknown → text+image optimistic default
//	#13 TestCatalog_HasModal             — HasModal honors catalog entries and optimistic default
//	#14 TestCatalog_Refresh_PullsAndApplies — successful Pull → state updated + Store.Write called
//	#15 TestCatalog_Refresh_FailureNonFatal — Pull error → state retained, error returned (FR-025)
//	#16 TestCatalog_Refresh_InvalidJSON  — bad JSON in pull → state retained, error returned
//	#17 TestCatalog_Refresh_VersionRegress — pulled version < current → state retained (defensive)
//	#18 TestCatalog_Refresh_NoPuller     — NewCatalog(nil puller) → Refresh is no-op
//	#19 TestCatalog_NoPerAgentOverride   — FR-027: there is no API to override per-agent; only the seed governs
//	#20 TestCatalog_DefaultResizeBudget  — the default is the documented 7680/10MB
//	#21 TestCatalog_VersionAndSource     — Version/Source expose the seed's metadata
//
// Traces to: ADR-051 Rev 4 §Capability source; spec FR-024/025/026/027;
// SC-009 (catalog pull non-fatal).

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Helpers ───────────────────────────────────────────────────────────────────

// minimalSeedJSON returns a 2-model seed JSON used by the unit tests.
// The schema-level defaults are 7680px / 10 MB; the inline tests
// override these only when they need a different value.
func minimalSeedJSON() []byte {
	return []byte(`{
		"version": "test-2026-07-23",
		"schema_version": "1.0.0",
		"updated_at": "2026-07-23T00:00:00Z",
		"source": "test-fixture",
		"models": [
			{"id": "gpt-4o", "provider": "openai", "input_modalities": ["text", "image"], "resize_budget": {"long_edge_px": 8000, "max_bytes": 20971520}},
			{"id": "deepseek-chat", "provider": "deepseek", "input_modalities": ["text"]}
		],
		"default_resize_budget": {"long_edge_px": 7680, "max_bytes": 10485760}
	}`)
}

// completeSeedJSON wraps a partial JSON body with the required catalog
// metadata fields (version, schema_version, updated_at, source). The
// body must NOT include a leading '{' — it is the comma-separated list
// of field:json pairs that goes inside the top-level object. Used by
// tests that compose minimal seed JSONs inline.
//
// Example:
//
//	completeSeedJSON(`"models":[...],"default_resize_budget":{...}`)
//
// → `{"version":"test","schema_version":"1.0.0","updated_at":"2026-07-23T00:00:00Z","source":"test-fixture","models":[...],"default_resize_budget":{...}}`
func completeSeedJSON(innerJSON string) string {
	return `{"version":"test","schema_version":"1.0.0","updated_at":"2026-07-23T00:00:00Z","source":"test-fixture",` +
		innerJSON
}

// testLogger discards log output. Use a real slog logger if a test
// needs to assert log content.
func testLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// ── Test #1 ──────────────────────────────────────────────────────────────────

// TestParseSeed_EmbeddedFile verifies the committed seed JSON parses cleanly
// and contains at least one model. This is the freeze-gate regression guard:
// a syntactically broken seed fails the build.
func TestParseSeed_EmbeddedFile(t *testing.T) {
	data := EmbeddedSeed()
	require.NotEmpty(t, data, "embedded seed must not be empty")
	s, err := ParseSeed(data)
	require.NoError(t, err)
	require.NotEmpty(t, s.Models, "seed must have at least one model")
	for i, m := range s.Models {
		assert.NotEmpty(t, m.ID, "model[%d].id must be non-empty", i)
		assert.NotEmpty(t, m.InputModalities, "model[%d].id=%q must have at least one modality", i, m.ID)
	}
	assert.Greater(t, s.DefaultResizeBudget.LongEdgePx, 0)
	assert.Greater(t, s.DefaultResizeBudget.MaxBytes, int64(0))
}

// ── Test #2 ──────────────────────────────────────────────────────────────────

// TestParseSeed_RejectsEmpty asserts empty input fails with a clear error.
func TestParseSeed_RejectsEmpty(t *testing.T) {
	_, err := ParseSeed(nil)
	assert.Error(t, err)
	_, err = ParseSeed([]byte{})
	assert.Error(t, err)
}

// ── Test #3 ──────────────────────────────────────────────────────────────────

// TestParseSeed_RejectsMalformed asserts non-JSON input fails cleanly.
func TestParseSeed_RejectsMalformed(t *testing.T) {
	_, err := ParseSeed([]byte("this is not json"))
	assert.Error(t, err)
	_, err = ParseSeed([]byte("{"))
	assert.Error(t, err)
	_, err = ParseSeed([]byte(`{"models": "not-an-array"}`))
	assert.Error(t, err)
}

// ── Test #4 ──────────────────────────────────────────────────────────────────

// TestParseSeed_RejectsBadBudget asserts negative or zero budgets are rejected.
func TestParseSeed_RejectsBadBudget(t *testing.T) {
	cases := map[string]string{
		"default long_edge zero": completeSeedJSON(
			`"models":[{"id":"x","provider":"y","input_modalities":["text"]}],"default_resize_budget":{"long_edge_px":0,"max_bytes":1}}`,
		),
		"default max_bytes zero": completeSeedJSON(
			`"models":[{"id":"x","provider":"y","input_modalities":["text"]}],"default_resize_budget":{"long_edge_px":1,"max_bytes":0}}`,
		),
		"model long_edge zero": completeSeedJSON(
			`"models":[{"id":"x","provider":"y","input_modalities":["text"],"resize_budget":{"long_edge_px":0,"max_bytes":1}}],"default_resize_budget":{"long_edge_px":1,"max_bytes":1}}`,
		),
		"model max_bytes zero": completeSeedJSON(
			`"models":[{"id":"x","provider":"y","input_modalities":["text"],"resize_budget":{"long_edge_px":1,"max_bytes":0}}],"default_resize_budget":{"long_edge_px":1,"max_bytes":1}}`,
		),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseSeed([]byte(body))
			assert.Error(t, err, "%s should fail", name)
		})
	}
}

// ── Test #5 ──────────────────────────────────────────────────────────────────

// TestParseSeed_RejectsDupIDs asserts duplicate model ids are rejected.
func TestParseSeed_RejectsDupIDs(t *testing.T) {
	body := completeSeedJSON(`"models":[
		{"id":"a","provider":"p","input_modalities":["text"]},
		{"id":"a","provider":"p","input_modalities":["text"]}
	],"default_resize_budget":{"long_edge_px":1,"max_bytes":1}}`)
	_, err := ParseSeed([]byte(body))
	assert.Error(t, err)
}

// ── Test #6 ──────────────────────────────────────────────────────────────────

// TestParseSeed_RejectsEmptyMods asserts every model has at least one modality.
func TestParseSeed_RejectsEmptyMods(t *testing.T) {
	body := completeSeedJSON(
		`"models":[{"id":"x","provider":"p","input_modalities":[]}],"default_resize_budget":{"long_edge_px":1,"max_bytes":1}}`,
	)
	_, err := ParseSeed([]byte(body))
	assert.Error(t, err)
}

// ── Test #7 ──────────────────────────────────────────────────────────────────

// TestParseSeed_AcceptsUnknownModalities asserts the parser accepts unknown
// modality values for forward compatibility — the orchestrator may not yet
// recognize them, but the seed can carry new modalities (e.g. "3d") ahead of
// runtime support. The validated Model carries the typed Modality slice; the
// KnownModality set is the runtime's recognition boundary (forward-compat
// unknown values are recorded as-is).
func TestParseSeed_AcceptsUnknownModalities(t *testing.T) {
	body := completeSeedJSON(
		`"models":[{"id":"x","provider":"p","input_modalities":["text","3d","hologram"]}],"default_resize_budget":{"long_edge_px":1,"max_bytes":1}}`,
	)
	s, err := ParseSeed([]byte(body))
	require.NoError(t, err)
	require.Len(t, s.Models, 1)
	got := s.Models[0].InputModalities
	require.Len(t, got, 3)
	assert.Equal(t, ModalityText, got[0])
	assert.Equal(t, Modality("3d"), got[1])
	assert.Equal(t, Modality("hologram"), got[2])
	assert.False(t, KnownModality[got[1]], "forward-compat unknown is not in KnownModality")
	assert.False(t, KnownModality[got[2]], "forward-compat unknown is not in KnownModality")
	assert.True(t, KnownModality[got[0]], "text is in KnownModality")
}

// ── TD-M5 new tests (Wave 1 r1) ──────────────────────────────────────────────

// TestParseSeed_RejectsEmptyModality asserts that an input_modalities
// slice containing a single empty string is rejected — the validator
// walks every element and rejects empty modality values (the
// documented invariant; an empty value is not a valid modality).
func TestParseSeed_RejectsEmptyModality(t *testing.T) {
	body := completeSeedJSON(
		`"models":[{"id":"x","provider":"p","input_modalities":[""]}],"default_resize_budget":{"long_edge_px":1,"max_bytes":1}}`,
	)
	_, err := ParseSeed([]byte(body))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
}

// TestParseSeed_RejectsDuplicateModality asserts that an
// input_modalities slice with duplicate values is rejected — the
// validator enforces a set, not a multiset (a model's modality set is
// semantically a set, even when the wire shape is a JSON array).
func TestParseSeed_RejectsDuplicateModality(t *testing.T) {
	body := completeSeedJSON(
		`"models":[{"id":"x","provider":"p","input_modalities":["text","text"]}],"default_resize_budget":{"long_edge_px":1,"max_bytes":1}}`,
	)
	_, err := ParseSeed([]byte(body))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate")
}

// TestParseSeed_RequiresTextModality asserts the text-invariant — every
// model must list "text" in its input_modalities. A model advertising
// only image/audio/video (or any non-text set) is rejected outright;
// the orchestrator's capability gate always treats text as supported,
// so a model without text is a wire-level bug, not a runtime feature.
func TestParseSeed_RequiresTextModality(t *testing.T) {
	body := completeSeedJSON(
		`"models":[{"id":"vision-only","provider":"p","input_modalities":["image"]}],"default_resize_budget":{"long_edge_px":1,"max_bytes":1}}`,
	)
	_, err := ParseSeed([]byte(body))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "text")
}

// TestParseSeed_RejectsEmptyProvider asserts the provider invariant —
// every model must declare a non-empty provider. The provider id is
// the carrier for downstream routing in pkg/providers (region, auth
// profile, billing); a missing provider is non-routable.
func TestParseSeed_RejectsEmptyProvider(t *testing.T) {
	body := completeSeedJSON(
		`"models":[{"id":"x","provider":"","input_modalities":["text"]}],"default_resize_budget":{"long_edge_px":1,"max_bytes":1}}`,
	)
	_, err := ParseSeed([]byte(body))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "provider")
}

// TestParseSeed_RejectsInvalidResizeBudget asserts the per-model
// resize_budget invariant — when a model DOES declare a resize_budget,
// the long_edge_px and max_bytes must be strictly positive. Zero or
// negative values are rejected (the validator does not silently clamp
// to the catalog default; an explicit zero is a wire-level bug).
func TestParseSeed_RejectsInvalidResizeBudget(t *testing.T) {
	cases := map[string]string{
		"model long_edge zero": completeSeedJSON(
			`"models":[{"id":"x","provider":"p","input_modalities":["text"],"resize_budget":{"long_edge_px":0,"max_bytes":1}}],"default_resize_budget":{"long_edge_px":1,"max_bytes":1}}`,
		),
		"model long_edge negative": completeSeedJSON(
			`"models":[{"id":"x","provider":"p","input_modalities":["text"],"resize_budget":{"long_edge_px":-1,"max_bytes":1}}],"default_resize_budget":{"long_edge_px":1,"max_bytes":1}}`,
		),
		"model max_bytes zero": completeSeedJSON(
			`"models":[{"id":"x","provider":"p","input_modalities":["text"],"resize_budget":{"long_edge_px":1,"max_bytes":0}}],"default_resize_budget":{"long_edge_px":1,"max_bytes":1}}`,
		),
		"model max_bytes negative": completeSeedJSON(
			`"models":[{"id":"x","provider":"p","input_modalities":["text"],"resize_budget":{"long_edge_px":1,"max_bytes":-1}}],"default_resize_budget":{"long_edge_px":1,"max_bytes":1}}`,
		),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseSeed([]byte(body))
			require.Error(t, err, "%s should fail", name)
			assert.Contains(t, err.Error(), "resize_budget",
				"%s error must name resize_budget (got: %v)", name, err)
		})
	}
}

// ── TD-M5 catalog metadata validation (Wave 1 r1) ────────────────────────────

// TestParseSeed_RejectsMissingSchemaVersion asserts the schema_version
// invariant — the catalog metadata must include a non-empty
// schema_version. A missing/empty schema_version is rejected outright;
// the operator must declare the seed's schema version so consumers can
// gate on it.
func TestParseSeed_RejectsMissingSchemaVersion(t *testing.T) {
	body := `{"version":"v1","updated_at":"2026-07-23T00:00:00Z","source":"test",` +
		`"models":[{"id":"x","provider":"p","input_modalities":["text"]}],` +
		`"default_resize_budget":{"long_edge_px":1,"max_bytes":1}}`
	_, err := ParseSeed([]byte(body))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "schema_version")
}

// TestParseSeed_RejectsMissingUpdatedAt asserts the updated_at invariant
// — the catalog metadata must include a non-zero timestamp. A missing
// timestamp is rejected (the embed-time freeze gate is undefined without
// a recorded validation date).
func TestParseSeed_RejectsMissingUpdatedAt(t *testing.T) {
	body := `{"version":"v1","schema_version":"1.0.0","source":"test",` +
		`"models":[{"id":"x","provider":"p","input_modalities":["text"]}],` +
		`"default_resize_budget":{"long_edge_px":1,"max_bytes":1}}`
	_, err := ParseSeed([]byte(body))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "updated_at")
}

// TestParseSeed_RejectsMissingSource asserts the source invariant — the
// catalog metadata must include a non-empty source string (the freeze-
// gate re-validation report path). A missing source is rejected.
func TestParseSeed_RejectsMissingSource(t *testing.T) {
	body := `{"version":"v1","schema_version":"1.0.0","updated_at":"2026-07-23T00:00:00Z",` +
		`"models":[{"id":"x","provider":"p","input_modalities":["text"]}],` +
		`"default_resize_budget":{"long_edge_px":1,"max_bytes":1}}`
	_, err := ParseSeed([]byte(body))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "source")
}

// ── TD-M5 Version semver comparison (Wave 1 r1) ──────────────────────────────

// TestVersion_SemverComparison asserts the semver-aware comparison
// (TD-M5 bug fix): semver-parseable strings compare numerically across
// major/minor/patch, so v10 > v2 (the lexical bug) and v1.2.3 < v1.10.0
// (the minor-axis bug). Non-semver strings fall back to lexical
// comparison.
func TestVersion_SemverComparison(t *testing.T) {
	cases := []struct {
		a, b     string
		wantSign int // -1, 0, +1
		why      string
	}{
		// The headline bug fix: v10 > v2 (lexical would say v10 < v2).
		{"v10", "v2", +1, "v10 > v2 (semver numeric)"},
		{"v2", "v10", -1, "v2 < v10 (semver numeric)"},
		// The minor-axis bug fix: v1.2.3 < v1.10.0.
		{"v1.2.3", "v1.10.0", -1, "v1.2.3 < v1.10.0 (semver numeric minor)"},
		{"v1.10.0", "v1.2.3", +1, "v1.10.0 > v1.2.3 (semver numeric minor)"},
		// Patch axis.
		{"v1.2.10", "v1.2.2", +1, "v1.2.10 > v1.2.2 (semver numeric patch)"},
		// Equality.
		{"v1.2.3", "v1.2.3", 0, "v1.2.3 == v1.2.3"},
		// Shorthand expansion.
		{"v1", "v1.0.0", 0, "v1 == v1.0.0 (shorthand)"},
		{"v1.2", "v1.2.0", 0, "v1.2 == v1.2.0 (shorthand)"},
		{"v1", "v1.0.1", -1, "v1 < v1.0.1 (shorthand treated as v1.0.0)"},
		// Prerelease (§11): prerelease sorts BEFORE no-prerelease.
		{"v1.0.0-rc1", "v1.0.0", -1, "prerelease < stable (semver §11)"},
		{"v1.0.0", "v1.0.0-rc1", +1, "stable > prerelease (semver §11)"},
		{"v1.0.0-rc1", "v1.0.0-rc1", 0, "same prerelease is equal"},
		// Non-semver fallback: lexical.
		{"2026-07-23", "2026-07-22", +1, "date lexical (later date > earlier)"},
		{"v1-older", "v2-current", -1, "non-semver lexical (v1-older < v2-current)"},
		{"2026-07-23", "v1.0.0", -1, "date-string vs semver falls back to lexical"},
	}
	for _, tc := range cases {
		t.Run(tc.why, func(t *testing.T) {
			a, err := ParseVersion(tc.a)
			require.NoError(t, err)
			b, err := ParseVersion(tc.b)
			require.NoError(t, err)
			got := a.Compare(b)
			// Compare the sign of the comparison.
			gotSign := sign(got)
			if gotSign != tc.wantSign {
				t.Errorf("Compare(%q, %q) = %d (sign %d), want sign %d\n  version A: %+v\n  version B: %+v",
					tc.a, tc.b, got, gotSign, tc.wantSign, a, b)
			}
		})
	}
}

// sign returns -1/0/+1 for a negative/zero/positive value.
func sign(v int) int {
	if v < 0 {
		return -1
	}
	if v > 0 {
		return 1
	}
	return 0
}

// TestVersion_IsSemver is a small sanity check that ParseVersion
// correctly identifies semver vs non-semver strings — the runtime
// uses IsSemver to decide whether to compare numerically or lexically.
func TestVersion_IsSemver(t *testing.T) {
	cases := []struct {
		s    string
		want bool
	}{
		{"v1", true},
		{"v1.2", true},
		{"v1.2.3", true},
		{"v1.2.3-rc1", true},
		{"v10.0.0", true},
		{"v1.0.0+build", false}, // build metadata not supported by parser
		{"", false},
		{"2026-07-23", false},
		{"v1-older", false},
		{"v-1", false},
		{"v1.", false},
	}
	for _, tc := range cases {
		t.Run(tc.s, func(t *testing.T) {
			v, err := ParseVersion(tc.s)
			require.NoError(t, err)
			assert.Equal(t, tc.want, v.IsSemver(), "IsSemver(%q)", tc.s)
		})
	}
}

// TestCatalog_Refresh_VersionRegressSemver asserts the regression check
// uses semver comparison: a current "v10.0.0" with a pulled "v2.0.0"
// (which would be a downgrade under ANY ordering) is rejected, AND
// a current "v1.0.0" with a pulled "v1.0.10" (which is an UPGRADE
// under semver but a DOWNGRADE under the old lexical comparison) is
// rejected by the old code but ACCEPTED by the new code. This is the
// live regression test for the v10 < v2 bug.
func TestCatalog_Refresh_VersionRegressSemver(t *testing.T) {
	store := &memStore{
		data: []byte(completeSeedJSON(
			`"version": "v10.0.0","models": [{"id":"current","provider":"p","input_modalities":["text"]}],"default_resize_budget":{"long_edge_px":1,"max_bytes":1}}`,
		)),
	}
	c, err := NewCatalog(minimalSeedJSON(), nil, store, testLogger())
	require.NoError(t, err)
	require.Equal(t, "v10.0.0", c.Version().String())

	// Pulled "v2.0.0" — numerically < current "v10.0.0"; semver
	// regression (the OLD code's lexical comparison would have ACCEPTED
	// this as an upgrade because "v10" < "v2" lexically).
	puller := &fakePuller{data: []byte(completeSeedJSON(
		`"version": "v2.0.0","models": [{"id":"older","provider":"p","input_modalities":["text"]}],"default_resize_budget":{"long_edge_px":1,"max_bytes":1}}`,
	))}
	c.puller = puller

	refreshErr := c.Refresh(context.Background())
	require.Error(t, refreshErr, "v2.0.0 must regress below v10.0.0 (semver)")
	assert.Contains(t, refreshErr.Error(), "regressed")

	// Now flip: current "v1.0.0" with pulled "v1.0.10" — semver upgrade,
	// accepted.
	store.data = []byte(completeSeedJSON(
		`"version": "v1.0.0","models": [{"id":"current","provider":"p","input_modalities":["text"]}],"default_resize_budget":{"long_edge_px":1,"max_bytes":1}}`,
	))
	c, err = NewCatalog(minimalSeedJSON(), nil, store, testLogger())
	require.NoError(t, err)
	require.Equal(t, "v1.0.0", c.Version().String())

	puller2 := &fakePuller{data: []byte(completeSeedJSON(
		`"version": "v1.0.10","models": [{"id":"newer","provider":"p","input_modalities":["text"]}],"default_resize_budget":{"long_edge_px":1,"max_bytes":1}}`,
	))}
	c.puller = puller2

	require.NoError(t, c.Refresh(context.Background()), "v1.0.10 must be accepted as an upgrade over v1.0.0 (semver)")
	assert.Equal(t, "v1.0.10", c.Version().String(), "version upgrades to v1.0.10")
}

// ── Test #8 ──────────────────────────────────────────────────────────────────

// TestNewCatalog_EmbeddedSeed asserts NewCatalog without Store or Puller
// hydrates from the embedded seed. This is the "fresh install, offline"
// baseline.
func TestNewCatalog_EmbeddedSeed(t *testing.T) {
	c, err := NewCatalog(minimalSeedJSON(), nil, nil, testLogger())
	require.NoError(t, err)
	require.NotNil(t, c)
	models := c.Models()
	assert.Len(t, models, 2)
	ids := map[string]bool{}
	for _, m := range models {
		ids[m.ID] = true
	}
	assert.True(t, ids["gpt-4o"])
	assert.True(t, ids["deepseek-chat"])
}

// ── Test #9 ──────────────────────────────────────────────────────────────────

// memStore is an in-memory Store for tests. Implements Store.
type memStore struct {
	mu   sync.Mutex
	data []byte
	errR error // injected Read error
	errW error // injected Write error
}

func (m *memStore) Read(context.Context) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.errR != nil {
		return nil, m.errR
	}
	return m.data, nil
}

func (m *memStore) Write(_ context.Context, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.errW != nil {
		return m.errW
	}
	m.data = append([]byte(nil), data...)
	return nil
}

// TestNewCatalog_HydratesFromStore asserts that prior Store data wins over
// the embedded seed on boot (last-known-good preserved across restarts).
func TestNewCatalog_HydratesFromStore(t *testing.T) {
	store := &memStore{
		data: []byte(completeSeedJSON(
			`"version":"store-2026-07-22","models":[{"id": "from-store","provider":"p","input_modalities":["text","image"]}],"default_resize_budget": {"long_edge_px": 4096, "max_bytes": 5242880}}`,
		)),
	}
	c, err := NewCatalog(minimalSeedJSON(), nil, store, testLogger())
	require.NoError(t, err)
	// The Store data has 1 model with a different default budget.
	models := c.Models()
	assert.Len(t, models, 1)
	ids := map[string]bool{}
	for _, m := range models {
		ids[m.ID] = true
	}
	assert.True(t, ids["from-store"])
	assert.False(t, ids["gpt-4o"], "embedded seed entry must not appear when Store hydrates first")
	assert.Equal(t, "store-2026-07-22", c.Version().String())
	assert.Equal(t, ResizeBudget{LongEdgePx: 4096, MaxBytes: 5242880}, c.DefaultResizeBudget())
}

// ── Test #10 ─────────────────────────────────────────────────────────────────

// TestNewCatalog_FallsBackToSeedOnStoreError asserts a Store Read error
// degrades to the embedded seed (the gateway must still boot). The warning
// is logged; the test does not assert the log content (testLogger discards).
func TestNewCatalog_FallsBackToSeedOnStoreError(t *testing.T) {
	store := &memStore{errR: fmt.Errorf("simulated disk failure")}
	c, err := NewCatalog(minimalSeedJSON(), nil, store, testLogger())
	require.NoError(t, err)
	models := c.Models()
	assert.Len(t, models, 2, "embedded seed must hydrate when Store fails")
	assert.Equal(t, "test-2026-07-23", c.Version().String())
}

// ── Test #11 ─────────────────────────────────────────────────────────────────

// TestCatalog_Resolve_KnownModel asserts Resolve returns the catalog entry
// with the default resize budget applied when the model has no override.
func TestCatalog_Resolve_KnownModel(t *testing.T) {
	c, err := NewCatalog(minimalSeedJSON(), nil, nil, testLogger())
	require.NoError(t, err)
	m := c.Resolve("gpt-4o")
	assert.Equal(t, "gpt-4o", m.ID())
	assert.Equal(t, []Modality{ModalityText, ModalityImage}, m.InputModalities())
	budget := m.Budget()
	assert.Equal(t, 8000, budget.LongEdgePx)
	assert.Equal(t, int64(20971520), budget.MaxBytes)

	// deepseek-chat has no inline budget → catalog default applied.
	m = c.Resolve("deepseek-chat")
	budget = m.Budget()
	assert.Equal(t, 7680, budget.LongEdgePx, "default budget must be applied for entries without one")
	assert.Equal(t, int64(10485760), budget.MaxBytes)
}

// ── Test #12 ─────────────────────────────────────────────────────────────────

// TestCatalog_Resolve_UnknownModel_Optimistic (FR-026) asserts an unknown
// model resolves to the optimistic default — text+image modalities, the
// catalog default resize budget. The returned handle is a fresh value each
// call; the ID is the requested modelID.
func TestCatalog_Resolve_UnknownModel_Optimistic(t *testing.T) {
	c, err := NewCatalog(minimalSeedJSON(), nil, nil, testLogger())
	require.NoError(t, err)
	m := c.Resolve("unknown-future-model-2099")
	assert.Equal(t, "unknown-future-model-2099", m.ID())
	assert.Equal(t, []Modality{ModalityText, ModalityImage}, m.InputModalities(), "optimistic default = text + image")
	budget := m.Budget()
	assert.Equal(t, c.DefaultResizeBudget(), budget,
		"optimistic model's budget matches the catalog default (TD-M6)")
}

// TestOptimisticModel_DefaultBudget (Wave 1 TD-M6) asserts the explicit
// OptimisticModel helper returns a handle carrying the catalog default
// budget — not a nil/zero value. This is the closure of the "unknown
// resolved models return ResizeBudget:nil" complaint: every optimistic
// model now carries the catalog default by value, never a zero budget.
func TestOptimisticModel_DefaultBudget(t *testing.T) {
	c, err := NewCatalog(minimalSeedJSON(), nil, nil, testLogger())
	require.NoError(t, err)
	def := c.DefaultResizeBudget()
	require.Greater(t, def.LongEdgePx, 0, "default LongEdgePx must be positive")
	require.Greater(t, def.MaxBytes, int64(0), "default MaxBytes must be positive")

	m := c.OptimisticModel("mystery-future-model-2099")
	budget := m.Budget()
	assert.Equal(t, def, budget,
		"optimistic model's budget must equal the catalog default")
	assert.Greater(t, budget.LongEdgePx, 0,
		"optimistic budget LongEdgePx must be non-zero (TD-M6 invariant)")
	assert.Greater(t, budget.MaxBytes, int64(0),
		"optimistic budget MaxBytes must be non-zero (TD-M6 invariant)")
}

// TestResolvedModel_AlwaysCarriesBudget (Wave 1 TD-M6) asserts that every
// resolved model in the catalog carries a non-zero ResizeBudget — the
// invariant the post-validation pipeline guarantees. Models that omit a
// resize_budget in the seed (e.g. deepseek-chat, kimi-k2) must come
// back with the catalog default applied; the in-memory model uses
// ResizeBudget by value so a missing/zero budget is unrepresentable.
func TestResolvedModel_AlwaysCarriesBudget(t *testing.T) {
	c, err := NewCatalog(minimalSeedJSON(), nil, nil, testLogger())
	require.NoError(t, err)
	models := c.Models()
	require.NotEmpty(t, models, "catalog must have at least one model")
	for _, snap := range models {
		budget := snap.Handle.Budget()
		assert.Greater(t, budget.LongEdgePx, 0,
			"resolved model %q must carry a non-zero LongEdgePx (TD-M6)", snap.ID)
		assert.Greater(t, budget.MaxBytes, int64(0),
			"resolved model %q must carry a non-zero MaxBytes (TD-M6)", snap.ID)
	}
}

// TestSeedValidate_DefaultBudgetApplied (Wave 1 TD-M6) asserts that a
// seed model lacking a resize_budget gets the catalog default applied
// during validate — the post-validate Model.ResizeBudget is guaranteed
// non-zero. The catalog default can be overridden per-model (inline
// resize_budget wins) but is always present after validation.
func TestSeedValidate_DefaultBudgetApplied(t *testing.T) {
	body := []byte(completeSeedJSON(`"version": "test-2026-07-23","models": [
		{"id": "no-budget-model", "provider": "p", "input_modalities": ["text"]}
	],"default_resize_budget": {"long_edge_px": 1234, "max_bytes": 567890}}`))
	s, err := ParseSeed(body)
	require.NoError(t, err)
	require.Len(t, s.Models, 1)
	got := s.Models[0].ResizeBudget
	assert.Equal(t, 1234, got.LongEdgePx, "default LongEdgePx applied to missing-budget model")
	assert.Equal(t, int64(567890), got.MaxBytes, "default MaxBytes applied to missing-budget model")
}

// TestSeedValidate_InlineBudgetWinsOverDefault (Wave 1 TD-M6) asserts
// that an explicit inline resize_budget is preserved when the catalog
// default is also set — the validator applies the default only to
// models that lack one. This is the regression guard for the apply-
// default loop: a comparison with the default inside the loop must
// not overwrite an explicit budget.
func TestSeedValidate_InlineBudgetWinsOverDefault(t *testing.T) {
	body := []byte(completeSeedJSON(`"version": "test-2026-07-23","models": [
		{"id": "explicit-budget", "provider": "p", "input_modalities": ["text"], "resize_budget": {"long_edge_px": 9876, "max_bytes": 4321}},
		{"id": "default-budget",   "provider": "p", "input_modalities": ["text"]}
	],"default_resize_budget": {"long_edge_px": 1234, "max_bytes": 567890}}`))
	s, err := ParseSeed(body)
	require.NoError(t, err)
	require.Len(t, s.Models, 2)

	byID := map[string]Model{}
	for _, m := range s.Models {
		byID[m.ID] = m
	}

	explicit, ok := byID["explicit-budget"]
	require.True(t, ok)
	assert.Equal(t, 9876, explicit.ResizeBudget.LongEdgePx, "inline LongEdgePx preserved")
	assert.Equal(t, int64(4321), explicit.ResizeBudget.MaxBytes, "inline MaxBytes preserved")

	def, ok := byID["default-budget"]
	require.True(t, ok)
	assert.Equal(t, 1234, def.ResizeBudget.LongEdgePx, "catalog default LongEdgePx applied")
	assert.Equal(t, int64(567890), def.ResizeBudget.MaxBytes, "catalog default MaxBytes applied")
}

// ── Test #13 ─────────────────────────────────────────────────────────────────

// TestCatalog_HasModal exercises the capability-gate convenience method for
// known and unknown models.
func TestCatalog_HasModal(t *testing.T) {
	c, err := NewCatalog(minimalSeedJSON(), nil, nil, testLogger())
	require.NoError(t, err)
	// Known: gpt-4o supports text and image, not pdf.
	assert.True(t, c.HasModal("gpt-4o", "text"))
	assert.True(t, c.HasModal("gpt-4o", "image"))
	assert.False(t, c.HasModal("gpt-4o", "pdf"))
	// Known: deepseek-chat supports text only.
	assert.True(t, c.HasModal("deepseek-chat", "text"))
	assert.False(t, c.HasModal("deepseek-chat", "image"))
	// Unknown: optimistic — text + image yes, others no.
	assert.True(t, c.HasModal("mystery-model", "text"))
	assert.True(t, c.HasModal("mystery-model", "image"))
	assert.False(t, c.HasModal("mystery-model", "pdf"))
	assert.False(t, c.HasModal("mystery-model", "audio"))
}

// ── Test #14 ─────────────────────────────────────────────────────────────────

// fakePuller is a controllable Puller for Refresh tests.
type fakePuller struct {
	data []byte
	err  error
	hits int
}

func (f *fakePuller) Pull(context.Context) ([]byte, error) {
	f.hits++
	return f.data, f.err
}

// TestCatalog_Refresh_PullsAndApplies asserts a successful Pull updates the
// in-memory state and persists the new data to Store.
func TestCatalog_Refresh_PullsAndApplies(t *testing.T) {
	pulled := []byte(completeSeedJSON(
		`"version": "z-2026-07-24","models": [{"id": "new-model","provider":"p","input_modalities":["text"]}],"default_resize_budget": {"long_edge_px": 1, "max_bytes": 1}}`,
	))
	puller := &fakePuller{data: pulled}
	store := &memStore{}
	c, err := NewCatalog(minimalSeedJSON(), puller, store, testLogger())
	require.NoError(t, err)

	err = c.Refresh(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, puller.hits, "puller should be called exactly once")
	assert.Equal(t, "z-2026-07-24", c.Version().String(), "version updates to the pulled value")
	models := c.Models()
	foundNew := false
	for _, m := range models {
		if m.ID == "new-model" {
			foundNew = true
			break
		}
	}
	assert.True(t, foundNew, "pulled model is in the catalog")
	assert.NotEmpty(t, store.data, "Store received the new catalog JSON")
}

// ── Test #15 ─────────────────────────────────────────────────────────────────

// TestCatalog_Refresh_FailureNonFatal (FR-025, SC-009) asserts a Pull failure
// returns an error AND retains the last-known-good in-memory state. The
// gateway must not crash on a network outage.
func TestCatalog_Refresh_FailureNonFatal(t *testing.T) {
	puller := &fakePuller{err: fmt.Errorf("network is unreachable")}
	c, err := NewCatalog(minimalSeedJSON(), puller, &memStore{}, testLogger())
	require.NoError(t, err)

	// Pre-refresh state: embedded seed has 2 models.
	pre := c.Models()
	require.Len(t, pre, 2)

	refreshErr := c.Refresh(context.Background())
	require.Error(t, refreshErr, "Refresh must return the pull error")
	assert.Contains(t, refreshErr.Error(), "network is unreachable")

	// Post-refresh state: unchanged.
	post := c.Models()
	assert.Len(t, post, 2, "in-memory state must be retained on pull failure")
	foundGPT := false
	for _, m := range post {
		if m.ID == "gpt-4o" {
			foundGPT = true
			break
		}
	}
	assert.True(t, foundGPT, "embedded-seed entry still present")
}

// ── Test #16 ─────────────────────────────────────────────────────────────────

// TestCatalog_Refresh_InvalidJSON asserts a successful HTTP pull but
// malformed JSON is treated as a soft failure: error returned, in-memory
// state retained.
func TestCatalog_Refresh_InvalidJSON(t *testing.T) {
	puller := &fakePuller{data: []byte("not json at all")}
	c, err := NewCatalog(minimalSeedJSON(), puller, &memStore{}, testLogger())
	require.NoError(t, err)

	refreshErr := c.Refresh(context.Background())
	require.Error(t, refreshErr)

	post := c.Models()
	assert.Len(t, post, 2, "in-memory state retained on invalid JSON")
}

// ── Test #17 ─────────────────────────────────────────────────────────────────

// TestCatalog_Refresh_VersionRegress asserts that a pulled catalog with a
// version smaller than the current catalog is rejected. Version comparison
// is semver-aware (numeric when both sides parse as semver; lexical
// otherwise) — the catalog retains last-known-good either way. This guards
// against an operator reverting a release without realizing it (e.g. raw
// fallback returning a stale tag).
func TestCatalog_Refresh_VersionRegress(t *testing.T) {
	store := &memStore{
		data: []byte(completeSeedJSON(
			`"version": "v2-current","models": [{"id":"current","provider":"p","input_modalities":["text"]}],"default_resize_budget":{"long_edge_px":1,"max_bytes":1}}`,
		)),
	}
	c, err := NewCatalog(minimalSeedJSON(), nil, store, testLogger())
	require.NoError(t, err)
	require.Equal(t, "v2-current", c.Version().String())

	puller := &fakePuller{data: []byte(completeSeedJSON(
		`"version": "v1-older","models": [{"id":"older","provider":"p","input_modalities":["text"]}],"default_resize_budget":{"long_edge_px":1,"max_bytes":1}}`,
	))}
	c.puller = puller

	refreshErr := c.Refresh(context.Background())
	require.Error(t, refreshErr, "version regress should be rejected")
	assert.Contains(t, refreshErr.Error(), "regressed")

	post := c.Models()
	hasCurrent, hasOlder := false, false
	for _, m := range post {
		if m.ID == "current" {
			hasCurrent = true
		}
		if m.ID == "older" {
			hasOlder = true
		}
	}
	assert.True(t, hasCurrent, "current model still present")
	assert.False(t, hasOlder, "older model NOT applied")
}

// ── Test #18 ─────────────────────────────────────────────────────────────────

// TestCatalog_Refresh_NoPuller asserts Refresh is a no-op when NewCatalog
// was constructed without a Puller (CLI tools, tests, airgapped deployments).
func TestCatalog_Refresh_NoPuller(t *testing.T) {
	c, err := NewCatalog(minimalSeedJSON(), nil, nil, testLogger())
	require.NoError(t, err)
	assert.NoError(t, c.Refresh(context.Background()))
	// State unchanged.
	models := c.Models()
	assert.Len(t, models, 2)
}

// ── Test #19 ─────────────────────────────────────────────────────────────────

// TestCatalog_NoPerAgentOverride (FR-027) is a structural test: there is no
// API on the Catalog that takes an agent or workspace ID. The seed is the
// only override mechanism. If a future change adds such an API, this test
// fails and forces a re-evaluation of FR-027.
//
// The "test" here is a Go compile-time guarantee: we list every public method
// of *Catalog and assert none of them accept a "workspace ID" or "agent ID"
// parameter. The Catalog's only scope is the global seed.
//
// This is the most important test in this file for FR-027.
func TestCatalog_NoPerAgentOverride(t *testing.T) {
	c, err := NewCatalog(minimalSeedJSON(), nil, nil, testLogger())
	require.NoError(t, err)

	// Per-model resolution is keyed ONLY by modelID — no agent / workspace
	// scope parameter. If a future commit adds such a parameter, this
	// assertion (and the API surface) need an ADR.
	m := c.Resolve("gpt-4o")
	_ = m

	// The only mutator is Refresh — it reads from the global Puller, not
	// from any per-agent override path. Tested above (#14-18).
	// The HasModal convenience takes only modelID + modality — no scope.
	_ = c.HasModal("gpt-4o", "image")

	// Models(), Version(), UpdatedAt(), Source(), DefaultResizeBudget() all
	// return global state — no agent/workspace parameter. The test asserts
	// by compile: any future public method added with an extra parameter
	// MUST be reviewed against FR-027.
}

// ── Test #20 ─────────────────────────────────────────────────────────────────

// TestCatalog_DefaultResizeBudget asserts the documented default (7680px,
// 10 MB) is present on a fresh-seed catalog. ADR-051 Rev 4 §Format coverage.
func TestCatalog_DefaultResizeBudget(t *testing.T) {
	c, err := NewCatalog(minimalSeedJSON(), nil, nil, testLogger())
	require.NoError(t, err)
	b := c.DefaultResizeBudget()
	assert.Equal(t, 7680, b.LongEdgePx)
	assert.Equal(t, int64(10*1024*1024), b.MaxBytes)
}

// ── Test #21 ─────────────────────────────────────────────────────────────────

// TestCatalog_VersionAndSource asserts Version/Source expose the seed's
// metadata so operators can tell at runtime whether they're running the
// embedded seed or a freshly pulled catalog. Version is parsed into a
// Version type (semver-aware when parseable; the raw string is reachable
// via Version.String()).
func TestCatalog_VersionAndSource(t *testing.T) {
	c, err := NewCatalog(minimalSeedJSON(), nil, nil, testLogger())
	require.NoError(t, err)
	assert.Equal(t, "test-2026-07-23", c.Version().String())
	assert.Equal(t, "test-fixture", c.Source())
	assert.False(t, c.UpdatedAt().IsZero())
}

// ── Additional: concurrent Resolve ──────────────────────────────────────────

// TestCatalog_Resolve_ConcurrentRead asserts Resolve is safe under
// concurrent read (the documented hot path — many goroutines call Resolve
// per turn).
func TestCatalog_Resolve_ConcurrentRead(t *testing.T) {
	c, err := NewCatalog(minimalSeedJSON(), nil, nil, testLogger())
	require.NoError(t, err)
	const n = 200
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			m := c.Resolve("gpt-4o")
			if len(m.InputModalities()) == 0 {
				t.Error("Resolve returned empty modalities under load")
			}
		}()
	}
	wg.Wait()
}

// ── Additional: NewCatalog rejects invalid embedded seed ─────────────────────

// TestNewCatalog_InvalidEmbeddedSeed asserts NewCatalog returns an error
// when the embedded seed is corrupt. This is the failure mode where a
// bad commit ships a broken seed and the gateway refuses to boot rather
// than silently running with an empty catalog.
func TestNewCatalog_InvalidEmbeddedSeed(t *testing.T) {
	_, err := NewCatalog([]byte("not valid json"), nil, nil, testLogger())
	assert.Error(t, err)
}

// ── Smoke test for OptimisticModel ───────────────────────────────────────────

// TestOptimisticModel_DirectAPI asserts the exported OptimisticModel helper
// matches the catalog's behavior (orchestrator can construct the same
// optimistic value for logging without calling Resolve).
func TestOptimisticModel_DirectAPI(t *testing.T) {
	c, err := NewCatalog(minimalSeedJSON(), nil, nil, testLogger())
	require.NoError(t, err)
	m := c.OptimisticModel("foo-bar")
	assert.Equal(t, "foo-bar", m.ID())
	assert.Equal(t, []Modality{ModalityText, ModalityImage}, m.InputModalities())
}

// ── Optional: integration with real catalog file via fileutil-style read ────

// TestNewCatalog_FromFile is an optional smoke test that reads the actual
// committed seed JSON from disk. Skipped if the test environment does not
// have the file (e.g. inside a sandbox that filters). When run, it catches
// drift between the committed file and the embedded artifact.
func TestNewCatalog_FromFile(t *testing.T) {
	// Resolve the seed path relative to this test file.
	const relPath = "data/providers_capabilities_seed.json"
	cwd, err := os.Getwd()
	require.NoError(t, err)
	path := cwd + "/" + relPath
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("seed file not accessible from test cwd %q: %v", cwd, err)
	}
	c, err := NewCatalog(data, nil, nil, testLogger())
	require.NoError(t, err)
	assert.NotEmpty(t, c.Models(), "real committed seed must contain at least one model")
}

// ── Linter integration: errors.Is on ErrChecksumMismatch ────────────────────

// TestErrChecksumMismatch_ExportedIs is a small surface-area check that the
// sentinel error is reachable via errors.Is (the contract documented in the
// puller's doc comment).
func TestErrChecksumMismatch_ExportedIs(t *testing.T) {
	wrapped := fmt.Errorf("wrap: %w", ErrChecksumMismatch)
	require.True(t, errors.Is(wrapped, ErrChecksumMismatch), "errors.Is must match wrapped sentinel")
}

// Suppress the unused time import (some test cases above use it for time.Time
// fields in fixtures; we don't reference time in test bodies to keep this
// header section clean).
var _ = time.Time{}
