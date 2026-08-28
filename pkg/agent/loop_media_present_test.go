// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// loop_media_present_test.go — Wave 3 T9 orchestrator integration tests.
//
// These tests assert the NEW observable contract that T9 wires into
// resolveMediaRefsWithOffload:
//
//   - Step 1 capability gate (FR-010): a catalog that says the model lacks
//     the image modality routes images to step 5 offload, skipping native
//     send (step 2). A vision-capable model proceeds to step 2. A nil
//     catalog is optimistic (FR-026) — the gate always passes.
//   - Manifest refcount lifecycle (FR-007a): workspace library refs
//     increment the manifest refcount when processed; the per-session
//     dedup wrapper prevents over-counting.
//
// The existing TestResolveMediaRefs_* tests in loop_media_test.go and
// loop_test.go exercise the nil-catalog / nil-refcounter path (identical to
// the pre-T9 behavior) and are preserved unchanged — they map to the same
// acceptance criteria via the optimistic-degradation posture.

package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/media/library"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

// catalogDocJSON builds a minimal valid ADR-067 2.0.0 catalog document with
// one provider carrying one model with the given input modalities. It is the
// fixture behind every capability-gate assertion below: the gate resolves by
// the exact (provider, model) pair, so both halves of the key must be real.
func catalogDocJSON(t *testing.T, providerID, modelID string, modalities ...string) []byte {
	t.Helper()
	doc := map[string]any{
		"schema_version":        "2.0.0",
		"version":               "v2026.7.23",
		"updated_at":            "2026-07-23T00:00:00Z",
		"source":                "test",
		"default_resize_limits": map[string]any{"long_edge_px": 7680, "max_bytes": 10485760},
		"providers": []map[string]any{
			{
				"id":            providerID,
				"name":          providerID,
				"api":           "https://api.example.test/v1",
				"protocol":      "openai-compatible",
				"tier":          "standard",
				"auth_methods":  []string{"api_key"},
				"resize_limits": map[string]any{"long_edge_px": 7680, "max_bytes": 10485760},
				"models": []map[string]any{
					{
						"id":               modelID,
						"name":             modelID,
						"context_window":   128000,
						"input_modalities": modalities,
						"tool_call":        true,
						"status":           "active",
					},
				},
			},
		},
	}
	data, err := json.Marshal(doc)
	require.NoError(t, err)
	return data
}

// textOnlyCatalogJSON is the fixture whose model FAILS the image gate.
func textOnlyCatalogJSON(t *testing.T, providerID, modelID string) []byte {
	t.Helper()
	return catalogDocJSON(t, providerID, modelID, "text")
}

// visionCatalogJSON is the fixture whose model PASSES the image gate.
func visionCatalogJSON(t *testing.T, providerID, modelID string) []byte {
	t.Helper()
	return catalogDocJSON(t, providerID, modelID, "text", "image")
}

func mustCatalog(t *testing.T, docJSON []byte) *catalog.Catalog {
	t.Helper()
	c, err := catalog.NewCatalog(docJSON)
	require.NoError(t, err)
	return c
}

// realPNGBytes returns a minimal valid 1x1 PNG.
func realPNGBytes() []byte {
	return []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53,
		0xDE, 0x00, 0x00, 0x00, 0x10, 0x49, 0x44, 0x41,
		0x54, 0x78, 0x9C, 0x62, 0x6A, 0x68, 0x68, 0x00,
		0x04, 0x00, 0x00, 0xFF, 0xFF, 0x03, 0x0C, 0x01,
		0x83, 0x71, 0x4B, 0xD2, 0x4E, 0x00, 0x00, 0x00,
		0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE, 0x42, 0x60,
		0x82,
	}
}

