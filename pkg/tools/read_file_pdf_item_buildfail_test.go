package tools

// BUILD-FAIL FILE — spec #1968 (read-file native PDF, FR-7/FR-8). The tests
// here need symbols that do not exist on the base tree, so they live in
// their own file: while the symbols are missing, ONLY this file fails to
// build and the package's other tests still compile and run.
//
// Expected new symbols (spec §5.2 FR-7; declared exactly as the tests use
// them):
//
//	// on ToolResult (result.go), beside InspectionImages:
//	InspectionPDFs []InspectionPDF `json:"-"`
//
//	// the live-turn PDF evidence item, mirroring InspectionImage's shape:
//	type InspectionPDF struct {
//		Bytes       []byte
//		Name        string
//		Pages       int // 0 = unknown (no /Count, or not parseable locally)
//		SHA256      string
//		HasText     bool
//		Reauthorize func(context.Context) error
//	}
//
// Oracles: spec §6 rows T7 ("tool, both tools: text, scanned, encrypted |
// one item each with exact bytes; header/R1/N1/N3 first") and FR-8's
// "With offset/length: text pages only, no item"; §5.6 fixed notice content.

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// requireSinglePDFItem asserts the one-item contract of FR-7: exactly one
// InspectionPDFs entry carrying the exact bytes, the file name, the hex
// SHA-256 and a Reauthorize closure.
func requireSinglePDFItem(t *testing.T, label string, res *ToolResult, name string, want []byte, wantPages int, wantHasText bool) {
	t.Helper()
	if res.IsError {
		t.Fatalf("%s: read errored: %s", label, res.ForLLM)
	}
	if len(res.InspectionPDFs) != 1 {
		t.Fatalf("%s: InspectionPDFs = %d items, want exactly one (FR-7: every %%PDF- file gets one item)", label, len(res.InspectionPDFs))
	}
	item := res.InspectionPDFs[0]
	if !bytesEquiv(item.Bytes, want) {
		t.Fatalf("%s: item bytes do not match the file (%d vs %d bytes)", label, len(item.Bytes), len(want))
	}
	if item.Name != name {
		t.Fatalf("%s: item Name = %q, want %q", label, item.Name, name)
	}
	if item.Pages != wantPages {
		t.Fatalf("%s: item Pages = %d, want %d", label, item.Pages, wantPages)
	}
	if item.HasText != wantHasText {
		t.Fatalf("%s: item HasText = %v, want %v", label, item.HasText, wantHasText)
	}
	sum := sha256.Sum256(want)
	if item.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("%s: item SHA256 = %q, want the hex digest of the bytes", label, item.SHA256)
	}
	if item.Reauthorize == nil {
		t.Fatalf("%s: item Reauthorize must be set (FR-10 reauthorizes tool items per attempt)", label)
	}
}

