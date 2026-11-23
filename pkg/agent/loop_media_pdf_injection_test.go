package agent

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeSyntheticPDF is the third copy of the round-1 fixture
// (pkg/docextract/extract_pdf_test.go, pkg/tools/filesystem_pdf_read_test.go;
// test helpers cannot be imported across packages — keep the three in
// lock-step): one-page PDF, Type0/Identity-H font with a ToUnicode CMap,
// FlateDecode streams. The compressed bytes never contain the readable text,
// so ONLY real extraction can satisfy the injection assertions below — a
// plain-text-layer PDF would leak its text in the raw bytes and fake a pass.
func makeSyntheticPDF(t *testing.T, nulFreeHeader bool, text string) []byte {
	t.Helper()

	runes := []rune(text)
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
		header = "%" + strings.Repeat("x", 700) + "\n"
	} else {
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

	pdf := out.Bytes()
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		t.Fatal("fixture broken: not a PDF")
	}
	return pdf
}

func pdfFlateEncode(b []byte) []byte {
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	_, _ = zw.Write(b)
	_ = zw.Close()
	return buf.Bytes()
}

// makeASCIIPDFBytes builds the classic all-ASCII minimal PDF (uncompressed
// Helvetica content stream) — the shape the round-4 CHECK probe used to
// demonstrate raw-PDF injection at this boundary: the text path accepts those
// bytes as valid UTF-8 and the injection wraps the whole syntax as Content.
// Fourth copy of the fixture family (docextract, tools, agent-Flate); keep
// them in lock-step.
func makeASCIIPDFBytes(t *testing.T, text string) []byte {
	t.Helper()
	var out bytes.Buffer
	var offs []int
	out.WriteString("%PDF-1.7\n%" + strings.Repeat("x", 700) + "\n")
	obj := func(body string) {
		offs = append(offs, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", len(offs), body)
	}
	obj("<</Type/Catalog/Pages 2 0 R>>")
	obj("<</Type/Pages/Count 1/Kids[3 0 R]>>")
	obj("<</Type/Page/Parent 2 0 R/MediaBox[0 0 612 792]/Contents 4 0 R/Resources<</Font<</F1 5 0 R>>>>>>")
	content := fmt.Sprintf("BT /F1 12 Tf 72 700 Td (%s) Tj ET", text)
	obj(fmt.Sprintf("<</Length %d>>\nstream\n%s\nendstream", len(content), content))
	obj("<</Type/Font/Subtype/Type1/BaseFont/Helvetica/Encoding/WinAnsiEncoding>>")
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(offs)+1)
	for _, o := range offs {
		fmt.Fprintf(&out, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&out, "trailer\n<</Size %d/Root 1 0 R>>\nstartxref\n%d\n%%%%EOF\n", len(offs)+1, xref)
	return out.Bytes()
}

// assertNoRawPDFInjection forbids any raw PDF syntax in a model-visible
// injection (round-4 CHECK finding F3's agent-side oracle: never "%PDF-" or
// "obj" — any verbatim dump of the fixture carries all of these markers).
func assertNoRawPDFInjection(t *testing.T, injection string) {
	t.Helper()
	for _, banned := range []string{"%PDF-", "obj", "endstream", "endobj", "xref", "FlateDecode"} {
		if strings.Contains(injection, banned) {
			t.Fatalf("raw PDF syntax injected as model content (%q found):\n%s", banned, injection)
		}
	}
}

// TestBuildDocumentInjection_PDFAttachmentWithTextMIMEExtractsText — round-4
// CHECK finding F3 at the upload boundary: an attachment named upload.pdf that
// arrives with Content-Type: text/plain (metadata MIME is preserved by the
// upload path, loop_media.go::detectMIME) must be PDF-extracted — the model
// sees the extracted text, never the raw "%PDF- ... obj ... xref ..." bytes.
// Today ExtractBytes honours the text MIME first and buildDocumentInjection
// wraps the raw bytes as ordinary Content — the whole test fails.
func TestBuildDocumentInjection_PDFAttachmentWithTextMIMEExtractsText(t *testing.T) {
	t.Run("extracted text replaces the raw syntax", func(t *testing.T) {
		const venueText = "Venue: Grand Hotel Amsterdam, 12 March"
		pdf := makeSyntheticPDF(t, true, venueText)
		path := filepath.Join(t.TempDir(), "upload.pdf")
		if err := os.WriteFile(path, pdf, 0o644); err != nil {
			t.Fatal(err)
		}

		injection := buildDocumentInjection(path, "text/plain", "upload.pdf", int64(len(pdf)))

		assertNoRawPDFInjection(t, injection)
		if !strings.Contains(injection, venueText) {
			t.Fatalf("expected the extracted document text in the injection, got:\n%s", injection)
		}
	})

	t.Run("ASCII PDF under text/plain does not inject raw syntax", func(t *testing.T) {
		// The exact CHECK round-4 leak shape: all-ASCII PDF bytes survive the
		// text path's UTF-8 check, so today the whole syntax is injected as
		// model content and assertNoRawPDFInjection fires below.
		const venueText = "Venue: Grand Hotel Amsterdam, 12 March"
		pdf := makeASCIIPDFBytes(t, venueText)
		path := filepath.Join(t.TempDir(), "upload.pdf")
		if err := os.WriteFile(path, pdf, 0o644); err != nil {
			t.Fatal(err)
		}

		injection := buildDocumentInjection(path, "text/plain", "upload.pdf", int64(len(pdf)))

		assertNoRawPDFInjection(t, injection)
		if !strings.Contains(injection, venueText) {
			t.Fatalf("expected the extracted document text in the injection, got:\n%s", injection)
		}
	})

	t.Run("text-less PDF yields the honest failure notice", func(t *testing.T) {
		pdf := makeSyntheticPDF(t, true, "")
		path := filepath.Join(t.TempDir(), "upload.pdf")
		if err := os.WriteFile(path, pdf, 0o644); err != nil {
			t.Fatal(err)
		}

		injection := buildDocumentInjection(path, "text/plain", "upload.pdf", int64(len(pdf)))

		assertNoRawPDFInjection(t, injection)
		if !strings.Contains(injection, "content could not be extracted") {
			t.Fatalf("expected the honest extraction-failure notice, got:\n%s", injection)
		}
	})
}

// makeSVGBytes — copy of the pkg/docextract fixture (test helpers cannot be
// imported across packages); keep in lock-step. One-line SVG source: the
// fallback an image/svg+xml attachment must keep on a text-only model.
func makeSVGBytes(t *testing.T) []byte {
	t.Helper()
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><rect width="1" height="1"/></svg>`)
	if !bytes.HasPrefix(svg, []byte("<svg")) {
		t.Fatal("fixture broken: not SVG source")
	}
	return svg
}

// TestBuildDocumentInjection_SVGNamedPDFKeepsSourceMarkup — round-5 CHECK
// finding N1 at the caller boundary: an SVG attachment that arrives with a
// .pdf FILENAME must still inject the SVG source markup (the image-MIME
// fallback), never the PDF extraction failure notice. Fails today with
// "content could not be extracted: pdf open failed: not a PDF file: invalid
// header" because ExtractBytes lets the .pdf name override the image MIME.
func TestBuildDocumentInjection_SVGNamedPDFKeepsSourceMarkup(t *testing.T) {
	const svgSource = `<svg xmlns="http://www.w3.org/2000/svg"><rect width="1" height="1"/></svg>`

	t.Run("image/svg+xml with a .pdf name keeps the markup", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "diagram.pdf")
		if err := os.WriteFile(path, makeSVGBytes(t), 0o644); err != nil {
			t.Fatal(err)
		}
		injection := buildDocumentInjection(path, "image/svg+xml", "diagram.pdf", int64(len(svgSource)))
		if strings.Contains(injection, "content could not be extracted") {
			t.Fatalf("SVG source fallback lost behind the .pdf name: %s", injection)
		}
		if !strings.Contains(injection, svgSource) {
			t.Fatalf("expected the exact SVG source in the injection, got:\n%s", injection)
		}
	})

	t.Run("the same bytes under a .svg name stay the control", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "diagram.svg")
		if err := os.WriteFile(path, makeSVGBytes(t), 0o644); err != nil {
			t.Fatal(err)
		}
		injection := buildDocumentInjection(path, "image/svg+xml", "diagram.svg", int64(len(svgSource)))
		if strings.Contains(injection, "content could not be extracted") {
			t.Fatalf("SVG fallback broken for its own extension: %s", injection)
		}
		if !strings.Contains(injection, svgSource) {
			t.Fatalf("expected the exact SVG source in the injection, got:\n%s", injection)
		}
	})
}