// TestPresentation_Step1Gate_TextOnlyModel_RoutesToOffload (FR-010, US-5 AC1,
// spec test #16): when the capability catalog says the model lacks the image
// modality, the orchestrator skips native send (step 2) and routes the image
// directly to step 5 offload — no data URL is emitted, and the guidance +
// filesystem path appear in content.
func TestPresentation_Step1Gate_TextOnlyModel_RoutesToOffload(t *testing.T) {
	const model = "deepseek/deepseek-chat"
	store := media.NewFileMediaStore()

	pngPath := filepath.Join(t.TempDir(), "img.png")
	require.NoError(t, os.WriteFile(pngPath, realPNGBytes(), 0o600))
	ref, err := store.Store(pngPath, media.MediaMeta{
		Filename:    "img.png",
		ContentType: "image/png",
	}, "test-scope")
	require.NoError(t, err)

	cat := mustCatalog(t, textOnlyCatalogJSON(t, "acme", model))
	workDir := filepath.Join(t.TempDir(), "work")
	sink := &offloadSink{workDir: workDir}

	msgs := []providers.Message{
		{Role: "user", Content: "describe this", Media: []string{ref}},
	}
	result := resolveMediaRefsWithOffload(msgs, store, 10*1024*1024, "acme", model, sink, cat, nil, "")

	require.Len(t, result, 1)
	// No image data URL — the gate skipped native send.
	assert.Empty(t, result[0].Media, "text-only model must not receive an image data URL")
	// Step 5 fired: guidance + filesystem path in content.
	assert.Contains(t, result[0].Content, "Cannot read this image with "+model)
	assert.Contains(t, result[0].Content, workDir, "offload path injected")
}

// TestPresentation_Step1Gate_VisionModel_Proceeds (FR-010, US-5 AC2, spec
// test #17): when the catalog says the model HAS the image modality, the gate
// passes and step 2 normalize runs — the image appears as a PNG data URL.
func TestPresentation_Step1Gate_VisionModel_Proceeds(t *testing.T) {
	const model = "anthropic/claude-sonnet-4"
	store := media.NewFileMediaStore()

	pngPath := filepath.Join(t.TempDir(), "img.png")
	require.NoError(t, os.WriteFile(pngPath, realPNGBytes(), 0o600))
	ref, err := store.Store(pngPath, media.MediaMeta{
		Filename:    "img.png",
		ContentType: "image/png",
	}, "test-scope")
	require.NoError(t, err)

	cat := mustCatalog(t, visionCatalogJSON(t, "acme", model))

	msgs := []providers.Message{
		{Role: "user", Content: "describe this", Media: []string{ref}},
	}
	result := resolveMediaRefsWithOffload(msgs, store, 10*1024*1024, "acme", model, nil, cat, nil, "")

	require.Len(t, result, 1)
	// Step 2 ran: the image is a normalized PNG data URL.
	require.Len(t, result[0].Media, 1, "vision model must receive the normalized image")
	assert.True(t, strings.HasPrefix(result[0].Media[0], "data:image/png;base64,"),
		"gate-pass routes to step 2 normalize, got prefix %q", result[0].Media[0][:30])
}

// TestPresentation_Step1Gate_NilCatalog_Optimistic (FR-026): a nil catalog
// means the gate is not wired; the optimistic default applies (assume
// image-capable). The image proceeds to step 2 normalize as before T9.
func TestPresentation_Step1Gate_NilCatalog_Optimistic(t *testing.T) {
	store := media.NewFileMediaStore()

	pngPath := filepath.Join(t.TempDir(), "img.png")
	require.NoError(t, os.WriteFile(pngPath, realPNGBytes(), 0o600))
	ref, err := store.Store(pngPath, media.MediaMeta{
		Filename:    "img.png",
		ContentType: "image/png",
	}, "test-scope")
	require.NoError(t, err)

	msgs := []providers.Message{
		{Role: "user", Content: "describe this", Media: []string{ref}},
	}
	// nil catalog → optimistic → step 2 runs even for an unknown model.
	result := resolveMediaRefsWithOffload(msgs, store, 10*1024*1024, "", "some-unknown-model", nil, nil, nil, "")

	require.Len(t, result, 1)
	require.Len(t, result[0].Media, 1, "nil catalog = optimistic; image is normalized")
	assert.True(t, strings.HasPrefix(result[0].Media[0], "data:image/png;base64,"))
}

