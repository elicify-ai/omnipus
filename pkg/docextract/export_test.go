package docextract

import "testing"

// HoldAllPDFParseSlotsForTest lets the external test package (which can import
// pkg/tools without an import cycle) occupy both shared PDF parse slots with
// stuck parses. Cleanup is registered on t.
func HoldAllPDFParseSlotsForTest(t *testing.T, venue []byte) {
	t.Helper()
	holdAllPDFParseSlots(t, venue)
}

// MakeVenuePDFForTest returns the synthetic one-page venue PDF.
func MakeVenuePDFForTest(t *testing.T) []byte {
	t.Helper()
	return makeSyntheticPDF(t, true, pdfVenueText)
}
