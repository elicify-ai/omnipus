// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package docextract

// Review FIX 2 regression tests: pkg/docextract/extract.go's extractPptx and
// extractXlsx used to swallow the shared 25 MiB archive-decompression-budget
// error identically to a genuine per-slide/per-sheet parse error — the
// budget-exceeded case discarded whatever partial text had been collected
// for the entry that hit the wall, kept iterating remaining entries against
// an already-exhausted budget, and returned ok=true with NO truncation
// marker. Consumers (pkg/agent/loop_media.go, pkg/tools/filesystem.go) gate
// purely on ok, so the model reasoned from partial content believing it was
// complete. The fix threads an explicit archiveReadBudget.exceeded flag
// (set only when a read is actually rejected because more data remained
// beyond the cap — NOT merely when the budget counter reaches zero, since
// an exact-fit read legitimately lands there too) through to both
// extractors, which now append the file's established "[truncated: ...]"
// notice (matching the convention already used by the gzip/tar paths) and
// distinguish "budget exhausted before any content" from "genuinely no
// text content" in the ok=false reason string.
//
// Test inventory:
//
//	TestArchiveBudgetReader_ExactFit_DoesNotMarkExceeded  — budget runs out exactly at real EOF: not truncation
//	TestArchiveBudgetReader_MoreDataThanBudget_MarksExceeded — real data remains beyond the cap: IS truncation
//	TestExtractPptx_BudgetExhaustion_MarksTruncation      — 2 slides, budget covers only slide 1 → ok=true, slide1 text + truncation marker, slide2 absent
//	TestExtractPptx_BudgetExhaustedBeforeAnyContent_ReturnsDistinctReason — budget too small even for slide 1 → ok=false with a budget-specific reason, not the generic "no text content found"
//	TestExtractPptx_GenuinelyEmpty_NoFalsePositiveTruncation — ample budget, slide has no text → ok=false "no text content found" (unchanged, no truncation mislabel)
//	TestExtractXlsx_BudgetExhaustion_MarksTruncation      — mirrors the pptx truncation-marker case for sheets
//	TestExtractXlsx_BudgetExhaustedBeforeAnyContent_ReturnsDistinctReason — mirrors the pptx exhausted-before-content case
//
// Review FIX 1 regression tests: extractDocx/parseDocxXML never received the
// FIX 2 treatment at all — parseDocxXML discarded any partial text already
// collected in sb and returned a generic "docx: xml decode error" on ANY
// dec.Token() failure, including one caused by the shared budget wall. A
// .docx has only one text-bearing archive entry (word/document.xml, unlike
// pptx/xlsx's many slides/sheets), so the budget is checked once inside
// parseDocxXML's own token loop rather than per-entry.
//
//	TestExtractDocx_BudgetExhaustion_MarksTruncation      — budget covers only paragraph 1 → ok=true, paragraph 1 text preserved + truncation marker, paragraph 2 absent
//	TestExtractDocx_BudgetExhaustedBeforeAnyContent_ReturnsDistinctReason — budget too small even for the XML prolog → ok=false with a budget-specific reason
//	TestExtractDocx_GenuinelyEmpty_NoFalsePositiveTruncation — ample budget, document has no text → ok=false "docx: no text content found" (unchanged)
//	TestExtractDocx_GenuineXMLError_UnrelatedToBudget_StillReportsDecodeError — malformed XML with an ample budget must still report the original decode-error reason, not a truncation notice

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

// ── Low-level archiveReadBudget/archiveBudgetReader invariant ───────────────