// TestPresentation_Step1Gate_TextOnlyModel_SVG_GetsOffloadPlusMarkup (FR-010 +
// FR-022): a text-only model with an SVG skips step 2 (rasterize) but still
// gets step 5 (offload + guidance) AND step 6 (SVG markup text-injection) —
// the steps compose. This is the "malformed SVG on text-only model" path from
// the spec (US-9 AC1), generalized: even a VALID SVG on a text-only model
// routes through offload+markup because the gate blocks rasterization.
func TestPresentation_Step1Gate_TextOnlyModel_SVG_GetsOffloadPlusMarkup(t *testing.T) {
	const model = "z-ai/glm-5.2"
	store := media.NewFileMediaStore()

	svgPath := filepath.Join(t.TempDir(), "circle.svg")
	require.NoError(t, os.WriteFile(svgPath, []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100"><circle cx="50" cy="50" r="40" fill="blue"/></svg>`,
	), 0o600))
	ref, err := store.Store(svgPath, media.MediaMeta{
		Filename:    "circle.svg",
		ContentType: "image/svg+xml",
	}, "test-scope")
	require.NoError(t, err)

	cat := mustCatalog(t, textOnlyCatalogJSON(t, "acme", model))
	workDir := filepath.Join(t.TempDir(), "work")
	sink := &offloadSink{workDir: workDir}

	msgs := []providers.Message{
		{Role: "user", Content: "what shape is this", Media: []string{ref}},
	}
	result := resolveMediaRefsWithOffload(msgs, store, 10*1024*1024, "acme", model, sink, cat, nil, "")

	require.Len(t, result, 1)
	// Gate blocked rasterization: no data URL.
	assert.Empty(t, result[0].Media, "text-only model must not receive a rasterized SVG data URL")
	content := result[0].Content
	// Step 5: guidance + path.
	assert.Contains(t, content, "Cannot read this image with "+model)
	assert.Contains(t, content, workDir)
	// Step 6: SVG markup injected.
	assert.Contains(t, content, "[Attached file", "step 6 text injection must fire for SVG")
	assert.Contains(t, content, "<circle", "SVG markup must be present")
	// FR-022: guidance prefixes the markup.
	guidanceIdx := strings.Index(content, "Cannot read this image")
	markupIdx := strings.Index(content, "<circle")
	require.True(t, guidanceIdx >= 0 && markupIdx >= 0)
	assert.Less(t, guidanceIdx, markupIdx, "guidance must prefix the SVG markup")
}

// mockRefcounter is a test double for workspaceRefcounter that records calls.
type mockRefcounter struct {
	mu          sync.Mutex
	incremented map[string]int
	decremented map[string]int
}

func newMockRefcounter() *mockRefcounter {
	return &mockRefcounter{
		incremented: map[string]int{},
		decremented: map[string]int{},
	}
}

func (m *mockRefcounter) IncrementRefcount(mediaID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.incremented[mediaID]++
	return m.incremented[mediaID], nil
}

func (m *mockRefcounter) DecrementRefcount(mediaID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.decremented[mediaID]++
	return m.decremented[mediaID], nil
}

// TestPresentation_RefcountIncrement_WorkspaceRef (FR-007a, spec test #36):
// the incrementWorkspaceRef helper parses a media://workspace/<ws>/<id> ref
// and calls IncrementRefcount with the media ID. Legacy media://<uuid> refs
// and a nil refcounter are no-ops (no manifest entry / no tracking).
func TestPresentation_RefcountIncrement_WorkspaceRef(t *testing.T) {
	rc := newMockRefcounter()
	const mediaID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	wsRef := "media://workspace/ws-1/" + mediaID
	legacyRef := "media://550e8400-e29b-41d4-a716-446655440000"

	// Workspace ref → increment fires.
	incrementWorkspaceRef(rc, wsRef)
	rc.mu.Lock()
	assert.Equal(t, 1, rc.incremented[mediaID],
		"workspace ref must increment the manifest refcount for its media ID")
	rc.mu.Unlock()

	// Legacy ref → no-op (no manifest entry).
	incrementWorkspaceRef(rc, legacyRef)
	rc.mu.Lock()
	_, hasLegacy := rc.incremented["550e8400-e29b-41d4-a716-446655440000"]
	assert.False(t, hasLegacy, "legacy media://<uuid> refs must not increment the manifest refcount")
	rc.mu.Unlock()

	// Nil refcounter → no-op (no panic).
	incrementWorkspaceRef(nil, wsRef)
}

