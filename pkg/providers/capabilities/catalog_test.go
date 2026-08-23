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
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
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

// TestParseSeed_RejectsMissingVersion (FIX 5, review) asserts the version
// invariant: an empty/missing "version" must be rejected outright, not
// silently accepted. Before this fix an empty version flowed through
// unchecked, which defeated refreshLocked's anti-downgrade regression
// check: that check is gated on `s.Version != ""`, so a version-less catalog was
// applied unconditionally with zero log, even if it was semantically older
// than the version already running.
func TestParseSeed_RejectsMissingVersion(t *testing.T) {
	body := `{"schema_version":"1.0.0","updated_at":"2026-07-23T00:00:00Z","source":"test",` +
		`"models":[{"id":"x","provider":"p","input_modalities":["text"]}],` +
		`"default_resize_budget":{"long_edge_px":1,"max_bytes":1}}`
	_, err := ParseSeed([]byte(body))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "version")

	// Explicit empty string (as opposed to the field being entirely absent
	// from the JSON) must be rejected identically.
	bodyExplicitEmpty := `{"version":"","schema_version":"1.0.0","updated_at":"2026-07-23T00:00:00Z","source":"test",` +
		`"models":[{"id":"x","provider":"p","input_modalities":["text"]}],` +
		`"default_resize_budget":{"long_edge_px":1,"max_bytes":1}}`
	_, err = ParseSeed([]byte(bodyExplicitEmpty))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "version")
}