// TestArchiveBudgetReader_ExactFit_DoesNotMarkExceeded pins the subtlety the
// fix relies on: when the budget's remaining count reaches exactly zero at
// the same moment the underlying reader legitimately runs out of real data
// (an exact-fit read), that is NOT truncation — the probe-read at
// remaining<=0 observes n==0 (real EOF), not n>0, so exceeded must stay
// false. Without this distinction, `remaining <= 0` alone would be a false
// truncation signal for the common case of "the archive fit exactly within
// budget."
func TestArchiveBudgetReader_ExactFit_DoesNotMarkExceeded(t *testing.T) {
	data := []byte("hello budget")
	budget := &archiveReadBudget{remaining: int64(len(data))}
	r := budget.reader(bytes.NewReader(data))
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: unexpected error %v", err)
	}
	if string(got) != string(data) {
		t.Fatalf("got %q, want %q", got, data)
	}
	if budget.exceeded {
		t.Fatal("exact-fit read must NOT set budget.exceeded")
	}
}

// TestArchiveBudgetReader_MoreDataThanBudget_MarksExceeded is the converse:
// when real data remains beyond the budget cap, the reader must return
// errArchiveDecompressionLimit AND set budget.exceeded so callers that
// otherwise swallow the error (extractPptx, extractXlsx) can still detect
// the truncation.
func TestArchiveBudgetReader_MoreDataThanBudget_MarksExceeded(t *testing.T) {
	data := []byte("hello budget, more than five bytes")
	budget := &archiveReadBudget{remaining: 5}
	r := budget.reader(bytes.NewReader(data))
	_, err := io.ReadAll(r)
	if !errors.Is(err, errArchiveDecompressionLimit) {
		t.Fatalf("expected errArchiveDecompressionLimit, got %v", err)
	}
	if !budget.exceeded {
		t.Fatal("expected budget.exceeded=true when more data remains beyond the cap")
	}
}

// ── extractPptx ───────────────────────────────────────────────────────────

// pptxSlideXML renders the minimal slide XML template makePptx uses, for
// tests that need precise control over multiple slides' exact byte length
// (the zip entries are stored with Deflate, but f.Open() yields exactly the
// original uncompressed bytes on read, so the budget math below is exact).
func pptxSlideXML(text string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>` +
		`<p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">` +
		`<p:cSld><p:spTree><p:sp><p:txBody>` +
		`<a:p><a:r><a:t>` + text + `</a:t></a:r></a:p>` +
		`</p:txBody></p:sp></p:spTree></p:cSld></p:sld>`
}