// TestPresentation_RefcountIncrement_PerSessionDedup (FR-007a): the
// sessionRefcounter wrapper dedupes increments per session — processing the
// same workspace ref multiple times in one session increments the library
// only once.
func TestPresentation_RefcountIncrement_PerSessionDedup(t *testing.T) {
	inner := newMockRefcounter()
	wrapped := &sessionRefcounter{lib: inner, seen: &sync.Map{}}

	mediaID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	ref := "media://workspace/ws-1/" + mediaID

	// Process the same ref three times.
	incrementWorkspaceRef(wrapped, ref)
	incrementWorkspaceRef(wrapped, ref)
	incrementWorkspaceRef(wrapped, ref)

	inner.mu.Lock()
	assert.Equal(t, 1, inner.incremented[mediaID],
		"per-session dedup: the library is incremented exactly once for repeated refs")
	inner.mu.Unlock()
}

// TestModelSupportsImage_NilCatalogOptimistic verifies the optimistic
// default: a nil catalog means the gate always passes.
func TestModelSupportsImage_NilCatalogOptimistic(t *testing.T) {
	assert.True(t, modelSupportsImage(nil, "any-provider", "any-model"),
		"nil catalog → optimistic → image-capable")
}

// TestModelSupportsImage_CatalogGates verifies the catalog-driven gate and,
// with it, the ADR-067 FR-003 exact-pair rule: the gate answers for a
// (provider, model) pair, and the RIGHT model id under the WRONG provider is
// a miss, not a hit.
func TestModelSupportsImage_CatalogGates(t *testing.T) {
	textOnly := mustCatalog(t, textOnlyCatalogJSON(t, "acme", "text-only-model"))
	vision := mustCatalog(t, visionCatalogJSON(t, "acme", "vision-model"))

	assert.False(t, modelSupportsImage(textOnly, "acme", "text-only-model"),
		"catalog says model lacks image → gate blocks")
	assert.True(t, modelSupportsImage(vision, "acme", "vision-model"),
		"catalog says model has image → gate passes")
	// FR-004: an unknown model resolves optimistically (text + image).
	assert.True(t, modelSupportsImage(textOnly, "acme", "not-in-catalog"),
		"unknown model → optimistic")
	// FR-003: the same model id under a provider that does not carry it is a
	// miss — never a prefix-stripped or bare-id hit on the other row.
	assert.True(t, modelSupportsImage(textOnly, "other-provider", "text-only-model"),
		"right model id, wrong provider → miss → optimistic, never the text-only row")
}

// TestModelSupportsPDF_MissIsNotOptimistic pins the asymmetry FR-004
// deliberately creates: a nil catalog is optimistic for every modality, but a
// catalog MISS is optimistic for text and image only. An unknown model must
// therefore route a PDF to offload rather than send a document block the
// provider would reject.
func TestModelSupportsPDF_MissIsNotOptimistic(t *testing.T) {
	assert.True(t, modelSupportsPDF(nil, "acme", "any-model"),
		"nil catalog → optimistic for every modality")

	loaded := mustCatalog(t, catalogDocJSON(t, "acme", "doc-model", "text", "image", "pdf"))
	assert.True(t, modelSupportsPDF(loaded, "acme", "doc-model"),
		"catalog says model has pdf → gate passes")
	assert.False(t, modelSupportsPDF(loaded, "acme", "unknown-model"),
		"catalog miss → optimistic set is text+image only → pdf gate blocks")
}

