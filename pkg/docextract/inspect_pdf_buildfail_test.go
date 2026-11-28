package docextract

// BUILD-FAIL FILE — spec #1968 (read-file native PDF, FR-6). The tests here
// need symbols that do not exist on the base tree, so they live in their own
// file: while the symbols are missing, ONLY this file fails to build and the
// package's other tests still compile and run.
//
// Expected new symbols (spec §5.2 FR-6; declared exactly as the tests use
// them):
//
//	// InspectPDF parses a PDF's structure without rendering: page count
//	// (0 = unknown, e.g. the page tree carries no /Count) and the
//	// extractable text. It must NOT classify failures by reason string —
//	// the typed errors below are the contract.
//	func InspectPDF(data []byte) (PDFInfo, error)
//
//	type PDFInfo struct {
//		Pages int   // 0 = unknown
//		Text  string
//	}
//
//	var ErrPDFEncrypted           = errors.New(...) // password/owner-protected document
//	var ErrPDFUnsupportedVersion  = errors.New(...) // %PDF- header this build does not read
//	var ErrPDFMalformed           = errors.New(...) // anything structurally broken
//
// Oracles: spec §6 row T6b ("encrypted, %PDF-2.0, malformed, /Count missing →
// exact typed error / Pages 0") and FR-6 ("no reason-string matching"). The
// fixtures are built in-test and never committed (§6.1).

import (
	"bytes"
	"compress/zlib"
	"errors"
	"fmt"
	"testing"
)

// --- in-test PDF fixture builders -----------------------------------------
//
// pdfObj is one numbered object body; assemblePDF emits the complete file
// with a byte-accurate xref table (one entry per object number 0..max, free
// entries in the gaps — a real xref layout).

type pdfObj struct {
	num    int
	body   string
	offset int
}

const fixtureCatalogNum = 888
const fixturePagesNum = 999

// assemblePDF lays out header + objects + xref/trailer/startxref and returns
// the complete PDF bytes. trailerExtra is appended to the trailer dictionary
// (e.g. /Encrypt + /ID).
func assemblePDF(header string, objs []pdfObj, trailerExtra string) []byte {
	highest := fixturePagesNum
	byNum := make(map[int]*pdfObj, len(objs))
	for i := range objs {
		byNum[objs[i].num] = &objs[i]
		if objs[i].num > highest {
			highest = objs[i].num
		}
	}

	var buf bytes.Buffer
	offset := 0
	write := func(s string) {
		offset += len(s)
		buf.WriteString(s)
	}
	write(header + "\n")
	for i := range objs {
		objs[i].offset = offset
		write(fmt.Sprintf("%d 0 obj\n%s\nendobj\n", objs[i].num, objs[i].body))
	}
	xrefStart := offset
	write(fmt.Sprintf("xref\n0 %d\n", highest+1))
	write("0000000000 65535 f \n")
	for n := 1; n <= highest; n++ {
		if o, ok := byNum[n]; ok {
			write(fmt.Sprintf("%010d 00000 n \n", o.offset))
		} else {
			write("0000000000 65535 f \n")
		}
	}
	trailer := fmt.Sprintf("/Size %d /Root %d 0 R", highest+1, fixtureCatalogNum)
	if trailerExtra != "" {
		trailer += " " + trailerExtra
	}
	write(fmt.Sprintf("trailer\n<< %s >>\nstartxref\n%d\n%%%%EOF\n", trailer, xrefStart))
	return buf.Bytes()
}

// fixtureCatalogObject is the /Type /Catalog object every builder's trailer
// points at; the pages tree is always numbered fixturePagesNum.
func fixtureCatalogObject() pdfObj {
	return pdfObj{num: fixtureCatalogNum, body: fmt.Sprintf(`<< /Type /Catalog /Pages %d 0 R >>`, fixturePagesNum)}
}