// bytesEquiv is bytes.Equal kept local so this file's imports stay minimal.
func bytesEquiv(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestReadFile_PDFItem (T7, B) — every %PDF- file read without offset/length
// and within max_media_size gets exactly one InspectionPDFs item with exact
// bytes, and the persisted notices (header + R1 for text; N1 + R1 for no
// text; N3 + R1 for not parseable locally) come before the body.
func TestReadFile_PDFItem(t *testing.T) {
	t.Run("text pdf: one item, header and R1 before the text", func(t *testing.T) {
		data := buildTextPDF(t, "1.4", 1, true, nil)
		root := t.TempDir()
		for _, reader := range pdfReaders(t, root, "report.pdf", data, MaxReadFileSize) {
			res := reader.exec(context.Background(), map[string]any{"path": "report.pdf"})
			requireSinglePDFItem(t, reader.name+"/text", res, "report.pdf", data, 1, true)

			headerIdx := strings.Index(res.ForLLM, "[document: report.pdf | extracted text | chars 0-")
			r1Idx := strings.Index(res.ForLLM, pdfR1Full)
			bodyIdx := strings.Index(res.ForLLM, "PAGE 1 text")
			if headerIdx < 0 || r1Idx < 0 || bodyIdx < 0 {
				t.Fatalf("%s: persisted text view must carry the document header, full R1 and the text:\n%s", reader.name, res.ForLLM)
			}
			if headerIdx > r1Idx || r1Idx > bodyIdx {
				t.Fatalf("%s: notices must precede the body (header@%d, R1@%d, body@%d)", reader.name, headerIdx, r1Idx, bodyIdx)
			}
			// F14: the SPA preview shows the first 20 lines — every notice
			// must sit within them, or the user never sees it.
			pdfAssertWithinPreviewLines(t, reader.name+"/text", res.ForLLM, headerIdx, r1Idx)
		}
	})

	t.Run("scanned pdf: one item, N1 plus R1", func(t *testing.T) {
		data := buildScannedPDF(t, 1)
		root := t.TempDir()
		for _, reader := range pdfReaders(t, root, "scan.pdf", data, MaxReadFileSize) {
			res := reader.exec(context.Background(), map[string]any{"path": "scan.pdf"})
			requireSinglePDFItem(t, reader.name+"/scanned", res, "scan.pdf", data, 1, false)

			// §5.6 N1, fixed content — non-error, names the document, states
			// the native readability of PDF-capable models.
			if !strings.Contains(res.ForLLM, "no extractable text (likely scanned)") {
				t.Fatalf("%s: N1 notice missing:\n%s", reader.name, res.ForLLM)
			}
			if !strings.Contains(res.ForLLM, "PDF-capable models") {
				t.Fatalf("%s: N1 must point at PDF-capable models:\n%s", reader.name, res.ForLLM)
			}
			if !strings.Contains(res.ForLLM, pdfR1Full) {
				t.Fatalf("%s: full R1 must accompany N1 (FR-8: no text → N1 + R1):\n%s", reader.name, res.ForLLM)
			}
			if strings.Index(res.ForLLM, "no extractable text (likely scanned)") > strings.Index(res.ForLLM, pdfR1Full) {
				t.Fatalf("%s: N1 must precede R1", reader.name)
			}
			// N7: FR-8's contract for a no-text document is N1 + R1 — there
			// is NO extracted-text output and therefore NO extracted-text
			// header (§5.6 pins the header to the "extracted text" form
			// only). The document NAME must still appear (the N1 line names
			// it), and nothing may pretend text was extracted.
			assert.NotContains(t, res.ForLLM, "| extracted text |",
				"a scanned PDF has no extracted text, so no extracted-text header (FR-8)")
			assert.Contains(t, res.ForLLM, "scan.pdf", "N1 names the document")
			pdfAssertWithinPreviewLines(t, reader.name+"/scanned", res.ForLLM,
				strings.Index(res.ForLLM, "no extractable text"), strings.Index(res.ForLLM, pdfR1Full))
		}
	})

	t.Run("encrypted pdf: one item, N3 with the typed reason", func(t *testing.T) {
		data := buildEncryptedPDF(t)
		root := t.TempDir()
		for _, reader := range pdfReaders(t, root, "secure.pdf", data, MaxReadFileSize) {
			res := reader.exec(context.Background(), map[string]any{"path": "secure.pdf"})
			// Not parseable locally (encrypted): still one item, Pages
			// unknown (0), HasText false.
			requireSinglePDFItem(t, reader.name+"/encrypted", res, "secure.pdf", data, 0, false)

			// §5.6 N3, fixed content, typed reason in the parentheses —
			// non-error, points at PDF-capable models.
			if !strings.Contains(res.ForLLM, "could not be read locally (encrypted)") {
				t.Fatalf("%s: N3 notice with the encrypted reason missing:\n%s", reader.name, res.ForLLM)
			}
			if !strings.Contains(res.ForLLM, "PDF-capable models") {
				t.Fatalf("%s: N3 must point at PDF-capable models:\n%s", reader.name, res.ForLLM)
			}
			if !strings.Contains(res.ForLLM, pdfR1Full) {
				t.Fatalf("%s: full R1 must accompany N3 (FR-8: not parseable → N3 + R1):\n%s", reader.name, res.ForLLM)
			}
			// N7: N3 + R1 is the whole FR-8 contract for an unparseable
			// document — no extracted-text header may be invented.
			assert.NotContains(t, res.ForLLM, "| extracted text |",
				"an unparseable PDF has no extracted text, so no extracted-text header (FR-8)")
			assert.Contains(t, res.ForLLM, "secure.pdf", "N3 names the document")
			pdfAssertWithinPreviewLines(t, reader.name+"/encrypted", res.ForLLM,
				strings.Index(res.ForLLM, "could not be read locally"), strings.Index(res.ForLLM, pdfR1Full))
		}
	})

	// T7 extras (round 2): the FR-7/FR-8 negative edges — the oversize and
	// offset/length outcomes, the typed N3 malformed reason, and the JSON
	// exclusion of the InspectionPDFs field.
	t.Run("over max_media_size with text: L1 plus the text, no item", func(t *testing.T) {
		// FR-8 row "over max_media_size": text if extractable + L1. Not an
		// error, and FR-7's precondition (bytes ≤ max_media_size) fails, so
		// NO InspectionPDFs item.
		data := buildTextPDF(t, "1.4", 1, true, nil)
		root := t.TempDir()
		for _, reader := range pdfReaders(t, root, "big.pdf", data, 16) {
			res := reader.exec(context.Background(), map[string]any{"path": "big.pdf"})
			if res.IsError {
				t.Fatalf("%s: an oversize read with extractable text must succeed with L1:\n%s", reader.name, res.ForLLM)
			}
			if len(res.InspectionPDFs) != 0 {
				t.Fatalf("%s: an oversize read must not carry an InspectionPDFs item (FR-7), got %d", reader.name, len(res.InspectionPDFs))
			}
			// §5.6 L1, fixed content.
			if !strings.Contains(res.ForLLM, "[PDF over the ") ||
				!strings.Contains(res.ForLLM, " media size setting; not sent natively]") {
				t.Fatalf("%s: L1 notice with the fixed wording missing:\n%s", reader.name, clip(res.ForLLM))
			}
			if !strings.Contains(res.ForLLM, "PAGE 1 text") {
				t.Fatalf("%s: the extracted text must still travel:\n%s", reader.name, clip(res.ForLLM))
			}
		}
	})

	t.Run("over max_media_size without text: the E1 error, no item", func(t *testing.T) {
		// FR-8 row "over max_media_size": else error E1. The error wording
		// is the implementer's; the contract is: IsError, no item.
		data := buildScannedPDF(t, 1)
		root := t.TempDir()
		for _, reader := range pdfReaders(t, root, "bigscan.pdf", data, 16) {
			res := reader.exec(context.Background(), map[string]any{"path": "bigscan.pdf"})
			if !res.IsError {
				t.Fatalf("%s: an oversize PDF without extractable text must error (E1):\n%s", reader.name, res.ForLLM)
			}
			if len(res.InspectionPDFs) != 0 {
				t.Fatalf("%s: the E1 error must not carry an InspectionPDFs item, got %d", reader.name, len(res.InspectionPDFs))
			}
		}
	})

	t.Run("unsupported version retains one item and the typed N3 notice", func(t *testing.T) {
		data := buildTextPDF(t, "2.0", 1, true, nil)
		for _, reader := range pdfReaders(t, t.TempDir(), "version2.pdf", data, MaxReadFileSize) {
			res := reader.exec(t.Context(), map[string]any{"path": "version2.pdf"})
			requireSinglePDFItem(t, reader.name+"/unsupported", res, "version2.pdf", data, 0, false)
			assert.Contains(t, res.ForLLM, "could not be read locally (unsupported version)")
			assert.Contains(t, res.ForLLM, pdfR1Full)
			pdfAssertWithinPreviewLines(t, reader.name+"/unsupported", res.ForLLM, strings.Index(res.ForLLM, "unsupported version"), strings.Index(res.ForLLM, pdfR1Full))
		}
	})

	t.Run("malformed pdf: one item with N3's typed malformed reason", func(t *testing.T) {
		// FR-8 row "not parseable locally": N3 (non-error, typed reason) +
		// R1 — malformed is one of N3's reasons, so the read SUCCEEDS with
		// an item (Pages unknown, no text) exactly like the encrypted case.
		root := t.TempDir()
		broken := []byte("%PDF-1.4\nthis structure is broken, there is no xref here")
		for _, reader := range pdfReaders(t, root, "broken.pdf", broken, MaxReadFileSize) {
			res := reader.exec(context.Background(), map[string]any{"path": "broken.pdf"})
			requireSinglePDFItem(t, reader.name+"/malformed", res, "broken.pdf", broken, 0, false)
			if !strings.Contains(res.ForLLM, "could not be read locally (malformed)") {
				t.Fatalf("%s: N3 with the typed malformed reason missing:\n%s", reader.name, clip(res.ForLLM))
			}
			if !strings.Contains(res.ForLLM, pdfR1Full) {
				t.Fatalf("%s: full R1 must accompany N3 (FR-8):\n%s", reader.name, clip(res.ForLLM))
			}
		}
	})

	t.Run("offset/length on a no-text pdf: the E3 error, no item", func(t *testing.T) {
		// FR-8: "With offset/length: ... no text → E3".
		data := buildScannedPDF(t, 1)
		root := t.TempDir()
		for _, reader := range pdfReaders(t, root, "scan.pdf", data, MaxReadFileSize) {
			res := reader.exec(context.Background(), map[string]any{"path": "scan.pdf", "offset": 0, "length": 6})
			if !res.IsError {
				t.Fatalf("%s: offset/length on a PDF without text must error (E3):\n%s", reader.name, res.ForLLM)
			}
			if len(res.InspectionPDFs) != 0 {
				t.Fatalf("%s: the E3 error must not carry an InspectionPDFs item, got %d", reader.name, len(res.InspectionPDFs))
			}
		}
	})

	t.Run("offset/length on an unparseable pdf: the E2 error, no item", func(t *testing.T) {
		// FR-8: "With offset/length: ... not parseable → E2".
		data := buildEncryptedPDF(t)
		root := t.TempDir()
		for _, reader := range pdfReaders(t, root, "secure.pdf", data, MaxReadFileSize) {
			res := reader.exec(context.Background(), map[string]any{"path": "secure.pdf", "offset": 0, "length": 6})
			if !res.IsError {
				t.Fatalf("%s: offset/length on an unparseable PDF must error (E2):\n%s", reader.name, res.ForLLM)
			}
			if len(res.InspectionPDFs) != 0 {
				t.Fatalf("%s: the E2 error must not carry an InspectionPDFs item, got %d", reader.name, len(res.InspectionPDFs))
			}
		}
	})

	t.Run("InspectionPDFs is excluded from the marshalled result (json:\"-\")", func(t *testing.T) {
		data := buildTextPDF(t, "1.4", 1, true, nil)
		res := &ToolResult{
			ForLLM:         "inspected",
			InspectionPDFs: []InspectionPDF{{Bytes: data, Name: "report.pdf", Pages: 1, HasText: true}},
		}
		blob, err := json.Marshal(res)
		if err != nil {
			t.Fatalf("marshal ToolResult: %v", err)
		}
		s := string(blob)
		if strings.Contains(s, "InspectionPDF") {
			t.Fatalf("marshalled ToolResult must not expose InspectionPDFs:\n%s", s)
		}
		if strings.Contains(s, base64.StdEncoding.EncodeToString(data)) {
			t.Fatalf("marshalled ToolResult must not leak the PDF bytes in any form:\n%s", clip(s))
		}
	})
}

// TestReadFile_PDFItem_PaginationNoItem (FR-8, B) — with offset/length the
// result is text pages only: no InspectionPDFs item at all, and exactly the
// requested text window (F14: no extra text beyond the window).
func TestReadFile_PDFItem_PaginationNoItem(t *testing.T) {
	data := buildTextPDF(t, "1.4", 2, true, nil)
	root := t.TempDir()
	for _, reader := range pdfReaders(t, root, "report.pdf", data, MaxReadFileSize) {
		full := reader.exec(context.Background(), map[string]any{"path": "report.pdf"})
		assert.False(t, full.IsError)
		assert.Regexp(t, `^\n?PAGE 1 text\nPAGE 2 text\n?$`, pdfTextBody(full.ForLLM), "complete fixture text without imposing newline normalization")
		res := reader.exec(context.Background(), map[string]any{"path": "report.pdf", "offset": 0, "length": 6})
		if res.IsError {
			t.Fatalf("%s: paged text-PDF read errored: %s", reader.name, res.ForLLM)
		}
		if len(res.InspectionPDFs) != 0 {
			t.Fatalf("%s: offset/length must not produce an InspectionPDFs item (FR-8), got %d", reader.name, len(res.InspectionPDFs))
		}
		// N7/F14: exactly six characters of the complete extraction below,
		// including any extractor newline. No eleven-character phrase or
		// page-2 suffix may escape this window.
		if strings.Contains(res.ForLLM, "PAGE 1 text") {
			t.Fatalf("%s: the six-character window must not leak past char 5 ('PAGE 1 text' is 11 chars):\n%s", reader.name, res.ForLLM)
		}
		if strings.Contains(res.ForLLM, "PAGE 2") {
			t.Fatalf("%s: paged result must not leak page 2:\n%s", reader.name, res.ForLLM)
		}
		// The header states the REQUESTED range values (offset 0 — N7).
		pdfAssertPagedWindow(t, reader.name+"/paged", full.ForLLM, res.ForLLM, "report.pdf", 2, 0, 6)
		shifted := reader.exec(context.Background(), map[string]any{"path": "report.pdf", "offset": 5, "length": 5})
		assert.False(t, shifted.IsError)
		assert.Empty(t, shifted.InspectionPDFs)
		pdfAssertPagedWindow(t, reader.name+"/shifted", full.ForLLM, shifted.ForLLM, "report.pdf", 2, 5, 5)
	}
}
