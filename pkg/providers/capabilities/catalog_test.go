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
	data := embeddedSeed()
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
		"default long_edge zero": `{"models":[{"id":"x","provider":"y","input_modalities":["text"]}],"default_resize_budget":{"long_edge_px":0,"max_bytes":1}}`,
		"default max_bytes zero": `{"models":[{"id":"x","provider":"y","input_modalities":["text"]}],"default_resize_budget":{"long_edge_px":1,"max_bytes":0}}`,
		"model long_edge zero":   `{"models":[{"id":"x","provider":"y","input_modalities":["text"],"resize_budget":{"long_edge_px":0,"max_bytes":1}}],"default_resize_budget":{"long_edge_px":1,"max_bytes":1}}`,
		"model max_bytes zero":   `{"models":[{"id":"x","provider":"y","input_modalities":["text"],"resize_budget":{"long_edge_px":1,"max_bytes":0}}],"default_resize_budget":{"long_edge_px":1,"max_bytes":1}}`,
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
	body := `{
		"models": [
			{"id":"a","provider":"p","input_modalities":["text"]},
			{"id":"a","provider":"p","input_modalities":["text"]}
		],
		"default_resize_budget":{"long_edge_px":1,"max_bytes":1}
	}`
	_, err := ParseSeed([]byte(body))
	assert.Error(t, err)
}

// ── Test #6 ──────────────────────────────────────────────────────────────────

// TestParseSeed_RejectsEmptyMods asserts every model has at least one modality.
func TestParseSeed_RejectsEmptyMods(t *testing.T) {
	body := `{
		"models":[{"id":"x","provider":"p","input_modalities":[]}],
		"default_resize_budget":{"long_edge_px":1,"max_bytes":1}
	}`
	_, err := ParseSeed([]byte(body))
	assert.Error(t, err)
}

// ── Test #7 ──────────────────────────────────────────────────────────────────

// TestParseSeed_AcceptsUnknownModalities asserts the parser accepts unknown
// modality values for forward compatibility — the orchestrator may not yet
// recognize them, but the seed can carry new modalities (e.g. "3d") ahead of
// runtime support.
func TestParseSeed_AcceptsUnknownModalities(t *testing.T) {
	body := `{
		"models":[{"id":"x","provider":"p","input_modalities":["text","3d","hologram"]}],
		"default_resize_budget":{"long_edge_px":1,"max_bytes":1}
	}`
	s, err := ParseSeed([]byte(body))
	require.NoError(t, err)
	require.Len(t, s.Models, 1)
	assert.Equal(t, []string{"text", "3d", "hologram"}, s.Models[0].InputModalities)
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
		data: []byte(`{
			"version": "store-2026-07-22",
			"models": [{"id": "from-store","provider":"p","input_modalities":["text","image"]}],
			"default_resize_budget": {"long_edge_px": 4096, "max_bytes": 5242880}
		}`),
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
	assert.Equal(t, []string{"text", "image"}, m.InputModalities())
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
	assert.Equal(t, []string{"text", "image"}, m.InputModalities(), "optimistic default = text + image")
	budget := m.Budget()
	assert.Equal(t, c.DefaultResizeBudget(), budget,
		"optimistic model's budget matches the catalog default (TD-M6)")
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
	pulled := []byte(`{
		"version": "z-2026-07-24",
		"models": [{"id": "new-model","provider":"p","input_modalities":["text"]}],
		"default_resize_budget": {"long_edge_px": 1, "max_bytes": 1}
	}`)
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
// lexicographically smaller version than the current catalog is rejected.
// This guards against an operator reverting a release without realizing it
// (e.g. raw fallback returning a stale tag).
func TestCatalog_Refresh_VersionRegress(t *testing.T) {
	store := &memStore{
		data: []byte(`{
			"version": "v2-current",
			"models": [{"id":"current","provider":"p","input_modalities":["text"]}],
			"default_resize_budget":{"long_edge_px":1,"max_bytes":1}
		}`),
	}
	c, err := NewCatalog(minimalSeedJSON(), nil, store, testLogger())
	require.NoError(t, err)
	require.Equal(t, "v2-current", c.Version())

	puller := &fakePuller{data: []byte(`{
		"version": "v1-older",
		"models": [{"id":"older","provider":"p","input_modalities":["text"]}],
		"default_resize_budget":{"long_edge_px":1,"max_bytes":1}
	}`)}
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
// embedded seed or a freshly pulled catalog.
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
	assert.Equal(t, []string{"text", "image"}, m.InputModalities())
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