// buildTextPDF builds a valid N-page PDF whose page i draws "PAGE i text" as
// ASCII text operators (uncompressed content stream). version is the header
// version string (e.g. "1.4", "2.0"). withCount controls whether the pages
// tree carries a /Count entry.
func buildTextPDF(t *testing.T, version string, pages int, withCount bool, contentFor func(page int) string) []byte {
	t.Helper()
	if contentFor == nil {
		contentFor = func(page int) string {
			return fmt.Sprintf("BT /F1 12 Tf 72 720 Td (PAGE %d text) Tj ET", page+1)
		}
	}

	// Font shared by all pages.
	objs := []pdfObj{{num: 1, body: `<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>`}}

	// Content streams, one per page.
	contentIDs := make([]int, pages)
	next := 2
	for i := 0; i < pages; i++ {
		contentIDs[i] = next
		next++
		objs = append(objs, pdfObj{num: contentIDs[i], body: fmt.Sprintf(`<< /Length %d >>`+"\n"+
			"stream\n%s\nendstream", len(contentFor(i)), contentFor(i))})
	}

	// Page objects.
	pageIDs := make([]int, pages)
	for i := 0; i < pages; i++ {
		pageIDs[i] = next
		next++
		objs = append(objs, pdfObj{num: pageIDs[i], body: fmt.Sprintf(
			`<< /Type /Page /Parent %d 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 1 0 R >> >> /Contents %d 0 R >>`,
			fixturePagesNum, contentIDs[i])})
	}

	// Pages tree.
	countDict := ""
	if withCount {
		countDict = fmt.Sprintf("/Count %d ", pages)
	}
	kids := ""
	for i, id := range pageIDs {
		if i > 0 {
			kids += " "
		}
		kids += fmt.Sprintf("%d 0 R", id)
	}
	objs = append(objs, pdfObj{num: fixturePagesNum, body: fmt.Sprintf(
		`<< %s/Type /Pages /Kids [%s] >>`, countDict, kids)})
	objs = append(objs, fixtureCatalogObject())

	return assemblePDF(fmt.Sprintf("%%PDF-%s", version), objs, "")
}

// buildEncryptedPDF builds a structurally valid PDF carrying an /Encrypt
// dictionary (standard security handler, empty user password, V1 R2 40-bit —
// the keys are fixture filler; the reader must detect the ENCRYPTION, not
// decrypt).
func buildEncryptedPDF(t *testing.T) []byte {
	t.Helper()
	objs := []pdfObj{
		{num: 1, body: `<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>`},
		{num: 2, body: "<< /Length 44 >>\nstream\nBT /F1 12 Tf 72 720 Td (secret) Tj ET\nendstream"},
		{num: 3, body: fmt.Sprintf(
			`<< /Type /Page /Parent %d 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 1 0 R >> >> /Contents 2 0 R >>`,
			fixturePagesNum)},
		{num: fixturePagesNum, body: `<< /Count 1 /Type /Pages /Kids [3 0 R] >>`},
		// The encryption dictionary: standard security handler, R2/V1.
		{num: 998, body: `<< /Filter /Standard /V 1 /R 2 /O (01234567890123456789012345678901) /U (01234567890123456789012345678901) /P -1 >>`},
		{num: fixtureCatalogNum, body: fmt.Sprintf(`<< /Type /Catalog /Pages %d 0 R >>`, fixturePagesNum)},
	}
	return assemblePDF("%PDF-1.4", objs,
		`/Encrypt 998 0 R /ID [<0123456789ABCDEF0123456789ABCDEF> <0123456789ABCDEF0123456789ABCDEF>]`)
}

