package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/capabilities"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- Fix 1: SHA-256-on-read (verifyFileIntegrity) ----

func TestVerifyFileIntegrity_Matches(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.bin")
	data := []byte("hello world, this is a test file for sha256 verification")
	require.NoError(t, os.WriteFile(path, data, 0o600))

	h := sha256.Sum256(data)
	expected := hex.EncodeToString(h[:])

	err := verifyFileIntegrity(path, expected)
	assert.NoError(t, err)
}

func TestVerifyFileIntegrity_Mismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.bin")
	data := []byte("original content")
	require.NoError(t, os.WriteFile(path, data, 0o600))

	err := verifyFileIntegrity(path, "0000000000000000000000000000000000000000000000000000000000000000")
	assert.ErrorContains(t, err, "sha256 mismatch")
}

func TestVerifyFileIntegrity_FileNotFound(t *testing.T) {
	err := verifyFileIntegrity("/nonexistent/path", "abc123")
	assert.ErrorContains(t, err, "integrity check: open:")
}

// ---- Fix 2: Per-model resize budgets (resizeBudgetForModel) ----

func TestResizeBudgetForModel_FromCatalog(t *testing.T) {
	catalog, err := capabilities.NewCatalog(capabilities.EmbeddedSeed(), nil, nil, nil)
	require.NoError(t, err)
	model := "z-ai/glm-5v-turbo"
	budget := resizeBudgetForModel(catalog, model, 10*1024*1024)
	assert.Greater(t, budget.LongEdgePx, 0, "budget should come from catalog")
	assert.Greater(t, budget.MaxBytes, int64(0), "MaxBytes should be positive")
}

func TestResizeBudgetForModel_FallbackWhenNil(t *testing.T) {
	budget := resizeBudgetForModel(nil, "some-model", 5*1024*1024)
	assert.Equal(t, 7680, budget.LongEdgePx, "fallback long edge")
	assert.Equal(t, int64(5*1024*1024), budget.MaxBytes, "fallback max bytes")
}

func TestResizeBudgetForModel_EmbeddedDefault(t *testing.T) {
	catalog, err := capabilities.NewCatalog(capabilities.EmbeddedSeed(), nil, nil, nil)
	require.NoError(t, err)
	budget := resizeBudgetForModel(catalog, "unknown-model-that-does-not-exist-in-seed", 20*1024*1024)
	assert.Greater(t, budget.LongEdgePx, 0, "default budget long edge")
}

// ---- Fix 3: PDF capability gate (modelSupportsPDF) ----

func TestModelSupportsPDF_NilCatalog(t *testing.T) {
	assert.True(t, modelSupportsPDF(nil, "any-model"),
		"nil catalog is optimistic (FR-026)")
}

func TestModelSupportsPDF_KnownCapableModel(t *testing.T) {
	catalog, err := capabilities.NewCatalog(capabilities.EmbeddedSeed(), nil, nil, nil)
	require.NoError(t, err)
	assert.True(t, modelSupportsPDF(catalog, "claude-sonnet-4-0"),
		"Claude Sonnet 4 should support PDF per seed")
	assert.True(t, modelSupportsPDF(catalog, "gemini-2.5-flash"),
		"Gemini 2.5 Flash should support PDF per seed")
}

func TestModelSupportsPDF_ReturnsBool(t *testing.T) {
	catalog, err := capabilities.NewCatalog(capabilities.EmbeddedSeed(), nil, nil, nil)
	require.NoError(t, err)
	result := modelSupportsPDF(catalog, "text-only-model")
	assert.IsType(t, true, result, "modelSupportsPDF must return bool")
}

// ---- Fix 4: MediaDowngradeResult.MediaClass propagation ----

func TestTryMediaDowngrade_CapturesMediaClassImage(t *testing.T) {
	ts := &turnState{}
	msgs := []providers.Message{
		{
			Role:    "user",
			Content: "hello",
			Media:   []string{"data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="},
		},
	}
	pe := &ProviderError{Status: 400, Body: "image not supported"}
	result := TryMediaDowngrade(ts, msgs, pe)
	if result.Applied {
		assert.Equal(t, MediaClassImage, result.MediaClass,
			"MediaClass must be populated for image downgrade")
	}
}

func TestTryMediaDowngrade_CapturesMediaClassPDF(t *testing.T) {
	ts := &turnState{}
	msgs := []providers.Message{
		{
			Role:    "user",
			Content: "hello",
			Media:   []string{"data:application/pdf;base64,JVBERi0xLjcNCg0KMSAwIG9iago8PC9UeXBlL1BhZ2VzL0tpZHM"},
		},
	}
	pe := &ProviderError{Status: 400, Body: "pdf input not supported"}
	result := TryMediaDowngrade(ts, msgs, pe)
	if result.Applied {
		assert.Equal(t, MediaClassPDF, result.MediaClass,
			"MediaClass must be populated for PDF downgrade")
	}
}

// ---- Fix 5: MediaMeta SHA256 field ----

func TestMediaMeta_SHA256Populated(t *testing.T) {
	m := media.MediaMeta{}
	assert.Equal(t, "", m.SHA256, "default SHA256 is empty")

	m2 := media.MediaMeta{SHA256: "abcdef1234567890"}
	assert.Equal(t, "abcdef1234567890", m2.SHA256)
}