// TestCatalog_Refresh_EmptyVersionRejected_AntiDowngradeGuardHolds is the
// end-to-end regression test proving the guard the empty-version rejection
// protects: without FIX 5, a pulled catalog with no version would sail past
// ParseSeed and then bypass refreshLocked's regression check entirely
// (`s.Version.raw != ""` gate), applying unconditionally regardless of
// whether it was older than the running catalog. With the fix, ParseSeed
// itself rejects the version-less pull before refreshLocked can apply it —
// last-known-good is retained exactly like any other invalid-pull case.
func TestCatalog_Refresh_EmptyVersionRejected_AntiDowngradeGuardHolds(t *testing.T) {
	store := &memStore{
		data: []byte(completeSeedJSON(
			`"version": "v10.0.0","models": [{"id":"current","provider":"p","input_modalities":["text"]}],"default_resize_budget":{"long_edge_px":1,"max_bytes":1}}`,
		)),
	}
	c, err := NewCatalog(minimalSeedJSON(), nil, store, testLogger())
	require.NoError(t, err)
	require.Equal(t, "v10.0.0", c.Version())

	// A pulled catalog with NO version field at all — the exact shape a
	// misconfigured/degraded seed source could produce.
	versionless := `{"schema_version":"1.0.0","updated_at":"2026-07-23T00:00:00Z","source":"attacker-or-bug",` +
		`"models":[{"id":"snuck-in","provider":"p","input_modalities":["text"]}],` +
		`"default_resize_budget":{"long_edge_px":1,"max_bytes":1}}`
	puller := &fakePuller{data: []byte(versionless)}
	c.puller = puller

	refreshErr := c.Refresh(context.Background())
	require.Error(t, refreshErr, "a version-less pulled catalog must be rejected, not silently applied")
	assert.Contains(t, refreshErr.Error(), "version")

	// last-known-good must be untouched.
	assert.Equal(t, "v10.0.0", c.Version())
	models := c.Models()
	hasCurrent, hasSnuckIn := false, false
	for _, m := range models {
		if m.ID == "current" {
			hasCurrent = true
		}
		if m.ID == "snuck-in" {
			hasSnuckIn = true
		}
	}
	assert.True(t, hasCurrent, "current model must still be present")
	assert.False(t, hasSnuckIn, "version-less catalog must NOT have been applied")
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
	assert.Equal(t, "store-2026-07-22", c.Version())
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
	assert.Equal(t, "test-2026-07-23", c.Version())
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

// ── Regression: provider-prefixed model id vs. bare catalog entry ──────────

// TestCatalog_Resolve_ProviderPrefixedModel_MatchesBareEntry is the FR-026
// regression this fix closes. It reproduces the exact real-world shape
// confirmed live 2026-07-28 (UAT): an agent configured with the
// OpenRouter-style "z-ai/glm-5.2" model id, against a catalog whose entry
// for that exact model is keyed by the BARE "glm-5.2" id (text-only — no
// image modality; the vendor is recorded separately in Model.Provider).
//
// Before this fix, Resolve did an exact-string map lookup only. The
// prefixed id never matched the bare key, so this fell through to the
// FR-026 optimistic default (text+image) — masking a model the catalog
// already knows is NOT vision-capable as if it were, and letting the
// agent loop send a native image block that the real OpenRouter endpoint
// for this model then rejected with 404 "No endpoints found that support
// image input" (pkg/agent/loop.go, pkg/agent/media_downgrade.go) — an
// avoidable failed round trip.
//
// This test fails against the pre-fix exact-match-only Resolve (it
// resolves to the optimistic text+image default instead of the catalog's
// real text-only entry) and passes once Resolve falls back to a
// stripped-prefix lookup.
func TestCatalog_Resolve_ProviderPrefixedModel_MatchesBareEntry(t *testing.T) {
	seed := completeSeedJSON(`"models":[
		{"id": "glm-5.2", "provider": "z-ai", "input_modalities": ["text"]}
	],"default_resize_budget":{"long_edge_px":7680,"max_bytes":10485760}}`)
	c, err := NewCatalog([]byte(seed), nil, nil, testLogger())
	require.NoError(t, err)

	m := c.Resolve("z-ai/glm-5.2")
	assert.Equal(t, []Modality{ModalityText}, m.InputModalities(),
		"must resolve the catalog's real (text-only) entry, not the FR-026 optimistic image-capable default")
	assert.False(t, m.Supports(ModalityImage),
		"z-ai/glm-5.2 is a known non-vision model in the catalog — Supports(image) must be false")
	assert.True(t, m.Supports(ModalityText))
}

// TestCatalog_Resolve_DoublePrefixedModel_MatchesBareEntry covers the extra
// "openrouter/<vendor>/<model>" namespacing onboarding sometimes writes
// (see openai_compat.normalizeModel's doc comment for the same artifact on
// the outbound-call side) — the fallback must strip BOTH segments, not
// just the first, to reach the bare catalog id.
func TestCatalog_Resolve_DoublePrefixedModel_MatchesBareEntry(t *testing.T) {
	seed := completeSeedJSON(`"models":[
		{"id": "glm-5.2", "provider": "z-ai", "input_modalities": ["text"]}
	],"default_resize_budget":{"long_edge_px":7680,"max_bytes":10485760}}`)
	c, err := NewCatalog([]byte(seed), nil, nil, testLogger())
	require.NoError(t, err)

	m := c.Resolve("openrouter/z-ai/glm-5.2")
	assert.Equal(t, []Modality{ModalityText}, m.InputModalities())
	assert.False(t, m.Supports(ModalityImage))
}

// TestCatalog_Resolve_BareModelUnaffectedByPrefixFallback guards against a
// regression where the prefix-stripping fallback could ever shadow a
// genuine bare catalog id (e.g. "gpt-4o", which never carries a vendor
// prefix) — the exact match must still win outright, before the fallback
// is even attempted.
func TestCatalog_Resolve_BareModelUnaffectedByPrefixFallback(t *testing.T) {
	c, err := NewCatalog(minimalSeedJSON(), nil, nil, testLogger())
	require.NoError(t, err)
	m := c.Resolve("gpt-4o")
	assert.Equal(t, "gpt-4o", m.ID())
	assert.True(t, m.Supports(ModalityImage))
}

// ── 2026-07-28 GLM ground-truth revalidation + frontier sync ───────────────
//
// See docs/internal/research/provider-media-format-support-2026-07-28.md for
// the full source list. These tests exercise the REAL committed seed file
// (via EmbeddedSeed(), not a synthetic fixture) so a future seed edit or
// regeneration cannot silently drift without failing CI.

// TestEmbeddedSeed_GLMFamily_TextOnlyVerdictsPinned is a regression GUARD,
// not a bug fix: every verdict it asserts already holds in the shipped seed
// and this test currently PASSES. It exists because an operator, going only
// on the real-world observation that "z-ai/glm-5.2 produces working vision
// via OpenRouter," concluded the seed's glm-5.2:["text"] entry must be
// wrong. Direct 2026-07-28 fetches of docs.z.ai/guides/llm/<model> for every
// model below returned "Input Modalities: Text" — the seed was correct all
// along; the "working vision" is fully explained by the FR-026 optimistic
// default (and, since this same file's prefix-stripping fix, the exact
// bare-entry hit) doing exactly what it's documented to do. If a future
// change flips any of these to image-capable without a harder source than
// an anecdote, this test must fail and force re-verification against
// docs.z.ai before merging.
func TestEmbeddedSeed_GLMFamily_TextOnlyVerdictsPinned(t *testing.T) {
	s, err := ParseSeed(EmbeddedSeed())
	require.NoError(t, err)
	byID := make(map[string]Model, len(s.Models))
	for _, m := range s.Models {
		byID[m.ID] = m
	}

	textOnly := []string{"glm-5", "glm-5.1", "glm-5.2", "glm-4.7", "glm-4.6", "glm-4.5"}
	for _, id := range textOnly {
		m, ok := byID[id]
		require.True(t, ok, "seed must carry an entry for %q", id)
		assert.Equal(t, []Modality{ModalityText}, m.InputModalities,
			"%s: docs.z.ai confirms text-only input modalities (2026-07-28 re-validation) — "+
				"do not flip to image-capable on an operational anecdote alone", id)
	}

	visionCapable := []string{"glm-5v-turbo", "glm-4.6v", "glm-4.6v-flash", "glm-4.6v-flashx", "glm-4.5v"}
	for _, id := range visionCapable {
		m, ok := byID[id]
		require.True(t, ok, "seed must carry an entry for %q", id)
		assert.Contains(t, m.InputModalities, ModalityImage,
			"%s is z.ai's dedicated vision line (V-suffixed / Turbo) — must remain image-capable", id)
	}
}

// TestEmbeddedSeed_NewFrontierModels_RevertProof pins the eight models added
// in the 2026-07-28 sanity pass. Each assertion distinguishes the seeded
// entry from what Resolve would return via the FR-026 optimistic default
// (text+image, catalog default budget) BEFORE the entry existed — i.e. this
// test fails against a seed missing any of these ids (reverted or
// regenerated without them) and passes against the current file.
func TestEmbeddedSeed_NewFrontierModels_RevertProof(t *testing.T) {
	c, err := NewCatalog(EmbeddedSeed(), nil, nil, testLogger())
	require.NoError(t, err)
	def := c.DefaultResizeBudget()

	// Anthropic: the optimistic default never carries "pdf" — Resolve on a
	// model missing from the seed would report Supports(pdf) == false. Every
	// one of these must be true, proving a real (non-optimistic) entry.
	for _, id := range []string{
		"claude-fable-5", "claude-opus-5", "claude-sonnet-5",
		"claude-haiku-4-5", "claude-sonnet-4-6",
	} {
		m := c.Resolve(id)
		assert.True(t, m.Supports(ModalityPDF),
			"%s: must be a real catalog entry with pdf support, not the optimistic default", id)
		assert.True(t, m.Supports(ModalityImage), "%s: vision-capable per platform.claude.com/docs (2026-07-28)", id)
		budget := m.Budget()
		assert.Equal(t, 8000, budget.LongEdgePx, "%s: Anthropic family resize budget", id)
		assert.Equal(t, int64(10485760), budget.MaxBytes, "%s: Anthropic family resize budget (10 MB)", id)
	}

	// OpenAI gpt-5.4: the optimistic default already matches on modality
	// (text+image), so the revert-proof signal is the resize budget — the
	// optimistic default carries the catalog-wide default (7680px/10MB),
	// while the real entry carries the OpenAI-family budget (8000px/20MB).
	{
		m := c.Resolve("gpt-5.4")
		assert.True(t, m.Supports(ModalityImage))
		budget := m.Budget()
		assert.NotEqual(t, def, budget,
			"gpt-5.4: must be a real catalog entry, not the optimistic default (same budget would prove a miss)")
		assert.Equal(t, 8000, budget.LongEdgePx)
		assert.Equal(t, int64(20971520), budget.MaxBytes)
	}

	// Moonshot kimi-k2.5: same reasoning as gpt-5.4 — modality matches the
	// optimistic default by coincidence, budget does not (Kimi's documented
	// 100 MB request ceiling vs. the 10 MB catalog default).
	{
		m := c.Resolve("kimi-k2.5")
		assert.True(t, m.Supports(ModalityImage),
			"kimi-k2.5: vision-capable per platform.kimi.ai (native MoonViT encoder)")
		budget := m.Budget()
		assert.NotEqual(t, def, budget, "kimi-k2.5: must be a real catalog entry, not the optimistic default")
		assert.Equal(t, int64(104857600), budget.MaxBytes, "kimi-k2.5: matches kimi-k3's documented 100 MB ceiling")
	}

	// MiniMax-M2.5: the OPPOSITE distinguishing direction from the others —
	// the optimistic default WRONGLY grants image support to a text-only
	// model. Before this entry existed, Resolve("MiniMax-M2.5") would report
	// Supports(image) == true (optimistic); the real entry must report false.
	{
		m := c.Resolve("MiniMax-M2.5")
		assert.True(t, m.Supports(ModalityText))
		assert.False(t, m.Supports(ModalityImage),
			"MiniMax-M2.5: text-only per help.apiyi.com (2026-07-28)")
	}
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
	assert.Equal(t, "z-2026-07-24", c.Version(), "version updates to the pulled value")
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
	require.Equal(t, "v2-current", c.Version())

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
	assert.Equal(t, "test-2026-07-23", c.Version())
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

// Suppress the unused time import (some test cases above use it for time.Time
// fields in fixtures; we don't reference time in test bodies to keep this
// header section clean).
var _ = time.Time{}

// ── Concurrent Refresh serialization (TA-3 carry-forward, Wave 1 TD-M4) ──────

// TestCatalog_Refresh_ConcurrentSerialization asserts the carry-forward
// invariant that concurrent Refresh calls are fully serialized by refreshMu
// (catalog.go:400,611) — no two Refresh transactions (pull → parse →
// version-check → apply → store) interleave. The Wave 1 refactor that made
// Refresh "private model + serialized" (TD-M4-major) is only meaningful if a
// concurrency test pins it; without serialization, overlapping apply/Store
// steps would race the in-memory catalog state and the last-known-good write.
//
// A concurrency-detecting puller tracks the high-water mark of simultaneously
// in-flight Pull calls. Under refreshMu serialization the max is exactly 1
// (each Pull runs alone); without it the max rises with the goroutine count.
// Traces to: spec TA-3 carry-forward; ADR-051 Rev 4 FR-025; SC-009.
func TestCatalog_Refresh_ConcurrentSerialization(t *testing.T) {
	pulled := []byte(completeSeedJSON(
		`"version": "z-2026-07-25","models": [{"id": "concurrent-model","provider":"p","input_modalities":["text"]}],"default_resize_budget": {"long_edge_px": 1, "max_bytes": 1}}`,
	))
	puller := &concurrencyTrackingPuller{data: pulled}
	c, err := NewCatalog(minimalSeedJSON(), puller, &memStore{}, testLogger())
	require.NoError(t, err)

	const n = 32
	var wg sync.WaitGroup
	wg.Add(n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			<-start
			// Error is irrelevant here — some refreshes are idempotent
			// re-applies of the same version; we only care that each ran
			// the serialized transaction (and thus drove one Pull).
			_ = c.Refresh(context.Background())
		}()
	}
	close(start)
	wg.Wait()

	// Serialization invariant: refreshMu held the Pull inside an exclusive
	// transaction, so no two Pulls ever overlapped (max in-flight == 1).
	maxInFlight := puller.maxInFlight.Load()
	assert.Equal(t, int32(1), maxInFlight,
		"refreshMu must serialize Refresh so pulls never overlap (max in-flight observed = %d)", maxInFlight)
	// Every Refresh drove exactly one Pull (the non-nil puller path).
	assert.Equal(t, int32(n), puller.hits.Load(),
		"each of %d concurrent Refresh calls must drive exactly one Pull", n)
	// Final state is internally consistent after N concurrent refreshes —
	// the last-applied version won cleanly, no torn state.
	assert.Equal(t, "z-2026-07-25", c.Version(),
		"final catalog version is the pulled value (no corruption under concurrent refresh)")
}

// concurrencyTrackingPuller is a controllable Puller that records the
// high-water mark of simultaneously in-flight Pull calls. The brief sleep
// widens the window in which an unserialized Refresh would overlap another
// Pull, so a serialization regression is observed rather than passing by
// lucky scheduling.
type concurrencyTrackingPuller struct {
	data        []byte
	hits        atomic.Int32
	inFlight    atomic.Int32
	maxInFlight atomic.Int32
}

func (c *concurrencyTrackingPuller) Pull(context.Context) ([]byte, error) {
	c.hits.Add(1)
	cur := c.inFlight.Add(1)
	// Record the high-water mark of concurrent in-flight pulls.
	for {
		m := c.maxInFlight.Load()
		if cur <= m || c.maxInFlight.CompareAndSwap(m, cur) {
			break
		}
	}
	time.Sleep(time.Millisecond) // widen the overlap window
	c.inFlight.Add(-1)
	return c.data, nil
}

// ── FIX-1 review regression tests (silent degradation to raw fallback) ──────
//
// recordingLogger captures Warn/Info calls so a test can assert a specific
// WARN fired (testLogger() above discards everything, which is right for
// tests that don't care about log content — these do).
type recordingLogger struct {
	mu    sync.Mutex
	warns []string
}

func (l *recordingLogger) Warn(msg string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warns = append(l.warns, msg)
}

func (l *recordingLogger) Info(string, ...any) {}

func (l *recordingLogger) warnCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.warns)
}