// TestResizeBudgetForModel_HitMissAndNilCatalog pins the FR-004 budget
// contract the media pipeline depends on: a hit carries the provider's own
// resize_limits, a miss carries the document default, and no catalog at all
// carries the package default long edge with the operator's byte cap.
func TestResizeBudgetForModel_HitMissAndNilCatalog(t *testing.T) {
	const maxSize = 3 * 1024 * 1024

	doc := map[string]any{
		"schema_version":        "2.0.0",
		"version":               "v2026.7.23",
		"updated_at":            "2026-07-23T00:00:00Z",
		"source":                "test",
		"default_resize_limits": map[string]any{"long_edge_px": 7680, "max_bytes": 10485760},
		"providers": []map[string]any{
			{
				"id":            "acme",
				"name":          "acme",
				"api":           "https://api.example.test/v1",
				"protocol":      "openai-compatible",
				"tier":          "standard",
				"auth_methods":  []string{"api_key"},
				"resize_limits": map[string]any{"long_edge_px": 4096, "max_bytes": 5242880},
				"models": []map[string]any{{
					"id": "m1", "name": "m1", "context_window": 1000,
					"input_modalities": []string{"text", "image"},
					"tool_call":        true, "status": "active",
				}},
			},
		},
	}
	data, err := json.Marshal(doc)
	require.NoError(t, err)
	cat := mustCatalog(t, data)

	assert.Equal(t,
		catalog.ResizeLimits{LongEdgePx: 4096, MaxBytes: 5242880},
		resizeBudgetForModel(cat, "acme", "m1", maxSize),
		"hit → the provider's own resize_limits")
	assert.Equal(t,
		catalog.ResizeLimits{LongEdgePx: 7680, MaxBytes: 10485760},
		resizeBudgetForModel(cat, "acme", "unknown", maxSize),
		"miss → the document default_resize_limits")
	assert.Equal(t,
		catalog.ResizeLimits{LongEdgePx: catalog.DefaultResizeLimits.LongEdgePx, MaxBytes: maxSize},
		resizeBudgetForModel(nil, "acme", "m1", maxSize),
		"no catalog → package default long edge with the operator byte cap")
}

// ── E2E (env-gated, spec test #33 / #34) ─────────────────────────────────────
//
// Per the spec's E2E gating rule (TDD plan header, line 915): all provider-
// touching E2E tests MUST be gated behind env vars and skip when unset — no
// live provider calls in default CI. OMNIPUS_E2E_VISION_MODEL /
// OMNIPUS_E2E_NO_VISION_MODEL carry the model id under test; when absent these
// t.Skip cleanly (CI green). When present they drive the full 7-step
// presentation chain — the deterministic "any file, any model → useful turn"
// guarantee (SC-003) — against that real model id. A live HTTP provider call
// is a future hook (OMNIPUS_E2E_PROVIDER_KEY); the env gate is the required
// minimum and what CI honors.

// TestE2E_AnyFileAnyModel_UsefulTurn (spec test #33, SC-003): upload an AVIF
// (a format no provider accepts natively and no pure-Go decoder can normalize)
// and run the full presentation chain against a vision-capable model. The turn
// MUST be useful — the file offloads to work/ with guidance, never a dead turn
// or a raw error surfaced to the user. Gated behind OMNIPUS_E2E_VISION_MODEL.
func TestE2E_AnyFileAnyModel_UsefulTurn(t *testing.T) {
	model := os.Getenv("OMNIPUS_E2E_VISION_MODEL")
	if model == "" {
		t.Skip("skipping media E2E; set OMNIPUS_E2E_VISION_MODEL=<model-id> to run")
	}
	store := media.NewFileMediaStore()
	ref, _ := storeFile(t, store, "photo.avif", "image/avif", []byte("unsupported-fake-bytes"))

	workDir := filepath.Join(t.TempDir(), "work")
	sink := &offloadSink{workDir: workDir}
	cat := mustCatalog(t, visionCatalogJSON(t, "acme", model))

	msgs := []providers.Message{
		{Role: "user", Content: "describe this attachment", Media: []string{ref}},
	}
	result := resolveMediaRefsWithOffload(msgs, store, 10*1024*1024, "acme", model, sink, cat, nil, "")

	require.Len(t, result, 1, "chain must produce a result message")
	// SC-003: useful turn — AVIF routes to offload, not the dead-end marker,
	// because an offload sink is configured.
	assert.NotContains(t, result[0].Content, "attachment unavailable",
		"AVIF must not dead-end at the honest marker when an offload sink exists")
	// AVIF has no pure-Go decoder → no image data URL is emitted (FR-016:
	// no passthrough); it offloads with guidance + a reachable path instead.
	assert.Empty(t, result[0].Media, "AVIF must not be sent as a data URL to any model")
	assert.Contains(t, result[0].Content, "Cannot read this image with "+model,
		"useful turn: offload guidance names the model")
	assert.Contains(t, result[0].Content, workDir,
		"useful turn: the offloaded copy's filesystem path is injected (agent can read it)")
}

