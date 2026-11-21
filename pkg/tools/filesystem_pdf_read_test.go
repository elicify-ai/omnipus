package tools

import (
	"bytes"
	"compress/zlib"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// The PDF fixture below is duplicated from pkg/docextract/extract_pdf_test.go
// (test helpers cannot be imported across packages, and a shared non-test
// package would be production code). Keep the two in lock-step: one-page PDF,
// Type0/Identity-H font with a ToUnicode CMap, FlateDecode streams — so the
// raw bytes never contain the readable text and read_file can only return the
// text by routing through document extraction.

func pdfFlateEncode(b []byte) []byte {
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	_, _ = zw.Write(b)
	_ = zw.Close()
	return buf.Bytes()
}

// makeSyntheticPDF builds a one-page PDF that displays text via a
// Type0/Identity-H font with a ToUnicode CMap and FlateDecode streams.
// nulFreeHeader=true pads the header comment line so the first 512 bytes of
// the file contain no NUL byte — the real-world shape issue #1959 is about
// (ASCII header and objects before any compressed stream). nulFreeHeader=false
// writes exactly one literal NUL inside the header comment line instead, so
// the sniffed 512-byte prefix does contain a NUL.
//
// Spec source: issue #1959 required behaviour items 1, 2 and 3.
func makeSyntheticPDF(t *testing.T, nulFreeHeader bool, text string) []byte {
	t.Helper()

	runes := []rune(text)
	// glyph ids 1..n for distinct runes
	gid := map[rune]int{}
	var order []rune
	for _, r := range runes {
		if _, ok := gid[r]; !ok {
			gid[r] = len(gid) + 1
			order = append(order, r)
		}
	}
	var hex strings.Builder
	for _, r := range runes {
		fmt.Fprintf(&hex, "%04X", gid[r])
	}
	content := []byte(fmt.Sprintf("BT /F1 12 Tf 72 700 Td <%s> Tj ET", hex.String()))

	var cm strings.Builder
	cm.WriteString("/CIDInit /ProcSet findresource begin 12 dict begin begincmap\n")
	cm.WriteString("/CMapName /Adobe-Identity-UCS def /CMapType 2 def\n")
	cm.WriteString("1 begincodespacerange <0000> <FFFF> endcodespacerange\n")
	fmt.Fprintf(&cm, "%d beginbfchar\n", len(order))
	for _, r := range order {
		fmt.Fprintf(&cm, "<%04X> <%04X>\n", gid[r], r)
	}
	cm.WriteString("endbfchar\nendcmap CMapName currentdict /CMap defineresource pop end end\n")

	var header string
	if nulFreeHeader {
		// 700 ASCII comment bytes: the first 512 bytes of the file are all
		// comment and contain no NUL.
		header = "%" + strings.Repeat("x", 700) + "\n"
	} else {
		// One literal NUL at byte offset 9+1+256=266, inside the comment line
		// (comment bytes up to end-of-line are ignored by a PDF reader).
		header = "%" + strings.Repeat("x", 256) + "\x00" + strings.Repeat("x", 256) + "\n"
	}

	var out bytes.Buffer
	var offs []int
	out.WriteString("%PDF-1.7\n" + header)
	obj := func(body string) {
		offs = append(offs, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", len(offs), body)
	}
	stream := func(dict string, data []byte) {
		offs = append(offs, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n<<%s/Filter/FlateDecode/Length %d>>\nstream\n", len(offs), dict, len(data))
		out.Write(data)
		out.WriteString("\nendstream\nendobj\n")
	}
	obj("<</Type/Catalog/Pages 2 0 R>>")
	obj("<</Type/Pages/Count 1/Kids[3 0 R]>>")
	obj("<</Type/Page/Parent 2 0 R/MediaBox[0 0 612 792]/Contents 4 0 R/Resources<</Font<</F1 5 0 R>>>>>>")
	stream("", pdfFlateEncode(content))
	obj("<</Type/Font/Subtype/Type0/BaseFont/AAAAAA+Test/Encoding/Identity-H/DescendantFonts[6 0 R]/ToUnicode 7 0 R>>")
	obj("<</Type/Font/Subtype/CIDFontType2/BaseFont/AAAAAA+Test/CIDSystemInfo<</Registry(Adobe)/Ordering(Identity)/Supplement 0>>/DW 600>>")
	stream("", pdfFlateEncode([]byte(cm.String())))
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(offs)+1)
	for _, o := range offs {
		fmt.Fprintf(&out, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&out, "trailer\n<</Size %d/Root 1 0 R>>\nstartxref\n%d\n%%%%EOF\n", len(offs)+1, xref)
	return out.Bytes()
}

// requireHeaderNULs is an instrument guard: it verifies the fixture actually
// has (or has not) a NUL byte within the sniffed 512-byte prefix, so a test
// can never silently exercise the wrong detection branch.
func requireHeaderNULs(t *testing.T, pdf []byte, wantNUL bool) {
	t.Helper()
	head := pdf[:min(len(pdf), 512)]
	if got := bytes.Contains(head, []byte{0}); got != wantNUL {
		t.Fatalf("fixture broken: NUL in first 512 bytes = %v, want %v", got, wantNUL)
	}
}

// venuePDFText is the text the synthetic PDF displays; issue #1959 item 4
// fixes its length at 38 characters with "Grand" at character offset 7.
const venuePDFText = "Venue: Grand Hotel Amsterdam, 12 March"

// assertExtractedPDFPage asserts the full documented extraction contract of
// issue #1959 item 1: a [document: ...] header carrying the marker and the
// character range "a-b of 38", the page body as the exact tail of the output,
// and none of the raw PDF syntax ever leaking through as ordinary text.
func assertExtractedPDFPage(t *testing.T, out, displayName, headerRange, body string) {
	t.Helper()
	wantHeader := fmt.Sprintf("[document: %s | extracted text | %s]", displayName, headerRange)
	if !strings.Contains(out, wantHeader) {
		t.Fatalf("expected document header %q, got:\n%s", wantHeader, out)
	}
	if !strings.HasSuffix(out, "\n\n"+body) {
		t.Fatalf("expected extracted page body %q as the output tail, got:\n%s", body, out)
	}
	for _, banned := range []string{"%PDF-", "stream", "endobj", "FlateDecode", "[file: "} {
		if strings.Contains(out, banned) {
			t.Fatalf("raw PDF syntax leaked into output (%q found):\n%s", banned, out)
		}
	}
	if strings.ContainsRune(out, '�') {
		t.Fatalf("U+FFFD replacement rune in output:\n%s", out)
	}
}

// TestReadFile_PDFWithoutNULInHeaderReturnsExtractedText — issue #1959 item 1
// (the bug): read_file on a valid PDF whose first 512 bytes contain no NUL
// must return the extracted text under the [document: ...] header, never the
// raw bytes (%PDF- header, objects, compressed streams). Fails today because
// filesystem.go's readOpenFile only routes to extraction when the sniffed
// prefix contains a NUL byte.
func TestReadFile_PDFWithoutNULInHeaderReturnsExtractedText(t *testing.T) {
	ws := t.TempDir()
	pdf := makeSyntheticPDF(t, true, venuePDFText)
	requireHeaderNULs(t, pdf, false)
	path := writeWorkspaceFile(t, ws, "invitation.pdf", pdf)

	tool := NewReadFileTool(ws, false, MaxReadFileSize)
	res := tool.Execute(context.Background(), map[string]any{"path": path})

	if res.IsError {
		t.Fatalf("expected PDF extraction to succeed, got error: %s", res.ForLLM)
	}
	assertExtractedPDFPage(t, res.ForLLM, "invitation.pdf", "chars 0-38 of 38", venuePDFText)
}

// TestReadFile_PDFWithNULInHeaderReturnsExtractedText — issue #1959 item 2:
// the pre-existing NUL-byte routing must keep extracting after any fix to the
// NUL-free path. Green today (existing behaviour); it guards the fix from
// regressing the branch that already works.
func TestReadFile_PDFWithNULInHeaderReturnsExtractedText(t *testing.T) {
	ws := t.TempDir()
	pdf := makeSyntheticPDF(t, false, venuePDFText)
	requireHeaderNULs(t, pdf, true)
	path := writeWorkspaceFile(t, ws, "spec-nul.pdf", pdf)

	tool := NewReadFileTool(ws, false, MaxReadFileSize)
	res := tool.Execute(context.Background(), map[string]any{"path": path})

	if res.IsError {
		t.Fatalf("expected PDF extraction to succeed, got error: %s", res.ForLLM)
	}
	assertExtractedPDFPage(t, res.ForLLM, "spec-nul.pdf", "chars 0-38 of 38", venuePDFText)
}

// TestReadFile_PDFSignatureDecidesNotFilename — issue #1959 item 3: the %PDF-
// signature decides, not the file extension. A NUL-free PDF named .bin (or
// with no extension at all) must be extracted like any PDF. Fails today for
// the same NUL-sniff reason as item 1.
func TestReadFile_PDFSignatureDecidesNotFilename(t *testing.T) {
	for _, name := range []string{"invitation.bin", "invitation"} {
		t.Run(name, func(t *testing.T) {
			ws := t.TempDir()
			pdf := makeSyntheticPDF(t, true, venuePDFText)
			requireHeaderNULs(t, pdf, false)
			path := writeWorkspaceFile(t, ws, name, pdf)

			tool := NewReadFileTool(ws, false, MaxReadFileSize)
			res := tool.Execute(context.Background(), map[string]any{"path": path})

			if res.IsError {
				t.Fatalf("expected signature-routed PDF extraction to succeed, got error: %s", res.ForLLM)
			}
			assertExtractedPDFPage(t, res.ForLLM, name, "chars 0-38 of 38", venuePDFText)
		})
	}
}

// TestReadFile_PDFPaginationUsesCharacterOffsets — issue #1959 item 4: offset
// and length are CHARACTER positions into the extracted text, not file bytes.
// With the fixed text "Venue: Grand Hotel Amsterdam, 12 March" (38 chars),
// offset=7 length=5 returns exactly "Grand" under a "chars 7-12 of 38" header
// with a TRUNCATED/continue hint; the final page ends the document without a
// truncation hint; an offset past the end yields END OF DOCUMENT.
// Fails today: the NUL-free PDF is never extracted, so no character pagination
// exists for it at all.
func TestReadFile_PDFPaginationUsesCharacterOffsets(t *testing.T) {
	ws := t.TempDir()
	pdf := makeSyntheticPDF(t, true, venuePDFText)
	requireHeaderNULs(t, pdf, false)
	path := writeWorkspaceFile(t, ws, "invitation.pdf", pdf)

	tool := NewReadFileTool(ws, false, MaxReadFileSize)

	t.Run("mid page returns the requested character window", func(t *testing.T) {
		res := tool.Execute(context.Background(), map[string]any{
			"path": path, "offset": float64(7), "length": float64(5),
		})
		if res.IsError {
			t.Fatalf("mid page errored: %s", res.ForLLM)
		}
		assertExtractedPDFPage(t, res.ForLLM, "invitation.pdf", "chars 7-12 of 38", "Grand")
		if !strings.Contains(res.ForLLM, "TRUNCATED") || !strings.Contains(res.ForLLM, "offset=12") {
			t.Fatalf("expected TRUNCATED hint with continue offset=12, got:\n%s", res.ForLLM)
		}
	})

	t.Run("final page ends the document", func(t *testing.T) {
		res := tool.Execute(context.Background(), map[string]any{
			"path": path, "offset": float64(12), "length": float64(100),
		})
		if res.IsError {
			t.Fatalf("final page errored: %s", res.ForLLM)
		}
		assertExtractedPDFPage(t, res.ForLLM, "invitation.pdf", "chars 12-38 of 38", " Hotel Amsterdam, 12 March")
		if strings.Contains(res.ForLLM, "TRUNCATED") {
			t.Fatalf("final page must not advertise truncation, got:\n%s", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "END OF DOCUMENT") {
			t.Fatalf("expected end-of-document marker, got:\n%s", res.ForLLM)
		}
	})

	t.Run("offset past the end yields end of document", func(t *testing.T) {
		res := tool.Execute(context.Background(), map[string]any{
			"path": path, "offset": float64(1000), "length": float64(5),
		})
		if res.IsError {
			t.Fatalf("past-end page errored: %s", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "chars 38-38 of 38") {
			t.Fatalf("expected clamped header chars 38-38 of 38, got:\n%s", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "END OF DOCUMENT") {
			t.Fatalf("expected end-of-document marker, got:\n%s", res.ForLLM)
		}
	})
}

// TestReadFile_PDFWithNoExtractableTextErrors — issue #1959 item 5: a PDF
// that yields no text must return IsError=true with the structured
// "binary file detected: could not extract document text (...)" message —
// never raw PDF syntax or stream bytes as ordinary text. Both fixtures are
// NUL-free, which is exactly the path that today falls through to the raw
// read (fails today).
func TestReadFile_PDFWithNoExtractableTextErrors(t *testing.T) {
	t.Run("corrupt garbage after the header", func(t *testing.T) {
		ws := t.TempDir()
		garbage := []byte("%PDF-1.7\n" + strings.Repeat("not a pdf object at all ", 32))
		requireHeaderNULs(t, garbage, false)
		path := writeWorkspaceFile(t, ws, "corrupt.pdf", garbage)

		tool := NewReadFileTool(ws, false, MaxReadFileSize)
		res := tool.Execute(context.Background(), map[string]any{"path": path})

		if !res.IsError {
			t.Fatalf("expected corrupt PDF to error, got ordinary output:\n%s", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "could not extract document text") {
			t.Fatalf("expected extraction-failure message, got: %s", res.ForLLM)
		}
		if strings.Contains(res.ForLLM, "not a pdf object at all") {
			t.Fatalf("raw file content echoed back in the error:\n%s", res.ForLLM)
		}
	})

	t.Run("valid structure but empty text stream", func(t *testing.T) {
		ws := t.TempDir()
		pdf := makeSyntheticPDF(t, true, "")
		requireHeaderNULs(t, pdf, false)
		path := writeWorkspaceFile(t, ws, "empty-text.pdf", pdf)

		tool := NewReadFileTool(ws, false, MaxReadFileSize)
		res := tool.Execute(context.Background(), map[string]any{"path": path})

		if !res.IsError {
			t.Fatalf("expected text-less PDF to error, got ordinary output:\n%s", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "could not extract document text") {
			t.Fatalf("expected extraction-failure message, got: %s", res.ForLLM)
		}
		for _, banned := range []string{"%PDF-", "stream", "endobj", "FlateDecode"} {
			if strings.Contains(res.ForLLM, banned) {
				t.Fatalf("raw PDF syntax leaked into output (%q found):\n%s", banned, res.ForLLM)
			}
		}
	})
}

// TestReadFile_PDFSignatureMidLineIsStillPlainText — issue #1959 item 6 (first
// half): a .txt file that merely CONTAINS the signature mid-line (byte 0 is
// ordinary text) is still plain text under the [file: ...] header. Pins the
// false-positive bound of the signature check: green today, and must stay
// green after the fix (a fix that routes every sniffed file through
// extraction, or that matches the signature anywhere but byte 0, dies here).
func TestReadFile_PDFSignatureMidLineIsStillPlainText(t *testing.T) {
	ws := t.TempDir()
	content := []byte("The wire format \"%PDF- is the signature of a PDF\" begins every PDF file.\n")
	if bytes.HasPrefix(content, []byte("%PDF-")) {
		t.Fatal("fixture broken: content must not START with the signature (that is item 8's case)")
	}
	path := writeWorkspaceFile(t, ws, "plain_notes.txt", content)

	tool := NewReadFileTool(ws, false, MaxReadFileSize)
	res := tool.Execute(context.Background(), map[string]any{"path": path})

	if res.IsError {
		t.Fatalf("expected plain text read, got error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "[file: plain_notes.txt | total:") {
		t.Fatalf("expected plain-text file header, got:\n%s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, `"%PDF- is the signature of a PDF"`) {
		t.Fatalf("expected the full line with the mid-line signature, got:\n%s", res.ForLLM)
	}
}

// TestReadFile_PDFExtensionWithPlainTextErrors — issue #1959 item 6 (second
// half): a text file named x.pdf says PDF by extension; extraction fails, so
// read_file must return the structured error rather than silently falling back
// to raw text. Fails today: the file is NUL-free, so the plain-text path
// returns "not a pdf" as ordinary content.
func TestReadFile_PDFExtensionWithPlainTextErrors(t *testing.T) {
	ws := t.TempDir()
	path := writeWorkspaceFile(t, ws, "x.pdf", []byte("not a pdf\n"))

	tool := NewReadFileTool(ws, false, MaxReadFileSize)
	res := tool.Execute(context.Background(), map[string]any{"path": path})

	if !res.IsError {
		t.Fatalf("extension says PDF and extraction failed: expected a structured error, got ordinary output:\n%s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "could not extract document text") {
		t.Fatalf("expected extraction-failure message, got: %s", res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "not a pdf\n") {
		t.Fatalf("raw file content leaked back as text instead of the structured error:\n%s", res.ForLLM)
	}
}

// TestReadFile_TextFileStartingWithPDFSignatureErrors — issue #1959 item 8
// (decided behaviour): a plain text file whose content merely STARTS with
// "%PDF-" but has no PDF objects is NOT returned as raw text and is not lost
// silently: it returns a structured error saying the text could not be
// extracted, without echoing the file's raw lines. Fails today: no NUL byte,
// so the plain-text path returns the content verbatim.
func TestReadFile_TextFileStartingWithPDFSignatureErrors(t *testing.T) {
	ws := t.TempDir()
	path := writeWorkspaceFile(t, ws, "notes.txt", []byte("%PDF- notes for tomorrow\nbuy milk\n"))

	tool := NewReadFileTool(ws, false, MaxReadFileSize)
	res := tool.Execute(context.Background(), map[string]any{"path": path})

	if !res.IsError {
		t.Fatalf("expected a structured error for signature-prefixed non-PDF text, got ordinary output:\n%s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "could not extract document text") {
		t.Fatalf("expected extraction-failure message, got: %s", res.ForLLM)
	}
	for _, raw := range []string{"buy milk", "notes for tomorrow"} {
		if strings.Contains(res.ForLLM, raw) {
			t.Fatalf("raw file line %q echoed back instead of a clean structured error:\n%s", raw, res.ForLLM)
		}
	}
}

// TestReadFile_PDFPaginationCountsCharactersNotBytesForNonASCII pins that the
// offset is a character (rune) position when the extracted text holds
// multi-byte characters: in "Café Zürich 12 März" offset 5 / length 6 is
// "Zürich"; a byte offset would start one character early (#1959).
func TestReadFile_PDFPaginationCountsCharactersNotBytesForNonASCII(t *testing.T) {
	ws := t.TempDir()
	pdf := makeSyntheticPDF(t, true, "Café Zürich 12 März")
	requireHeaderNULs(t, pdf, false)
	path := writeWorkspaceFile(t, ws, "unicode.pdf", pdf)

	tool := NewReadFileTool(ws, false, MaxReadFileSize)
	res := tool.Execute(context.Background(), map[string]any{
		"path": path, "offset": float64(5), "length": float64(6),
	})
	if res.IsError {
		t.Fatalf("unicode page errored: %s", res.ForLLM)
	}
	assertExtractedPDFPage(t, res.ForLLM, "unicode.pdf", "chars 5-11 of 19", "Zürich")
}

// trailingPDFSuffixes are the boundary suffixes the trailing-whitespace
// regression pins. Exactly 100 trailing LF bytes made the underlying PDF
// parser panic with "index out of range [-1]" in its end-of-file trim loop
// (CHECK finding on issue #1959); 99/101 and the CR variant pin the boundary
// so a one-off guard cannot sneak past.
// Duplicated from pkg/docextract/extract_pdf_test.go (test helpers cannot be
// imported across packages); keep the two in lock-step.
var trailingPDFSuffixes = []struct {
	name   string
	suffix []byte
}{
	{"exactly 100 trailing LF", bytes.Repeat([]byte{'\n'}, 100)},
	{"99 trailing LF", bytes.Repeat([]byte{'\n'}, 99)},
	{"101 trailing LF", bytes.Repeat([]byte{'\n'}, 101)},
	{"100 trailing CR", bytes.Repeat([]byte{'\r'}, 100)},
}

// makeSyntheticPDFWithSuffix builds the NUL-free synthetic venue PDF with a
// trailing whitespace run appended, asserting both instrument properties: the
// suffix really is the last bytes of the file, and the first 512 bytes stay
// NUL-free (so the file exercises the signature-routing branch, not the NUL
// branch). Duplicated from pkg/docextract/extract_pdf_test.go.
func makeSyntheticPDFWithSuffix(t *testing.T, suffix []byte) []byte {
	t.Helper()
	pdf := append(makeSyntheticPDF(t, true, venuePDFText), suffix...)
	if !bytes.HasSuffix(pdf, suffix) {
		t.Fatal("fixture broken: trailing suffix is not the file's tail")
	}
	requireHeaderNULs(t, pdf, false)
	return pdf
}

// assertNoRawPDFBytes asserts the error branch of a PDF read never leaks raw
// PDF bytes: any verbatim dump of the fixture carries all four banned markers
// (%PDF-, endstream, endobj, FlateDecode), so leak detection is complete even
// though the bare word "stream" is not banned here — a parser's reason string
// may legitimately contain it in prose.
func assertNoRawPDFBytes(t *testing.T, out string) {
	t.Helper()
	for _, banned := range []string{"%PDF-", "endstream", "endobj", "FlateDecode"} {
		if strings.Contains(out, banned) {
			t.Fatalf("raw PDF bytes leaked into the error (%q found):\n%s", banned, out)
		}
	}
}

// TestReadFile_PDFTrailingWhitespaceRunMustNotPanic — tools-level twin of the
// docextract trailing-whitespace regression: read_file on the venue PDF with a
// trailing whitespace run must return a ToolResult — either the extracted page
// (chars 0-38 of 38, full text, no raw syntax) or the structured
// "binary file detected: could not extract document text (...)" error, never
// raw %PDF-/stream bytes and never a panic ("index out of range [-1]" at
// exactly 100 trailing LF bytes). The deferred recover does NOT hide the
// panic: it converts it into a test failure carrying the panic value.
func TestReadFile_PDFTrailingWhitespaceRunMustNotPanic(t *testing.T) {
	for _, tc := range trailingPDFSuffixes {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("read_file panicked (%s): %v", tc.name, r)
				}
			}()

			ws := t.TempDir()
			pdf := makeSyntheticPDFWithSuffix(t, tc.suffix)
			path := writeWorkspaceFile(t, ws, "trailing-whitespace.pdf", pdf)

			tool := NewReadFileTool(ws, false, MaxReadFileSize)
			res := tool.Execute(context.Background(), map[string]any{"path": path})

			if res.IsError {
				if !strings.Contains(res.ForLLM, "binary file detected: could not extract document text (") {
					t.Fatalf("expected the structured extraction error, got: %s", res.ForLLM)
				}
				assertNoRawPDFBytes(t, res.ForLLM)
				return
			}
			assertExtractedPDFPage(t, res.ForLLM, "trailing-whitespace.pdf", "chars 0-38 of 38", venuePDFText)
		})
	}
}

// --- cyclic page tree (parser hang, CHECK round-2 finding on #1959) ----------

// makeCyclicPageTreePDF builds a small valid-structure PDF whose page tree is
// self-referencing (the Pages node's /Kids lists the node itself) — the shape
// that makes the underlying parser's page walk loop forever inside
// GetPlainText. Duplicated from pkg/docextract/extract_pdf_test.go (test
// helpers cannot be imported across packages); keep the two in lock-step.
func makeCyclicPageTreePDF(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	var offs []int
	out.WriteString("%PDF-1.7\n%" + strings.Repeat("x", 700) + "\n")
	obj := func(body string) {
		offs = append(offs, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", len(offs), body)
	}
	obj("<</Type/Catalog/Pages 2 0 R>>")
	// The hazard: the Pages node lists ITSELF as its only kid.
	obj("<</Type/Pages/Count 1/Kids [2 0 R]>>")
	obj("<</Type/Page/Parent 2 0 R/MediaBox [0 0 612 792]/Resources<</Font<</F1 4 0 R>>>>/Contents 5 0 R>>")
	obj("<</Type/Font/Subtype/Type1/BaseFont/Helvetica/Encoding/WinAnsiEncoding>>")
	content := "BT /F1 12 Tf 72 700 Td (Cyclic page tree text) Tj ET"
	obj(fmt.Sprintf("<</Length %d>>\nstream\n%s\nendstream", len(content), content))
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(offs)+1)
	for _, o := range offs {
		fmt.Fprintf(&out, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&out, "trailer\n<</Size %d/Root 1 0 R>>\nstartxref\n%d\n%%%%EOF\n", len(offs)+1, xref)

	pdf := out.Bytes()
	// Instrument guard: the self-reference IS the hazard (see the docextract twin).
	if !bytes.Contains(pdf, []byte("/Kids [2 0 R]")) {
		t.Fatal("fixture broken: page tree is not self-referencing")
	}
	requireHeaderNULs(t, pdf, false)
	return pdf
}

// TestReadFile_PDFCyclicPageTreeReturnsStructuredErrorNotHang — tools-level
// guard for the cyclic page-tree hang: read_file must return a ToolResult —
// the structured "could not extract document text" error (or, if the parser
// is ever taught to tolerate the cycle, the extracted page) — and must never
// hang forever.
//
// This test drives the DEFAULT pdfExtractTimeout (15s) because package tools
// cannot reach docextract's unexported budget, so passing runs can take up to
// that budget; the 25s watch-dog below only fires on a true hang (the parser
// loop, which no clean timeout would end). Skipped under -short for the same
// reason.
func TestReadFile_PDFCyclicPageTreeReturnsStructuredErrorNotHang(t *testing.T) {
	if testing.Short() {
		t.Skip("default-budget wait (up to 15s) — skipped under -short")
	}
	ws := t.TempDir()
	path := writeWorkspaceFile(t, ws, "cyclic-pages.pdf", makeCyclicPageTreePDF(t))
	tool := NewReadFileTool(ws, false, MaxReadFileSize)

	done := make(chan *ToolResult, 1)
	go func() { done <- tool.Execute(context.Background(), map[string]any{"path": path}) }()

	var res *ToolResult
	select {
	case res = <-done:
	case <-time.After(25 * time.Second):
		t.Fatalf("read_file did not return within 25s — page-tree walk stuck in an infinite loop")
	}

	if res.IsError {
		if !strings.Contains(res.ForLLM, "could not extract document text") {
			t.Fatalf("expected the structured extraction error, got: %s", res.ForLLM)
		}
		assertNoRawPDFBytes(t, res.ForLLM)
		return
	}
	assertExtractedPDFPage(t, res.ForLLM, "cyclic-pages.pdf", "chars 0-21 of 21", "Cyclic page tree text")
}