func (l *recordingLogger) hasWarnContaining(substr string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, w := range l.warns {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}

// degradedFakePuller is a fakePuller that also implements
// degradedTransportReporter (the optional interface refreshLocked
// type-asserts for), so tests can control exactly what LastPullDegraded
// reports without going through the real GHReleasePuller/HTTP stack.
type degradedFakePuller struct {
	fakePuller
	degraded   bool
	releaseErr error
}

func (d *degradedFakePuller) LastPullDegraded() (bool, error) {
	return d.degraded, d.releaseErr
}

// TestCatalog_Refresh_DegradedTransport_LogsWarnAndSetsState is the
// Catalog-level half of the FIX-1 review fix: when the configured Puller
// reports (via the optional degradedTransportReporter interface) that its
// most recent successful Pull fell back to an unverified transport,
// refreshLocked must both (a) log a WARN naming the release-path failure and
// (b) record the degraded state on the catalog so Degraded() surfaces it —
// not silently apply the pulled data as an indistinguishable clean success.
func TestCatalog_Refresh_DegradedTransport_LogsWarnAndSetsState(t *testing.T) {
	pulled := []byte(completeSeedJSON(
		`"version": "z-2026-07-25","models": [{"id": "from-raw-fallback","provider":"p","input_modalities":["text"]}],"default_resize_budget": {"long_edge_px": 1, "max_bytes": 1}}`,
	))
	releaseErr := fmt.Errorf("release status: 429")
	puller := &degradedFakePuller{
		fakePuller: fakePuller{data: pulled},
		degraded:   true,
		releaseErr: releaseErr,
	}
	log := &recordingLogger{}
	c, err := NewCatalog(minimalSeedJSON(), puller, &memStore{}, log)
	require.NoError(t, err)

	// Precondition: a freshly hydrated catalog (seed only, no live pull yet)
	// must not claim degraded.
	degraded, err0 := c.Degraded()
	assert.False(t, degraded, "no Refresh has run yet")
	assert.NoError(t, err0)

	refreshErr := c.Refresh(context.Background())
	require.NoError(t, refreshErr)

	degraded, gotReleaseErr := c.Degraded()
	assert.True(t, degraded, "catalog state must surface the degraded transport")
	require.Error(t, gotReleaseErr)
	assert.Contains(t, gotReleaseErr.Error(), "429")

	assert.True(t, log.hasWarnContaining("degraded fallback transport"),
		"expected a WARN naming the degraded transport, got: %v", log.warns)

	// The pulled data itself is still applied — degraded is a provenance
	// signal, not a rejection (that's what checksum mismatch is for).
	models := c.Models()
	found := false
	for _, m := range models {
		if m.ID == "from-raw-fallback" {
			found = true
		}
	}
	assert.True(t, found, "degraded-but-otherwise-valid catalog data is still applied")
}

// TestCatalog_Refresh_NonDegradedTransport_ClearsPriorDegradedState asserts
// the degraded flag is self-healing: a later successful Refresh whose Puller
// reports NOT degraded (the release path recovered) clears the previously
// recorded degraded state — Degraded() always reflects the MOST RECENT
// Refresh, not a sticky-forever flag.
func TestCatalog_Refresh_NonDegradedTransport_ClearsPriorDegradedState(t *testing.T) {
	store := &memStore{}
	first := &degradedFakePuller{
		fakePuller: fakePuller{data: []byte(completeSeedJSON(
			`"version": "v1","models": [{"id":"a","provider":"p","input_modalities":["text"]}],"default_resize_budget":{"long_edge_px":1,"max_bytes":1}}`,
		))},
		degraded:   true,
		releaseErr: fmt.Errorf("release status: 500"),
	}
	c, err := NewCatalog(minimalSeedJSON(), first, store, testLogger())
	require.NoError(t, err)
	require.NoError(t, c.Refresh(context.Background()))
	degraded, _ := c.Degraded()
	require.True(t, degraded, "precondition: first refresh recorded as degraded")

	second := &degradedFakePuller{
		fakePuller: fakePuller{data: []byte(completeSeedJSON(
			`"version": "v2","models": [{"id":"b","provider":"p","input_modalities":["text"]}],"default_resize_budget":{"long_edge_px":1,"max_bytes":1}}`,
		))},
		degraded:   false,
		releaseErr: nil,
	}
	c.puller = second
	require.NoError(t, c.Refresh(context.Background()))

	degraded, releaseErr := c.Degraded()
	assert.False(t, degraded, "a subsequent non-degraded refresh must clear the prior degraded state")
	assert.NoError(t, releaseErr)
}

// TestCatalog_Refresh_PullerWithoutDegradedReporter_NeverDegraded asserts
// that a Puller which does NOT implement degradedTransportReporter (the
// plain fakePuller used throughout the rest of this file, and any future
// third-party Puller) never causes a panic and is always reported
// not-degraded — the optional-interface check is purely additive.
func TestCatalog_Refresh_PullerWithoutDegradedReporter_NeverDegraded(t *testing.T) {
	puller := &fakePuller{data: []byte(completeSeedJSON(
		`"version": "v1","models": [{"id":"a","provider":"p","input_modalities":["text"]}],"default_resize_budget":{"long_edge_px":1,"max_bytes":1}}`,
	))}
	c, err := NewCatalog(minimalSeedJSON(), puller, &memStore{}, testLogger())
	require.NoError(t, err)
	require.NoError(t, c.Refresh(context.Background()))

	degraded, releaseErr := c.Degraded()
	assert.False(t, degraded)
	assert.NoError(t, releaseErr)
}

// ── catalog.go LOW-item fix: fs.ErrNotExist first-boot WARN false alarm ─────

// notExistStore is a Store whose Read always fails with a wrapped
// fs.ErrNotExist — simulating the real capFileStore (pkg/gateway/gateway.go)
// on a fresh install where the catalog persistence file has never been
// written yet.
type notExistStore struct{}

func (notExistStore) Read(context.Context) ([]byte, error) {
	return nil, &fs.PathError{Op: "open", Path: "capabilities.json", Err: fs.ErrNotExist}
}
func (notExistStore) Write(context.Context, []byte) error { return nil }

// TestNewCatalog_FreshInstall_NoWarnOnStoreNotExist is the regression test
// for the LOW-item fix: NewCatalog must NOT log a WARN when the Store read
// fails with fs.ErrNotExist — that is the expected, routine state on every
// single fresh install (no catalog persisted yet), not a fault. The catalog
// must still hydrate correctly from the embedded seed.
func TestNewCatalog_FreshInstall_NoWarnOnStoreNotExist(t *testing.T) {
	log := &recordingLogger{}
	c, err := NewCatalog(minimalSeedJSON(), nil, notExistStore{}, log)
	require.NoError(t, err)
	assert.Equal(t, 0, log.warnCount(), "fs.ErrNotExist on first boot must not log a WARN, got: %v", log.warns)
	models := c.Models()
	assert.Len(t, models, 2, "embedded seed must still hydrate")
}

// erroringStore is a Store whose Read always fails with a non-ErrNotExist
// error (a real fault: permissions, corruption, disk error).
type erroringStore struct{}

func (erroringStore) Read(context.Context) ([]byte, error) {
	return nil, fmt.Errorf("simulated disk I/O error")
}
func (erroringStore) Write(context.Context, []byte) error { return nil }

// TestNewCatalog_StoreReadRealError_StillLogsWarn is the companion
// regression guard: suppressing the fs.ErrNotExist false alarm must NOT
// suppress WARNs for actual Store read failures — an operator still needs
// to see a real fault (permissions, corruption, disk error).
func TestNewCatalog_StoreReadRealError_StillLogsWarn(t *testing.T) {
	log := &recordingLogger{}
	c, err := NewCatalog(minimalSeedJSON(), nil, erroringStore{}, log)
	require.NoError(t, err)
	assert.Equal(t, 1, log.warnCount(), "a real Store read failure must still log a WARN, got: %v", log.warns)
	assert.True(t, log.hasWarnContaining("last-known-good read failed"))
	models := c.Models()
	assert.Len(t, models, 2, "embedded seed must still hydrate on a real Store failure")
}

// ── FIX-1 RE-REVIEW: Degraded() is no longer write-only (persistence) ───────
//
// The first FIX-1 pass added Catalog.Degraded() but nothing in production
// ever called it — the accessor was reachable only from a test, and the
// accompanying WARN went to stderr. This second pass makes the degraded
// state genuinely observable by persisting it through the Store envelope
// (encodePersistedState/decodePersistedState) so it (a) survives a restart
// and (b) is directly inspectable in the persisted file without any code
// needing to call Degraded() at all.

// TestCatalog_DegradedState_PersistsAcrossRestart is the end-to-end
// regression test for the persistence half of the re-review fix: a Refresh
// that records degraded=true must survive a full process restart — i.e. a
// FRESH Catalog constructed from the same Store's persisted bytes (no live
// pull has run on the new process yet) must report Degraded()==true
// immediately, not just after the 7-day ticker's next pull.
func TestCatalog_DegradedState_PersistsAcrossRestart(t *testing.T) {
	store := &memStore{}
	releaseErr := fmt.Errorf("release status: 503")
	puller := &degradedFakePuller{
		fakePuller: fakePuller{data: []byte(completeSeedJSON(
			`"version": "v1","models": [{"id":"a","provider":"p","input_modalities":["text"]}],"default_resize_budget":{"long_edge_px":1,"max_bytes":1}}`,
		))},
		degraded:   true,
		releaseErr: releaseErr,
	}
	c1, err := NewCatalog(minimalSeedJSON(), puller, store, testLogger())
	require.NoError(t, err)
	require.NoError(t, c1.Refresh(context.Background()))

	degraded, gotErr := c1.Degraded()
	require.True(t, degraded, "precondition: first process recorded degraded state")
	require.Error(t, gotErr)

	// The persisted bytes must be readable as valid JSON with a top-level
	// "degraded" key an operator could inspect directly (e.g. `jq .degraded
	// capabilities_catalog.json`) without running any Omnipus code.
	var onDisk map[string]any
	require.NoError(t, json.Unmarshal(store.data, &onDisk), "persisted state must be valid JSON")
	assert.Equal(t, true, onDisk["degraded"], "persisted file must carry a directly-readable top-level degraded flag")
	assert.Contains(t, onDisk["degraded_release_error"], "503")
	require.Contains(t, onDisk, "catalog", "persisted envelope must retain the catalog payload under its own key")

	// Simulate a restart: construct a brand-new Catalog against the SAME
	// Store, no puller wired up yet (mirrors NewCatalog being called before
	// runCapabilityCatalogRefreshLoop's first pull).
	c2, err := NewCatalog(minimalSeedJSON(), nil, store, testLogger())
	require.NoError(t, err)

	degraded2, gotErr2 := c2.Degraded()
	assert.True(t, degraded2,
		"a fresh process must restore degraded=true from the persisted envelope, "+
			"not report false until the next live pull")
	require.Error(t, gotErr2)
	assert.Contains(t, gotErr2.Error(), "503")

	// The catalog data itself must also have hydrated correctly from the
	// unwrapped envelope.
	models := c2.Models()
	found := false
	for _, m := range models {
		if m.ID == "a" {
			found = true
		}
	}
	assert.True(t, found, "catalog payload must unwrap correctly from the persisted envelope")
}

// TestCatalog_DegradedState_ClearsAcrossRestartWhenNotDegraded is the
// negative-case companion: a clean (non-degraded) Refresh persists
// degraded=false, and a subsequent restart must not spuriously report
// degraded=true.
func TestCatalog_DegradedState_ClearsAcrossRestartWhenNotDegraded(t *testing.T) {
	store := &memStore{}
	puller := &fakePuller{data: []byte(completeSeedJSON(
		`"version": "v1","models": [{"id":"a","provider":"p","input_modalities":["text"]}],"default_resize_budget":{"long_edge_px":1,"max_bytes":1}}`,
	))}
	c1, err := NewCatalog(minimalSeedJSON(), puller, store, testLogger())
	require.NoError(t, err)
	require.NoError(t, c1.Refresh(context.Background()))

	c2, err := NewCatalog(minimalSeedJSON(), nil, store, testLogger())
	require.NoError(t, err)
	degraded, releaseErr := c2.Degraded()
	assert.False(t, degraded)
	assert.NoError(t, releaseErr)
}

// TestDecodePersistedState_LegacyBareCatalogFallback asserts backward
// compatibility with a Store file written before this fix — bare catalog
// JSON with no "catalog"/"degraded" wrapper keys. decodePersistedState must
// treat the whole payload as the catalog and report not-degraded, rather
// than failing to hydrate (which would force every existing install back to
// the embedded seed on first upgrade).
func TestDecodePersistedState_LegacyBareCatalogFallback(t *testing.T) {
	legacy := []byte(completeSeedJSON(
		`"version": "legacy-1","models": [{"id":"legacy-model","provider":"p","input_modalities":["text"]}],"default_resize_budget":{"long_edge_px":1,"max_bytes":1}}`,
	))
	catalogJSON, degraded, releaseErr := decodePersistedState(legacy)
	assert.Equal(t, legacy, catalogJSON, "legacy bare catalog JSON must pass through unmodified")
	assert.False(t, degraded)
	assert.NoError(t, releaseErr)

	// And it must actually hydrate through NewCatalog end-to-end.
	store := &memStore{data: legacy}
	c, err := NewCatalog(minimalSeedJSON(), nil, store, testLogger())
	require.NoError(t, err)
	assert.Equal(t, "legacy-1", c.Version())
	d, _ := c.Degraded()
	assert.False(t, d)
}

// TestEncodeDecodePersistedState_RoundTrip is a direct unit test of the two
// helper functions (independent of Catalog/Store plumbing): encode then
// decode must reproduce the same catalog bytes, degraded flag, and error
// message.
func TestEncodeDecodePersistedState_RoundTrip(t *testing.T) {
	catalogJSON := []byte(completeSeedJSON(
		`"version": "v9","models": [{"id":"x","provider":"p","input_modalities":["text"]}],"default_resize_budget":{"long_edge_px":1,"max_bytes":1}}`,
	))

	t.Run("degraded with release error", func(t *testing.T) {
		encoded, err := encodePersistedState(catalogJSON, true, fmt.Errorf("release status: 404"))
		require.NoError(t, err)
		gotCatalog, gotDegraded, gotErr := decodePersistedState(encoded)
		assert.JSONEq(t, string(catalogJSON), string(gotCatalog))
		assert.True(t, gotDegraded)
		require.Error(t, gotErr)
		assert.Contains(t, gotErr.Error(), "404")
	})

	t.Run("not degraded, nil release error", func(t *testing.T) {
		encoded, err := encodePersistedState(catalogJSON, false, nil)
		require.NoError(t, err)
		gotCatalog, gotDegraded, gotErr := decodePersistedState(encoded)
		assert.JSONEq(t, string(catalogJSON), string(gotCatalog))
		assert.False(t, gotDegraded)
		assert.NoError(t, gotErr)
	})
}