// TestE2E_TextOnlyModel_ImageSurvivesAsOffload (spec test #34, US-5 AC1): a
// text-only model must never receive an image block (step 1 capability gate),
// but the image still "survives" — it offloads to work/ with guidance so the
// turn is useful, never silently stripped/lost. Gated behind
// OMNIPUS_E2E_NO_VISION_MODEL.
func TestE2E_TextOnlyModel_ImageSurvivesAsOffload(t *testing.T) {
	model := os.Getenv("OMNIPUS_E2E_NO_VISION_MODEL")
	if model == "" {
		t.Skip("skipping media E2E; set OMNIPUS_E2E_NO_VISION_MODEL=<model-id> to run")
	}
	store := media.NewFileMediaStore()
	ref, _ := storeFile(t, store, "img.png", "image/png", realPNGBytes())

	workDir := filepath.Join(t.TempDir(), "work")
	sink := &offloadSink{workDir: workDir}
	cat := mustCatalog(t, textOnlyCatalogJSON(t, "acme", model))

	msgs := []providers.Message{
		{Role: "user", Content: "describe this image", Media: []string{ref}},
	}
	result := resolveMediaRefsWithOffload(msgs, store, 10*1024*1024, "acme", model, sink, cat, nil, "")

	require.Len(t, result, 1, "chain must produce a result message")
	// The image survives as offload, not as a native send: no data URL.
	assert.Empty(t, result[0].Media,
		"text-only model must not receive an image data URL (capability gate)")
	// And it is not dropped — the offload path + guidance are present.
	assert.Contains(t, result[0].Content, "Cannot read this image with "+model,
		"image survives: offload guidance present")
	assert.Contains(t, result[0].Content, workDir,
		"image survives: the offloaded copy is reachable at the injected path")
}

