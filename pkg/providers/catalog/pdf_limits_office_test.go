package catalog

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// Office-preview PDF limits after the #1968 merge (founder decision
// 2026-11-28, "one mechanism for both"): the catalogue carries only the pdf
// input modality; a provider's native-PDF size and per-request document
// count come from engine configuration (config.Config.ResolvePDFLimits).
// These tests keep the original intent of the office-preview catalogue
// tests — a provider's documented byte and document-count limits resolve
// exactly, are immutable to callers, are unspecified for an unknown
// provider, and a non-positive value is never accepted as a limit — but
// assert it against the one mechanism that now owns the limits.

// pdfLimitsOfficeConfig is an operator pdf_limits section with one provider
// row stating the two office-preview limits.
func pdfLimitsOfficeConfig(t *testing.T, provider string, bytes, documents int) *config.Config {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"overrides": []map[string]any{{
			"provider":                  provider,
			"max_file_bytes":            bytes,
			"max_documents_per_request": documents,
		}},
	})
	require.NoError(t, err)
	var section config.PDFLimitsConfig
	require.NoError(t, json.Unmarshal(raw, &section))
	return &config.Config{PDFLimits: &section}
}

func TestOfficePDFLimits_ParseResolveAndCopyIsolation(t *testing.T) {
	for _, limits := range []struct{ bytes, documents int }{{1, 1}, {20 << 20, 5}, {(20 << 20) + 1, 6}} {
		m := fixtureMap(t)
		row := provider(t, m, 0)
		// A legacy catalogue pdf_limits member is ignored, never a limit.
		row["pdf_limits"] = map[string]any{"max_document_bytes": 7, "max_documents_per_request": 7}
		id, ok := row["id"].(string)
		require.True(t, ok)
		modelMap, ok := models(t, row)[0].(map[string]any)
		require.True(t, ok)
		modelID, ok := modelMap["id"].(string)
		require.True(t, ok)
		c := mustCatalog(t, encode(t, m))
		require.True(t, c.Resolve(id, modelID).Found())

		cfg := pdfLimitsOfficeConfig(t, id, limits.bytes, limits.documents)
		got := cfg.ResolvePDFLimits(id, modelID)
		gotBytes, set := got.MaxFileBytes()
		require.True(t, set)
		require.Equal(t, int64(limits.bytes), gotBytes)
		gotDocs, set := got.MaxDocumentsPerRequest()
		require.True(t, set)
		require.Equal(t, int64(limits.documents), gotDocs)
		// PDFLimits is a value with unexported fields: a caller's copy
		// cannot change what the next resolution returns.
		again := cfg.ResolvePDFLimits(id, modelID)
		require.Equal(t, got, again, "public callers must not mutate the resolved limits")
	}
}

func TestOfficePDFLimits_AbsentAndUnknownProviderRemainUnspecified(t *testing.T) {
	m := fixtureMap(t)
	row := provider(t, m, 0)
	delete(row, "pdf_limits")
	id, ok := row["id"].(string)
	require.True(t, ok)
	mustCatalog(t, encode(t, m))
	// An operator row for one provider states nothing for another; the
	// unknown provider gets only the shipped default row.
	cfg := pdfLimitsOfficeConfig(t, id, 1, 1)
	shipped := (*config.Config)(nil).ResolvePDFLimits("unknown-provider", "unknown-model")
	require.Equal(t, shipped, cfg.ResolvePDFLimits("unknown-provider", "unknown-model"))
	gotBytes, _ := cfg.ResolvePDFLimits("unknown-provider", "unknown-model").MaxFileBytes()
	require.NotEqual(t, int64(1), gotBytes, "a provider row must not leak to another provider")
}

func TestOfficePDFLimits_RejectNonpositiveAndIncompleteObjects(t *testing.T) {
	// Catalogue side: a malformed legacy pdf_limits member no longer rejects
	// the feed; it is ignored like any unknown member (#1968 R1).
	for _, value := range []any{map[string]any{}, map[string]any{"max_document_bytes": 0, "max_documents_per_request": 5}, map[string]any{"max_document_bytes": -1}} {
		m := fixtureMap(t)
		provider(t, m, 0)["pdf_limits"] = value
		stale, err := ParseDocument(encode(t, m))
		require.NoError(t, err)
		delete(provider(t, m, 0), "pdf_limits")
		clean, err := ParseDocument(encode(t, m))
		require.NoError(t, err)
		require.True(t, reflect.DeepEqual(clean, stale), "a catalogue pdf_limits member must not survive parsing")
	}
	// Config side: a negative limit is rejected with its field path. Zero is
	// the config's documented "no limit" value, not an error (#1968 config
	// contract; the office catalogue rejected zero).
	for _, field := range []string{"max_file_bytes", "max_documents_per_request"} {
		raw, err := json.Marshal(map[string]any{"overrides": []map[string]any{{"provider": "openai", field: -1}}})
		require.NoError(t, err)
		var section config.PDFLimitsConfig
		err = json.Unmarshal(raw, &section)
		require.ErrorContains(t, err, field)
		require.ErrorContains(t, err, "must be >= 0")
	}
}