// buildCompressedPDF builds a one-page PDF whose content stream is
// zlib-compressed (FlateDecode) — the realistic real-world shape.
func buildCompressedPDF(t *testing.T, text string) []byte {
	t.Helper()
	var z bytes.Buffer
	w := zlib.NewWriter(&z)
	fmt.Fprintf(w, "BT /F1 12 Tf 72 720 Td (%s) Tj ET", text)
	if err := w.Close(); err != nil {
		t.Fatalf("compress content: %v", err)
	}
	content := z.Bytes()
	objs := []pdfObj{
		{num: 1, body: `<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>`},
		{num: 2, body: fmt.Sprintf(`<< /Length %d /Filter /FlateDecode >>`+"\nstream\n%s\nendstream", len(content), content)},
		{num: 3, body: fmt.Sprintf(
			`<< /Type /Page /Parent %d 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 1 0 R >> >> /Contents 2 0 R >>`,
			fixturePagesNum)},
		{num: fixturePagesNum, body: `<< /Count 1 /Type /Pages /Kids [3 0 R] >>`},
		{num: fixtureCatalogNum, body: fmt.Sprintf(`<< /Type /Catalog /Pages %d 0 R >>`, fixturePagesNum)},
	}
	return assemblePDF("%PDF-1.4", objs, "")
}

// --- T6b -------------------------------------------------------------------

// TestInspectPDF_TypedErrors (T6b, B) — InspectPDF classifies every failure
// with its typed error and reports Pages 0 when the page tree carries no
// /Count. FR-6: no reason-string matching anywhere in the caller.
func TestInspectPDF_TypedErrors(t *testing.T) {
	t.Run("encrypted PDF is ErrPDFEncrypted", func(t *testing.T) {
		_, err := InspectPDF(buildEncryptedPDF(t))
		if !errors.Is(err, ErrPDFEncrypted) {
			t.Fatalf("encrypted PDF: got %v, want ErrPDFEncrypted", err)
		}
	})

	t.Run("PDF 2.0 header is ErrPDFUnsupportedVersion", func(t *testing.T) {
		data := buildTextPDF(t, "2.0", 1, true, nil)
		if !bytes.HasPrefix(data, []byte("%PDF-2.0")) {
			t.Fatalf("fixture header broken: %q", data[:9])
		}
		_, err := InspectPDF(data)
		if !errors.Is(err, ErrPDFUnsupportedVersion) {
			t.Fatalf("%%PDF-2.0 document: got %v, want ErrPDFUnsupportedVersion", err)
		}
	})

	t.Run("malformed bytes are ErrPDFMalformed", func(t *testing.T) {
		// Starts with the magic but the structure is broken: the xref
		// offset points past the end of the file.
		data := buildTextPDF(t, "1.4", 1, true, nil)
		broken := append([]byte(nil), data[:len(data)-10]...)
		broken = append(broken, "%%EOF\n"...)
		_, err := InspectPDF(broken)
		if !errors.Is(err, ErrPDFMalformed) {
			t.Fatalf("truncated document: got %v, want ErrPDFMalformed", err)
		}

		// Garbage with no structure at all is malformed too.
		if _, err := InspectPDF([]byte("this is not a pdf at all, just noise bytes")); !errors.Is(err, ErrPDFMalformed) {
			t.Fatalf("garbage bytes: got %v, want ErrPDFMalformed", err)
		}
	})

	t.Run("missing /Count reports Pages 0 with the text", func(t *testing.T) {
		// The page tree exists and the page content draws text, but the
		// pages tree omits /Count: the page number is UNKNOWN (0), and the
		// document is otherwise readable.
		data := buildTextPDF(t, "1.4", 1, false, func(int) string {
			return "BT /F1 12 Tf 72 720 Td (countless) Tj ET"
		})
		info, err := InspectPDF(data)
		if err != nil {
			t.Fatalf("a readable PDF without /Count must not error, got: %v", err)
		}
		if info.Pages != 0 {
			t.Fatalf("Pages = %d, want 0 (unknown without /Count)", info.Pages)
		}
		if info.Text != "countless" {
			t.Fatalf("Text = %q, want %q", info.Text, "countless")
		}
	})

	t.Run("well-formed pages report their count and text", func(t *testing.T) {
		data := buildCompressedPDF(t, "compressed hello")
		info, err := InspectPDF(data)
		if err != nil {
			t.Fatalf("valid one-page PDF: %v", err)
		}
		if info.Pages != 1 {
			t.Fatalf("Pages = %d, want 1", info.Pages)
		}
		if info.Text != "compressed hello" {
			t.Fatalf("Text = %q, want %q", info.Text, "compressed hello")
		}
	})
}