// buildPptxZipReader writes each slide (in order) to ppt/slides/slideN.xml
// and returns the opened *zip.Reader.
func buildPptxZipReader(t *testing.T, slides ...string) *zip.Reader {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i, xml := range slides {
		w, err := zw.Create(fmt.Sprintf("ppt/slides/slide%d.xml", i+1))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(xml)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	return zr
}

// TestExtractPptx_BudgetExhaustion_MarksTruncation is the core review-FIX-2
// regression test: slide 1 fits entirely within the budget and its text
// must survive; slide 2 starts only once the budget is already exhausted,
// so it must be entirely absent AND the result must carry the file's
// standard "[truncated: ...]" notice — not silently look complete.
func TestExtractPptx_BudgetExhaustion_MarksTruncation(t *testing.T) {
	slide1 := pptxSlideXML("Slide one has this exact recognizable text.")
	slide2 := pptxSlideXML("Slide two content that must never appear in the output.")
	zr := buildPptxZipReader(t, slide1, slide2)

	// Exactly the byte length of slide 1's raw XML: slide 1 can be read to
	// its own real EOF (exact fit, not truncation for slide 1 itself), but
	// nothing is left over for slide 2.
	budget := &archiveReadBudget{remaining: int64(len(slide1))}

	text, ok, reason := extractPptx(zr, budget)
	if !ok {
		t.Fatalf("expected ok=true (slide 1's content survives), reason=%q", reason)
	}
	if !strings.Contains(text, "Slide one has this exact recognizable text.") {
		t.Fatalf("expected slide 1 text preserved, got %q", text)
	}
	if strings.Contains(text, "Slide two content") {
		t.Fatalf("slide 2 must NOT appear (budget exhausted before it could be read), got %q", text)
	}
	if !strings.Contains(text, "[truncated:") {
		t.Fatalf("expected the file's standard truncation notice, got %q", text)
	}
	if !budget.exceeded {
		t.Fatal("expected budget.exceeded=true after slide 2 was skipped")
	}
}

// TestExtractPptx_BudgetExhaustedBeforeAnyContent_ReturnsDistinctReason
// covers the ok=false branch: when the budget is exhausted before ANY
// slide's text can be read, the reason string must name the budget
// specifically (distinguishing "truncated by budget" from "genuinely no
// content") rather than the generic "pptx: no text content found".
func TestExtractPptx_BudgetExhaustedBeforeAnyContent_ReturnsDistinctReason(t *testing.T) {
	slide1 := pptxSlideXML("This text is never reached.")
	zr := buildPptxZipReader(t, slide1)

	// A budget far too small for even one XML token.
	budget := &archiveReadBudget{remaining: 1}

	text, ok, reason := extractPptx(zr, budget)
	if ok {
		t.Fatalf("expected ok=false (no content survived the budget), got text=%q", text)
	}
	if !strings.Contains(reason, "budget") {
		t.Fatalf("expected reason to name the decompression budget, got %q", reason)
	}
	if !budget.exceeded {
		t.Fatal("expected budget.exceeded=true")
	}
}

// TestExtractPptx_GenuinelyEmpty_NoFalsePositiveTruncation is the guard
// against over-correction: a slide with ample budget but genuinely no
// extractable text must still report the original, unchanged
// "pptx: no text content found" reason — the fix must not mislabel an
// empty document as truncated.
func TestExtractPptx_GenuinelyEmpty_NoFalsePositiveTruncation(t *testing.T) {
	emptySlide := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">` +
		`<p:cSld><p:spTree></p:spTree></p:cSld></p:sld>`
	zr := buildPptxZipReader(t, emptySlide)
	budget := &archiveReadBudget{remaining: maxDecompressedBytes}

	_, ok, reason := extractPptx(zr, budget)
	if ok {
		t.Fatal("expected ok=false for a slide with no text content")
	}
	if reason != "pptx: no text content found" {
		t.Fatalf("expected the unchanged generic reason, got %q", reason)
	}
	if budget.exceeded {
		t.Fatal("ample budget must not be reported as exceeded")
	}
}

// ── extractXlsx ───────────────────────────────────────────────────────────

// xlsxSheetXML renders a minimal worksheet XML with one inline-string row,
// mirroring makeXlsx's template but as a standalone string so tests can
// measure its exact byte length for budget math.
func xlsxSheetXML(cellText string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>` +
		`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>` +
		`<row r="1"><c t="inlineStr"><is><t>` + cellText + `</t></is></c></row>` +
		`</sheetData></worksheet>`
}

func buildXlsxZipReader(t *testing.T, sheets ...string) *zip.Reader {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i, xml := range sheets {
		w, err := zw.Create(fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(xml)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	return zr
}

// TestExtractXlsx_BudgetExhaustion_MarksTruncation mirrors
// TestExtractPptx_BudgetExhaustion_MarksTruncation for worksheets: sheet 1
// fits entirely within budget, sheet 2 starts only once it is exhausted.
func TestExtractXlsx_BudgetExhaustion_MarksTruncation(t *testing.T) {
	sheet1 := xlsxSheetXML("SheetOneRecognizableCell")
	sheet2 := xlsxSheetXML("SheetTwoCellThatMustNeverAppear")
	zr := buildXlsxZipReader(t, sheet1, sheet2)

	budget := &archiveReadBudget{remaining: int64(len(sheet1))}

	text, ok, reason := extractXlsx(zr, budget)
	if !ok {
		t.Fatalf("expected ok=true (sheet 1's content survives), reason=%q", reason)
	}
	if !strings.Contains(text, "SheetOneRecognizableCell") {
		t.Fatalf("expected sheet 1 text preserved, got %q", text)
	}
	if strings.Contains(text, "SheetTwoCellThatMustNeverAppear") {
		t.Fatalf("sheet 2 must NOT appear (budget exhausted before it could be read), got %q", text)
	}
	if !strings.Contains(text, "[truncated:") {
		t.Fatalf("expected the file's standard truncation notice, got %q", text)
	}
	if !budget.exceeded {
		t.Fatal("expected budget.exceeded=true after sheet 2 was skipped")
	}
}

// TestExtractXlsx_BudgetExhaustedBeforeAnyContent_ReturnsDistinctReason
// mirrors the pptx ok=false/distinct-reason case for worksheets.
func TestExtractXlsx_BudgetExhaustedBeforeAnyContent_ReturnsDistinctReason(t *testing.T) {
	sheet1 := xlsxSheetXML("Never reached")
	zr := buildXlsxZipReader(t, sheet1)

	budget := &archiveReadBudget{remaining: 1}

	text, ok, reason := extractXlsx(zr, budget)
	if ok {
		t.Fatalf("expected ok=false (no content survived the budget), got text=%q", text)
	}
	if !strings.Contains(reason, "budget") {
		t.Fatalf("expected reason to name the decompression budget, got %q", reason)
	}
	if !budget.exceeded {
		t.Fatal("expected budget.exceeded=true")
	}
}

// ── extractDocx ───────────────────────────────────────────────────────────

// docxParagraphXML renders a minimal single-paragraph fragment of
// word/document.xml with precise, measurable byte length (mirroring
// pptxSlideXML/xlsxSheetXML's own rationale).
func docxParagraphXML(text string) string {
	return `<w:p><w:r><w:t>` + text + `</w:t></w:r></w:p>`
}

// docxDocumentXML wraps one or more paragraph fragments in the minimal
// word/document.xml envelope.
func docxDocumentXML(paragraphs ...string) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	sb.WriteString(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">`)
	sb.WriteString(`<w:body>`)
	for _, p := range paragraphs {
		sb.WriteString(p)
	}
	sb.WriteString(`</w:body></w:document>`)
	return sb.String()
}

