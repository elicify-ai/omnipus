package catalog

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Dispatch requirement: catalog pdf_limits must survive parse and public
// Resolve unchanged. Values here are fixture inputs, not copied defaults.
// Schema field names/positive-value validation come from the documented
// PDFLimits / Handle.PDFLimits / parsePDFLimits contract; the office spec
// itself does not specify this optional JSON object's schema (reported gap).
func TestOfficePDFLimits_ParseResolveAndCopyIsolation(t *testing.T) {
	for _, limits := range []struct{ bytes, documents int }{{1, 1}, {20 << 20, 5}, {(20 << 20) + 1, 6}} {
		m := fixtureMap(t)
		row := provider(t, m, 0)
		row["pdf_limits"] = map[string]any{"max_document_bytes": limits.bytes, "max_documents_per_request": limits.documents}
		id, ok := row["id"].(string)
		require.True(t, ok)
		modelRow := models(t, row)[0]
		modelMap, ok := modelRow.(map[string]any)
		require.True(t, ok)
		modelID, ok := modelMap["id"].(string)
		require.True(t, ok)
		c := mustCatalog(t, encode(t, m))
		handle := c.Resolve(id, modelID)
		require.True(t, handle.Found())
		got := handle.PDFLimits()
		require.Equal(t, &PDFLimits{MaxDocumentBytes: limits.bytes, MaxDocumentsPerRequest: limits.documents}, got)
		got.MaxDocumentBytes = 999
		got.MaxDocumentsPerRequest = 999
		require.Equal(t, &PDFLimits{MaxDocumentBytes: limits.bytes, MaxDocumentsPerRequest: limits.documents}, handle.PDFLimits(), "public callers must not mutate the immutable snapshot")
	}
}

func TestOfficePDFLimits_AbsentAndUnknownProviderRemainUnspecified(t *testing.T) {
	m := fixtureMap(t)
	row := provider(t, m, 0)
	delete(row, "pdf_limits")
	id, ok := row["id"].(string)
	require.True(t, ok)
	c := mustCatalog(t, encode(t, m))
	require.Nil(t, c.Resolve(id, "unknown-model").PDFLimits())
	require.Nil(t, c.Resolve("unknown-provider", "unknown-model").PDFLimits())
}

func TestOfficePDFLimits_RejectNonpositiveAndIncompleteObjects(t *testing.T) {
	cases := []struct {
		name  string
		value any
		field string
	}{
		{"empty", map[string]any{}, "max_document_bytes"},
		{"zero_bytes", map[string]any{"max_document_bytes": 0, "max_documents_per_request": 5}, "max_document_bytes"},
		{"negative_bytes", map[string]any{"max_document_bytes": -1, "max_documents_per_request": 5}, "max_document_bytes"},
		{"zero_documents", map[string]any{"max_document_bytes": 1, "max_documents_per_request": 0}, "max_documents_per_request"},
		{"negative_documents", map[string]any{"max_document_bytes": 1, "max_documents_per_request": -1}, "max_documents_per_request"},
		{"missing_documents", map[string]any{"max_document_bytes": 1}, "max_documents_per_request"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := fixtureMap(t)
			provider(t, m, 0)["pdf_limits"] = tc.value
			doc, err := ParseDocument(encode(t, m))
			require.Nil(t, doc)
			require.ErrorContains(t, err, "pdf_limits."+tc.field)
			require.ErrorContains(t, err, "must be positive")
		})
	}
}