// TestNormalizeImage_CachedBySHA256 (FR-004, ADR-051 Rev 4 Gap 1):
// calling encodeImageToDataURLCached twice with the same source bytes +
// same model slot + same budget must return a byte-identical data URL,
// and the second call must hit the sha256-keyed cache (verified via
// the process-wide GlobalNormalizeCacheStats counters).
//
// The contract under test:
//
//   - The sha256 is the source-of-truth identity (NOT the sha256 of
//     the normalized output), so the cache key is content-derived and
//     identical inputs trivially produce identical outputs.
//   - The cache is consulted BEFORE the decode → resize → encode
//     pipeline (FR-004), so the second call must skip that work
//     entirely — observable via a +1 hit on the global counter.
//   - The model slot and the budget are part of the cache key, so two
//     calls that differ in either field do NOT collide. Verified by
//     asserting hit-counter deltas across budget/model variations.
//   - The cache is process-wide (sync.Once); the test reads the
//     counters via library.GlobalNormalizeCacheStats to assert the
//     relative deltas around each call.
func TestNormalizeImage_CachedBySHA256(t *testing.T) {
	const model = "anthropic/claude-sonnet-4"

	pngPath := filepath.Join(t.TempDir(), "cache-img.png")
	require.NoError(t, os.WriteFile(pngPath, realPNGBytes(), 0o600))
	info, err := os.Stat(pngPath)
	require.NoError(t, err)

	// Use the same budget shape the production orchestrator would
	// produce for this model via resizeBudgetForModel (the FR-014 path).
	budget := catalog.ResizeLimits{LongEdgePx: 7680, MaxBytes: 10 * 1024 * 1024}

	// First call: populates the cache (FR-004 miss path).
	dataURL1 := encodeImageToDataURLCached(pngPath, "image/png", info, 10*1024*1024, model, budget)
	require.NotEmpty(t, dataURL1, "first call must produce a data URL")
	assert.True(t, strings.HasPrefix(dataURL1, "data:image/png;base64,"),
		"first call: PNG output for a 1×1 PNG within budget, got prefix %q",
		dataURL1[:min(40, len(dataURL1))])

	// Second call with identical inputs: must hit the cache and return
	// byte-identical output. The hit-counter delta across this call is
	// the FR-004 hit assertion.
	hitsBefore2 := library.GlobalNormalizeCacheStats().Hits
	dataURL2 := encodeImageToDataURLCached(pngPath, "image/png", info, 10*1024*1024, model, budget)
	hitsAfter2 := library.GlobalNormalizeCacheStats().Hits

	assert.Equal(t, dataURL1, dataURL2,
		"second call must return byte-identical data URL (cache hit)")
	assert.Equal(t, hitsBefore2+1, hitsAfter2,
		"second call must register exactly one cache hit (FR-004)")

	// Third call: distinct budget. The cache key MUST differ (the
	// budget is part of the key), so the call is a MISS — hit counter
	// is unchanged across this call.
	budgetTight := catalog.ResizeLimits{LongEdgePx: 1024, MaxBytes: 1 * 1024 * 1024}
	hitsBefore3 := library.GlobalNormalizeCacheStats().Hits
	dataURL3 := encodeImageToDataURLCached(pngPath, "image/png", info, 10*1024*1024, model, budgetTight)
	hitsAfter3 := library.GlobalNormalizeCacheStats().Hits
	require.NotEmpty(t, dataURL3)
	assert.Equal(t, hitsBefore3, hitsAfter3,
		"distinct budget = distinct cache key = MISS, hit counter unchanged")

	// Fourth call: same tight budget as call 3. This MUST hit (same
	// cache key as call 3 just populated).
	hitsBefore4 := library.GlobalNormalizeCacheStats().Hits
	dataURL4 := encodeImageToDataURLCached(pngPath, "image/png", info, 10*1024*1024, model, budgetTight)
	hitsAfter4 := library.GlobalNormalizeCacheStats().Hits
	assert.Equal(t, dataURL3, dataURL4,
		"same inputs as call 3 = same cache key = byte-identical output")
	assert.Equal(t, hitsBefore4+1, hitsAfter4,
		"fourth call must register exactly one cache hit (same key as call 3)")

	// Fifth call: distinct model slot, original budget. The model
	// slot is part of the cache key, so this MUST miss.
	hitsBefore5 := library.GlobalNormalizeCacheStats().Hits
	dataURL5 := encodeImageToDataURLCached(pngPath, "image/png", info, 10*1024*1024, "openai/gpt-4o", budget)
	hitsAfter5 := library.GlobalNormalizeCacheStats().Hits
	require.NotEmpty(t, dataURL5)
	assert.Equal(t, hitsBefore5, hitsAfter5,
		"distinct model slot = distinct cache key = MISS, hit counter unchanged")

	// Sixth call: back to the original (model, budget) pair. This MUST
	// hit (call 1 populated this exact key).
	hitsBefore6 := library.GlobalNormalizeCacheStats().Hits
	dataURL6 := encodeImageToDataURLCached(pngPath, "image/png", info, 10*1024*1024, model, budget)
	hitsAfter6 := library.GlobalNormalizeCacheStats().Hits
	assert.Equal(t, dataURL1, dataURL6,
		"sixth call (original inputs) returns the same data URL as call 1")
	assert.Equal(t, hitsBefore6+1, hitsAfter6,
		"sixth call must register exactly one cache hit (same key as call 1)")
}