// buildDocxZipReader writes documentXML to word/document.xml and returns the
// opened *zip.Reader.
func buildDocxZipReader(t *testing.T, documentXML string) *zip.Reader {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, writeErr := w.Write([]byte(documentXML)); writeErr != nil {
		t.Fatal(writeErr)
	}
	if closeErr := zw.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	return zr
}

// TestExtractDocx_BudgetExhaustion_MarksTruncation is the core review-FIX-1
// regression test: paragraph 1 (and everything before it) fits entirely
// within the budget and its text must survive; paragraph 2 starts only once
// the budget is already exhausted, so it must be entirely absent AND the
// result must carry the file's standard "[truncated: ...]" notice — not
// silently look complete, and not discard paragraph 1's text the way the
// pre-fix "docx: xml decode error" path used to.
func TestExtractDocx_BudgetExhaustion_MarksTruncation(t *testing.T) {
	para1 := docxParagraphXML("Paragraph one has this exact recognizable text.")
	para2 := docxParagraphXML("Paragraph two content that must never appear in the output.")
	fullDoc := docxDocumentXML(para1, para2)
	docThroughPara1 := docxDocumentXML(para1)
	// docxDocumentXML always appends the closing "</w:body></w:document>"
	// suffix; trim it back off so the budget lands exactly at the end of
	// paragraph 1's own closing tag, with nothing of the envelope's closer
	// counted early.
	docThroughPara1 = strings.TrimSuffix(docThroughPara1, `</w:body></w:document>`)
	zr := buildDocxZipReader(t, fullDoc)

	// Exactly the byte length of everything through paragraph 1's raw XML:
	// paragraph 1 can be fully tokenized (its closing </w:p> only needs
	// bytes already delivered), but nothing is left over to read paragraph
	// 2's opening tag.
	budget := &archiveReadBudget{remaining: int64(len(docThroughPara1))}

	text, ok, reason := extractDocx(zr, budget)
	if !ok {
		t.Fatalf("expected ok=true (paragraph 1's content survives), reason=%q", reason)
	}
	if !strings.Contains(text, "Paragraph one has this exact recognizable text.") {
		t.Fatalf("expected paragraph 1 text preserved, got %q", text)
	}
	if strings.Contains(text, "Paragraph two content") {
		t.Fatalf("paragraph 2 must NOT appear (budget exhausted before it could be read), got %q", text)
	}
	if !strings.Contains(text, "[truncated:") {
		t.Fatalf("expected the file's standard truncation notice, got %q", text)
	}
	if !budget.exceeded {
		t.Fatal("expected budget.exceeded=true after paragraph 2 was cut off")
	}
}

// TestExtractDocx_BudgetExhaustedBeforeAnyContent_ReturnsDistinctReason
// covers the ok=false branch: when the budget is exhausted before ANY
// paragraph text can be read, the reason string must name the budget
// specifically rather than the generic "docx: no text content found".
func TestExtractDocx_BudgetExhaustedBeforeAnyContent_ReturnsDistinctReason(t *testing.T) {
	para1 := docxParagraphXML("This text is never reached.")
	zr := buildDocxZipReader(t, docxDocumentXML(para1))

	// A budget far too small for even the XML prolog.
	budget := &archiveReadBudget{remaining: 1}

	text, ok, reason := extractDocx(zr, budget)
	if ok {
		t.Fatalf("expected ok=false (no content survived the budget), got text=%q", text)
	}
	if !strings.Contains(reason, "budget") {
		t.Fatalf("expected reason to name the decompression budget, got %q", reason)
	}
	if !budget.exceeded {
		t.Fatal("expected budget.exceeded=true")
	}
}

// TestExtractDocx_GenuinelyEmpty_NoFalsePositiveTruncation is the guard
// against over-correction: a document with ample budget but genuinely no
// extractable text must still report the original, unchanged
// "docx: no text content found" reason — the fix must not mislabel an empty
// document as truncated.
func TestExtractDocx_GenuinelyEmpty_NoFalsePositiveTruncation(t *testing.T) {
	zr := buildDocxZipReader(t, docxDocumentXML())
	budget := &archiveReadBudget{remaining: maxDecompressedBytes}

	_, ok, reason := extractDocx(zr, budget)
	if ok {
		t.Fatal("expected ok=false for a document with no text content")
	}
	if reason != "docx: no text content found" {
		t.Fatalf("expected the unchanged generic reason, got %q", reason)
	}
	if budget.exceeded {
		t.Fatal("ample budget must not be reported as exceeded")
	}
}

// TestExtractDocx_GenuineXMLError_UnrelatedToBudget_StillReportsDecodeError
// pins the sibling behavior parseDocxXML must keep: a genuinely malformed
// document (truncated mid-tag, nothing to do with the decompression budget)
// with an AMPLE budget must still return the original
// "docx: xml decode error: ..." reason, not a truncation notice — the fix
// only special-cases the budget-exceeded path, exactly like
// extractPptx/extractXlsx's own per-entry error handling only special-cases
// budget.exceeded rather than every possible decode error.
func TestExtractDocx_GenuineXMLError_UnrelatedToBudget_StillReportsDecodeError(t *testing.T) {
	malformed := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` +
		`<w:body><w:p><w:r><w:t>incomplete, no closing tags at all`
	zr := buildDocxZipReader(t, malformed)
	budget := &archiveReadBudget{remaining: maxDecompressedBytes}

	text, ok, reason := extractDocx(zr, budget)
	if ok {
		t.Fatalf("expected ok=false for malformed XML, got text=%q", text)
	}
	if !strings.HasPrefix(reason, "docx: xml decode error:") {
		t.Fatalf("expected the original decode-error reason, got %q", reason)
	}
	if strings.Contains(reason, "budget") {
		t.Fatalf("a genuine XML error unrelated to the budget must not mention it, got %q", reason)
	}
	if budget.exceeded {
		t.Fatal("ample budget must not be reported as exceeded for a genuine XML corruption")
	}
}
